// Observational production-loopback startup sample; not a cold-start benchmark.
// Example: node scripts/measure-production-startup.mjs --origin http://127.0.0.1:3012 --path /prefetch --trials 10 --binary /path/server --out report.json --counter 'Prefetch companion'
import { chromium } from '@playwright/test';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { mkdir, readFile, realpath, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { cpus, platform, arch, release } from 'node:os';
import { gzipSync } from 'node:zlib';

const usage = 'Usage: node scripts/measure-production-startup.mjs --origin http://127.0.0.1:PORT --path /prefetch --trials 10 --out report.json [--binary /path/server] [--chromium /path/chromium] [--counter "Prefetch companion"]';
if (process.argv.includes('--help')) { console.log(usage); process.exit(0); }
const options = {}, allowed = new Set(['origin', 'path', 'trials', 'out', 'binary', 'chromium', 'counter']);
for (let i = 2; i < process.argv.length; i += 2) {
  const name = process.argv[i].slice(2), value = process.argv[i + 1];
  if (!process.argv[i].startsWith('--') || !allowed.has(name) || value === undefined || Object.hasOwn(options, name)) throw new Error(usage);
  options[name] = value;
}
if (!options.origin || !options.out) throw new Error(usage);
const origin = new URL(options.origin), path = options.path || '/prefetch', trials = Number(options.trials || 10);
if (!['http:', 'https:'].includes(origin.protocol) || !['127.0.0.1', 'localhost', '[::1]'].includes(origin.hostname) || origin.username || origin.password || origin.pathname !== '/' || origin.search || origin.hash) throw new Error('This observation supports a loopback HTTP(S) origin only.');
if (!path.startsWith('/') || path.startsWith('//') || new URL(path, origin).origin !== origin.origin || new URL(path, origin).hash || !Number.isInteger(trials) || trials < 1 || trials > 100) throw new Error('Invalid application path or trial count (1..100).');
const target = new URL(path, origin).href, hash = bytes => createHash('sha256').update(bytes).digest('hex');
const commandOutput = (command, args) => execFileSync(command, args, { encoding: 'utf8' }).trim();
const artifact = options.binary ? { serverPath: await realpath(options.binary), serverSHA256: hash(await readFile(options.binary)), baseCommit: commandOutput('git', ['rev-parse', 'HEAD']), goBuildInfo: commandOutput('go', ['version', '-m', resolve(options.binary)]) } : undefined;
const warmup = await fetch(target, { redirect: 'error', signal: AbortSignal.timeout(30_000) });
if (warmup.status !== 200 || !warmup.headers.get('content-type')?.includes('text/html')) throw new Error('Warmup did not return a successful HTML document.');
const warmupHTML = await warmup.text();
if (!/<h1\b[^>]*>\s*[^<\s]/i.test(warmupHTML) || !warmupHTML.includes('data-nextgo-island=')) throw new Error('Warmup lacks a meaningful server-rendered heading and island.');

const samples = [], gzipSizes = new Map();
const browser = await chromium.launch({ headless: true, executablePath: options.chromium || process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH });
try {
  for (let index = 0; index < trials; index++) {
    const context = await browser.newContext();
    try {
      const page = await context.newPage(), errors = [], assetBodies = [];
      page.on('pageerror', error => errors.push(error.message));
      page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
      page.on('response', response => {
        if (!new URL(response.url()).pathname.endsWith('.wasm')) return;
        // Start reading the actual browser response immediately. Never silently
        // replace unavailable debugger bytes with another request/artifact.
        const body = response.body().then(bytes => ({ response, bytes }), error => ({ error: String(error) }));
        assetBodies.push(body);
      });
      await page.addInitScript(() => {
        const observation = window.__nextgoStartup = { readyAtMS: null, ssr: null, instantiations: [], memories: [], memoryAtReady: [] };
        const originalInstantiate = WebAssembly.instantiate, originalStreaming = WebAssembly.instantiateStreaming;
        const record = (kind, startedAtMS, value) => {
          const instance = value instanceof WebAssembly.Instance ? value : value.instance;
          const memories = Object.values(instance.exports).filter(item => item instanceof WebAssembly.Memory);
          for (const memory of memories) if (!observation.memories.includes(memory)) observation.memories.push(memory);
          observation.instantiations.push({ kind, startedAtMS, resolvedAtMS: performance.now(), linearMemoryBytes: memories.map(memory => memory.buffer.byteLength) });
          return value;
        };
        WebAssembly.instantiate = function (...args) {
          const started = performance.now();
          return Reflect.apply(originalInstantiate, this, args).then(value => record('instantiate', started, value));
        };
        if (originalStreaming) WebAssembly.instantiateStreaming = function (...args) {
          const started = performance.now();
          return Reflect.apply(originalStreaming, this, args).then(value => record('instantiateStreaming', started, value));
        };
        const observer = new MutationObserver(() => {
          if (!observation.ssr && document.getElementById('nextgo-initial-ready')) {
            const root = document.getElementById('nextgo-app') || document.body;
            observation.ssr = { atMS: performance.now(), heading: document.querySelector('h1')?.textContent.trim() || '', textCharacters: root.textContent.trim().length, islands: root.querySelectorAll('[data-nextgo-island]').length, disabledButtons: root.querySelectorAll('button[disabled]').length };
          }
          if (document.body?.getAttribute('data-nextgo-hydration') === 'ready' && observation.readyAtMS === null) {
            observation.readyAtMS = performance.now(); observation.memoryAtReady = observation.memories.map(memory => memory.buffer.byteLength); observer.disconnect();
          }
        });
        observer.observe(document, { childList: true, subtree: true, attributes: true, attributeFilter: ['data-nextgo-hydration'] });
      });
      const started = performance.now();
      const response = await page.goto(target, { waitUntil: 'commit', timeout: 30_000 }); const commitObserved = performance.now();
      if (response.status() !== 200 || page.url() !== target) throw new Error(`Trial ${index + 1}: unexpected document status or redirect.`);
      await page.waitForFunction(() => Number.isFinite(window.__nextgoStartup?.readyAtMS), null, { timeout: 30_000, polling: 10 }); const finished = performance.now();
      const observed = await page.evaluate(() => {
        const value = window.__nextgoStartup, timing = performance.getEntriesByType('navigation')[0];
        return { readyAtMS: value.readyAtMS, ssr: value.ssr, instantiations: value.instantiations, linearMemoryBytes: value.memoryAtReady, navigation: { startTimeMS: timing.startTime, responseStartMS: timing.responseStart, responseEndMS: timing.responseEnd }, resources: performance.getEntriesByType('resource').filter(item => new URL(item.name).pathname.endsWith('.wasm')).map(item => ({ url: item.name, encodedBodyBytes: item.encodedBodySize, decodedBodyBytes: item.decodedBodySize })) };
      });
      if (!observed.ssr?.heading || observed.ssr.textCharacters < 40 || observed.ssr.islands < 1 || observed.ssr.disabledButtons < 1 || !observed.linearMemoryBytes.length || !observed.instantiations.length) throw new Error(`Trial ${index + 1}: missing meaningful SSR or returned WASM memory evidence.`);
      const assets = [];
      for (const pending of assetBodies) {
        const result = await pending; if (result.error) throw new Error(`Trial ${index + 1}: actual WASM body unavailable: ${result.error}`);
        const { response: asset, bytes } = result;
        if (asset.status() !== 200 || !asset.headers()['content-type']?.includes('application/wasm') || bytes.length < 8 || bytes.subarray(0, 4).toString('hex') !== '0061736d') throw new Error(`Trial ${index + 1}: invalid WASM asset response.`);
        const sha256 = hash(bytes); if (!gzipSizes.has(sha256)) gzipSizes.set(sha256, gzipSync(bytes, { level: 9 }).length);
        assets.push({ url: asset.url(), file: new URL(asset.url()).pathname.split('/').pop(), sha256, raw: bytes.length, gzip9: gzipSizes.get(sha256), contentEncoding: asset.headers()['content-encoding'] || 'identity', bodySource: 'browser-response', resourceTiming: observed.resources.find(item => item.url === asset.url()) });
      }
      if (!assets.some(asset => asset.file === 'app.wasm')) throw new Error(`Trial ${index + 1}: the actual core WASM response was not recorded.`);
      if (samples.length && assets.find(asset => asset.file === 'app.wasm').sha256 !== samples[0].assets.find(asset => asset.file === 'app.wasm').sha256) throw new Error('Core WASM changed during observation.');
      let interaction;
      if (options.counter) {
        const region = page.getByRole('region', { name: options.counter, exact: true }), value = region.locator('[data-count]'); const before = Number(await value.textContent());
        if (!Number.isSafeInteger(before)) throw new Error('Counter did not contain an integer.');
        await region.getByRole('button', { name: `Increase ${options.counter}`, exact: true }).click();
        await page.waitForFunction(({ label, expected }) => [...document.querySelectorAll('[aria-label]')].find(node => node.getAttribute('aria-label') === label)?.querySelector('[data-count]')?.textContent === String(expected), { label: options.counter, expected: before + 1 }, { timeout: 5_000 });
        interaction = { label: options.counter, before, after: Number(await value.textContent()), measuredAfterReadiness: true };
      }
      if (errors.length) throw new Error(`Trial ${index + 1}: browser errors: ${errors.join('; ')}`);
      samples.push({ trial: index + 1, elapsedMS: finished - started, commitToReadyElapsedMS: finished - commitObserved, browserNavigationToReadyMS: observed.readyAtMS - observed.navigation.startTimeMS, ...observed, assets, interaction, errors });
      console.error(`Production-loopback trial ${index + 1}/${trials}: browser ready ${observed.readyAtMS.toFixed(1)}ms; ${assets[0].raw} raw WASM bytes.`);
    } finally { await context.close(); }
  }
  if (artifact && artifact.serverSHA256 !== hash(await readFile(artifact.serverPath))) throw new Error('Native binary changed during observation.');
  const median = values => { const sorted = [...values].sort((a, b) => a - b), middle = Math.floor(sorted.length / 2); return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2; };
  const report = {
    date: new Date().toISOString(), origin: origin.origin, path, artifact,
    environment: { go: artifact?.goBuildInfo.split('\n')[0] || commandOutput('go', ['version']), browser: browser.version(), node: process.version, platform: platform(), arch: arch(), release: release(), cpu: cpus()[0]?.model },
    scope: `${trials} fresh Chromium contexts in one browser process, standalone production executable on loopback; warm OS/server with an HTML warmup, default browser caching and automatic visible-link work, no CPU/network throttling; browser process may reuse compiled code. Observational wrappers/body capture add instrumentation work. Not cold-field startup, live Go heap, peak memory, physical commitment, RSS or a competing-framework comparison.`,
    method: { readiness: 'MutationObserver timestamp of body data-nextgo-hydration=ready, relative to browser navigation performance time origin. elapsedMS is Node wall time from goto(waitUntil=commit) call through observed readiness; commitToReadyElapsedMS starts when that goto promise returns and includes automation/poll overhead.', memory: 'Actual exported WebAssembly.Memory objects from resolved native instantiate/instantiateStreaming calls, invoked with Reflect.apply preserving receiver/arguments/return value. Capacities recorded at instantiation and readiness; references retained until context close.', bytes: 'Decoded body bytes from each actual browser WASM response; SHA-256 and raw length over those bytes. gzip9 is Node zlib level9 recompression, not necessarily the HTTP encoding size. Resource Timing separately reports browser encoded/decoded body sizes. No artifact-refetch fallback.', ssr: 'Meaningful heading, text, islands and disabled controls observed when the initial-ready marker parses, before hydration.', interaction: options.counter ? 'Real Go counter clicked only after startup timing is recorded.' : 'Not requested.' },
    warmup: { status: warmup.status, htmlBytes: Buffer.byteLength(warmupHTML), htmlSHA256: hash(warmupHTML) }, samples,
    medianReadyMS: median(samples.map(sample => sample.elapsedMS)), medianBrowserNavigationToReadyMS: median(samples.map(sample => sample.browserNavigationToReadyMS)), medianCommitToReadyElapsedMS: median(samples.map(sample => sample.commitToReadyElapsedMS)),
    referenceBudgets: { coreRawBytes: 6 * 1024 * 1024, coreGzipBytes: 1677721, result: samples.every(sample => sample.assets.find(asset => asset.file === 'app.wasm').raw <= 6 * 1024 * 1024 && sample.assets.find(asset => asset.file === 'app.wasm').gzip9 <= 1677721) ? 'PASS for recorded reference sizes only; broader release performance gate remains open' : 'FAIL; release performance gate remains open' },
  };
  const output = resolve(options.out); await mkdir(dirname(output), { recursive: true }); await writeFile(output, JSON.stringify(report, null, 2) + '\n'); console.log(output);
} finally { await browser.close(); }
