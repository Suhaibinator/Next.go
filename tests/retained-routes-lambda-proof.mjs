// Pure LOCAL mock-Runtime evidence checks. These do not qualify native identity
// or AWS behavior; the caller must separately run strictNativeProof.
import assert from 'node:assert/strict';

const limits=Object.freeze({reports:128,observations:128,queue:32,error:64<<10,envelope:6<<20,body:4<<20,metadata:16<<10,stream:32<<20});
const plain=value=>value!==null&&typeof value==='object'&&Object.getPrototypeOf(value)===Object.prototype;
const integer=(value,min=0,max=Number.MAX_SAFE_INTEGER)=>Number.isSafeInteger(value)&&value>=min&&value<=max;
const path=value=>typeof value==='string'&&value.length>0&&value.length<=8192&&value.startsWith('/')&&!value.startsWith('//')&&!value.includes('#');
const runtimeID=value=>typeof value==='string'&&/^retained-runtime-[1-9][0-9]{0,15}$/.test(value);
function expectedList(values){
  assert.ok(Array.isArray(values)&&values.length<=32,'bounded external invocation error expectations');
  const paths=new Set();
  for(const value of values){
    assert.ok(plain(value)&&Object.keys(value).sort().join(',')==='class,message,path,type','exact invocation error expectation schema');
    assert.ok(path(value.path)&&typeof value.message==='string'&&value.message.length>0&&value.message.length<=2048&&value.type==='NextGoInvocationError','external invocation error fields');
    assert.ok(value.class==='deadline'||value.class==='buffered-overflow','known invocation error expectation class');
    if(value.class==='deadline')assert.ok(value.path==='/api/task'&&value.message==='context deadline exceeded','exact task deadline expectation');
    else assert.ok(/^\/_nextgo\/[a-f0-9]{20}\/(?:app|module-[a-f0-9]{64})\.wasm$/.test(value.path)&&value.message==='HTTP response exceeds buffered Lambda payload limit','exact buffered asset overflow expectation');
    assert.ok(!paths.has(value.path),'one externally expected error per exact path');paths.add(value.path);
  }
  return values;
}
function identity(value){
  assert.ok(plain(value)&&runtimeID(value.id)&&value.runtimeID===value.id,'exact Runtime invocation identity');
  assert.equal(value.gatewayRequestID,value.id.replace('retained-runtime-','retained-gateway-'),'separate gateway request identity');
  assert.ok(path(value.path),'exact report application path');
}
function ledger(snapshot){
  assert.ok(plain(snapshot)&&snapshot.policy==='retained-strict-v1'&&(snapshot.mode==='buffered'||snapshot.mode==='streaming'),'strict LOCAL Runtime policy and mode');
  for(const key of ['droppedReports','droppedFailures','droppedObservations'])assert.equal(snapshot[key],0,'no dropped Runtime evidence');
  assert.ok(Array.isArray(snapshot.failures)&&snapshot.failures.length===0,'no Runtime protocol or cleanup failures');
  assert.ok(Array.isArray(snapshot.reports)&&snapshot.reports.length<=limits.reports&&Array.isArray(snapshot.observations)&&snapshot.observations.length<=limits.observations,'bounded complete Runtime ledgers');
  assert.ok(integer(snapshot.queued,0,limits.queue)&&integer(snapshot.invocations),'bounded Runtime admission counters');
  const queued=new Map(),dispatches=new Map(),acks=new Map();let active=null;
  for(let index=0;index<snapshot.observations.length;index++){
    const observation=snapshot.observations[index];
    assert.ok(plain(observation)&&observation.order===index+1&&integer(observation.at),'whole ordered observation ledger');
    if(observation.kind==='poll'){
      assert.ok(active===null&&observation.id===null&&observation.runtimeID===null&&observation.gatewayRequestID===null&&observation.path===null,'next poll cannot overtake an active publication');
      continue;
    }
    identity(observation);
    if(observation.kind==='queued'){
      assert.ok(!queued.has(observation.id)&&integer(observation.admissionDeadline)&&observation.admissionDeadline>=observation.at,'unique admission before outer deadline');
      queued.set(observation.id,observation);
    }else if(observation.kind==='dispatch'){
      const admission=queued.get(observation.id);
      assert.ok(admission&&active===null&&!dispatches.has(observation.id),'one physical serial dispatch per admitted invocation');
      assert.ok(admission.path===observation.path&&admission.gatewayRequestID===observation.gatewayRequestID&&admission.order<observation.order&&admission.at<=observation.at,'dispatch binds prior same-path admission');
      assert.equal(observation.admissionDeadline,admission.admissionDeadline,'unchanged admission deadline');
      assert.equal(observation.deadline,observation.at+14000,'actual dispatch-relative14s deadline');
      assert.ok(observation.at<observation.admissionDeadline,'dispatch before admission expiry');
      dispatches.set(observation.id,observation);active=observation.id;
    }else{
      assert.equal(observation.kind,'report-ack','known Runtime observation kind');
      assert.ok(active===observation.id&&!acks.has(observation.id),'ACK settles the exact active invocation once');
      const dispatch=dispatches.get(observation.id);
      assert.ok(dispatch&&dispatch.path===observation.path&&dispatch.gatewayRequestID===observation.gatewayRequestID&&dispatch.order<observation.order&&dispatch.at<=observation.at,'ACK binds same-path dispatch');
      assert.ok(observation.publicationKind==='response'||observation.publicationKind==='error','known Runtime publication');
      assert.equal(observation.ack,202,'physical Runtime publication ACK');
      acks.set(observation.id,observation);active=null;
    }
  }
  assert.equal(snapshot.invocations,queued.size,'admission count matches complete observation history');
  assert.equal(snapshot.active,active,'current active lease agrees with the physical serial ledger');
  const reports=new Map();let previousOrder=0;
  for(const report of snapshot.reports){
    identity(report);assert.equal(report.mode,snapshot.mode,'report transport mode');
    const dispatch=dispatches.get(report.id),ack=acks.get(report.id),admission=queued.get(report.id);
    assert.ok(dispatch&&ack&&admission&&!reports.has(report.id),'one report for an actual dispatch and ACK');
    assert.ok(report.path===dispatch.path&&report.gatewayRequestID===dispatch.gatewayRequestID&&report.kind===ack.publicationKind,'report binds exact path, identity and publication');
    assert.equal(report.ack,202,'every real publication acknowledged');
    assert.equal(report.queuedOrder,admission.order,'report admission order');assert.equal(report.dispatchOrder,dispatch.order,'report dispatch order');
    assert.equal(report.ackOrder,ack.order,'report ACK order');assert.equal(report.order,ack.order,'report publication order');
    assert.ok(report.order>previousOrder,'reports retain terminal order');previousOrder=report.order;
    assert.ok(integer(report.queuedAt)&&report.queuedAt<=admission.at,'admission timestamp precedes queued observation');
    assert.equal(report.admissionDeadline,report.queuedAt+15000,'unchanged admission-relative15s outer budget');
    assert.equal(report.admissionDeadline,dispatch.admissionDeadline,'report outer deadline binding');
    assert.equal(report.dispatchedAt,dispatch.at,'report dispatch timestamp binding');assert.equal(report.deadline,dispatch.deadline,'report actual deadline binding');
    assert.ok(integer(report.acknowledgedAt)&&report.acknowledgedAt>=dispatch.at&&ack.at>=report.acknowledgedAt,'actual acknowledgement timestamps');
    assert.equal(typeof report.deadlineExpired,'boolean','observed deadline flag');
    assert.ok(Array.isArray(report.trailers)&&report.trailers.length===0,'no forbidden or late Runtime stream trailers');
    assert.ok(integer(report.runtimeBytes,1)&&integer(report.rawJSONBytes,1)&&integer(report.httpBytes)&&integer(report.metadataBytes),'actual bounded publication byte counters');
    if(report.kind==='error'){
      assert.ok(report.runtimeBytes<=limits.error&&report.rawJSONBytes===report.runtimeBytes&&report.httpBytes===0&&report.metadataBytes===0&&report.base64flag===null&&report.statusCode===null,'pre-response bounded Runtime error JSON');
      assert.ok(typeof report.errorMessage==='string'&&report.errorMessage.length<=2048&&typeof report.errorType==='string'&&report.errorType.length<=256,'exact Runtime error strings');
      assert.deepEqual(report.error,{type:report.errorType,message:report.errorMessage},'nested Runtime error agrees with exact fields');
    }else{
      assert.equal(report.kind,'response','known Runtime report kind');
      assert.ok(integer(report.statusCode,200,599)&&report.error===null&&report.errorMessage===null&&report.errorType===null,'actual application response rather than invocation error');
      if(snapshot.mode==='buffered'){
        assert.ok(report.runtimeBytes<=limits.envelope&&report.rawJSONBytes===report.runtimeBytes&&report.httpBytes<=limits.body&&report.metadataBytes===0&&typeof report.base64flag==='boolean','buffered4MiB body/6MiB full envelope and explicit base64 flag');
        const encodedBodyBytes=report.base64flag?Math.ceil(report.httpBytes/3)*4:report.httpBytes;
        assert.ok(report.rawJSONBytes>=encodedBodyBytes,'buffered full JSON accounts for encoded body bytes');
      }else{
        assert.ok(report.metadataBytes>=1&&report.metadataBytes<=limits.metadata&&report.rawJSONBytes===report.metadataBytes&&report.httpBytes<=limits.stream&&report.base64flag===null,'stream16KiB metadata/32MiB body bounds');
        assert.equal(report.runtimeBytes,report.metadataBytes+8+report.httpBytes,'actual streaming prelude delimiter and body byte accounting');
      }
    }
    reports.set(report.id,report);
  }
  assert.equal(reports.size,acks.size,'every physical ACK has its retained exact report');
  return {dispatches,reports};
}

export function assertTaskDeadline(snapshot,dispatch){
  const evidence=ledger(snapshot);
  const taskDispatches=[...evidence.dispatches.values()].filter(item=>item.path==='/api/task');
  assert.equal(taskDispatches.length,1,'one actual task dispatch, no replay');
  assert.deepEqual(dispatch,taskDispatches[0],'caller task dispatch binds actual observation');
  assert.equal(dispatch.deadline,dispatch.at+14000,'exact task invocation deadline');
  const errors=snapshot.reports.filter(item=>item.kind==='error'&&item.path==='/api/task');
  assert.equal(errors.length,1,'one terminal task error');const report=errors[0];
  assert.equal(report.id,dispatch.id,'task error belongs to the observed invocation');
  assert.equal(report.errorMessage,'context deadline exceeded','exact task deadline message');assert.equal(report.errorType,'NextGoInvocationError','exact task invocation error type');
  assert.equal(report.dispatchedAt,dispatch.at,'task error dispatch timestamp');assert.equal(report.deadline,dispatch.deadline,'task error actual deadline');
  assert.equal(report.deadlineExpired,true,'task actual deadline expired before publication');
  assert.ok(report.acknowledgedAt>=dispatch.deadline&&report.ack===202&&report.order>dispatch.order&&report.ackOrder===report.order,'task error physically ACKed after actual deadline');
  return report;
}

export function assertLambdaReports(snapshot,{expectedErrors=[]}={}){
  expectedList(expectedErrors);const evidence=ledger(snapshot);
  const errors=snapshot.reports.filter(report=>report.kind==='error');
  assert.equal(errors.length,expectedErrors.length,'only externally expected invocation errors');
  for(const expected of expectedErrors){
    const matches=errors.filter(report=>report.path===expected.path&&report.errorMessage===expected.message&&report.errorType===expected.type);
    assert.equal(matches.length,1,'one exact externally expected error/path/message/type');
    if(expected.class==='deadline')assertTaskDeadline(snapshot,evidence.dispatches.get(matches[0].id));
    else assert.equal(snapshot.mode,'buffered','buffered-overflow is not a streaming allowance');
  }
  return errors;
}

export function validateLambdaProof(proof,{mode,expectedErrors=[]}={}){
  assert.ok(plain(proof)&&(mode==='buffered'||mode==='streaming'),'explicit LOCAL proof mode');
  expectedList(expectedErrors);expectedList(proof.expectedInvocationErrors);
  assert.equal(JSON.stringify(proof.expectedInvocationErrors),JSON.stringify(expectedErrors),'persisted error expectations byte-equal external authority');
  assert.equal(proof.transport,'LOCAL mock Lambda '+mode,'LOCAL transport binding');
  const snapshot=proof.lambda;assert.equal(snapshot?.mode,mode,'actual gateway mode');assertLambdaReports(snapshot,{expectedErrors});
  assert.ok(snapshot.stopping===true&&snapshot.poisoned===false&&snapshot.joined===true&&snapshot.active===null&&snapshot.queued===0&&snapshot.activePublication===null&&snapshot.responseCommitted===false,'joined clean gateway with no active leases');
  assert.ok(plain(snapshot.exit)&&snapshot.exit.code===0&&snapshot.exit.signal===null&&!snapshot.exit.error&&!snapshot.exit.closeError,'actual worker close result');
  const cleanup=snapshot.cleanup;
  assert.ok(plain(cleanup)&&cleanup.termAccepted===true&&cleanup.forced===false&&cleanup.deferredOwnership===false&&cleanup.signalTimedOut===false&&cleanup.joinTimedOut===false&&cleanup.listenerJoinTimedOut===false,'identity-authorized TERM and physical close without timeout or fallback');
  assert.ok(integer(cleanup.startedAt)&&integer(cleanup.completedAt)&&cleanup.completedAt>=cleanup.startedAt,'completed cleanup observation');
  assert.ok(snapshot.listenerClosed?.runtime===true&&snapshot.listenerClosed?.public===true&&snapshot.sockets?.runtime===0&&snapshot.sockets?.public===0,'both real listeners and socket sets physically closed');
  const worker=snapshot.worker;
  assert.ok(typeof proof.binary==='string'&&proof.binary.startsWith('/')&&proof.binary.length<=4096&&typeof proof.cwd==='string'&&proof.cwd.startsWith('/')&&proof.cwd.length<=4096&&typeof proof.sha256==='string'&&/^[a-f0-9]{64}$/.test(proof.sha256)&&integer(proof.bytes,1)&&integer(proof.nativeOwnerUID),'explicit native artifact and owner fields');
  assert.ok(plain(worker)&&integer(proof.pid,1)&&worker.pid===proof.pid&&snapshot.childPID===proof.pid&&proof.nativeIdentity?.pid===proof.pid&&worker.nativeIdentity?.pid===proof.pid,'actual worker/native PID binding');
  assert.deepEqual(worker.nativeIdentity,proof.nativeIdentity,'worker native identity belongs to the separately checked process');
  for(const key of ['binary','sha256','bytes','cwd','nativeOwnerUID'])assert.equal(worker[key],proof[key],'worker and outer artifact/owner binding');
  for(const item of [proof,worker]){
    assert.ok(item.closed===true&&item.joined===true&&item.shutdownRequested===true&&item.termSent===true&&item.forced===false&&item.code===0&&item.signal===null&&item.unexpectedExit===false&&item.groupEmpty===true&&item.cwdRemoved===true&&item.identityRefused===false&&item.cleanupDeferred===false,'completed owned native worker proof');
  }
  assert.equal(proof.listenerRefused,true,'outer fixture observed both listeners refusing connections');
  return proof;
}
