// Hosted qualification of the unchanged client-only cases against one actual
// production demo. This helper never constructs application responses.
import {spawn} from 'node:child_process';
import {constants} from 'node:fs';
import {chmod, lstat, mkdir, open, readdir, realpath, writeFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {dirname, isAbsolute, join, relative, sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import assert from 'node:assert/strict';

const checkout = fileURLToPath(new URL('..', import.meta.url));
const sdk = process.env.NEXTGO_CLIENT_ONLY_GO_VERSION;
const root = process.env.NEXTGO_CLIENT_ONLY_CI;
const uid = process.getuid?.();
const phase = process.argv[2];
const markerName = 'ownership.json';
const hash = value => createHash('sha256').update(value).digest('hex');
const inside = (parent, path) => {const rel = relative(parent, path); return rel !== '' && !rel.startsWith(`..${sep}`) && rel !== '..' && !isAbsolute(rel);};
const fail = message => {throw new Error(message);};
const json = (path, value) => writeFile(path, JSON.stringify(value, null, 2) + '\n', {flag: 'wx', mode: 0o600});
let owned = false;

// Regular-file reads cannot follow links or consume unbounded reports/logs.
async function bounded(path, limit) {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const before = await file.stat();
    assert(before.isFile() && before.nlink === 1 && before.size <= limit, `Invalid bounded file: ${path}`);
    const bytes = Buffer.alloc(before.size + 1); let length = 0;
    while (length < bytes.length) {const part = await file.read(bytes, length, bytes.length - length, null); if (!part.bytesRead) break; length += part.bytesRead;}
    const after = await file.stat();
    assert(length === before.size && after.size === before.size && after.ino === before.ino && after.dev === before.dev && after.mtimeMs === before.mtimeMs, `File changed: ${path}`);
    return {bytes: bytes.subarray(0, length), stat: after};
  } finally {await file.close();}
}
async function pin(path, limit = 128 << 20) {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const before = await file.stat(), digest = createHash('sha256'), buffer = Buffer.alloc(64 << 10); let bytes = 0;
    assert(before.isFile() && before.nlink === 1 && before.size <= limit, `Invalid pin: ${path}`);
    for (;;) {const part = await file.read(buffer, 0, buffer.length, null); if (!part.bytesRead) break; bytes += part.bytesRead; assert(bytes <= limit); digest.update(buffer.subarray(0, part.bytesRead));}
    const after = await file.stat(); assert(bytes === before.size && after.size === before.size && after.ino === before.ino && after.dev === before.dev && after.mtimeMs === before.mtimeMs, `Pin changed: ${path}`);
    return {path, sha256: digest.digest('hex'), bytes, mode: after.mode & 0o777, uid: after.uid};
  } finally {await file.close();}
}
async function readJSON(path, limit = 16 << 20) {return JSON.parse((await bounded(path, limit)).bytes.toString('utf8'));}
async function verifySources() {
  const source = await readJSON(join(root,'report','source.json'));
  for(const expected of source.sources) assert.deepEqual(await pin(expected.path,1 << 20),expected,'Qualification source bytes or mode changed');
  assert.deepEqual(await pin(await realpath(process.execPath)),source.nodeExecutable,'Node executable changed');
}
async function validateRoot() {
  assert(process.platform === 'linux' && process.arch === 'x64' && Number.isSafeInteger(uid) && uid > 0, 'Hosted qualification requires actual nonroot Linux AMD64');
  assert(process.version === 'v24.22.1' && ['1.24.13', '1.27.3'].includes(sdk), 'Pinned Node/Go matrix required');
  assert(isAbsolute(root || '') && isAbsolute(process.env.RUNNER_TEMP || ''), 'Absolute owned root and RUNNER_TEMP required');
  const info = await lstat(root), canonical = await realpath(root), temp = await realpath(process.env.RUNNER_TEMP);
  assert(info.isDirectory() && !info.isSymbolicLink() && info.uid === uid && (info.mode & 0o777) === 0o700, 'Root must be current-UID private directory');
  assert(canonical === root && inside(temp, root) && /^nextgo-client-only-ci\.[A-Za-z0-9]+$/.test(root.slice(root.lastIndexOf('/') + 1)) && !inside(checkout, root), 'Unexpected owned root');
  return {version: 1, root, uid, gid: process.getgid(), sdk, node: process.version, platform: process.platform, arch: process.arch, checkout};
}
async function ownership() {
  const info = await validateRoot(), marker = await readJSON(join(root, markerName), 8192);
  for (const key of Object.keys(info)) assert.deepEqual(marker[key], info[key], `Ownership mismatch ${key}`);
  owned = true; return marker;
}
function commandEnv(extra = {}) {
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_') && key !== 'NEXTGO_ENV_DIR' && key !== 'PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH'));
  return {...env, GOTOOLCHAIN: 'local', GOFLAGS: '-p=1 -mod=readonly', GOMAXPROCS: '1', GOMEMLIMIT: '384MiB', GOTELEMETRY: 'off', GOCACHE: join(root, 'go-cache'), GOMODCACHE: join(root, 'mod-cache'), TMPDIR: join(root, 'tmp'), ...extra};
}
const existsGroup = pid => {try {process.kill(-pid, 0); return true;} catch (error) {if (error.code === 'ESRCH') return false; throw error;}};
// Every command owns a new process group, bounded stdout/stderr, one TERM and
// one KILL deadline, and a physical close/stdio join. There are no retries.
async function command(name, executable, args, timeout, extra = {}) {
  assert(/^[a-z0-9-]+$/.test(name));
  const started = new Date().toISOString(), chunks = []; let bytes = 0, cause = '', termSent = false, killSent = false, child;
  const done = new Promise((resolveDone, rejectDone) => {
    child = spawn(executable, args, {cwd: checkout, detached: true, stdio: ['ignore', 'pipe', 'pipe'], env: commandEnv(extra)});
    const terminate = (signal) => {if (!child.pid) return; try {process.kill(-child.pid, signal);} catch (error) {if (error.code !== 'ESRCH') throw error;}};
    const stop = reason => {if (cause) return; cause = reason; termSent = true; terminate('SIGTERM'); killTimer = setTimeout(() => {killSent = true; terminate('SIGKILL');}, 10_000); joinTimer = setTimeout(() => {
      // A missing join is a RED receipt. Release only controller-side handles
      // so evidence collection can run; never label the command/descendants gone.
      child.stdout.destroy(); child.stderr.destroy(); child.unref();
      process.off('SIGINT', onSignal); process.off('SIGTERM', onSignal);
      rejectDone(new Error(`${name}: physical command close missing after TERM/KILL`));
    }, 15_000);};
    let killTimer, joinTimer;
    const timer = setTimeout(() => stop('command-timeout'), timeout);
    const onSignal = () => stop('controller-signal');
    process.once('SIGINT', onSignal); process.once('SIGTERM', onSignal);
    const output = chunk => {if (bytes + chunk.length > 4 << 20) {stop('command-log-bound'); return;} chunks.push(chunk); bytes += chunk.length;};
    child.stdout.on('data', output); child.stderr.on('data', output);
    child.once('error', error => {cause ||= `spawn-${error.code || 'error'}`;});
    child.once('close', (code, signal) => {
      clearTimeout(timer); clearTimeout(killTimer); clearTimeout(joinTimer);
      process.off('SIGINT', onSignal); process.off('SIGTERM', onSignal);
      let groupRemaining = false;
      if (child.pid) {groupRemaining = existsGroup(child.pid); if (groupRemaining) {cause ||= 'owned-process-group-remains'; termSent = true; terminate('SIGTERM'); killSent = true; terminate('SIGKILL');}}
      resolveDone({name, executable, args, started, ended: new Date().toISOString(), pid: child.pid ?? null, code, signal, cause, termSent, killSent, closed: true, stdioJoined: true, groupRemaining, bytes});
    });
  });
  let receipt;
  try {receipt = await done;}
  catch (error) {receipt = {name, executable, args, started, ended: new Date().toISOString(), pid: child?.pid ?? null, closed: false, stdioJoined: false, cause: String(error).slice(0, 1024), termSent, killSent, bytes};}
  const log = Buffer.concat(chunks, bytes);
  await writeFile(join(root, 'report', `${name}.log`), log, {flag: 'wx', mode: 0o600});
  await json(join(root, 'report', `${name}.command.json`), receipt);
  assert(receipt.closed && receipt.stdioJoined && receipt.code === 0 && receipt.signal === null && !receipt.cause && !receipt.groupRemaining, `${name} failed; bounded receipt/log preserved`);
  return log.toString('utf8');
}
const titles = [
  'delayed WASM and lazy bytes preserve useful fallbacks while Go mounts explicit views inside stable boundaries',
  'error after eager and lazy setup restores exact fallback nodes, drafts and focus and removes new listeners',
  'panic after eager and lazy setup restores exact fallback nodes, drafts and focus and removes new listeners',
  'Go navigation disposes eager and lazy browser-view listeners and remounts independent state on return',
  'a superseded lazy download never replaces detached fallbacks and the returned page mounts normally',
];
const projects = ['client-only-chromium', 'client-only-firefox', 'client-only-webkit', 'client-only-webkit-touch'];
const expected = new Set(projects.flatMap(project => titles.map(title => `${project}\n${title}`)));
function cases(report) {
  const records = [];
  const walk = suites => {for (const suite of suites || []) {for (const spec of suite.specs || []) {assert(spec.file === 'client-only.spec.mjs', 'Unexpected spec'); for (const test of spec.tests || []) records.push({spec, test, key: `${test.projectName}\n${spec.title}`});} walk(suite.suites);}};
  walk(report.suites); assert.equal(records.length, 20); assert.equal(new Set(records.map(value => value.key)).size, 20);
  for (const {key, test} of records) {assert(expected.has(key), `Unexpected case ${key}`); assert.equal(test.expectedStatus, 'passed');}
  assert.deepEqual(report.errors || [], []); return records;
}
async function originalGone(proof) {
  try {
    const file = await open(`/proc/${proof.pid}/stat`, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    let text;
    try {const bytes = Buffer.alloc(8193), part = await file.read(bytes, 0, bytes.length, null); assert(part.bytesRead <= 8192); text = bytes.subarray(0, part.bytesRead).toString('utf8');} finally {await file.close();}
    const ticks = text.slice(text.lastIndexOf(')') + 2).trim().split(/\s+/)[19];
    assert(/^\d+$/.test(ticks || '') && ticks !== proof.nativeIdentity.startTimeTicks, 'Original native process still present');
    return 'pid-reused-original-gone';
  } catch (error) {if (error.code === 'ENOENT') return 'absent'; throw error;}
}
async function validateProof(record, artifact, suite = 'client-only') {
  const attachmentName = suite === 'client-only' ? 'client-only-native-process-and-wire-proof' : 'native-data-source-and-wire-proof';
  const result = record.test.results[0], attachments = result.attachments.filter(value => value.name === attachmentName);
  assert.equal(attachments.length, 1); const attachment = attachments[0];
  assert(attachment.contentType === 'application/json' && typeof attachment.body === 'string' && attachment.body.length < 1400000 && !attachment.path, 'Expected bounded inline genuine fixture proof');
  const bytes = Buffer.from(attachment.body, 'base64'); assert(bytes.length <= 1 << 20);
  const proof = JSON.parse(bytes.toString('utf8')), p = proof.process, id = p.nativeIdentity;
  assert(p.binary === artifact.path && p.sha256 === artifact.sha256 && p.bytes === artifact.bytes);
  assert(p.artifactUID === uid && !(p.artifactMode & 0o222) && (p.artifactMode & 0o111));
  assert(p.parentUID === uid && id?.supported === true && id.observed === true && id.pid === p.pid && id.parentPID > 0 && id.processGroup === p.pid && /^\d+$/.test(id.startTimeTicks));
  assert(id.uids.length === 4 && id.uids.every(value => value === uid && value > 0));
  assert(p.cwdRemoved === true && inside(join(root, 'tmp'), p.cwd) && !inside(checkout, p.cwd));
  try {await lstat(p.cwd); fail('Native working directory still exists');} catch (error) {if (error.code !== 'ENOENT') throw error;}
  assert(p.closed && p.joined && p.termSent && p.shutdownRequested && !p.forced && !p.unexpectedExit && p.code === 0 && p.signal === null);
  assert(!proof.gatewayListening && !proof.sourceListening && proof.httpJoined && proof.sockets === 0 && proof.upstreams === 0);
  assert.equal(proof.listeners.length, 2); assert.deepEqual(proof.listeners.map(value => value.port).sort(), [4160, 4162]);
  assert(proof.listeners.every(value => value.started && value.bindJoined && value.closed && !value.listening && !value.failedBind && value.errors.length === 0));
  assert(proof.wire.length > 0 && proof.wire.length <= 256 && proof.wire.every(value => !value.observerError));
  if(suite === 'client-only') {
    assert.deepEqual(proof.source, []);
    assert(proof.wire.some(value => value.path.startsWith('/client-only') && value.status === 200 && value.headersReceived));
  } else {
    assert(['metadata-stream','metadata-clear'].includes(suite));
    assert(Array.isArray(proof.source) && proof.source.length <= 64 && proof.source.every(value=>value.unexpected === false));
    // The existing speculative stream case deliberately gets 204 and no loader.
    const speculative = record.spec.title === 'uncached speculative requests return conservative 204 without invoking the metadata data source';
    if(speculative) assert.deepEqual(proof.source, []); else assert(proof.source.length > 0);
    assert(proof.wire.some(value=>value.path.startsWith('/'+suite) && value.status === (speculative ? 204 : 200) && value.headersReceived));
  }
  return {case: record.key, pid: p.pid, parentUID: p.parentUID, nativeIdentity: id, cwd: p.cwd, cwdRemoved: true, originalProcess: await originalGone(p), proofSHA256: hash(bytes), bytes: bytes.length};
}
async function listenerEvidence(ports = [4160,4161,4162]) {
  const evidence = [];
  for (const name of ['tcp', 'tcp6']) {
    const file = await open(`/proc/net/${name}`, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    let length = 0; const bytes = Buffer.alloc((1 << 20) + 1);
    try {while (length < bytes.length) {const part = await file.read(bytes, length, bytes.length - length, null); if (!part.bytesRead) break; length += part.bytesRead;}} finally {await file.close();}
    assert(length <= 1 << 20, 'Bounded listener inspection exceeded');
    const rows = bytes.subarray(0, length).toString('utf8').split('\n').slice(1).map(line => line.trim().split(/\s+/)).filter(fields => fields.length > 3 && ports.includes(parseInt(fields[1].split(':')[1], 16)) && fields[3] === '0A');
    evidence.push({table: name, selectedListeningRows: rows});
  }
  return evidence;
}
async function init() {
  const info = await validateRoot(); assert.deepEqual(await readdir(root), []);
  await json(join(root, markerName), info); owned = true;
  for (const name of ['go-cache','mod-cache','tmp','browser','browser-metadata-stream','browser-metadata-clear','report','artifacts']) await mkdir(join(root, name), {mode: 0o700});
  const sources = [];
  for (const path of ['tests/client-only.spec.mjs','tests/client-only.config.mjs','tests/serve-metadata-stream.mjs','tests/link-status-gates.mjs','scripts/test-browser.mjs','scripts/client-only-ci.mjs','package.json','package-lock.json','go.mod','.github/workflows/ci.yml','tests/metadata-stream.spec.mjs','tests/metadata-stream.config.mjs','tests/metadata-clear.spec.mjs','tests/metadata-clear.config.mjs','tests/metadata-stream-deployment.test.mjs','tests/metadata-stream-deployment-fixture.mjs','tests/lambda-features-gateway.mjs']) sources.push(await pin(join(checkout, path), 1 << 20));
  await json(join(root, 'report', 'source.json'), {sources, nodeExecutable:await pin(await realpath(process.execPath)), githubSHA: process.env.GITHUB_SHA || '', checkoutHEAD: (await command('checkout-head', 'git', ['rev-parse','HEAD'], 10_000)).trim()});
}
async function build() {
  await ownership();
  const version = await command('go-version', 'go', ['version'], 10_000);
  assert.equal(version.trim(), `go version go${sdk} linux/amd64`);
  const goroot = (await command('go-root', 'go', ['env','GOROOT'], 10_000)).trim(); assert(isAbsolute(goroot));
  const tools = [];
  for(const name of ['bin/go','pkg/tool/linux_amd64/compile','pkg/tool/linux_amd64/link']) tools.push(await pin(await realpath(join(goroot,name))));
  const cli = join(root, 'artifacts', 'nextgo');
  await command('cli-build', 'go', ['build','-trimpath','-buildvcs=false','-o',cli,'./cmd/nextgo'], 10 * 60_000);
  await chmod(cli, 0o500);
  await command('generated-check', cli, ['generate','-check','./examples/demo'], 60_000);
  await command('demo-build', cli, ['build','-out',join(root,'artifacts','demo'),'./examples/demo'], 12 * 60_000);
  const binary = join(root, 'artifacts', 'demo', 'server'); await chmod(binary, 0o500);
  const artifact = await pin(binary); assert(artifact.uid === uid && artifact.bytes > 0);
  await json(join(root, 'report', 'build.json'), {version: 1, sdk, node: process.version, uid, tools, cli: await pin(cli), artifact, goVersion: version.trim()});
  assert(isAbsolute(process.env.GITHUB_ENV || '')); await writeFile(process.env.GITHUB_ENV, `NEXTGO_METADATA_STREAM_SERVER_SHA256=${artifact.sha256}\n`, {flag:'a'});
}
async function qualify() {
  await ownership(); const build = await readJSON(join(root, 'report', 'build.json'));
  await verifySources();
  const artifact = await pin(join(root,'artifacts','demo','server'));
  assert.deepEqual(artifact, build.artifact); assert(!(artifact.mode & 0o222) && artifact.uid === uid);
  assert(process.env.NEXTGO_E2E_SERVER_BINARY === artifact.path && process.env.NEXTGO_METADATA_STREAM_SERVER_SHA256 === artifact.sha256 && process.env.NEXTGO_E2E_RESULTS === join(root,'browser'));
  const pkg = await readJSON(join(checkout,'node_modules','@playwright','test','package.json'), 1 << 20); assert.equal(pkg.version,'1.64.1');
  const cli = join(checkout,'node_modules','@playwright','test','cli.js');
  await command('discovery',process.execPath,[cli,'test','--config','tests/client-only.config.mjs','--list','--reporter=json'],60_000,{PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report','discovery.json')});
  const discovery = cases(await readJSON(join(root,'report','discovery.json')));
  assert(discovery.every(value => value.test.results.length === 0), 'Discovery must not execute cases');
  let launchLog;
  try {launchLog = await command('browser-matrix',process.execPath,[join(checkout,'scripts','test-browser.mjs'),'--config','tests/client-only.config.mjs','--reporter=line,json'],22 * 60_000,{PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report','report.json'),DEBUG:'pw:browser',DEBUG_COLORS:'0'});}
  finally {await verifySources();}
  // Capture actual launch entrypoints, including Chromium's headless shell.
  // WebKit's entrypoint is its shipped launcher; this is not a whole-install hash.
  const launches = [...launchLog.matchAll(/<launching> (\/[^\s]+) /g)].map(match => match[1]);
  const launchedPIDs = [...launchLog.matchAll(/<launched> pid=(\d+)/g)].map(match=>Number(match[1]));
  const exitedPIDs = [...launchLog.matchAll(/\[pid=(\d+)\] <process did exit: exitCode=0, signal=null>/g)].map(match=>Number(match[1]));
  assert(launchedPIDs.length >= 4 && launchedPIDs.length <= 20 && launches.length === launchedPIDs.length);
  assert.equal(new Set(launchedPIDs).size,launchedPIDs.length); assert.deepEqual([...exitedPIDs].sort((a,b)=>a-b),[...launchedPIDs].sort((a,b)=>a-b),'Actual browser launch/exit-0 evidence incomplete');
  const browsers = [];
  for (const name of ['chromium','firefox','webkit']) {
    const paths = [...new Set(launches.filter(path => new RegExp(`/${name}${name === 'chromium' ? '(?:_headless_shell)?' : ''}-[0-9]+/`).test(path)))];
    assert(paths.length > 0 && paths.length <= 2, `Actual ${name} launch entrypoint missing`);
    for (const path of paths) browsers.push({name, launchPath:path, executable:await pin(await realpath(path),512 << 20)});
  }
  await json(join(root,'report','browsers.json'), {playwright:pkg.version, browserDefinitions:await pin(join(checkout,'node_modules','playwright-core','browsers.json'),1 << 20), browsers, launchedPIDs, exitedPIDs});
  const report = await readJSON(join(root,'report','report.json')), records = cases(report);
  assert.equal(report.stats.expected,20); for (const key of ['unexpected','flaky','skipped']) assert.equal(report.stats[key],0);
  const proofs = [];
  for (const record of records) {
    assert(record.spec.ok && record.test.status === 'expected' && record.test.results.length === 1);
    const result = record.test.results[0]; assert(result.status === 'passed' && result.retry === 0 && !(result.errors || []).length);
    proofs.push(await validateProof(record,artifact));
  }
  assert.deepEqual(await pin(artifact.path),artifact);
  assert.equal(new Set(proofs.map(proof=>`${proof.pid}:${proof.nativeIdentity.startTimeTicks}`)).size,20);
  const listeners = await listenerEvidence(); assert(listeners.every(table=>table.selectedListeningRows.length === 0),'Qualification ports still listening');
  await json(join(root,'report','qualification.json'), {version:1,status:'PASS',sdk,node:process.version,platform:process.platform,arch:process.arch,uid,cases:20,profiles:projects,discovered:20,skipped:0,retries:0,artifact,proofs,listeners});
}
// These are the existing source-declared cases, including the three instantiated
// late-mode cases. Discovery must match every title/profile pair exactly.
const metadataTitles = {
  'metadata-stream': [
    'initial browser receives useful fallback HTML and hydrated Go before genuine deferred metadata resolves',
    'late head completion preserves body, resource and JSON-LD nodes plus focus, input, counter, scroll and history',
    'crawler, unknown agent and HEAD requests block for final metadata from the same native loader',
    'disabled initial HTML streaming blocks a recognized browser until final metadata is ready',
    'negotiated Go navigation commits a fallback shell then exactly one metadata terminal before done',
    'navigation away cancels the real pending metadata read and cannot commit its stale title',
    ...['fail','invalid','readonly'].map(mode=>`a committed ${mode} contribution keeps complete fallback with a generic failed receipt`),
    'missing or unsupported metadata negotiation blocks and preserves the ordinary existing stream shape',
    'uncached speculative requests return conservative 204 without invoking the metadata data source',
  ],
  'metadata-clear': [
    'initial deferred metadata clears inherited scalars while retaining real Go controls and head companions',
    'negotiated Go navigation clears inherited scalars without replacing the committed page or reloading the document',
  ],
};
function metadataCases(report,suite) {
  const wanted = new Set(['chromium','firefox','webkit','webkit-touch'].flatMap(profile=>metadataTitles[suite].map(title=>`${suite}-${profile}\n${title}`)));
  const records = [];
  const walk = suites=>{for(const group of suites || []) {for(const spec of group.specs || []) {assert.equal(spec.file,suite+'.spec.mjs');for(const test of spec.tests || []) records.push({spec,test,key:`${test.projectName}\n${spec.title}`});}walk(group.suites);}};
  walk(report.suites);assert.equal(records.length,wanted.size);assert.equal(new Set(records.map(record=>record.key)).size,wanted.size);
  for(const record of records) {assert(wanted.has(record.key));assert.equal(record.test.expectedStatus,'passed');}
  assert.deepEqual(report.errors || [],[]);return records;
}
async function pinnedArtifact() {
  await ownership();await verifySources();
  const build = await readJSON(join(root,'report','build.json')), artifact = await pin(join(root,'artifacts','demo','server'));
  assert.deepEqual(artifact,build.artifact);assert(!(artifact.mode & 0o222) && artifact.uid === uid);
  assert(process.env.NEXTGO_E2E_SERVER_BINARY === artifact.path && process.env.NEXTGO_METADATA_STREAM_SERVER_SHA256 === artifact.sha256);
  return artifact;
}
async function metadataBrowser(suite) {
  assert(Object.hasOwn(metadataTitles,suite));const artifact = await pinnedArtifact();
  const count = metadataTitles[suite].length * 4, output = join(root,'browser-'+suite), config = `tests/${suite}.config.mjs`;
  const pkg = await readJSON(join(checkout,'node_modules','@playwright','test','package.json'),1 << 20);assert.equal(pkg.version,'1.64.1');
  const cli = join(checkout,'node_modules','@playwright','test','cli.js');
  await command(suite+'-discovery',process.execPath,[cli,'test','--config',config,'--list','--reporter=json'],60_000,{NEXTGO_E2E_RESULTS:output,PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report',suite+'-discovery.json')});
  const discovery = metadataCases(await readJSON(join(root,'report',suite+'-discovery.json')),suite);
  assert(discovery.every(record=>record.test.results.length === 0),'Discovery must not execute cases');
  let log;
  try {log=await command(suite+'-matrix',process.execPath,[join(checkout,'scripts','test-browser.mjs'),'--config',config,'--reporter=line,json'],22 * 60_000,{NEXTGO_E2E_RESULTS:output,PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report',suite+'-report.json'),DEBUG:'pw:browser',DEBUG_COLORS:'0'});}
  finally {await verifySources();}
  const launches=[...log.matchAll(/<launching> (\/[^\s]+) /g)].map(match=>match[1]);
  const launchedPIDs=[...log.matchAll(/<launched> pid=(\d+)/g)].map(match=>Number(match[1]));
  const exitedPIDs=[...log.matchAll(/\[pid=(\d+)\] <process did exit: exitCode=0, signal=null>/g)].map(match=>Number(match[1]));
  assert(launchedPIDs.length >= 4 && launchedPIDs.length <= count && launches.length === launchedPIDs.length);
  assert.equal(new Set(launchedPIDs).size,launchedPIDs.length);assert.deepEqual([...exitedPIDs].sort((a,b)=>a-b),[...launchedPIDs].sort((a,b)=>a-b));
  const browsers=[];
  for(const name of ['chromium','firefox','webkit']) {
    const paths=[...new Set(launches.filter(path=>new RegExp(`/${name}${name === 'chromium' ? '(?:_headless_shell)?' : ''}-[0-9]+/`).test(path)))];
    assert(paths.length > 0 && paths.length <= 2);
    for(const path of paths) browsers.push({name,launchPath:path,executable:await pin(await realpath(path),512 << 20)});
  }
  const report=await readJSON(join(root,'report',suite+'-report.json')),records=metadataCases(report,suite);
  assert.equal(report.stats.expected,count);for(const key of ['unexpected','flaky','skipped']) assert.equal(report.stats[key],0);
  const proofs=[];
  for(const record of records) {
    assert(record.spec.ok && record.test.status === 'expected' && record.test.results.length === 1);
    const result=record.test.results[0];assert(result.status === 'passed' && result.retry === 0 && !(result.errors || []).length);
    proofs.push(await validateProof(record,artifact,suite));
  }
  assert.equal(new Set(proofs.map(proof=>`${proof.pid}:${proof.nativeIdentity.startTimeTicks}`)).size,count);
  assert.deepEqual(await pin(artifact.path),artifact);
  const listeners=await listenerEvidence();assert(listeners.every(table=>table.selectedListeningRows.length === 0));
  await json(join(root,'report',suite+'-qualification.json'),{version:1,status:'PASS',suite,sdk,node:process.version,uid,cases:count,discovered:count,skipped:0,retries:0,artifact,proofs,listeners,browsers,playwright:pkg.version,launchedPIDs,exitedPIDs});
}
async function metadataDeployment() {
  const artifact=await pinnedArtifact();
  const families=[
    'unknown UA, HEAD and legacy metadata negotiation block on final',
    'opted-in browser HTML carries genuine fallback then metadata terminal',
    'exact v1 navigation acknowledgment has shell metadata done ownership',
    'late native failure retains fallback and redacts private diagnostics',
    'client cancellation physically closes source and next real Go invocation runs',
  ];
  const wanted=new Set(['http','buffered','streaming'].flatMap(mode=>families.map(title=>`${mode}: ${title}`)));
  let tap;
  try {tap=await command('metadata-deployment',process.execPath,['--test','--test-concurrency=1','--test-reporter=tap','tests/metadata-stream-deployment.test.mjs'],13 * 60_000,{NEXTGO_METADATA_DEPLOY_BINARY:artifact.path,NEXTGO_METADATA_DEPLOY_SHA256:artifact.sha256});}
  finally {await verifySources();}
  const results=[...tap.matchAll(/^ok (\d+) - ([^\n\r]+)$/gm)];
  assert.equal(results.length,15);assert.equal(new Set(results.map(match=>match[2])).size,15);
  for(const [index,match] of results.entries()) {assert.equal(Number(match[1]),index+1);assert(wanted.has(match[2]));}
  assert(!/^not ok\b/m.test(tap) && !/^(?:not )?ok [^\r\n]* #\s*(?:SKIP|TODO)\b/im.test(tap));assert.equal((tap.match(/^1\.\.15$/gm) || []).length,1);
  for(const [key,value] of Object.entries({tests:15,suites:0,pass:15,fail:0,cancelled:0,skipped:0,todo:0})) assert.equal((tap.match(new RegExp(`^# ${key} (\\d+)$`,'m')) || [])[1],String(value));
  assert.deepEqual(await pin(artifact.path),artifact);
  const listeners=await listenerEvidence([4160,4161,4162,4280]);assert(listeners.every(table=>table.selectedListeningRows.length === 0));
  await json(join(root,'report','metadata-deployment-qualification.json'),{version:1,status:'PASS',sdk,node:process.version,cases:15,artifact,results:results.map(match=>({number:Number(match[1]),title:match[2]})),tapSHA256:hash(tap),listeners,cleanupEvidence:'Each unchanged test awaits fixture.close with real native/group, listener, socket, cwd and artifact assertions. No durable per-case UID/process receipt is emitted; none is inferred.'});
}
async function collect() {
  await ownership();
  const final = {version:1,sourcePinsMatch:false,listeners:null,errors:[],physicalFailureCleanup:'No claim for detached children lacking a fixture receipt; no unidentified process is signaled. Hosted runner teardown is a separate platform boundary.'};
  try {await verifySources(); final.sourcePinsMatch=true;} catch(error) {final.errors.push(String(error).slice(0,1024));}
  try {final.listeners=await listenerEvidence([4160,4161,4162,4280]); if(final.listeners.some(table=>table.selectedListeningRows.length)) final.errors.push('Selected qualification listener still present');} catch(error) {final.errors.push(String(error).slice(0,1024));}
  await json(join(root,'report','final-observation.json'),final);
  const output = join(checkout,'.nextgo-test-results',`client-only-ci-${sdk}`);
  await mkdir(dirname(output),{recursive:true}); await mkdir(output,{mode:0o700});
  const summary = {version:1,sdk,root,files:[],omitted:[],bytes:0,limits:{total:128 << 20,file:32 << 20,files:512,entries:4096,depth:16}};
  let entries = 0;
  async function copyTree(path, dest, depth, browser) {
    if (++entries > summary.limits.entries || depth > summary.limits.depth) {if(summary.omitted.length<512)summary.omitted.push({path:relative(root,path),reason:'scan-bound'});return;}
    const info = await lstat(path); if(info.isSymbolicLink()) {summary.omitted.push({path:relative(root,path),reason:'symlink'});return;}
    if(info.isDirectory()) {for(const entry of (await readdir(path,{withFileTypes:true})).sort((a,b)=>a.name.localeCompare(b.name))) {if(entries>=summary.limits.entries)break; await copyTree(join(path,entry.name),join(dest,entry.name),depth+1,browser);} return;}
    if(!info.isFile() || (browser && !/\.(zip|png|webm|md|json)$/.test(path))) return;
    if(summary.files.length>=summary.limits.files || info.size>summary.limits.file || summary.bytes+info.size>summary.limits.total) {if(summary.omitted.length<512)summary.omitted.push({path:relative(root,path),reason:'file-or-total-bound',bytes:info.size});return;}
    const value = await bounded(path,summary.limits.file); await mkdir(dirname(dest),{recursive:true,mode:0o700}); await writeFile(dest,value.bytes,{flag:'wx',mode:0o600});
    summary.files.push({path:relative(output,dest),bytes:value.bytes.length,sha256:hash(value.bytes)});summary.bytes+=value.bytes.length;
  }
  await copyTree(join(root,markerName),join(output,markerName),0,false);
  await copyTree(join(root,'report'),join(output,'report'),0,false);
  await copyTree(join(root,'browser'),join(output,'browser'),0,true);
  for(const suite of ['metadata-stream','metadata-clear']) await copyTree(join(root,'browser-'+suite),join(output,'browser-'+suite),0,true);
  await json(join(output,'collection.json'),summary);
  console.log(`Collected ${summary.files.length} bounded evidence files (${summary.bytes} bytes); omissions ${summary.omitted.length}`);
  assert.deepEqual(final.errors,[],'Final source/listener observation failed; bounded evidence preserved');
}
try {
  const phases={init,build,qualify,collect,'metadata-stream':()=>metadataBrowser('metadata-stream'),'metadata-clear':()=>metadataBrowser('metadata-clear'),'metadata-deployment':metadataDeployment};
  assert(Object.hasOwn(phases,phase) && process.argv.length === 3,'Use init, build, qualify, metadata-stream, metadata-clear, metadata-deployment or collect');
  await phases[phase]();
} catch(error) {
  if(owned && phase!=='collect') {try {await json(join(root,'report',`${phase}-failure.json`),{status:'FAIL',phase,error:String(error).slice(0,4096),sdk,node:process.version,time:new Date().toISOString()});} catch {}}
  console.error(String(error).slice(0,4096)); process.exitCode=1;
}
