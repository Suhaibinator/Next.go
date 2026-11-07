// Pure evidence-auditor negatives. No application responses or processes.
import test from 'node:test';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';
import {goResults,browserCases,validateNativeProof,strictNativeProof,proofAttachment} from './retained-routes-ci.mjs';

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
