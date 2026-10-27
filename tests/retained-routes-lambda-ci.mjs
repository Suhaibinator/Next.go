// LOCAL actual-ZIP qualification for both apps. Original native144/146 remains
// mandatory; no source/compiler/provider evidence is synthesized by this lane.
import {mkdir,realpath,symlink,writeFile} from 'node:fs/promises';
import {join,dirname,relative,isAbsolute,resolve,sep} from 'node:path';
import {pathToFileURL,fileURLToPath} from 'node:url';
import {check,sha,json,readBounded,writeJSON} from './static-shell-ci-build.mjs';
import {owned,childEnv,unchanged,runChild,strictNativeProof,proofAttachment,browserClosureTotals,validateLazyModuleProof,validateLazyModuleMatrix,lazyModuleTitle} from './retained-routes-ci.mjs';
import {extractLambdaArtifact,assertLambdaBuildInfo,artifactHash} from './retained-routes-lambda-artifact.mjs';
import {nativeTarget} from './retained-routes-target.mjs';
import {validateLambdaProof} from './retained-routes-lambda-proof.mjs';
import {lambdaApps,lambdaAppOrder,lambdaProfiles,lambdaModes,nativeOnly,deadlineCase,lambdaCases,lambdaClosureTotals} from './retained-routes-lambda-matrix.mjs';
import {probeZIPAssets,expectedAssetErrors,validateZIPModuleProof} from './retained-routes-lambda-assets.mjs';
const self=fileURLToPath(import.meta.url),maximumReport=16<<20;
export {nativeOnly,deadlineCase,lambdaCases};
const outside=(parent,path)=>{const value=relative(parent,path);return isAbsolute(value)||value==='..'||value.startsWith('..'+sep);};
const taskError=()=>[{path:'/api/task',message:'context deadline exceeded',type:'NextGoInvocationError',class:'deadline'}];
export function adaptSharedSpec(source,fixtureURL,gatesURL,app='retained'){
  check(Object.hasOwn(lambdaApps,app),'reviewed shared app');const first="'./"+lambdaApps[app].fixture+"'",second="'./link-status-gates.mjs'";
  check(source.split(first).length===2&&source.split(second).length===2,'exact original two import seams');return source.replace(first,JSON.stringify(fixtureURL)).replace(second,JSON.stringify(gatesURL));
}
async function sameSDK(native){
  const target=nativeTarget(process.env.NEXTGO_RETAINED_NATIVE_ARCH,process.platform,process.arch);check(target.goArch===native.sdk.nativeTarget,'actual native target changed');
  check(process.platform==='linux'&&native.sdk.platform==='linux'&&native.sdk.architecture===process.arch&&native.sdk.nativeTarget===process.env.NEXTGO_RETAINED_NATIVE_ARCH&&native.sdk.version==='go'+process.env.GO_VERSION&&process.version===native.sdk.node.version&&await realpath(process.execPath)===native.sdk.node.path&&sha(await readBounded(process.execPath,128<<20))===native.sdk.node.sha256,'actual selected native Node/SDK context changed');
  for(const [name,pin]of Object.entries(native.sdk.files)){const bytes=await readBounded(join(native.sdk.root,name),128<<20);check(bytes.length===pin.bytes&&sha(bytes)===pin.sha256,'actual SDK compiler/runner/shim changed: '+name);}
  check(sha(await readBounded(native.nativeCompiler.path,128<<20))===native.nativeCompiler.sha256,'actual qualified native C compiler changed');
}
export async function checkHarness(state){
  const {root,source,report}=state;check(report.harness&&Object.keys(report.harness).sort().join(',')==='merge,retained','both materialized apps required');
  for(const id of lambdaAppOrder){
    const app=lambdaApps[id],h=report.harness[id];check(h.folder===join(root,'lambda-harness',id)&&await realpath(h.folder)===h.folder,'canonical per-app materialized harness');
    const observed={app:id,sharedSHA256:sha(await readBounded(join(h.folder,app.sharedFile),1<<20)),nodeModules:await realpath(join(h.folder,'node_modules')),sourceNodeModules:await realpath(join(source,'node_modules'))};
    if(id==='retained')observed.counterpartSHA256=sha(await readBounded(join(h.folder,'retained-routes-lambda.spec.mjs'),1<<20));
    report.harnessChecks??=[];check(report.harnessChecks.length<64,'finite harness observation bound');report.harnessChecks.push(observed);
    check(observed.sharedSHA256===h.sharedSHA256&&observed.nodeModules===h.nodeModules&&observed.sourceNodeModules===h.nodeModules&&(id!=='retained'||observed.counterpartSHA256===h.counterpartSHA256),'materialized bodies/imports/dependency identity changed');
  }
}
async function context(){
  const {root,source}=await owned(),native=await json(join(root,'campaign.json'),8<<20),nativeProof=await json(join(root,'browser-proof.json'),4<<20);check(native.status==='PASSED','actual original retained72/73 and merge72/73 must pass first');browserClosureTotals(nativeProof);await sameSDK(native);
  const inputs=await json(join(root,'inputs.json'),1<<20);check(inputs.lambdaQualification?.enabled===true&&inputs.lambdaQualification.actualZIPRequired===true&&inputs.lambdaQualification.applications?.join(',')==='retained,merge','reviewed two-app actual ZIP input contract required');
  const sourceState=await json(join(root,'source-generated.json'),8<<20);await unchanged(source,sourceState);
  let report;try{report=await json(join(root,'lambda-campaign.json'),maximumReport);}catch(error){if(error.code!=='ENOENT')throw error;report={version:2,status:'NOT_STARTED',scope:'LOCAL actual ZIP bootstrap for retained and merge; buffered and streaming; no AWS',steps:[],sdk:native.sdk,nativeSourceCommit:native.sourceCommit,nativeProofSHA256:sha(await readBounded(join(root,'browser-proof.json'),4<<20)),apps:{},modes:{},ZIPHTTPBaselines:[]};}
  check(report.version===2&&report.nativeProofSHA256===sha(await readBounded(join(root,'browser-proof.json'),4<<20)),'original two-app proof changed');
  const env=childEnv(root,source,{GOROOT:native.sdk.root,PATH:dirname(native.sdk.go)+':'+dirname(native.sdk.node.path)+':/usr/bin:/bin',CGO_ENABLED:'1',CC:native.nativeCompiler.path});return {root,source,native,nativeProof,inputs,report,env,sourceState};
}
async function step(state,name,command,args,env=state.env,timeoutMS=60_000,stdoutLimit=1<<20){state.report.steps.push({name,command,args,timeoutMS});await writeJSON(join(state.root,'lambda-campaign.json'),state.report,maximumReport);return runChild(state.root,name,command,args,{cwd:state.source,env,timeoutMS,stdoutLimit});}
async function guarded(action){const state=await context();let error;try{await action(state);}catch(failure){error=failure;state.report.status='FAILED';state.report.failure=String(failure.message).slice(0,2048);}finally{for(const [name,guard]of [['source',()=>unchanged(state.source,state.sourceState)],['sdk',()=>sameSDK(state.native)],['harness',async()=>{if(state.report.harness)await checkHarness(state);}]]){try{await guard();}catch(failure){error=error?new AggregateError([error,failure],'LOCAL Lambda phase and final '+name+' guard failed'):failure;state.report.status='FAILED';state.report[name+'Failure']=String(failure.message).slice(0,1024);}}await writeJSON(join(state.root,'lambda-campaign.json'),state.report,maximumReport);}if(error)throw error;}
async function materialize(state){
  const {root,source,report}=state;await mkdir(join(root,'lambda-harness'),{mode:0o700});report.harness={};
  for(const id of lambdaAppOrder){const app=lambdaApps[id],folder=join(root,'lambda-harness',id);await mkdir(folder,{mode:0o700});const original=await readBounded(join(source,'tests',app.spec),1<<20),fixture=pathToFileURL(join(source,'tests',app.lambdaFixture)).href,gates=pathToFileURL(join(source,'tests/link-status-gates.mjs')).href;
    const shared=adaptSharedSpec(original.toString('utf8'),fixture,gates,id);await writeFile(join(folder,app.sharedFile),shared,{flag:'wx',mode:0o600});const h={folder,originalSpecSHA256:sha(original),sharedSHA256:sha(Buffer.from(shared)),nodeModules:await realpath(join(source,'node_modules'))};
    if(id==='retained'){const counterpart=await readBounded(join(source,'tests/retained-routes-lambda.spec.mjs'),1<<20),needle="'./retained-routes-lambda-fixture.mjs'";check(counterpart.toString('utf8').split(needle).length===2,'one counterpart import binding');const adapted=counterpart.toString('utf8').replace(needle,JSON.stringify(fixture));await writeFile(join(folder,'retained-routes-lambda.spec.mjs'),adapted,{flag:'wx',mode:0o600});Object.assign(h,{counterpartOriginalSHA256:sha(counterpart),counterpartSHA256:sha(Buffer.from(adapted))});}
    await symlink(join(source,'node_modules'),join(folder,'node_modules'),'dir');check(await realpath(join(folder,'node_modules'))===h.nodeModules,'one canonical Playwright dependency instance');report.harness[id]=h;
  }
  await checkHarness(state);
}
async function packageLambda(){await guarded(async state=>{
  const {root,source,native,report}=state;check(report.status==='NOT_STARTED','one actual two-app package build, no retry');
  await step(state,'lambda-auditor-tests',process.execPath,['--test','tests/retained-routes-lambda-fixture.test.mjs','tests/retained-routes-lambda-ci.test.mjs','tests/retained-routes-lambda-proof.test.mjs','tests/retained-routes-lambda-matrix.test.mjs','tests/retained-routes-lambda-assets.test.mjs','tests/retained-strict-gateway.test.mjs'],state.env,60_000);
  check(sha(await readBounded(native.cli.path,128<<20))===native.cli.sha256,'actual qualified CLI changed');
  for(const id of lambdaAppOrder){const app=lambdaApps[id],output=join(root,'dist','lambda-'+id+'-package');await step(state,'lambda-'+id+'-package-build',native.cli.path,['build','-target','lambda','-arch',native.sdk.nativeTarget,'-out',output,app.example],state.env,20*60_000);
    const artifact=await extractLambdaArtifact(output,join(root,'dist','lambda-'+id+'-bootstrap'),{arch:native.sdk.nativeTarget,goVersion:process.env.GO_VERSION}),identity=await artifactHash(artifact.binary),server={path:artifact.binary,sha256:identity.sha256,bytes:identity.bytes,dev:identity.dev,inode:identity.inode,parent:identity.parent};
    const buildInfo=await step(state,'lambda-'+id+'-bootstrap-build-info',native.sdk.go,['version','-m',server.path],state.env,60_000,128<<10);assertLambdaBuildInfo(buildInfo.toString('utf8'),server.path,{arch:native.sdk.nativeTarget,goVersion:process.env.GO_VERSION});report.apps[id]={app:id,artifact,server,bootstrapBuildInfo:buildInfo.toString('utf8')};
  }
  await materialize(state);report.status='PACKAGED';await writeJSON(join(root,'lambda-campaign.json'),report,maximumReport);
  for(const id of lambdaAppOrder){const app=report.apps[id];await step(state,'lambda-'+id+'-http-baseline',process.execPath,[self,'http-baseline',id],{...state.env,NEXTGO_RETAINED_SERVER_BINARY:app.server.path,NEXTGO_RETAINED_SERVER_SHA256:app.server.sha256,NEXTGO_RETAINED_REQUIRE_LINUX_IDENTITY:'1'},120_000);
    const baseline=await json(join(root,'lambda-'+id+'-http-baseline.json'),4<<20);check(baseline.status==='PASSED'&&baseline.app===id,'own extracted-bootstrap HTTP baseline');await strictNativeProof(baseline.native,{server:app.server,sdk:report.sdk},root,source);app.baseline=baseline;report.ZIPHTTPBaselines.push({app:id,...baseline.native});
  }
  check(report.ZIPHTTPBaselines.length===2&&new Set(report.ZIPHTTPBaselines.map(value=>value.cwd)).size===2,'distinct actual per-app ZIP HTTP lifetimes');report.status='BASELINE_ATTESTED';
});}
async function baseline(id){await guarded(async state=>{
  const {root,report}=state;check(report.status==='PACKAGED'&&Object.hasOwn(lambdaApps,id)&&report.apps[id],'both actual ZIP extractions precede each baseline');const app=report.apps[id],{startRetainedServer}=await import('./retained-routes-fixture.mjs');let native,primary;const result={version:2,app:id,status:'RUNNING',native:null};
  try{native=await startRetainedServer();Object.assign(result,await probeZIPAssets(native,id,app.artifact,report.sdk.files['lib/wasm/wasm_exec.js'].sha256,{mode:'http'}));result.nativeCGO1Comparison=await json(join(root,id==='retained'?'asset-proof.json':'merge-asset-proof.json'),2<<20);result.comparisonMeaning='Own CGO0 ZIP baseline; original per-app CGO1 proof remains a distinct executable and is not borrowed';}
  catch(error){primary=error;}finally{if(native){try{await native.close();}catch(error){primary=primary?new AggregateError([primary,error]):error;}result.native=native.snapshot();}result.status=primary?'FAILED':'PASSED';await writeJSON(join(root,'lambda-'+id+'-http-baseline.json'),result,4<<20);}if(primary)throw primary;
});}
async function probe(mode){await guarded(async state=>{
  const {root,source,report}=state;check(lambdaModes.includes(mode)&&report.status==='BASELINE_ATTESTED'&&!report.modes[mode]&&(mode==='buffered'||report.modes.buffered?.status==='PASSED'),'ordered own per-app asset probes required');report.modes[mode]={status:'ASSETS_RUNNING',apps:{},auxiliaryNativeProofs:[]};
  for(const id of lambdaAppOrder){const app=report.apps[id];let native,primary;const result={version:2,app:id,status:'RUNNING',mode,native:null};
    try{const {startRetainedServer}=await import('./retained-routes-lambda-fixture.mjs');const permitted=Object.values(app.baseline.assets).filter(asset=>asset.mime==='application/wasm'&&asset.bytes>4<<20).map(asset=>asset.path);native=await startRetainedServer({app:id,mode,binary:app.server.path,expected:app.server.sha256,permittedAssetOverflows:permitted});Object.assign(result,await probeZIPAssets(native,id,app.artifact,report.sdk.files['lib/wasm/wasm_exec.js'].sha256,{mode,baseline:app.baseline}));}
    catch(error){primary=error;}finally{if(native){try{await native.close();}catch(error){primary=primary?new AggregateError([primary,error]):error;}result.native=native.snapshot();}result.status=primary?'FAILED':'PASSED';await writeJSON(join(root,'lambda-'+mode+'-'+id+'-assets.json'),result,4<<20);}if(primary)throw primary;
    validateLambdaProof(result.native,{mode,expectedErrors:expectedAssetErrors(app.baseline,mode)});await strictNativeProof(result.native,{server:app.server,sdk:report.sdk},root,source);report.modes[mode].apps[id]={status:'ASSETS_ATTESTED',assets:result};report.modes[mode].auxiliaryNativeProofs.push({app:id,...result.native});
  }
  report.modes[mode].status='ASSETS_ATTESTED';
});}
async function browser(mode){await guarded(async state=>{
  const {root,source,report,inputs}=state;check(lambdaModes.includes(mode)&&report.modes[mode]?.status==='ASSETS_ATTESTED','both actual per-app transport flights required');await checkHarness(state);const part=report.modes[mode],all=[],cwd=new Set(part.auxiliaryNativeProofs.map(value=>value.cwd));
  for(const id of lambdaAppOrder){const app=lambdaApps[id],own=report.apps[id],h=report.harness[id],titles=inputs[app.titles].filter(title=>id!=='retained'||title!==nativeOnly).concat(id==='retained'?[deadlineCase]:[]);check(titles.length===18,'exact per-app original body/counterpart inventory');
    const output=join(root,'browser','lambda-'+mode,id),env={...state.env,HOME:join(root,'browser-home'),NEXTGO_RETAINED_LAMBDA_APP:id,NEXTGO_RETAINED_LAMBDA_MODE:mode,NEXTGO_RETAINED_LAMBDA_BINARY:own.server.path,NEXTGO_RETAINED_LAMBDA_SHA256:own.server.sha256,NEXTGO_RETAINED_LAMBDA_TEST_DIR:h.folder,NEXTGO_E2E_RESULTS:output},cli=join(source,'node_modules','@playwright','test','cli.js'),args=[cli,'test','--config=tests/retained-routes-lambda.config.mjs','--workers=1','--retries=0','--max-failures=1'];
    const discoveryPath=join(root,'report','lambda-'+mode+'-'+id+'-discovery.json'),resultPath=join(root,'report','lambda-'+mode+'-'+id+'-results.json');await step(state,'lambda-'+mode+'-'+id+'-discovery',process.execPath,[...args,'--list','--reporter=json'],{...env,PLAYWRIGHT_JSON_OUTPUT_FILE:discoveryPath},120_000);const discovered=lambdaCases(await json(discoveryPath,8<<20),mode,app,titles,false);
    await step(state,'lambda-'+mode+'-'+id+'-matrix',process.execPath,[...args,'--reporter=line,json'],{...env,PLAYWRIGHT_JSON_OUTPUT_FILE:resultPath},25*60_000,4<<20);await checkHarness(state);const results=lambdaCases(await json(resultPath,128<<20),mode,app,titles,true);check(JSON.stringify(discovered.map(value=>value.id).sort())===JSON.stringify(results.map(value=>value.id).sort()),'per-app execution/discovery differs');const native=[],modules=[];
    for(const item of results){const attachments=item.test.results[0].attachments.filter(value=>value.name===app.attachment);check(attachments.length===1,'one own actual ZIP proof per app/case');const proof=await proofAttachment(attachments[0],root);check(proof.app===id&&!cwd.has(proof.cwd)&&proof.transport==='LOCAL mock Lambda '+mode&&proof.lambda?.mode===mode,'actual app/transport/cwd binding');cwd.add(proof.cwd);validateLambdaProof(proof,{mode,expectedErrors:id==='retained'&&item.title===deadlineCase?taskError():[]});await strictNativeProof(proof,{server:own.server,sdk:report.sdk},root,source);native.push({app:id,id:item.id,title:item.title,...proof});
      const values=item.test.results[0].attachments.filter(value=>value.name==='merge-lazy-module-proof'),required=id==='merge'&&item.title===lazyModuleTitle;check(values.length===(required?1:0),'real per-app lazy module proof on exact case only');if(required){const observed=await proofAttachment(values[0],root,4096);validateLazyModuleProof(observed,own.baseline.prefix,'http://127.0.0.1:4188');validateZIPModuleProof(observed,own.baseline);const profile=lambdaProfiles.find(profile=>item.test.projectName===id+'-'+mode+'-'+profile);check(profile,'exact known module case profile');modules.push({id:'merge-'+profile+'|'+lazyModuleTitle,proof:observed});}
    }
    if(id==='merge'){part.lazyModuleProofs=modules;part.actualModuleAssets=validateLazyModuleMatrix(modules,own.baseline.prefix,'http://127.0.0.1:4188');}
    Object.assign(part.apps[id],{status:'PASSED',cases:72,nativeProcesses:native,auxiliaryNativeProcesses:1,totalNativeProcesses:73});all.push(...native);
  }
  check(all.length===144&&cwd.size===146,'own144actual cases plus2asset lifetimes per mode');Object.assign(part,{status:'PASSED',cases:144,nativeProcesses:all,auxiliaryNativeProcesses:2,totalNativeProcesses:146});await writeJSON(join(root,'lambda-'+mode+'-browser-proof.json'),part,8<<20);
  if(lambdaModes.every(value=>report.modes[value]?.status==='PASSED')){Object.assign(report,{status:'PASSED',caseExecutions:432,nativeClosures:440,closureAccounting:'Original native146 +own ZIPHTTP2 +buffered146 +streaming146; both apps own all artifacts and asset/module proofs'});lambdaClosureTotals(report,state.nativeProof);}
});}
// Re-read every actual native/ZIP/transport closure and module authority before
// destructive successful-root removal. Collection never turns failure into PASS.
export async function verifyLambdaCleanup(root,source,native,nativeProof){
  browserClosureTotals(nativeProof);const report=await json(join(root,'lambda-campaign.json'),maximumReport);lambdaClosureTotals(report,nativeProof);check(report.nativeProofSHA256===sha(await readBounded(join(root,'browser-proof.json'),4<<20)),'native proof changed before cleanup');
  for(const id of lambdaAppOrder){const app=report.apps[id],baseline=await json(join(root,'lambda-'+id+'-http-baseline.json'),4<<20);check(baseline.status==='PASSED'&&baseline.app===id&&JSON.stringify(baseline)===JSON.stringify(app.baseline),'own current ZIP baseline changed');await strictNativeProof(baseline.native,{server:app.server,sdk:native.sdk},root,source);}
  for(const mode of lambdaModes){const result=await json(join(root,'lambda-'+mode+'-browser-proof.json'),8<<20);check(JSON.stringify(result)===JSON.stringify(report.modes[mode]),'own actual mode result changed');for(const id of lambdaAppOrder){const app=report.apps[id],part=result.apps[id],assets=await json(join(root,'lambda-'+mode+'-'+id+'-assets.json'),4<<20);check(assets.status==='PASSED'&&assets.app===id&&JSON.stringify(assets)===JSON.stringify(part.assets),'own current transport assets changed');validateLambdaProof(assets.native,{mode,expectedErrors:expectedAssetErrors(app.baseline,mode)});await strictNativeProof(assets.native,{server:app.server,sdk:native.sdk},root,source);for(const value of part.nativeProcesses){validateLambdaProof(value,{mode,expectedErrors:id==='retained'&&value.title===deadlineCase?taskError():[]});await strictNativeProof(value,{server:app.server,sdk:native.sdk},root,source);}if(id==='merge'){validateLazyModuleMatrix(result.lazyModuleProofs,app.baseline.prefix,'http://127.0.0.1:4188');for(const item of result.lazyModuleProofs)validateZIPModuleProof(item.proof,app.baseline);}}}
  return report;
}
async function main(){const command=process.argv[2],argument=process.argv[3];if(command==='package')return packageLambda();if(command==='http-baseline')return baseline(argument);if(command==='assets')return probe(argument);if(command==='browser')return browser(argument);throw Error('Expected package | http-baseline retained|merge | assets buffered|streaming | browser buffered|streaming');}
if(process.argv[1]&&import.meta.url===pathToFileURL(process.argv[1]).href)main().catch(error=>{console.error(error);process.exitCode=1;});
