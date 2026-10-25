// Real native replicas and bounded byte-delay gates. No session credentials,
// cookies, signing keys or action request bodies enter the observation records.
import { spawn } from 'node:child_process';
import { randomBytes } from 'node:crypto';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as requestHTTP } from 'node:http';
import { readerToken, adminToken } from './access-fixture.mjs';

const output = join(process.env.NEXTGO_E2E_ROOT, 'cookie-dispatch'); await mkdir(output, { recursive: true });
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
const key = randomBytes(32).toString('hex');
const environment = { ...base, NEXTGO_DEMO_COOKIE_KEY: key, NEXTGO_DEMO_COOKIE_ALLOW_INSECURE: 'true', NEXTGO_DEMO_READER_TOKEN: readerToken, NEXTGO_DEMO_ADMIN_TOKEN: adminToken };
const children = [], sockets = new Set(), gates = new Map(), records = [], activeActions = new Set();
let stopping = false, sequence = 0, coldReady;
const maxPrefix = 2 << 20;
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], { stdio: 'inherit', env: base }); children.push(build);
  const code = await new Promise((resolve, reject) => { build.once('error', reject); build.once('exit', resolve); }); children.splice(children.indexOf(build), 1);
  if (code !== 0) throw new Error('cookie-dispatch fixture build failed');
}
const binary = process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server');
function json(response, status, value) { response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end(JSON.stringify(value)); }
function spawnReplica(port, scoped = false) {
  const child = spawn(binary, ['-addr', `127.0.0.1:${port}`], { cwd: output, stdio: 'inherit', env: { ...environment, ...(scoped ? { NEXTGO_DEMO_BASE_PATH: '/scoped', NEXTGO_DEMO_LOCALES: 'true', NEXTGO_DEMO_LOCALE_DETECT: 'false', NEXTGO_DEMO_TRAILING_SLASH: 'true' } : {}) } });
  children.push(child); child.once('error', error => { console.error(error.message); shutdown(1); });
  child.once('exit', code => { if (!stopping) { console.error('cookie-dispatch replica exited', port, code); shutdown(code || 1); } });
}
async function startCold() {
  if (!coldReady) coldReady = (async () => {
    spawnReplica(4193);
    for (let attempt = 0; attempt < 100; attempt++) {
      const ready = await new Promise(resolve => {
        const request = requestHTTP({ hostname: '127.0.0.1', port: 4193, path: '/api/health', headers: { Host: '127.0.0.1:4190' }, timeout: 1000 }, response => { response.resume(); resolve(response.statusCode === 200); });
        request.on('error', () => resolve(false)); request.on('timeout', () => { request.destroy(); resolve(false); }); request.end();
      });
      if (ready) return;
      if (stopping) throw new Error('cookie-dispatch fixture is stopping');
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    throw new Error('cold cookie replica did not become ready');
  })();
  return coldReady;
}
function release(gate) {
  gates.delete(gate.key);
  if (gate.response.destroyed) { gate.incoming.destroy(); return; }
  const begin = () => { if (!gate.response.destroyed) { gate.incoming.pipe(gate.response); gate.incoming.resume(); } };
  const pending = gate.pending; gate.pending = undefined;
  if (pending?.length && !gate.response.write(pending)) gate.response.once('drain', begin); else begin();
}
const gateway = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1:4190');
  if (url.pathname === '/_fixture/cookie/records' && request.method === 'GET') { json(response, 200, records); return; }
  if (url.pathname === '/_fixture/cookie/gate' && request.method === 'GET') {
    const gate = gates.get(url.searchParams.get('key'));
    json(response, 200, { held: !!gate?.held, id: gate?.record.id, phase: gate?.phase, frames: gate?.frames }); return;
  }
  if (['/_fixture/cookie/release', '/_fixture/cookie/drop'].includes(url.pathname) && request.method === 'POST') {
    const gate = gates.get(url.searchParams.get('key'));
    if (!gate?.held) { json(response, gate ? 409 : 404, { error: 'held gate unavailable' }); return; }
    if (url.pathname.endsWith('/drop')) { gates.delete(gate.key); gate.incoming.destroy(); gate.response.destroy(); } else release(gate);
    json(response, 200, {}); return;
  }
  if (url.pathname === '/_fixture/cookie/start-cold' && request.method === 'POST') {
    try { await startCold(); json(response, 200, { ready: true }); } catch { json(response, 500, { error: 'cold replica unavailable' }); }
    return;
  }
  const trace = request.headers['x-fixture-cookie-trace'], gateKey = request.headers['x-fixture-cookie-gate'], wasmKey = request.headers['x-fixture-cookie-wasm'];
  const phase = request.headers['x-fixture-cookie-phase'] || 'body';
  const cold = request.headers['x-fixture-cookie-replica'] === 'cold';
  if (cold && !coldReady) { json(response, 503, { error: 'start the cold replica before routing to it' }); return; }
  const headers = { ...request.headers, host: '127.0.0.1:4190', 'accept-encoding': 'identity' };
  for (const name of Object.keys(headers)) if (name.startsWith('x-fixture-cookie-')) delete headers[name];
  const action = request.method === 'POST' && url.pathname.includes('/_nextgo/actions/');
  const record = { id: ++sequence, trace: typeof trace === 'string' ? trace.slice(0, 128) : '', method: request.method, path: url.pathname, status: 0, delivered: false, aborted: false, outstandingBefore: action ? activeActions.size : 0, hasSetCookie: false };
  if (action && activeActions.size >= 64) { json(response, 503, { error: 'fixture action capacity exceeded' }); return; }
  if (action) activeActions.add(record.id);
  if (action || /\/(cookie-session|optimistic|action-view)\/?$/.test(url.pathname)) { records.push(record); if (records.length > 128) records.shift(); }
  response.once('finish', () => { record.delivered = true; activeActions.delete(record.id); });
  response.once('close', () => { activeActions.delete(record.id); if (!response.writableEnded) record.aborted = true; });
  const upstream = requestHTTP({ hostname: '127.0.0.1', port: url.pathname.startsWith('/scoped') ? 4192 : cold ? 4193 : 4191, path: request.url, method: request.method, headers }, incoming => {
    record.status = incoming.statusCode; record.hasSetCookie = !!incoming.headers['set-cookie']; response.writeHead(incoming.statusCode, incoming.headers);
    incoming.on('error', () => response.destroy());
    const media = String(incoming.headers['content-type']);
    const wasm = typeof wasmKey === 'string' && wasmKey.length > 0 && wasmKey.length <= 128 && url.pathname.endsWith('/app.wasm') && incoming.statusCode === 200 && media.startsWith('application/wasm');
    const typed = typeof gateKey === 'string' && gateKey.length > 0 && gateKey.length <= 128 && action && ['body', 'result', 'shell'].includes(phase) && incoming.statusCode === 200 && (media.startsWith('application/json') || media.startsWith('application/x-nextgo-action-view+json'));
    if (!wasm && !typed) { incoming.pipe(response); return; }
    const selected = wasm ? wasmKey : gateKey;
    if (gates.size >= 8 || gates.has(selected)) { incoming.destroy(); response.destroy(new Error('cookie-dispatch gate capacity exceeded')); return; }
    const gate = { key: selected, incoming, response, record, phase: wasm ? 'wasm' : phase, held: false, frames: 0, pending: undefined };
    gates.set(selected, gate);
    response.once('close', () => { if (gates.get(selected) === gate) gates.delete(selected); incoming.destroy(); });
    if (wasm || phase === 'body') { incoming.pause(); response.flushHeaders(); gate.held = true; return; }
    if (!media.startsWith('application/x-nextgo-action-view+json')) { incoming.destroy(); response.destroy(new Error('result gate requires genuine action-view frames')); return; }
    let prefix = Buffer.alloc(0), cursor = 0;
    const onData = chunk => {
      prefix = Buffer.concat([prefix, chunk]);
      if (prefix.length > maxPrefix) { incoming.destroy(); response.destroy(new Error('cookie-dispatch prefix bound exceeded')); return; }
      let newline;
      while ((newline = prefix.indexOf(10, cursor)) >= 0) {
        let frame; try { frame = JSON.parse(prefix.subarray(cursor, newline).toString('utf8')); } catch { incoming.destroy(); response.destroy(new Error('invalid genuine action-view frame')); return; }
        if (gate.frames === 0 && frame.type !== 'result' || gate.frames === 1 && frame.type !== 'shell') { incoming.destroy(); response.destroy(new Error('unexpected genuine action-view prefix')); return; }
        gate.frames++; cursor = newline + 1;
        if (gate.frames === (phase === 'result' ? 1 : 2)) {
          incoming.pause(); incoming.off('data', onData); response.write(prefix.subarray(0, cursor));
          gate.pending = prefix.subarray(cursor); gate.held = true; prefix = Buffer.alloc(0); return;
        }
      }
    };
    incoming.on('data', onData);
  });
  upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end('cookie-dispatch upstream unavailable'); });
  request.on('aborted', () => upstream.destroy()); response.on('close', () => { if (!response.writableEnded) upstream.destroy(); }); request.pipe(upstream);
});
gateway.on('connection', socket => { sockets.add(socket); socket.once('close', () => sockets.delete(socket)); });
function shutdown(code = 0) {
  if (stopping) return; stopping = true; process.exitCode = code;
  for (const gate of gates.values()) { gate.incoming.destroy(); gate.response.destroy(); } gates.clear();
  for (const socket of sockets) socket.destroy(); gateway.close(); for (const child of children) child.kill('SIGTERM');
}
gateway.once('error', error => { console.error(error.message); shutdown(1); });
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => shutdown());
spawnReplica(4191); spawnReplica(4192, true); gateway.listen(4190, '127.0.0.1');
