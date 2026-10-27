// Runs only an explicitly supplied, hash-pinned real production executable.
// It never builds or constructs page, navigation or application API responses.
import {spawn} from 'node:child_process';
import {constants} from 'node:fs';
import {open, lstat, mkdtemp, realpath, rm} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {dirname, isAbsolute, join, resolve} from 'node:path';
import {tmpdir} from 'node:os';
import {checkNativeChild,checkedChildSignal} from './retained-routes-native-identity.mjs';

export const retainedOrigin = 'http://127.0.0.1:4188';
const pause = (ms, signal) => new Promise((resolve, reject) => {
  const stopped = () => {clearTimeout(timer); reject(signal.reason);};
  const timer = setTimeout(() => {signal.removeEventListener('abort', stopped); resolve();}, ms);
  if (signal.aborted) stopped(); else signal.addEventListener('abort', stopped, {once:true});
});
const groupPresent = pid => {try {process.kill(-pid,0); return true;} catch(error) {if(error.code==='ESRCH')return false; throw error;}};
async function artifactHash(path) {
  const parent=dirname(path);
  if(!isAbsolute(path)||resolve(path)!==path||await realpath(parent)!==parent)throw Error('Retained producer parent must be canonical');
  const directory=await lstat(parent,{bigint:true}),owner=typeof process.getuid==='function'?process.getuid():null;
  if(!directory.isDirectory()||directory.isSymbolicLink()||(directory.mode&0o022n)!==0n||owner!==null&&directory.uid!==BigInt(owner))throw Error('Retained producer parent is not cooperatively owned');
  const file=await open(path,constants.O_RDONLY|constants.O_NOFOLLOW|constants.O_NONBLOCK);
  try{
    const before=await file.stat({bigint:true});
    if(!before.isFile()||before.nlink!==1n||!(before.mode&0o111n)||(before.mode&0o022n)!==0n||before.size<1n||before.size>128n<<20n||owner!==null&&before.uid!==BigInt(owner))throw Error('Invalid retained example executable');
    const digest=createHash('sha256'),bytes=Buffer.alloc(64<<10);let count=0;
    for(;;){const part=await file.read(bytes,0,bytes.length,null);if(!part.bytesRead)break;count+=part.bytesRead;if(count>128<<20)throw Error('Executable size changed');digest.update(bytes.subarray(0,part.bytesRead));}
    const after=await file.stat({bigint:true}),afterDirectory=await lstat(parent,{bigint:true});
    if(BigInt(count)!==before.size||after.size!==before.size||after.dev!==before.dev||after.ino!==before.ino||after.mtimeNs!==before.mtimeNs||after.mode!==before.mode||after.uid!==before.uid||after.nlink!==before.nlink||afterDirectory.dev!==directory.dev||afterDirectory.ino!==directory.ino||afterDirectory.uid!==directory.uid||afterDirectory.mode!==directory.mode||await realpath(parent)!==parent)throw Error('Executable or producer parent changed while hashing');
    return {sha256:digest.digest('hex'),bytes:count,dev:before.dev.toString(),inode:before.ino.toString(),parent:{path:parent,dev:directory.dev.toString(),inode:directory.ino.toString()}};
  }finally{await file.close();}
}

export async function startRetainedServer({signal}={}) {
  const identityMode=process.env.NEXTGO_RETAINED_REQUIRE_LINUX_IDENTITY;
  if(identityMode!==undefined&&identityMode!=='0'&&identityMode!=='1')throw Error('Invalid retained Linux identity requirement');
  const requireLinuxIdentity=identityMode==='1';
  if(requireLinuxIdentity&&process.platform!=='linux')throw Error('Retained strict native identity requires Linux before launch');
  if(process.platform==='linux'&&typeof process.getuid!=='function')throw Error('Linux native owner UID is unavailable');
  const binary=process.env.NEXTGO_RETAINED_SERVER_BINARY, expected=process.env.NEXTGO_RETAINED_SERVER_SHA256;
  if(!isAbsolute(binary||'') || Buffer.byteLength(binary)>4096 || !/^[a-f0-9]{64}$/.test(expected||'')) throw Error('An absolute prebuilt retained server and SHA256 are required; no build fallback exists');
  const pinned=await artifactHash(binary);
  if(pinned.sha256!==expected)throw Error('Retained example executable SHA256 mismatch');
  const startup=signal?AbortSignal.any([signal,AbortSignal.timeout(20_000)]):AbortSignal.timeout(20_000);
  startup.throwIfAborted();
  const cwd=await mkdtemp(join(tmpdir(),'nextgo-retained-example-')), identity=await lstat(cwd);
  const proof={version:1,binary,sha256:expected,bytes:pinned.bytes,cwd,cwdRemoved:false,pid:null,closed:false,joined:false,shutdownRequested:false,termSent:false,forced:false,code:null,signal:null,unexpectedExit:false,groupEmpty:null,listenerRefused:false,platform:process.platform,requireLinuxIdentity,nativeIdentityVersion:1,nativeOwnerUID:process.platform==='linux'?process.getuid():null,nativeIdentity:null,identityChecks:[],identityRefused:false,cleanupDeferred:false,identityFailure:'',artifact:{path:binary,dev:pinned.dev,inode:pinned.inode,parent:pinned.parent}};
  const logs=[];let logBytes=0,closePromise,child,spawnError;let resolveClose;
  const childClosed=new Promise(resolve=>{resolveClose=resolve;});
  const environment=Object.fromEntries(Object.entries(process.env).filter(([name])=>!name.startsWith('AWS_LAMBDA_')&&!name.startsWith('NEXTGO_LAMBDA_')&&!name.startsWith('NEXTGO_DEMO_')&&name!=='NEXTGO_ENV_DIR'));
  child=spawn(binary,['-addr','127.0.0.1:4188','-env-dir',cwd],{cwd,detached:process.platform!=='win32',stdio:['ignore','pipe','pipe'],env:{...environment,GOMAXPROCS:'1'}});
  proof.pid=child.pid??null;
  let capturedIdentity=null;
  const expectedIdentity=process.platform==='linux'?{pid:child.pid,uid:process.getuid(),executable:{path:binary,dev:pinned.dev,inode:pinned.inode}}:null;
  const output=bytes=>{const remaining=(128<<10)-logBytes;if(remaining>0){const part=bytes.subarray(0,remaining);logs.push(part);logBytes+=part.length;}};
  child.stdout.on('data',output);child.stderr.on('data',output);
  child.once('error',error=>{spawnError=error;});
  child.once('close',(code,signal)=>{proof.closed=true;proof.joined=true;proof.code=code;proof.signal=signal;proof.unexpectedExit=!proof.shutdownRequested;resolveClose();});
  const deferIdentityCleanup=error=>{
    proof.identityRefused=true;proof.cleanupDeferred=true;proof.identityFailure=String(error.code||error.message).slice(0,160);
    // Do not regain authority after a failed check. Preserve cwd/receipts for
    // the outer controller; unref without signalling or closing child pipes.
    child.unref();child.stdout.unref?.();child.stderr.unref?.();
  };
  const fixture={origin:retainedOrigin,snapshot(){return {...structuredClone(proof),logs:Buffer.concat(logs,logBytes).toString('utf8')};},close(){
    if(closePromise)return closePromise;
    closePromise=(async()=>{
      proof.shutdownRequested=true;
      if(!proof.closed){
        if(proof.identityRefused){const refusal=Error('Native identity authority lost; outer controller required');refusal.nativeIdentityRefusal=true;throw refusal;}
        proof.termSent=await checkedChildSignal(child,capturedIdentity,expectedIdentity,'SIGTERM',proof.identityChecks);
        let timer;
        const graceful=await Promise.race([childClosed.then(()=>true),new Promise(resolve=>{timer=setTimeout(()=>resolve(false),12_000);})]).finally(()=>clearTimeout(timer));
        if(!graceful){proof.forced=await checkedChildSignal(child,capturedIdentity,expectedIdentity,'SIGKILL',proof.identityChecks);
          await Promise.race([childClosed,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('Retained native close did not join')),3000);})]).finally(()=>clearTimeout(timer));}
      }
      proof.groupEmpty=process.platform==='win32'?null:!Number.isSafeInteger(child.pid)||child.pid<1||!groupPresent(child.pid);
      const controller=new AbortController();const timeout=setTimeout(()=>controller.abort(),500);
      try {const response=await fetch(retainedOrigin+'/api/health',{signal:controller.signal});await response.body?.cancel();}
      catch(error){proof.listenerRefused=error?.cause?.code==='ECONNREFUSED';}
      finally{clearTimeout(timeout);}
      const ending=await artifactHash(binary);
      if(ending.sha256!==expected||ending.dev!==pinned.dev||ending.inode!==pinned.inode||ending.parent.dev!==pinned.parent.dev||ending.parent.inode!==pinned.parent.inode)throw Error('Retained executable changed during browser case');
      const present=await lstat(cwd);
      if(!present.isDirectory()||present.isSymbolicLink()||present.dev!==identity.dev||present.ino!==identity.ino)throw Error('Owned native directory identity changed');
      await rm(cwd,{recursive:true,force:false});
      try{await lstat(cwd);throw Error('Owned native directory remains');}catch(error){if(error.code!=='ENOENT')throw error;proof.cwdRemoved=true;}
    })().catch(error=>{
      if(error.nativeIdentityRefusal)deferIdentityCleanup(error);
      throw error;
    });return closePromise;
  }};
  try {
    if(process.platform==='linux'){
      capturedIdentity=await checkNativeChild(child,null,expectedIdentity,'capture',proof.identityChecks);
      if(!capturedIdentity)throw Error('Native child exited before identity capture');
      proof.nativeIdentity=capturedIdentity;
    }
    for(;;){startup.throwIfAborted();if(spawnError||proof.closed)throw spawnError||Error('Retained native server exited before readiness');
      let healthy=false;
      try{const response=await fetch(retainedOrigin+'/api/health',{signal:AbortSignal.any([startup,AbortSignal.timeout(250)])});
        const reader=response.body?.getReader();let count=0;const parts=[];
        if(reader){try{for(;;){const part=await reader.read();if(part.done)break;count+=part.value.byteLength;if(count>4096)throw Error('Health body bound exceeded');parts.push(Buffer.from(part.value));}}finally{await reader.cancel().catch(()=>{});reader.releaseLock();}}
        const health=JSON.parse(Buffer.concat(parts,count).toString('utf8'));
        healthy=response.ok&&health.ok===true&&health.pid===child.pid&&!proof.closed;
      }catch(error){if(startup.aborted)throw error;}
      if(healthy){
        if(process.platform==='linux'&&!await checkNativeChild(child,capturedIdentity,expectedIdentity,'readiness',proof.identityChecks))throw Error('Native child exited during readiness identity check');
        return fixture;
      }
      await pause(50,startup);
    }
  }catch(error){if(error.nativeIdentityRefusal)deferIdentityCleanup(error);try{await fixture.close();}catch(cleanup){error=new AggregateError([error,cleanup],'Retained fixture startup and cleanup failed');}error.retainedProof=fixture.snapshot();throw error;}
}
