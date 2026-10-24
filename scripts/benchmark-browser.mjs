// Compare two existing WASM builds in fresh headless Chromium processes.
// This harness measures one identical SSR island, not a competing framework.
import { createServer } from 'node:http';
import { readFile, writeFile } from 'node:fs/promises';
import { gzipSync } from 'node:zlib';
import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { cpus, platform, release } from 'node:os';
import { join, resolve } from 'node:path';
import { chromium } from 'playwright';

const options = {};
for (let index = 2; index < process.argv.length; index += 2) {
  const key = process.argv[index];
  if (!key.startsWith('--') || process.argv[index + 1] === undefined) throw new Error('Arguments must be --name value pairs');
  options[key.slice(2)] = process.argv[index + 1];
}
if (!options.baseline || !options.candidate) throw new Error('Usage: node scripts/benchmark-browser.mjs --baseline /path/base.wasm --candidate /path/candidate.wasm [--out /path/results.json] [--chromium /path/chrome] [--trials 6]');
const trials = Number(options.trials || 6);
const cpuRate = Number(options.cpu || 4);
const latency = Number(options.latency || 40);
const throughput = Number(options.mbps || 10) * 1_000_000 / 8;
if (!Number.isInteger(trials) || trials < 1 || cpuRate < 1 || latency < 0 || throughput <= 0) throw new Error('Invalid benchmark settings');
const goRoot = execFileSync('go', ['env', 'GOROOT'], { encoding: 'utf8' }).trim();
const runtime = await readFile(options.runtime || join(goRoot, 'lib/wasm/wasm_exec.js'));
const variants = {};
for (const name of ['baseline', 'candidate']) {
  const bytes = await readFile(resolve(options[name]));
  const gzip = gzipSync(bytes, { level: 9 });
  variants[name] = { gzip, metadata: { path: resolve(options[name]), sha256: createHash('sha256').update(bytes).digest('hex'), rawBytes: bytes.length, gzipBytes: gzip.length } };
}
const html = `<!doctype html><html><head><meta name="description" content="benchmark"><script src="/wasm_exec.js"></script></head><body data-nextgo-build="bench"><div id="nextgo-app"><section data-nextgo-island="counter" data-nextgo-props='{"initial":3,"label":"benchmark"}'><output data-count>3</output><button data-increment disabled>+</button><button data-decrement disabled>-</button><button data-reset disabled>Reset</button></section></div><div id="nextgo-status" hidden></div><script>
window.timings = {};
new MutationObserver(() => {
  if (document.body.dataset.nextgoHydration === 'ready') window.timings.ready = performance.now();
}).observe(document.body, { attributes: true, attributeFilter: ['data-nextgo-hydration'] });
(async () => {
  let start = performance.now();
  const response = await fetch('/' + new URLSearchParams(location.search).get('variant') + '.wasm');
  const bytes = await response.arrayBuffer();
  timings.download = performance.now() - start;
  start = performance.now();
  const go = new Go();
  const result = await WebAssembly.instantiate(bytes, go.importObject);
  timings.instantiate = performance.now() - start;
  start = performance.now();
  go.run(result.instance);
  timings.run = performance.now() - start;
})().catch(error => { window.benchmarkError = String(error); });
</script></body></html>`;
const server = createServer((request, response) => {
  response.setHeader('Cache-Control', 'no-store');
  const path = new URL(request.url, 'http://localhost').pathname;
  if (path === '/wasm_exec.js') {
    response.setHeader('Content-Type', 'application/javascript');
    response.end(runtime);
  } else if (path === '/baseline.wasm' || path === '/candidate.wasm') {
    const variant = variants[path.slice(1, -5)];
    response.setHeader('Content-Type', 'application/wasm');
    response.setHeader('Content-Encoding', 'gzip');
    response.setHeader('Content-Length', variant.gzip.length);
    response.end(variant.gzip);
  } else {
    response.setHeader('Content-Type', 'text/html');
    response.end(html);
  }
});
await new Promise((accept, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', accept); });
const url = `http://127.0.0.1:${server.address().port}`;
const samples = [];
let browserVersion;
try {
  for (let trial = 0; trial < trials; trial++) {
    for (const variant of trial % 2 ? ['candidate', 'baseline'] : ['baseline', 'candidate']) {
      const browser = await chromium.launch({ headless: true, ...(options.chromium ? { executablePath: options.chromium } : {}) });
      try {
        browserVersion ||= browser.version();
        const context = await browser.newContext();
        const page = await context.newPage();
        const errors = [];
        page.on('pageerror', error => errors.push(error.message));
        const cdp = await context.newCDPSession(page);
        await cdp.send('Network.enable');
        await cdp.send('Network.setCacheDisabled', { cacheDisabled: true });
        await cdp.send('Network.emulateNetworkConditions', { offline: false, latency, downloadThroughput: throughput, uploadThroughput: throughput });
        await cdp.send('Emulation.setCPUThrottlingRate', { rate: cpuRate });
        await page.goto(url + '/?variant=' + variant);
        await page.waitForFunction(() => window.timings?.ready || window.benchmarkError, null, { timeout: 60_000 });
        const result = await page.evaluate(() => ({ ...window.timings, error: window.benchmarkError }));
        if (result.error || errors.length) throw new Error(JSON.stringify({ result, errors }));
        samples.push({ trial, variant, ...result });
        console.log(JSON.stringify(samples.at(-1)));
      } finally {
        await browser.close();
      }
    }
  }
} finally {
  await new Promise(accept => server.close(accept));
}
const median = values => {
  const sorted = values.toSorted((a, b) => a - b);
  const middle = Math.floor(sorted.length / 2);
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2;
};
const report = {
  measuredAt: new Date().toISOString(),
  environment: { node: process.version, go: execFileSync('go', ['version'], { encoding: 'utf8' }).trim(), chromium: browserVersion, platform: platform(), osRelease: release(), cpu: cpus()[0]?.model },
  settings: { trials, freshBrowserPerSample: true, cacheDisabled: true, gzipLevel: 9, latencyMS: latency, megabitsPerSecond: throughput * 8 / 1_000_000, cpuThrottle: cpuRate },
  variants: Object.fromEntries(Object.entries(variants).map(([name, value]) => [name, value.metadata])),
  mediansMS: Object.fromEntries(['baseline', 'candidate'].map(variant => [variant, Object.fromEntries(['ready', 'download', 'instantiate', 'run'].map(metric => [metric, median(samples.filter(sample => sample.variant === variant).map(sample => sample[metric]))]))])),
  samples,
};
if (options.out) await writeFile(resolve(options.out), JSON.stringify(report, null, 2) + '\n');
console.log(JSON.stringify(report, null, 2));
