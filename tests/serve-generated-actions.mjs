// Real generated application plus bounded transport gates; no action mocks.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as requestHTTP } from 'node:http';

const output = join(process.env.NEXTGO_E2E_ROOT, 'generated-actions'); await mkdir(output, { recursive: true });
const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
const children = []; let stopping = false;
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], { stdio: 'inherit', env }); children.push(build);
  const code = await new Promise((resolve, reject) => { build.once('error', reject); build.once('exit', resolve); }); children.splice(children.indexOf(build), 1);
  if (code !== 0) throw new Error('generated-action fixture build failed');
}
const server = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4182'], { cwd: output, stdio: 'inherit', env }); children.push(server);
const gates = new Map(), records = []; let sequence = 0;
function json(response, status, data) { response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end(JSON.stringify(data)); }
const gateway = createServer((request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1:4181');
  if (url.pathname === '/_fixture/records') { json(response, 200, records); return; }
  if (url.pathname === '/_fixture/gate') { const gate = gates.get(url.searchParams.get('key')); json(response, 200, { held: !!gate, status: gate?.status, id: gate?.id }); return; }
  if (url.pathname === '/_fixture/release' && request.method === 'POST') {
    const key = url.searchParams.get('key'), gate = gates.get(key);
    if (!gate) { json(response, 404, {}); return; }
    gates.delete(key); gate.incoming.pipe(gate.response); gate.incoming.resume(); json(response, 200, {}); return;
  }
  const headers = { ...request.headers, host: '127.0.0.1:4181', 'accept-encoding': 'identity' };
  const wasmGate = headers['x-fixture-generated-wasm'], actionGate = headers['x-fixture-generated-action'];
  delete headers['x-fixture-generated-wasm']; delete headers['x-fixture-generated-action'];
  const record = { id: ++sequence, method: request.method, path: request.url, status: 0, aborted: false, complete: false };
  if (url.pathname.startsWith('/_nextgo/actions/')) { records.push(record); if (records.length > 128) records.shift(); }
  const upstream = requestHTTP({ hostname: '127.0.0.1', port: 4182, path: request.url, method: request.method, headers }, incoming => {
    record.status = incoming.statusCode; response.writeHead(incoming.statusCode, incoming.headers);
    const key = url.pathname.endsWith('/app.wasm') ? wasmGate : request.method === 'POST' && url.pathname === '/_nextgo/actions/generated/add/v1' ? actionGate : undefined;
    if (typeof key === 'string' && key.length <= 128 && incoming.statusCode === 200) {
      if (gates.size >= 8 || gates.has(key)) { incoming.destroy(); response.destroy(); return; }
      incoming.pause(); response.flushHeaders(); const gate = { incoming, response, status: incoming.statusCode, id: record.id }; gates.set(key, gate);
      response.on('close', () => { if (gates.get(key) === gate) gates.delete(key); incoming.destroy(); });
    } else incoming.pipe(response);
    incoming.on('end', () => { record.complete = true; }); incoming.on('error', () => response.destroy());
  });
  upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end('generated-action fixture upstream unavailable'); });
  request.on('aborted', () => upstream.destroy()); response.on('close', () => { if (!response.writableEnded) { record.aborted = true; upstream.destroy(); } }); request.pipe(upstream);
});
gateway.listen(4181, '127.0.0.1');
function shutdown(code = 0) { if (stopping) return; stopping = true; process.exitCode = code; for (const gate of gates.values()) { gate.incoming.destroy(); gate.response.destroy(); } gateway.closeAllConnections(); gateway.close(); for (const child of children) child.kill('SIGTERM'); }
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => shutdown());
server.once('exit', code => { if (!stopping) { console.error('generated-action server exited', code); shutdown(code || 1); } });
