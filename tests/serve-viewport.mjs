// Isolated real production/development servers with bounded transport evidence.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { createServer, request as requestHTTP } from 'node:http';
import { StringDecoder } from 'node:string_decoder';

const output = join(process.env.NEXTGO_E2E_ROOT, 'viewport'); await mkdir(output, { recursive: true });
const source = process.cwd();
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
const children = []; let stopping = false;
function start(command, args, options = {}) { const child = spawn(command, args, { stdio: 'inherit', env: base, ...options }); children.push(child); return child; }
async function run(command, args, env = base) { const child = start(command, args, { env }); if (await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', resolve); }) !== 0) throw new Error('viewport fixture build failed'); children.splice(children.indexOf(child), 1); }
if (!process.env.NEXTGO_E2E_SERVER_BINARY) await run('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/demo'], { ...base, NEXTGO_DEMO_PARTIAL_REGION_FAIL: 'true' });
const cli = join(output, 'nextgo'); await run('go', ['build', '-o', cli, './cmd/nextgo']);
start(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4172'], { cwd: output, env: { ...base, NEXTGO_DEMO_PARTIAL_SHELL_FAIL: 'true' } });
start(cli, ['dev', '-addr', '127.0.0.1:4173', resolve(source, 'examples/demo')], { cwd: source });

let sequence = 0;
const records = [], gates = new Map(), released = new Set();
function release(state) { if (state.released) return; state.released = true; for (const chunk of state.chunks) state.response.write(chunk); state.chunks = []; if (state.ended) state.response.end(); }
function json(response, status, value) { response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end(JSON.stringify(value)); }
const gateway = createServer(async (request, response) => {
  const url = new URL(request.url, 'http://127.0.0.1:4171');
  if (url.pathname === '/_fixture/ready') {
    try { const checks = await Promise.all([4172, 4173].map(async port => (await fetch(`http://127.0.0.1:${port}/api/health`)).status)); json(response, checks.every(status => status === 200) ? 200 : 503, {}); } catch { json(response, 503, {}); }
    return;
  }
  if (url.pathname === '/_fixture/requests') { json(response, 200, records); return; }
  if (url.pathname === '/_fixture/release' && request.method === 'POST') {
    const key = url.searchParams.get('key'), id = Number(url.searchParams.get('id'));
    if (!id) released.add(key);
    for (const [number, state] of gates) if (state.key === key && (!id || id === number)) { release(state); gates.delete(number); }
    json(response, 200, {}); return;
  }
  const record = { id: ++sequence, path: request.url, method: request.method, purpose: request.headers['x-nextgo-purpose'] || '', navigate: request.headers['x-nextgo-navigate'] || '', shellHint: request.headers['x-nextgo-shell'] || '', status: 0, complete: false, delivered: false, aborted: false };
  records.push(record); if (records.length > 512) records.shift();
  const upstream = requestHTTP({ hostname: '127.0.0.1', port: 4172, method: request.method, path: request.url, headers: { ...request.headers, host: '127.0.0.1:4171', 'accept-encoding': 'identity' } }, incoming => {
    record.status = incoming.statusCode; record.prefetch = incoming.headers['x-nextgo-prefetch'] || ''; record.shellProtocol = incoming.headers['x-nextgo-shell-protocol'] || '';
    response.writeHead(incoming.statusCode, incoming.headers);
    const media = String(incoming.headers['content-type']); const captureJSON = record.purpose === 'prefetch' && media.includes('application/json'); const captureFrame = media.includes('application/x-nextgo-stream+json');
    let text = '', captured = false; const decoder = new StringDecoder('utf8');
    const key = url.searchParams.get('gate'); const held = record.purpose === 'prefetch' && key && !released.has(key);
    const state = { key, response, chunks: [], bytes: 0, ended: false, released: !held };
    if (held) gates.set(record.id, state);
    incoming.on('data', chunk => {
      if ((captureJSON || captureFrame) && !captured) {
        text += decoder.write(chunk);
        if (captureFrame && text.includes('\n')) { captured = true; try { record.shellFrame = JSON.parse(text.slice(0, text.indexOf('\n'))); } catch { record.wireError = true; } text = ''; }
        if (text.length > 256 * 1024) { captured = true; text = ''; record.wireError = true; }
      }
      if (state.released) response.write(chunk);
      else { state.chunks.push(chunk); state.bytes += chunk.length; if (state.bytes > (2 << 20)) response.destroy(new Error('viewport fixture body bound exceeded')); }
    });
    incoming.on('end', () => {
      record.complete = true; state.ended = true;
      if (captureJSON && !captured) { try { const body = JSON.parse(text); if (body.shell) record.publicShell = body; } catch { record.wireError = true; } }
      if (state.released) response.end();
    });
    incoming.on('error', () => response.destroy());
  });
  upstream.on('error', () => { if (!response.headersSent) response.writeHead(502); response.end('viewport upstream unavailable'); });
  response.on('finish', () => { record.delivered = true; });
  request.on('aborted', () => upstream.destroy()); response.on('close', () => { gates.delete(record.id); if (!response.writableEnded) { record.aborted = true; upstream.destroy(); } }); request.pipe(upstream);
});
gateway.listen(4171, '127.0.0.1');
function shutdown(code = 0) { if (stopping) return; stopping = true; process.exitCode = code; for (const state of gates.values()) state.response.destroy(); gateway.closeAllConnections(); gateway.close(); for (const child of children) child.kill('SIGTERM'); }
for (const signal of ['SIGTERM', 'SIGINT']) process.on(signal, () => shutdown());
for (const child of children) child.once('exit', code => { if (!stopping) { console.error('viewport server exited', code); shutdown(code || 1); } });
