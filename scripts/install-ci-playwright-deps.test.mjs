import assert from 'node:assert/strict';
import test from 'node:test';
import { configBytes, effectivePolicy, policy, rootInvocation } from './install-ci-playwright-deps.mjs';

const exact = configBytes.toString();
const paths = { config: '/owned space/apt.conf', node: '/setup-node/bin/node', cli: '/workspace/node_modules/playwright-core/cli.js', unusedBrowserCache: '/owned space/unused-browser-cache' };

test('effective policy accepts exactly configured keys and filters unrelated settings', () => {
  assert.deepEqual(effectivePolicy(`APT::Architecture "amd64";\n${exact}`), policy);
});
test('effective policy rejects missing, duplicate, overridden and malformed target values', () => {
  for (const text of [exact.replace('Acquire::Retries "2";\n', ''), exact + 'Acquire::Retries "2";\n', exact.replace('"30"', '"31"'), exact.replace('"2";', '2;'), exact.replace('Acquire::Retries', 'acquire::Retries')]) {
    assert.throws(() => effectivePolicy(text));
  }
});
test('effective policy bounds captured input', () => {
  assert.throws(() => effectivePolicy('x'.repeat((1 << 20) + 1)));
});
test('root identity, effective dump and install share identical explicit environment', () => {
  const calls = ['identity', 'dump', 'deps'].map((phase) => rootInvocation(paths, phase));
  for (const call of calls) {
    assert.equal(call.command, '/usr/bin/sudo');
    assert.deepEqual(call.args.slice(0, 5), ['-n', '-u', 'root', '--', '/usr/bin/env']);
    assert.ok(!call.args.includes('-i'));
    for (const name of ['NODE_OPTIONS', 'NODE_PATH', 'NODE_COMPILE_CACHE', 'NODE_V8_COVERAGE', 'NODE_REDIRECT_WARNINGS', 'PW_PROFILE_DIR', 'PW_INSTRUMENT_MODULES', 'BASH_ENV', 'ENV']) {
      const index = call.args.indexOf(name); assert.ok(index > 0 && call.args[index - 1] === '-u');
    }
    assert.ok(call.args.includes(`APT_CONFIG=${paths.config}`));
    assert.ok(call.args.includes('PATH=/usr/sbin:/usr/bin:/sbin:/bin'));
    assert.ok(call.args.includes(`PLAYWRIGHT_BROWSERS_PATH=${paths.unusedBrowserCache}`));
  }
  const assignment = calls[0].args.indexOf(`PLAYWRIGHT_BROWSERS_PATH=${paths.unusedBrowserCache}`) + 1;
  assert.deepEqual(calls[0].args.slice(0, assignment), calls[1].args.slice(0, assignment));
  assert.deepEqual(calls[0].args.slice(0, assignment), calls[2].args.slice(0, assignment));
  assert.deepEqual(calls[0].args.slice(assignment), ['/usr/bin/id', '-u']);
  assert.deepEqual(calls[1].args.slice(assignment), ['/usr/bin/apt-config', 'dump']);
  assert.deepEqual(calls[2].args.slice(assignment), [paths.node, paths.cli, 'install-deps', 'chromium', 'firefox', 'webkit']);
});
test('privileged phase builder rejects arbitrary commands and nonliteral paths', () => {
  assert.throws(() => rootInvocation(paths, 'install'));
  assert.throws(() => rootInvocation({ ...paths, cli: 'relative/cli.js' }, 'deps'));
  assert.throws(() => rootInvocation({ ...paths, config: '/bad\0path' }, 'deps'));
});
