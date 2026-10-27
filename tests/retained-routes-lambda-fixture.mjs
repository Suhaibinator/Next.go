// Actual CLI ZIP bootstrap only. The local bridge never constructs app responses.
import {spawn} from 'node:child_process';
import {mkdtemp,lstat,rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join,isAbsolute} from 'node:path';
import {connect} from 'node:net';
import {test as base,expect} from '@playwright/test';
import {artifactHash} from './retained-routes-lambda-artifact.mjs';
import {checkNativeChild,checkedChildSignal} from './retained-routes-native-identity.mjs';
import {startLambdaGateway} from './lambda-features-gateway.mjs';

export const retainedLambdaOrigin='http://127.0.0.1:4188';
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const bounded=async(promise,ms,message)=>{let timer;try{return await Promise.race([promise,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error(message)),ms);})]);}finally{clearTimeout(timer);}};
const groupPresent=pid=>{try{process.kill(-pid,0);return true;}catch(error){if(error.code==='ESRCH')return false;throw error;}};
export async function refused(origin){const url=new URL(origin);return new Promise((resolve,reject)=>{const socket=connect({host:'127.0.0.1',port:Number(url.port)});let result;const timer=setTimeout(()=>{result=false;socket.destroy();},500);socket.once('connect',()=>{result=false;socket.destroy();});socket.once('error',error=>{result=error.code==='ECONNREFUSED';});socket.once('close',()=>{clearTimeout(timer);resolve(result===true);});});}
export {assertLambdaReports,assertTaskDeadline,validateLambdaProof} from './retained-routes-lambda-proof.mjs';
import {assertLambdaReports} from './retained-routes-lambda-proof.mjs';
export async function startRetainedServer({signal,app=process.env.NEXTGO_RETAINED_LAMBDA_APP||'retained',mode=process.env.NEXTGO_RETAINED_LAMBDA_MODE,binary=process.env.NEXTGO_RETAINED_LAMBDA_BINARY,expected=process.env.NEXTGO_RETAINED_LAMBDA_SHA256,permittedAssetOverflows=[]}={}){
  if(process.platform!=='linux'||typeof process.getuid!=='function')throw Error('Retained Lambda qualification requires actual native Linux identity');
  if(!['buffered','streaming'].includes(mode)||!isAbsolute(binary||'')||!/^[a-f0-9]{64}$/.test(expected||''))throw Error('Explicit mode and actual hash-pinned extracted CLI bootstrap required');
  if(!['retained','merge'].includes(app)||!Array.isArray(permittedAssetOverflows)||permittedAssetOverflows.length>16||new Set(permittedAssetOverflows).size!==permittedAssetOverflows.length||!permittedAssetOverflows.every(path=>typeof path==='string'&&/^\/_nextgo\/[a-f0-9]{20}\/(?:app|module-[a-f0-9]{64})\.wasm$/.test(path)))throw Error('Known app and bounded exact own-ZIP overflow paths required');
  const allowedOverflows=new Set(permittedAssetOverflows);
  const pinned=await artifactHash(binary);if(pinned.sha256!==expected)throw Error('Extracted CLI bootstrap hash mismatch');
  const startup=signal?AbortSignal.any([signal,AbortSignal.timeout(20_000)]):AbortSignal.timeout(20_000);startup.throwIfAborted();
  const cwd=await mkdtemp(join(tmpdir(),'nextgo-retained-example-')),directory=await lstat(cwd);
  const proof={version:1,app,binary,sha256:expected,bytes:pinned.bytes,cwd,cwdRemoved:false,pid:null,closed:false,joined:false,shutdownRequested:false,termSent:false,forced:false,code:null,signal:null,unexpectedExit:false,groupEmpty:null,listenerRefused:false,listenerKind:'LOCAL mock public gateway; the RuntimeAPI native worker has no HTTP listener',platform:'linux',requireLinuxIdentity:true,nativeIdentityVersion:1,nativeOwnerUID:process.getuid(),nativeIdentity:null,identityChecks:[],identityRefused:false,cleanupDeferred:false,identityFailure:'',artifact:{path:binary,dev:pinned.dev,inode:pinned.inode,parent:pinned.parent}};
  let child,captured,expectedIdentity,gateway,startupGatewayProof,closePromise,expectedErrors=[],spawnError,closedResolve;
  const closed=new Promise(resolve=>{closedResolve=resolve;});const log=[];let used=0;
  const output=bytes=>{const part=bytes.subarray(0,Math.max(0,(128<<10)-used));log.push(part);used+=part.length;};
  function refuse(error){proof.identityRefused=true;proof.cleanupDeferred=true;proof.identityFailure=String(error.code||error.message).slice(0,160);child?.unref();child?.stdout?.unref?.();child?.stderr?.unref?.();}
  async function requestSignal(signal){
    if(proof.identityRefused)throw Error('Native identity authority permanently refused; outer controller required');
    proof.shutdownRequested=true;
    try{const sent=await checkedChildSignal(child,captured,expectedIdentity,signal,proof.identityChecks);if(signal==='SIGTERM')proof.termSent=sent;if(signal==='SIGKILL')proof.forced=sent;return sent;}catch(error){if(error.nativeIdentityRefusal)refuse(error);throw error;}
  }
  async function finalize(){
    if(child){if(!proof.closed)throw Error('Cannot remove native cwd before physical close');proof.groupEmpty=!groupPresent(child.pid);if(!proof.groupEmpty)throw Error('Native process group remains');}
    else proof.groupEmpty=true;
    const ending=await artifactHash(binary);if(ending.sha256!==expected||ending.dev!==pinned.dev||ending.inode!==pinned.inode||ending.parent.dev!==pinned.parent.dev||ending.parent.inode!==pinned.parent.inode)throw Error('Actual extracted bootstrap changed');
    const info=await lstat(cwd);if(!info.isDirectory()||info.isSymbolicLink()||info.dev!==directory.dev||info.ino!==directory.ino)throw Error('Owned native cwd identity changed');await rm(cwd,{recursive:true,force:false});
    try{await lstat(cwd);throw Error('Owned native cwd remains');}catch(error){if(error.code!=='ENOENT')throw error;proof.cwdRemoved=true;}
  }
  const factory=async({binary:actual,args,env})=>{
    if(actual!==binary||args.length!==0)throw Error('Unexpected strict factory artifact/argv');
    child=spawn(binary,['-env-dir',cwd],{cwd,detached:true,shell:false,stdio:['ignore','pipe','pipe'],env});proof.pid=child.pid??null;
    child.stdout.on('data',output);child.stderr.on('data',output);child.once('error',error=>{spawnError=error;});
    child.once('close',(code,signal)=>{proof.closed=true;proof.joined=true;proof.code=code;proof.signal=signal;proof.unexpectedExit=!proof.shutdownRequested;closedResolve({code,signal,error:spawnError?.message});});
    expectedIdentity={pid:child.pid,uid:process.getuid(),executable:{path:binary,dev:pinned.dev,inode:pinned.inode}};
    try{captured=await checkNativeChild(child,null,expectedIdentity,'capture',proof.identityChecks);if(!captured)throw Error('Native worker closed before identity capture');proof.nativeIdentity=captured;}
    catch(error){if(error.nativeIdentityRefusal)refuse(error);throw error;}
    return {child,requestSignal,closed,snapshot:()=>structuredClone(proof)};
  };
  const fixture={origin:retainedLambdaOrigin,expectTaskDeadline(){if(app!=='retained'||expectedErrors.length)throw Error('Error expectation already armed');expectedErrors.push({path:'/api/task',message:'context deadline exceeded',type:'NextGoInvocationError',class:'deadline'});},expectAssetOverflow(path){if(mode!=='buffered'||!allowedOverflows.has(path)||expectedErrors.length>=16||expectedErrors.some(value=>value.path===path))throw Error('Exact own-ZIP buffered WASM/module overflow expectation required');expectedErrors.push({path,message:'HTTP response exceeds buffered Lambda payload limit',type:'NextGoInvocationError',class:'buffered-overflow'});},snapshot(){return {...structuredClone(proof),logs:Buffer.concat(log,used).toString('utf8'),transport:'LOCAL mock Lambda '+mode,expectedInvocationErrors:structuredClone(expectedErrors),lambda:gateway?.snapshot()??startupGatewayProof??null};},async waitFor(predicate,{timeout=20_000}={}){const end=Date.now()+timeout;for(;;){signal?.throwIfAborted();const value=gateway.snapshot();if(predicate(value))return value;if(Date.now()>=end)throw Error('Bounded Lambda observation wait expired');await pause(20);}},close(){
    if(closePromise)return closePromise;closePromise=(async()=>{
      const errors=[];if(gateway){const until=Date.now()+2000;while(gateway.snapshot().active!==null||gateway.snapshot().queued!==0){if(Date.now()>=until){errors.push(Error('Existing LOCAL invocations did not drain before shutdown'));break;}await pause(20);}proof.invocationDrainObserved=gateway.snapshot().active===null&&gateway.snapshot().queued===0;try{await gateway.close();}catch(error){errors.push(error);}}
      // Factory rejection may precede returning a worker handle to the gateway.
      if(child&&!proof.closed&&!proof.identityRefused){try{await requestSignal('SIGTERM');await bounded(closed,12_000,'Native factory failure cleanup did not close');}catch(error){errors.push(error);}}
      if(proof.identityRefused)errors.push(Error('Native identity refused; no unknown PID signal or successful cleanup claim'));
      if(!child||proof.closed){try{await finalize();}catch(error){errors.push(error);}}
      if(gateway){const snapshot=gateway.snapshot();try{assertLambdaReports(snapshot,{expectedErrors});if(!snapshot.joined||!snapshot.listenerClosed?.runtime||!snapshot.listenerClosed?.public||snapshot.sockets?.runtime!==0||snapshot.sockets?.public!==0)throw Error('LOCAL gateway/runtime listeners or sockets not physically closed');proof.listenerRefused=await refused(gateway.origin)&&await refused(snapshot.runtimeOrigin);if(!proof.listenerRefused)throw Error('LOCAL gateway/runtime listener remains');}catch(error){errors.push(error);}}
      if(child&&(!proof.closed||proof.code!==0||proof.signal!==null||proof.forced||proof.unexpectedExit||!proof.termSent||!proof.groupEmpty))errors.push(Error('Actual bootstrap did not close gracefully with exact ownership'));
      if(errors.length)throw new AggregateError(errors,'Retained Lambda fixture cleanup/protocol failed');
    })();return closePromise;
  }};
  try{
    gateway=await startLambdaGateway({binary,mode,port:4188,policy:'retained-strict-v1',trustedEnvironment:{GOMAXPROCS:'1',GOMEMLIMIT:'384MiB',LANG:'C.UTF-8',TMPDIR:tmpdir()},workerFactory:factory});
    for(;;){startup.throwIfAborted();if(spawnError||proof.closed)throw spawnError||Error('Bootstrap closed before readiness');
      let healthy=false;try{const response=await fetch(fixture.origin+'/api/health',{signal:AbortSignal.any([startup,AbortSignal.timeout(500)])});const reader=response.body.getReader(),parts=[];let bytes=0;try{for(;;){const value=await reader.read();if(value.done)break;bytes+=value.value.byteLength;if(bytes>4096)throw Error('Health body bound');parts.push(Buffer.from(value.value));}}finally{await reader.cancel().catch(()=>{});reader.releaseLock();}const health=JSON.parse(Buffer.concat(parts,bytes).toString('utf8'));healthy=response.ok&&health.ok===true&&health.pid===child.pid;}catch(error){if(startup.aborted)throw error;}
      if(healthy){if(!await checkNativeChild(child,captured,expectedIdentity,'readiness',proof.identityChecks))throw Error('Bootstrap closed during readiness identity check');return fixture;}await pause(50);
    }
  }catch(error){startupGatewayProof=error.gatewaySnapshot??null;if(error.nativeIdentityRefusal)refuse(error);try{await fixture.close();}catch(cleanup){error=new AggregateError([error,cleanup],'Lambda startup and cleanup failed');}error.retainedProof=fixture.snapshot();throw error;}
}
export const test=base.extend({native:async({page},use,info)=>{
  let native;try{try{native=await startRetainedServer();}catch(error){if(error.retainedProof)await info.attach((process.env.NEXTGO_RETAINED_LAMBDA_APP||'retained')+'-startup-proof',{body:JSON.stringify(error.retainedProof),contentType:'application/json'});throw error;}await use(native);}finally{const errors=[];try{await page.close();}catch(error){errors.push(error);}if(native){try{await native.close();}catch(error){errors.push(error);}const proof=native.snapshot();await info.attach(proof.app+'-native-process-proof',{body:JSON.stringify(proof),contentType:'application/json'});expect(proof.closed&&proof.joined&&proof.termSent&&proof.cwdRemoved&&proof.listenerRefused&&proof.groupEmpty).toBe(true);expect(proof.forced||proof.unexpectedExit).toBe(false);expect(proof.code).toBe(0);}if(errors.length)throw new AggregateError(errors,'Lambda counterpart cleanup failed');}
}});
export {expect};
