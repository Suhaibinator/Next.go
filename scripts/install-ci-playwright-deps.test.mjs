import assert from 'node:assert/strict';
import test from 'node:test';
import { configBytes, effectivePolicy, policy, rootInvocation } from './install-ci-playwright-deps.mjs';

const entries = Object.entries(policy);
const render = (prefix) => entries.map(([key, value]) => `${prefix}${key} "${value}";\n`).join('');
// Actual apt-config dump shape after its own binary branch has moved to root.
const exact = render('') + render('Binary::apt-get::');
const expected = { aptConfig: policy, aptGetSubtree: policy };
const paths = { config: '/owned space/apt.conf', node: '/setup-node/bin/node', cli: '/workspace/node_modules/playwright-core/cli.js', unusedBrowserCache: '/owned space/unused-browser-cache' };

test('task-local configuration pins general and both binary-specific policies', () => {
  assert.equal(configBytes.toString(), render('') + render('Binary::apt-config::') + render('Binary::apt-get::'));
  assert.equal(configBytes.toString().trim().split('\n').length, 9);
});
test('effective policy accepts validated apt-config root and retained apt-get sibling', () => {
  assert.deepEqual(effectivePolicy(`APT::Architecture "amd64";\n${exact}`), expected);
});
for (const [label, prefix] of [['apt-config root', ''], ['apt-get subtree', 'Binary::apt-get::']]) {
  test(`effective policy rejects missing, overridden and duplicate ${label} values`, () => {
    for (const [key, value] of entries) {
      const line = `${prefix}${key} "${value}";\n`;
      for (const invalid of [exact.replace(line, ''), exact.replace(line, `${prefix}${key} "99";\n`), exact + line]) {
        assert.throws(() => effectivePolicy(invalid));
      }
    }
  });
}
test('preserved hosted Retries1 first failure stays red before dependencies', () => {
  const originalFailure = exact.replace('Acquire::Retries "2";\n', 'Acquire::Retries "1";\n');
  assert.throws(() => effectivePolicy(originalFailure), /Effective APT policy mismatch: apt-config root/);
  assert.deepEqual(effectivePolicy(exact), expected);
});
test('effective policy rejects malformed or case-aliased scope keys', () => {
  for (const invalid of [exact.replace('Acquire::Retries "2";', 'Acquire::Retries 2;'), exact.replace('Binary::apt-get::Acquire::Retries', 'Binary::apt-get::acquire::Retries')]) {
    assert.throws(() => effectivePolicy(invalid));
  }
});
test('effective policy bounds captured input before scope parsing', () => {
  assert.throws(() => effectivePolicy('x'.repeat((1 << 20) + 1)), /APT dump exceeds bound/);
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
