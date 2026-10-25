// Loader-only tests use inert bytes and never execute or impersonate a Go app.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {mkdtemp, mkdir, readFile, rm, symlink, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import test from 'node:test';
import {loopbackOrigin, readIndependentArtifacts, shaFile} from './independent-zones-deployment-fixture.mjs';

const hash = bytes => createHash('sha256').update(bytes).digest('hex');
async function operatorIndex(t) {
  const root = await mkdtemp(join(tmpdir(), 'independent-artifact-loader-'));
  t.after(() => rm(root, {recursive: true, force: true}));
  const index = {version: 1, origins: {A1: 'http://127.0.0.1:4261', A2: 'http://127.0.0.1:4262', B1: 'http://127.0.0.1:4263'}, relayOrigin: 'http://127.0.0.1:4264', binarySHA256: {}}, releases = {};
  for (const [key, id, zone, base] of [['releaseA1', 'A1', 'alpha', ''], ['releaseA2', 'A2', 'alpha', ''], ['releaseB1', 'B1', 'blog', '/blog']]) {
    const bytes = Buffer.from(`inert loader-only ${id}`), binarySHA256 = hash(bytes);
    const release = {version: 1, releaseID: id, runtimeID: id.toLowerCase().repeat(10), zone: {id: zone, host: '127.0.0.1:4260', basePath: base}, publicOrigin: 'http://127.0.0.1:4260', assetPrefix: `${base}/_nextgo/releases/${id}`, assetsSHA256: 'a'.repeat(64), topologySHA256: 'b'.repeat(64), navigationProtocol: 3, actionProtocol: '1'};
    releases[id] = {release, binarySHA256}; index[key] = id; index.binarySHA256[key] = binarySHA256;
    await mkdir(join(root, id)); await writeFile(join(root, id, 'server'), bytes); await writeFile(join(root, id, 'zone-release.json'), JSON.stringify(releases[id]));
  }
  for (const [key, id] of [['ingressI1', 'I1'], ['ingressI2', 'I2'], ['ingressRollback', 'rollback'], ['ingressHTTPSDenial', 'https-denial']]) {
    const artifact = name => {const value = structuredClone(releases[name]); if (id === 'https-denial') value.release.publicOrigin = 'https://127.0.0.1:4260'; return value;};
    const retained = id === 'I1' || id === 'https-denial' ? ['A1'] : ['A1', 'A2'];
    const manifest = {version: 1, topologySHA256: 'b'.repeat(64), zones: [
      {binding: releases.A1.release.zone, activeRelease: id === 'I2' ? 'A2' : 'A1', releases: retained.map(name => ({artifact: artifact(name), origin: name === 'A2' ? index.relayOrigin : index.origins[name]}))},
      {binding: releases.B1.release.zone, activeRelease: 'B1', releases: [{artifact: artifact('B1'), origin: index.origins.B1}]},
    ]};
    const bytes = Buffer.from(`inert loader-only ${id}`); index[key] = id; index.binarySHA256[key] = hash(bytes);
    await mkdir(join(root, id)); await writeFile(join(root, id, 'server'), bytes); await writeFile(join(root, id, 'zone-ingress.json'), JSON.stringify(manifest));
  }
  const path = join(root, 'artifact-index.json'); await writeFile(path, JSON.stringify(index)); return {root, path, index};
}

test('independent fixture origins reject external and ambiguous transport URLs', () => {
  assert.equal(loopbackOrigin('http://127.0.0.1:4261').port, '4261');
  for (const value of ['https://127.0.0.1:4261', 'http://localhost:4261', 'http://127.0.0.1', 'http://127.0.0.1:4261/path', 'http://127.0.0.1:4261?x=1', 'http://user@127.0.0.1:4261', 'http://127.0.0.1:4261#x']) assert.throws(() => loopbackOrigin(value));
});
test('independent fixture binds all seven native bytes and portable manifest directories', async t => {
  const fixture = await operatorIndex(t), parsed = await readIndependentArtifacts(fixture.path);
  assert.equal(Object.keys(parsed.releases).length, 3); assert.equal(Object.keys(parsed.ingresses).length, 4);
  await writeFile(join(fixture.root, 'I2', 'server'), 'changed native bytes');
  await assert.rejects(readIndependentArtifacts(fixture.path), /actual ingress matches operator native pin/);
});
test('independent fixture rejects published wrong release routing even with unchanged native pin', async t => {
  const fixture = await operatorIndex(t), path = join(fixture.root, 'I2', 'zone-ingress.json'), manifest = JSON.parse(await readFile(path, 'utf8'));
  manifest.zones[0].activeRelease = 'A1'; await writeFile(path, JSON.stringify(manifest));
  await assert.rejects(readIndependentArtifacts(fixture.path), /published ingress snapshot must bind/);
});
test('independent fixture rejects symlinked native artifact before execution', async t => {
  const fixture = await operatorIndex(t), link = join(fixture.root, 'linked-server');
  await symlink(join(fixture.root, 'A1', 'server'), link);
  await assert.rejects(shaFile(link), /bounded regular native artifact required/);
});
