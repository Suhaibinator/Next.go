import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {join} from 'node:path';
import {staticArtifactPin} from './client-only-deployment-fixture.mjs';
import {bounded} from './metadata-stream-deployment-fixture.mjs';
import {startMetadataStaticServer} from './metadata-static-server.mjs';

export async function startSocialMetadataStaticDeployment({root = process.env.NEXTGO_SOCIAL_METADATA_STATIC_ROOT, sha256 = process.env.NEXTGO_SOCIAL_METADATA_STATIC_SHA256, port = 4283, metadataCount = 10,
  pages = ['lambda-features/index.html','lambda-features/about/index.html','lambda-features/override/index.html','lambda-features/disabled/index.html']} = {}) {
  assert.match(sha256 || '', /^[a-f0-9]{64}$/);
  const pin = await staticArtifactPin(root); assert.equal(pin.sha256, sha256);
  const manifest = JSON.parse(await readFile(join(root, '.nextgo-export.json'), 'utf8'));
  assert.equal(manifest.basePath, '/lambda-features'); assert.equal((manifest.metadata ?? []).length, metadataCount);
  for (const file of [...pages, ...(manifest.metadata ?? []).map(entry => entry.path.slice(1))]) {
    assert.ok(pin.files.some(entry => entry.path === file), `missing published file ${file}`);
  }
  const server = await startMetadataStaticServer(root, port), cleanupProof = {staticServerClosed: false, artifactUnchanged: false};
  let closing;
  return {origin: `http://127.0.0.1:${port}`, artifact: {root, sha256, files: pin.files, bytes: pin.bytes}, cleanupProof, sourceRecords: [],
    close() {
      closing ||= (async () => {
        const closed = new Promise((done, reject) => server.close(error => error ? reject(error) : done()));
        server.closeIdleConnections(); const timer = setTimeout(() => server.closeAllConnections(), 1000);
        try {await bounded(closed, 3000);} finally {clearTimeout(timer);}
        assert.equal(server.listening, false); cleanupProof.staticServerClosed = true;
        assert.equal((await staticArtifactPin(root)).sha256, sha256); cleanupProof.artifactUnchanged = true;
      })(); return closing;
    }};
}
