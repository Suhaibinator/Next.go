// Bounded LOCAL observations, with actual pinned Go processes and owned cleanup.
// No timing threshold, provider/cold-cache claim or cross-SDK speed comparison.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {request} from 'node:http';
import {performance} from 'node:perf_hooks';
import {artifactPin, bounded, startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';

const targets = JSON.parse(process.env.NEXTGO_SOCIAL_OBSERVATION_TARGETS || '[]');
assert.ok(Array.isArray(targets) && targets.length > 0 && targets.length <= 2, 'supply one or two pinned SDK targets');
const output = {format: 'nextgo-social-metadata-local-observations-v1', targets: [],
  scope: 'Fresh Go processes on warm local filesystem/cache; ordinary loopback HTTP only; no browser, provider or isolated CPU/load conditions.',
  startWindow: 'Before fixture construction to real Go health response; includes artifact SHA-256 reads, temporary directory/source-listener setup, process spawn, startup PNG production/validation and readiness polling.',
  requestWindow: 'Client request creation to complete identity PNG body over a new TCP connection (agent:false); includes Node/network/Go response work.',
  limitations: ['Five processes and 25 PNG requests per SDK are descriptive observations, not tail/load statistics.', 'No matched no-producer control isolates PNG startup cost.', 'No cold filesystem, RSS/CPU/allocation/CLS measurement or hosted serverless qualification.', 'No performance release threshold or SDK speed comparison is inferred.']};

async function png(origin) {
  const started = performance.now();
  return bounded(new Promise((done, reject) => {
    const outgoing = request(origin + '/opengraph-image.png', {agent: false, headers: {'Accept-Encoding': 'identity'}, timeout: 5000}, response => {
      const chunks = []; let length = 0;
      response.on('data', data => {length += data.length; if (length > 1 << 20) response.destroy(new Error('PNG observation byte bound')); else chunks.push(data);});
      response.once('error', reject);
      response.once('end', () => {
        try {
          const data = Buffer.concat(chunks, length), hash = createHash('sha256').update(data).digest('hex');
          assert.equal(response.statusCode, 200); assert.equal(response.headers['content-type'], 'image/png');
          assert.equal(response.headers.etag, '"' + hash + '"'); assert.equal(data.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
          assert.equal(data.readUInt32BE(16), 1200); assert.equal(data.readUInt32BE(20), 630);
          done({milliseconds: performance.now() - started, bytes: length, sha256: hash});
        } catch (error) {reject(error);}
      });
    });
    outgoing.once('timeout', () => outgoing.destroy(new Error('PNG observation request deadline')));
    outgoing.once('error', reject); outgoing.end();
  }), 6000);
}
function range(values) {
  const sorted = [...values].sort((a, b) => a - b);
  return {minimum: sorted[0], median: sorted[Math.floor(sorted.length / 2)], maximum: sorted.at(-1)};
}
for (const target of targets) {
  assert.equal(typeof target.go, 'string'); assert.equal(await artifactPin(target.binary), target.sha256);
  const samples = [];
  for (let sample = 0; sample < 5; sample++) {
    const started = performance.now();
    const fixture = await startMetadataDeployment('http', {binary: target.binary, sha256: target.sha256, port: 4284,
      startupEnvironment: {NEXTGO_METADATA_BASE_PATH: '', NEXTGO_METADATA_ASSET_PREFIX: ''}});
    const window = performance.now() - started, replies = [];
    try {for (let i = 0; i < 5; i++) replies.push(await png(fixture.origin));} finally {await fixture.close();}
    assert.equal(fixture.sourceRecords.length, 0);
    for (const [key, value] of Object.entries(fixture.cleanupProof)) assert.equal(value, key !== 'runtimeGatewayClosed', key);
    samples.push({fixtureStartToHealthMilliseconds: window, requests: replies, cleanup: fixture.cleanupProof});
  }
  output.targets.push({...target, samples, fixtureStartToHealthMilliseconds: range(samples.map(x => x.fixtureStartToHealthMilliseconds)),
    loopbackPNGMilliseconds: range(samples.flatMap(x => x.requests.map(r => r.milliseconds)))});
}
console.log(JSON.stringify(output, null, 2));
