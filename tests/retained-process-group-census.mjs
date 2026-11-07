// Failure diagnostics only. A /proc scan is not an atomic process identity,
// absence proof, signal capability, cleanup receipt, or qualification result.
import {constants} from 'node:fs';
import {open,opendir} from 'node:fs/promises';
import {performance} from 'node:perf_hooks';

export const groupCensusLimits=Object.freeze({entries:4096,pids:1024,statBytes:8192,reservedReadBytes:8<<20,members:12,reasons:8,elapsedMS:500,observationBytes:4096,commandProofBytes:64<<10,proofReserveBytes:4096});
const reasonNames=new Set(['UNSUPPORTED_PLATFORM','INVALID_GROUP','INVALID_CLOCK','DEADLINE','ENTRY_LIMIT','PID_LIMIT','READ_LIMIT','MEMBER_LIMIT','DIRECTORY_READ','STAT_MISSING','STAT_DENIED','STAT_READ','STAT_INVALID','STAT_CHANGED','OBSERVER_FAILURE','OBSERVATION_BUDGET','PROOF_BUDGET']);
const numeric=/^(0|[1-9][0-9]{0,19})$/,maxU64=18446744073709551615n,minI64=-9223372036854775808n;
const validPID=value=>Number.isSafeInteger(value)&&value>0&&value<=2147483647;
const textBytes=value=>Buffer.byteLength(value,'utf8');
function failure(code){const error=Error(code);error.censusReason=code;return error;}

// Parse every numeric kernel stat field, while discarding the arbitrary comm
// field. Zero PPID/PGID/session/start is valid for kernel tasks in this census.
export function parseGroupStat(raw){
  if(typeof raw!=='string'||textBytes(raw)>groupCensusLimits.statBytes||raw.includes('\0')||Buffer.from(raw).toString('utf8')!==raw)throw failure('STAT_INVALID');
  if(raw.endsWith('\n'))raw=raw.slice(0,-1);
  const prefix=/^([1-9][0-9]{0,9}) \(/.exec(raw),end=raw.lastIndexOf(') ');
  if(!prefix||end<prefix[0].length-1)throw failure('STAT_INVALID');
  const fields=raw.slice(end+2).split(' ');
  if(fields.length<50||fields.length>64||!/^[A-Za-z]$/.test(fields[0]))throw failure('STAT_INVALID');
  for(const field of fields.slice(1)){
    if(!/^-?(0|[1-9][0-9]{0,19})$/.test(field))throw failure('STAT_INVALID');
    const number=BigInt(field);if(number<minI64||number>maxU64)throw failure('STAT_INVALID');
  }
  const scalar=value=>{if(!numeric.test(value))throw failure('STAT_INVALID');const number=BigInt(value);if(number>2147483647n)throw failure('STAT_INVALID');return Number(number);};
  const pid=scalar(prefix[1]),ppid=scalar(fields[1]),pgid=scalar(fields[2]),session=scalar(fields[3]);
  if(!validPID(pid)||!numeric.test(fields[19])||BigInt(fields[19])>maxU64)throw failure('STAT_INVALID');
  return {pid,ppid,state:fields[0],pgid,session,startTicks:fields[19]};
}
function errorReason(error){
  if(error?.censusReason&&reasonNames.has(error.censusReason))return error.censusReason;
  if(error?.code==='ENOENT'||error?.code==='ESRCH')return 'STAT_MISSING';
  if(error?.code==='EACCES'||error?.code==='EPERM')return 'STAT_DENIED';
  return 'STAT_READ';
}
function incomplete(group,reason){return {version:1,status:'INCOMPLETE',atomic:false,signalAuthority:false,absenceProof:false,group:validPID(group)?group:0,reasons:[reason],members:[]};}

// The injected IO is a pure test seam. Production uses the fixed /proc adapter
// below, never a caller-selected filesystem path or command. Time bounds stop
// admission after 500ms; they do not claim preemption of an in-flight kernel IO.
export async function collectGroupCensus(group,io){
  if(!validPID(group))return incomplete(group,'INVALID_GROUP');
  if(io?.platform!=='linux')return incomplete(group,'UNSUPPORTED_PLATFORM');
  const result={version:1,status:'COMPLETE_SCAN_OBSERVATION',atomic:false,signalAuthority:false,absenceProof:false,group,entriesRead:0,pidsRead:0,statReads:0,reservedReadBytes:0,successfulStatBytes:0,elapsedMS:0,reasons:[],members:[]};
  const add=reason=>{result.status='INCOMPLETE';if(!result.reasons.includes(reason)&&result.reasons.length<groupCensusLimits.reasons)result.reasons.push(reason);};
  let started,last;
  try{started=last=io.now();}catch{return incomplete(group,'INVALID_CLOCK');}
  if(!Number.isFinite(started))return incomplete(group,'INVALID_CLOCK');
  const within=()=>{
    let now;try{now=io.now();}catch{add('INVALID_CLOCK');return false;}
    if(!Number.isFinite(now)||now<last){add('INVALID_CLOCK');return false;}
    last=now;result.elapsedMS=Math.min(60000,Math.ceil(now-started));
    if(now-started>=groupCensusLimits.elapsedMS){add('DEADLINE');return false;}return true;
  };
  const read=async pid=>{
    if(!within())return null;
    // Reserve the full per-read cap plus one byte for truncation detection,
    // including failed reads. Aggregate IO cannot outrun this reservation.
    const reserve=groupCensusLimits.statBytes+1;
    if(result.reservedReadBytes+reserve>groupCensusLimits.reservedReadBytes){add('READ_LIMIT');return null;}
    result.reservedReadBytes+=reserve;result.statReads++;
    try{
      const raw=await io.readStat(pid,groupCensusLimits.statBytes);
      if(!within())return null;
      if(typeof raw!=='string')throw failure('STAT_INVALID');
      const bytes=textBytes(raw);if(bytes>groupCensusLimits.statBytes)throw failure('STAT_INVALID');
      result.successfulStatBytes+=bytes;
      const value=parseGroupStat(raw);if(value.pid!==pid)throw failure('STAT_CHANGED');return value;
    }catch(error){add(errorReason(error));return null;}
  };
  const seen=new Set();
  try{
    for await(const entry of io.entries()){
      if(!within())break;
      result.entriesRead++;
      if(typeof entry!=='string'||entry.length>255){add('DIRECTORY_READ');break;}
      if(/^[1-9][0-9]{0,9}$/.test(entry)){
        const pid=Number(entry);
        if(!validPID(pid)||seen.has(pid)){add('DIRECTORY_READ');break;}
        if(result.pidsRead>=groupCensusLimits.pids){add('PID_LIMIT');break;}
        seen.add(pid);result.pidsRead++;
        const before=await read(pid);
        if(before?.pgid===group){
          if(result.members.length>=groupCensusLimits.members){add('MEMBER_LIMIT');break;}
          const after=await read(pid);
          if(after){
            if(['pid','ppid','pgid','session','startTicks'].some(key=>before[key]!==after[key]))add('STAT_CHANGED');
            else result.members.push(after);
          }
        }
        if(result.reasons.some(reason=>['DEADLINE','INVALID_CLOCK','READ_LIMIT'].includes(reason)))break;
      }
      // Do not consume an unbounded extra entry merely to prove iteration EOF.
      // Exactly-at-cap scans stay incomplete unless EOF was already observed.
      if(result.entriesRead>=groupCensusLimits.entries){add('ENTRY_LIMIT');break;}
    }
  }catch{add('DIRECTORY_READ');}
  within();result.members.sort((a,b)=>a.pid-b.pid);
  if(textBytes(JSON.stringify(result))>groupCensusLimits.observationBytes)return incomplete(group,'OBSERVATION_BUDGET');
  return result;
}

async function readProcStat(pid,cap){
  const file=await open('/proc/'+pid+'/stat',constants.O_RDONLY|constants.O_NOFOLLOW|constants.O_NONBLOCK);
  try{
    const bytes=Buffer.alloc(cap+1);let used=0;
    while(used<bytes.length){const part=await file.read(bytes,used,bytes.length-used,null);if(!part.bytesRead)break;used+=part.bytesRead;}
    if(used>cap)throw failure('STAT_INVALID');
    try{return new TextDecoder('utf-8',{fatal:true}).decode(bytes.subarray(0,used));}catch{throw failure('STAT_INVALID');}
  }finally{await file.close();}
}
async function* procEntries(){
  // opendir's iterator closes its handle on break, throw, and natural EOF.
  const directory=await opendir('/proc',{bufferSize:16});
  for await(const entry of directory)yield entry.name;
}
export async function observeLinuxProcessGroup(group){
  try{return await collectGroupCensus(group,{platform:process.platform,now:()=>performance.now(),entries:procEntries,readStat:readProcStat});}
  catch{return incomplete(group,'OBSERVER_FAILURE');}
}

// Preserve the original proof's bounded writer. An observation can never make
// an already-full proof unwriteable or replace existing command/identity data.
export function attachGroupCensus(proof,observation){
  if(!proof||typeof proof!=='object'||Object.hasOwn(proof,'groupCensus'))return false;
  const maximum=groupCensusLimits.commandProofBytes-groupCensusLimits.proofReserveBytes;
  const fits=value=>{try{return textBytes(JSON.stringify({...proof,groupCensus:value},null,2)+'\n')<=maximum;}catch{return false;}};
  let bounded=false;try{bounded=!!observation&&textBytes(JSON.stringify(observation))<=groupCensusLimits.observationBytes;}catch{}
  if(bounded&&fits(observation)){proof.groupCensus=observation;return true;}
  const fallback=incomplete(proof.pid,'PROOF_BUDGET');
  if(fits(fallback)){proof.groupCensus=fallback;return true;}return false;
}
