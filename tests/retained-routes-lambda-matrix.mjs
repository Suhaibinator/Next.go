// Pure qualification inventory/accounting. This module starts no application,
// browser or process and cannot replace the caller's actual identity auditor.
import {basename, isAbsolute, normalize} from 'node:path';

const check = (value, message) => { if (!value) throw Error('Lambda matrix: ' + message); };
export const lambdaProfiles = Object.freeze(['chromium', 'firefox', 'webkit', 'webkit-touch']);
export const lambdaModes = Object.freeze(['buffered', 'streaming']);
export const lambdaAppOrder = Object.freeze(['retained', 'merge']);
export const nativeOnly = 'hidden effects stop and a real owned HTTP read is canceled without replay';
export const deadlineCase = 'a serial Lambda task drains by its invocation deadline before retained navigation and never replays';
export const lazyModuleTitle = 'actual lazy ordinary Go source revokes loaded and download-pending owners and recovers fresh same-document scopes';
export const lambdaApps = Object.freeze({
  retained: Object.freeze({id: 'retained', example: './examples/retained', spec: 'retained-routes.spec.mjs', fixture: 'retained-routes-fixture.mjs', lambdaFixture: 'retained-routes-lambda-fixture.mjs', sharedFile: 'retained-shared.spec.mjs', titles: 'browserTitles', initial: '/a', css: '/assets/retained.css', attachment: 'retained-native-process-proof', cases: 72, shared: 17, counterpart: 1}),
  merge: Object.freeze({id: 'merge', example: './examples/merge', spec: 'merge-spec.mjs', fixture: 'merge-fixture.mjs', lambdaFixture: 'merge-lambda-fixture.mjs', sharedFile: 'merge-shared.spec.mjs', titles: 'mergeBrowserTitles', initial: '/?q=a', css: '/assets/merge.css', attachment: 'merge-native-process-proof', cases: 72, shared: 18, counterpart: 0}),
});
const exactApp = app => check(Object.values(lambdaApps).includes(app), 'reviewed application declaration required');
const sharedFile = (app, title) => app.id === 'retained' && title === deadlineCase ? 'retained-routes-lambda.spec.mjs' : app.sharedFile;
const project = (app, mode, profile) => `${app.id}-${mode}-${profile}`;
const lambdaID = (app, mode, profile, title) => `${project(app, mode, profile)}|${sharedFile(app, title)}|${title}`;
function titleList(titles, app) {
  check(Array.isArray(titles) && titles.length === 18 && new Set(titles).size === 18 && titles.every(value => typeof value === 'string' && value.length > 0 && value.length <= 1024), 'eighteen bounded distinct titles required');
  check(!titles.includes(nativeOnly) && (app.id === 'retained' ? titles.includes(deadlineCase) && !titles.includes(lazyModuleTitle) : !titles.includes(deadlineCase) && titles.includes(lazyModuleTitle)), 'honest per-app task counterpart and actual merge lazy case required');
}
export function lambdaCases(report, mode, app, titles, executed) {
  exactApp(app); check(lambdaModes.includes(mode) && typeof executed === 'boolean', 'explicit reviewed mode and phase required'); titleList(titles, app);
  const expectedProjects = lambdaProfiles.map(profile => project(app, mode, profile));
  check(Array.isArray(report?.config?.projects) && report.config.projects.length === 4 && new Set(report.config.projects.map(value => value.name)).size === 4 && report.config.projects.every(value => expectedProjects.includes(value.name) && value.retries === 0), 'exact four zero-retry application/transport projects required');
  check(Array.isArray(report.errors) && report.errors.length === 0 && report.config.version === '1.64.1' && report.config.workers === 1 && report.config.maxFailures === 1, 'real locked bounded reporter configuration required');
  if (executed) check(report.stats?.expected === 72 && report.stats.unexpected === 0 && report.stats.flaky === 0 && report.stats.skipped === 0, 'seventy-two actual passed cases required');
  check(Array.isArray(report.suites), 'report suites required');
  const result = [], seen = new Set(), pending = report.suites.map(suite => [suite, 0]); let visits = 0;
  while (pending.length) {
    const [suite, depth] = pending.pop(); check(++visits <= 4096 && depth <= 16 && suite && typeof suite === 'object', 'bounded suite traversal required');
    for (const spec of suite.specs || []) {
      check(++visits <= 4096 && titles.includes(spec.title) && typeof spec.file === 'string' && spec.file.length <= 4096 && basename(spec.file) === sharedFile(app, spec.title) && Array.isArray(spec.tests), 'exact title and adapted source identity required');
      for (const test of spec.tests) {
        check(++visits <= 4096 && expectedProjects.includes(test.projectName) && test.expectedStatus === 'passed' && Array.isArray(test.results), 'exact application/transport/profile required');
        const id = test.projectName + '|' + basename(spec.file) + '|' + spec.title;
        check(!seen.has(id), 'duplicate case'); seen.add(id);
        if (executed) check(test.status === 'expected' && test.results.length === 1 && test.results[0].status === 'passed' && test.results[0].retry === 0, 'one actually passing attempt without skips/retries/flakes required');
        else check(test.results.length === 0, 'discovery must not execute tests');
        result.push({id, app: app.id, title: spec.title, test});
      }
    }
    for (const child of suite.suites || []) pending.push([child, depth + 1]);
  }
  check(result.length === 72, 'exact seventy-two discovered cases required');
  for (const name of expectedProjects) check(result.filter(item => item.test.projectName === name).length === 18, 'all eighteen cases required on each actual profile');
  return result;
}
const closedSummary = item => check(item && item.closed === true && item.groupEmpty === true && item.listenerRefused === true, 'physical native closure summary required');
function closedFull(item) {
  closedSummary(item);
  check(['joined', 'shutdownRequested', 'termSent', 'cwdRemoved'].every(name => item[name] === true) && ['forced', 'unexpectedExit', 'identityRefused', 'cleanupDeferred'].every(name => item[name] === false) && item.code === 0 && item.signal === null, 'complete actual graceful native closure required');
}
function addCWD(item, cwd, full = false) {
  if (full) closedFull(item); else closedSummary(item);
  check(typeof item.cwd === 'string' && item.cwd.length <= 4096 && isAbsolute(item.cwd) && normalize(item.cwd) === item.cwd && !cwd.has(item.cwd), 'canonical independently owned cwd cannot be reused across any application or producer');
  cwd.add(item.cwd);
}
function equalRecords(left, right) { return JSON.stringify(left) === JSON.stringify(right); }
function nativeInventory(proof, cwd) {
  check(proof?.version === 2 && proof.status === 'PASSED' && proof.cases === 144 && proof.auxiliaryNativeProcesses === 2 && proof.totalNativeProcesses === 146 && Array.isArray(proof.nativeProcesses) && proof.nativeProcesses.length === 144 && Array.isArray(proof.auxiliaryNativeProofs) && proof.auxiliaryNativeProofs.length === 2, 'unchanged two-app native144/146 evidence required');
  const expected = Object.create(null), ids = new Set();
  for (const name of lambdaAppOrder) {
    const app = lambdaApps[name], part = proof.apps?.[name], cases = proof.nativeProcesses.filter(value => value.app === name), probes = proof.auxiliaryNativeProofs.filter(value => value.app === name);
    check(part?.cases === 72 && part.totalNativeProcesses === 73 && part.auxiliaryNativeProcesses === 1 && Array.isArray(part.nativeProcesses) && equalRecords(part.nativeProcesses, cases) && cases.length === 72 && probes.length === 1, 'per-app native seventy-two cases plus own asset probe required');
    const titlesByProfile = Object.fromEntries(lambdaProfiles.map(profile => [profile, new Set()])); expected[name] = cases.map(item => {
      check(typeof item.id === 'string' && item.id.length <= 1200 && !ids.has(item.id), 'distinct native case identity required'); ids.add(item.id);
      const profile = lambdaProfiles.find(value => item.id.startsWith(name + '-' + value + '|')); check(profile, 'exact native application/profile binding required');
      const title = item.id.slice((name + '-' + profile + '|').length); check(title.length > 0 && title.length <= 1024 && !titlesByProfile[profile].has(title), 'unique bounded native title required'); titlesByProfile[profile].add(title);
      addCWD(item, cwd); return {profile, title};
    });
    const firstTitles = JSON.stringify([...titlesByProfile[lambdaProfiles[0]]].sort());
    for (const titles of Object.values(titlesByProfile)) {
      check(JSON.stringify([...titles].sort()) === firstTitles, 'each original profile must execute the same eighteen titles');
      check(titles.size === 18, 'eighteen native cases per profile required');
      check(name === 'retained' ? titles.has(nativeOnly) && !titles.has(deadlineCase) : titles.has(lazyModuleTitle) && !titles.has(nativeOnly) && !titles.has(deadlineCase), 'original native task and real merge module cases remain required');
    }
    for (const probe of probes) addCWD(probe, cwd);
  }
  check(ids.size === 144 && cwd.size === 146, 'original native closure cardinality required'); return expected;
}
// The caller separately performs strictNativeProof and protocol/module audits
// on actual receipts. These totals cannot create signal authority or substitute
// the CGO1 native artifact's bytes for either actual CGO0 ZIP/bootstrap baseline.
export function lambdaClosureTotals(proof, nativeProof) {
  check(proof?.version === 2 && proof.status === 'PASSED' && proof.caseExecutions === 432 && proof.nativeClosures === 440, 'whole-cell432 browser/440 native lifetimes required');
  const cwd = new Set(), expectedNative = nativeInventory(nativeProof, cwd);
  check(Array.isArray(proof.ZIPHTTPBaselines) && proof.ZIPHTTPBaselines.length === 2, 'two own extracted-ZIP HTTP baselines required');
  for (const name of lambdaAppOrder) {
    const baseline = proof.ZIPHTTPBaselines.filter(value => value.app === name); check(baseline.length === 1, 'one independently owned ZIP HTTP baseline per application required'); addCWD(baseline[0], cwd, true);
  }
  for (const mode of lambdaModes) {
    const matrix = proof.modes?.[mode];
    check(matrix?.status === 'PASSED' && matrix.cases === 144 && matrix.totalNativeProcesses === 146 && Array.isArray(matrix.nativeProcesses) && matrix.nativeProcesses.length === 144 && Array.isArray(matrix.auxiliaryNativeProofs) && matrix.auxiliaryNativeProofs.length === 2, 'each transport must execute both seventy-two-case apps plus both asset probes');
    const globalIDs = new Set();
    for (const name of lambdaAppOrder) {
      const app = lambdaApps[name], part = matrix.apps?.[name], cases = matrix.nativeProcesses.filter(value => value.app === name), probes = matrix.auxiliaryNativeProofs.filter(value => value.app === name);
      check(part?.cases === 72 && part.auxiliaryNativeProcesses === 1 && part.totalNativeProcesses === 73 && Array.isArray(part.nativeProcesses) && equalRecords(part.nativeProcesses, cases) && cases.length === 72 && probes.length === 1, 'per-app transport case/probe receipts required');
      const expected = new Set(expectedNative[name].map(({profile, title}) => lambdaID(app, mode, profile, name === 'retained' && title === nativeOnly ? deadlineCase : title)));
      for (const item of cases) {
        check(typeof item.id === 'string' && item.id.length <= 1600 && expected.delete(item.id) && !globalIDs.has(item.id), 'transport cannot omit, relabel or substitute an original native case'); globalIDs.add(item.id);
        const title = item.id.slice(item.id.indexOf('|', item.id.indexOf('|') + 1) + 1); check(item.title === title, 'case title receipt must agree with exact identity');
        check(item.transport === 'LOCAL mock Lambda ' + mode && item.lambda?.mode === mode, 'actual transport receipt required'); addCWD(item, cwd, true);
      }
      check(expected.size === 0, 'all original cases and honest task counterpart must execute');
      check(probes[0].transport === 'LOCAL mock Lambda ' + mode && probes[0].lambda?.mode === mode, 'own per-app actual transport asset probe required'); addCWD(probes[0], cwd, true);
    }
    check(globalIDs.size === 144, 'transport case cardinality required');
  }
  check(cwd.size === 440, 'all four hundred forty physical native lifetimes require distinct owned cwd receipts');
  return {browser: 432, native: 440, original: {browser: 144, native: 146}, ZIPHTTPBaselines: 2, buffered: {browser: 144, native: 146}, streaming: {browser: 144, native: 146}, perApplication: {browser: 216, native: 220}};
}
