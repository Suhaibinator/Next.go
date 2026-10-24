// All app replicas see one public Host. Selection is a fixture concern and is
// removed before forwarding, so cookies/authorization do not defeat the test.
import { spawn } from 'node:child_process';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as httpRequest, Agent } from 'node:http';

const publicURL = 'http://127.0.0.1:4149';
const cacheURL = 'http://127.0.0.1:4150';
const cacheAuthority = '127.0.0.1:4154';
const token = 'nextgo-shared-page-browser-public-fixture-token';
const replicas = { a: '127.0.0.1:4151', b: '127.0.0.1:4152', fail: '127.0.0.1:4153' };
const output = join(process.env.NEXTGO_E2E_ROOT, 'shared-pages');
await mkdir(output, { recursive: true });
const children = new Set();
const servers = new Set();
const scopes = new Map(); // Observed ID/policy only; never payloads or tokens.
const agent = new Agent({ keepAlive: true, maxSockets: 64 });
let stopping = false;
let ready = false;
let pageReads = 0;
function stop() {
  if (stopping) return;
  stopping = true;
  agent.destroy();
  for (const server of servers) { server.close(); server.closeAllConnections(); }
  for (const child of children) child.kill('SIGTERM');
}
process.on('SIGTERM', stop); process.on('SIGINT', stop);
async function build(args) {
  const child = spawn('go', args, { stdio: 'inherit' }); children.add(child);
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', resolve); });
  children.delete(child);
  if (code !== 0 || stopping) throw new Error(`shared-page fixture build exited ${code}`);
}
if (!process.env.NEXTGO_E2E_SERVER_BINARY) await build(['run', './cmd/nextgo', 'build', '-out', output, './examples/demo']);
if (!process.env.NEXTGO_E2E_CACHE_BINARY) await build(['build', '-trimpath', '-o', join(output, 'nextgo-cache'), './cmd/nextgo-cache']);

const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_')));
env.NEXTGO_CACHE_TOKEN = token; env.NEXTGO_DEMO_SHARED_ENDPOINT = cacheURL;
function start(binary, args, additions = {}) {
  const child = spawn(binary, args, { cwd: output, stdio: 'inherit', env: { ...env, ...additions } }); children.add(child);
  child.once('error', error => { console.error(error); process.exitCode = 1; stop(); });
  child.once('exit', code => {
    children.delete(child);
    if (!stopping) { process.exitCode = code || 1; stop(); }
  });
}
function respond(response, status, value) {
  response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' });
  response.end(JSON.stringify(value));
}
async function body(request, limit = 2 << 20) {
  const chunks = []; let size = 0;
  for await (const chunk of request) { size += chunk.length; if (size > limit) throw new Error('fixture body exceeds limit'); chunks.push(chunk); }
  return Buffer.concat(chunks);
}
function proxy(request, response, target, headers, bytes, observed) {
  const upstream = httpRequest(`http://${target}${request.url}`, { method: request.method, headers, agent }, incoming => {
    if (observed) {
      const parts = []; let size = 0;
      incoming.on('data', chunk => { size += chunk.length; if (size <= 2 << 20) parts.push(chunk); });
      incoming.on('end', () => {
        if (incoming.statusCode !== 200 || size > 2 << 20) return;
        try {
          const result = JSON.parse(Buffer.concat(parts).toString('utf8'));
          if (result.Version === 3 && result.View && observed.ID?.Schema === 'demo-pages-v1' && ['begin', 'begin-stale'].includes(observed.Operation)) {
            for (const path of observed.Policy?.Paths || []) {
              if (scopes.size < 32 || scopes.has(path)) scopes.set(path, { ID: observed.ID, Policy: observed.Policy });
            }
          }
        } catch { /* incompatible/malformed operations stay observable to the caller */ }
      });
    }
    response.writeHead(incoming.statusCode || 502, incoming.headers); incoming.pipe(response);
    incoming.once('error', () => response.destroy());
  });
  upstream.once('error', () => { if (!response.headersSent) respond(response, 502, { error: 'fixture upstream unavailable' }); else response.destroy(); });
  request.once('aborted', () => upstream.destroy());
  response.once('close', () => { if (!response.writableEnded) upstream.destroy(); });
  if (bytes) upstream.end(bytes); else request.pipe(upstream);
}
const cacheProxy = createServer(async (request, response) => {
  try {
    const bytes = await body(request);
    let operation;
    try { operation = JSON.parse(bytes.toString('utf8')); } catch { /* relay untouched for backend validation */ }
    if (operation?.ID?.Schema === 'demo-pages-v1' && ['begin', 'begin-stale'].includes(operation.Operation)) pageReads++;
    proxy(request, response, cacheAuthority, { ...request.headers, host: cacheAuthority }, bytes, operation);
  } catch { respond(response, 413, { error: 'fixture cache envelope too large' }); }
});
const gateway = createServer((request, response) => {
  const url = new URL(request.url, publicURL);
  if (url.pathname === '/_fixture/ready') { respond(response, ready ? 200 : 503, { ready }); return; }
  if (url.pathname === '/_fixture/cache-scope' && request.method === 'GET') {
    const scope = scopes.get(url.searchParams.get('path'));
    respond(response, scope ? 200 : 404, scope || { error: 'no observed page lookup' }); return;
  }
  if (url.pathname === '/_fixture/stats') { respond(response, 200, { pageReads }); return; }
  if (url.pathname === '/_fixture/reset' && request.method === 'POST') { scopes.clear(); pageReads = 0; respond(response, 200, { ok: true }); return; }
  const selected = request.headers['x-fixture-replica'] || 'a';
  if (!Object.hasOwn(replicas, selected)) { respond(response, 400, { error: 'unknown fixture replica' }); return; }
  const headers = { ...request.headers, host: new URL(publicURL).host };
  delete headers['x-fixture-replica'];
  proxy(request, response, replicas[selected], headers);
});
for (const [server, port] of [[cacheProxy, 4150], [gateway, 4149]]) {
  servers.add(server);
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(port, '127.0.0.1', resolve); });
}
start(process.env.NEXTGO_E2E_CACHE_BINARY || join(output, 'nextgo-cache'), ['-addr', cacheAuthority, '-namespace', 'demo']);
for (const [name, address] of Object.entries(replicas)) start(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', address], name === 'fail' ? { NEXTGO_DEMO_SHARED_PAGE_FAIL: 'true' } : {});
for (let attempt = 0; attempt < 600 && !stopping; attempt++) {
  try {
    const health = await Promise.all(Object.values(replicas).map(authority => fetch(`http://${authority}/api/health`)));
    await Promise.all(health.map(response => response.arrayBuffer()));
    if (health.every(response => response.status === 200)) {
      const cache = await fetch(cacheURL, { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ Version: 3, Operation: 'expire', Namespace: 'demo', Selector: { Kind: 'all', Value: '' } }) });
      await cache.arrayBuffer();
      if (cache.status === 200) { ready = true; break; }
    }
  } catch { /* readiness depends on all owned listeners, not process existence */ }
  await new Promise(resolve => setTimeout(resolve, 50));
}
if (!ready && !stopping) { process.exitCode = 1; console.error('shared-page fixture readiness timed out'); stop(); }
