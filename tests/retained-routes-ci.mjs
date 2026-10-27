// Dedicated development-only qualification owner. No framework/runtime import.
// Its source packet is unqualified until a bound campaign records all checks.
import {spawn} from 'node:child_process';
import {constants} from 'node:fs';
import {chmod,lstat,mkdir,mkdtemp,open,readdir,realpath,rm,writeFile} from 'node:fs/promises';
import {basename,delimiter,dirname,isAbsolute,join,relative,resolve,sep} from 'node:path';
import {pathToFileURL,fileURLToPath} from 'node:url';
import {createHash,randomUUID} from 'node:crypto';
import {gzipSync} from 'node:zlib';
import {check,sha,readBounded,writeJSON,json,inventory,commandEnvironment} from './static-shell-ci-build.mjs';

const self=fileURLToPath(import.meta.url),profiles=['chromium','firefox','webkit','webkit-touch'];
const moduleFilter='^(TestModuleTarget|TestModuleLinkStatus|TestMetadata|TestClientView|TestClientFallback|TestRefresh|TestFormFieldValidation|TestFormValidationStatus|TestFieldValidation|TestOptionalChoice|TestOffline|TestSavedAssetURL)';
const generated=['routes/nextgo_gen.go','actionrefs/nextgo_gen.go','actionrefs/nextgo_gen_js.go','actionrefs/nextgo_gen_server.go'].map(path=>'examples/retained/'+path);
const outside=(parent,path)=>{const value=relative(parent,path);return isAbsolute(value)||value==='..'||value.startsWith('..'+sep);};
const hex=value=>typeof value==='string'&&/^[a-f0-9]{64}$/.test(value);
const content=items=>items.map(({path,bytes,sha256})=>({path,bytes,sha256}));
const shellQuote=value=>"'"+value.replaceAll("'","'\\''")+"'";
const groupPresent=pid=>{try{process.kill(-pid,0);return true;}catch(error){if(error.code==='ESRCH')return false;throw error;}};
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));

// Command groups are observed, never used as historical signal authority.
// Unresolved descendants/identity are a failed campaign for outer containment.
async function runChild(root,name,command,args,{cwd,env,timeoutMS,stdoutLimit=1<<20}){
  check(/^[a-z0-9-]+$/.test(name)&&Number.isInteger(timeoutMS)&&timeoutMS>0&&isAbsolute(command)&&await realpath(command)===command,'canonical finite command required');
  const executable=await lstat(command,{bigint:true});check(executable.isFile()&&executable.nlink>=1n&&Boolean(executable.mode&0o111n),'regular command executable required');
  const {checkNativeChild,checkedChildSignal}=await import(pathToFileURL(join(cwd,'tests','retained-routes-native-identity.mjs')).href);
  const proof={name,command,args,pid:null,code:null,signal:null,closed:false,groupEmpty:false,timedOut:false,interrupted:false,outputExceeded:false,cleanupTimedOut:false,termSent:false,killSent:false,identity:null,identityChecks:[],identityUnavailableAfterClose:false,cleanupDeferred:false,stdoutBytes:0,stderrBytes:0};
  const buffers={stdout:[],stderr:[]};let child,watchdog,spawnError,resolveClose,initial,expected,stopping;
  const closed=new Promise(resolve=>{resolveClose=resolve;});
  async function stop(){
    if(stopping)return stopping;
    stopping=(async()=>{
      try{
        if(!proof.closed){check(initial,'no verified live command leader; outer controller required');proof.termSent=await checkedChildSignal(child,initial,expected,'SIGTERM',proof.identityChecks);}
        await Promise.race([closed,pause(3000)]);
        if(!proof.closed){proof.killSent=await checkedChildSignal(child,initial,expected,'SIGKILL',proof.identityChecks);await Promise.race([closed,pause(2000)]);}
        proof.groupEmpty=proof.pid?!groupPresent(proof.pid):true;
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
    child.once('error',error=>{spawnError=error;});child.once('close',(code,signal)=>{Object.assign(proof,{closed:true,code,signal});resolveClose();});
    for(const stream of ['stdout','stderr']){check(child[stream],'supervised pipe unavailable');child[stream].on('data',chunk=>{
      const key=stream+'Bytes',cap=stream==='stdout'?stdoutLimit:1<<20,used=proof[key]||0,room=Math.max(0,cap-used);
      if(room)buffers[stream].push(chunk.subarray(0,room));proof[key]=used+chunk.length;
      if(proof[key]>cap){proof.outputExceeded=true;void stop();}
    });}
    watchdog=setTimeout(()=>{proof.timedOut=true;void stop();},timeoutMS);
    try{initial=await checkNativeChild(child,null,expected,'capture',proof.identityChecks);proof.identity=initial;}
    catch(error){
      // A command may finish before /proc is sampled. This gives no signal
      // authority: accept only an already joined successful absent group.
      await Promise.race([closed,pause(50)]);
      if(proof.closed&&proof.code===0&&!groupPresent(child.pid)){proof.identityUnavailableAfterClose=true;}else{proof.identityFailure=String(error.message).slice(0,512);await stop();}
    }
    while(!proof.closed&&!proof.cleanupDeferred){await Promise.race([closed,pause(50)]);if(stopping)await stopping;}
    if(stopping)await stopping;
    proof.groupEmpty=proof.pid?!groupPresent(proof.pid):true;
    if(!proof.groupEmpty)proof.cleanupDeferred=true;
    check(!spawnError&&proof.closed&&proof.groupEmpty&&!proof.cleanupDeferred&&!proof.cleanupTimedOut&&!proof.timedOut&&!proof.interrupted&&!proof.outputExceeded&&proof.code===0&&proof.signal===null,'owned command failed: '+name);
    return Buffer.concat(buffers.stdout);
  }finally{
    if(child?.pid&&!proof.closed&&!proof.cleanupDeferred)await stop();
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
async function init(){
  check(process.platform==='linux'&&process.arch==='x64'&&process.version==='v24.22.1','Linux AMD64 Node24.22.1 required');
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
  check(contract.version===1&&contract.scope==='Linux AMD64 native HTTP only'&&contract.baseCommit==='768bbb712e9a285b6b8252469c817b6a922739a5'&&Array.isArray(contract.files)&&contract.files.length>34&&contract.files.length<=128,'reviewed input contract required');
  for(const target of ['native','core','devcore','portable'])check(Array.isArray(contract.tests?.[target])&&contract.tests[target].length>0&&contract.tests[target].length<=128&&new Set(contract.tests[target]).size===contract.tests[target].length&&contract.tests[target].every(name=>/^Test[A-Za-z0-9_]+$/.test(name)),'named target PASS contract: '+target);
  check(Array.isArray(contract.browserTitles)&&contract.browserTitles.length===18&&new Set(contract.browserTitles).size===18&&contract.browserTitles.every(title=>typeof title==='string'&&title.length<=1024),'exact18 reviewed browser titles required');
  const seen=new Set();
  for(const file of contract.files){check(typeof file.path==='string'&&file.path.length<=512&&!isAbsolute(file.path)&&!/[\\\n\r]/.test(file.path)&&file.path.split('/').every(part=>part&&part!=='.'&&part!=='..')&&hex(file.sha256)&&!seen.has(file.path),'input path/hash contract');seen.add(file.path);check(sha(await readBounded(join(source,file.path),16<<20))===file.sha256,'source pin changed: '+file.path);}
  for(const path of ['tests/retained-routes.spec.mjs','tests/retained-routes.config.mjs','tests/retained-routes-fixture.mjs','tests/retained-routes-native-identity.mjs','tests/retained-routes-native-identity.test.mjs','tests/static-shell-ci-build.mjs','tests/retained-routes-ci.mjs','tests/retained-routes-ci.test.mjs','.github/workflows/retained-routes-browser.yml','package.json','package-lock.json','go.mod'])check(seen.has(path),'missing producer/test/dependency pin: '+path);
  await writeJSON(join(root,'inputs.json'),contract,1<<20);
  await writeJSON(join(root,'campaign.json'),{version:1,status:'INITIALIZED',scope:contract.scope,sourceCommit:process.env.GITHUB_SHA,run:process.env.GITHUB_RUN_ID,attempt:process.env.GITHUB_RUN_ATTEMPT,sdkRequested:process.env.GO_VERSION,inputContractSHA256:sha(await readBounded(join(source,'tests','retained-routes-ci-inputs.json'),1<<20)),sourceContentSHA256:sha(Buffer.from(JSON.stringify(content(before)))),steps:[],unqualified:['serverless mock Runtime modes','ARM64','cross-platform/resource/security release gates','full V10/79 capabilities/11 gates']},8<<20);
}
function childEnv(root,source,extras={}){
  return commandEnvironment(root,{HOME:join(root,'home'),TMPDIR:join(root,'tmp'),PLAYWRIGHT_BROWSERS_PATH:'/ms-playwright',PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD:'1',npm_config_cache:join(root,'npm-cache'),npm_config_userconfig:'/dev/null',GIT_CONFIG_COUNT:'1',GIT_CONFIG_KEY_0:'safe.directory',GIT_CONFIG_VALUE_0:source,GITHUB_WORKSPACE:source,RUNNER_TEMP:process.env.RUNNER_TEMP,GITHUB_RUN_ID:process.env.GITHUB_RUN_ID,GITHUB_RUN_ATTEMPT:process.env.GITHUB_RUN_ATTEMPT,GITHUB_JOB:process.env.GITHUB_JOB,NEXTGO_RETAINED_CI:root,...extras});
}
async function executablePath(name){for(const directory of process.env.PATH.split(delimiter)){try{const path=await realpath(join(directory,name));if((await lstat(path)).isFile())return path;}catch{}}throw Error('required executable missing: '+name);}
async function sdk(root,source){
  let go;
  for(const path of process.env.PATH.split(delimiter)){try{const value=await realpath(join(path,'go'));if((await lstat(value)).isFile()){go=value;break;}}catch{}}
  check(go,'Go executable missing');const sdkRoot=dirname(dirname(go));
  const version=(await readBounded(join(sdkRoot,'VERSION'),4096)).toString('utf8');
  check(['1.24.13','1.27.3'].includes(process.env.GO_VERSION)&&version.split('\n')[0]==='go'+process.env.GO_VERSION,'exact approved SDK required');
  if(process.env.GOROOT)check(await realpath(process.env.GOROOT)===sdkRoot,'PATH/GOROOT mismatch');
  const files={};for(const name of ['bin/go','bin/gofmt','pkg/tool/linux_amd64/compile','pkg/tool/linux_amd64/link','pkg/tool/linux_amd64/vet','lib/wasm/go_js_wasm_exec','lib/wasm/wasm_exec_node.js','lib/wasm/wasm_exec.js']){const data=await readBounded(join(sdkRoot,name),128<<20);files[name]={bytes:data.length,sha256:sha(data)};}
  const node=await realpath(process.execPath),data=await readBounded(node,128<<20);
  return {go,root:sdkRoot,version:version.split('\n')[0],files,node:{path:node,version:process.version,sha256:sha(data)},platform:process.platform,architecture:process.arch,uid:process.getuid()};
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

// -exec is transparent: metadata never enters test stdout or alters test args.
// The test stays in its Go-owned package cwd and supervisor process group.
async function testExec(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20),binary=process.argv[3],args=process.argv.slice(4);
  check(report.sdk&&isAbsolute(binary||'')&&await realpath(binary)===binary&&!outside(root,binary)&&!outside(source,await realpath(process.cwd())),'test executable outside owned build/source roots');
  const mode=process.env.NEXTGO_RETAINED_TEST_MODE,phase=process.env.NEXTGO_RETAINED_TEST_PHASE;
  check(['native','wasm'].includes(mode)&&/^[a-z0-9-]+$/.test(phase||'')&&args.length<=64,'test execution identity');
  const before=await testArtifact(binary),wasm=before.header.startsWith('0061736d01000000');
  check((mode==='wasm')===wasm&&(mode!=='native'||before.header.startsWith('7f454c460201')&&before.header.slice(36,40)==='3e00'),'test compiler target/magic mismatch');
  const executable=mode==='wasm'?join(report.sdk.root,'lib/wasm/go_js_wasm_exec'):binary;
  const argv=mode==='wasm'?[binary,...args]:args;
  const packageDirectory=relative(source,await realpath(process.cwd())).split(sep).join('/');
  const proof={version:1,phase,mode,cwd:await realpath(process.cwd()),package:'github.com/Suhaibinator/Next.go'+(packageDirectory?'/'+packageDirectory:''),binary,bytes:before.bytes,sha256:before.sha256,header:before.header,dev:before.dev,inode:before.inode,runner:executable,args:argv,pid:null,closed:false,code:null,signal:null,unchanged:false};
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
  const cc=await executablePath('gcc'),compiler=await testArtifact(cc);env.CC=cc;
  const ccVersion=await step(state,'cc-version',cc,['--version'],env,60000,64<<10);report.nativeCompiler={path:cc,sha256:compiler.sha256,bytes:compiler.bytes,version:ccVersion.toString('utf8')};
  const version=await step(state,'go-version',report.sdk.go,['version'],env,60000,64<<10);report.goVersion=version.toString('utf8');check(report.goVersion==='go version go'+process.env.GO_VERSION+' linux/amd64\n','actual executing SDK version mismatch');
  const goEnv=await step(state,'go-environment',report.sdk.go,['env','-json','GOOS','GOARCH','GOROOT','GOVERSION','GOTOOLCHAIN','GOWORK','CGO_ENABLED','CC','GOFLAGS','GOCACHE','GOMODCACHE'],env,60000,64<<10);report.goEnvironment=JSON.parse(goEnv.toString('utf8'));
  check(report.goEnvironment.GOOS==='linux'&&report.goEnvironment.GOARCH==='amd64'&&report.goEnvironment.GOROOT===report.sdk.root&&report.goEnvironment.GOVERSION===report.sdk.version&&report.goEnvironment.GOTOOLCHAIN==='local'&&report.goEnvironment.GOWORK==='off'&&report.goEnvironment.CGO_ENABLED==='1'&&report.goEnvironment.CC===cc&&report.goEnvironment.GOCACHE===join(root,'go-cache')&&report.goEnvironment.GOMODCACHE===join(root,'mod-cache'),'actual native Go environment mismatch');
  const wrapper=join(root,'exec-wrapper.sh');await writeFile(wrapper,'#!/bin/sh\nexec '+shellQuote(process.execPath)+' '+shellQuote(self)+' test-exec "$@"\n',{mode:0o500,flag:'wx'});
  const formatted=await step(state,'format-check',join(report.sdk.root,'bin/gofmt'),['-l',...inputs.files.filter(file=>file.path.endsWith('.go')).map(file=>file.path)],env,60000);check(formatted.length===0,'source requires separate formatting derivative');
  const cli=join(root,'dist','nextgo');await step(state,'build-cli',report.sdk.go,['build','-trimpath','-buildvcs=false','-o',cli,'./cmd/nextgo'],env,8*60000);
  await unchanged(source,await json(join(root,'source-before.json'),8<<20));
  await step(state,'generate',cli,['generate','./examples/retained'],env,60000);
  const after=await inventory(source,true),before=await json(join(root,'source-before.json'),8<<20);
  check(JSON.stringify(before.filter(file=>!generated.includes(file.path)))===JSON.stringify(after.filter(file=>!generated.includes(file.path))),'generation changed unrelated source');
  report.generated=[];for(const path of generated){const data=await readBounded(join(source,path),1<<20);check(data.toString('utf8').startsWith('// Code generated by nextgo'),'generated helper provenance');report.generated.push({path,bytes:data.length,sha256:sha(data)});}
  await writeJSON(join(root,'source-generated.json'),after,8<<20);await step(state,'generate-check',cli,['generate','--check','./examples/retained'],env,60000);await unchanged(source,after);
  const summaries={};
  async function tests(name,args,extra,required,allowed=[]){const output=await step(state,name,report.sdk.go,['test','-count=1','-json','-timeout=10m','-exec='+wrapper,...args],{...env,...extra,NEXTGO_RETAINED_TEST_PHASE:name,NEXTGO_RETAINED_TEST_MODE:name==='native'?'native':'wasm'},name==='native'?45*60000:15*60000,32<<20);summaries[name]=goResults(output,required,allowed);await writeJSON(join(root,'go-summary.json'),summaries,16<<20);await unchanged(source,after);}
  await tests('native',['-race','./...'],{},inputs.tests.native,inputs.tests.nativeSkips||[]);
  await step(state,'vet-native',report.sdk.go,['vet','./...'],env,15*60000);await unchanged(source,after);
  const wasm={GOOS:'js',GOARCH:'wasm',CGO_ENABLED:'0'};
  const core=['./client','./mutation','./offline','./offline/worker','./internal/offlinejson','./ui'];
  const dev=['./client','./mutation','./ui'];
  await tests('core',core,wasm,inputs.tests.core);
  await tests('devcore',['-tags=nextgo_devrefresh',...dev],wasm,inputs.tests.devcore);
  for(const [name,tags]of [['module','nextgo_module'],['devmodule','nextgo_module,nextgo_devrefresh']]){
    await tests(name,['-tags='+tags,'./client','-run',moduleFilter],wasm,[],name==='devmodule'?['github.com/Suhaibinator/Next.go/client|TestRefreshActivityScriptCallerCancelRetainsDownloadCallbacks']:[]);
    check(summaries[name].tests.some(test=>test.pass===1),'selected module suite ran no tests');
    await tests(name+'-retained',['-tags='+tags,'./client','-run','^(TestRetention|TestRetained)'],wasm,inputs.tests.portable);
  }
  for(const [name,tags,packages]of [['core','',core],['devcore','nextgo_devrefresh',dev],['module','nextgo_module',['./client']],['devmodule','nextgo_module,nextgo_devrefresh',['./client']]])await step(state,'vet-'+name,report.sdk.go,['vet',...(tags?['-tags='+tags]:[]),...packages],{...env,...wasm},10*60000);
  const receipts=[];for(const name of await readdir(join(root,'exec'))){check(receipts.length<512,'test executable receipt bound');receipts.push(await json(join(root,'exec',name),64<<10));}
  for(const [phase,summary]of Object.entries(summaries)){
    const expected=summary.packages.filter(([,status])=>status==='pass').map(([name])=>name).sort(),actual=receipts.filter(proof=>proof.phase===phase);
    check(JSON.stringify(actual.map(proof=>proof.package).sort())===JSON.stringify(expected),'actual executable/package completion mismatch: '+phase);
    check(actual.every(proof=>proof.closed===true&&proof.code===0&&proof.signal===null&&proof.unchanged===true&&Number.isSafeInteger(proof.pid)&&proof.pid>0&&hex(proof.sha256)&&proof.mode===(phase==='native'?'native':'wasm')),'invalid actual test-executable proof');
  }
  await writeJSON(join(root,'test-executable-summary.json'),receipts,4<<20);
  await step(state,'harness-unit-tests',process.execPath,['--test','tests/retained-routes-native-identity.test.mjs','tests/retained-routes-ci.test.mjs'],env,60000);
  await unchanged(source,after);report.status='UNITS_PASSED';report.cli={path:cli,sha256:sha(await readBounded(cli,128<<20))};await chmod(cli,0o500);await writeJSON(join(root,'campaign.json'),report,8<<20);
}

async function boundedHTTP(url,maximum,contentType){
  const response=await fetch(url,{redirect:'error',cache:'no-store',signal:AbortSignal.timeout(15000),headers:{'Accept-Encoding':'identity'}});
  check(response.status===200&&response.url===url&&(!response.headers.get('content-encoding')||response.headers.get('content-encoding')==='identity'),'actual identity-encoded same-origin HTTP required');
  check(response.headers.get('content-type')?.toLowerCase().startsWith(contentType),'unexpected asset MIME');
  const reader=response.body?.getReader();check(reader,'response body required');const parts=[];let bytes=0;
  try{for(;;){const part=await reader.read();if(part.done)break;bytes+=part.value.byteLength;check(bytes<=maximum,'actual response exceeds byte cap');parts.push(Buffer.from(part.value));}}finally{await reader.cancel().catch(()=>{});reader.releaseLock();}
  const data=Buffer.concat(parts,bytes),length=response.headers.get('content-length');if(length!==null)check(/^(0|[1-9][0-9]*)$/.test(length)&&Number(length)===bytes,'asset content-length mismatch');
  return {data,headers:Object.fromEntries(response.headers),sha256:sha(data),bytes};
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
  const env=childEnv(root,source,{GOROOT:report.sdk.root,CGO_ENABLED:'1',CC:report.nativeCompiler.path}),state={root,source,report},output=join(root,'dist','retained');
  check(sha(await readBounded(report.cli.path,128<<20))===report.cli.sha256,'CLI changed');
  await step(state,'build-production',report.cli.path,['build','--out',output,'./examples/retained'],env,20*60000);await unchanged(source,await json(join(root,'source-generated.json'),8<<20));
  const path=join(output,'server'),data=await readBounded(path,128<<20),info=await lstat(path,{bigint:true});await chmod(path,0o500);
  check(data.subarray(0,6).toString('hex')==='7f454c460201'&&data.subarray(18,20).toString('hex')==='3e00','Linux AMD64 production ELF required');
  const parent=await lstat(output,{bigint:true});check(parent.isDirectory()&&await realpath(output)===output&&parent.uid===BigInt(process.getuid())&&(parent.mode&0o022n)===0n,'cooperatively owned producer directory required');
  report.server={path,sha256:sha(data),bytes:data.length,dev:info.dev.toString(),inode:info.ino.toString(),parent:{path:output,dev:parent.dev.toString(),inode:parent.ino.toString()}};await writeJSON(join(root,'campaign.json'),report,8<<20);
  const metadata=await step(state,'production-build-info',report.sdk.go,['version','-m',report.cli.path,path],env,60000,128<<10);report.productionBuildInfo=metadata.toString('utf8');for(const file of [report.cli.path,path])check(report.productionBuildInfo.includes(file+': go'+process.env.GO_VERSION+'\n'),'actual production build metadata missing selected compiler: '+file);
  await step(state,'asset-probe',process.execPath,[self,'asset-probe'],{...env,NEXTGO_RETAINED_SERVER_BINARY:path,NEXTGO_RETAINED_SERVER_SHA256:report.server.sha256,NEXTGO_RETAINED_REQUIRE_LINUX_IDENTITY:'1'},2*60000);
  const probe=await json(join(root,'asset-proof.json'),2<<20);check(probe.status==='PASSED'&&probe.assets['wasm_exec.js'].sha256===report.sdk.files['lib/wasm/wasm_exec.js'].sha256,'actual embedded SDK runtime mismatch');
  await strictNativeProof(probe.native,report,root,source);report.status='PRODUCTION_ATTESTED';await writeJSON(join(root,'campaign.json'),report,8<<20);
}
async function assetProbe(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20),proof={version:1,status:'RUNNING',assets:{},native:null};
  const {startRetainedServer}=await import(pathToFileURL(join(source,'tests','retained-routes-fixture.mjs')).href);let native;
  try{
    native=await startRetainedServer();const html=await boundedHTTP(native.origin+'/a',1<<20,'text/html');
    const prefixes=[...html.data.toString('utf8').matchAll(/src="(\/_nextgo\/[a-f0-9]{20}\/)wasm_exec\.js"/g)].map(match=>match[1]);check(prefixes.length===1,'one canonical real SSR runtime prefix required');
    proof.ssr={sha256:html.sha256,bytes:html.bytes,prefix:prefixes[0]};
    for(const [name,type,limit]of [['app.wasm','application/wasm',64<<20],['wasm_exec.js','text/javascript',1<<20],['boot.js','text/javascript',1<<20]]){const value=await boundedHTTP(native.origin+prefixes[0]+name,limit,type);if(name==='app.wasm')check(value.data.subarray(0,8).equals(Buffer.from([0,97,115,109,1,0,0,0])),'real WASM magic');proof.assets[name]={sha256:value.sha256,bytes:value.bytes,headers:value.headers};}
    const css=await boundedHTTP(native.origin+'/assets/retained.css',256<<10,'text/css');proof.assets.css={sha256:css.sha256,bytes:css.bytes};
    check(proof.assets['wasm_exec.js'].sha256===report.sdk.files['lib/wasm/wasm_exec.js'].sha256,'served runtime differs from selected SDK');
    await native.close();proof.native=native.snapshot();await strictNativeProof(proof.native,report,root,source);proof.status='PASSED';
  }catch(error){proof.status='FAILED';proof.failure=String(error.message).slice(0,2048);if(error.retainedProof)proof.native=error.retainedProof;throw error;}
  finally{
    let cleanupError;
    if(native){try{await native.close();}catch(error){cleanupError=error;proof.status='FAILED';proof.cleanupFailure=String(error.message).slice(0,2048);}finally{proof.native=native.snapshot();}}
    await writeJSON(join(root,'asset-proof.json'),proof,2<<20);
    if(cleanupError)throw cleanupError;
  }
}

function browserCases(report,execution){
  check(Array.isArray(report.errors)&&report.errors.length===0,'Playwright global error');const found=[],pending=(report.suites||[]).map(suite=>[suite,0]);let visits=0;
  while(pending.length){const [suite,depth]=pending.pop();check(++visits<=4096&&depth<=16,'browser report bound');for(const spec of suite.specs||[]){check(typeof spec.title==='string'&&spec.title.length<=1024&&typeof spec.file==='string'&&spec.file.endsWith('retained-routes.spec.mjs'),'browser source identity');for(const test of spec.tests||[]){check(++visits<=4096&&profiles.some(profile=>test.projectName==='retained-'+profile),'browser project identity');if(execution)check(test.expectedStatus==='passed'&&test.status==='expected'&&test.results?.length===1&&test.results[0].status==='passed'&&test.results[0].retry===0,'one actual passing attempt required');found.push({id:test.projectName+'|'+spec.title,test});}}for(const child of suite.suites||[])pending.push([child,depth+1]);}
  check(found.length===72&&new Set(found.map(item=>item.id)).size===72,'exact72 distinct cases required');for(const profile of profiles)check(found.filter(item=>item.test.projectName==='retained-'+profile).length===18,'exact18 cases per profile');
  if(execution)check(report.stats?.expected===72&&report.stats.unexpected===0&&report.stats.flaky===0&&report.stats.skipped===0&&report.config?.workers===1&&report.config.maxFailures===1&&report.config.version==='1.64.1','complete zero-skip/flake/retry matrix required');return found;
}
async function proofAttachment(attachment,root){
  check(attachment.contentType==='application/json','native proof MIME');let data;
  if(typeof attachment.body==='string'){check(attachment.body.length<=3<<20&&/^[A-Za-z0-9+/]*={0,2}$/.test(attachment.body),'proof body bound/encoding');data=Buffer.from(attachment.body,'base64');check(data.toString('base64')===attachment.body,'canonical proof body');}
  else{check(isAbsolute(attachment.path||'')&&!outside(join(root,'browser'),attachment.path),'proof path outside owned browser output');data=await readBounded(attachment.path,2<<20);}
  check(data.length<=2<<20,'proof byte limit');return JSON.parse(data.toString('utf8'));
}
async function browser(){
  const {root,source}=await owned(),report=await json(join(root,'campaign.json'),8<<20),inputs=await json(join(root,'inputs.json'),1<<20);check(report.status==='PRODUCTION_ATTESTED','native artifact probe required');
  await unchanged(source,await json(join(root,'source-generated.json'),8<<20));
  const info=await lstat(join(root,'browser-home'));check(info.isDirectory()&&!info.isSymbolicLink()&&info.uid===process.getuid()&&!(info.mode&0o077)&&await realpath(join(root,'browser-home'))===join(root,'browser-home'),'owned command-scoped browser HOME required');
  const env=childEnv(root,source,{HOME:join(root,'browser-home'),NEXTGO_RETAINED_SERVER_BINARY:report.server.path,NEXTGO_RETAINED_SERVER_SHA256:report.server.sha256,NEXTGO_RETAINED_REQUIRE_LINUX_IDENTITY:'1',NEXTGO_E2E_RESULTS:join(root,'browser')}),state={root,source,report};
  const actual=await step(state,'browser-versions',process.execPath,[self,'browser-versions'],env,2*60000,64<<10);report.actualBrowserRuntime=JSON.parse(actual.toString('utf8'));check(profiles.slice(0,3).every(name=>typeof report.actualBrowserRuntime[name]?.version==='string'&&report.actualBrowserRuntime[name].version.length>0),'actual browser version metadata missing');
  const cli=join(source,'node_modules','@playwright','test','cli.js'),common=[cli,'test','--config=tests/retained-routes.config.mjs','--workers=1','--retries=0','--max-failures=1'];
  await step(state,'browser-discovery',process.execPath,[...common,'--list','--reporter=json'],{...env,PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report','discovery.json')},2*60000);
  const discovery=browserCases(await json(join(root,'report','discovery.json'),8<<20),false);
  const expected=profiles.flatMap(profile=>inputs.browserTitles.map(title=>'retained-'+profile+'|'+title)).sort();check(JSON.stringify(discovery.map(item=>item.id).sort())===JSON.stringify(expected),'discovery differs from frozen source title contract');
  await step(state,'browser-matrix',process.execPath,[...common,'--reporter=line,json'],{...env,PLAYWRIGHT_JSON_OUTPUT_FILE:join(root,'report','results.json')},25*60000,4<<20);
  const results=browserCases(await json(join(root,'report','results.json'),128<<20),true);check(JSON.stringify(discovery.map(item=>item.id).sort())===JSON.stringify(results.map(item=>item.id).sort()),'execution/discovery mismatch');
  const native=[],cwd=new Set();for(const item of results){const attachments=item.test.results[0].attachments.filter(value=>value.name==='retained-native-process-proof');check(attachments.length===1,'one actual native proof per browser case');const proof=await proofAttachment(attachments[0],root);check(!cwd.has(proof.cwd),'native cwd reused across cases');cwd.add(proof.cwd);native.push({id:item.id,...await strictNativeProof(proof,report,root,source)});}
  check(native.length===72&&sha(await readBounded(report.server.path,128<<20))===report.server.sha256,'native process/artifact completion');
  await unchanged(source,await json(join(root,'source-generated.json'),8<<20));
  const ending=await sdk(root,source);check(JSON.stringify(ending)===JSON.stringify(report.sdk),'SDK/Node changed during qualification');
  check((await testArtifact(report.nativeCompiler.path)).sha256===report.nativeCompiler.sha256,'native race/link compiler changed');
  const head=await step(state,'ending-head',report.git,['-C',source,'rev-parse','HEAD'],env,60000,64<<10);check(head.toString('utf8').trim()===report.checkoutHead,'checkout revision changed during qualification');
  await writeJSON(join(root,'browser-proof.json'),{version:1,status:'PASSED',cases:72,profiles:4,retries:0,skips:0,nativeProcesses:native,auxiliaryNativeProcesses:1,totalNativeProcesses:73,actualBrowserRuntime:report.actualBrowserRuntime,sourceCommit:report.sourceCommit,inputContractSHA256:report.inputContractSHA256,server:report.server,sdk:report.sdk.version,scope:report.scope},2<<20);
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
  for(const name of ['owner.json','inputs.json','campaign.json','go-summary.json','test-executable-summary.json','asset-proof.json','browser-proof.json','source-before.json','source-generated.json'])await copy(join(root,name),name,16<<20);
  for(const name of ['discovery.json','results.json'])await copy(join(root,'report',name),name+'.gz',name==='results.json'?128<<20:8<<20,true);
  const logs=await readdir(join(root,'logs'));check(logs.length<=128,'command evidence count bound');for(const name of logs)await copy(join(root,'logs',name),'log-'+name+(name.endsWith('.stdout')?'.gz':''),name.endsWith('.stdout')?32<<20:1<<20,name.endsWith('.stdout'));
  const executions=await readdir(join(root,'exec'));check(executions.length<=512,'test executable receipt count bound');for(const name of executions)await copy(join(root,'exec',name),'exec-'+name,64<<10);
  // Retain only the first actual failing attempt's bounded, same-root trace.
  // No directory recursion, arbitrary attachment path or cache upload occurs.
  let trace={status:'not-required'};
  try{
    const report=await json(join(root,'report','results.json'),128<<20);
    if(report.stats?.unexpected||report.stats?.flaky||report.errors?.length){
      const pending=(report.suites||[]).map(suite=>[suite,0]),failures=[];let visits=0;
      while(pending.length){const [suite,depth]=pending.pop();check(++visits<=4096&&depth<=16,'failure report traversal bound');for(const spec of suite.specs||[])for(const test of spec.tests||[])for(const result of test.results||[]){check(++visits<=4096,'failure attempt count bound');if(['failed','timedOut'].includes(result.status)){const time=Date.parse(result.startTime);check(Number.isSafeInteger(time),'failure time missing');failures.push({time,result});}}for(const child of suite.suites||[])pending.push([child,depth+1]);}
      failures.sort((a,b)=>a.time-b.time);const candidates=failures[0]?.result.attachments?.filter(item=>item.name==='trace'&&item.contentType==='application/zip')||[];check(candidates.length===1,'one first-failure trace unavailable');
      const path=candidates[0].path;check(isAbsolute(path||'')&&path.length<=4096&&resolve(path)===path&&!outside(join(root,'browser'),path)&&basename(path)==='trace.zip','unsafe first-failure trace path');
      await copy(path,'first-failure-trace.zip',8<<20);trace={status:'attempted',path};
    }
  }catch(error){trace={status:'unavailable',reason:String(error.code||error.message).slice(0,160)};}
  const complete=copied.every(item=>!item.omitted);
  await writeJSON(join(root,'evidence','collection.json'),{version:1,totalBytes:total,limit,complete,copied,firstFailureTrace:trace},256<<10);
  const report=await json(join(root,'campaign.json'),8<<20);if(report.status==='PASSED')check(complete,'passing execution lacks complete bounded evidence');
}
async function cleanup(){
  const {root}=await owned(),report=await json(join(root,'campaign.json'),8<<20);
  // An incomplete campaign may contain an independently grouped browser/native
  // process. Do not erase its failed ownership receipts or pretend graceful
  // cleanup; the outer ephemeral job/container is the containment owner.
  check(report.status==='PASSED','incomplete campaign retained for outer controller containment');
  check((await json(join(root,'evidence','collection.json'),256<<10)).complete===true,'evidence must complete before successful root removal');
  const proof=await json(join(root,'browser-proof.json'),2<<20);check(proof.status==='PASSED'&&proof.nativeProcesses.length===72&&proof.totalNativeProcesses===73,'complete native closure receipts required before removal');
  for(const name of (await readdir(join(root,'logs'))).filter(name=>name.endsWith('.proof.json'))){const item=await json(join(root,'logs',name),64<<10);check(item.closed&&item.groupEmpty&&!item.cleanupTimedOut&&(!item.pid||!groupPresent(item.pid)),'supervised command group still present');}
  await rm(root,{recursive:true,force:false,maxRetries:0});try{await lstat(root);throw Error('owned qualification root remains');}catch(error){if(error.code!=='ENOENT')throw error;}
  process.stdout.write('Marker-verified successful qualification root removed; incomplete roots are never silently removed.\n');
}
async function main(){const mode=process.argv[2];if(mode==='init')return init();if(mode==='dependencies')return dependencies();if(mode==='units')return units();if(mode==='production')return production();if(mode==='asset-probe')return assetProbe();if(mode==='browser')return browser();if(mode==='browser-versions')return browserVersions();if(mode==='collect')return collect();if(mode==='cleanup')return cleanup();if(mode==='test-exec')return testExec();throw Error('expected init/dependencies/units/production/asset-probe/browser/browser-versions/collect/cleanup/test-exec');}
export {goResults,browserCases,validateNativeProof,strictNativeProof,proofAttachment};
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href)main().catch(error=>{console.error(String(error.stack||error));process.exitCode=1;});
