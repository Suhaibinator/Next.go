// Pure observation/parser negatives. No /proc access, process launch, signal,
// browser, sleeping, or runtime cleanup is performed by these test cases.
import test from 'node:test';
import assert from 'node:assert/strict';
import {groupCensusLimits as limits,parseGroupStat,collectGroupCensus,attachGroupCensus} from './retained-process-group-census.mjs';

function stat(pid,{ppid=1,pgid=700,session=700,startTicks='99',state='S',comm='discard me'}={}){
  const fields=[state,...Array(49).fill('0')];
  fields[1]=String(ppid);fields[2]=String(pgid);fields[3]=String(session);fields[19]=String(startTicks);
  return `${pid} (${comm}) ${fields.join(' ')}\n`;
}
function io(entries,readStat,{now=()=>0,platform='linux'}={}){
  return {platform,now,entries:async function*(){for(const entry of entries)yield String(entry);},readStat};
}
const sample=()=>({version:1,status:'COMPLETE_SCAN_OBSERVATION',atomic:false,signalAuthority:false,absenceProof:false,group:700,reasons:[],members:[{pid:701,ppid:1,state:'S',pgid:700,session:700,startTicks:'99'}]});

test('census parses kernel zero identities and discards arbitrary parenthesized comm data',()=>{
  assert.deepEqual(parseGroupStat(stat(701,{comm:'private ) nested ( name'})),{pid:701,ppid:1,state:'S',pgid:700,session:700,startTicks:'99'});
  assert.deepEqual(parseGroupStat(stat(2,{ppid:0,pgid:0,session:0,startTicks:'0',state:'I'})),{pid:2,ppid:0,state:'I',pgid:0,session:0,startTicks:'0'});
  const good=stat(701);
  for(const raw of [null,'701 missing comm',good.replace(' S ',' SS '),good.replace('700 700','-1 700'),good.replace('701 (','2147483648 ('),good.replace(' 99 ',' 18446744073709551616 '),good+'extra',good.replace('discard me','x'.repeat(limits.statBytes)),good.replace('discard me','\u0000'),good.replace('discard me','\ud800')])assert.throws(()=>parseGroupStat(raw));
});

test('census observes only matching stable lifetimes without retaining names or obtaining authority',async()=>{
  const reads=[];
  const result=await collectGroupCensus(700,io(['self','701','900'],async pid=>{reads.push(pid);return stat(pid,{pgid:pid===900?900:700,comm:'DO_NOT_RETAIN_AUTH_DATA'});}));
  assert.equal(result.status,'COMPLETE_SCAN_OBSERVATION');assert.deepEqual(reads,[701,701,900]);
  assert.equal(result.entriesRead,3);assert.equal(result.pidsRead,2);assert.equal(result.statReads,3);
  assert.equal(result.members.length,1);assert.deepEqual(result.members[0],{pid:701,ppid:1,state:'S',pgid:700,session:700,startTicks:'99'});
  assert.equal(result.atomic,false);assert.equal(result.signalAuthority,false);assert.equal(result.absenceProof,false);
  assert.equal(JSON.stringify(result).includes('AUTH_DATA'),false);
});

test('empty census remains an observation and cannot prove group absence',async()=>{
  const result=await collectGroupCensus(700,io(['800'],async pid=>stat(pid,{pgid:800})));
  assert.deepEqual(result.members,[]);assert.equal(result.status,'COMPLETE_SCAN_OBSERVATION');assert.equal(result.absenceProof,false);
});

test('missing denied malformed or changed stat observations stay incomplete rather than empty cleanup',async()=>{
  const faults=[
    async()=>{const error=Error('PRIVATE_FAILURE');error.code='ENOENT';throw error;},
    async()=>{const error=Error('PRIVATE_FAILURE');error.code='EPERM';throw error;},
    async()=>stat(702),async()=>stat(701).replace(' S ',' ! ')
  ];
  for(const read of faults){const result=await collectGroupCensus(700,io(['701'],read));assert.equal(result.status,'INCOMPLETE');assert.equal(result.reasons.length,1);assert.equal(result.absenceProof,false);assert.equal(JSON.stringify(result).includes('PRIVATE_FAILURE'),false);}
  for(const change of [{startTicks:'100'},{pgid:800},{session:800},{ppid:44}]){
    let calls=0;const result=await collectGroupCensus(700,io(['701'],async()=>stat(701,++calls===1?{}:change)));
    assert.equal(result.status,'INCOMPLETE');assert.ok(result.reasons.includes('STAT_CHANGED'));assert.deepEqual(result.members,[]);
  }
});

test('directory failure duplicates invalid platform and invalid group do not become absent group proofs',async()=>{
  let reads=0;
  for(const group of [0,-1,NaN,2147483648,'700']){
    const result=await collectGroupCensus(group,io(['701'],async()=>{reads++;return stat(701);}));assert.equal(result.status,'INCOMPLETE');assert.deepEqual(result.reasons,['INVALID_GROUP']);
  }
  const unsupported=await collectGroupCensus(700,io(['701'],async()=>{reads++;return stat(701);},{platform:'darwin'}));assert.deepEqual(unsupported.reasons,['UNSUPPORTED_PLATFORM']);assert.equal(reads,0);
  const duplicate=await collectGroupCensus(700,io(['701','701'],async()=>stat(701)));assert.equal(duplicate.status,'INCOMPLETE');assert.ok(duplicate.reasons.includes('DIRECTORY_READ'));
  const broken=await collectGroupCensus(700,{platform:'linux',now:()=>0,entries:async function*(){yield '701';throw Error('SECRET_DIRECTORY');},readStat:async()=>stat(701)});assert.equal(broken.status,'INCOMPLETE');assert.equal(JSON.stringify(broken).includes('SECRET_DIRECTORY'),false);
});

test('entry member and conservative aggregate read limits stop finite scans and close iterators',async()=>{
  let closed=false,reads=0;
  const entries=await collectGroupCensus(700,{platform:'linux',now:()=>0,entries:async function*(){try{for(let i=0;i<limits.entries+10;i++)yield 'ignored-'+i;}finally{closed=true;}},readStat:async()=>{reads++;return stat(701);}});
  assert.equal(entries.entriesRead,limits.entries);assert.equal(reads,0);assert.equal(closed,true);assert.ok(entries.reasons.includes('ENTRY_LIMIT'));
  const members=await collectGroupCensus(700,io(Array.from({length:limits.members+1},(_,i)=>701+i),async pid=>stat(pid)));
  assert.equal(members.members.length,limits.members);assert.ok(members.reasons.includes('MEMBER_LIMIT'));assert.equal(members.status,'INCOMPLETE');
  const budget=await collectGroupCensus(700,io(Array.from({length:limits.pids+1},(_,i)=>2000+i),async pid=>stat(pid,{pgid:800})));
  assert.ok(budget.reservedReadBytes<=limits.reservedReadBytes);assert.ok(budget.statReads*(limits.statBytes+1)<=limits.reservedReadBytes);assert.ok(budget.reasons.includes('READ_LIMIT'));assert.equal(budget.status,'INCOMPLETE');
  const oversize=await collectGroupCensus(700,io(['701'],async()=>stat(701,{comm:'x'.repeat(limits.statBytes)})));assert.ok(oversize.reasons.includes('STAT_INVALID'));assert.equal(oversize.status,'INCOMPLETE');
});

test('deadline and invalid monotonic clock stop new reads without timers or abandoned promises',async()=>{
  let calls=0,time=0;
  const expired=await collectGroupCensus(700,io(['701','702'],async()=>{calls++;time=limits.elapsedMS;return stat(701);},{now:()=>time}));
  assert.equal(calls,1);assert.equal(expired.statReads,1);assert.ok(expired.reasons.includes('DEADLINE'));assert.equal(expired.status,'INCOMPLETE');assert.deepEqual(expired.members,[]);
  for(const clock of [()=>NaN,(()=>{let n=10;return()=>n--;})()]){
    const result=await collectGroupCensus(700,io(['701'],async()=>{throw Error('should not read');},{now:clock}));assert.equal(result.status,'INCOMPLETE');assert.ok(result.reasons.includes('INVALID_CLOCK'));
  }
});

test('diagnostic attachment preserves exact failure flags and bounded pretty-JSON command evidence',()=>{
  const proof={pid:700,closed:true,code:1,signal:null,groupEmpty:false,cleanupDeferred:true,identityChecks:[]},before=structuredClone(proof);
  assert.equal(attachGroupCensus(proof,sample()),true);assert.deepEqual(Object.fromEntries(Object.entries(proof).filter(([key])=>key!=='groupCensus')),before);
  assert.ok(Buffer.byteLength(JSON.stringify(proof,null,2)+'\n')<=limits.commandProofBytes-limits.proofReserveBytes);
  assert.equal(attachGroupCensus(proof,sample()),false);
  const enormous={...before,padding:'x'.repeat(limits.commandProofBytes)},saved=JSON.stringify(enormous);assert.equal(attachGroupCensus(enormous,sample()),false);assert.equal(JSON.stringify(enormous),saved);
  const circular={};circular.self=circular;
  const fallback={...before};assert.equal(attachGroupCensus(fallback,circular),true);assert.deepEqual(fallback.groupCensus.reasons,['PROOF_BUDGET']);assert.equal(fallback.groupEmpty,false);
  const bigObservation={...sample(),padding:'x'.repeat(limits.observationBytes)},bounded={...before};assert.equal(attachGroupCensus(bounded,bigObservation),true);assert.equal(bounded.groupCensus.status,'INCOMPLETE');assert.deepEqual(bounded.groupCensus.reasons,['PROOF_BUDGET']);
});
