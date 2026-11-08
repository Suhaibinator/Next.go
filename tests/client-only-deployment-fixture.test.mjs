import assert from 'node:assert/strict';
import {mkdtemp, mkdir, rm, symlink, truncate, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {test} from 'node:test';
import {staticArtifactPin, startClientOnlyDeployment} from './client-only-deployment-fixture.mjs';
import {startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';

test('static artifact pins paths and bytes independently of file creation order', async () => {
  const root = await mkdtemp(join(tmpdir(), 'nextgo-client-only-pin-'));
  try {
    await mkdir(join(root, 'nested'));
    await writeFile(join(root, 'z'), 'last'); await writeFile(join(root, 'nested', 'a'), 'first');
    const initial = await staticArtifactPin(root);
    await rm(join(root, 'z')); await writeFile(join(root, 'z'), 'last');
    assert.deepEqual(await staticArtifactPin(root), initial);
    await writeFile(join(root, 'nested', 'a'), 'changed');
    assert.notEqual((await staticArtifactPin(root)).sha256, initial.sha256);
    await rm(join(root, 'nested', 'a')); await writeFile(join(root, 'nested', 'b'), 'first');
    assert.notEqual((await staticArtifactPin(root)).sha256, initial.sha256);
  } finally {await rm(root, {recursive: true, force: true});}
});

test('static artifact rejects empty trees, symlinks and entry overflow', async () => {
  const root = await mkdtemp(join(tmpdir(), 'nextgo-client-only-bounds-'));
  try {
    await assert.rejects(staticArtifactPin(root), /empty/);
    await writeFile(join(root, 'file'), 'real');
    await symlink(join(root, 'file'), join(root, 'link'));
    await assert.rejects(staticArtifactPin(root), /symlinks/);
    await rm(join(root, 'link'));
    await truncate(join(root, 'file'), (128 << 20) + 1);
    await assert.rejects(staticArtifactPin(root), /byte bound/);
    await writeFile(join(root, 'file'), 'real');
    for (let i = 0; i < 1024; i++) await mkdir(join(root, String(i)));
    await assert.rejects(staticArtifactPin(root), /entry bound/);
  } finally {await rm(root, {recursive: true, force: true});}
});

test('unknown deployment modes and relative artifact roots fail before startup', async () => {
  await assert.rejects(startClientOnlyDeployment('other'), /unknown/);
  await assert.rejects(staticArtifactPin('relative'), /absolute/);
});

test('invalid fixture ports fail before artifact admission or listener startup', async () => {
  for (const port of [0, 65536, 4281.5, '4281']) {
    await assert.rejects(startMetadataDeployment('http', {port}), /invalid fixture port/);
  }
});
