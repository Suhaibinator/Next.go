// Prebuilt artifacts only. The dynamic modes use the existing actual Go HTTP /
// Runtime API fixture; static mode serves files without an application process.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {lstat, readdir, realpath} from 'node:fs/promises';
import {isAbsolute, join, relative} from 'node:path';
import {artifactPin, bounded, startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';
import {startMetadataStaticServer} from './metadata-static-server.mjs';

export const deploymentModes = ['http', 'buffered', 'streaming', 'static'];
export async function staticArtifactPin(root) {
  assert.ok(isAbsolute(root || ''), 'static artifact requires an absolute directory');
  assert.equal(await realpath(root), root, 'static artifact root must be canonical');
  const files = []; let bytes = 0, entries = 0;
  async function walk(directory) {
    for (const name of (await readdir(directory)).sort()) {
      assert.ok(++entries <= 1024, 'static artifact entry bound');
      const path = join(directory, name), info = await lstat(path);
      assert.equal(info.isSymbolicLink(), false, 'static artifact cannot contain symlinks');
      if (info.isDirectory()) {await walk(path); continue;}
      assert.ok(info.isFile(), 'static artifact requires regular files');
      bytes += info.size; assert.ok(bytes <= 128 << 20, 'static artifact byte bound');
      files.push({path: relative(root, path), bytes: info.size, sha256: await artifactPin(path)});
    }
  }
  await walk(root);
  assert.ok(files.length > 0, 'static artifact cannot be empty');
  return {sha256: createHash('sha256').update(JSON.stringify(files)).digest('hex'), bytes, files};
}

export async function startClientOnlyDeployment(mode) {
  assert.ok(deploymentModes.includes(mode), 'unknown client-only deployment mode');
  if (mode !== 'static') {
    const binary = process.env.NEXTGO_E2E_SERVER_BINARY, sha256 = process.env.NEXTGO_METADATA_STREAM_SERVER_SHA256;
    const fixture = await startMetadataDeployment(mode, {binary, sha256, port: 4281});
    return {...fixture, mode, path: (mode === 'http' ? '' : '/lambda-features') + '/client-only',
      snapshot: () => ({mode, binary, sha256, source: fixture.sourceRecords, cleanup: {...fixture.cleanupProof}, runtime: fixture.runtimeProof() ?? null})};
  }
  const root = process.env.NEXTGO_CLIENT_ONLY_STATIC_ROOT, expected = process.env.NEXTGO_CLIENT_ONLY_STATIC_SHA256;
  assert.match(expected || '', /^[a-f0-9]{64}$/, 'pin the exported static tree');
  const pin = await staticArtifactPin(root); assert.equal(pin.sha256, expected, 'static artifact pin mismatch');
  for (const path of ['lambda-features/client-only/index.html', 'lambda-features/about/index.html', 'lambda-features/_nextgo/data/client-only.json', 'lambda-features/_nextgo/data/about.json']) {
    assert.ok(pin.files.some(file => file.path === path), `missing exported path ${path}`);
  }
  const server = await startMetadataStaticServer(root, 4281);
  const proof = {mode, root, sha256: expected, bytes: pin.bytes, files: pin.files, closed: false};
  let closing;
  return {mode, origin: 'http://127.0.0.1:4281', path: '/lambda-features/client-only', snapshot: () => structuredClone(proof), close() {
    closing ||= (async () => {
      const closed = new Promise((done, reject) => server.close(error => error ? reject(error) : done()));
      server.closeIdleConnections();
      const timer = setTimeout(() => server.closeAllConnections(), 1000);
      try {await bounded(closed, 3000);} finally {clearTimeout(timer);}
      assert.equal(server.listening, false); proof.closed = true;
      assert.equal((await staticArtifactPin(root)).sha256, expected, 'static artifact changed during browser test');
    })();
    return closing;
  }};
}
