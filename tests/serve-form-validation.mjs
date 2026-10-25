// Forward genuine sample responses. Gates hold original WASM or validation
// bytes; they never synthesize field errors, previews, or application HTML.
import {spawn} from 'node:child_process';
import {mkdir} from 'node:fs/promises';
import {join} from 'node:path';
import {createServer, request as httpRequest} from 'node:http';

const output = join(process.env.NEXTGO_E2E_ROOT, 'form-validation');
await mkdir(output, {recursive: true});
const base = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('NEXTGO_FORM_VALIDATION_') && !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_')));
const children = new Map(), gates = new Map(), records = [], sockets = new Set();
let stopping = false, stopPromise, gateway, sequence = 0, active = 0;
function own(command, args, options) {
  const child = spawn(command, args, {...options, detached: process.platform !== 'win32'});
  const closed = new Promise(resolve => child.once('close', resolve));
  children.set(child, closed); child.once('close', () => children.delete(child));
  return child;
}
function signal(child, name) {
  try {if (process.platform === 'win32') child.kill(name); else if (child.pid) process.kill(-child.pid, name);} catch (error) {if (error.code !== 'ESRCH') console.error('fixture child signal:', error.code);}
}
function stop(code = 0) {
  if (stopPromise) return stopPromise;
  stopping = true; process.exitCode = code;
  stopPromise = (async () => {
    for (const gate of gates.values()) {gate.incoming?.destroy(); gate.response.destroy();}
    gates.clear(); for (const socket of sockets) socket.destroy();
    const serverClosed = gateway ? new Promise(resolve => gateway.close(resolve)) : Promise.resolve();
    const owned = [...children];
    for (const [child] of owned) signal(child, 'SIGTERM');
    let timer;
    const finished = Promise.allSettled([serverClosed, ...owned.map(([, closed]) => closed)]);
    await Promise.race([finished, new Promise(resolve => {timer = setTimeout(resolve, 2000);})]);
    clearTimeout(timer);
    // The group belongs exclusively to this fixture, including go-run's CLI
    // and compiler descendants. A closed parent must not orphan its children.
    for (const [child] of owned) signal(child, 'SIGKILL');
    await Promise.race([finished, new Promise(resolve => {timer = setTimeout(resolve, 500);})]);
    clearTimeout(timer);
  })();
  return stopPromise;
}
for (const signalName of ['SIGTERM', 'SIGINT']) process.on(signalName, () => {void stop();});
if (!process.env.NEXTGO_E2E_SERVER_BINARY) {
  const build = own('go', ['run', './cmd/nextgo', 'build', '-out', output, './examples/form-validation'], {stdio: 'inherit', env: base});
  const code = await new Promise((resolve, reject) => {build.once('exit', resolve); build.once('error', reject);});
  if (code !== 0 || stopping) {await stop(1); throw new Error('form-validation fixture build failed or interrupted');}
}
function json(response, status, value) {if (response.destroyed || response.headersSent) return; response.writeHead(status, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'}); response.end(JSON.stringify(value));}
async function control(request, response, url) {
  const operation = url.pathname.slice('/_fixture/form-validation/'.length);
  const method = operation === 'release' ? 'POST' : 'GET';
  if (!['records', 'gate', 'release'].includes(operation)) {json(response, 404, {}); return;}
  if (request.method !== method) {json(response, 405, {}); return;}
  const contentType = request.headers['content-type'];
  if (contentType !== undefined && contentType !== 'application/json') {json(response, 415, {}); return;}
  if (request.headers['transfer-encoding'] !== undefined || request.headers['content-length'] !== undefined && request.headers['content-length'] !== '0') {json(response, 400, {}); return;}
  // Administrative operations accept no payload, even if a client lies about
  // its headers. No request body is buffered or silently ignored.
  try {for await (const chunk of request) {if (chunk.length) {json(response, 400, {}); return;}}} catch {if (!response.destroyed) json(response, 400, {}); return;}
  if ([...url.searchParams.keys()].some(key => key !== 'key') || url.searchParams.getAll('key').length > 1) {json(response, 400, {}); return;}
  const key = url.searchParams.get('key') || '';
  if (operation !== 'records' && !/^[a-zA-Z0-9_-]{1,128}$/.test(key) || operation === 'records' && key !== '') {json(response, 400, {}); return;}
  if (operation === 'records') {json(response, 200, records); return;}
  const gate = gates.get(key);
  if (operation === 'gate') {json(response, 200, {held: !!gate?.held, id: gate?.record.id, kind: gate?.kind}); return;}
  if (!gate) {json(response, 404, {}); return;}
  release(gate); json(response, 200, {});
}
function release(gate) {
  gate.released = true;
  if (!gate.held) return;
  if (gate.kind === 'wasm') {
    gates.delete(gate.key);
    if (!gate.response.destroyed) {gate.incoming.pipe(gate.response); gate.incoming.resume();}
    return;
  }
  gates.delete(gate.key);
  if (!gate.response.destroyed) {for (const chunk of gate.chunks) gate.response.write(chunk); gate.response.end();}
  gate.chunks = [];
}
gateway = createServer((request, response) => {
  const rawURL = request.url;
  if (stopping || request.headers.host !== '127.0.0.1:4220' || typeof rawURL !== 'string' || rawURL.length > 4096 || !rawURL.startsWith('/') || rawURL.startsWith('//') || /[\\\s#\x00-\x1f\x7f]/.test(rawURL) || /%(?![0-9a-f]{2})/i.test(rawURL)) {json(response, 400, {}); return;}
  const url = new URL(rawURL, 'http://127.0.0.1:4220');
  if (url.pathname.startsWith('/_fixture/form-validation/')) {void control(request, response, url); return;}
  const validation = url.pathname === '/forms-demo/api/validate';
  const preview = url.pathname === '/forms-demo/api/preview';
  const wasmKey = request.headers['x-fixture-form-wasm'];
  const validationKey = request.headers['x-fixture-form-gate'];
  const rawKey = validation ? validationKey : url.pathname.endsWith('/app.wasm') ? wasmKey : undefined;
  const key = typeof rawKey === 'string' && rawKey.length > 0 && rawKey.length <= 128 ? rawKey : '';
  if (key && (gates.size >= 8 || gates.has(key))) {json(response, 429, {error: 'fixture gate capacity'}); return;}
  if ((validation || preview) && active >= 32) {json(response, 429, {error: 'fixture request capacity'}); return;}
  const trace = request.headers['x-fixture-form-trace'];
  const record = {id: ++sequence, trace: typeof trace === 'string' ? trace.slice(0, 128) : '', method: request.method, path: url.pathname, status: 0, complete: false, aborted: false};
  if (validation) {record.field = (url.searchParams.get('field') || '').slice(0, 16); record.value = (url.searchParams.get('value') || '').slice(0, 128);}
  if (validation || preview || key) {if (records.length === 256) records.shift(); records.push(record);}
  const gate = key ? {key, record, response, kind: validation ? 'validation' : 'wasm', held: false, released: false, chunks: [], bytes: 0} : undefined;
  if (gate) gates.set(key, gate);
  const headers = {...request.headers, host: '127.0.0.1:4220', 'accept-encoding': 'identity'};
  for (const name of Object.keys(headers)) if (name.startsWith('x-fixture-form-')) delete headers[name];
  if (validation || preview) active++;
  const upstream = httpRequest({hostname: '127.0.0.1', port: 4221, path: request.url, method: request.method, headers}, incoming => {
    record.status = incoming.statusCode;
    response.writeHead(incoming.statusCode, incoming.headers);
    incoming.on('error', () => response.destroy());
    incoming.on('end', () => {record.complete = true;});
    if (!gate) {incoming.pipe(response); return;}
    gate.incoming = incoming;
    response.flushHeaders();
    if (gate.kind === 'wasm') {incoming.pause(); gate.held = true; if (gate.released) release(gate); return;}
    incoming.on('data', chunk => {
      gate.bytes += chunk.length;
      if (gate.bytes > 8192) {gate.chunks = []; incoming.destroy(); response.destroy(); return;}
      gate.chunks.push(chunk);
    });
    incoming.on('end', () => {gate.held = true; if (gate.released) release(gate);});
  });
  upstream.on('error', () => {if (!response.headersSent) response.writeHead(502); response.end();});
  request.on('aborted', () => upstream.destroy());
  response.on('close', () => {
    if (validation || preview) active--;
    if (!response.writableEnded) {record.aborted = true; upstream.destroy(); gate?.incoming?.destroy();}
    if (gate) {gate.chunks = []; if (gates.get(key) === gate) gates.delete(key);}
  });
  request.pipe(upstream);
});
gateway.on('connection', socket => {sockets.add(socket); socket.once('close', () => sockets.delete(socket));});
const server = own(process.env.NEXTGO_E2E_SERVER_BINARY || join(output, 'server'), ['-addr', '127.0.0.1:4221'], {cwd: output, stdio: 'inherit', env: {...base, NEXTGO_FORM_VALIDATION_BASE_PATH: '/forms-demo', NEXTGO_FORM_VALIDATION_ASSET_PREFIX: '/form-assets'}});
server.once('error', error => {console.error(error); stop(1);});
server.once('exit', code => {if (!stopping) stop(code || 1);});
gateway.once('error', error => {console.error(error); stop(1);});
gateway.listen(4220, '127.0.0.1');
