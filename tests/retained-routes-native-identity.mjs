// Linux evidence for one cooperative, detached native child. This is not a
// pidfd: a /proc check followed by child.kill is not an atomic ownership claim.
import {constants} from 'node:fs';
import {open, readlink, stat} from 'node:fs/promises';
import {posix} from 'node:path';

export const nativeIdentityLimits=Object.freeze({statBytes:8192,statusBytes:16384,statusLines:128,lineBytes:1024,pathBytes:4096,statFields:64,receipts:8});
const uidNames=['real','effective','saved','filesystem'];
const u64=18446744073709551615n,i64min=-9223372036854775808n;
const fail=message=>{const error=Error('Retained native identity: '+message);error.nativeIdentityRefusal=true;throw error;};
function text(raw,cap){
  if(typeof raw!=='string'||Buffer.byteLength(raw)>cap||raw.includes('\0')||Buffer.from(raw).toString('utf8')!==raw)fail('invalid text or read bound');
  return raw;
}
function decimal(raw,{max=u64,min=0n}={}){
  if(typeof raw!=='string'||!(/^(0|[1-9][0-9]{0,19})$/).test(raw))fail('invalid decimal field');
  const value=BigInt(raw);if(value<min||value>max)fail('decimal field bound');return raw;
}
function pid(raw){return Number(decimal(raw,{min:1n,max:2147483647n}));}
function uid(raw){return Number(decimal(raw,{max:4294967295n}));}
function executable(value){
  if(!value||typeof value!=='object')fail('missing executable');
  const path=text(value.path,nativeIdentityLimits.pathBytes);
  if(!posix.isAbsolute(path)||posix.normalize(path)!==path||path.endsWith(' (deleted)'))fail('noncanonical executable path');
  return Object.freeze({path,dev:decimal(value.dev),inode:decimal(value.inode,{min:1n})});
}

export function parseProcStat(raw){
  raw=text(raw,nativeIdentityLimits.statBytes);
  if(raw.endsWith('\n'))raw=raw.slice(0,-1);
  const prefix=/^([1-9][0-9]{0,9}) \(/.exec(raw),end=raw.lastIndexOf(') ');
  if(!prefix||end<prefix[0].length-1)fail('invalid stat comm boundary');
  const fields=raw.slice(end+2).split(' ');
  // Field3 is state; fields4..52 are bounded numeric kernel fields. Later
  // additions are accepted only up to the fixed parser-field ceiling.
  if(fields.length<50||fields.length>nativeIdentityLimits.statFields||!/^[A-Za-z]$/.test(fields[0]))fail('invalid stat fields');
  for(const field of fields.slice(1)){
    if(!/^-?(0|[1-9][0-9]{0,19})$/.test(field))fail('invalid stat number');
    const number=BigInt(field);if(number<i64min||number>u64)fail('stat numeric bound');
  }
  return Object.freeze({pid:pid(prefix[1]),pgid:pid(fields[2]),session:pid(fields[3]),startTicks:decimal(fields[19],{min:1n})});
}

export function parseProcStatus(raw){
  raw=text(raw,nativeIdentityLimits.statusBytes);
  if(raw.endsWith('\n'))raw=raw.slice(0,-1);
  const lines=raw.split('\n');if(lines.length>nativeIdentityLimits.statusLines)fail('status line count');
  let processPID,uids;
  for(const line of lines){
    if(Buffer.byteLength(line)>nativeIdentityLimits.lineBytes||line.includes('\r'))fail('status line bound');
    if(line.startsWith('Pid:')){
      const match=/^Pid:[ \t]+([0-9]+)$/.exec(line);
      if(processPID!==undefined||!match)fail('invalid or duplicate status PID');processPID=pid(match[1]);
    }else if(line.startsWith('Uid:')){
      const match=/^Uid:[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)[ \t]+([0-9]+)$/.exec(line);
      if(uids||!match)fail('invalid or duplicate four UIDs');
      uids=Object.freeze(Object.fromEntries(uidNames.map((name,index)=>[name,uid(match[index+1])])));
    }
  }
  if(processPID===undefined||!uids)fail('missing status PID or four UIDs');
  return Object.freeze({pid:processPID,uids});
}

export function validateNativeIdentity(value,expected,initial){
  if(!value||value.platform!=='linux'||!expected||!Number.isSafeInteger(expected.pid)||expected.pid<1||expected.pid>2147483647||!Number.isSafeInteger(expected.uid)||expected.uid<0||expected.uid>4294967295)fail('invalid identity contract');
  if(value.pid!==expected.pid||value.pgid!==expected.pid||value.session!==expected.pid)fail('PID PGID or session mismatch');
  if(!value.uids||uidNames.some(name=>!Number.isSafeInteger(value.uids[name])||value.uids[name]!==expected.uid))fail('UID mismatch');
  const startTicks=decimal(value.startTicks,{min:1n}),actualExe=executable(value.executable),wantedExe=executable(expected.executable);
  if(actualExe.path!==wantedExe.path||actualExe.dev!==wantedExe.dev||actualExe.inode!==wantedExe.inode)fail('executable mismatch');
  const identity=Object.freeze({platform:'linux',pid:value.pid,uids:Object.freeze(Object.fromEntries(uidNames.map(name=>[name,value.uids[name]]))),pgid:value.pgid,session:value.session,startTicks,executable:actualExe});
  if(initial){
    const prior=validateNativeIdentity(initial,expected);
    if(prior.startTicks!==startTicks||uidNames.some(name=>prior.uids[name]!==identity.uids[name]))fail('process lifetime changed');
  }
  return identity;
}

// Pure assembly seam: PID/start identity brackets both UID snapshots and the
// executable observation. It does not claim the observation is an atomic pidfd.
export function validateStableNativeSnapshot({before,firstStatus,executable:exe,lastStatus,after},expected,initial){
  if(!before||!after||!firstStatus||!lastStatus||firstStatus.pid!==before.pid||lastStatus.pid!==after.pid)fail('status PID mismatch');
  const first=validateNativeIdentity({platform:'linux',...before,uids:firstStatus.uids,executable:exe},expected,initial);
  const last=validateNativeIdentity({platform:'linux',...after,uids:lastStatus.uids,executable:exe},expected,initial);
  if(first.pid!==last.pid||first.pgid!==last.pgid||first.session!==last.session||first.startTicks!==last.startTicks||uidNames.some(name=>first.uids[name]!==last.uids[name]))fail('unstable proc snapshot');
  return last;
}

async function boundedProc(path,cap){
  const file=await open(path,constants.O_RDONLY|constants.O_NOFOLLOW|constants.O_NONBLOCK);
  try{
    const bytes=Buffer.alloc(cap+1);let length=0;
    while(length<bytes.length){const part=await file.read(bytes,length,bytes.length-length,null);if(!part.bytesRead)break;length+=part.bytesRead;}
    if(length>cap)fail('proc read bound');
    return new TextDecoder('utf-8',{fatal:true}).decode(bytes.subarray(0,length));
  }finally{await file.close();}
}

export async function readNativeIdentity(processPID,expected,initial){
  if(process.platform!=='linux'||!Number.isSafeInteger(processPID)||processPID<1||processPID>2147483647)fail('Linux PID required');
  const root='/proc/'+processPID;
  const before=parseProcStat(await boundedProc(root+'/stat',nativeIdentityLimits.statBytes));
  const firstStatus=parseProcStatus(await boundedProc(root+'/status',nativeIdentityLimits.statusBytes));
  const path=text(await readlink(root+'/exe'),nativeIdentityLimits.pathBytes);
  const file=await stat(root+'/exe',{bigint:true});if(!file.isFile())fail('executable is not regular');
  if(text(await readlink(root+'/exe'),nativeIdentityLimits.pathBytes)!==path)fail('executable link changed');
  const lastStatus=parseProcStatus(await boundedProc(root+'/status',nativeIdentityLimits.statusBytes));
  const after=parseProcStat(await boundedProc(root+'/stat',nativeIdentityLimits.statBytes));
  return validateStableNativeSnapshot({before,firstStatus,executable:{path,dev:file.dev.toString(),inode:file.ino.toString()},lastStatus,after},expected,initial);
}

function live(child){return Number.isSafeInteger(child.pid)&&child.pid>0&&child.exitCode===null&&child.signalCode===null;}
function reserve(receipts){if(!Array.isArray(receipts)||receipts.length>=nativeIdentityLimits.receipts)fail('identity receipt bound');}
function errorText(error){return String(error?.code||error?.message||'identity check failed').slice(0,160);}
export async function checkNativeChild(child,initial,expected,phase,receipts){
  if(phase!=='capture'&&phase!=='readiness')fail('invalid identity phase');
  reserve(receipts);
  if(!live(child)){receipts.push({phase,outcome:'already-exited',identity:null});return null;}
  try{
    const identity=await readNativeIdentity(child.pid,expected,initial);
    if(!live(child)){receipts.push({phase,outcome:'already-exited',identity:null});return null;}
    receipts.push({phase,outcome:'verified',identity});return identity;
  }catch(error){receipts.push({phase,outcome:'refused',identity:null,error:errorText(error)});error.nativeIdentityRefusal=true;throw error;}
}

export async function checkedChildSignal(child,initial,expected,signal,receipts){
  if(signal!=='SIGTERM'&&signal!=='SIGKILL')fail('unsupported signal');
  reserve(receipts);
  if(receipts.some(receipt=>receipt.outcome==='refused')){
    receipts.push({phase:'before-signal',signal,outcome:'refused',identity:null,error:'previous identity refusal'});
    fail('previous identity refusal; outer controller required');
  }
  if(!live(child)){receipts.push({phase:'before-signal',signal,outcome:'already-exited',identity:null});return false;}
  let identity=null;
  try{
    if(process.platform==='linux'){
      if(!initial)fail('no captured process identity');
      identity=await readNativeIdentity(child.pid,expected,initial);
    }
    if(!live(child)){receipts.push({phase:'before-signal',signal,outcome:'already-exited',identity:null});return false;}
    // There is no await between the final handle-live check and this call.
    // Group absence is never used as authority to signal a numeric PGID.
    const sent=child.kill(signal);
    receipts.push({phase:'before-signal',signal,outcome:sent?'signal-sent':'signal-not-sent',identity});return sent;
  }catch(error){receipts.push({phase:'before-signal',signal,outcome:'refused',identity:null,error:errorText(error)});error.nativeIdentityRefusal=true;throw error;}
}
