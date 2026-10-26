// Private qualification overlay. Only the loopback JSON DATA SOURCE is
// constructed here; all application HTML/metadata/frames come from pinned Go.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {constants, createReadStream} from 'node:fs';
import {lstat, mkdtemp, rm} from 'node:fs/promises';
import {createServer, request} from 'node:http';
import {connect} from 'node:net';
import {tmpdir} from 'node:os';
import {isAbsolute, join} from 'node:path';
import {startLambdaGateway} from './lambda-features-gateway.mjs';

export const browserUA = 'Mozilla/5.0 (X11; Linux aarch64) AppleWebKit/537.36 Chrome/140.0.0.0 Safari/537.36';
export const fallbackTitle = 'Loading public metadata — Next.go';
export const finalValue = Object.freeze({version: 1, title: 'Public metadata resolved — Next.go', description: 'Final local deployment metadata.', canonical: 'https://example.com/metadata-stream?phase=final', socialTitle: 'Resolved public metadata', socialDescription: 'One completed Go metadata contribution.'});
export const bounded = (promise, ms = 10_000) => {
  let timer; return Promise.race([promise, new Promise((_, reject) => {timer = setTimeout(() => reject(new Error('Owned fixture observation deadline')), ms);})]).finally(() => clearTimeout(timer));
};
const deferred = () => {let resolve; const promise = new Promise(done => {resolve = done;}); return {promise, resolve};};
async function waitUntil(check, message) {
  const deadline = Date.now() + 6500;
  while (!await check()) {if (Date.now() >= deadline) throw new Error(message); await new Promise(done => setTimeout(done, 10));}
}
async function processAbsent(pid) {
  assert.ok(Number.isInteger(pid) && pid > 1);
  const absent = id => {try {process.kill(id, 0); return false;} catch (error) {if (error.code === 'ESRCH') return true; throw error;}};
  // Both Go spawn paths create a new process group. Signal0 only observes the
  // exact owned PID/group; EPERM is a failure, never evidence of absence.
  assert.notEqual(process.platform, 'win32', 'physical group proof requires the qualified POSIX host/container');
  await waitUntil(() => absent(pid) && absent(-pid), 'Owned native PID/process group did not disappear');
}
async function listenerAbsent(port) {
  const result = await bounded(new Promise(done => {
    const socket = connect({host: '127.0.0.1', port}); let settled = false;
    const finish = value => {if (settled) return; settled = true; socket.destroy(); done(value);};
    socket.once('connect', () => finish('connected')); socket.once('error', error => finish(error.code)); socket.setTimeout(500, () => finish('probe-timeout'));
  }), 1000);
  assert.equal(result, 'ECONNREFUSED', 'Owned listener must be physically closed');
}
export async function artifactPin(binary) {
  assert.ok(isAbsolute(binary || '')); const info = await lstat(binary); assert.ok(info.isFile() && !info.isSymbolicLink() && info.size > 0 && info.size <= 128 << 20);
  const hash = createHash('sha256'); let size = 0;
  for await (const bytes of createReadStream(binary, {flags: constants.O_RDONLY | constants.O_NOFOLLOW})) {size += bytes.length; assert.ok(size <= 128 << 20); hash.update(bytes);}
  assert.equal(size, info.size); return hash.digest('hex');
}
function settingsEnvironment(source, html, mode, timeout, envdir) {
  // A complete trusted environment avoids even reading ambient credential
  // variables. Existing gateway adds ONLY its local Runtime API configuration.
  return {HOME: envdir, TMPDIR: tmpdir(), PATH: '/usr/bin:/bin', GOMAXPROCS: '1', NEXTGO_DEMO_METADATA_SOURCE: source, NEXTGO_DEMO_METADATA_SOURCE_TIMEOUT: '10s', NEXTGO_DEMO_STREAM_TIMEOUT: timeout, NEXTGO_DEMO_STREAM_MAX_STREAMS: '1', NEXTGO_DEMO_STREAM_MAX_LOADERS: '1', NEXTGO_DEMO_METADATA_HTML: html, NEXTGO_DEMO_METADATA_FIXTURE: 'true', NEXTGO_DEMO_METADATA_TEST_MODE: mode};
}

export function observe(origin, path, {method = 'GET', headers = {}} = {}) {
  const completed = deferred(), listeners = new Set(); let chunks = [], size = 0;
  const state = {status: null, headers: {}, text: '', ended: false, error: null, headersReceived: false};
  const update = () => {for (const listener of [...listeners]) listener();};
  const outgoing = request(new URL(path, origin), {method, timeout: 12_000, headers: {'Accept-Encoding': 'identity', ...headers}}, response => {
    state.headersReceived = true; state.status = response.statusCode; state.headers = response.headers; update();
    response.on('data', bytes => {size += bytes.length; if (size > 2 << 20) {response.destroy(new Error('Actual response byte bound')); return;} chunks.push(bytes); state.text = Buffer.concat(chunks, size).toString('utf8'); update();});
    response.once('end', () => {state.ended = true; completed.resolve(state); update();});
    response.once('error', error => {state.error = error.code || error.message; completed.resolve(state); update();});
  });
  outgoing.once('timeout', () => outgoing.destroy(new Error('Actual request deadline'))); outgoing.once('error', error => {state.error = error.code || error.message; completed.resolve(state); update();}); outgoing.end();
  return {state, done: completed.promise, cancel() {outgoing.destroy(new Error('Intentional local client cancellation'));}, waitFor(predicate) {
    if (predicate(state)) return Promise.resolve(state);
    let listener; const promise = new Promise((done, reject) => {listener = () => {if (predicate(state)) done(state); else if (state.error || state.ended) reject(new Error('Actual response settled before expected observation'));}; listeners.add(listener);});
    return bounded(promise).finally(() => listeners.delete(listener));
  }};
}
export async function startMetadataDeployment(transport, {html = true, mode = '', cancelCase = false} = {}) {
  assert.ok(['http', 'buffered', 'streaming'].includes(transport));
  const binary = process.env.NEXTGO_METADATA_DEPLOY_BINARY, expected = process.env.NEXTGO_METADATA_DEPLOY_SHA256;
  assert.match(expected || '', /^[a-f0-9]{64}$/); assert.equal(await artifactPin(binary), expected);
  const envdir = await mkdtemp(join(tmpdir(), 'nextgo-metadata-local-env-')), pending = [], gates = new Set(), sockets = new Set(), sourceRecords = [];
  let worker, native, nativeClosed, sourcePort, logs = '', closed = false;
  const cleanupProof = {nativePIDAndGroupAbsent: false, applicationListenerAbsent: false, sourceListenerAbsent: false, sourceSocketsClosed: false, emptyEnvDirectoryRemoved: false, runtimeGatewayClosed: false};
  const source = createServer((req, res) => {
    const gate = pending.shift();
    if (req.method !== 'GET' || req.url !== '/value' || req.headers.host !== `127.0.0.1:${source.address().port}` || !gate || sourceRecords.length >= 32) {res.writeHead(400).end(); return;}
    const record = {headers: req.headers, entered: true, released: false, finished: false, canceled: false}; sourceRecords.push(record); gate.record = record; gate.entered.resolve(record);
    res.once('close', () => {record.canceled = !res.writableFinished; gate.closed.resolve(record); gates.delete(gate);});
    gate.respond = () => {if (res.destroyed || res.writableEnded) return; record.released = true; res.writeHead(200, {'Content-Type': 'application/json', 'Cache-Control': 'no-store'}); res.end(JSON.stringify(finalValue), () => {record.finished = true;});};
    if (gate.released) gate.respond();
  });
  source.maxConnections = 8; source.maxHeadersCount = 32; source.headersTimeout = 3000;
  source.on('connection', socket => {sockets.add(socket); socket.once('close', () => sockets.delete(socket));});
  const basePath = transport === 'http' ? '' : '/lambda-features';
  const fixture = {origin: 'http://127.0.0.1:4280', path: basePath + '/metadata-stream', sourceRecords, cleanupProof, runtimeProof: () => worker?.snapshot(), arm() {assert.ok(pending.length < 8 && gates.size < 8); const gate = {entered: deferred(), closed: deferred(), released: false, release() {gate.released = true; gate.respond?.();}}; gates.add(gate); pending.push(gate); return gate;}, async health() {
    const actual = observe(fixture.origin, basePath + '/api/health'); const result = await bounded(actual.done);
    if (result.error) {const error = new Error('Actual Go health transport failed'); error.code = result.error; error.beforeHeaders = !result.headersReceived; throw error;}
    assert.equal(result.status, 200); assert.equal(JSON.parse(result.text).framework, 'Next.go'); if (worker) {const report = worker.snapshot().reports.at(-1); assert.equal(report.path, basePath + '/api/health'); assert.equal(report.statusCode, 200); assert.ok(report.httpBytes > 0);} return result;
  }, async close() {
    if (closed) return; closed = true; const failures = [];
    for (const gate of gates) gate.release();
    const ownedPID = worker?.snapshot().childPID || native?.pid;
    try {if (worker) {await worker.close(); const proof = worker.snapshot(); assert.equal(proof.joined, true); assert.equal(proof.exit.code, 0); assert.equal(proof.exit.signal, null); assert.deepEqual(proof.failures, []); assert.equal(proof.droppedReports + proof.droppedFailures, 0); cleanupProof.runtimeGatewayClosed = true;} else if (native) {
      native.kill('SIGTERM'); let result;
      try {result = await bounded(nativeClosed, 6500);} catch (error) {native.kill('SIGKILL'); await bounded(nativeClosed, 1500); throw error;}
      assert.equal(result.code, 0, logs); assert.equal(result.signal, null, logs);
    }} catch (error) {failures.push(error);}
    if (ownedPID) try {await processAbsent(ownedPID); cleanupProof.nativePIDAndGroupAbsent = true;} catch (error) {failures.push(error);}
    for (const socket of sockets) socket.destroy();
    try {await bounded(new Promise(done => source.close(done)), 2500); await waitUntil(() => !source.listening && sockets.size === 0, 'Source sockets did not physically close'); cleanupProof.sourceSocketsClosed = true;} catch (error) {failures.push(error);}
    if (ownedPID) try {await listenerAbsent(4280); cleanupProof.applicationListenerAbsent = true;} catch (error) {failures.push(error);}
    if (sourcePort) try {await listenerAbsent(sourcePort); cleanupProof.sourceListenerAbsent = true;} catch (error) {failures.push(error);}
    try {await rm(envdir, {recursive: true, force: true}); await assert.rejects(lstat(envdir), {code: 'ENOENT'}); cleanupProof.emptyEnvDirectoryRemoved = true;} catch (error) {failures.push(error);}
    try {assert.equal(await artifactPin(binary), expected);} catch (error) {failures.push(error);}
    if (failures.length) throw new AggregateError(failures, 'Owned metadata cleanup failed; never count forced termination as PASS');
  }};
  try {
    await new Promise((done, reject) => {source.once('error', reject); source.listen(0, '127.0.0.1', done);});
    sourcePort = source.address().port;
    const sourceURL = `http://127.0.0.1:${source.address().port}/value`, timeout = cancelCase ? '2s' : '8s';
    if (transport === 'http') {
      const clean = settingsEnvironment(sourceURL, String(html), mode, timeout, envdir);
      native = spawn(binary, ['-addr', '127.0.0.1:4280', '-env-dir', envdir], {cwd: envdir, detached: true, shell: false, stdio: ['ignore', 'pipe', 'pipe'], env: clean});
      native.stdout.on('data', bytes => {logs = (logs + bytes.toString()).slice(-16 << 10);}); native.stderr.on('data', bytes => {logs = (logs + bytes.toString()).slice(-16 << 10);});
      nativeClosed = new Promise(done => {native.once('error', error => done({error: error.code})); native.once('close', (code, signal) => done({code, signal}));});
    } else worker = await startLambdaGateway({binary, mode: transport, port: 4280, workerArgs: ['-env-dir', envdir], trustedEnvironment: settingsEnvironment(sourceURL, String(html), mode, timeout, envdir)});
    // Readiness is the real Go API endpoint, never the gateway ready shortcut.
    const deadline = Date.now() + 10_000;
    for (;;) {try {await fixture.health(); break;} catch (error) {if (!error.beforeHeaders || !['ECONNREFUSED', 'ECONNRESET'].includes(error.code) || Date.now() >= deadline) throw error; await new Promise(done => setTimeout(done, 25));}}
    return fixture;
  } catch (error) {try {await fixture.close();} catch (cleanup) {throw new AggregateError([error, cleanup], 'Metadata startup and cleanup failed');} throw error;}
}
