// Pure evidence-auditor negatives. No application responses or processes.
import test from 'node:test';
import assert from 'node:assert/strict';
import {mkdtemp,mkdir,writeFile,symlink,realpath,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
import {fileURLToPath} from 'node:url';
import {observeRetainedOwnerScroll} from './retained-owner-scroll-observation.mjs';
import {standaloneAD01FIFOTests,validateStandaloneAD01Requirements,goResults,browserCases,validateNativeProof,strictNativeProof,proofAttachment,apps,browserClosureTotals,validateQualificationRequirements,validateFullGoSource,validateLazyModuleProof,validateLazyModuleMatrix,lazyModuleTitle,joinClosedCommandGroup,validateCommandCompletion,waitForCommandClose} from './retained-routes-ci.mjs';

import {nativeTarget,nativeToolPaths,assertNativeELF,assertNativeGoEnvironment,assertNativeBuildInfo} from './retained-routes-target.mjs';

const clone=value=>structuredClone(value),source=fileURLToPath(new URL('..',import.meta.url));
const pkg='github.com/Suhaibinator/Next.go/client';
const events=values=>Buffer.from(values.map(value=>JSON.stringify(value)).join('\n')+'\n');
const passed=[{Action:'start',Package:pkg},{Action:'run',Package:pkg,Test:'TestRequired'},{Action:'pass',Package:pkg,Test:'TestRequired'},{Action:'pass',Package:pkg}];

test('Go evidence requires one actual run and pass for each named regression',()=>{
  const proof=goResults(events(passed),['TestRequired']);assert.equal(proof.requiredPasses.length,1);assert.equal(proof.tests[0].run,1);
  for(const value of [passed.filter(item=>item.Test!=='TestRequired'),[...passed,passed[2]],passed.map(item=>item.Action==='pass'&&item.Test?{...item,Action:'skip'}:item),[{Action:'fail',Package:pkg}]])assert.throws(()=>goResults(events(value),['TestRequired']));
  assert.throws(()=>goResults(Buffer.from('not-json\n')));assert.throws(()=>goResults(Buffer.alloc(0)));
});
test('Go skip allowances remain exact rather than permitting whole target skips',()=>{
  const skipped=[{Action:'run',Package:pkg,Test:'TestKnownSkip'},{Action:'skip',Package:pkg,Test:'TestKnownSkip'},{Action:'pass',Package:pkg}];
  assert.throws(()=>goResults(events(skipped)));
  assert.equal(goResults(events(skipped),[],[pkg+'|TestKnownSkip']).skipped.length,1);
  assert.throws(()=>goResults(events(skipped.slice(1)),[],[pkg+'|TestKnownSkip']));
  assert.throws(()=>goResults(events(skipped),['TestKnownSkip'],[pkg+'|TestKnownSkip']));
});
test('an allowed module host-only skip never substitutes for its devcore counterpart',()=>{
  const name='TestRefreshActivityScriptCallerCancelRetainsDownloadCallbacks';
  const moduleEvents=events([{Action:'run',Package:pkg,Test:name},{Action:'skip',Package:pkg,Test:name},{Action:'pass',Package:pkg}]);
  assert.equal(goResults(moduleEvents,[],[pkg+'|'+name]).skipped.length,1);
  assert.throws(()=>goResults(events([{Action:'pass',Package:pkg}]),[name]));
  assert.doesNotThrow(()=>goResults(events([{Action:'run',Package:pkg,Test:name},{Action:'pass',Package:pkg,Test:name},{Action:'pass',Package:pkg}]),[name]));
});

// Synthetic Go JSON below exercises rejection only; it is never campaign data.
function additionalAD01Contract(){
  const tests=Object.fromEntries(Object.entries({native:89,core:202,devcore:169,module:44,devmodule:46,portable:13,ui:12}).map(([phase,count])=>[phase,Array.from({length:count},(_,index)=>'TestInherited'+phase+index)]));
  return {tests,standaloneAD01FIFORequirements:{version:1,baselineRequiredOccurrences:600,additionalRequiredOccurrences:21,requiredByPhase:Object.fromEntries(['native','core','devcore'].map(phase=>[phase,[...standaloneAD01FIFOTests]]))}};
}
function additionalAD01Events(){return [...passed.slice(0,-1),...standaloneAD01FIFOTests.flatMap(name=>[{Action:'run',Package:pkg,Test:name},{Action:'pass',Package:pkg,Test:name}]),passed.at(-1)];}
test('standalone AD01 contract preserves inherited 600 and requires seven separate roots in exactly three phases',()=>{
  const contract=additionalAD01Contract(),before=clone(contract.tests),requirements=validateStandaloneAD01Requirements(contract);
  assert.deepEqual(contract.tests,before);assert.deepEqual(Object.keys(requirements).sort(),['core','devcore','native']);
  assert.equal(Object.values(requirements).reduce((count,names)=>count+names.length,0),21);
  assert.equal(before.native.length+before.core.length+before.devcore.length+before.module.length+before.devmodule.length+2*before.portable.length+2*before.ui.length,600);
  for(const mutate of [value=>{delete value.standaloneAD01FIFORequirements;},value=>{value.standaloneAD01FIFORequirements.additionalRequiredOccurrences=20;},value=>{value.standaloneAD01FIFORequirements.baselineRequiredOccurrences=621;},value=>{value.standaloneAD01FIFORequirements.requiredByPhase.module=[...standaloneAD01FIFOTests];},value=>{delete value.standaloneAD01FIFORequirements.requiredByPhase.devcore;},value=>{value.standaloneAD01FIFORequirements.requiredByPhase.core.pop();},value=>{value.standaloneAD01FIFORequirements.requiredByPhase.native.reverse();},value=>{value.standaloneAD01FIFORequirements.requiredByPhase.native[1]=standaloneAD01FIFOTests[0];},value=>{value.tests.native[0]=standaloneAD01FIFOTests[0];},value=>{value.tests.devcore.pop();}]){
    const value=clone(contract);mutate(value);assert.throws(()=>validateStandaloneAD01Requirements(value));
  }
});
test('AD01 actual Go evidence preserves baseline credits and reports seven additional passes separately',()=>{
  const value=additionalAD01Events(),proof=goResults(events(value),['TestRequired'],[],standaloneAD01FIFOTests);
  assert.deepEqual(proof.requiredPasses,['TestRequired']);assert.deepEqual(proof.additionalRequiredPasses,standaloneAD01FIFOTests);
  assert.equal(proof.additionalRequiredPasses.length,7);assert.equal(proof.tests.filter(item=>standaloneAD01FIFOTests.includes(item.name)&&item.run===1&&item.pass===1&&item.skip===0).length,7);
  assert.deepEqual(goResults(events(passed),['TestRequired']).additionalRequiredPasses,[]);
});
test('AD01 missing roots and subtest-only substitutes cannot receive additional PASS credit',()=>{
  for(const name of standaloneAD01FIFOTests){
    const value=additionalAD01Events().filter(item=>item.Test!==name);assert.throws(()=>goResults(events(value),['TestRequired'],[],standaloneAD01FIFOTests));
    const subtest=additionalAD01Events().map(item=>item.Test===name?{...item,Test:name+'/child'}:item);assert.throws(()=>goResults(events(subtest),['TestRequired'],[],standaloneAD01FIFOTests));
  }
});
test('an explicitly allowed skip cannot substitute for any mandatory AD01 extra root',()=>{
  for(const name of standaloneAD01FIFOTests){const value=additionalAD01Events().map(item=>item.Test===name&&item.Action==='pass'?{...item,Action:'skip'}:item);assert.throws(()=>goResults(events(value),['TestRequired'],[pkg+'|'+name],standaloneAD01FIFOTests));}
});
test('AD01 repeated roots and same-name foreign package roots are rejected',()=>{
  for(const name of standaloneAD01FIFOTests){
    const value=additionalAD01Events();assert.throws(()=>goResults(events([...value,{Action:'run',Package:pkg,Test:name},{Action:'pass',Package:pkg,Test:name}]),['TestRequired'],[],standaloneAD01FIFOTests));
    assert.throws(()=>goResults(events([...value,{Action:'run',Package:pkg+'/foreign',Test:name},{Action:'pass',Package:pkg+'/foreign',Test:name},{Action:'pass',Package:pkg+'/foreign'}]),['TestRequired'],[],standaloneAD01FIFOTests));
  }
});
test('AD01 failed extra roots cannot pass through a successful baseline or package completion',()=>{
  for(const name of standaloneAD01FIFOTests){const value=additionalAD01Events().map(item=>item.Test===name&&item.Action==='pass'?{...item,Action:'fail'}:item);assert.throws(()=>goResults(events(value),['TestRequired'],[],standaloneAD01FIFOTests));}
});
test('additional Go requirements reject duplicate overlap malformed and oversized lists',()=>{
  for(const names of [[standaloneAD01FIFOTests[0],standaloneAD01FIFOTests[0]],['TestRequired'],['bad/name'],[null],Array.from({length:257},(_,index)=>'TestExtra'+index)])assert.throws(()=>goResults(events(additionalAD01Events()),['TestRequired'],[],names));
});

function matrix(){return {errors:[],config:{version:'1.64.1',workers:1,maxFailures:1},stats:{expected:72,unexpected:0,flaky:0,skipped:0},suites:[{specs:Array.from({length:18},(_,index)=>({title:'case '+index,file:'tests/retained-routes.spec.mjs',tests:['chromium','firefox','webkit','webkit-touch'].map(profile=>({projectName:'retained-'+profile,expectedStatus:'passed',status:'expected',results:[{status:'passed',retry:0,attachments:[]}]}))}))}]};}
test('browser audit requires exact72 unique one-attempt cases across all four profiles',()=>{
  assert.equal(browserCases(matrix(),true).length,72);
  const mutations=[value=>{value.stats.expected=71;},value=>{value.stats.skipped=1;},value=>{value.stats.flaky=1;},value=>{value.stats.unexpected=1;},value=>{value.config.workers=2;},value=>{value.config.version='other';},value=>{value.errors.push({message:'driver failed'});},value=>{value.suites[0].specs.pop();},value=>{value.suites[0].specs[1].title=value.suites[0].specs[0].title;},value=>{value.suites[0].specs[0].tests[0].projectName='unreviewed';},value=>{value.suites[0].specs[0].tests[0].results[0].retry=1;},value=>{value.suites[0].specs[0].tests[0].results.push({status:'passed',retry:1});}];
  for(const mutate of mutations){const value=matrix();mutate(value);assert.throws(()=>browserCases(value,true));}
});
function native(){
  const server={path:'/tmp/qualification/dist/retained/server',sha256:'a'.repeat(64),bytes:100,dev:'1',inode:'2',parent:{path:'/tmp/qualification/dist/retained',dev:'1',inode:'3'}};
  const report={server,sdk:{uid:1000}},identity={platform:'linux',pid:456,uids:{real:1000,effective:1000,saved:1000,filesystem:1000},pgid:456,session:456,startTicks:'9007199254740993',executable:{path:server.path,dev:server.dev,inode:server.inode}};
  const proof={version:1,binary:server.path,sha256:server.sha256,bytes:server.bytes,pid:456,cwd:'/tmp/qualification/tmp/nextgo-retained-example-abc',closed:true,joined:true,shutdownRequested:true,termSent:true,cwdRemoved:true,listenerRefused:true,forced:false,unexpectedExit:false,code:0,signal:null,groupEmpty:true,platform:'linux',requireLinuxIdentity:true,nativeIdentityVersion:1,nativeOwnerUID:1000,nativeIdentity:identity,identityRefused:false,cleanupDeferred:false,identityFailure:'',artifact:{path:server.path,dev:server.dev,inode:server.inode,parent:server.parent},identityChecks:[{phase:'capture',outcome:'verified',identity},{phase:'readiness',outcome:'verified',identity},{phase:'before-signal',signal:'SIGTERM',outcome:'signal-sent',identity}]};
  // Reporter JSON has independent observations, not JavaScript object aliases.
  return {proof:JSON.parse(JSON.stringify(proof)),report,root:'/tmp/qualification'};
}
test('native closure audit rejects coerced flags artifact aliases and missing ownership',()=>{
  const {proof,report,root}=native();assert.doesNotThrow(()=>validateNativeProof(proof,report,root));
  const mutations=[value=>{value.closed='true';},value=>{value.forced='false';},value=>{value.forced=true;},value=>{value.code=1;},value=>{value.signal='SIGKILL';},value=>{value.identityRefused=true;},value=>{value.nativeOwnerUID=0;},value=>{value.cleanupDeferred=true;},value=>{value.groupEmpty=false;},value=>{value.listenerRefused=false;},value=>{value.sha256='b'.repeat(64);},value=>{value.artifact.parent.inode='4';},value=>{value.cwd='/tmp/foreign';},value=>{value.cwd='/tmp/qualification/tmp/../tmp/nextgo-retained-example-abc';}];
  for(const mutate of mutations){const value=clone(proof);mutate(value);assert.throws(()=>validateNativeProof(value,report,root));}
});
test('native identity audit requires capture readiness and same-lifetime pre-TERM receipts',async()=>{
  const {proof,report,root}=native();assert.equal((await strictNativeProof(proof,report,root,source)).identity.startTicks,'9007199254740993');
  const mutations=[value=>{value.nativeIdentity.uids.saved=0;},value=>{value.identityChecks.pop();},value=>{value.identityChecks[2].signal='SIGKILL';},value=>{value.identityChecks[1].outcome='already-exited';},value=>{value.identityChecks[2].identity.startTicks='9007199254740994';},value=>{value.nativeIdentity.executable.inode='4';}];
  for(const mutate of mutations){const value=clone(proof);mutate(value);await assert.rejects(strictNativeProof(value,report,root,source));}
});
test('proof attachment audit rejects wrong MIME malformed encoding and foreign paths before reading',async()=>{
  const payload={known:true},body=Buffer.from(JSON.stringify(payload)).toString('base64');
  assert.deepEqual(await proofAttachment({contentType:'application/json',body},'/tmp/qualification'),payload);
  for(const attachment of [{contentType:'text/plain',body},{contentType:'application/json',body:'%%%='},{contentType:'application/json',body:'='.repeat((3<<20)+1)},{contentType:'application/json',path:'/tmp/foreign-proof.json'}])await assert.rejects(proofAttachment(attachment,'/tmp/qualification'));
});

// Synthetic headers below exercise audit rejection only, not artifact production.
test('native target requires explicit supported architecture and matching actual Node host',()=>{
  assert.equal(nativeTarget('amd64','linux','x64').goArch,'amd64');
  assert.equal(nativeTarget('arm64','linux','arm64').goArch,'arm64');
  for(const args of [[undefined,'linux','x64'],['x64','linux','x64'],['__proto__','linux','x64'],['arm64','linux','x64'],['amd64','linux','arm64'],['arm64','darwin','arm64']])assert.throws(()=>nativeTarget(...args));
});
test('native SDK tools and ELF reject borrowed architectures, WASM, class and endian',()=>{
  for(const arch of ['amd64','arm64']){
    const target=nativeTarget(arch,'linux',arch==='amd64'?'x64':'arm64');
    assert.deepEqual(nativeToolPaths(target).filter(name=>name.startsWith('pkg/tool/')),['compile','link','vet'].map(name=>'pkg/tool/linux_'+arch+'/'+name));
    const header=Buffer.alloc(20);header.set(Buffer.from('7f454c460201','hex'));header.set(Buffer.from(target.elfMachine,'hex'),18);assert.doesNotThrow(()=>assertNativeELF(header,target));
    const other=nativeTarget(arch==='amd64'?'arm64':'amd64','linux',arch==='amd64'?'arm64':'x64');assert.throws(()=>assertNativeELF(header,other));
    for(const mutate of [h=>{h[4]=1;},h=>{h[5]=2;},h=>{h[0]=0;}]){const invalid=Buffer.from(header);mutate(invalid);assert.throws(()=>assertNativeELF(invalid,target));}
    assert.throws(()=>assertNativeELF(Buffer.from('0061736d01000000','hex'),target));
  }
  assert.throws(()=>nativeToolPaths({goArch:'arm64'}));
});
test('native Go environment rejects cross target and host while preserving the selected pair',()=>{
  const target=nativeTarget('arm64','linux','arm64'),env={GOOS:'linux',GOARCH:'arm64',GOHOSTOS:'linux',GOHOSTARCH:'arm64'};
  assert.doesNotThrow(()=>assertNativeGoEnvironment(env,target));
  for(const key of Object.keys(env))assert.throws(()=>assertNativeGoEnvironment({...env,[key]:key.includes('ARCH')?'amd64':'darwin'},target));
  assert.throws(()=>assertNativeGoEnvironment({GOOS:'linux',GOARCH:'arm64'},target));
});
test('actual build metadata requires the selected SDK and native target in each executable record',()=>{
  const target=nativeTarget('arm64','linux','arm64'),record='/owned/server: go1.27.3\n\tbuild\tGOOS=linux\n\tbuild\tGOARCH=arm64\n\tbuild\tCGO_ENABLED=1\n';
  assert.doesNotThrow(()=>assertNativeBuildInfo(record,'/owned/server','1.27.3',target));
  for(const invalid of [record.replace('GOARCH=arm64','GOARCH=amd64'),record.replace('go1.27.3','go1.24.13'),record.replace('CGO_ENABLED=1','CGO_ENABLED=0'),record+record,record.replace('\tbuild\tGOOS=linux\n',''),'/foreign'+record,record+'\tbuild\tGOARCH=amd64\n',record+'\tbuild\tGOARCH=arm64\n'])assert.throws(()=>assertNativeBuildInfo(invalid,'/owned/server','1.27.3',target));
  assert.throws(()=>assertNativeBuildInfo('/other: go1.27.3\n\tbuild\tGOARCH=arm64\n'+record.replace('GOARCH=arm64','GOARCH=amd64'),'/owned/server','1.27.3',target));
});

// These synthetic report objects audit evidence rejection only; they never
// replace a real application response, process receipt or browser execution.
test('new merge application requires its own exact72 source and profiles despite equal legacy cardinality',()=>{
  const value=matrix();
  for(const spec of value.suites[0].specs){spec.file='tests/merge-spec.mjs';for(const item of spec.tests)item.projectName=item.projectName.replace('retained-','merge-');}
  assert.equal(browserCases(value,true,apps.merge).length,72);
  assert.throws(()=>browserCases(matrix(),true,apps.merge));assert.throws(()=>browserCases(value,true,apps.retained));assert.throws(()=>browserCases(value,true,{...apps.merge}));
  const skipped=clone(value);skipped.stats.skipped=1;assert.throws(()=>browserCases(skipped,true,apps.merge));
});
function twoAppClosures(){
  const value={version:2,status:'PASSED',cases:144,auxiliaryNativeProcesses:2,totalNativeProcesses:146,apps:{},nativeProcesses:[],auxiliaryNativeProofs:[]};
  for(const id of ['retained','merge']){
    const app=apps[id],records=Array.from({length:app.cases},(_,index)=>({app:id,id:id+'-profile|'+index,cwd:'/owned/tmp/'+id+'-'+index,closed:true,groupEmpty:true,listenerRefused:true}));
    value.nativeProcesses.push(...records);value.auxiliaryNativeProofs.push({app:id,cwd:'/owned/tmp/'+id+'-probe',closed:true,groupEmpty:true,listenerRefused:true});
    value.apps[id]={cases:app.cases,nativeProcesses:records,auxiliaryNativeProcesses:1,totalNativeProcesses:app.cases+1,assetProof:app.assetProof};
  }
  return value;
}
test('all146 closures preserve old73 subset and independently require new73',()=>{
  assert.deepEqual(browserClosureTotals(twoAppClosures()),{browser:144,native:146,retained:73,merge:73});
  const mutations=[value=>{value.totalNativeProcesses=145;},value=>{value.apps.retained.totalNativeProcesses=72;},value=>{value.apps.merge.totalNativeProcesses=72;},value=>{value.auxiliaryNativeProofs.pop();},value=>{value.nativeProcesses.pop();},value=>{value.apps.merge.nativeProcesses[0].cwd=value.apps.retained.nativeProcesses[0].cwd;},value=>{value.apps.merge.nativeProcesses[0].closed=false;},value=>{value.apps.merge.assetProof=value.apps.retained.assetProof;}];
  for(const mutate of mutations){const value=twoAppClosures();mutate(value);assert.throws(()=>browserClosureTotals(value));}
});

// These pure schemas test mandatory selection plumbing, not actual execution.
test('all new personality requirements are mandatory bounded unique names including more than128 core groups',()=>{
  const contract={tests:{},browserTitles:Array.from({length:18},(_,i)=>'old '+i),mergeBrowserTitles:[...Array.from({length:17},(_,i)=>'new '+i),lazyModuleTitle]};
  for(const target of ['native','core','devcore','module','devmodule','portable','ui'])contract.tests[target]=Array.from({length:target==='core'?133:1},(_,i)=>'TestRequired'+i);
  assert.doesNotThrow(()=>validateQualificationRequirements(contract));
  const changes=[value=>{delete value.tests.module;},value=>{delete value.tests.devmodule;},value=>{value.tests.module=[];},value=>{value.tests.core.push(value.tests.core[0]);},value=>{value.tests.core=Array.from({length:257},(_,i)=>'TestRequired'+i);},value=>{value.tests.ui=['arbitrary'];},value=>{value.mergeBrowserTitles.pop();},value=>{value.mergeBrowserTitles[value.mergeBrowserTitles.length-1]='different lazy case';},value=>{value.mergeBrowserTitles[1]=value.mergeBrowserTitles[0];}];
  for(const change of changes){const value=clone(contract);change(value);assert.throws(()=>validateQualificationRequirements(value));}
});
test('a missing new named Props or Merge PASS never becomes optional in a module personality',()=>{
  const required=['TestPropsNewSource','TestMergeNewPlan'];
  for(const missing of required){const actual=required.filter(name=>name!==missing).flatMap(name=>[{Action:'run',Package:pkg,Test:name},{Action:'pass',Package:pkg,Test:name}]);actual.push({Action:'pass',Package:pkg});assert.throws(()=>goResults(events(actual),required));}
});

// Synthetic module observations below exercise schema rejection only. They do
// not supply application bytes, production metadata, browser PASS or served proof.
const modulePrefix='/_nextgo/'+'a'.repeat(20)+'/',moduleOrigin='http://127.0.0.1:4188';
function moduleProof(digest='b'.repeat(64),bytes=12345){return {version:1,abi:6,records:['initial','held-recovery'].map(phase=>({phase,url:moduleOrigin+modulePrefix+'module-'+digest+'.wasm',status:200,mime:'application/wasm',bytes,sha256:digest,complete:true,wasmMagic:'0061736d01000000',hostABI:6,buildPrefix:modulePrefix}))};}
function moduleMatrix(){return ['chromium','firefox','webkit','webkit-touch'].map(profile=>({id:'merge-'+profile+'|'+lazyModuleTitle,proof:moduleProof()}));}
test('actual module proof schema requires exactly two complete ABI6 response records and no compiled size substitute',()=>{
  assert.deepEqual(validateLazyModuleProof(moduleProof(),modulePrefix,moduleOrigin),{sha256:'b'.repeat(64),bytes:12345,abi:6,buildPrefix:modulePrefix,downloads:2});
  const changes=[v=>{v.abi=5;},v=>{v.version='1';},v=>{v.compiledSize=12345;},v=>{v.records.pop();},v=>{v.records.push(v.records[0]);},v=>{v.records[0].compiledSize=12345;},v=>{v.records[0].status='200';},v=>{v.records[0].mime='text/javascript';},v=>{v.records[0].complete=1;},v=>{v.records[0].wasmMagic='0061736d02000000';},v=>{v.records[1].hostABI=5;},v=>{v.records.reverse();}];
  for(const change of changes){const value=moduleProof();change(value);assert.throws(()=>validateLazyModuleProof(value,modulePrefix,moduleOrigin));}
});
test('module proof URL hash build prefix native origin and decoded size cannot borrow main or foreign assets',()=>{
  const changes=[v=>{v.records[0].url='https://example.com'+modulePrefix+'module-'+v.records[0].sha256+'.wasm';},v=>{v.records[0].url=moduleOrigin+modulePrefix+'app.wasm';},v=>{v.records[0].url+='?version=1';},v=>{v.records[0].buildPrefix='/_nextgo/'+'c'.repeat(20)+'/';},v=>{v.records[0].sha256='c'.repeat(64);},v=>{v.records[0].bytes=0;},v=>{v.records[0].bytes=(8<<20)+1;},v=>{v.records[0].bytes='12345';},v=>{v.records[0].bytes=12345.5;}];
  for(const change of changes){const value=moduleProof();change(value);assert.throws(()=>validateLazyModuleProof(value,modulePrefix,moduleOrigin));}
  assert.throws(()=>validateLazyModuleProof(moduleProof(),modulePrefix.replaceAll('a','c'),moduleOrigin));
  assert.throws(()=>validateLazyModuleProof(moduleProof(),modulePrefix,'http://example.com:4188'));
});
test('both actual module downloads must agree in decoded hash and bytes rather than merely each having valid URLs',()=>{
  const differentHash=moduleProof();differentHash.records[1]=moduleProof('c'.repeat(64)).records[1];assert.throws(()=>validateLazyModuleProof(differentHash,modulePrefix,moduleOrigin));
  const differentBytes=moduleProof();differentBytes.records[1].bytes++;assert.throws(()=>validateLazyModuleProof(differentBytes,modulePrefix,moduleOrigin));
});
test('each of four exact lazy profiles requires its own proof and all eight downloads must agree',()=>{
  assert.deepEqual(validateLazyModuleMatrix(moduleMatrix(),modulePrefix,moduleOrigin),{sha256:'b'.repeat(64),bytes:12345,abi:6,buildPrefix:modulePrefix,profiles:4,downloads:8});
  const changes=[v=>{v.pop();},v=>{v.push(v[0]);},v=>{v[1].id=v[0].id;},v=>{v[0].id='retained-chromium|'+lazyModuleTitle;},v=>{v[0].id='merge-chromium|different case';},v=>{v[0].proof=moduleProof('c'.repeat(64));},v=>{v[0].proof=moduleProof('b'.repeat(64),12346);},v=>{v[0].borrowed=true;}];
  for(const change of changes){const value=moduleMatrix();change(value);assert.throws(()=>validateLazyModuleMatrix(value,modulePrefix,moduleOrigin));}
});
test('module attachment has its own finite4KiB decoded cap while native proof default remains unchanged',async()=>{
  const body=Buffer.from(JSON.stringify(moduleProof())).toString('base64');assert.deepEqual(await proofAttachment({contentType:'application/json',body},'/tmp/qualification',4096),moduleProof());
  const oversized=Buffer.from(JSON.stringify({padding:'x'.repeat(4096)})).toString('base64');await assert.rejects(proofAttachment({contentType:'application/json',body:oversized},'/tmp/qualification',4096));
  await assert.rejects(proofAttachment({contentType:'application/json',body},'/tmp/qualification',8192));
});

// Pure fake-native accessor tests. These do not simulate native browser scroll
// restoration, substitute application responses or prove a browser diagnosis.
function scrollAccessorFixture() {
  const calls = {get: 0, set: 0, coercions: 0, receivers: [], inputs: []};
  const state = {value: 0, getError: null, setError: null, setResult: undefined, busy: false, active: true};
  const prototype = {};
  Object.defineProperty(prototype, 'scrollTop', {configurable: true, enumerable: true,
    get() {calls.get++; calls.receivers.push(this); if (state.getError) throw state.getError; return state.value;},
    set(value) {calls.set++; calls.receivers.push(this); calls.inputs.push(value); if (state.setError) throw state.setError; state.value = Number(value); return state.setResult;},
  });
  const owner = Object.create(prototype);
  Object.assign(owner, {hidden: false, isConnected: true, hasAttribute: name => name === 'data-nextgo-view-active' && state.active});
  const world = {location: {pathname: '/owner'}, document: {querySelector: () => owner, getElementById: () => ({hasAttribute: name => name === 'aria-busy' && state.busy})}};
  return {owner, world, calls, state, prototype};
}
test('owner scroll observer install and finish perform zero native scroll reads or writes', () => {
  const f = scrollAccessorFixture(), original = Object.getOwnPropertyDescriptor(f.prototype, 'scrollTop');
  assert.equal(observeRetainedOwnerScroll({world: f.world}).installed, true);
  assert.equal(f.calls.get, 0); assert.equal(f.calls.set, 0);
  const result = observeRetainedOwnerScroll({world: f.world, command: 'finish'});
  assert.equal(result.disposed, true); assert.equal(result.stateCleared, true); assert.equal(result.rows.length, 0);
  assert.equal(f.calls.get, 0); assert.equal(f.calls.set, 0);
  assert.equal(Object.getOwnPropertyDescriptor(f.owner, 'scrollTop'), undefined);
  assert.deepEqual(Object.getOwnPropertyDescriptor(f.prototype, 'scrollTop'), original);
});
test('owner scroll observer forwards exactly one native call with original receiver and coercion', () => {
  const f = scrollAccessorFixture(); observeRetainedOwnerScroll({world: f.world});
  const input = {valueOf() {f.calls.coercions++; return 200;}};
  f.owner.scrollTop = input;
  assert.equal(f.calls.set, 1); assert.equal(f.calls.get, 0); assert.equal(f.calls.coercions, 1); assert.equal(f.calls.inputs[0], input);
  assert.equal(f.owner.scrollTop, 200); assert.equal(f.calls.get, 1);
  f.state.setResult = 17;
  const own = Object.getOwnPropertyDescriptor(f.owner, 'scrollTop');
  assert.equal(own.set.call(f.owner, 42), 17); assert.equal(f.calls.set, 2); assert.equal(f.calls.get, 1);
  assert.ok(f.calls.receivers.every(owner => owner === f.owner));
  const result = observeRetainedOwnerScroll({world: f.world, command: 'finish'});
  assert.equal(f.calls.set, 2); assert.equal(f.calls.get, 1);
  assert.deepEqual(result.rows.map(row => [row.get, row.set, row.requestedPresent, row.returnedPresent]), [[false,true,false,false],[true,false,false,true],[false,true,true,true]]);
  assert.equal(result.rows[0].returned, 0); assert.equal(result.rows[2].returned, 17);
  const special = scrollAccessorFixture(); observeRetainedOwnerScroll({world: special.world});
  special.state.value = NaN; assert.ok(Number.isNaN(special.owner.scrollTop));
  special.owner.scrollTop = Infinity; assert.equal(special.state.value, Infinity);
  const nonfinite = observeRetainedOwnerScroll({world: special.world, command: 'finish'});
  assert.equal(special.calls.get, 1); assert.equal(special.calls.set, 1);
  assert.equal(nonfinite.rows[0].returnedPresent, false); assert.equal(nonfinite.rows[1].requestedPresent, false);
});
test('owner scroll observer preserves native thrown identity without coercion or hidden getter', () => {
  const f = scrollAccessorFixture(); observeRetainedOwnerScroll({world: f.world});
  const setterError = new Error('native setter'), getterError = new Error('native getter');
  const input = {valueOf() {throw Error('observer must not coerce');}};
  f.state.setError = setterError;
  assert.throws(() => {f.owner.scrollTop = input;}, error => error === setterError);
  assert.equal(f.calls.set, 1); assert.equal(f.calls.get, 0); assert.equal(f.calls.coercions, 0);
  f.state.getError = getterError;
  assert.throws(() => f.owner.scrollTop, error => error === getterError);
  const result = observeRetainedOwnerScroll({world: f.world, command: 'finish'});
  assert.equal(f.calls.set, 1); assert.equal(f.calls.get, 1);
  assert.equal(result.rows.length, 2); assert.ok(result.rows.every(row => row.threw));
  assert.ok(result.rows.every(row => !row.returnedPresent));
  assert.equal(result.rows[0].requestedPresent, false);
});
test('owner scroll observer records only finite scalars and truthful phase booleans with a hard64row cap', () => {
  const f = scrollAccessorFixture(); observeRetainedOwnerScroll({world: f.world});
  f.world.location.pathname = '/b'; f.owner.hidden = true; f.owner.isConnected = false; f.state.busy = true; f.state.active = false;
  for (let i = 0; i < 65; i++) {f.state.value = i; assert.equal(f.owner.scrollTop, i);}
  const result = observeRetainedOwnerScroll({world: f.world, command: 'finish'});
  assert.equal(f.calls.get, 65); assert.equal(f.calls.set, 0); assert.equal(result.total, 65); assert.equal(result.dropped, 1); assert.equal(result.complete, false);
  assert.equal(result.rows.length, 64); assert.deepEqual(result.rows.map(row => row.sequence), Array.from({length: 64}, (_, i) => i + 1));
  assert.ok(result.rows.every(row => row.phaseKnown && row.pathB && !row.pathOwner && row.busy && !row.connected && row.hidden && !row.active));
  assert.ok(result.rows.every(row => Object.values(row).every(value => typeof value === 'boolean' || typeof value === 'number' && Number.isFinite(value))));
  const fields = ['sequence','get','set','threw','requestedPresent','requested','returnedPresent','returned','phaseKnown','pathOwner','pathB','busy','connected','hidden','active'].sort();
  assert.ok(result.rows.every(row => {assert.deepEqual(Object.keys(row).sort(), fields); return true;}));
  for (const unavailable of [() => null, () => {throw Error('phase unavailable');}]) {
    const unknown = scrollAccessorFixture(); unknown.world.document.getElementById = unavailable;
    observeRetainedOwnerScroll({world: unknown.world}); assert.equal(unknown.owner.scrollTop, 0);
    const report = observeRetainedOwnerScroll({world: unknown.world, command: 'finish'});
    assert.equal(unknown.calls.get, 1); assert.equal(unknown.calls.set, 0);
    assert.equal(report.complete, false); assert.equal(report.rows[0].phaseKnown, false);
  }
});
test('owner scroll observer refuses existing or unsupported properties without modifying them', () => {
  const f = scrollAccessorFixture();
  Object.defineProperty(f.owner, 'scrollTop', {value: 9, configurable: true});
  const own = Object.getOwnPropertyDescriptor(f.owner, 'scrollTop');
  assert.equal(observeRetainedOwnerScroll({world: f.world}).installed, false);
  assert.deepEqual(Object.getOwnPropertyDescriptor(f.owner, 'scrollTop'), own); assert.equal(f.calls.get, 0); assert.equal(f.calls.set, 0);
  const missing = scrollAccessorFixture(); Object.setPrototypeOf(missing.owner, {});
  assert.equal(observeRetainedOwnerScroll({world: missing.world}).installed, false);
  const closed = scrollAccessorFixture(); Object.preventExtensions(closed.owner);
  assert.equal(observeRetainedOwnerScroll({world: closed.world}).installed, false);
  assert.equal(observeRetainedOwnerScroll({world: closed.world, command: 'finish'}).reason, 'not-installed');
});
test('owner scroll observer disposal never overwrites a later foreign own descriptor', () => {
  const f = scrollAccessorFixture(); observeRetainedOwnerScroll({world: f.world});
  Object.defineProperty(f.owner, 'scrollTop', {value: 23, configurable: true});
  const result = observeRetainedOwnerScroll({world: f.world, command: 'finish'});
  assert.equal(result.disposed, false); assert.equal(result.complete, false); assert.equal(result.stateCleared, true);
  assert.equal(Object.getOwnPropertyDescriptor(f.owner, 'scrollTop').value, 23);
  assert.equal(f.calls.get, 0); assert.equal(f.calls.set, 0);
});

// New lifecycle integration source uses the actual runner join and completion
// functions with synchronous fake kernel outcomes and joined fake timer receipts.
// It launches no process, touches no /proc and schedules no real timer.
const drainCommandFixture=(code=0)=>({name:'bounded-command',pid:700,closed:true,code,signal:null,groupEmpty:false,cleanupDeferred:false,cleanupTimedOut:false,timedOut:false,interrupted:false,outputExceeded:false});
function commandDrainIO(outcomes,{onWait}={}){
  let clock=0,index=0;const observed=[],waits=[];
  return {observed,waits,io:{now:()=>clock,probe:group=>{observed.push(group);return outcomes[Math.min(index++,outcomes.length-1)];},wait:async(ms,signal)=>{waits.push(ms);clock+=ms;onWait?.(signal);return {joined:true,outstanding:0,timersStarted:1,timersCleared:1,abortListenersAdded:signal?1:0,abortListenersRemoved:signal?1:0};}}};
}
test('actual command completion keeps exit1 failed even after a joined ESRCH group drain',async()=>{
  const proof=drainCommandFixture(1),fake=commandDrainIO(['PRESENT','PRESENT','ESRCH']);
  const result=await joinClosedCommandGroup(proof,{deadlineMS:500},fake.io);
  assert.equal(result.empty,true);assert.equal(result.lastOutcome,'ESRCH');assert.equal(result.joined,true);assert.equal(result.outstanding,0);assert.equal(proof.groupEmpty,true);assert.deepEqual(fake.observed,[700,700,700]);assert.equal(fake.waits.length,2);
  assert.throws(()=>validateCommandCompletion(proof));
});
test('actual command acceptance requires both original zero-exit guards and the attached ESRCH receipt',async()=>{
  const proof=drainCommandFixture(),fake=commandDrainIO(['ESRCH']);await joinClosedCommandGroup(proof,{deadlineMS:500},fake.io);
  assert.strictEqual(validateCommandCompletion(proof),proof);assert.equal(fake.waits.length,0);
  for(const change of [{closed:false},{groupEmpty:false},{code:1},{signal:'SIGTERM'},{cleanupDeferred:true},{cleanupTimedOut:true},{timedOut:true},{interrupted:true},{outputExceeded:true},{groupDrain:undefined}])assert.throws(()=>validateCommandCompletion({...proof,...change}));
  assert.throws(()=>validateCommandCompletion(proof,Error('spawn failure')));
  for(const mutate of [r=>{r.deadlineMS=r.finishedAtMS;},r=>{r.attempts=99;},r=>{r.timersCleared=r.timersStarted+1;},r=>{r.outstanding=null;},r=>{r.signalAuthority=true;}]){const changed=structuredClone(proof);mutate(changed.groupDrain);assert.throws(()=>validateCommandCompletion(changed));}

});
test('present permission and unavailable command groups never gain empty credit from leader close',async()=>{
  for(const outcome of ['PRESENT','PERMISSION_DENIED','UNAVAILABLE']){
    const proof=drainCommandFixture(),fake=commandDrainIO([outcome]);const result=await joinClosedCommandGroup(proof,{deadlineMS:35},fake.io);
    assert.equal(result.empty,false);assert.equal(result.signalAuthority,false);assert.equal(result.censusConsulted,false);assert.equal(proof.groupEmpty,false);assert.equal(proof.cleanupDeferred,true);assert.equal(result.outstanding,0);assert.throws(()=>validateCommandCompletion(proof));
  }
});
test('original command deadline prevents fresh post-close budget and a later scripted ESRCH is not consumed',async()=>{
  const proof=drainCommandFixture(),fake=commandDrainIO(['PRESENT','ESRCH']);const result=await joinClosedCommandGroup(proof,{deadlineMS:10},fake.io);
  assert.equal(result.empty,false);assert.equal(result.reason,'DEADLINE_EXPIRED');assert.equal(result.deadlineMS,10);assert.deepEqual(fake.observed,[700]);assert.deepEqual(fake.waits,[10]);assert.equal(result.outstanding,0);assert.throws(()=>validateCommandCompletion(proof));
});
test('interruption output cap timeout and a superseded normal join preserve their original failures',async()=>{
  for(const flag of ['interrupted','outputExceeded','timedOut']){const proof={...drainCommandFixture(),[flag]:true},fake=commandDrainIO(['ESRCH']);await joinClosedCommandGroup(proof,{deadlineMS:500},fake.io);assert.equal(proof.groupEmpty,false);assert.equal(proof.cleanupDeferred,true);assert.throws(()=>validateCommandCompletion(proof));}
  const proof=drainCommandFixture(),fake=commandDrainIO(['ESRCH']);await joinClosedCommandGroup(proof,{deadlineMS:500,canCredit:()=>false},fake.io);assert.equal(proof.groupEmpty,false);assert.throws(()=>validateCommandCompletion(proof));
});
test('abort during a joined observation wait leaves no normal command credit or outstanding timer',async()=>{
  const controller=new AbortController(),proof=drainCommandFixture(),fake=commandDrainIO(['PRESENT','ESRCH'],{onWait:()=>controller.abort()});const result=await joinClosedCommandGroup(proof,{deadlineMS:500,signal:controller.signal},fake.io);
  assert.equal(result.reason,'ABORTED');assert.equal(result.empty,false);assert.equal(result.outstanding,0);assert.equal(result.joined,true);assert.deepEqual(fake.observed,[700]);assert.equal(proof.groupEmpty,false);assert.throws(()=>validateCommandCompletion(proof));
});
test('insufficient proof space fails closed and preserves the pre-existing command evidence',async()=>{
  const proof={...drainCommandFixture(),padding:'x'.repeat(60<<10)},before=structuredClone(proof),fake=commandDrainIO(['ESRCH']);await joinClosedCommandGroup(proof,{deadlineMS:500},fake.io);
  assert.equal(proof.groupEmpty,false);assert.equal(proof.cleanupDeferred,true);assert.equal(Object.hasOwn(proof,'groupDrain'),false);assert.equal(proof.padding,before.padding);assert.equal(proof.code,before.code);assert.throws(()=>validateCommandCompletion(proof));
});
function joinedCloseTimers(){let next=0;const handles=new Map(),cleared=[];return {handles,cleared,io:{setTimeout:(callback,ms)=>{const id=++next;handles.set(id,{callback,ms});return id;},clearTimeout:id=>{cleared.push(id);handles.delete(id);}}};}
test('command-close completion cancels its losing timer before resolve or reject',async()=>{
  for(const reject of [false,true]){let resolveClose,rejectClose;const completion=new Promise((resolve,reject)=>{resolveClose=resolve;rejectClose=reject;}),timers=joinedCloseTimers(),waiting=waitForCommandClose(completion,3000,timers.io);assert.equal(timers.handles.size,1);if(reject)rejectClose(Error('fake close failure'));else resolveClose();if(reject)await assert.rejects(waiting);else assert.equal(await waiting,true);assert.equal(timers.handles.size,0);assert.equal(timers.cleared.length,1);}
});
test('command-close expiry owns and clears its timer without claiming real child closure',async()=>{
  const timers=joinedCloseTimers(),completion=new Promise(()=>{}),waiting=waitForCommandClose(completion,2000,timers.io);const [{callback,ms}]=timers.handles.values();assert.equal(ms,2000);callback();assert.equal(await waiting,false);assert.equal(timers.handles.size,0);assert.equal(timers.cleared.length,1);
  for(const bad of [0,-1,NaN,3001])assert.throws(()=>waitForCommandClose(completion,bad,timers.io));
});

// Real bounded temporary files exercise source admission; no Go program runs.
async function fullGoFixture(run){
  const root=await realpath(await mkdtemp(join(tmpdir(),'nextgo-go-source-guard-')));
  try{
    const files=[{path:'z.go',data:Buffer.from('package guard\n')},{path:'nested/a.go',data:Buffer.from('package nested\n')}];
    await mkdir(join(root,'nested'));for(const item of files)await writeFile(join(root,item.path),item.data);
    await writeFile(join(root,'ordinary.txt'),'not part of Go identity');
    const digest=bytes=>createHash('sha256').update(bytes).digest('hex');
    const rows=files.map(item=>({path:item.path,bytes:item.data.length,sha256:digest(item.data)}));
    // Independently computed with the publisher's Python canonical encoder.
    const expected={files:2,bytes:29,sha256:'b4854a877c1aafa3cefb9504aafd49a405af52d4c7dc09d65825f2178676eb4f',treeEncoding:'sortedpathUTF8+NUL+exactsourcebytes+NUL'};
    await run({root,files,rows,expected,digest});
  }finally{await rm(root,{recursive:true,force:false,maxRetries:0});}
}
test('full-Go guard matches publisher ordering and checks Go outside selected pins',async()=>{
  await fullGoFixture(async({root,rows,expected})=>{
    const before=structuredClone(rows),actual=await validateFullGoSource(root,[...rows].reverse(),expected);
    assert.deepEqual(actual,expected);assert.deepEqual(rows,before);
  });
});
test('full-Go guard rejects modified missing added and stale expected source',async()=>{
  await fullGoFixture(async({root,rows,expected,digest})=>{
    await assert.rejects(validateFullGoSource(root,rows.slice(1),expected));
    await assert.rejects(validateFullGoSource(root,[...rows,{path:'extra.go',bytes:0,sha256:digest(Buffer.alloc(0))}],expected));
    await assert.rejects(validateFullGoSource(root,rows,{...expected,bytes:expected.bytes+1}));
    await assert.rejects(validateFullGoSource(root,rows,{...expected,sha256:'0'.repeat(64)}));
    await writeFile(join(root,rows[0].path),'package changed\n');
    await assert.rejects(validateFullGoSource(root,rows,expected));
    await rm(join(root,rows[0].path));
    await assert.rejects(validateFullGoSource(root,rows,expected));
  });
});
test('full-Go guard rejects duplicate unsafe and aliased paths before source credit',async()=>{
  await fullGoFixture(async({root,rows,expected})=>{
    await assert.rejects(validateFullGoSource(root,[rows[0],rows[0]],expected));
    for(const path of ['../outside.go','/outside.go','nested/../outside.go','nested\\outside.go','bad\u0000.go'])await assert.rejects(validateFullGoSource(root,[{...rows[0],path},rows[1]],expected));
    await symlink(join(root,rows[0].path),join(root,'alias.go'));
    await assert.rejects(validateFullGoSource(root,[{...rows[0],path:'alias.go'},rows[1]],expected));
    await symlink(join(root,'nested'),join(root,'alias-directory'));
    await assert.rejects(validateFullGoSource(root,[rows[0],{...rows[1],path:'alias-directory/a.go'}],expected));
  });
});
test('full-Go guard refuses unbounded and malformed expected identity',async()=>{
  await fullGoFixture(async({root,rows,expected})=>{
    for(const delta of [{files:0},{files:8193},{bytes:-1},{bytes:(128<<20)+1},{sha256:'bad'},{treeEncoding:'other'}])await assert.rejects(validateFullGoSource(root,rows,{...expected,...delta}));
    await assert.rejects(validateFullGoSource(root,[{...rows[0],bytes:(16<<20)+1},rows[1]],expected));
    await assert.rejects(validateFullGoSource(root,[{...rows[0],sha256:'bad'},rows[1]],expected));
  });
});
