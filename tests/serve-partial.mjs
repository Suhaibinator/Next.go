// Build-time providers are healthy; request-region providers are deliberately
// unavailable during build. Runtime reverses that condition for every replica.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as requestHTTP } from 'node:http';
import { randomBytes } from 'node:crypto';
import { StringDecoder } from 'node:string_decoder';

const output = join(process.env.NEXTGO_E2E_ROOT, 'partial');
await mkdir(output, { recursive: true });
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], { stdio: 'inherit', env: { ...base, NEXTGO_DEMO_PARTIAL_REGION_FAIL: 'true' } });
  if (await new Promise(resolve => build.once('exit', resolve)) !== 0) process.exit(1);
}
const shellTests = process.env.NEXTGO_PARTIAL_SHELL_TEST === 'true';
const controlToken = randomBytes(32).toString('hex');
const addresses = { first: 4164, broken: 4165, second: 4166, ...(shellTests ? { live: 4167, busy: 4168 } : {}) };
const children = [];
const gates = new Map();
const shellWire = new Map();
let holder;
async function releaseHolder() {
  if (!holder) return;
  const previous = holder; holder = undefined;
  previous.request.destroy(); previous.response?.destroy(); await previous.done;
}
async function holdStream(replica) {
  if (holder) throw new Error('stream already held');
  const state = {}; holder = state; state.done = new Promise(resolve => { state.finish = resolve; });
  await new Promise((resolve, reject) => {
    let buffered = '', ready = false;
    const timer = setTimeout(() => { reject(new Error('holder shell unavailable')); state.request.destroy(); }, 5000);
    state.request = requestHTTP({ hostname: '127.0.0.1', port: addresses[replica], path: '/stream?hold=1' }, incoming => {
      state.response = incoming; incoming.on('error', () => {}); incoming.on('close', state.finish);
      incoming.on('data', chunk => { if (ready) return; buffered += chunk.toString('utf8'); if (incoming.statusCode !== 200 || buffered.length > 128 * 1024) { clearTimeout(timer); reject(new Error('invalid holder response')); state.request.destroy(); } else if (buffered.includes('data-stream-loading')) { ready = true; clearTimeout(timer); resolve(); } });
    });
    state.request.on('error', error => { clearTimeout(timer); state.finish(); if (!ready) reject(error); }); state.request.end();
  });
}
let stopping = false;
const gateway = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1:4163');
  if (shellTests && request.method === 'GET' && url.pathname === '/_fixture/shell-wire') {
    const value = shellWire.get(url.searchParams.get('trace') + '\0' + url.searchParams.get('url'));
    response.writeHead(value ? 200 : 404, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end(JSON.stringify(value || { error: 'shell frame not captured' })); return;
  }
  if (shellTests && request.method === 'POST' && ['/_fixture/expire', '/_fixture/hold-stream', '/_fixture/release-stream'].includes(url.pathname)) {
    const replica = url.searchParams.get('replica') || 'first';
    if (!Object.hasOwn(addresses, replica)) { response.writeHead(400); response.end('unknown replica'); return; }
    try {
      if (url.pathname === '/_fixture/expire') {
        const result = await fetch(`http://127.0.0.1:${addresses[replica]}/partial/control`, { method: 'POST', headers: { Authorization: `Bearer ${controlToken}` } });
        if (result.status !== 204) throw new Error(`real invalidation returned ${result.status}`);
      } else if (url.pathname === '/_fixture/hold-stream') await holdStream(replica);
      else await releaseHolder();
      response.writeHead(200, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end('{}');
    } catch (error) { await releaseHolder(); console.error(error.message); response.writeHead(500); response.end('fixture operation failed'); }
    return;
  }
  if (url.pathname === '/_fixture/release-region' && request.method === 'POST') {
    const gate = gates.get(url.searchParams.get('key'));
    if (!gate) { response.writeHead(404); response.end('region gate not found'); return; }
    gate.released = true; gate.response.write(gate.pending); gate.pending = '';
    if (gate.ended) gate.response.end();
    gates.delete(url.searchParams.get('key')); response.end('released'); return;
  }
  const replica = request.headers['x-fixture-partial'] || 'first';
  if (!Object.hasOwn(addresses, replica)) { response.writeHead(400); response.end('unknown fixture replica'); return; }
  const headers = { ...request.headers, host: '127.0.0.1:4163', 'accept-encoding': 'identity' }; delete headers['x-fixture-partial']; delete headers['x-fixture-partial-gate']; delete headers['x-fixture-shell-trace'];
  const upstream = requestHTTP({ hostname: '127.0.0.1', port: addresses[replica], method: request.method, path: request.url, headers }, incoming => {
    response.writeHead(incoming.statusCode, incoming.headers); incoming.on('error', () => response.destroy());
    const key = request.headers['x-fixture-partial-gate'];
    const media = String(incoming.headers['content-type']);
    const navigationStream = media.includes('application/x-nextgo-stream+json');
    const trace = request.headers['x-fixture-shell-trace'];
    if (shellTests && trace && navigationStream && request.headers['x-nextgo-purpose'] !== 'prefetch') {
      // Keep only the genuine public first frame, before forwarding. Browser
      // stream cleanup can make CDP response.body unavailable after EOF; no
      // private chunks, cookies, authorization or control secrets are retained.
      let first = '', captured = false; const decoder = new StringDecoder('utf8');
      incoming.on('data', chunk => {
        if (captured) return;
        first += typeof chunk === 'string' ? chunk : decoder.write(chunk);
        const end = first.indexOf('\n');
        if (end < 0 && first.length <= 256 * 1024) return;
        captured = true;
        let value;
        try { value = end >= 0 && end <= 256 * 1024 ? { status: incoming.statusCode, frame: JSON.parse(first.slice(0, end)) } : { error: 'fixture shell-frame bound exceeded' }; }
        catch { value = { error: 'invalid genuine shell frame' }; }
        const key = trace + '\0' + request.url; shellWire.delete(key); shellWire.set(key, value);
        while (shellWire.size > 32) shellWire.delete(shellWire.keys().next().value);
        first = '';
      });
    }
    if (!key || (!media.includes('text/html') && !navigationStream) || !url.pathname.startsWith('/partial')) { incoming.pipe(response); return; }
    const gate = { response, pending: '', started: false, released: false, ended: false }; gates.set(key, gate);
    incoming.setEncoding('utf8');
    incoming.on('data', chunk => {
      if (gate.released) { response.write(chunk); return; }
      gate.pending += chunk;
      if (!gate.started) {
        if (navigationStream) {
          // A complete real shell frame is the negotiated-stream commit point.
          const end = gate.pending.indexOf('\n');
          if (end >= 0) { response.write(gate.pending.slice(0, end + 1)); gate.pending = gate.pending.slice(end + 1); gate.started = true; }
        } else {
          // Preserve the real server's complete initial Flush, including its
          // bootstrap-ready marker. Splitting that marker into a tiny later write
          // can leave WebKit's incremental parser waiting behind this test gate.
          const marker = gate.pending.indexOf('<span id="nextgo-initial-ready"');
          const end = marker < 0 ? -1 : gate.pending.indexOf('</span>', marker);
          if (end >= 0) {
            const complete = end + '</span>'.length;
            response.write(gate.pending.slice(0, complete)); gate.pending = gate.pending.slice(complete); gate.started = true;
          }
        }
      }
      if (gate.pending.length > (2 << 20)) response.destroy(new Error('fixture region gate overflow'));
    });
    incoming.on('end', () => { gate.ended = true; if (gate.released) response.end(); });
    response.on('close', () => gates.delete(key));
  });
  upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end('partial fixture upstream unavailable'); });
  request.on('aborted', () => upstream.destroy()); response.on('close', () => { if (!response.writableEnded) upstream.destroy(); }); request.pipe(upstream);
});
for (const [name, port] of Object.entries(addresses)) {
  const child = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', `127.0.0.1:${port}`], { cwd: output, stdio: 'inherit', env: { ...base, NEXTGO_DEMO_PARTIAL_SHELL_FAIL: name === 'live' ? 'false' : 'true', NEXTGO_DEMO_PARTIAL_REGION_FAIL: name === 'broken' ? 'true' : 'false', ...(shellTests ? { NEXTGO_DEMO_PARTIAL_CONTROLS: 'true', NEXTGO_DEMO_PARTIAL_CONTROL_TOKEN: controlToken, NEXTGO_DEMO_PARTIAL_AUTH_FIXTURE: 'true' } : {}), ...(name === 'busy' ? { NEXTGO_DEMO_STREAM_MAX_STREAMS: '1', NEXTGO_DEMO_STREAM_MAX_LOADERS: '1', NEXTGO_DEMO_STREAM_HOLD: 'true' } : {}) } });
  children.push(child);
  child.once('exit', code => { if (!stopping) { console.error(`partial ${name} replica exited ${code}`); shutdown(code || 1); } });
}
function shutdown(code = 0) { if (stopping) return; stopping = true; process.exitCode = code; void releaseHolder(); for (const gate of gates.values()) gate.response.destroy(); gateway.closeAllConnections(); gateway.close(); for (const child of children) child.kill('SIGTERM'); }
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => shutdown());
gateway.listen(4163, '127.0.0.1');
