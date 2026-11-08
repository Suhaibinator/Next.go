// Local production comparison only. See docs/FRAMEWORK_COMPARISON.md for scope.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {mkdir, mkdtemp, readFile, rm, stat, writeFile} from 'node:fs/promises';
import {createServer} from 'node:net';
import {cpus, tmpdir} from 'node:os';
import {dirname, join, resolve} from 'node:path';
import {performance} from 'node:perf_hooks';
import {createRequire} from 'node:module';
import {chromium} from '@playwright/test';

const args = new Map(process.argv.slice(2).map(value => {
  const index = value.indexOf('=');
  assert(index > 2 && value.startsWith('--'), `expected --name=value: ${value}`);
  return [value.slice(2, index), value.slice(index + 1)];
}));
for (const key of args.keys()) assert(['go27', 'go24', 'next', 'out', 'samples', 'requests'].includes(key), `unknown option ${key}`);
for (const key of ['go27', 'go24', 'next', 'out']) assert(args.has(key), `missing --${key}`);
const samples = Number(args.get('samples') ?? 12), requests = Number(args.get('requests') ?? 1000);
assert(Number.isInteger(samples) && samples >= 3 && samples <= 100);
assert(Number.isInteger(requests) && requests >= 100 && requests <= 10000);
const out = resolve(args.get('out'));
const require = createRequire(import.meta.url);
await mkdir(out, {recursive: true});
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const percentile = (values, quantile) => [...values].sort((a, b) => a - b)[Math.ceil(quantile * values.length) - 1];
const distribution = values => ({count: values.length, min: Math.min(...values), median: percentile(values, .5), p95: percentile(values, .95), p99: percentile(values, .99), max: Math.max(...values)});
async function address() {
  const server = createServer();
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}
async function stats(pid) {
  const status = await readFile(`/proc/${pid}/status`, 'utf8');
  const record = await readFile(`/proc/${pid}/stat`, 'utf8');
  const fields = record.slice(record.lastIndexOf(')') + 1).trim().split(/\s+/);
  const kb = name => Number(new RegExp(`^${name}:\\s+(\\d+) kB$`, 'm').exec(status)?.[1]);
  return {rssBytes: kb('VmRSS') * 1024, highWaterRSSBytes: kb('VmHWM') * 1024, userTicks: Number(fields[11]), systemTicks: Number(fields[12])};
}
async function start(name, executable, next = false) {
  const port = await address(), cwd = next ? dirname(executable) : await mkdtemp(join(tmpdir(), 'nextgo-comparison-'));
  const logs = [], began = performance.now();
  const child = spawn(next ? process.execPath : executable, next ? [executable] : ['-addr', `127.0.0.1:${port}`, '-env-dir', cwd], {
    cwd, stdio: ['ignore', 'pipe', 'pipe'],
    env: {PATH: '/usr/bin:/bin', GOMAXPROCS: '2', NODE_ENV: 'production', NEXT_TELEMETRY_DISABLED: '1', PORT: String(port), HOSTNAME: '127.0.0.1'},
  });
  child.stdout.on('data', chunk => logs.push(chunk));
  child.stderr.on('data', chunk => logs.push(chunk));
  let result;
  const done = new Promise(resolve => {
    child.once('error', error => { result = {error: error.message}; resolve(result); });
    child.once('exit', (code, signal) => { result = {code, signal}; resolve(result); });
  });
  const origin = `http://127.0.0.1:${port}`;
  let stopped = false;
  const stop = async () => {
    if (stopped) return;
    stopped = true;
    if (!result) child.kill('SIGTERM');
    const deadline = performance.now() + 12000;
    while (!result && performance.now() < deadline) await sleep(20);
    const forced = !result;
    if (forced) child.kill('SIGKILL');
    await done;
    await writeFile(join(out, `${name}.log`), Buffer.concat(logs));
    if (!next) await rm(cwd, {recursive: true, force: true});
    assert(!forced, `${name} required forced shutdown`);
    assert(result.code === 0 || result.signal === 'SIGTERM' || next && result.code === 143, `${name} exited unexpectedly: ${JSON.stringify(result)}`);
    let reachable = false;
    try { await fetch(origin, {signal: AbortSignal.timeout(300)}); reachable = true; } catch {}
    assert(!reachable, `${name} listener remains`);
    return {exit: result, forced, listenerClosed: true};
  };
  try {
    for (;;) {
      assert(!result, `${name} exited during startup: ${JSON.stringify(result)}`);
      try {
        const response = await fetch(origin, {signal: AbortSignal.timeout(500)});
        if (response.status === 200 && (await response.text()).includes('data-comparison-page="home"')) break;
      } catch {}
      assert(performance.now() - began < 30000, `${name} startup timeout`);
      await sleep(20);
    }
    return {name, origin, stop, pid: child.pid, startupToFirstSSRMs: performance.now() - began, readyStats: await stats(child.pid)};
  } catch (error) { await stop(); throw error; }
}

async function browserSample(server, index, javascript = true) {
  // Chromium creates Unix sockets below TMPDIR; long build-workspace paths can
  // exceed the kernel socket-name bound. Playwright owns/cleans these files.
  const browser = await chromium.launch({headless: true, executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined, env: {...process.env, TMPDIR: '/tmp'}});
  try {
    report.environment.chromium ??= browser.version();
    assert.equal(browser.version(), report.environment.chromium);
    const context = await browser.newContext({javaScriptEnabled: javascript, viewport: {width: 1280, height: 800}, reducedMotion: 'reduce'});
    if (javascript) await context.addInitScript(() => {
      window.__comparisonDocument = crypto.randomUUID();
      window.__comparisonPaint = {lcpMs: null, cls: 0};
      const observer = new MutationObserver(() => {
        const output = document.querySelector('[data-count]');
        if (output) { window.__comparisonSSR = output; observer.disconnect(); }
      });
      observer.observe(document, {subtree: true, childList: true});
      new PerformanceObserver(list => {
        for (const entry of list.getEntries()) window.__comparisonPaint.lcpMs = entry.startTime;
      }).observe({type: 'largest-contentful-paint', buffered: true});
      new PerformanceObserver(list => {
        for (const entry of list.getEntries()) if (!entry.hadRecentInput) window.__comparisonPaint.cls += entry.value;
      }).observe({type: 'layout-shift', buffered: true});
    });
    const page = await context.newPage(), errors = [], consoleErrors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('console', message => { if (message.type() === 'error') consoleErrors.push(message.text()); });
    const began = performance.now();
    const response = await page.goto(server.origin, {waitUntil: 'load'});
    assert.equal(response.status(), 200);
    assert.equal(await page.locator('[data-count]').textContent(), '3');
    if (!javascript) {
      assert(await page.locator('[data-increment]').isDisabled());
      await page.locator('[data-about]').click();
      await page.locator('[data-comparison-page="about"]').waitFor();
      return {index, javascript: false, meaningfulSSR: true, nativeNavigation: true};
    }
    await page.locator('[data-comparison-counter][data-ready="true"]').waitFor();
    assert(await page.locator('[data-increment]').isEnabled());
    const nodeGotoToObservedReadyMs = performance.now() - began;
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const initial = await page.evaluate(() => {
      const navigation = performance.getEntriesByType('navigation')[0];
      return {
        document: window.__comparisonDocument,
        ssrNodePreserved: window.__comparisonSSR === document.querySelector('[data-count]'),
        appReadyMs: performance.getEntriesByName('comparison:ready')[0]?.startTime,
        ttfbMs: navigation.responseStart - navigation.requestStart,
        fcpMs: performance.getEntriesByName('first-contentful-paint')[0]?.startTime,
        ...window.__comparisonPaint,
        resources: [navigation, ...performance.getEntriesByType('resource')].map(entry => ({
          path: new URL(entry.name).pathname, initiatorType: entry.entryType === 'navigation' ? 'document' : entry.initiatorType,
          encodedBodyBytes: entry.encodedBodySize, decodedBodyBytes: entry.decodedBodySize, transferBytes: entry.transferSize,
        })),
      };
    });
    assert(initial.ssrNodePreserved, `${server.name} replaced SSR counter node`);
    assert(Number.isFinite(initial.appReadyMs));
    await page.locator('[data-increment]').click();
    await page.waitForFunction(() => document.querySelector('[data-count]').textContent === '4');
    const navigationStart = performance.now();
    await page.locator('[data-about]').click();
    await page.locator('[data-comparison-page="about"]').waitFor();
    const nodeClickToObservedNavigationMs = performance.now() - navigationStart;
    assert.equal(await page.evaluate(() => window.__comparisonDocument), initial.document, `${server.name} performed full-document navigation`);
    assert.deepEqual(errors, []);
    assert.deepEqual(consoleErrors, []);
    delete initial.document;
    return {index, javascript: true, ...initial, nodeGotoToObservedReadyMs, nodeClickToObservedNavigationMs, counterInteraction: '3 -> 4', pageErrors: errors, consoleErrors};
  } finally { await browser.close(); }
}

async function load(server, concurrency) {
  for (let n = 0; n < 10; n++) { const response = await fetch(server.origin); await response.arrayBuffer(); }
  let next = 0;
  const timings = [], bytes = [], before = await stats(server.pid), start = performance.now();
  await Promise.all(Array.from({length: concurrency}, async () => {
    for (;;) {
      if (next++ >= requests) return;
      const began = performance.now(), response = await fetch(server.origin, {headers: {'Accept-Encoding': 'identity'}, signal: AbortSignal.timeout(10000)});
      const body = await response.text();
      assert.equal(response.status, 200);
      assert(body.includes('data-comparison-page="home"'));
      assert(!/(?:^|,)\s*public\b/i.test(response.headers.get('cache-control') || ''), 'unexpected public route cache');
      bytes.push(Buffer.byteLength(body)); timings.push(performance.now() - began);
    }
  }));
  const elapsedMs = performance.now() - start, after = await stats(server.pid);
  return {concurrency, requests: timings.length, elapsedMs, completedPerSecond: timings.length / elapsedMs * 1000, responseAndBodyMs: distribution(timings), bodyBytes: distribution(bytes), before, after};
}

async function servedArtifacts(server, variant) {
  const paths = [...new Set(variant.samples.flatMap(sample => sample.resources.map(resource => resource.path)))].sort();
  const dir = join(out, 'served', server.name);
  await mkdir(dir, {recursive: true});
  const artifacts = [];
  for (const path of paths) {
    const url = new URL(path, server.origin);
    assert.equal(url.origin, server.origin);
    const response = await fetch(url, {headers: {'Accept-Encoding': 'gzip'}, signal: AbortSignal.timeout(10000)});
    assert.equal(response.status, 200);
    const chunks = [];
    let size = 0;
    for await (const chunk of response.body) {
      size += chunk.length;
      if (size > 64 * 1024 * 1024) throw new Error('served artifact exceeds 64 MiB');
      chunks.push(chunk);
    }
    const bytes = Buffer.concat(chunks), sha256 = hash(bytes);
    await writeFile(join(dir, sha256 + '.bin'), bytes);
    artifacts.push({path, decodedBytes: bytes.length, decodedSHA256: sha256, contentType: response.headers.get('content-type'), contentEncoding: response.headers.get('content-encoding'), declaredContentLength: response.headers.get('content-length')});
  }
  return artifacts;
}

const servers = [];
const report = {
  format: 'nextgo-local-framework-comparison-v1', recordedAtUTC: new Date().toISOString(),
  environment: {platform: process.platform, arch: process.arch, node: process.version, playwright: require('@playwright/test/package.json').version, cpuModel: cpus()[0].model, logicalCPUs: cpus().length, browserExecutable: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || 'playwright-managed', viewport: '1280x800', network: 'loopback, no throttle', GoGOMAXPROCS: 2},
  controls: {samplesPerVariant: samples, freshBrowserProcessPerSample: true, javascriptWarmupSamples: 0, serverOSCaches: 'warm after startup SSR probe', sampleOrder: 'round-robin, reversed on alternating rounds', routeCache: 'request-time SSR, prefetch disabled', loadRequestsPerConcurrency: requests, loadEncoding: 'identity', credentials: 'none; complete synthetic child environment'},
  variants: [],
  limitations: ['Local synthetic two-route counter workload, not framework-wide performance parity.', 'Shared host, no CPU affinity/cgroup/network throttle; descriptive percentiles only.', 'Browser FCP/LCP/CLS are lab observations, not field Web Vitals, INP or real devices.', 'Readiness marks are application instrumentation, not complete framework hydration.', 'RSS/HWM are server process snapshots, excluding browsers, descendants and machine memory.', 'Load timing includes local client and response consumption; requests/second does not establish saturation.', 'Startup has one process observation per variant, with warm OS/dependency caches.', 'No hosted/serverless/CDN performance or release budget approval.'],
};
let failure;
try {
  assert.equal(process.platform, 'linux', 'this runner uses Linux procfs');
  for (const [dependency, version] of [['next', '16.4.5'], ['react', '19.3.1'], ['react-dom', '19.3.1']]) {
    assert.equal(JSON.parse(await readFile(join(args.get('next'), 'node_modules', dependency, 'package.json'), 'utf8')).version, version);
  }
  for (const [name, file, next] of [['go1.27', args.get('go27'), false], ['go1.24', args.get('go24'), false], ['nextjs16.4.5', join(args.get('next'), 'server.js'), true]]) {
    const executable = resolve(file), info = await stat(executable);
    assert(info.isFile());
    const server = await start(name, executable, next); servers.push(server);
    report.variants.push({name, entrypointSHA256: hash(await readFile(executable)), startupToFirstSSRMs: server.startupToFirstSSRMs, readyStats: server.readyStats, noJS: await browserSample(server, 0, false), samples: [], load: []});
  }
  for (let round = 0; round < samples; round++) {
    const order = round % 2 ? [2, 1, 0] : [0, 1, 2];
    for (const index of order) report.variants[index].samples.push(await browserSample(servers[index], round));
    await writeFile(join(out, 'partial.json'), JSON.stringify(report, null, 2) + '\n');
  }
  for (const concurrency of [1, 8]) for (let index = 0; index < servers.length; index++) report.variants[index].load.push(await load(servers[index], concurrency));
  for (let index = 0; index < servers.length; index++) report.variants[index].servedArtifacts = await servedArtifacts(servers[index], report.variants[index]);
  for (const variant of report.variants) {
    variant.summary = Object.fromEntries(['appReadyMs', 'ttfbMs', 'fcpMs', 'lcpMs', 'cls', 'nodeGotoToObservedReadyMs', 'nodeClickToObservedNavigationMs'].map(key => [key, distribution(variant.samples.map(sample => sample[key]).filter(Number.isFinite))]));
  }
} catch (error) {
  failure = error;
  report.failure = {message: error.message, stack: error.stack};
} finally {
  const results = await Promise.allSettled(servers.map(server => server.stop()));
  report.cleanup = results.map((result, index) => ({name: servers[index].name, status: result.status, ...(result.status === 'rejected' ? {error: String(result.reason)} : result.value)}));
  await writeFile(join(out, 'result.json'), JSON.stringify(report, null, 2) + '\n');
  for (const result of results) if (result.status === 'rejected' && !failure) failure = result.reason;
}
if (failure) throw failure;
assert.equal(report.variants.length, 3);
console.log(`Compared three variants, ${samples} fresh-browser samples each, with owned server/listener cleanup.`);
