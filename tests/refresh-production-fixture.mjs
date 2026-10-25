// Prebuilt artifacts only. No Go compiler, cloud service, or response synthesis.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {createReadStream} from 'node:fs';
import {request as httpRequest} from 'node:http';
import {resolve} from 'node:path';
import {gunzipSync} from 'node:zlib';
import {startLambdaGateway} from './lambda-features-gateway.mjs';
import {startMetadataStaticServer} from './metadata-static-server.mjs';

export const base = '/refresh', assets = '/refresh-assets', note = 'Production fixture public note';
export const modes = ['http', 'buffered', 'streaming', 'static'];
export const originFor = mode => `http://127.0.0.1:${mode === 'static' ? 4243 : 4242}`;
export async function fileDigest(path) {const hash = createHash('sha256'); for await (const bytes of createReadStream(path)) hash.update(bytes); return hash.digest('hex');}
export function request(origin, path, {method = 'GET', headers = {}} = {}) {
  return new Promise((done, reject) => {
    const outgoing = httpRequest(new URL(path, origin), {method, headers: {'Accept-Encoding': 'gzip', ...headers}, timeout: 15_000}, response => {
      const chunks = []; let size = 0;
      response.on('data', bytes => {size += bytes.length; if (size > 16 << 20) response.destroy(new Error('refresh fixture response bound')); else chunks.push(bytes);});
      response.once('error', reject);
      response.once('end', () => {try {
        let body = Buffer.concat(chunks, size);
        if (body.length && response.headers['content-encoding'] === 'gzip') body = gunzipSync(body, {maxOutputLength: 16 << 20});
        done({status: response.statusCode, headers: response.headers, body, text: body.toString('utf8')});
      } catch (error) {reject(error);}});
    });
    outgoing.once('error', reject); outgoing.once('timeout', () => outgoing.destroy(new Error('refresh fixture request timeout'))); outgoing.end();
  });
}
function environment() {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('NEXTGO_REFRESH_') && key !== 'NEXTGO_ENV_DIR'));
  return {...env, NEXTGO_REFRESH_BASE_PATH: base, NEXTGO_REFRESH_ASSET_PREFIX: assets, NEXTGO_REFRESH_PUBLIC_NOTE: note};
}
export async function startRefreshProduction(mode) {
  assert.ok(modes.includes(mode), 'unknown prebuilt refresh transport');
  const origin = originFor(mode);
  if (mode === 'static') {
    assert.ok(process.env.NEXTGO_REFRESH_STATIC_ROOT, 'set NEXTGO_REFRESH_STATIC_ROOT to the completed mounted export; this fixture never builds');
    const root = resolve(process.env.NEXTGO_REFRESH_STATIC_ROOT), server = await startMetadataStaticServer(root, 4243);
    return {origin, proof: {mode, root}, close: async () => {
      server.closeIdleConnections(); const closed = new Promise(done => server.close(done));
      const timer = setTimeout(() => server.closeAllConnections(), 1000); try {await closed;} finally {clearTimeout(timer);}
    }};
  }
  const selected = process.env.NEXTGO_REFRESH_SERVER_BINARY || process.env.NEXTGO_E2E_SERVER_BINARY;
  assert.ok(selected, 'set NEXTGO_REFRESH_SERVER_BINARY to the production refresh server; this fixture never builds');
  const binary = resolve(selected), proof = {mode, binary, sha256: await fileDigest(binary)};
  if (mode !== 'http') {
    const values = environment(), keys = [...new Set([...Object.keys(process.env).filter(key => key.startsWith('NEXTGO_REFRESH_')), 'NEXTGO_REFRESH_BASE_PATH', 'NEXTGO_REFRESH_ASSET_PREFIX', 'NEXTGO_REFRESH_PUBLIC_NOTE'])];
    const previous = keys.map(key => process.env[key]);
    for (const key of keys) delete process.env[key];
    for (const key of ['NEXTGO_REFRESH_BASE_PATH', 'NEXTGO_REFRESH_ASSET_PREFIX', 'NEXTGO_REFRESH_PUBLIC_NOTE']) process.env[key] = values[key];
    try {const worker = await startLambdaGateway({binary, mode, port: 4242}); return {origin, proof, close: () => worker.close()};}
    finally {for (let i = 0; i < keys.length; i++) if (previous[i] === undefined) delete process.env[keys[i]]; else process.env[keys[i]] = previous[i];}
  }
  const child = spawn(binary, ['-addr', '127.0.0.1:4242'], {cwd: '/', detached: process.platform !== 'win32', env: environment(), stdio: ['ignore', 'ignore', 'pipe']});
  let logs = ''; child.stderr.on('data', bytes => {logs = (logs + bytes.toString()).slice(-8192);});
  const exited = new Promise(done => {child.once('error', error => done({error})); child.once('exit', (code, signal) => done({code, signal}));});
  async function close() {
    child.kill('SIGTERM'); let timer;
    const result = await Promise.race([exited, new Promise(done => {timer = setTimeout(() => {child.kill('SIGKILL'); done({timeout: true});}, 6500);})]);
    clearTimeout(timer); if (result.timeout) await exited;
    assert.equal(result.timeout, undefined, logs); assert.equal(result.error, undefined, logs); assert.equal(result.code, 0, logs);
  }
  try {
    for (let n = 0; n < 100; n++) {
      try {if ((await request(origin, `${base}/api/status`)).status === 200) return {origin, proof, close};} catch {}
      await new Promise(done => setTimeout(done, 50));
    }
    throw new Error(`prebuilt refresh server startup failed: ${logs}`);
  } catch (error) {await close(); throw error;}
}
