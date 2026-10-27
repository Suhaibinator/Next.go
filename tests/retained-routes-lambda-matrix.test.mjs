// Data-only validator regressions. Actual app/RuntimeAPI/browser evidence must
// still be produced by the separate owner; these fixtures are never evidence.
import test from 'node:test';
import assert from 'node:assert/strict';
import {lambdaApps, lambdaAppOrder, lambdaModes, lambdaProfiles, nativeOnly, deadlineCase, lazyModuleTitle, lambdaCases, lambdaClosureTotals} from './retained-routes-lambda-matrix.mjs';

const originalTitles = app => app.id === 'retained' ? Array.from({length: 17}, (_, index) => 'retained shared ' + index).concat(nativeOnly) : Array.from({length: 17}, (_, index) => 'merge shared ' + index).concat(lazyModuleTitle);
const modeTitles = app => originalTitles(app).map(title => title === nativeOnly ? deadlineCase : title);
const sourceFile = (app, title) => app.id === 'retained' && title === deadlineCase ? 'retained-routes-lambda.spec.mjs' : app.sharedFile;
function report(mode, app, executed) {
  return {errors: [], config: {version: '1.64.1', workers: 1, maxFailures: 1, projects: lambdaProfiles.map(profile => ({name: `${app.id}-${mode}-${profile}`, retries: 0}))}, stats: {expected: 72, unexpected: 0, flaky: 0, skipped: 0}, suites: [{specs: modeTitles(app).map(title => ({title, file: '/owned/harness/' + sourceFile(app, title), tests: lambdaProfiles.map(profile => ({projectName: `${app.id}-${mode}-${profile}`, expectedStatus: 'passed', status: 'expected', results: executed ? [{status: 'passed', retry: 0, attachments: []}] : []}))}))}]};
}
function full(cwd) {
  return {cwd, closed: true, joined: true, shutdownRequested: true, termSent: true, cwdRemoved: true, groupEmpty: true, listenerRefused: true, forced: false, unexpectedExit: false, identityRefused: false, cleanupDeferred: false, code: 0, signal: null};
}
function fixture() {
  const nativeProof = {version: 2, status: 'PASSED', cases: 144, auxiliaryNativeProcesses: 2, totalNativeProcesses: 146, apps: {}, nativeProcesses: [], auxiliaryNativeProofs: []};
  for (const name of lambdaAppOrder) {
    const app = lambdaApps[name], cases = [];
    for (const profile of lambdaProfiles) for (const [index, title] of originalTitles(app).entries()) cases.push({app: name, id: `${name}-${profile}|${title}`, ...full(`/owned/native/${name}/${profile}/${index}`)});
    nativeProof.nativeProcesses.push(...cases); nativeProof.auxiliaryNativeProofs.push({app: name, ...full('/owned/native/' + name + '/assets')});
    nativeProof.apps[name] = {cases: 72, totalNativeProcesses: 73, auxiliaryNativeProcesses: 1, nativeProcesses: cases};
  }
  const proof = {version: 2, status: 'PASSED', caseExecutions: 432, nativeClosures: 440, ZIPHTTPBaselines: lambdaAppOrder.map(name => ({app: name, ...full('/owned/ZIPHTTP/' + name)})), modes: {}};
  for (const mode of lambdaModes) {
    const matrix = {status: 'PASSED', cases: 144, totalNativeProcesses: 146, apps: {}, nativeProcesses: [], auxiliaryNativeProofs: []};
    for (const name of lambdaAppOrder) {
      const app = lambdaApps[name], cases = [];
      for (const profile of lambdaProfiles) for (const [index, title] of modeTitles(app).entries()) cases.push({app: name, id: `${name}-${mode}-${profile}|${sourceFile(app, title)}|${title}`, title, transport: 'LOCAL mock Lambda ' + mode, lambda: {mode}, ...full(`/owned/${mode}/${name}/${profile}/${index}`)});
      matrix.nativeProcesses.push(...cases); matrix.auxiliaryNativeProofs.push({app: name, transport: 'LOCAL mock Lambda ' + mode, lambda: {mode}, ...full(`/owned/${mode}/${name}/assets`)});
      matrix.apps[name] = {cases: 72, nativeProcesses: cases, auxiliaryNativeProcesses: 1, totalNativeProcesses: 73};
    }
    proof.modes[mode] = matrix;
  }
  return {proof, nativeProof};
}

test('Both explicit apps discover and execute all eighteen cases on all four transport-specific profiles', () => {
  for (const mode of lambdaModes) for (const app of Object.values(lambdaApps)) {
    assert.equal(lambdaCases(report(mode, app, false), mode, app, modeTitles(app), false).length, 72);
    assert.equal(lambdaCases(report(mode, app, true), mode, app, modeTitles(app), true).length, 72);
  }
  assert.equal(lambdaApps.retained.shared, 17); assert.equal(lambdaApps.retained.counterpart, 1);
  assert.equal(lambdaApps.merge.shared, 18); assert.equal(lambdaApps.merge.counterpart, 0);
});

test('Collection rejects borrowed application/profile/source identities and every incomplete execution state', () => {
  const changes = [
    value => { value.suites[0].specs.pop(); },
    value => { value.suites[0].specs[0].tests[0].projectName = 'merge-buffered-chromium'; },
    value => { value.suites[0].specs[0].file = 'merge-shared.spec.mjs'; },
    value => { value.suites[0].specs[1].title = value.suites[0].specs[0].title; },
    value => { value.suites[0].specs[0].tests[0].results[0].retry = 1; },
    value => { value.suites[0].specs[0].tests[0].results.push({status: 'passed', retry: 1}); },
    value => { value.suites[0].specs[0].tests[0].results[0].status = 'skipped'; },
    value => { value.config.projects[0].retries = 1; },
    value => { value.config.projects[1].name = value.config.projects[0].name; },
    value => { value.stats.skipped = 1; }, value => { value.stats.flaky = 1; },
    value => { value.errors.push({message: 'browser owner failed'}); },
  ];
  for (const change of changes) {
    const value = report('buffered', lambdaApps.retained, true); change(value);
    assert.throws(() => lambdaCases(value, 'buffered', lambdaApps.retained, modeTitles(lambdaApps.retained), true));
  }
  assert.throws(() => lambdaCases(report('buffered', lambdaApps.retained, false), 'buffered', lambdaApps.retained, modeTitles(lambdaApps.retained), true));
  assert.throws(() => lambdaCases(report('buffered', lambdaApps.retained, true), 'buffered', lambdaApps.retained, originalTitles(lambdaApps.retained), true));
  assert.throws(() => lambdaCases(report('buffered', lambdaApps.merge, true), 'buffered', lambdaApps.merge, modeTitles(lambdaApps.merge).slice(0, -1).concat(deadlineCase), true));
});

test('Prospective two-app total requires the original native146, two own ZIP baselines and both transport146 lifetimes', () => {
  const {proof, nativeProof} = fixture();
  assert.deepEqual(lambdaClosureTotals(proof, nativeProof), {browser: 432, native: 440, original: {browser: 144, native: 146}, ZIPHTTPBaselines: 2, buffered: {browser: 144, native: 146}, streaming: {browser: 144, native: 146}, perApplication: {browser: 216, native: 220}});
});

test('Closure accounting rejects a borrowed native or other-app asset probe and missing independent ZIP baseline', () => {
  for (const change of [
    value => { value.proof.ZIPHTTPBaselines.pop(); },
    value => { value.proof.ZIPHTTPBaselines[1].app = 'retained'; },
    value => { value.proof.ZIPHTTPBaselines[0].cwd = value.nativeProof.auxiliaryNativeProofs[0].cwd; },
    value => { value.proof.modes.buffered.auxiliaryNativeProofs[1].app = 'retained'; },
    value => { value.proof.modes.streaming.auxiliaryNativeProofs[0].cwd = value.proof.modes.buffered.auxiliaryNativeProofs[0].cwd; },
    value => { value.nativeProof.auxiliaryNativeProofs[1].app = 'retained'; },
    value => { value.proof.nativeClosures = 438; },
  ]) {
    const value = fixture(); change(value); assert.throws(() => lambdaClosureTotals(value.proof, value.nativeProof));
  }
});

test('Closure accounting keeps all original cases, the honest task replacement, actual lazy case and physical joins mandatory', () => {
  for (const change of [
    value => { value.proof.modes.buffered.nativeProcesses.pop(); },
    value => { value.proof.modes.buffered.apps.merge.nativeProcesses = value.proof.modes.buffered.apps.merge.nativeProcesses.slice(0, -1); },
    value => { value.proof.modes.buffered.nativeProcesses[0].id = value.proof.modes.buffered.nativeProcesses[1].id; },
    value => { value.proof.modes.buffered.nativeProcesses[0].title = 'not the recorded title'; },
    value => { value.proof.modes.buffered.nativeProcesses[0].transport = 'LOCAL mock Lambda streaming'; },
    value => { value.proof.modes.buffered.nativeProcesses[0].closed = false; },
    value => { value.proof.modes.buffered.nativeProcesses[0].joined = false; },
    value => { value.proof.modes.buffered.nativeProcesses[0].groupEmpty = false; },
    value => { value.proof.modes.buffered.nativeProcesses[0].cleanupDeferred = true; },
    value => { value.proof.modes.buffered.nativeProcesses[0].forced = true; },
    value => { value.proof.modes.buffered.nativeProcesses[0].cwd = '/owned/noncanonical/../cwd'; },
    value => { value.proof.modes.buffered.nativeProcesses[0].cwd = value.nativeProof.nativeProcesses[0].cwd; },
    value => { value.nativeProof.apps.merge.cases = 71; },
    value => { value.proof.modes.streaming.status = 'NOT_STARTED'; },
  ]) {
    const value = fixture(); change(value); assert.throws(() => lambdaClosureTotals(value.proof, value.nativeProof));
  }
  const missingLazy = fixture();
  for (const item of missingLazy.nativeProof.nativeProcesses) if (item.app === 'merge' && item.id.endsWith('|' + lazyModuleTitle)) item.id = item.id.replace(lazyModuleTitle, 'not a real lazy module case');
  assert.throws(() => lambdaClosureTotals(missingLazy.proof, missingLazy.nativeProof));
  const nativeTask = fixture(), item = nativeTask.proof.modes.buffered.nativeProcesses.find(value => value.title === deadlineCase);
  item.id = item.id.replace(deadlineCase, nativeOnly); item.title = nativeOnly;
  assert.throws(() => lambdaClosureTotals(nativeTask.proof, nativeTask.nativeProof));
});
