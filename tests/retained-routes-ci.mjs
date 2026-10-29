// Dedicated development-only qualification owner. No framework/runtime import.
// Its source packet is unqualified until a bound campaign records all checks.
import {spawn} from 'node:child_process';
import {constants} from 'node:fs';
import {chmod,lstat,mkdir,mkdtemp,open,readdir,realpath,rm,writeFile} from 'node:fs/promises';
import {basename,delimiter,dirname,isAbsolute,join,relative,resolve,sep} from 'node:path';
import {pathToFileURL,fileURLToPath} from 'node:url';
import {createHash,randomUUID} from 'node:crypto';
import {gzipSync} from 'node:zlib';
import {performance} from 'node:perf_hooks';
import {probeRuntimeAssets} from './retained-routes-assets.mjs';
import {check,sha,readBounded,writeJSON,json,inventory,commandEnvironment} from './static-shell-ci-build.mjs';
import {nativeTarget,nativeToolPaths,assertNativeELF,assertNativeGoEnvironment,assertNativeBuildInfo} from './retained-routes-target.mjs';
import {observeLinuxProcessGroup,attachGroupCensus} from './retained-process-group-census.mjs';
import {groupDrainLimits,drainClosedProcessGroup,attachGroupDrain,validGroupDrainObservation} from './retained-command-group-drain.mjs';
const selectedTarget=()=>nativeTarget(process.env.NEXTGO_RETAINED_NATIVE_ARCH,process.platform,process.arch);
const browserPath=root=>selectedTarget().goArch==='amd64'?'/ms-playwright':join(root,'playwright');

const self=fileURLToPath(import.meta.url),profiles=['chromium','firefox','webkit','webkit-touch'];
const moduleFilter='^(TestModuleTarget|TestModuleLinkStatus|TestMetadata|TestClientView|TestClientFallback|TestRefresh|TestFormFieldValidation|TestFormValidationStatus|TestFieldValidation|TestOptionalChoice|TestOffline|TestSavedAssetURL|TestProps|TestMerge|TestModuleHostRegistrationPreservesDocumentModuleOwnership$)';
const apps=Object.freeze({
  retained:Object.freeze({id:'retained',example:'./examples/retained',config:'tests/retained-routes.config.mjs',file:'retained-routes.spec.mjs',titles:'browserTitles',perProfile:18,cases:72,attachment:'retained-native-process-proof',discovery:'discovery.json',results:'results.json',assetProof:'asset-proof.json'}),
  merge:Object.freeze({id:'merge',example:'./examples/merge',config:'tests/merge-config.mjs',file:'merge-spec.mjs',titles:'mergeBrowserTitles',perProfile:18,cases:72,attachment:'merge-native-process-proof',discovery:'merge-discovery.json',results:'merge-results.json',assetProof:'merge-asset-proof.json'}),
});
const appOrder=Object.freeze(['retained','merge']);
const lazyModuleTitle='actual lazy ordinary Go source revokes loaded and download-pending owners and recovers fresh same-document scopes';
const lazyModuleAttachment='merge-lazy-module-proof';
const generated=appOrder.flatMap(app=>['routes/nextgo_gen.go','actionrefs/nextgo_gen.go','actionrefs/nextgo_gen_js.go','actionrefs/nextgo_gen_server.go'].map(path=>'examples/'+app+'/'+path));
const outside=(parent,path)=>{const value=relative(parent,path);return isAbsolute(value)||value==='..'||value.startsWith('..'+sep);};
const hex=value=>typeof value==='string'&&/^[a-f0-9]{64}$/.test(value);
const content=items=>items.map(({path,bytes,sha256})=>({path,bytes,sha256}));
const shellQuote=value=>"'"+value.replaceAll("'","'\\''")+"'";
const groupPresent=pid=>{try{process.kill(-pid,0);return true;}catch(error){if(error.code==='ESRCH')return false;throw error;}};
// Own the deadline timer even when the real child closes first. A settled
// race alone would otherwise leave its losing timeout callback scheduled.
function waitForCommandClose(completion,timeoutMS,timers={setTimeout,clearTimeout}){
  check(Number.isInteger(timeoutMS)&&timeoutMS>0&&timeoutMS<=3000&&typeof timers.setTimeout==='function'&&typeof timers.clearTimeout==='function','finite command-close wait required');
  return new Promise((resolve,reject)=>{
    let handle,armed=false,pending,settled=false;
    const finish=(closed,error)=>{
      if(settled)return;
      if(!armed){pending={closed,error};return;}
      settled=true;
      try{if(handle!==undefined)timers.clearTimeout(handle);}catch(failure){error=error||failure;}
      if(error)reject(error);else resolve(closed);
    };
    try{handle=timers.setTimeout(()=>finish(false),timeoutMS);armed=true;if(pending)finish(pending.closed,pending.error);Promise.resolve(completion).then(()=>finish(true),error=>finish(false,error));}
    catch(error){armed=true;finish(false,error);}
  });
}
// This helper does not release the command on leader close. Only an attached,
// physically joined ESRCH observation can establish this limited group credit.
async function joinClosedCommandGroup(proof,options,io){
  proof.groupEmpty=false;
  const result=await drainClosedProcessGroup(proof.pid,{...options,closed:proof.closed===true},io);
  try{attachGroupDrain(proof,result);}catch{proof.cleanupDeferred=true;return result;}
  const unchangedAuthority=proof.closed===true&&!proof.timedOut&&!proof.interrupted&&!proof.outputExceeded&&!proof.cleanupDeferred&&!options.signal?.aborted&&(!options.canCredit||options.canCredit());
  proof.groupEmpty=unchangedAuthority&&result.empty===true;
  if(!proof.groupEmpty)proof.cleanupDeferred=true;
  return result;
}
function validateCommandCompletion(proof,spawnError){
  const drain=proof.groupDrain;
  check(!spawnError&&proof.closed===true&&proof.groupEmpty===true&&proof.cleanupDeferred===false&&proof.cleanupTimedOut===false&&proof.timedOut===false&&proof.interrupted===false&&proof.outputExceeded===false&&proof.code===0&&proof.signal===null,'owned command failed: '+proof.name);
  check(Number.isSafeInteger(proof.pid)&&proof.pid>1&&validGroupDrainObservation(drain)&&drain.version===1&&drain.group===proof.pid&&drain.closed===true&&drain.empty===true&&drain.status==='EMPTY'&&drain.reason==='EMPTY'&&drain.lastOutcome==='ESRCH'&&drain.joined===true&&drain.outstanding===0&&drain.signalAuthority===false&&drain.censusConsulted===false&&drain.processTreeAbsence===false,'owned command lacks joined after-close ESRCH observation: '+proof.name);
  return proof;
}

// Command groups are observed, never used as historical signal authority.
// Unresolved descendants/identity are a failed campaign for outer containment.
async function runChild(root,name,command,args,{cwd,env,timeoutMS,stdoutLimit=1<<20}){
  check(/^[a-z0-9-]+$/.test(name)&&Number.isInteger(timeoutMS)&&timeoutMS>0&&isAbsolute(command)&&await realpath(command)===command,'canonical finite command required');
  const executable=await lstat(command,{bigint:true});check(executable.isFile()&&executable.nlink>=1n&&Boolean(executable.mode&0o111n),'regular command executable required');
  const {checkNativeChild,checkedChildSignal}=await import(pathToFileURL(join(cwd,'tests','retained-routes-native-identity.mjs')).href);
  const proof={name,command,args,pid:null,code:null,signal:null,closed:false,groupEmpty:false,timedOut:false,interrupted:false,outputExceeded:false,cleanupTimedOut:false,termSent:false,killSent:false,identity:null,identityChecks:[],identityUnavailableAfterClose:false,cleanupDeferred:false,stdoutBytes:0,stderrBytes:0};
  const buffers={stdout:[],stderr:[]},drainAbort=new AbortController();let child,watchdog,commandDeadlineMS,closedAtMS,spawnError,resolveClose,initial,expected,stopping,census;
  const closed=new Promise(resolve=>{resolveClose=resolve;});
  // Observe at most once after an explicit positive numeric-group probe. The
  // census is diagnostic only: it cannot establish absence or change drain flags.
  async function recordPresentGroup(){
    if(census)return census;
    census=(async()=>{
      try{attachGroupCensus(proof,await observeLinuxProcessGroup(proof.pid));}
      catch{attachGroupCensus(proof,{version:1,status:'INCOMPLETE',atomic:false,signalAuthority:false,absenceProof:false,group:proof.pid??0,reasons:['OBSERVER_FAILURE'],members:[]});}
    })();return census;
  }
  async function stop(){
    if(stopping)return stopping;
    drainAbort.abort();
    stopping=(async()=>{
      try{
        if(!proof.closed){check(initial,'no verified live command leader; outer controller required');proof.termSent=await checkedChildSignal(child,initial,expected,'SIGTERM',proof.identityChecks);}
        await waitForCommandClose(closed,3000);
        if(!proof.closed){proof.killSent=await checkedChildSignal(child,initial,expected,'SIGKILL',proof.identityChecks);await waitForCommandClose(closed,2000);}
        proof.groupEmpty=proof.pid?!groupPresent(proof.pid):true;
        if(!proof.groupEmpty)await recordPresentGroup();
        proof.cleanupTimedOut=!proof.closed||!proof.groupEmpty;
        check(!proof.cleanupTimedOut,'unresolved command group; outer controller required');
      }catch(error){proof.cleanupDeferred=true;proof.cleanupError=String(error.message).slice(0,512);child?.unref();child?.stdout?.unref?.();child?.stderr?.unref?.();}
    })();return stopping;
  }
  const interrupt=()=>{proof.interrupted=true;void stop();};
  process.on('SIGINT',interrupt);process.on('SIGTERM',interrupt);
  try{
    child=spawn(command,args,{cwd,env,detached:true,stdio:['ignore','pipe','pipe']});proof.pid=child.pid??null;
    expected={pid:child.pid,uid:process.getuid(),executable:{path:command,dev:executable.dev.toString(),inode:executable.ino.toString()}};
    child.once('error',error=>{spawnError=error;});child.once('close',(code,signal)=>{closedAtMS=performance.now();Object.assign(proof,{closed:true,code,signal});resolveClose();});
    for(const stream of ['stdout','stderr']){check(child[stream],'supervised pipe unavailable');child[stream].on('data',chunk=>{
      const key=stream+'Bytes',cap=stream==='stdout'?stdoutLimit:1<<20,used=proof[key]||0,room=Math.max(0,cap-used);
      if(room)buffers[stream].push(chunk.subarray(0,room));proof[key]=used+chunk.length;
      if(proof[key]>cap){proof.outputExceeded=true;void stop();}
    });}
    commandDeadlineMS=performance.now()+timeoutMS;
    watchdog=setTimeout(()=>{proof.timedOut=true;void stop();},timeoutMS);
    try{initial=await checkNativeChild(child,null,expected,'capture',proof.identityChecks);proof.identity=initial;}
    catch(error){
      // A command may finish before /proc is sampled. This gives no signal
      // authority: accept only an already joined successful absent group.
      await waitForCommandClose(closed,50);
      if(proof.closed&&proof.code===0&&!groupPresent(child.pid)){proof.identityUnavailableAfterClose=true;}else{proof.identityFailure=String(error.message).slice(0,512);await stop();}
    }
    while(!proof.closed&&!proof.cleanupDeferred){await waitForCommandClose(closed,50);if(stopping)await stopping;}
    if(stopping)await stopping;
    if(proof.closed&&!stopping&&!proof.cleanupDeferred&&!proof.timedOut&&!proof.interrupted&&!proof.outputExceeded){
      const drain=await joinClosedCommandGroup(proof,{deadlineMS:Math.min(commandDeadlineMS,closedAtMS+groupDrainLimits.maxBudgetMS),signal:drainAbort.signal,canCredit:()=>!stopping});
      // A watchdog or interrupt can request stop while the observation waits.
      // Join that existing cleanup path before writing any terminal receipt.
      if(stopping)await stopping;
      if(!proof.groupEmpty&&drain.samples?.some(sample=>sample.outcome==='PRESENT'))await recordPresentGroup();
    }else{
      // Existing failure/stop budgets and exact live-child signal authority are
      // unchanged. They gain no extra drain interval after a watchdog/cap/signal.
      proof.groupEmpty=proof.pid?!groupPresent(proof.pid):true;
      if(!proof.groupEmpty){proof.cleanupDeferred=true;await recordPresentGroup();}
    }
    validateCommandCompletion(proof,spawnError);
    return Buffer.concat(buffers.stdout);
  }finally{
    if(child?.pid&&!proof.closed&&!proof.cleanupDeferred)await stop();
    if(stopping)await stopping;
    drainAbort.abort();
    clearTimeout(watchdog);process.off('SIGINT',interrupt);process.off('SIGTERM',interrupt);
    await writeFile(join(root,'logs',name+'.stdout'),Buffer.concat(buffers.stdout),{mode:0o600});
    await writeFile(join(root,'logs',name+'.stderr'),Buffer.concat(buffers.stderr),{mode:0o600});
    await writeJSON(join(root,'logs',name+'.proof.json'),proof,64<<10);
  }
}

async function owned(){
  const root=process.env.NEXTGO_RETAINED_CI;
  check(isAbsolute(root||'')&&await realpath(root)===root&&basename(root).startsWith('nextgo-retained-ci.'),'canonical owned root required');
  const source=await realpath(process.env.GITHUB_WORKSPACE||process.cwd()),temp=await realpath(process.env.RUNNER_TEMP||'');
  const info=await lstat(root),marker=await json(join(root,'owner.json'),4096);
  check(info.isDirectory()&&!info.isSymbolicLink()&&info.uid===process.getuid()&&!(info.mode&0o077)&&outside(source,root)&&!outside(temp,root),'foreign evidence root');
  check(marker.purpose==='retained-native-ci-v1'&&marker.root===root&&marker.source===source&&marker.run===process.env.GITHUB_RUN_ID&&marker.attempt===process.env.GITHUB_RUN_ATTEMPT&&marker.job===process.env.GITHUB_JOB&&marker.dev===String(info.dev)&&marker.inode===String(info.ino),'owner marker mismatch');
  return {root,source};
}
// Pure schema seam used by init and rejection tests. It cannot establish a
// runtime PASS; goResults still requires each real named test completion.
function validateQualificationRequirements(contract){
  for(const target of ['native','core','devcore','module','devmodule','portable','ui'])check(Array.isArray(contract.tests?.[target])&&contract.tests[target].length>0&&contract.tests[target].length<=256&&new Set(contract.tests[target]).size===contract.tests[target].length&&contract.tests[target].every(name=>/^Test[A-Za-z0-9_]+$/.test(name)),'named target PASS contract: '+target);
  for(const app of Object.values(apps))check(Array.isArray(contract[app.titles])&&contract[app.titles].length===app.perProfile&&new Set(contract[app.titles]).size===app.perProfile&&contract[app.titles].every(title=>typeof title==='string'&&title.length<=1024),'exact reviewed browser titles required: '+app.id);
  check(contract.mergeBrowserTitles.includes(lazyModuleTitle),'real generated lazy module case is mandatory');
}
async function init(){
  const target=selectedTarget();check(process.version==='v24.22.1','exact native Node24.22.1 required');
  const source=await realpath(process.env.GITHUB_WORKSPACE||process.cwd()),temp=await realpath(process.env.RUNNER_TEMP||'');
  const root=await mkdtemp(join(temp,'nextgo-retained-ci.'));await chmod(root,0o700);
  check(outside(source,root)&&!/[\s'"\\]/.test(root),'owned output must be outside checkout and shell-wrapper-safe');
  for(const name of ['logs','tmp','go-cache','mod-cache','npm-cache','home','browser-home','browser','report','evidence','dist','exec'])await mkdir(join(root,name),{mode:0o700});
  const info=await lstat(root);
  await writeJSON(join(root,'owner.json'),{purpose:'retained-native-ci-v1',root,source,run:process.env.GITHUB_RUN_ID,attempt:process.env.GITHUB_RUN_ATTEMPT,job:process.env.GITHUB_JOB,uid:process.getuid(),dev:String(info.dev),inode:String(info.ino)},4096);
  check(typeof process.env.GITHUB_ENV==='string','GitHub environment file required');
  await writeFile(process.env.GITHUB_ENV,'NEXTGO_RETAINED_CI='+root+'\n',{flag:'a'});
  const before=await inventory(source,true);await writeJSON(join(root,'source-before.json'),before,8<<20);
  const contract=await json(join(source,'tests','retained-routes-ci-inputs.json'),1<<20);
  check(contract.ready===true,'reviewed corrected input rebind required before this workflow may execute');
  check(contract.version===1&&contract.scope==='Linux native HTTP only; explicit amd64 or arm64'&&JSON.stringify(contract.nativeArchitectures)===JSON.stringify(['amd64','arm64'])&&contract.baseCommit==='768bbb712e9a285b6b8252469c817b6a922739a5'&&Array.isArray(contract.files)&&contract.files.length>34&&contract.files.length<=207,'reviewed input contract required');
  validateQualificationRequirements(contract);
  const seen=new Set();
  for(const file of contract.files){check(typeof file.path==='string'&&file.path.length<=512&&!isAbsolute(file.path)&&!/[\\\n\r]/.test(file.path)&&file.path.split('/').every(part=>part&&part!=='.'&&part!=='..')&&hex(file.sha256)&&!seen.has(file.path),'input path/hash contract');seen.add(file.path);check(sha(await readBounded(join(source,file.path),16<<20))===file.sha256,'source pin changed: '+file.path);}
  for(const path of ['tests/retained-routes.spec.mjs','tests/retained-owner-scroll-observation.mjs','tests/retained-routes.config.mjs','tests/retained-routes-fixture.mjs','tests/retained-routes-native-identity.mjs','tests/retained-routes-native-identity.test.mjs','tests/retained-routes-fixture.test.mjs','tests/static-shell-ci-build.mjs','tests/retained-routes-ci.mjs','tests/retained-routes-ci.test.mjs','tests/retained-process-group-census.mjs','tests/retained-process-group-census.test.mjs','tests/retained-command-group-drain.mjs','tests/retained-command-group-drain.test.mjs','tests/retained-routes-target.mjs','.github/workflows/retained-routes-browser.yml','.github/workflows/retained-routes-source.yml','tests/merge-config.mjs','tests/merge-fixture.mjs','tests/merge-spec.mjs','tests/retained-routes-assets.mjs','tests/retained-routes-assets.test.mjs','scripts/test-wasm.sh','ui/utf8.go','ui/ui.go','ui/stream.go','ui/request_region.go','ui/utf8_test.go','retention_utf8_test.go','package.json','package-lock.json','go.mod'])check(seen.has(path),'missing producer/test/dependency pin: '+path);
  await writeJSON(join(root,'inputs.json'),contract,1<<20);
  await writeJSON(join(root,'campaign.json'),{version:1,status:'INITIALIZED',scope:contract.scope,nativeTarget:target.goArch,resourceEnvelope:target.goArch==='amd64'?'container: cpu2 memory4GiB no-extra-swap pids768 shm512MiB':'standard public ubuntu-24.04-arm hosted job; container hard limits not asserted',sourceCommit:process.env.GITHUB_SHA,run:process.env.GITHUB_RUN_ID,attempt:process.env.GITHUB_RUN_ATTEMPT,sdkRequested:process.env.GO_VERSION,inputContractSHA256:sha(await readBounded(join(source,'tests','retained-routes-ci-inputs.json'),1<<20)),sourceContentSHA256:sha(Buffer.from(JSON.stringify(content(before)))),steps:[],unqualified:['serverless mock Runtime modes','other architectures/platforms and separate SDK/arch cells','cross-platform/resource/security release gates','full V10/79 capabilities/11 gates']},8<<20);
}
function childEnv(root,source,extras={}){
  return commandEnvironment(root,{HOME:join(root,'home'),TMPDIR:join(root,'tmp'),PLAYWRIGHT_BROWSERS_PATH:browserPath(root),NEXTGO_RETAINED_NATIVE_ARCH:selectedTarget().goArch,PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD:'1',npm_config_cache:join(root,'npm-cache'),npm_config_userconfig:'/dev/null',GIT_CONFIG_COUNT:'1',GIT_CONFIG_KEY_0:'safe.directory',GIT_CONFIG_VALUE_0:source,GITHUB_WORKSPACE:source,RUNNER_TEMP:process.env.RUNNER_TEMP,GITHUB_RUN_ID:process.env.GITHUB_RUN_ID,GITHUB_RUN_ATTEMPT:process.env.GITHUB_RUN_ATTEMPT,GITHUB_JOB:process.env.GITHUB_JOB,NEXTGO_RETAINED_CI:root,...extras});
}
async function executablePath(name){for(const directory of process.env.PATH.split(delimiter)){try{const path=await realpath(join(directory,name));if((await lstat(path)).isFile())return path;}catch{}}throw Error('required executable missing: '+name);}
async function sdk(root,source){
  let go;
  for(const path of process.env.PATH.split(delimiter)){try{const value=await realpath(join(path,'go'));if((await lstat(value)).isFile()){go=value;break;}}catch{}}
  check(go,'Go executable missing');const sdkRoot=dirname(dirname(go));
  const version=(await readBounded(join(sdkRoot,'VERSION'),4096)).toString('utf8');
  check(['1.24.13','1.27.3'].includes(process.env.GO_VERSION)&&version.split('\n')[0]==='go'+process.env.GO_VERSION,'exact approved SDK required');
  if(process.env.GOROOT)check(await realpath(process.env.GOROOT)===sdkRoot,'PATH/GOROOT mismatch');
  const target=selectedTarget(),files={};for(const name of nativeToolPaths(target)){const data=await readBounded(join(sdkRoot,name),128<<20);if(name.startsWith('bin/')||name.startsWith('pkg/tool/'))assertNativeELF(data,target);files[name]={bytes:data.length,sha256:sha(data)};}
  const node=await realpath(process.execPath),data=await readBounded(node,128<<20);assertNativeELF(data,target);
  return {go,root:sdkRoot,version:version.split('\n')[0],files,node:{path:node,version:process.version,sha256:sha(data)},platform:process.platform,architecture:process.arch,nativeTarget:target.goArch,uid:process.getuid()};
}
async function unchanged(source,before){check(JSON.stringify(before)===JSON.stringify(await inventory(source,true)),'source changed outside explicit generation');}
async function step(state,name,command,args,env,timeoutMS,stdoutLimit=1<<20){
  const {root,source,report}=state;report.steps.push({name,command,args,timeoutMS,stdoutLimit});await writeJSON(join(root,'campaign.json'),report,8<<20);
  return runChild(root,name,command,args,{cwd:source,env,timeoutMS,stdoutLimit});
}
async function dependencies(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20);
  report.git=await executablePath('git');
  const head=await step({root,source,report},'checkout-head',report.git,['-C',source,'rev-parse','HEAD'],childEnv(root,source),60000,64<<10);
  report.checkoutHead=head.toString('utf8').trim();check(report.checkoutHead===report.sourceCommit&&/^[a-f0-9]{40}$/.test(report.checkoutHead),'checked-out commit mismatch');
  const npm=await realpath(join(dirname(process.execPath),'npm'));
  report.npm={path:npm,sha256:sha(await readBounded(npm,4<<20))};
  const version=await step({root,source,report},'npm-version',await realpath(process.execPath),[npm,'--version'],childEnv(root,source),60000,64<<10);
  check(/^\d+\.\d+\.\d+\n$/.test(version.toString('utf8')),'actual npm version missing');report.npm.version=version.toString('utf8').trim();
  await step({root,source,report},'npm-ci',await realpath(process.execPath),[npm,'ci','--ignore-scripts','--no-audit','--no-fund'],childEnv(root,source),6*60000);
  check(sha(await readBounded(npm,4<<20))===report.npm.sha256,'npm producer changed');
  check((await json(join(source,'node_modules','@playwright','test','package.json'),64<<10)).version==='1.64.1','locked Playwright mismatch');
  await unchanged(source,await json(join(root,'source-before.json'),8<<20));report.status='DEPENDENCIES_READY';await writeJSON(join(root,'campaign.json'),report,8<<20);
}

async function installBrowsers(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20);
  check(selectedTarget().goArch==='arm64'&&report.status==='DEPENDENCIES_READY'&&process.getuid()!==0,'native nonroot ARM dependency phase required');
  const cli=join(source,'node_modules','@playwright','test','cli.js');
  await step({root,source,report},'install-native-browsers',process.execPath,[cli,'install','--with-deps','chromium','firefox','webkit'],childEnv(root,source),55*60000,4<<20);
  await unchanged(source,await json(join(root,'source-before.json'),8<<20));
  report.browserInstallation={mode:'locked Playwright supported native ARM binaries plus system dependencies',path:browserPath(root),status:'COMMAND_CLOSED_0'};
  await writeJSON(join(root,'campaign.json'),report,8<<20);
}

// -exec is transparent: metadata never enters test stdout or alters test args.
// The test stays in its Go-owned package cwd and supervisor process group.
async function testExec(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20),binary=process.argv[3],args=process.argv.slice(4);
  check(report.sdk&&isAbsolute(binary||'')&&await realpath(binary)===binary&&!outside(root,binary)&&!outside(source,await realpath(process.cwd())),'test executable outside owned build/source roots');
  const mode=process.env.NEXTGO_RETAINED_TEST_MODE,phase=process.env.NEXTGO_RETAINED_TEST_PHASE;
  check(['native','wasm'].includes(mode)&&/^[a-z0-9-]+$/.test(phase||'')&&args.length<=64,'test execution identity');
  const before=await testArtifact(binary),wasm=before.header.startsWith('0061736d01000000');
  check((mode==='wasm')===wasm,'test compiler target/magic mismatch');if(mode==='native')assertNativeELF(Buffer.from(before.header,'hex'),selectedTarget());
  const executable=mode==='wasm'?join(report.sdk.root,'lib/wasm/go_js_wasm_exec'):binary;
  const argv=mode==='wasm'?[binary,...args]:args;
  const packageDirectory=relative(source,await realpath(process.cwd())).split(sep).join('/');
  const proof={version:1,phase,mode,nativeTarget:selectedTarget().goArch,cwd:await realpath(process.cwd()),package:'github.com/Suhaibinator/Next.go'+(packageDirectory?'/'+packageDirectory:''),binary,bytes:before.bytes,sha256:before.sha256,header:before.header,dev:before.dev,inode:before.inode,runner:executable,args:argv,pid:null,closed:false,code:null,signal:null,unchanged:false};
  let child;try{
    child=spawn(executable,argv,{cwd:process.cwd(),env:process.env,stdio:'inherit',detached:false});proof.pid=child.pid??null;
    await new Promise((resolve,reject)=>{child.once('error',reject);child.once('close',(code,signal)=>{proof.closed=true;proof.code=code;proof.signal=signal;resolve();});});
    const after=await testArtifact(binary);proof.unchanged=after.sha256===before.sha256&&after.dev===before.dev&&after.inode===before.inode;
    check(proof.closed&&proof.signal===null&&proof.unchanged,'test executable/close evidence failed');process.exitCode=proof.code??1;
  }finally{await writeJSON(join(root,'exec',phase+'-'+randomUUID()+'.json'),proof,64<<10);}
}
async function testArtifact(path){
  const before=await lstat(path,{bigint:true});check(before.isFile()&&!before.isSymbolicLink()&&before.nlink>=1n&&before.size>=20n&&before.size<=128n<<20n,'bounded compiler-produced executable required');
  const file=await open(path,constants.O_RDONLY|constants.O_NOFOLLOW|constants.O_NONBLOCK);
  try{
    const opened=await file.stat({bigint:true});check(opened.dev===before.dev&&opened.ino===before.ino&&opened.size===before.size,'test executable changed before read');
    const hash=createHash('sha256'),chunk=Buffer.alloc(64<<10);let bytes=0,header='';
    for(;;){const part=await file.read(chunk,0,chunk.length,null);if(!part.bytesRead)break;if(!bytes)header=chunk.subarray(0,20).toString('hex');bytes+=part.bytesRead;check(bytes<=128<<20,'test executable grew');hash.update(chunk.subarray(0,part.bytesRead));}
    const after=await file.stat({bigint:true}),name=await lstat(path,{bigint:true});check(BigInt(bytes)===before.size&&after.size===before.size&&after.mtimeNs===before.mtimeNs&&name.dev===before.dev&&name.ino===before.ino,'test executable changed during read');
    return {bytes,sha256:hash.digest('hex'),header,dev:before.dev.toString(),inode:before.ino.toString()};
  }finally{await file.close();}
}
function goResults(bytes,required=[],allowedSkips=[]){
  const packages=new Map(),tests=new Map(),skipped=[];let events=0;
  for(const line of bytes.toString('utf8').split('\n')){if(!line)continue;check(++events<=250000&&Buffer.byteLength(line)<=1<<20,'Go JSON event bound');const value=JSON.parse(line);check(typeof value.Package==='string'&&value.Package.length<=512,'Go package identity');if(value.Action==='fail')throw Error('Go failure: '+value.Package+'/'+(value.Test||''));if(value.Test){check(value.Test.length<=1024,'Go test name bound');const key=value.Package+'|'+value.Test;const item=tests.get(key)||{package:value.Package,name:value.Test,run:0,pass:0,skip:0};if(['run','pass','skip'].includes(value.Action))item[value.Action]++;tests.set(key,item);if(value.Action==='skip')skipped.push(key);}else if(['pass','skip'].includes(value.Action)){check(!packages.has(value.Package),'duplicate Go package completion');packages.set(value.Package,value.Action);}}
  check(packages.size>0,'no actual Go package completions');
  for(const item of tests.values())check(item.run===1&&item.pass+item.skip===1&&item.pass<=1&&item.skip<=1,'incomplete or repeated Go test result: '+item.package+'|'+item.name);
  for(const name of required){const found=[...tests.values()].filter(test=>test.name===name);check(found.length===1&&found[0].run===1&&found[0].pass===1&&found[0].skip===0,'required actual Go PASS missing: '+name);}
  for(const name of skipped)check(allowedSkips.some(allowed=>name===allowed),'unexpected Go test skip: '+name);
  return {events,packages:[...packages],tests:[...tests.values()],skipped,requiredPasses:required};
}
async function units(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20),inputs=await json(join(root,'inputs.json'),1<<20);
  check(report.status==='DEPENDENCIES_READY','dependency phase required');report.sdk=await sdk(root,source);await writeJSON(join(root,'campaign.json'),report,8<<20);
  const state={root,source,report},env=childEnv(root,source,{GOROOT:report.sdk.root,PATH:dirname(report.sdk.go)+delimiter+dirname(report.sdk.node.path)+delimiter+'/usr/bin:/bin',CGO_ENABLED:'1'});
  const cc=await executablePath('gcc'),compiler=await testArtifact(cc);assertNativeELF(Buffer.from(compiler.header,'hex'),selectedTarget());env.CC=cc;
  const ccVersion=await step(state,'cc-version',cc,['--version'],env,60000,64<<10);report.nativeCompiler={path:cc,sha256:compiler.sha256,bytes:compiler.bytes,version:ccVersion.toString('utf8')};
  const version=await step(state,'go-version',report.sdk.go,['version'],env,60000,64<<10);report.goVersion=version.toString('utf8');check(report.goVersion==='go version go'+process.env.GO_VERSION+' linux/'+selectedTarget().goArch+'\n','actual executing SDK version mismatch');
  const goEnv=await step(state,'go-environment',report.sdk.go,['env','-json','GOOS','GOARCH','GOHOSTOS','GOHOSTARCH','GOROOT','GOVERSION','GOTOOLCHAIN','GOWORK','CGO_ENABLED','CC','GOFLAGS','GOCACHE','GOMODCACHE'],env,60000,64<<10);report.goEnvironment=JSON.parse(goEnv.toString('utf8'));assertNativeGoEnvironment(report.goEnvironment,selectedTarget());
  check(report.goEnvironment.GOOS==='linux'&&report.goEnvironment.GOARCH===selectedTarget().goArch&&report.goEnvironment.GOROOT===report.sdk.root&&report.goEnvironment.GOVERSION===report.sdk.version&&report.goEnvironment.GOTOOLCHAIN==='local'&&report.goEnvironment.GOWORK==='off'&&report.goEnvironment.CGO_ENABLED==='1'&&report.goEnvironment.CC===cc&&report.goEnvironment.GOCACHE===join(root,'go-cache')&&report.goEnvironment.GOMODCACHE===join(root,'mod-cache'),'actual isolated producer Go environment mismatch');
  const wrapper=join(root,'exec-wrapper.sh');await writeFile(wrapper,'#!/bin/sh\nexec '+shellQuote(process.execPath)+' '+shellQuote(self)+' test-exec "$@"\n',{mode:0o500,flag:'wx'});
  const formatted=await step(state,'format-check',join(report.sdk.root,'bin/gofmt'),['-l',...inputs.files.filter(file=>file.path.endsWith('.go')).map(file=>file.path)],env,60000);check(formatted.length===0,'source requires separate formatting derivative: '+formatted.toString('utf8').slice(0,4096));
  const cli=join(root,'dist','nextgo');await step(state,'build-cli',report.sdk.go,['build','-trimpath','-buildvcs=false','-o',cli,'./cmd/nextgo'],env,8*60000);
  await unchanged(source,await json(join(root,'source-before.json'),8<<20));
  for(const app of appOrder)await step(state,app==='retained'?'generate':'generate-'+app,cli,['generate',apps[app].example],env,60000);
  const after=await inventory(source,true),before=await json(join(root,'source-before.json'),8<<20);
  check(JSON.stringify(before.filter(file=>!generated.includes(file.path)))===JSON.stringify(after.filter(file=>!generated.includes(file.path))),'generation changed unrelated source');
  report.generated=[];for(const path of generated){const data=await readBounded(join(source,path),1<<20);check(data.toString('utf8').startsWith('// Code generated by nextgo'),'generated helper provenance');report.generated.push({path,bytes:data.length,sha256:sha(data)});}
  await writeJSON(join(root,'source-generated.json'),after,8<<20);for(const app of appOrder)await step(state,app==='retained'?'generate-check':'generate-check-'+app,cli,['generate','--check',apps[app].example],env,60000);await unchanged(source,after);
  const summaries={};
  async function tests(name,args,extra,required,allowed=[]){const output=await step(state,name,report.sdk.go,['test','-count=1','-json','-timeout=10m','-exec='+wrapper,...args],{...env,...extra,NEXTGO_RETAINED_TEST_PHASE:name,NEXTGO_RETAINED_TEST_MODE:name==='native'?'native':'wasm'},name==='native'?45*60000:15*60000,32<<20);summaries[name]=goResults(output,required,allowed);await writeJSON(join(root,'go-summary.json'),summaries,16<<20);await unchanged(source,after);}
  // Native workspace/provenance regressions exercise Go's normal discovery and
  // VCS stamping. Keep the isolated build policy for every other phase.
  const nativeTestPolicy=Object.freeze({...inputs.nativeTestEnvironmentPolicy});
  check(Object.keys(nativeTestPolicy).sort().join(',')==='GOENV,GOFLAGS,GOWORK'&&nativeTestPolicy.GOENV==='off'&&nativeTestPolicy.GOWORK==='auto'&&nativeTestPolicy.GOFLAGS==='-p=1','reviewed native test environment policy required');
  report.nativeTestRequestedPolicy={...nativeTestPolicy};
  const nativeTestEnv={...env,...nativeTestPolicy};
  check(nativeTestEnv.GOENV==='off','native tests must disable the user Go environment file');
  const nativeGoEnv=await step(state,'native-go-environment',report.sdk.go,['env','-json','GOOS','GOARCH','GOHOSTOS','GOHOSTARCH','GOROOT','GOVERSION','GOTOOLCHAIN','GOENV','GOWORK','CGO_ENABLED','CC','GOFLAGS','GOCACHE','GOMODCACHE'],nativeTestEnv,60000,64<<10);
  report.nativeTestGoEnvironment=JSON.parse(nativeGoEnv.toString('utf8'));assertNativeGoEnvironment(report.nativeTestGoEnvironment,selectedTarget());
  await writeJSON(join(root,'campaign.json'),report,8<<20);
  const actualNative=report.nativeTestGoEnvironment;
  // go env GOENV reports the environment-file path, which is empty for GOENV=off.
  check(actualNative.GOOS==='linux'&&actualNative.GOARCH===selectedTarget().goArch&&actualNative.GOROOT===report.sdk.root&&actualNative.GOVERSION===report.sdk.version&&actualNative.GOTOOLCHAIN==='local'&&actualNative.GOENV===''&&actualNative.GOWORK===''&&actualNative.GOFLAGS==='-p=1'&&actualNative.CGO_ENABLED==='1'&&actualNative.CC===cc&&actualNative.GOCACHE===join(root,'go-cache')&&actualNative.GOMODCACHE===join(root,'mod-cache'),'actual native test Go environment mismatch');
  await tests('native',['-race','./...'],nativeTestPolicy,inputs.tests.native,inputs.tests.nativeSkips||[]);
  await step(state,'vet-native',report.sdk.go,['vet','./...'],env,15*60000);await unchanged(source,after);
  const wasm={GOOS:'js',GOARCH:'wasm',CGO_ENABLED:'0'};
  const core=['./client','./mutation','./offline','./offline/worker','./internal/offlinejson','./ui'];
  const dev=['./client','./mutation','./ui'];
  await tests('core',core,wasm,inputs.tests.core);
  await tests('devcore',['-tags=nextgo_devrefresh',...dev],wasm,inputs.tests.devcore);
  for(const [name,tags]of [['module','nextgo_module'],['devmodule','nextgo_module,nextgo_devrefresh']]){
    await tests(name,['-tags='+tags,'./client','-run',moduleFilter],wasm,inputs.tests[name],name==='devmodule'?['github.com/Suhaibinator/Next.go/client|TestRefreshActivityScriptCallerCancelRetainsDownloadCallbacks']:[]);
    check(summaries[name].tests.some(test=>test.pass===1),'selected module suite ran no tests');
    await tests(name+'-retained',['-tags='+tags,'./client','-run','^(TestRetention|TestRetained)'],wasm,inputs.tests.portable);
    await tests(name+'-ui',['-tags='+tags,'./ui'],wasm,inputs.tests.ui);
  }
  for(const [name,tags,packages]of [['core','',core],['devcore','nextgo_devrefresh',dev],['module','nextgo_module',['./client','./ui']],['devmodule','nextgo_module,nextgo_devrefresh',['./client','./ui']]])await step(state,'vet-'+name,report.sdk.go,['vet',...(tags?['-tags='+tags]:[]),...packages],{...env,...wasm},10*60000);
  const receipts=[];for(const name of await readdir(join(root,'exec'))){check(receipts.length<512,'test executable receipt bound');receipts.push(await json(join(root,'exec',name),64<<10));}
  for(const [phase,summary]of Object.entries(summaries)){
    const expected=summary.packages.filter(([,status])=>status==='pass').map(([name])=>name).sort(),actual=receipts.filter(proof=>proof.phase===phase);
    check(JSON.stringify(actual.map(proof=>proof.package).sort())===JSON.stringify(expected),'actual executable/package completion mismatch: '+phase);
    check(actual.every(proof=>proof.closed===true&&proof.code===0&&proof.signal===null&&proof.unchanged===true&&Number.isSafeInteger(proof.pid)&&proof.pid>0&&hex(proof.sha256)&&proof.mode===(phase==='native'?'native':'wasm')),'invalid actual test-executable proof');
  }
  await writeJSON(join(root,'test-executable-summary.json'),receipts,4<<20);
  await step(state,'harness-unit-tests',process.execPath,['--test','tests/retained-routes-native-identity.test.mjs','tests/retained-routes-fixture.test.mjs','tests/retained-routes-ci.test.mjs','tests/link-status-gates.test.mjs','tests/retained-routes-assets.test.mjs','tests/retained-process-group-census.test.mjs','tests/retained-command-group-drain.test.mjs'],env,60000);
  await unchanged(source,after);report.status='UNITS_PASSED';report.cli={path:cli,sha256:sha(await readBounded(cli,128<<20))};await chmod(cli,0o500);await writeJSON(join(root,'campaign.json'),report,8<<20);
}

function validateNativeProof(proof,report,root){
  check(proof&&proof.version===1&&proof.binary===report.server.path&&proof.sha256===report.server.sha256&&proof.bytes===report.server.bytes,'native artifact proof mismatch');
  check(Number.isSafeInteger(proof.pid)&&proof.pid>0&&['closed','joined','shutdownRequested','termSent','cwdRemoved','listenerRefused','groupEmpty'].every(key=>proof[key]===true)&&['forced','unexpectedExit','identityRefused','cleanupDeferred'].every(key=>proof[key]===false)&&proof.code===0&&proof.signal===null,'native cleanup proof incomplete');
  check(proof.platform==='linux'&&proof.requireLinuxIdentity===true&&proof.nativeIdentityVersion===1&&proof.nativeOwnerUID===report.sdk.uid&&proof.identityFailure==='','actual Linux identity enforcement missing');
  check(isAbsolute(proof.cwd)&&proof.cwd.length<=4096&&resolve(proof.cwd)===proof.cwd&&!outside(join(root,'tmp'),proof.cwd)&&basename(proof.cwd).startsWith('nextgo-retained-example-'),'native executable cwd must be owned outside-source tmp');
  check(proof.artifact?.path===report.server.path&&proof.artifact.dev===report.server.dev&&proof.artifact.inode===report.server.inode&&proof.artifact.parent?.path===report.server.parent.path&&proof.artifact.parent.dev===report.server.parent.dev&&proof.artifact.parent.inode===report.server.parent.inode,'cooperatively owned native artifact/parent mismatch');
  const identity=proof.nativeIdentity,expected={pid:proof.pid,uid:report.sdk.uid,executable:{path:report.server.path,dev:report.server.dev,inode:report.server.inode}};
  return {identity,expected};
}
async function strictNativeProof(proof,report,root,source){
  const {identity,expected}=validateNativeProof(proof,report,root);
  const {validateNativeIdentity}=await import(pathToFileURL(join(source,'tests','retained-routes-native-identity.mjs')).href);
  validateNativeIdentity(identity,expected);
  check(Array.isArray(proof.identityChecks)&&proof.identityChecks.length===3,'capture/readiness/TERM receipts required');
  for(const [index,phase]of ['capture','readiness','before-signal'].entries()){const receipt=proof.identityChecks[index];check(receipt.phase===phase&&receipt.outcome===(index===2?'signal-sent':'verified')&&(index!==2||receipt.signal==='SIGTERM'),'unexpected identity/signal receipt');validateNativeIdentity(receipt.identity,expected,identity);}
  return {pid:proof.pid,cwd:proof.cwd,identity,closed:true,groupEmpty:true,listenerRefused:true};
}
async function production(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20);check(report.status==='UNITS_PASSED','unit qualification required');
  const env=childEnv(root,source,{GOROOT:report.sdk.root,CGO_ENABLED:'1',CC:report.nativeCompiler.path}),state={root,source,report};
  check(sha(await readBounded(report.cli.path,128<<20))===report.cli.sha256,'CLI changed');report.servers={};report.productionBuildInfos={};
  for(const id of appOrder){
    const app=apps[id],suffix=id==='retained'?'':'-'+id,output=join(root,'dist',id);
    await step(state,'build-production'+suffix,report.cli.path,['build','--out',output,app.example],env,20*60000);await unchanged(source,await json(join(root,'source-generated.json'),8<<20));
    const path=join(output,'server'),data=await readBounded(path,128<<20),info=await lstat(path,{bigint:true});await chmod(path,0o500);assertNativeELF(data,selectedTarget());
    const parent=await lstat(output,{bigint:true});check(parent.isDirectory()&&await realpath(output)===output&&parent.uid===BigInt(process.getuid())&&(parent.mode&0o022n)===0n,'cooperatively owned producer directory required');
    const server={path,sha256:sha(data),bytes:data.length,dev:info.dev.toString(),inode:info.ino.toString(),parent:{path:output,dev:parent.dev.toString(),inode:parent.ino.toString()}};
    report.servers[id]=server;if(id==='retained')report.server=server;await writeJSON(join(root,'campaign.json'),report,8<<20);
    const metadata=await step(state,'production-build-info'+suffix,report.sdk.go,['version','-m',report.cli.path,path],env,60000,128<<10);report.productionBuildInfos[id]=metadata.toString('utf8');if(id==='retained')report.productionBuildInfo=report.productionBuildInfos[id];for(const file of [report.cli.path,path])assertNativeBuildInfo(report.productionBuildInfos[id],file,process.env.GO_VERSION,selectedTarget());
    await step(state,'asset-probe'+suffix,process.execPath,[self,'asset-probe',id],{...env,NEXTGO_RETAINED_SERVER_BINARY:path,NEXTGO_RETAINED_SERVER_SHA256:server.sha256,NEXTGO_RETAINED_REQUIRE_LINUX_IDENTITY:'1'},2*60000);
    const probe=await json(join(root,app.assetProof),2<<20);check(probe.status==='PASSED'&&probe.app===id&&probe.assets['wasm_exec.js'].sha256===report.sdk.files['lib/wasm/wasm_exec.js'].sha256,'actual per-app embedded SDK runtime mismatch');
    check(probe.assets.css.sha256===sha(await readBounded(join(source,'examples',id,'public',id+'.css'),256<<10)),'actual embedded CSS differs from pinned source');
    for(const name of ['app.wasm','wasm_exec.js','boot.js','css'])check(probe.assets[name].gzip.closed===true&&probe.assets[name].gzip.decodedSHA256===probe.assets[name].sha256&&probe.assets[name].gzip.decodedBytes===probe.assets[name].bytes,'actual per-app gzip/identity asset mismatch');
    await strictNativeProof(probe.native,{...report,server},root,source);
  }
  report.status='PRODUCTION_ATTESTED';await writeJSON(join(root,'campaign.json'),report,8<<20);
}
async function assetProbe(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20),id=process.argv[3];check(Object.hasOwn(apps,id),'explicit qualified application required');
  const app=apps[id],server=report.servers?.[id];check(server&&server.path===process.env.NEXTGO_RETAINED_SERVER_BINARY&&server.sha256===process.env.NEXTGO_RETAINED_SERVER_SHA256,'per-app prebuilt artifact binding required');
  const proof={version:1,app:id,status:'RUNNING',assets:{},native:null};
  const {startRetainedServer}=await import(pathToFileURL(join(source,'tests','retained-routes-fixture.mjs')).href);let native;
  try{
    native=await startRetainedServer();Object.assign(proof,await probeRuntimeAssets(native.origin,id,report.sdk.files['lib/wasm/wasm_exec.js'].sha256));
    await native.close();proof.native=native.snapshot();await strictNativeProof(proof.native,{...report,server},root,source);proof.status='PASSED';
  }catch(error){proof.status='FAILED';proof.failure=String(error.message).slice(0,2048);if(error.retainedProof)proof.native=error.retainedProof;throw error;}
  finally{
    let cleanupError;
    if(native){try{await native.close();}catch(error){cleanupError=error;proof.status='FAILED';proof.cleanupFailure=String(error.message).slice(0,2048);}finally{proof.native=native.snapshot();}}
    await writeJSON(join(root,app.assetProof),proof,2<<20);if(cleanupError)throw cleanupError;
  }
}

function browserCases(report,execution,app=apps.retained){
  check(Object.values(apps).includes(app),'reviewed browser application required');
  check(Array.isArray(report.errors)&&report.errors.length===0,'Playwright global error');const found=[],pending=(report.suites||[]).map(suite=>[suite,0]);let visits=0;
  while(pending.length){const [suite,depth]=pending.pop();check(++visits<=4096&&depth<=16,'browser report bound');for(const spec of suite.specs||[]){check(typeof spec.title==='string'&&spec.title.length<=1024&&typeof spec.file==='string'&&spec.file.endsWith(app.file),'browser source identity');for(const test of spec.tests||[]){check(++visits<=4096&&profiles.some(profile=>test.projectName===app.id+'-'+profile),'browser project identity');if(execution)check(test.expectedStatus==='passed'&&test.status==='expected'&&test.results?.length===1&&test.results[0].status==='passed'&&test.results[0].retry===0,'one actual passing attempt required');found.push({id:test.projectName+'|'+spec.title,test});}}for(const child of suite.suites||[])pending.push([child,depth+1]);}
  check(found.length===app.cases&&new Set(found.map(item=>item.id)).size===app.cases,'exact distinct cases required: '+app.id);for(const profile of profiles)check(found.filter(item=>item.test.projectName===app.id+'-'+profile).length===app.perProfile,'exact cases per profile: '+app.id);
  if(execution)check(report.stats?.expected===app.cases&&report.stats.unexpected===0&&report.stats.flaky===0&&report.stats.skipped===0&&report.config?.workers===1&&report.config.maxFailures===1&&report.config.version==='1.64.1','complete zero-skip/flake/retry matrix required');return found;
}
async function proofAttachment(attachment,root,maximum=2<<20){
  check(maximum===(2<<20)||maximum===4096,'reviewed attachment cap required');
  check(attachment.contentType==='application/json','native proof MIME');let data;
  if(typeof attachment.body==='string'){check(attachment.body.length<=maximum*1.5&&/^[A-Za-z0-9+/]*={0,2}$/.test(attachment.body),'proof body bound/encoding');data=Buffer.from(attachment.body,'base64');check(data.toString('base64')===attachment.body,'canonical proof body');}
  else{check(isAbsolute(attachment.path||'')&&!outside(join(root,'browser'),attachment.path),'proof path outside owned browser output');data=await readBounded(attachment.path,maximum);}
  check(data.length<=maximum,'proof byte limit');return JSON.parse(data.toString('utf8'));
}
function exactKeys(value,names){
  return !!value&&typeof value==='object'&&!Array.isArray(value)&&[Object.prototype,null].includes(Object.getPrototypeOf(value))&&Object.keys(value).sort().join(',')===[...names].sort().join(',');
}
function validateLazyModuleProof(value,prefix,origin){
  check(typeof prefix==='string'&&/^\/_nextgo\/[a-f0-9]{20}\/$/.test(prefix),'actual merge asset-probe build prefix required');
  check(origin==='http://127.0.0.1:4188','hash-pinned native fixture origin required');
  check(exactKeys(value,['version','abi','records'])&&value.version===1&&value.abi===6&&Array.isArray(value.records)&&value.records.length===2&&Buffer.byteLength(JSON.stringify(value))<=4096,'two bounded actual ABI6 module records required');
  let identity;
  for(const [index,record]of value.records.entries()){
    check(exactKeys(record,['phase','url','status','mime','bytes','sha256','complete','wasmMagic','hostABI','buildPrefix']),'exact actual module record schema required');
    check(record.phase===['initial','held-recovery'][index]&&record.status===200&&record.mime==='application/wasm'&&record.complete===true&&record.wasmMagic==='0061736d01000000'&&record.hostABI===6&&record.buildPrefix===prefix,'actual successful module response/host/build evidence required');
    check(Number.isSafeInteger(record.bytes)&&record.bytes>=8&&record.bytes<=8<<20&&hex(record.sha256),'bounded decoded module size/hash required');
    check(typeof record.url==='string'&&record.url.length<=256,'bounded module URL required');
    const url=new URL(record.url);check(url.href===record.url&&url.origin===origin&&!url.username&&!url.password&&!url.search&&!url.hash&&url.pathname===prefix+'module-'+record.sha256+'.wasm','actual content-addressed module must match its own app build and decoded SHA');
    const current={sha256:record.sha256,bytes:record.bytes};if(identity)check(current.sha256===identity.sha256&&current.bytes===identity.bytes,'both actual downloads must have identical content');else identity=current;
  }
  return {...identity,abi:6,buildPrefix:prefix,downloads:2};
}
function validateLazyModuleMatrix(items,prefix,origin){
  check(Array.isArray(items)&&items.length===4,'one actual lazy module proof per merge profile required');
  const seen=new Set();let identity;
  for(const item of items){
    check(exactKeys(item,['id','proof'])&&typeof item.id==='string'&&profiles.some(profile=>item.id==='merge-'+profile+'|'+lazyModuleTitle)&&!seen.has(item.id),'unique exact lazy case/profile identity required');seen.add(item.id);
    const current=validateLazyModuleProof(item.proof,prefix,origin);if(identity)check(current.sha256===identity.sha256&&current.bytes===identity.bytes,'all profiles must serve identical decoded module bytes');else identity=current;
  }
  return {...identity,profiles:4,downloads:8};
}
async function browser(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20),inputs=await json(join(root,'inputs.json'),1<<20);check(report.status==='PRODUCTION_ATTESTED','both native artifact probes required');
  await unchanged(source,await json(join(root,'source-generated.json'),8<<20));
  const info=await lstat(join(root,'browser-home'));check(info.isDirectory()&&!info.isSymbolicLink()&&info.uid===process.getuid()&&!(info.mode&0o077)&&await realpath(join(root,'browser-home'))===join(root,'browser-home'),'owned command-scoped browser HOME required');
  const baseEnv=childEnv(root,source,{HOME:join(root,'browser-home'),NEXTGO_RETAINED_REQUIRE_LINUX_IDENTITY:'1'}),state={root,source,report};
  const actual=await step(state,'browser-versions',process.execPath,[self,'browser-versions'],baseEnv,2*60000,64<<10);report.actualBrowserRuntime=JSON.parse(actual.toString('utf8'));check(profiles.slice(0,3).every(name=>typeof report.actualBrowserRuntime[name]?.version==='string'&&report.actualBrowserRuntime[name].version.length>0),'actual browser version metadata missing');
  const cli=join(source,'node_modules','@playwright','test','cli.js'),native=[],cwd=new Set(),perApp={},auxiliary=[],lazyModuleProofs=[];
  const {retainedOrigin}=await import(pathToFileURL(join(source,'tests','retained-routes-fixture.mjs')).href);let mergeBuildPrefix;
  for(const id of appOrder){
    const app=apps[id],server=report.servers[id],appReport={...report,server};
    const probe=await json(join(root,app.assetProof),2<<20);check(probe.status==='PASSED'&&probe.app===id,'per-app asset proof missing');if(id==='merge')mergeBuildPrefix=probe.ssr.prefix;
    check(!cwd.has(probe.native.cwd),'native probe cwd reused');cwd.add(probe.native.cwd);auxiliary.push({app:id,...await strictNativeProof(probe.native,appReport,root,source)});
    const env={...baseEnv,NEXTGO_RETAINED_SERVER_BINARY:server.path,NEXTGO_RETAINED_SERVER_SHA256:server.sha256,NEXTGO_E2E_RESULTS:join(root,'browser',id)};
    const suffix=id==='retained'?'':'-'+id,common=[cli,'test','--config='+app.config,'--workers=1','--retries=0','--max-failures=1'];
    await step(state,'browser-discovery'+suffix,process.execPath,[...common,'--list','--reporter=json'],{...env,PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report',app.discovery)},2*60000);
    const discovery=browserCases(await json(join(root,'report',app.discovery),8<<20),false,app);
    const expected=profiles.flatMap(profile=>inputs[app.titles].map(title=>app.id+'-'+profile+'|'+title)).sort();check(JSON.stringify(discovery.map(item=>item.id).sort())===JSON.stringify(expected),'discovery differs from frozen source title contract: '+id);
    await step(state,'browser-matrix'+suffix,process.execPath,[...common,'--reporter=line,json'],{...env,PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report',app.results)},25*60000,4<<20);
    const results=browserCases(await json(join(root,'report',app.results),128<<20),true,app);check(JSON.stringify(discovery.map(item=>item.id).sort())===JSON.stringify(results.map(item=>item.id).sort()),'execution/discovery mismatch: '+id);
    const processes=[];
    for(const item of results){
      const attachments=item.test.results[0].attachments.filter(value=>value.name===app.attachment);check(attachments.length===1,'one actual native proof per browser case');
      const proof=await proofAttachment(attachments[0],root);check(!cwd.has(proof.cwd),'native cwd reused across apps/cases/probes');cwd.add(proof.cwd);processes.push({app:id,id:item.id,...await strictNativeProof(proof,appReport,root,source)});
      const modules=item.test.results[0].attachments.filter(value=>value.name===lazyModuleAttachment),required=id==='merge'&&item.id===item.test.projectName+'|'+lazyModuleTitle;
      check(modules.length===(required?1:0),'exact lazy proof attachment required only on the real lazy case');
      if(required){const observed=await proofAttachment(modules[0],root,4096);validateLazyModuleProof(observed,mergeBuildPrefix,retainedOrigin);lazyModuleProofs.push({id:item.id,proof:observed});}
    }
    check(processes.length===app.cases&&sha(await readBounded(server.path,128<<20))===server.sha256,'per-app native process/artifact completion');native.push(...processes);
    perApp[id]={cases:app.cases,nativeProcesses:processes,auxiliaryNativeProcesses:1,totalNativeProcesses:app.cases+1,assetProof:app.assetProof,server};
    await unchanged(source,await json(join(root,'source-generated.json'),8<<20));
  }
  check(native.length===144&&auxiliary.length===2&&cwd.size===146&&perApp.retained.cases===72&&perApp.retained.totalNativeProcesses===73&&perApp.merge.cases===72&&perApp.merge.totalNativeProcesses===73,'exact unchanged subset plus new application closures required');
  const actualModuleAssets=validateLazyModuleMatrix(lazyModuleProofs,mergeBuildPrefix,retainedOrigin);
  const ending=await sdk(root,source);check(JSON.stringify(ending)===JSON.stringify(report.sdk),'SDK/Node changed during qualification');check((await testArtifact(report.nativeCompiler.path)).sha256===report.nativeCompiler.sha256,'native race/link compiler changed');
  const head=await step(state,'ending-head',report.git,['-C',source,'rev-parse','HEAD'],baseEnv,60000,64<<10);check(head.toString('utf8').trim()===report.checkoutHead,'checkout revision changed during qualification');
  await writeJSON(join(root,'browser-proof.json'),{version:2,status:'PASSED',cases:144,profiles:4,retries:0,skips:0,apps:perApp,nativeProcesses:native,auxiliaryNativeProofs:auxiliary,auxiliaryNativeProcesses:2,totalNativeProcesses:146,actualBrowserRuntime:report.actualBrowserRuntime,sourceCommit:report.sourceCommit,inputContractSHA256:report.inputContractSHA256,servers:report.servers,lazyModuleProofs,actualModuleAssets,sdk:report.sdk.version,scope:report.scope},4<<20);
  browserClosureTotals(await json(join(root,'browser-proof.json'),4<<20));
  report.status='PASSED';await writeJSON(join(root,'campaign.json'),report,8<<20);
}

async function browserVersions(){
  const {chromium,firefox,webkit}=await import('playwright'),versions={};
  for(const [name,type]of Object.entries({chromium,firefox,webkit})){
    const browser=await type.launch({headless:true});try{versions[name]={version:browser.version(),declaredExecutable:type.executablePath(),scope:'actual default launch version; declared executable path is not an assertion about a headless-shell child path'};}finally{await browser.close();}
  }
  process.stdout.write(JSON.stringify(versions));
}

async function collect(){
  const {root}=await owned();const copied=[],limit=64<<20;let total=0;
  async function copy(path,name,cap,compress=false){try{const bytes=await readBounded(path,cap),data=compress?gzipSync(bytes,{level:6}):bytes;check(total+data.length<=limit-(256<<10),'collection total bound');await writeFile(join(root,'evidence',name),data,{flag:'wx',mode:0o600});total+=data.length;copied.push({name,bytes:data.length,sha256:sha(data),originalBytes:bytes.length,originalSHA256:sha(bytes),gzip:compress});}catch(error){copied.push({name,omitted:String(error.code||error.message).slice(0,160)});}}
  for(const name of ['owner.json','inputs.json','campaign.json','go-summary.json','test-executable-summary.json','asset-proof.json','merge-asset-proof.json','browser-proof.json','source-before.json','source-generated.json'])await copy(join(root,name),name,16<<20);
  const lambdaEnabled=(await json(join(root,'inputs.json'),1<<20)).lambdaQualification?.enabled===true;
  if(lambdaEnabled){const {collectLambdaEvidence}=await import('./retained-routes-lambda-collection.mjs');await collectLambdaEvidence(root,copy);}
  for(const app of Object.values(apps))for(const name of [app.discovery,app.results])await copy(join(root,'report',name),name+'.gz',name===app.results?128<<20:8<<20,true);
  const logs=await readdir(join(root,'logs'));check(logs.length<=192,'two-app native plus Lambda command evidence count bound');for(const name of logs)await copy(join(root,'logs',name),'log-'+name+(name.endsWith('.stdout')?'.gz':''),name.endsWith('.stdout')?32<<20:1<<20,name.endsWith('.stdout'));
  const executions=await readdir(join(root,'exec'));check(executions.length<=512,'test executable receipt count bound');for(const name of executions)await copy(join(root,'exec',name),'exec-'+name,64<<10);
  // Retain only the first actual failing attempt's bounded, same-root trace.
  // No directory recursion, arbitrary attachment path or cache upload occurs.
  let trace={status:'not-required'};
  try{
    const failures=[],readErrors=[];let observedFailure=false;
    for(const app of Object.values(apps)){
      let report;try{report=await json(join(root,'report',app.results),128<<20);}catch(error){readErrors.push({app:app.id,reason:String(error.code||error.message).slice(0,160)});continue;}
      if(!(report.stats?.unexpected||report.stats?.flaky||report.errors?.length))continue;
      observedFailure=true;
      const pending=(report.suites||[]).map(suite=>[suite,0]);let visits=0;
      while(pending.length){const [suite,depth]=pending.pop();check(++visits<=4096&&depth<=16,'failure report traversal bound');for(const spec of suite.specs||[])for(const test of spec.tests||[])for(const result of test.results||[]){check(++visits<=4096,'failure attempt count bound');if(['failed','timedOut'].includes(result.status)){const time=Date.parse(result.startTime);check(Number.isSafeInteger(time),'failure time missing');failures.push({time,result,app:app.id});}}for(const child of suite.suites||[])pending.push([child,depth+1]);}
    }
    if(observedFailure){
      failures.sort((a,b)=>a.time-b.time);const candidates=failures[0]?.result.attachments?.filter(item=>item.name==='trace'&&item.contentType==='application/zip')||[];check(candidates.length===1,'one first-failure trace unavailable');
      const path=candidates[0].path;check(isAbsolute(path||'')&&path.length<=4096&&resolve(path)===path&&!outside(join(root,'browser'),path)&&basename(path)==='trace.zip','unsafe first-failure trace path');
      await copy(path,'first-failure-trace.zip',8<<20);trace={status:'attempted',path};
    }
  }catch(error){trace={status:'unavailable',reason:String(error.code||error.message).slice(0,160)};}
  if(lambdaEnabled&&trace.status==='not-required'){const {collectLambdaTrace}=await import('./retained-routes-lambda-collection.mjs');trace=await collectLambdaTrace(root,copy);}
  const complete=copied.every(item=>!item.omitted);
  await writeJSON(join(root,'evidence','collection.json'),{version:1,totalBytes:total,limit,complete,copied,firstFailureTrace:trace},256<<10);
  const report=await json(join(root,'campaign.json'),8<<20);if(report.status==='PASSED')check(complete,'passing execution lacks complete bounded evidence');
}
function browserClosureTotals(proof){
  check(proof?.version===2&&proof.status==='PASSED'&&proof.cases===144&&proof.auxiliaryNativeProcesses===2&&proof.totalNativeProcesses===146&&Array.isArray(proof.nativeProcesses)&&proof.nativeProcesses.length===144&&Array.isArray(proof.auxiliaryNativeProofs)&&proof.auxiliaryNativeProofs.length===2,'complete two-app native closure totals required');
  const ids=new Set(),cwd=new Set();
  for(const id of appOrder){
    const app=apps[id],part=proof.apps?.[id],cases=proof.nativeProcesses.filter(value=>value.app===id),probes=proof.auxiliaryNativeProofs.filter(value=>value.app===id);
    check(part?.cases===app.cases&&part.totalNativeProcesses===app.cases+1&&part.auxiliaryNativeProcesses===1&&part.assetProof===app.assetProof&&Array.isArray(part.nativeProcesses)&&part.nativeProcesses.length===app.cases&&cases.length===app.cases&&probes.length===1,'required per-app subset closure totals: '+id);
    check(JSON.stringify(part.nativeProcesses)===JSON.stringify(cases),'per-app/global closure records disagree');
    for(const item of cases){check(typeof item.id==='string'&&item.id.startsWith(id+'-')&&!ids.has(item.id),'duplicate/foreign case identity');ids.add(item.id);}
    for(const item of [...cases,...probes]){check(item.closed===true&&item.groupEmpty===true&&item.listenerRefused===true&&typeof item.cwd==='string'&&!cwd.has(item.cwd),'missing/reused actual closure cwd');cwd.add(item.cwd);}
  }
  check(ids.size===144&&cwd.size===146,'two-app native closure cardinality');return {browser:144,native:146,retained:73,merge:73};
}
async function cleanup(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20);
  // An incomplete campaign may contain an independently grouped browser/native
  // process. Do not erase its failed ownership receipts or pretend graceful
  // cleanup; the outer ephemeral job/container is the containment owner.
  check(report.status==='PASSED','incomplete campaign retained for outer controller containment');
  check((await json(join(root,'evidence','collection.json'),256<<10)).complete===true,'evidence must complete before successful root removal');
  const proof=await json(join(root,'browser-proof.json'),4<<20);browserClosureTotals(proof);
  const moduleAssets=await json(join(root,'merge-asset-proof.json'),2<<20),{retainedOrigin}=await import(pathToFileURL(join(source,'tests','retained-routes-fixture.mjs')).href);
  check(moduleAssets.status==='PASSED'&&moduleAssets.app==='merge','own merge asset probe required for cleanup');validateLazyModuleMatrix(proof.lazyModuleProofs,moduleAssets.ssr.prefix,retainedOrigin);
  for(const name of (await readdir(join(root,'logs'))).filter(name=>name.endsWith('.proof.json'))){const item=await json(join(root,'logs',name),64<<10);check(item.closed&&item.groupEmpty&&!item.cleanupTimedOut&&(!item.pid||!groupPresent(item.pid)),'supervised command group still present');}
  if((await json(join(root,'inputs.json'),1<<20)).lambdaQualification?.enabled===true){const {verifyLambdaCleanup}=await import('./retained-routes-lambda-ci.mjs');await verifyLambdaCleanup(root,source,report,proof);}
  await rm(root,{recursive:true,force:false,maxRetries:0});try{await lstat(root);throw Error('owned qualification root remains');}catch(error){if(error.code!=='ENOENT')throw error;}
  process.stdout.write('Marker-verified successful qualification root removed; incomplete roots are never silently removed.\n');
}
async function main(){const mode=process.argv[2];if(mode==='init')return init();if(mode==='dependencies')return dependencies();if(mode==='units')return units();if(mode==='install-browsers')return installBrowsers();if(mode==='production')return production();if(mode==='asset-probe')return assetProbe();if(mode==='browser')return browser();if(mode==='browser-versions')return browserVersions();if(mode==='collect')return collect();if(mode==='cleanup')return cleanup();if(mode==='test-exec')return testExec();throw Error('expected init/dependencies/units/production/asset-probe/browser/browser-versions/collect/cleanup/test-exec');}
export {goResults,browserCases,validateNativeProof,strictNativeProof,proofAttachment,apps,browserClosureTotals,validateQualificationRequirements,validateLazyModuleProof,validateLazyModuleMatrix,lazyModuleTitle,owned,childEnv,unchanged,runChild,joinClosedCommandGroup,validateCommandCompletion,waitForCommandClose};
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href)main().catch(error=>{console.error(String(error.stack||error));process.exitCode=1;});
