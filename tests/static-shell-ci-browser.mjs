// CI/dev-only consumer of four actual exports and the unchanged reviewed suite.
import {join, relative, isAbsolute, sep} from 'node:path';
import {pathToFileURL} from 'node:url';
import {check, commandEnvironment, inventory, json, ownedRoot, readBounded, runChild, variants, writeJSON} from './static-shell-ci-build.mjs';

const profiles = ['chromium','firefox','webkit','webkit-touch'];
const projects = variants.flatMap(variant => profiles.map(profile => variant.toLowerCase()+'-'+profile));
const sameContent = (left,right) => JSON.stringify(left.map(({path,bytes,sha256}) => ({path,bytes,sha256}))) === JSON.stringify(right.map(({path,bytes,sha256}) => ({path,bytes,sha256})));

function testsIn(report) {
  const found = [], pending = (report.suites || []).map(suite => [suite,0]); let visits = 0;
  check(Array.isArray(report.errors) && report.errors.length === 0,'Playwright report has global errors');
  while (pending.length) {
    const [suite,depth] = pending.pop(); check(++visits <= 4096 && depth <= 16,'report traversal bound');
    for (const spec of suite.specs || []) {
      check(typeof spec.title === 'string' && spec.title.length <= 1024 && typeof spec.file === 'string' && spec.file.endsWith('static-shell.spec.mjs'),'unexpected discovered source');
      for (const test of spec.tests || []) {check(++visits <= 4096 && projects.includes(test.projectName),'unexpected project'); found.push({id:test.projectName+'|'+spec.title,spec,test});}
    }
    for (const child of suite.suites || []) pending.push([child,depth+1]);
  }
  check(found.length === 48 && new Set(found.map(item => item.id)).size === 48,'exactly 48 distinct discovered cases required');
  for (const project of projects) check(found.filter(item => item.test.projectName === project).length === 3,'three cases required in every project');
  return found;
}
async function attachmentJSON(attachment, root) {
  check(attachment.contentType === 'application/json','unexpected proof attachment MIME');
  let bytes;
  if (typeof attachment.body === 'string') {
    check(attachment.body.length <= 3 << 20 && /^[A-Za-z0-9+/]*={0,2}$/.test(attachment.body),'proof attachment bound/encoding');
    bytes = Buffer.from(attachment.body,'base64');
    check(bytes.toString('base64') === attachment.body,'noncanonical proof body encoding');
  } else {
    check(typeof attachment.path === 'string' && isAbsolute(attachment.path),'proof attachment path required');
    const rel = relative(join(root,'browser'),attachment.path);
    check(!isAbsolute(rel) && rel !== '..' && !rel.startsWith('..'+sep),'proof outside owned browser results');
    bytes = await readBounded(attachment.path,2 << 20);
  }
  check(bytes.length <= 2 << 20,'proof attachment byte cap');
  return JSON.parse(bytes.toString('utf8'));
}
async function validateResults(root,build,discovery) {
  const report = await json(join(root,'report','results.json'),16 << 20), results = testsIn(report);
  check(report.config?.version === '1.64.1' && report.config.workers === 1 && report.config.maxFailures === 1,'actual Playwright version/concurrency/failure policy mismatch');
  check(report.stats?.expected === 48 && report.stats.unexpected === 0 && report.stats.flaky === 0 && report.stats.skipped === 0,'48 passes with zero failures/flakes/skips required');
  check(JSON.stringify(results.map(item=>item.id).sort()) === JSON.stringify(discovery.map(item=>item.id).sort()),'execution differs from exact discovery');
  const closures = [];
  for (const {id,test} of results) {
    check(test.expectedStatus === 'passed' && test.status === 'expected' && test.results?.length === 1 && test.results[0].status === 'passed' && test.results[0].retry === 0,'each case requires one actual passing attempt with zero retries');
    const variant = variants.find(value => test.projectName.startsWith(value.toLowerCase()+'-')), artifact = build.artifacts[variant];
    const attachments = test.results[0].attachments.filter(item => item.name === 'actual-static-files-and-owned-listener-proof');
    check(attachments.length === 1,'one actual static-host cleanup proof required per case');
    const proof = await attachmentJSON(attachments[0],root), close = proof.closure;
    check(proof.root === artifact.root && proof.manifestSHA256 === artifact.manifestSHA256 && proof.inventorySHA256 === artifact.hostInventorySHA256 && proof.topology === 'in-process prebuilt static files only','case/artifact identity mismatch');
    check(proof.listening === false && proof.sockets === 0 && proof.responseStreams === 0 && proof.handlers === 0 && proof.fileOperations?.length === 0 && proof.fileCloseErrors?.length === 0 && proof.overflow === 0 && proof.listenerErrors?.length === 0 && proof.expired === false,'actual static fixture cleanup failure');
    check(close && close.budgetMS === 5000 && close.deadline-close.startedAt === 5000 && close.serverCallbackJoined === true && close.socketJoinsComplete === true && close.handlerJoinsComplete === true && close.additionalJoinRequested === true && close.additionalJoinComplete === true && close.timedOut === false && close.failed === false && close.outerControllerTerminationRequired === false && close.listening === false && close.sockets === 0 && close.handlers === 0 && close.responseStreams === 0 && close.fileOperations?.length === 0 && close.fileCloseErrors?.length === 0 && close.probe?.started === true && close.probe.closeJoined === true && close.probe.result === 'ECONNREFUSED','physical deadline/join/refusal proof missing');
    closures.push({id,manifestSHA256:proof.manifestSHA256,port:close.port,ownerPID:proof.ownerPID,joined:true});
  }
  return {discovered:48,passed:48,skipped:0,retries:0,unexpected:0,closures};
}

async function versionProbe() {
  const {chromium,firefox,webkit} = await import('playwright');
  const actual = {};
  for (const [name,type] of Object.entries({chromium,firefox,webkit})) {
    const browser = await type.launch({headless:true});
    try {actual[name] = browser.version();} finally {await browser.close();}
  }
  process.stdout.write(JSON.stringify({node:process.version,platform:process.platform,arch:process.arch,browsers:actual}));
}

async function main() {
  if (process.argv[2] === '--version-probe') return versionProbe();
  const {root,source} = await ownedRoot(), build = await json(join(root,'build.json'),8 << 20);
  const proof = {version:1,status:'RUNNING',scope:'Linux AMD64 functional CI; not ARM64 resource/device/Cloud qualification',sdk:build.sdk?.version,sourceSHA256:build.sourceSHA256,discovered:null,passed:null};
  await writeJSON(join(root,'browser-proof.json'),proof);
  try {
    check(build.status === 'BUILT' && build.steps.length === 7 && build.checkModeNoWrite === true && build.generated.length === 4,'complete seven-step producer evidence required');
    check(JSON.stringify(build.sourceFiles) === JSON.stringify(await inventory(source,true)),'source changed between producer and browser consumer');
    const env = commandEnvironment(root,{NEXTGO_STATIC_SHELL_LEASE_MS:'90000',NEXTGO_E2E_RESULTS:join(root,'browser'),PLAYWRIGHT_BROWSERS_PATH:join(root,'browsers')});
    for (const variant of variants) {
      const artifact = build.artifacts[variant];
      const current = artifact && await inventory(artifact.root);
      check(artifact && sameContent(artifact.files,current) && current.every(file => file.mode === 0o444),'read-only actual export changed before browser phase');
      env['NEXTGO_STATIC_SHELL_'+variant+'_EXPORT'] = artifact.root;
      env['NEXTGO_STATIC_SHELL_'+variant+'_MANIFEST_SHA256'] = artifact.manifestSHA256;
    }
    const metadata = await runChild(root,'actual-browser-versions',process.execPath,[join(source,'tests','static-shell-ci-browser.mjs'),'--version-probe'],{cwd:source,env,timeoutMS:120000,stdoutLimit:64 << 10});
    proof.actualRuntime = JSON.parse(metadata.toString('utf8'));
    check(proof.actualRuntime.node === 'v24.22.1' && proof.actualRuntime.platform === 'linux' && proof.actualRuntime.arch === 'x64' && profiles.slice(0,3).every(name => typeof proof.actualRuntime.browsers[name] === 'string' && proof.actualRuntime.browsers[name].length > 0),'actual Node/browser metadata missing or incompatible');
    const cli = join(source,'node_modules','@playwright','test','cli.js');
    const common = [cli,'test','--config=tests/static-shell.config.mjs','--workers=1','--retries=0','--max-failures=1'];
    await runChild(root,'discover',process.execPath,[...common,'--list','--reporter=json'],{cwd:source,env:{...env,PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report','discovery.json')},timeoutMS:120000});
    const discovery = testsIn(await json(join(root,'report','discovery.json'),16 << 20));
    proof.discovered = discovery.length;
    await writeJSON(join(root,'browser-proof.json'),proof);
    await runChild(root,'browser',process.execPath,[...common,'--reporter=line,json'],{cwd:source,env:{...env,PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report','results.json')},timeoutMS:16*60000});
    Object.assign(proof,await validateResults(root,build,discovery));
    for (const variant of variants) {const current = await inventory(build.artifacts[variant].root); check(sameContent(build.artifacts[variant].files,current) && current.every(file => file.mode === 0o444),'read-only consumer changed actual exported bytes/permissions');}
    check(JSON.stringify(build.sourceFiles) === JSON.stringify(await inventory(source,true)),'browser phase mutated source');
    proof.status = 'PASSED';
  } catch (error) {proof.status='FAILED'; proof.failure=String(error.message).slice(0,2048); throw error;}
  finally {await writeJSON(join(root,'browser-proof.json'),proof);}
}
if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) main().catch(error => {console.error(String(error.stack || error)); process.exitCode=1;});
