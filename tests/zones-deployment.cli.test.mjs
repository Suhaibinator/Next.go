// Prebuilt CLI only: aggregate static export is explicitly unsupported and must
// refuse before replacing an existing publication. No application is built.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {mkdtemp, readFile, readdir, rm, writeFile} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, resolve} from 'node:path';
import test from 'node:test';

const binary = process.env.NEXTGO_ZONES_CLI ? resolve(process.env.NEXTGO_ZONES_CLI) : undefined;
test('prebuilt zones CLI refuses aggregate static export and preserves prior publication', {skip: !binary && 'set NEXTGO_ZONES_CLI to the frozen prebuilt CLI', timeout: 10_000}, async t => {
  const directory = await mkdtemp(join(tmpdir(), 'nextgo-zones-export-refusal-'));
  t.after(() => rm(directory, {recursive: true, force: true}));
  const manifest = join(directory, 'zones.json');
  await writeFile(manifest, JSON.stringify({version: 1, zones: [{id: 'marketing', host: '127.0.0.1:4230', basePath: '', app: './marketing'}]}));
  const previous = join(directory, 'index.html'), original = Buffer.from('<!doctype html><title>Previous publication</title>');
  await writeFile(previous, original);
  const child = spawn(binary, ['zones', 'export', '-out', directory, manifest], {cwd: '/', stdio: ['ignore', 'pipe', 'pipe']});
  const chunks = []; let size = 0, exceeded = false;
  for (const stream of [child.stdout, child.stderr]) stream.on('data', chunk => {size += chunk.length; if (size > 8192) {exceeded = true; child.kill('SIGKILL');} else chunks.push(chunk);});
  let timer, spawnError, timedOut = false;
  const result = await new Promise(done => {
    timer = setTimeout(() => {timedOut = true; child.kill('SIGKILL');}, 5000);
    child.once('error', error => {spawnError = error;}); child.once('close', (code, signal) => done({code, signal}));
  }).finally(() => clearTimeout(timer));
  assert.equal(timedOut, false); assert.equal(spawnError, undefined); assert.equal(exceeded, false); assert.equal(result.signal, null); assert.notEqual(result.code, 0);
  assert.match(Buffer.concat(chunks).toString(), /aggregate zones export is not supported/);
  assert.deepEqual(await readFile(previous), original); assert.deepEqual((await readdir(directory)).sort(), ['index.html', 'zones.json']);
  t.diagnostic(`CLI sha256=${createHash('sha256').update(await readFile(binary)).digest('hex')}; explicit aggregate rejection, unchanged prior output; no compilation`);
});
