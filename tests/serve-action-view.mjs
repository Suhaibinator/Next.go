// Forward genuine application responses. Gates only delay bytes; they never
// manufacture action results, page envelopes, stream frames or private errors.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as requestHTTP } from 'node:http';
import { createActionViewWireObserver } from './action-view-wire-observer.mjs';

const output = join(process.env.NEXTGO_E2E_ROOT, 'action-view');
await mkdir(output, { recursive: true });
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
const children = [], sockets = new Set(), gates = new Map(), records = [];
const endpoint = '/_nextgo/actions/view/save/v1', maxPrefix = 2 << 20;
let stopping = false, sequence = 0;
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], { stdio: 'inherit', env: base });
  children.push(build);
  const code = await new Promise((resolve, reject) => { build.once('error', reject); build.once('exit', resolve); });
  children.splice(children.indexOf(build), 1);
  if (code !== 0) throw new Error('action-view fixture build failed');
}
function json(response, status, value) {
  response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
  response.end(JSON.stringify(value));
}
function release(gate) {
  gates.delete(gate.key);
  if (gate.response.destroyed) { gate.incoming.destroy(); return; }
  const begin = () => { if (!gate.response.destroyed) { gate.incoming.pipe(gate.response); gate.incoming.resume(); } };
  const pending = gate.pending; gate.pending = undefined;
  if (pending?.length && !gate.response.write(pending)) gate.response.once('drain', begin); else begin();
}
const gateway = createServer((request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1:4185');
  if (url.pathname === '/_fixture/action-view/records' && request.method === 'GET') { json(response, 200, records); return; }
  if (url.pathname === '/_fixture/action-view/gate' && request.method === 'GET') {
    const gate = gates.get(url.searchParams.get('key'));
    json(response, 200, { held: !!gate?.held, id: gate?.record.id, phase: gate?.phase, sentFrames: gate?.frames, result: gate?.record.result }); return;
  }
  if (['/_fixture/action-view/release', '/_fixture/action-view/drop'].includes(url.pathname) && request.method === 'POST') {
    const gate = gates.get(url.searchParams.get('key'));
    if (!gate?.held) { json(response, gate ? 409 : 404, { error: 'held gate not found' }); return; }
    if (url.pathname.endsWith('/drop')) { gates.delete(gate.key); gate.incoming.destroy(); gate.response.destroy(); }
    else release(gate);
    json(response, 200, {}); return;
  }
  const trace = request.headers['x-fixture-action-view-trace'];
  const gateKey = request.headers['x-fixture-action-view-gate'];
  const wasmKey = request.headers['x-fixture-action-view-wasm'];
  const phase = request.headers['x-fixture-action-view-phase'] || 'result';
  const skew = request.headers['x-fixture-action-view-skew'];
  const transitions = request.headers['x-fixture-action-view-transitions'] === 'true';
  const headers = { ...request.headers, host: '127.0.0.1:4185', 'accept-encoding': 'identity' };
  for (const key of Object.keys(headers)) if (key.startsWith('x-fixture-action-view-')) delete headers[key];
  if (request.method === 'POST' && url.pathname.endsWith(endpoint)) {
    if (skew === 'build') headers['x-nextgo-action-build'] = 'fixture-obsolete-build';
    if (skew === 'configuration') headers['x-nextgo-configuration'] = '0'.repeat(64);
    if (skew === 'protocol') headers['x-nextgo-action-view'] = '999';
  }
  const record = { id: ++sequence, trace: typeof trace === 'string' ? trace.slice(0, 128) : '', method: request.method, path: request.url, status: 0, aborted: false, complete: false, frames: [], source: headers['x-nextgo-action-url'], view: headers['x-nextgo-action-view'] };
  if (url.pathname.endsWith(endpoint) || url.pathname.endsWith('/action-view') || url.pathname.endsWith('/action-view/')) {
    records.push(record); if (records.length > 128) records.shift();
  }
  const upstream = requestHTTP({ hostname: '127.0.0.1', port: url.pathname.startsWith('/scoped') ? 4187 : transitions ? 4188 : 4186, path: request.url, method: request.method, headers }, incoming => {
    record.status = incoming.statusCode;
    response.writeHead(incoming.statusCode, incoming.headers);
    incoming.on('error', () => response.destroy());
    incoming.on('end', () => { record.complete = true; });
    if (typeof wasmKey === 'string' && wasmKey.length > 0 && wasmKey.length <= 128 && url.pathname.endsWith('/app.wasm') && incoming.statusCode === 200 && String(incoming.headers['content-type']).startsWith('application/wasm')) {
      if (gates.size >= 8 || gates.has(wasmKey)) { incoming.destroy(); response.destroy(new Error('action-view WASM gate capacity exceeded')); return; }
      incoming.pause(); response.flushHeaders();
      const gate = { key: wasmKey, incoming, response, record, phase: 'wasm', frames: 0, held: true, pending: undefined };
      gates.set(wasmKey, gate);
      response.on('close', () => { if (gates.get(wasmKey) === gate) gates.delete(wasmKey); incoming.destroy(); });
      return;
    }
    const stream = String(incoming.headers['content-type']).startsWith('application/x-nextgo-action-view+json');
    if (stream && incoming.statusCode === 200 && request.method === 'POST' && url.pathname.endsWith(endpoint)) {
      // Independently observe the SAME original upstream chunks. This listener
      // neither writes, pauses nor transforms them; existing gates/pipes alone
      // own forwarding. Only bounded framing metadata survives in the record.
      const observer = createActionViewWireObserver(); let ended = false;
      Object.defineProperty(record, 'wire', { enumerable: true, get: () => observer.snapshot() });
      incoming.on('data', chunk => observer.feed(chunk));
      incoming.on('end', () => { ended = true; observer.end(); });
      incoming.on('error', () => observer.fail());
      incoming.on('close', () => { if (!ended) observer.fail(); });
    }
    if (typeof gateKey !== 'string' || gateKey.length === 0 || gateKey.length > 128 || !['result', 'shell'].includes(phase) || !stream || incoming.statusCode !== 200 || request.method !== 'POST' || !url.pathname.endsWith(endpoint)) { incoming.pipe(response); return; }
    if (gates.size >= 8 || gates.has(gateKey)) { incoming.destroy(); response.destroy(new Error('action-view gate capacity exceeded')); return; }
    // Reserve before reading any prefix so even responses whose first record
    // is delayed count against the same eight-flight gate budget.
    const gate = { key: gateKey, incoming, response, record, phase, frames: 0, held: false, pending: undefined };
    gates.set(gateKey, gate);
    response.on('close', () => { if (gates.get(gateKey) === gate) gates.delete(gateKey); incoming.destroy(); });
    let prefix = Buffer.alloc(0), cursor = 0, frameCount = 0;
    const onData = chunk => {
      prefix = Buffer.concat([prefix, chunk]);
      if (prefix.length > maxPrefix) { incoming.destroy(); response.destroy(new Error('action-view prefix bound exceeded')); return; }
      let newline;
      while ((newline = prefix.indexOf(10, cursor)) >= 0) {
        let frame;
        try { frame = JSON.parse(prefix.subarray(cursor, newline).toString('utf8')); }
        catch { incoming.destroy(); response.destroy(new Error('invalid genuine action-view frame')); return; }
        if (frameCount === 0 && frame.type !== 'result' || frameCount === 1 && frame.type !== 'shell') { incoming.destroy(); response.destroy(new Error('unexpected genuine action-view prefix')); return; }
        frameCount++; record.frames.push(frame.type);
        if (frame.type === 'result') record.result = frame.result;
        cursor = newline + 1;
        if (frameCount === (phase === 'result' ? 1 : 2)) {
          incoming.pause(); incoming.off('data', onData);
          response.write(prefix.subarray(0, cursor));
          gate.frames = frameCount; gate.held = true; gate.pending = prefix.subarray(cursor); prefix = Buffer.alloc(0);
          return;
        }
      }
    };
    incoming.on('data', onData);
  });
  upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end('action-view fixture upstream unavailable'); });
  request.on('aborted', () => upstream.destroy());
  response.on('close', () => { if (!response.writableEnded) { record.aborted = true; upstream.destroy(); } });
  request.pipe(upstream);
});
gateway.on('connection', socket => { sockets.add(socket); socket.once('close', () => sockets.delete(socket)); });
function shutdown(code = 0) {
  if (stopping) return; stopping = true; process.exitCode = code;
  for (const gate of gates.values()) { gate.incoming.destroy(); gate.response.destroy(); }
  gates.clear(); for (const socket of sockets) socket.destroy(); gateway.close();
  for (const child of children) child.kill('SIGTERM');
}
gateway.once('error', error => { console.error(error); shutdown(1); });
for (const [port, scoped, transitions] of [[4186, false, false], [4187, true, false], [4188, false, true]]) {
  const child = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', `127.0.0.1:${port}`], { cwd: output, stdio: 'inherit', env: { ...base, ...(scoped ? { NEXTGO_DEMO_BASE_PATH: '/scoped', NEXTGO_DEMO_LOCALES: 'true', NEXTGO_DEMO_LOCALE_DETECT: 'false', NEXTGO_DEMO_TRAILING_SLASH: 'true' } : {}), ...(transitions ? { NEXTGO_DEMO_VIEW_TRANSITIONS: 'true' } : {}) } });
  children.push(child); child.once('error', error => { console.error(error); shutdown(1); });
  child.once('exit', code => { if (!stopping) { console.error('action-view replica exited', port, code); shutdown(code || 1); } });
}
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => shutdown());
gateway.listen(4185, '127.0.0.1');
