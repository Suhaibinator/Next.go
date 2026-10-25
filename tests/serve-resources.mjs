// Real production binary behind a bounded body gate; no response mocking.
import {spawn} from 'node:child_process';
import {mkdir} from 'node:fs/promises';
import {join} from 'node:path';
import {createServer, request as httpRequest} from 'node:http';

const output = join(process.env.NEXTGO_E2E_ROOT, 'resources');
await mkdir(output, {recursive: true});
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = spawn('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], {stdio: 'inherit', env: base});
  if (await new Promise((resolve, reject) => {build.once('exit', resolve); build.once('error', reject);}) !== 0) process.exit(1);
}
const child = spawn(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4204'], {cwd: output, stdio: 'inherit', env: {...base, NEXTGO_DEMO_BASE_PATH: '/resource-demo', NEXTGO_DEMO_ASSET_PREFIX: '/resource-assets', NEXTGO_DEMO_CSP: 'true'}});
const records = [], gates = new Map();
let stopping = false;
function json(response, status, value) {response.writeHead(status, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'}); response.end(JSON.stringify(value));}
const gateway = createServer((request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1:4203');
  if (url.pathname === '/_fixture/resources/records') {json(response, 200, records); return;}
  if (url.pathname === '/_fixture/resources/release' && request.method === 'POST') {
    const gate = gates.get(url.searchParams.get('key'));
    if (gate) {gates.delete(gate.key); gate.released = true; if (gate.ended) responseRelease(gate);}
    json(response, 200, {}); return;
  }
  const tracked = url.pathname === '/resource-demo/api/resources';
  const key = tracked ? String(request.headers['x-resource-gate'] || '').slice(0, 128) : '';
  if (key && (gates.size >= 16 || gates.has(key))) {json(response, 429, {}); return;}
  const record = tracked ? {path: request.url, method: request.method, key, status: 0, held: false, aborted: false} : undefined;
  if (record) {if (records.length === 128) records.shift(); records.push(record);}
  const gate = key ? {key, response, record, chunks: [], size: 0, ended: false, released: false} : undefined;
  if (gate) gates.set(key, gate);
  const upstream = httpRequest({hostname: '127.0.0.1', port: 4204, path: request.url, method: request.method, headers: request.headers}, incoming => {
    if (record) record.status = incoming.statusCode;
    response.writeHead(incoming.statusCode, incoming.headers);
    if (!gate) {incoming.pipe(response); return;}
    response.flushHeaders();
    incoming.on('data', chunk => {
      gate.size += chunk.length;
      if (gate.size > 4096) {incoming.destroy(); response.destroy(); return;}
      gate.chunks.push(chunk);
    });
    incoming.on('end', () => {gate.ended = true; record.held = true; if (gate.released) responseRelease(gate);});
  });
  upstream.on('error', () => {if (!response.headersSent) response.writeHead(502); response.end();});
  response.on('close', () => {
    if (!response.writableEnded) {if (record) record.aborted = true; upstream.destroy();}
    if (gate && gates.get(key) === gate) gates.delete(key);
  });
  request.pipe(upstream);
});
function responseRelease(gate) {for (const chunk of gate.chunks) gate.response.write(chunk); gate.chunks = []; gate.response.end();}
gateway.listen(4203, '127.0.0.1');
function stop(code = 0) {if (stopping) return; stopping = true; process.exitCode = code; for (const gate of gates.values()) gate.response.destroy(); gates.clear(); gateway.closeAllConnections(); gateway.close(); child.kill('SIGTERM');}
for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => stop());
child.once('error', () => stop(1));
child.once('exit', code => {if (!stopping) stop(code || 1);});
