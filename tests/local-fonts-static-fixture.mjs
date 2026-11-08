import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {join} from 'node:path';
import {staticArtifactPin} from './client-only-deployment-fixture.mjs';
import {bounded} from './metadata-stream-deployment-fixture.mjs';
import {startMetadataStaticServer} from './metadata-static-server.mjs';

// Serve only the pinned published tree, with no Go process or Runtime API.
export async function startLocalFontsStaticDeployment() {
  const root = process.env.NEXTGO_LOCAL_FONTS_STATIC_ROOT, sha256 = process.env.NEXTGO_LOCAL_FONTS_STATIC_SHA256;
  assert.match(sha256 || '', /^[a-f0-9]{64}$/, 'pin the local-font static artifact');
  const pin = await staticArtifactPin(root);
  assert.equal(pin.sha256, sha256, 'local-font static artifact pin mismatch');
  const manifest = JSON.parse(await readFile(join(root, '.nextgo-export.json'), 'utf8'));
  assert.equal(manifest.basePath, '/lambda-features');
  assert.equal(manifest.fonts.length, 3);
  for (const path of ['lambda-features/local-fonts/index.html', 'lambda-features/about/index.html', 'lambda-features/_nextgo/data/local-fonts.json', 'lambda-features/_nextgo/data/about.json', ...manifest.fonts.map(entry => 'lambda-features' + entry.url)]) {
    assert.ok(pin.files.some(file => file.path === path), `missing declared exported file ${path}`);
  }
  const server = await startMetadataStaticServer(root, 4282);
  const cleanupProof = {staticServerClosed: false, artifactUnchanged: false};
  let closing;
  return {origin: 'http://127.0.0.1:4282', artifact: {root, sha256, bytes: pin.bytes, files: pin.files}, sourceRecords: [], cleanupProof,
    close() {
      closing ||= (async () => {
        const closed = new Promise((done, reject) => server.close(error => error ? reject(error) : done()));
        server.closeIdleConnections();
        const timer = setTimeout(() => server.closeAllConnections(), 1000);
        try {await bounded(closed, 3000);} finally {clearTimeout(timer);}
        assert.equal(server.listening, false); cleanupProof.staticServerClosed = true;
        assert.equal((await staticArtifactPin(root)).sha256, sha256, 'static artifact changed during browser test');
        cleanupProof.artifactUnchanged = true;
      })();
      return closing;
    }};
}
