// Online build qualification: the durable authority exists before nextgo build.
// Each scenario reserves a new ticket; no cache rollback or seed-file editing.
import { spawn } from 'node:child_process';
import { mkdir, chmod } from 'node:fs/promises';
import { join } from 'node:path';
import { createServer, request as httpRequest, Agent } from 'node:http';

const publicURL = 'http://127.0.0.1:4155';
const cacheURL = 'http://127.0.0.1:4156';
const token = 'nextgo-shared-prerender-public-fixture-token';
const addresses = { fail: '127.0.0.1:4157', healthy: '127.0.0.1:4158' };
const root = join(process.env.NEXTGO_E2E_ROOT, 'shared-prerender');
const data = join(root, 'authority');
await mkdir(data, { recursive: true, mode: 0o700 }); await chmod(data, 0o700);
const nextgo = join(root, 'nextgo');
const cacheBinary = join(root, 'nextgo-cache');
const children = new Set(); const apps = new Map();
const agent = new Agent({ keepAlive: true, maxSockets: 32 });
let authority, gateway, stopping = false, ready = false, busy = false, sequence = 0, artifact;
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_') && key !== 'NEXTGO_CACHE_TOKEN'));
const applicationEnv = { ...base, NEXTGO_CACHE_TOKEN: token, NEXTGO_DEMO_SHARED_ENDPOINT: cacheURL, NEXTGO_DEMO_SHARED_PRERENDER: 'true' };
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function own(command, args, options = {}) {
  const child = spawn(command, args, { stdio: 'inherit', env: base, ...options });
  const record = { child, expected: false, exited: false };
  children.add(record);
  record.done = new Promise((resolve, reject) => {
    child.once('error', error => { children.delete(record); record.exited = true; reject(error); });
    child.once('exit', (code, signal) => { children.delete(record); record.exited = true; resolve({ code, signal }); });
  });
  return record;
}
async function run(command, args, env = base) {
  const record = own(command, args, { env }); record.expected = true;
  const result = await record.done;
  if (result.code !== 0 || stopping) throw new Error(`fixture build exited ${result.code ?? result.signal}`);
}
async function stop(record, signal = 'SIGTERM') {
  if (!record || record.exited) return;
  record.expected = true; record.child.kill(signal);
  const timer = setTimeout(() => { if (!record.exited) record.child.kill('SIGKILL'); }, 12_000);
  try { await record.done; } finally { clearTimeout(timer); }
}
async function stopApps() { await Promise.all([...apps.values()].map(record => stop(record))); apps.clear(); }
function service(command, args, env) {
  const record = own(command, args, { cwd: root, env });
  record.done.then(result => { if (!record.expected && !stopping) { console.error(`shared prerender service exited ${result.code ?? result.signal}`); process.exitCode = 1; shutdown(); } }).catch(error => { console.error(error); process.exitCode = 1; shutdown(); });
  return record;
}
async function waitReady(url, expected = 200) {
  for (let i = 0; i < 400 && !stopping; i++) {
    try { const response = await fetch(url); await response.arrayBuffer(); if (response.status === expected) return; } catch { /* listener not ready */ }
    await sleep(25);
  }
  throw new Error(`owned listener not ready: ${url}`);
}
async function startAuthority() {
  authority = service(cacheBinary, ['-addr', new URL(cacheURL).host, '-namespace', 'demo', '-data-dir', data, '-disk-bytes', '67108864'], { ...base, NEXTGO_CACHE_TOKEN: token });
  await waitReady(cacheURL, 401); // Authentication refusal proves the opened store is listening, without mutations.
}
async function startApps() {
  if (!artifact) throw new Error('build an artifact before runtime startup');
  for (const [name, address] of Object.entries(addresses)) apps.set(name, service(join(artifact, 'server'), ['-addr', address], { ...applicationEnv, NEXTGO_DEMO_SHARED_PRERENDER_FAIL: name === 'fail' ? 'true' : 'false' }));
  await Promise.all(Object.values(addresses).map(address => waitReady(`http://${address}/api/health`)));
}
async function operation(name, extra = {}) {
  const response = await fetch(cacheURL, { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: JSON.stringify({ Version: 3, Operation: name, Namespace: 'demo', ...extra }) });
  const result = await response.json(); if (response.status !== 200) throw new Error(`authority operation failed: ${response.status}`); return result;
}
async function reset(options) {
  await stopApps();
  await operation('expire', { Selector: { Kind: 'all', Value: '' } });
  artifact = join(root, `generation-${++sequence}`);
  // Runtime's failing-source flag is deliberately absent while the builder
  // reads application data. Both build and runtime select the shared policy.
  await run(nextgo, ['build', '-out', artifact, './examples/demo'], applicationEnv);
  if (!options.deferStartup) await startApps();
  return { sequence, started: !options.deferStartup };
}
async function shutdown() {
  if (stopping) return; stopping = true; ready = false;
  agent.destroy(); gateway?.close(); gateway?.closeAllConnections();
  await Promise.all([...children].map(record => stop(record)));
}
process.on('SIGTERM', () => { void shutdown(); }); process.on('SIGINT', () => { void shutdown(); });
function respond(response, status, value) {
  if (response.destroyed || response.writableEnded) return;
  response.writeHead(status, { 'Content-Type': 'application/json', 'Cache-Control': 'no-store' }); response.end(JSON.stringify(value));
}
async function readOptions(request) {
  const chunks = []; let size = 0;
  for await (const chunk of request) { size += chunk.length; if (size > 4096) throw new Error('control envelope too large'); chunks.push(chunk); }
  return size ? JSON.parse(Buffer.concat(chunks).toString('utf8')) : {};
}
async function control(request, response, path) {
  if (busy) { respond(response, 409, { error: 'fixture operation active' }); return; }
  busy = true;
  try {
    const options = await readOptions(request);
    if (path === '/_fixture/reset') respond(response, 200, await reset(options));
    else if (path === '/_fixture/start-apps') { if (apps.size) throw new Error('apps already started'); await startApps(); respond(response, 200, { started: true }); }
    else if (path === '/_fixture/restart-apps') { await stopApps(); await startApps(); respond(response, 200, { restarted: true }); }
    else if (path === '/_fixture/restart-cache') { await stop(authority, 'SIGKILL'); await startAuthority(); respond(response, 200, { restarted: true }); }
    else if (path === '/_fixture/restart-all') { await stopApps(); await stop(authority, 'SIGKILL'); await startAuthority(); await startApps(); respond(response, 200, { restarted: true }); }
    else respond(response, 404, { error: 'unknown fixture operation' });
  } catch (error) { console.error(error.message); respond(response, 500, { error: 'fixture operation failed' }); }
  finally { busy = false; }
}
gateway = createServer((request, response) => {
  const path = new URL(request.url, publicURL).pathname;
  if (path === '/_fixture/ready') { respond(response, ready ? 200 : 503, { ready }); return; }
  if (path.startsWith('/_fixture/') && request.method === 'POST') { void control(request, response, path); return; }
  const selected = request.headers['x-fixture-replica'] || 'fail';
  if (!Object.hasOwn(addresses, selected) || !apps.has(selected)) { respond(response, 503, { error: 'runtime not started' }); return; }
  const headers = { ...request.headers, host: new URL(publicURL).host }; delete headers['x-fixture-replica'];
  const upstream = httpRequest(`http://${addresses[selected]}${request.url}`, { method: request.method, headers, agent }, incoming => { response.writeHead(incoming.statusCode || 502, incoming.headers); incoming.pipe(response); incoming.once('error', () => response.destroy()); });
  upstream.once('error', () => { if (!response.headersSent) respond(response, 502, { error: 'runtime unavailable' }); else response.destroy(); });
  request.once('aborted', () => upstream.destroy()); response.once('close', () => { if (!response.writableEnded) upstream.destroy(); });
  request.pipe(upstream);
});
await new Promise((resolve, reject) => { gateway.once('error', reject); gateway.listen(4155, '127.0.0.1', resolve); });
try {
  await run('go', ['build', '-trimpath', '-o', cacheBinary, './cmd/nextgo-cache']);
  await run('go', ['build', '-trimpath', '-o', nextgo, './cmd/nextgo']);
  await startAuthority(); ready = true;
} catch (error) { console.error(error.message); process.exitCode = 1; await shutdown(); }
