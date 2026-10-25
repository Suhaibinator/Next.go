// A single real aggregate binary serves every response. Three loopback
// listeners provide browser-valid authorities; the actual Host is preserved.
// Startup operator overrides never come from a request header or query.
import {spawn} from 'node:child_process';
import {mkdir} from 'node:fs/promises';
import {join} from 'node:path';
import {createServer, request as httpRequest} from 'node:http';

const output = join(process.env.NEXTGO_E2E_ROOT, 'zones');
await mkdir(output, {recursive: true});
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_ZONE_') && !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
const hosts = {marketing: '127.0.0.1:4230', blog: '127.0.0.1:4230', alpha: '127.0.0.1:4234', beta: '127.0.0.1:4235'};
const children = new Map(), servers = [], sockets = new Set(), gates = new Map(), records = [];
let stopping = false, stopPromise, sequence = 0, active = 0;
function own(command, args, options) {
  const child = spawn(command, args, {...options, detached: process.platform !== 'win32'});
  const closed = new Promise(resolve => child.once('close', resolve));
  children.set(child, closed); child.once('close', () => children.delete(child)); return child;
}
function signal(child, name) {try {if (process.platform === 'win32') child.kill(name); else if (child.pid) process.kill(-child.pid, name);} catch (error) {if (error.code !== 'ESRCH') console.error('zones fixture signal:', error.code);}}
function stop(code = 0) {
  if (stopPromise) return stopPromise;
  stopping = true; process.exitCode = code;
  stopPromise = (async () => {
    for (const gate of gates.values()) {gate.incoming?.destroy(); gate.response.destroy();}
    gates.clear(); for (const socket of sockets) socket.destroy();
    const closed = servers.map(server => new Promise(resolve => server.close(resolve))), owned = [...children];
    for (const [child] of owned) signal(child, 'SIGTERM');
    let timer; const finished = Promise.allSettled([...closed, ...owned.map(([, done]) => done)]);
    await Promise.race([finished, new Promise(resolve => {timer = setTimeout(resolve, 2000);})]); clearTimeout(timer);
    for (const [child] of owned) signal(child, 'SIGKILL');
    await Promise.race([finished, new Promise(resolve => {timer = setTimeout(resolve, 500);})]); clearTimeout(timer);
  })(); return stopPromise;
}
for (const name of ['SIGTERM', 'SIGINT']) process.on(name, () => {void stop();});
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = own('go', ['run', './cmd/nextgo', 'zones', 'build', '-out', output, './examples/zones/zones.json'], {stdio: 'inherit', env: base});
  const code = await new Promise((resolve, reject) => {build.once('exit', resolve); build.once('error', reject);});
  if (code !== 0 || stopping) {await stop(1); throw new Error('zones fixture build failed or interrupted');}
}
function json(response, status, value) {if (response.destroyed || response.headersSent) return; response.writeHead(status, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'}); response.end(JSON.stringify(value));}
function release(gate) {
  gate.released = true; if (!gate.held) return;
  gates.delete(gate.key); if (!gate.response.destroyed) {gate.incoming.pipe(gate.response); gate.incoming.resume();}
}
async function control(request, response, url) {
  const operation = url.pathname.slice('/_fixture/zones/'.length), method = operation === 'release' ? 'POST' : 'GET';
  if (!['records', 'gate', 'release'].includes(operation)) {json(response, 404, {}); return;}
  if (request.method !== method) {json(response, 405, {}); return;}
  if (request.headers['content-type'] !== undefined && request.headers['content-type'] !== 'application/json') {json(response, 415, {}); return;}
  if (request.headers['transfer-encoding'] !== undefined || request.headers['content-length'] !== undefined && request.headers['content-length'] !== '0') {json(response, 400, {}); return;}
  try {for await (const chunk of request) {if (chunk.length) {json(response, 400, {}); return;}}} catch {json(response, 400, {}); return;}
  if ([...url.searchParams.keys()].some(key => key !== 'key') || url.searchParams.getAll('key').length > 1) {json(response, 400, {}); return;}
  const key = url.searchParams.get('key') || '';
  if (operation === 'records') {if (key) {json(response, 400, {}); return;} json(response, 200, records); return;}
  if (!/^[a-zA-Z0-9_-]{1,128}$/.test(key)) {json(response, 400, {}); return;}
  const gate = gates.get(key);
  if (operation === 'gate') {json(response, 200, {held: !!gate?.held, id: gate?.record.id}); return;}
  if (!gate) {json(response, 404, {}); return;} release(gate); json(response, 200, {});
}
for (const port of [4230, 4234, 4235]) {
  const expectedHost = `127.0.0.1:${port}`;
  const gateway = createServer((request, response) => {
    const rawURL = request.url;
    if (stopping || request.headers.host !== expectedHost || typeof rawURL !== 'string' || rawURL.length > 4096 || !rawURL.startsWith('/') || rawURL.startsWith('//') || /[\\\s#\x00-\x1f\x7f]/.test(rawURL) || /%(?![0-9a-f]{2})/i.test(rawURL)) {json(response, 400, {}); return;}
    const url = new URL(rawURL, `http://${expectedHost}`);
    if (url.pathname.startsWith('/_fixture/zones/')) {if (port !== 4230) {json(response, 404, {}); return;} void control(request, response, url); return;}
    if (active >= 64) {json(response, 429, {error: 'fixture request capacity'}); return;}
    const trace = request.headers['x-fixture-zones-trace'];
    const record = {id: ++sequence, trace: typeof trace === 'string' ? trace.slice(0, 128) : '', host: expectedHost, method: request.method, path: url.pathname, navigate: request.headers['x-nextgo-navigate'] === '1', prefetch: request.headers['x-nextgo-purpose'] === 'prefetch', document: request.headers['sec-fetch-mode'] === 'navigate', status: 0, complete: false, aborted: false};
    if (records.length === 512) records.shift(); records.push(record);
    const rawKey = request.headers['x-fixture-zones-wasm'];
    const key = url.pathname.endsWith('/app.wasm') && typeof rawKey === 'string' && /^[a-zA-Z0-9_-]{1,128}$/.test(rawKey) ? rawKey : '';
    if (key && (gates.size >= 8 || gates.has(key))) {json(response, 429, {error: 'fixture gate capacity'}); return;}
    const gate = key ? {key, record, response, held: false, released: false} : undefined;
    if (gate) gates.set(key, gate);
    const headers = {...request.headers, 'accept-encoding': 'identity'};
    for (const name of Object.keys(headers)) if (name.startsWith('x-fixture-zones-')) delete headers[name];
    active++;
    const upstream = httpRequest({hostname: '127.0.0.1', port: 4231, path: rawURL, method: request.method, headers}, incoming => {
      record.status = incoming.statusCode; response.writeHead(incoming.statusCode, incoming.headers);
      incoming.on('error', () => response.destroy()); incoming.on('end', () => {record.complete = true;});
      if (!gate || incoming.statusCode !== 200 || !String(incoming.headers['content-type']).startsWith('application/wasm')) {if (gate) gates.delete(key); incoming.pipe(response); return;}
      gate.incoming = incoming; incoming.pause(); response.flushHeaders(); gate.held = true;
      if (gate.released) release(gate);
    });
    upstream.on('error', () => {if (!response.headersSent) response.writeHead(502); response.end();});
    request.on('aborted', () => upstream.destroy());
    response.on('close', () => {active--; if (!response.writableEnded) {record.aborted = true; upstream.destroy(); gate?.incoming?.destroy();} if (gate && gates.get(key) === gate) gates.delete(key);});
    request.pipe(upstream);
  });
  gateway.on('connection', socket => {sockets.add(socket); socket.once('close', () => sockets.delete(socket));});
  gateway.maxConnections = 64; gateway.headersTimeout = 10_000; gateway.requestTimeout = 30_000;
  gateway.once('error', error => {console.error(error); void stop(1);}); servers.push(gateway);
}
const server = own(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4231'], {cwd: output, stdio: 'inherit', env: {...base, NEXTGO_ZONE_HOSTS: JSON.stringify(hosts)}});
server.once('error', error => {console.error(error); void stop(1);}); server.once('exit', code => {if (!stopping) void stop(code || 1);});
for (let index = 0; index < servers.length; index++) servers[index].listen([4230, 4234, 4235][index], '127.0.0.1');
