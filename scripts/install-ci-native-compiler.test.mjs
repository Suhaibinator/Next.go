import assert from 'node:assert/strict';
import test from 'node:test';
import { compilerInvocation } from './install-ci-native-compiler.mjs';
import { rootInvocation, effectivePolicy, policy } from './install-ci-playwright-deps.mjs';

const paths = {config: '/owned space/apt.conf', node: '/setup-node/bin/node', cli: '/workspace/node_modules/playwright-core/cli.js', unusedBrowserCache: '/owned space/unused-browser-cache'};
const phases = {
  identity: ['/usr/bin/id', '-u'],
  dump: ['/usr/bin/apt-config', 'dump'],
  update: ['/usr/bin/apt-get', 'update'],
  compiler: ['/usr/bin/apt-get', 'install', '--yes', '--no-install-recommends', 'gcc', 'libc6-dev'],
};

test('already-root compiler installer keeps the reviewed explicit environment without sudo', () => {
  const reference = rootInvocation(paths, 'identity');
  for (const [phase, command] of Object.entries(phases)) {
    const result = compilerInvocation(paths, phase);
    assert.equal(result.command, '/usr/bin/env');
    assert.deepEqual(result.args, [...reference.args.slice(5, -2), ...command]);
    assert.ok(!result.args.includes('/usr/bin/sudo'));
    assert.ok(result.args.includes('APT_CONFIG=/owned space/apt.conf'));
    assert.ok(result.args.includes('PATH=/usr/sbin:/usr/bin:/sbin:/bin'));
    for (const name of ['NODE_OPTIONS', 'NODE_PATH', 'BASH_ENV', 'ENV']) {
      const index = result.args.indexOf(name); assert.ok(index > 0 && result.args[index - 1] === '-u');
    }
  }
});

test('compiler phase permits only fixed packages and no caller command', () => {
  assert.deepEqual(compilerInvocation(paths, 'compiler').args.slice(-6), phases.compiler);
  for (const phase of ['deps', 'upgrade', 'shell', 'toString', null, undefined]) assert.throws(() => compilerInvocation(paths, phase));
});

test('configuration path remains one literal argument and rejects relative or nul paths', () => {
  const quoted = {...paths, config: '/owned space/quote\" literal.conf'};
  assert.ok(compilerInvocation(quoted, 'compiler').args.includes('APT_CONFIG=' + quoted.config));
  for (const key of Object.keys(paths)) {
    assert.throws(() => compilerInvocation({...paths, [key]: 'relative/path'}, 'compiler'));
    assert.throws(() => compilerInvocation({...paths, [key]: '/bad\0path'}, 'compiler'));
  }
});

test('compiler policy reuse still refuses overridden root and apt-get acquisition bounds', () => {
  const render = prefix => Object.entries(policy).map(([key, value]) => `${prefix}${key} "${value}";\n`).join('');
  const valid = render('') + render('Binary::apt-get::');
  assert.deepEqual(effectivePolicy(valid), {aptConfig: policy, aptGetSubtree: policy});
  for (const prefix of ['', 'Binary::apt-get::']) {
    assert.throws(() => effectivePolicy(valid.replace(`${prefix}Acquire::Retries "2";`, `${prefix}Acquire::Retries "1";`)));
  }
});
