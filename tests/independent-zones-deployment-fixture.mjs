// Prebuilt operator artifacts only. No Go compiler, application source loading,
// external origin, or synthesized successful application response.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {constants, createReadStream} from 'node:fs';
import {lstat, open, realpath} from 'node:fs/promises';
import {createServer, request as httpRequest} from 'node:http';
import {dirname, join, resolve} from 'node:path';
import {gunzipSync} from 'node:zlib';
import {startLambdaGateway} from './lambda-features-gateway.mjs';

export const transports = ['http', 'buffered', 'streaming'];
export const identityHeaders = ['x-nextgo-zone-protocol', 'x-nextgo-zone-id', 'x-nextgo-zone-release', 'x-nextgo-zone-topology'];
const maxResponse = 32 << 20;
export async function shaFile(path) {
  const info = await lstat(path); assert.ok(info.isFile() && !info.isSymbolicLink() && info.size <= 256 << 20, 'bounded regular native artifact required');
  const hash = createHash('sha256'); let used = 0;
  for await (const bytes of createReadStream(path, {flags: constants.O_RDONLY | constants.O_NOFOLLOW})) {used += bytes.length; assert.ok(used <= 256 << 20); hash.update(bytes);}
  assert.equal(used, info.size, 'artifact changed while hashing'); return hash.digest('hex');
}
export function loopbackOrigin(value) {
  const url = new URL(value);
  assert.equal(url.protocol, 'http:'); assert.equal(url.hostname, '127.0.0.1'); assert.ok(url.port && +url.port > 0 && +url.port <= 65535);
  assert.equal(url.username, ''); assert.equal(url.password, ''); assert.equal(url.pathname, '/'); assert.equal(url.search, ''); assert.equal(url.hash, '');
  assert.equal(url.origin, value); return url;
}
async function jsonFile(path, maxBytes = 1 << 20) {
  const info = await lstat(path); assert.ok(info.isFile() && !info.isSymbolicLink() && info.size <= maxBytes, 'bounded regular operator JSON required');
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {const bytes = Buffer.alloc(maxBytes + 1); let used = 0; while (used < bytes.length) {const read = await file.read(bytes, used, bytes.length - used, used); if (!read.bytesRead) break; used += read.bytesRead;} assert.ok(used <= maxBytes && used === info.size, 'operator JSON changed or exceeds byte bound'); return JSON.parse(bytes.subarray(0, used).toString('utf8'));}
  finally {await file.close();}
}
export async function readIndependentArtifacts(filename) {
  const file = resolve(filename), input = await jsonFile(file, 64 << 10); assert.equal(input.version, 1);
  assert.ok(input.binarySHA256 && typeof input.binarySHA256 === 'object' && !Array.isArray(input.binarySHA256), 'operator index must pin every native executable');
  const pin = key => {assert.match(input.binarySHA256[key], /^[a-f0-9]{64}$/, `missing native pin for ${key}`); return input.binarySHA256[key];};
  const root = dirname(file), result = {index: file, indexSHA256: await shaFile(file), releases: {}, ingresses: {}};
  for (const [key, releaseID, zone] of [['releaseA1', 'A1', 'alpha'], ['releaseA2', 'A2', 'alpha'], ['releaseB1', 'B1', 'blog']]) {
    assert.equal(typeof input[key], 'string'); const directory = await realpath(resolve(root, input[key])), binary = join(directory, 'server');
    const manifest = await jsonFile(join(directory, 'zone-release.json')); assert.equal(manifest.release.version, 1); assert.equal(manifest.release.releaseID, releaseID); assert.equal(manifest.release.zone.id, zone);
    assert.match(manifest.binarySHA256, /^[a-f0-9]{64}$/); const binarySHA256 = await shaFile(binary); assert.equal(binarySHA256, manifest.binarySHA256, 'actual release binary matches its published artifact'); assert.equal(binarySHA256, pin(key), 'actual release matches operator native pin');
    result.releases[releaseID] = {directory, binary, binarySHA256, manifest, spec: manifest.release};
  }
  result.publicOrigin = result.releases.A1.spec.publicOrigin; result.publicHost = new URL(result.publicOrigin).host;
  assert.equal(result.publicOrigin, 'http://127.0.0.1:4260');
  for (const release of Object.values(result.releases)) {assert.equal(release.spec.publicOrigin, result.publicOrigin); assert.equal(release.spec.zone.host, result.publicHost); assert.equal(release.spec.zone.basePath, release.spec.zone.id === 'blog' ? '/blog' : ''); assert.equal(release.spec.topologySHA256, result.releases.A1.spec.topologySHA256);}
  assert.notEqual(result.releases.A1.spec.runtimeID, result.releases.A2.spec.runtimeID);
  for (const [key, name] of [['ingressI1', 'I1'], ['ingressI2', 'I2'], ['ingressRollback', 'rollback'], ['ingressHTTPSDenial', 'httpsDenial']]) {
    assert.equal(typeof input[key], 'string', `provide prebuilt ${key}`); const directory = await realpath(resolve(root, input[key]));
    const manifest = await jsonFile(join(directory, 'zone-ingress.json')); assert.equal(manifest.version, 1); assert.equal(manifest.topologySHA256, result.releases.A1.spec.topologySHA256);
    const binarySHA256 = await shaFile(join(directory, 'server')); assert.equal(binarySHA256, pin(key), 'actual ingress matches operator native pin');
    result.ingresses[name] = {directory, binary: join(directory, 'server'), binarySHA256, manifest};
  }
  result.origins = input.origins || {A1: 'http://127.0.0.1:4261', A2: 'http://127.0.0.1:4262', B1: 'http://127.0.0.1:4263'};
  assert.deepEqual(Object.keys(result.origins).sort(), ['A1', 'A2', 'B1']); for (const origin of Object.values(result.origins)) loopbackOrigin(origin);
  result.relayOrigin = input.relayOrigin || 'http://127.0.0.1:4264'; loopbackOrigin(result.relayOrigin);
  assert.equal(new Set([...Object.values(result.origins), result.relayOrigin]).size, 4);
  const normalize = value => ({...value, zones: [...value.zones].sort((a, b) => a.binding.id.localeCompare(b.binding.id)).map(zone => ({...zone, releases: [...zone.releases].sort((a, b) => a.artifact.release.releaseID.localeCompare(b.artifact.release.releaseID))}))});
  for (const [name, ingress] of Object.entries(result.ingresses)) {
    const artifact = id => {
      const value = structuredClone(result.releases[id].manifest);
      if (name === 'httpsDenial') value.release.publicOrigin = 'https://127.0.0.1:4260';
      return value;
    };
    const retained = name === 'I1' || name === 'httpsDenial' ? ['A1'] : ['A1', 'A2'];
    const expected = {version: 1, topologySHA256: result.releases.A1.spec.topologySHA256, zones: [
      {binding: result.releases.A1.spec.zone, activeRelease: name === 'I2' ? 'A2' : 'A1', releases: retained.map(id => ({artifact: artifact(id), origin: id === 'A2' ? result.relayOrigin : result.origins[id]}))},
      {binding: result.releases.B1.spec.zone, activeRelease: 'B1', releases: [{artifact: artifact('B1'), origin: result.origins.B1}]},
    ]};
    assert.deepEqual(normalize(ingress.manifest), normalize(expected), 'published ingress snapshot must bind the pinned releases and actual fixture origins');
  }
  return result;
}
export function expectation(release) {
  return {'X-Nextgo-Zone-Protocol': '1', 'X-Nextgo-Zone-ID': release.spec.zone.id, 'X-Nextgo-Zone-Release': release.spec.releaseID, 'X-Nextgo-Zone-Topology': release.spec.topologySHA256};
}
export function wireRequest(origin, path, {method = 'GET', headers = {}, body = ''} = {}) {
  return new Promise((done, reject) => {
    const chunks = [], observations = []; let size = 0; const began = Date.now();
    const outgoing = httpRequest(new URL(path, origin), {method, headers: {'Accept-Encoding': 'gzip', ...headers}, timeout: 20_000}, response => {
      response.on('data', bytes => {size += bytes.length; if (size > maxResponse) response.destroy(new Error('bounded release fixture response')); else {chunks.push(bytes); observations.push({atMs: Date.now() - began, end: size});}});
      response.once('error', reject); response.once('end', () => {try {
        let data = Buffer.concat(chunks, size); if (data.length && response.headers['content-encoding'] === 'gzip') data = gunzipSync(data, {maxOutputLength: maxResponse});
        done({status: response.statusCode, headers: response.headers, body: data, text: data.toString('utf8'), observations});
      } catch (error) {reject(error);}});
    });
    outgoing.once('error', reject); outgoing.once('timeout', () => outgoing.destroy(new Error('bounded release fixture request timeout'))); outgoing.end(body);
  });
}
function cleanEnvironment() {return Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_ZONE_') && key !== 'NEXTGO_ENV_DIR'));}
export async function startIndependentWorker(binary, mode, port, {backendHosts, allowedHosts} = {}) {
  assert.ok(transports.includes(mode)); const args = backendHosts ? ['-backend-hosts', backendHosts.join(',')] : [];
  if (mode !== 'http') return startLambdaGateway({binary, mode, port, workerArgs: args, allowedHosts});
  const child = spawn(binary, ['-addr', `127.0.0.1:${port}`, ...args], {cwd: '/', detached: process.platform !== 'win32', env: cleanEnvironment(), stdio: ['ignore', 'ignore', 'pipe']});
  let logs = ''; child.stderr.on('data', bytes => {logs = (logs + bytes.toString()).slice(-8192);});
  const exited = new Promise(done => {child.once('error', error => done({error})); child.once('exit', (code, signal) => done({code, signal}));});
  return {close: async () => {
    child.kill('SIGTERM'); let timer;
    const result = await Promise.race([exited, new Promise(done => {timer = setTimeout(() => {child.kill('SIGKILL'); done({timeout: true});}, 6500);})]); clearTimeout(timer); if (result.timeout) await exited;
    assert.equal(result.timeout, undefined, logs); assert.equal(result.error, undefined, logs); assert.equal(result.code, 0, logs);
  }};
}

// The operator controls this relay through a private in-process object. There
// is no HTTP control route, request-selected backend, or fabricated Go success.
export async function startIndependentRelay(artifacts) {
  const address = loopbackOrigin(artifacts.relayOrigin), sockets = new Set(), records = [];
  let mode = 'forward', target = 'A2';
  const server = createServer(async (incoming, outgoing) => {
    if (typeof incoming.url !== 'string' || incoming.url.length > 8192 || !incoming.url.startsWith('/') || incoming.url.startsWith('//') || /[\\\s\x00-\x1f\x7f]/.test(incoming.url)) {outgoing.writeHead(400); outgoing.end(); return;}
    const selected = {mode, target}, record = {method: incoming.method, path: incoming.url, target, mode, upstreamStatus: null, originalComplete: false}; records.push(record); if (records.length > 256) records.shift();
    if (selected.mode === 'before') {incoming.socket.destroy(); return;}
    const bytes = []; let size = 0;
    try {
      for await (const chunk of incoming) {size += chunk.length; assert.ok(size <= 64 << 10); bytes.push(chunk);}
      const destination = loopbackOrigin(artifacts.origins[selected.target]);
      const headers = {...incoming.headers, host: selected.mode === 'wrongHost' ? 'unowned-backend.example.test' : destination.host};
      delete headers.connection; delete headers['transfer-encoding'];
      const request = httpRequest(new URL(incoming.url, destination), {method: incoming.method, headers, timeout: 15_000}, response => {
        record.upstreamStatus = response.statusCode;
        if (selected.mode === 'after') {
          const chunks = []; let used = 0;
          response.on('data', bytes => {used += bytes.length; if (used > 64 << 10) response.destroy(new Error('bounded original action response')); else chunks.push(bytes);});
          response.once('end', () => {try {let original = Buffer.concat(chunks, used); record.originalWireSHA256 = createHash('sha256').update(original).digest('hex'); if (response.headers['content-encoding'] === 'gzip') original = gunzipSync(original, {maxOutputLength: 128 << 10}); record.originalComplete = true; record.originalHeaders = response.headers; record.originalBody = original.toString('utf8');} catch {record.originalError = 'Original response decoding failed';} finally {outgoing.destroy();}});
          response.once('error', () => outgoing.destroy());
        } else {
          outgoing.writeHead(response.statusCode, response.headers); response.pipe(outgoing); response.once('end', () => {record.originalComplete = true;}); response.once('error', () => outgoing.destroy());
        }
      });
      request.once('timeout', () => request.destroy(new Error('bounded relay timeout'))); request.once('error', () => outgoing.destroy()); incoming.once('aborted', () => request.destroy()); request.end(Buffer.concat(bytes, size));
    } catch {outgoing.destroy();}
  });
  server.maxConnections = 16; server.maxHeadersCount = 64; server.headersTimeout = 5000;
  server.on('connection', socket => {sockets.add(socket); socket.once('close', () => sockets.delete(socket));});
  await new Promise((done, reject) => {server.once('error', reject); server.listen(+address.port, '127.0.0.1', done);});
  return {records, configure(nextMode, nextTarget = 'A2') {assert.ok(['forward', 'before', 'after', 'wrongHost'].includes(nextMode)); assert.ok(['A1', 'A2'].includes(nextTarget)); mode = nextMode; target = nextTarget;}, close: async () => {for (const socket of sockets) socket.destroy(); await new Promise(done => server.close(done));}};
}
