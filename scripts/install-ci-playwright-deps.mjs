import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export const policy = Object.freeze({
  'Acquire::Retries': '2',
  'Acquire::http::Timeout': '30',
  'Acquire::https::Timeout': '30',
});
export const configBytes = Buffer.from(Object.entries(policy).map(([key, value]) => `${key} "${value}";\n`).join(''));
const systemPath = '/usr/sbin:/usr/bin:/sbin:/bin';
const engines = ['chromium', 'firefox', 'webkit'];
const rootOnlyUnsets = ['NODE_OPTIONS', 'NODE_PATH', 'NODE_COMPILE_CACHE', 'NODE_V8_COVERAGE', 'NODE_REDIRECT_WARNINGS', 'PW_PROFILE_DIR', 'PW_INSTRUMENT_MODULES', 'BASH_ENV', 'ENV'];

export function effectivePolicy(dump) {
  assert.equal(typeof dump, 'string');
  assert.ok(Buffer.byteLength(dump) <= 1 << 20, 'APT dump exceeds bound');
  const result = {};
  for (const line of dump.split(/\r?\n/)) {
    for (const key of Object.keys(policy)) {
      if (!line.startsWith(`${key} `)) continue;
      assert.equal(result[key], undefined, 'Duplicate effective APT policy key');
      const expected = `${key} "${policy[key]}";`;
      assert.equal(line.trimEnd(), expected, 'Effective APT policy mismatch');
      result[key] = policy[key];
    }
  }
  assert.deepEqual(result, policy, 'Required effective APT policy absent');
  return result;
}

export function rootInvocation({ config, node, cli, unusedBrowserCache }, phase) {
  for (const item of [config, node, cli, unusedBrowserCache]) {
    assert.ok(typeof item === 'string' && path.isAbsolute(item) && !item.includes('\0'), 'Expected literal absolute path');
  }
  const commands = {
    identity: ['/usr/bin/id', '-u'],
    dump: ['/usr/bin/apt-config', 'dump'],
    deps: [node, cli, 'install-deps', ...engines],
  };
  assert.ok(Object.hasOwn(commands, phase), 'Unsupported privileged phase');
  return {
    command: '/usr/bin/sudo',
    args: ['-n', '-u', 'root', '--', '/usr/bin/env',
      ...rootOnlyUnsets.flatMap((name) => ['-u', name]),
      `APT_CONFIG=${config}`, `PATH=${systemPath}`,
      `PLAYWRIGHT_BROWSERS_PATH=${unusedBrowserCache}`,
      ...commands[phase]],
  };
}

function captured(invocation, phase) {
  const result = spawnSync(invocation.command, invocation.args, {
    encoding: 'utf8', maxBuffer: 1 << 20, timeout: 10_000,
  });
  if (result.error || result.status !== 0 || result.signal) {
    throw new Error(`Privileged ${phase} failed (status=${result.status}, signal=${result.signal}, code=${result.error?.code ?? ''})`);
  }
  return result.stdout;
}

export function main() {
  assert.equal(process.platform, 'linux', 'CI dependency installer is Linux-only');
  assert.equal(process.env.GITHUB_ACTIONS, 'true', 'Only run in the explicitly configured CI job');
  assert.equal(process.argv.length, 2, 'No user-supplied installer arguments');
  const workspace = fs.realpathSync(process.cwd());
  const node = fs.realpathSync(process.execPath);
  const cli = fs.realpathSync(path.join(workspace, 'node_modules/playwright-core/cli.js'));
  const core = JSON.parse(fs.readFileSync(path.join(workspace, 'node_modules/playwright-core/package.json'), 'utf8'));
  const declared = JSON.parse(fs.readFileSync(path.join(workspace, 'package.json'), 'utf8')).devDependencies?.['@playwright/test'];
  assert.equal(core.version, '1.64.1', 'Root-only dependency path needs source review when Playwright changes');
  assert.equal(declared, core.version, 'Use the installed repository-pinned Playwright');
  const tempBase = fs.realpathSync(process.env.RUNNER_TEMP || os.tmpdir());
  const owned = fs.mkdtempSync(path.join(tempBase, 'nextgo-playwright-deps-'));
  fs.chmodSync(owned, 0o700);
  const config = path.join(owned, 'apt.conf');
  const unusedBrowserCache = path.join(owned, 'browser-cache-must-stay-absent');
  const fd = fs.openSync(config, fs.constants.O_WRONLY | fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_NOFOLLOW, 0o600);
  try { fs.writeFileSync(fd, configBytes); fs.fsyncSync(fd); } finally { fs.closeSync(fd); }
  const initial = fs.lstatSync(config);
  const configSHA256 = createHash('sha256').update(configBytes).digest('hex');
  function unchangedConfig() {
    const current = fs.lstatSync(config);
    assert.ok(current.isFile() && !current.isSymbolicLink() && current.nlink === 1);
    assert.equal(current.mode & 0o777, 0o600);
    assert.equal(current.dev, initial.dev); assert.equal(current.ino, initial.ino);
    assert.equal(current.size, configBytes.length);
    assert.equal(createHash('sha256').update(fs.readFileSync(config)).digest('hex'), configSHA256);
    assert.equal(fs.existsSync(unusedBrowserCache), false, 'install-deps unexpectedly wrote a browser cache');
  }
  const options = { config, node, cli, unusedBrowserCache };
  const proof = { version: 1, status: 'running', stage: 'configuration', config, configSHA256,
    configBytes: configBytes.length, configMode: '0600', node, nodeVersion: process.version,
    playwrightCLI: cli, playwrightVersion: core.version, privilegedCalls: {}, noEtcConfigurationWrites: true };
  let primaryError;
  try {
    unchangedConfig();
    proof.stage = 'root-identity';
    assert.equal(captured(rootInvocation(options, 'identity'), 'identity').trim(), '0');
    proof.rootUID = 0;
    proof.stage = 'effective-before';
    proof.effectiveBefore = effectivePolicy(captured(rootInvocation(options, 'dump'), 'APT policy'));
    // Print only the three approved effective values, never the full APT dump/environment.
    console.log(JSON.stringify({ phase: proof.stage, config, configSHA256, rootUID: 0, policy: proof.effectiveBefore }));
    unchangedConfig();
    proof.stage = 'system-dependencies';
    const invocation = rootInvocation(options, 'deps');
    const result = spawnSync(invocation.command, invocation.args, { stdio: 'inherit' });
    proof.privilegedCalls.installDeps = { status: result.status, signal: result.signal, errorCode: result.error?.code ?? null };
    if (result.error || result.status !== 0 || result.signal) throw new Error('System dependency installation failed');
    proof.stage = 'effective-after';
    proof.effectiveAfter = effectivePolicy(captured(rootInvocation(options, 'dump'), 'APT policy'));
    unchangedConfig();
    proof.status = 'passed'; proof.stage = 'complete';
  } catch (error) {
    primaryError = error; proof.status = 'failed'; proof.failure = error.message;
    throw error;
  } finally {
    // Preserve a first failure even if final local validation/proof writing also fails.
    try { unchangedConfig(); proof.configurationAndUnusedCacheVerified = true; }
    catch (error) {
      proof.configurationAndUnusedCacheVerified = false; proof.finalValidationFailure = error.message;
      if (!primaryError) { primaryError = error; proof.status = 'failed'; process.exitCode = 1; }
    }
    // Task-local configuration/proof are retained through runner teardown, including failure.
    try { fs.writeFileSync(path.join(owned, 'proof.json'), `${JSON.stringify(proof, null, 2)}\n`, { flag: 'wx', mode: 0o600 }); }
    catch (error) {
      proof.proofWriteFailure = error.code || 'write-failed';
      if (!primaryError) { proof.status = 'failed'; process.exitCode = 1; }
    }
    console.log(JSON.stringify(proof));
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  try { main(); } catch (error) { console.error(error.message); process.exitCode = 1; }
}
