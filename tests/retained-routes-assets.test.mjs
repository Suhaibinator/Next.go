// Pure byte/header audit tests only. No HTTP server, application route, child
// process, browser, build fallback or fabricated application response is used.
import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {gzipSync} from 'node:zlib';
import {probeRuntimeAssets, validateGzipAsset} from './retained-routes-assets.mjs';

const sha = data => createHash('sha256').update(data).digest('hex');
function specimen(data = Buffer.from('real content bytes\n'), contentType = 'text/css') {
  const compressed = gzipSync(data);
  return {wire: {data: compressed, status: 200, headers: {'content-type': contentType + '; charset=utf-8', 'content-encoding': 'gzip', vary: 'Origin, Accept-Encoding', 'content-length': String(compressed.length)}, complete: true, closed: true}, identity: {sha256: sha(data), bytes: data.length}};
}
function copy(value) { return {...value, data: Buffer.from(value.data), headers: {...value.headers}}; }

test('raw gzip audit hashes actual compressed bytes and exactly matches bounded identity bytes', () => {
  const {wire, identity} = specimen();
  assert.deepEqual(validateGzipAsset(wire, identity, 'text/css', 256 << 10), {sha256: sha(wire.data), bytes: wire.data.length, decodedSHA256: identity.sha256, decodedBytes: identity.bytes, closed: true});
  assert.notEqual(sha(wire.data), identity.sha256);
  // Go may use chunked HTTP; absence of Content-Length does not waive complete,
  // body-end and physical closure requirements supplied by the HTTP adapter.
  const chunked = copy(wire); delete chunked.headers['content-length'];
  assert.equal(validateGzipAsset(chunked, identity, 'text/css', 256 << 10).decodedSHA256, identity.sha256);
});

test('gzip audit rejects status aliases incomplete messages and unclosed wire exchanges', () => {
  const {wire, identity} = specimen();
  for (const change of [value => { value.status = 302; }, value => { value.status = '200'; }, value => { value.complete = false; }, value => { value.complete = 'true'; }, value => { value.closed = false; }, value => { value.closed = 1; }, value => { delete value.closed; }, value => { value.data = new Uint8Array(value.data); }]) {
    const value = copy(wire); change(value); assert.throws(() => validateGzipAsset(value, identity, 'text/css', 256 << 10));
  }
});

test('gzip proof requires exact MIME encoding and explicit Vary Accept-Encoding', () => {
  const {wire, identity} = specimen();
  const invalid = [headers => { headers['content-type'] = 'text/css-malformed'; }, headers => { headers['content-type'] = 'text/javascript'; }, headers => { delete headers['content-type']; }, headers => { headers['content-encoding'] = 'identity'; }, headers => { headers['content-encoding'] = 'gzip, br'; }, headers => { delete headers['content-encoding']; }, headers => { headers.vary = 'Origin'; }, headers => { headers.vary = '*'; }, headers => { headers.vary = 'Accept-Encoding,'; }, headers => { delete headers.vary; }, headers => { headers['Content-Encoding'] = 'gzip'; }, headers => { headers['x-invalid'] = '\n'; }, headers => { headers['x-array'] = ['gzip']; }];
  for (const change of invalid) { const value = copy(wire); change(value.headers); assert.throws(() => validateGzipAsset(value, identity, 'text/css', 256 << 10)); }
  const valid = copy(wire); valid.headers['content-encoding'] = 'GZip'; valid.headers.vary = 'origin, ACCEPT-ENCODING';
  assert.equal(validateGzipAsset(valid, identity, 'text/css', 256 << 10).closed, true);
  assert.throws(() => validateGzipAsset(wire, identity, 'text/css-extra', 256 << 10));
});

test('gzip audit rejects decoded Fetch body masquerading as content-encoded bytes and corrupt members', () => {
  const {wire, identity} = specimen();
  const values = [Buffer.from('real content bytes\n'), Buffer.from('not gzip'), wire.data.subarray(0, wire.data.length - 1), Buffer.alloc(0)];
  const crc = Buffer.from(wire.data); crc[crc.length - 8] ^= 1; values.push(crc);
  for (const data of values) { const value = copy(wire); value.data = data; value.headers['content-length'] = String(data.length); assert.throws(() => validateGzipAsset(value, identity, 'text/css', 256 << 10)); }
  const extra = copy(wire); extra.data = Buffer.concat([wire.data, gzipSync(Buffer.from('extra member'))]); extra.headers['content-length'] = String(extra.data.length);
  assert.throws(() => validateGzipAsset(extra, identity, 'text/css', 256 << 10));
});

test('decoded SHA and byte count must exactly equal the separately fetched identity representation', () => {
  const {wire, identity} = specimen();
  for (const value of [{...identity, sha256: '0'.repeat(64)}, {...identity, sha256: 'A'.repeat(64)}, {...identity, sha256: 'a'.repeat(63)}, {...identity, bytes: identity.bytes - 1}, {...identity, bytes: String(identity.bytes)}, {...identity, bytes: 0}, {...identity, bytes: 1 << 21}]) assert.throws(() => validateGzipAsset(wire, value, 'text/css', 256 << 10));
  const changed = specimen(Buffer.from('different body bytes'));
  assert.throws(() => validateGzipAsset(changed.wire, identity, 'text/css', 256 << 10));
});

test('content-length count and framing headers are bounded canonical observations', () => {
  const {wire, identity} = specimen();
  for (const length of ['0', String(wire.data.length + 1), '0' + wire.data.length, '-1', '1e2', '9'.repeat(32), '1,1']) {
    const value = copy(wire); value.headers['content-length'] = length; assert.throws(() => validateGzipAsset(value, identity, 'text/css', 256 << 10));
  }
  const oversized = copy(wire); oversized.headers['x-large'] = 'a'.repeat((8 << 10) + 1); assert.throws(() => validateGzipAsset(oversized, identity, 'text/css', 256 << 10));
  const many = copy(wire); for (let i = 0; i < 65; i++) many.headers['x-' + i] = 'value'; assert.throws(() => validateGzipAsset(many, identity, 'text/css', 256 << 10));
  const aggregate = copy(wire); for (let i = 0; i < 3; i++) aggregate.headers['x-' + i] = 'a'.repeat(6 << 10); assert.throws(() => validateGzipAsset(aggregate, identity, 'text/css', 256 << 10));
});

test('compressed and decompressed caps reject overflow before accepting a hash match', () => {
  const {wire, identity} = specimen(Buffer.from('small'));
  for (const maximum of [0, -1, 0.5, Number.MAX_SAFE_INTEGER, (64 << 20) + 1, '1024']) assert.throws(() => validateGzipAsset(wire, identity, 'text/css', maximum));
  const compressed = copy(wire); compressed.data = Buffer.alloc((1 << 20) + 1024 + 1); delete compressed.headers['content-length'];
  assert.throws(() => validateGzipAsset(compressed, identity, 'text/css', 1024));
  const bomb = specimen(Buffer.alloc(4096));
  // The claimed identity is deliberately small. maxOutputLength must stop the
  // gunzip before a final SHA comparison could accept an oversized allocation.
  assert.throws(() => validateGzipAsset(bomb.wire, identity, 'text/css', 1024));
  assert.throws(() => validateGzipAsset(wire, {...identity, bytes: 1025}, 'text/css', 1024));
});

test('WASM magic remains required after gzip identity equivalence', () => {
  const valid = specimen(Buffer.from([0, 97, 115, 109, 1, 0, 0, 0]), 'application/wasm');
  assert.equal(validateGzipAsset(valid.wire, valid.identity, 'application/wasm', 64 << 20).decodedBytes, 8);
  const invalid = specimen(Buffer.from('not wasm'), 'application/wasm');
  assert.throws(() => validateGzipAsset(invalid.wire, invalid.identity, 'application/wasm', 64 << 20));
});

test('probe input preflight rejects noncanonical or unowned endpoints and app/runtime selectors before any HTTP', async () => {
  for (const origin of ['https://127.0.0.1:4188', 'http://localhost:4188', 'http://example.com:4188', 'http://127.1:4188', 'http://2130706433:4188', 'http://127.0.0.1:04188', 'http://127.0.0.1:4188/', 'http://127.0.0.1:4188/path', 'http://127.0.0.1:4188?query', 'http://127.0.0.1:4188#fragment', 'http://user@127.0.0.1:4188', 'http://127.0.0.1', 'http://127.0.0.1:0', 'http://127.0.0.1:65536']) await assert.rejects(probeRuntimeAssets(origin, 'retained', 'a'.repeat(64)));
  await assert.rejects(probeRuntimeAssets('http://127.0.0.1:4188', 'other', 'a'.repeat(64)));
  for (const sha256 of ['', 'a'.repeat(63), 'A'.repeat(64), null]) await assert.rejects(probeRuntimeAssets('http://127.0.0.1:4188', 'merge', sha256));
});
