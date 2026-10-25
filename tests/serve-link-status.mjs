// A qualification fixture starts an already built, hash-pinned executable only.
// There is no Go command, source-build fallback, or inherited production helper.
import {spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {createReadStream} from 'node:fs';
import {lstat, mkdtemp, rm, writeFile} from 'node:fs/promises';
import {isAbsolute, join, relative, sep} from 'node:path';
import {tmpdir} from 'node:os';
import {fileURLToPath} from 'node:url';
import {readerToken, adminToken} from './access-fixture.mjs';

const root = fileURLToPath(new URL('..', import.meta.url));
const binary = process.env.NEXTGO_E2E_SERVER_BINARY;
const expected = process.env.NEXTGO_LINK_STATUS_SERVER_SHA256;
const proofPath = process.env.NEXTGO_LINK_STATUS_PROCESS_PROOF;
if (!binary || !isAbsolute(binary) || !/^[a-f0-9]{64}$/.test(expected || '') || !proofPath || !isAbsolute(proofPath)) throw new Error('link-status requires absolute prebuilt binary/proof paths and its SHA256');
const outside = path => { const rel = relative(root, path); return rel === '..' || rel.startsWith('..' + sep) || isAbsolute(rel); };
if (!outside(binary) || !outside(proofPath)) throw new Error('link-status artifact/proof must be outside frozen source');
const before = await lstat(binary);
if (!before.isFile() || before.size > 128 * 1024 * 1024 || !(before.mode & 0o111)) throw new Error('link-status requires a bounded regular executable artifact');
const hash = createHash('sha256');
let hashedBytes = 0;
for await (const chunk of createReadStream(binary)) { hashedBytes += chunk.length; if (hashedBytes > before.size) throw new Error('link-status artifact grew during hashing'); hash.update(chunk); }
if (hashedBytes !== before.size) throw new Error('link-status artifact truncated during hashing');
if (hash.digest('hex') !== expected) throw new Error('link-status prebuilt binary digest mismatch');
const after = await lstat(binary);
if (before.ino !== after.ino || before.dev !== after.dev || before.size !== after.size || before.mtimeMs !== after.mtimeMs) throw new Error('link-status artifact changed during hash verification');
const cwd = await mkdtemp(join(tmpdir(), 'nextgo-link-status-server-'));
const proof = {binary, sha256: expected, bytes: before.size, cwd, pid: null, spawned: false, closed: false, code: null, signal: null, termSent: false, killSent: false, joined: false, cwdRemoved: false, stopping: false, failure: ''};
let child, closed, stopPromise, requestedStop;
const stopRequest = new Promise(resolve => { requestedStop = resolve; });
function signal(name) {
  if (!child || !child.pid || proof.closed) return;
  if (name === 'SIGTERM') proof.termSent = true; else proof.killSent = true;
  try { if (process.platform === 'win32') child.kill(name); else process.kill(-child.pid, name); }
  catch (error) { if (error.code !== 'ESRCH') throw error; }
}
async function within(promise, ms) {
  let timer;
  try { return await Promise.race([promise.then(() => true), new Promise(resolve => { timer = setTimeout(() => resolve(false), ms); })]); }
  finally { clearTimeout(timer); }
}
function stop() {
  if (stopPromise) return stopPromise;
  stopPromise = (async () => {
    proof.stopping = true;
    signal('SIGTERM');
    if (closed && !await within(closed, 2500)) {
      signal('SIGKILL');
      if (!await within(closed, 1000)) {
        proof.failure ||= 'native child did not close after bounded TERM/KILL';
        child.unref();
        throw new Error(proof.failure);
      }
    }
    proof.joined = !child || proof.closed;
  })();
  return stopPromise;
}
const onSignal = () => requestedStop();
process.on('SIGTERM', onSignal); process.on('SIGINT', onSignal);
try {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_')));
  child = spawn(binary, ['-addr', '127.0.0.1:4100'], {cwd, stdio: 'inherit', detached: process.platform !== 'win32', env: {...env, NEXTGO_DEMO_READER_TOKEN: readerToken, NEXTGO_DEMO_ADMIN_TOKEN: adminToken}});
  proof.pid = child.pid || null; proof.spawned = !!child.pid;
  closed = new Promise(resolve => child.once('close', (code, name) => { proof.closed = true; proof.code = code; proof.signal = name; resolve(); }));
  child.once('error', error => { proof.failure = `native spawn failed: ${error.message}`; requestedStop(); });
  await Promise.race([closed, stopRequest]);
  if (proof.closed && !proof.stopping) { proof.failure ||= `native server exited unexpectedly (${proof.code}/${proof.signal})`; process.exitCode = 1; }
} catch (error) {
  proof.failure ||= String(error); process.exitCode = 1;
} finally {
  try { await stop(); } catch (error) { proof.failure ||= String(error); process.exitCode = 1; }
  process.off('SIGTERM', onSignal); process.off('SIGINT', onSignal);
  if (proof.joined) { await rm(cwd, {recursive: true, force: true}); proof.cwdRemoved = true; }
  await writeFile(proofPath, JSON.stringify(proof, null, 2) + '\n', {flag: 'wx'});
  if (!proof.joined || proof.failure) process.exitCode = 1;
}
