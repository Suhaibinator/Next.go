// CI image provisioning only; this is not part of the application runtime.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { configBytes, effectivePolicy, rootInvocation } from './install-ci-playwright-deps.mjs';

const commands = Object.freeze({
  identity: ['/usr/bin/id', '-u'],
  dump: ['/usr/bin/apt-config', 'dump'],
  update: ['/usr/bin/apt-get', 'update'],
  compiler: ['/usr/bin/apt-get', 'install', '--yes', '--no-install-recommends', 'gcc', 'libc6-dev'],
});

// Reuse the reviewed explicit root environment. An image job already is root;
// it must not depend on sudo being installed or accept arbitrary commands.
export function compilerInvocation(options, phase) {
  assert.ok(Object.hasOwn(commands, phase), 'Unsupported compiler phase');
  const reference = rootInvocation(options, 'identity');
  assert.equal(reference.command, '/usr/bin/sudo');
  assert.deepEqual(reference.args.slice(0, 5), ['-n', '-u', 'root', '--', '/usr/bin/env']);
  assert.deepEqual(reference.args.slice(-2), ['/usr/bin/id', '-u']);
  return { command: '/usr/bin/env', args: [...reference.args.slice(5, -2), ...commands[phase]] };
}

function captured(invocation, phase) {
  const result = spawnSync(invocation.command, invocation.args, {encoding: 'utf8', maxBuffer: 1 << 20, timeout: 10_000});
  if (result.error || result.status !== 0 || result.signal) throw Error(`Compiler ${phase} failed (status=${result.status}, signal=${result.signal}, code=${result.error?.code ?? ''})`);
  return result.stdout;
}

export function main() {
  assert.equal(process.platform, 'linux', 'Compiler provisioning is Linux CI only');
  assert.equal(process.env.GITHUB_ACTIONS, 'true', 'Only run in the configured CI job');
  assert.equal(process.getuid(), 0, 'The pinned image provisioning job must already be root');
  assert.equal(process.argv.length, 2, 'No user-supplied installer arguments');
  assert.equal(process.env.PLAYWRIGHT_BROWSERS_PATH, '/ms-playwright');
  const parent = fs.realpathSync(process.env.NEXTGO_BROWSER_IMAGE_CI);
  const parentInfo = fs.lstatSync(parent);
  assert.ok(parentInfo.isDirectory() && !parentInfo.isSymbolicLink());
  assert.equal(parentInfo.uid, process.getuid());
  assert.equal(parentInfo.mode & 0o777, 0o700);
  assert.equal(fs.realpathSync(process.env.HOME), path.join(parent, 'home'));
  const owned = fs.mkdtempSync(path.join(parent, 'compiler-apt-'));
  fs.chmodSync(owned, 0o700);
  const config = path.join(owned, 'apt.conf');
  const fd = fs.openSync(config, fs.constants.O_WRONLY | fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_NOFOLLOW, 0o600);
  try {fs.writeFileSync(fd, configBytes); fs.fsyncSync(fd);} finally {fs.closeSync(fd);}
  const initial = fs.lstatSync(config), configSHA256 = createHash('sha256').update(configBytes).digest('hex');
  const workspace = fs.realpathSync(process.cwd());
  const options = {config, node: fs.realpathSync(process.execPath), cli: path.join(workspace, 'node_modules/playwright-core/cli.js'), unusedBrowserCache: path.join(owned, 'unused-browser-cache')};
  function unchanged() {
    const current = fs.lstatSync(config);
    assert.ok(current.isFile() && !current.isSymbolicLink() && current.nlink === 1);
    assert.equal(current.mode & 0o777, 0o600); assert.equal(current.uid, process.getuid());
    assert.equal(current.dev, initial.dev); assert.equal(current.ino, initial.ino); assert.equal(current.size, configBytes.length);
    assert.equal(createHash('sha256').update(fs.readFileSync(config)).digest('hex'), configSHA256);
    assert.equal(fs.existsSync(options.unusedBrowserCache), false);
  }
  const proof = {version: 1, purpose: 'native-compiler-ci-provisioning', status: 'running', config, configSHA256,
    nodeVersion: process.version, noEtcAPTPolicyWrites: true, phases: [], browserDependenciesInstalledHere: false,
    boundary: 'APT network acquisition policy and existing workflow timeout; no hard process-tree cleanup claim'};
  let primaryError;
  try {
    unchanged(); assert.equal(captured(compilerInvocation(options, 'identity'), 'identity').trim(), '0');
    proof.effectiveBefore = effectivePolicy(captured(compilerInvocation(options, 'dump'), 'effective policy'));
    for (const phase of ['update', 'compiler']) {
      unchanged();
      const invocation = compilerInvocation(options, phase), result = spawnSync(invocation.command, invocation.args, {stdio: 'inherit'});
      proof.phases.push({phase, status: result.status, signal: result.signal, errorCode: result.error?.code ?? null});
      if (result.error || result.status !== 0 || result.signal) throw Error(`Compiler ${phase} failed`);
    }
    proof.effectiveAfter = effectivePolicy(captured(compilerInvocation(options, 'dump'), 'effective policy'));
    unchanged();
    proof.tools = {};
    for (const name of ['gcc', 'ld']) {
      const file = fs.realpathSync('/usr/bin/' + name), info = fs.statSync(file);
      assert.ok(info.isFile()); fs.accessSync(file, fs.constants.X_OK); proof.tools[name] = file;
    }
    proof.status = 'passed';
  } catch (error) {primaryError = error; proof.status = 'failed'; proof.failure = error.message; throw error;}
  finally {
    try {unchanged(); proof.configurationVerified = true;}
    catch (error) {proof.configurationVerified = false; proof.finalValidationFailure = error.message; if (!primaryError) {proof.status = 'failed'; process.exitCode = 1;}}
    try {fs.writeFileSync(path.join(owned, 'proof.json'), JSON.stringify(proof, null, 2) + '\n', {flag: 'wx', mode: 0o600});}
    catch (error) {proof.proofWriteFailure = error.code || 'write-failed'; if (!primaryError) {proof.status = 'failed'; process.exitCode = 1;}}
    console.log(JSON.stringify(proof));
  }
}
if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try {main();} catch (error) {console.error(error.message); process.exitCode = 1;}
}
