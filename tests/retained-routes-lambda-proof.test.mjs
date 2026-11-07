// Pure synthetic evidence negatives: no processes, sockets, browsers or Go.
// These fixtures never stand in for the separately required strictNativeProof.
import test from 'node:test';
import assert from 'node:assert/strict';
import {assertLambdaReports,validateLambdaProof,assertTaskDeadline} from './retained-routes-lambda-proof.mjs';

const clone=value=>structuredClone(value);
const deadline={path:'/api/task',message:'context deadline exceeded',type:'NextGoInvocationError',class:'deadline'};
const overflow={path:'/_nextgo/aaaaaaaaaaaaaaaaaaaa/app.wasm',message:'HTTP response exceeds buffered Lambda payload limit',type:'NextGoInvocationError',class:'buffered-overflow'};
function evidence(mode='buffered',expected){
  const id='retained-runtime-1',gatewayRequestID='retained-gateway-1',path=expected?.path||'/a',kind=expected?'error':'response';
  const at=1000,deadlineMS=at+14000,ackAt=expected?.class==='deadline'?deadlineMS:at+1;
  const identity={id,runtimeID:id,gatewayRequestID,path};
  const observations=[
    {kind:'poll',id:null,runtimeID:null,gatewayRequestID:null,path:null,order:1,at},
    {...identity,kind:'queued',order:2,at,admissionDeadline:at+15000},
    {...identity,kind:'dispatch',order:3,at,deadline:deadlineMS,admissionDeadline:at+15000},
    {...identity,kind:'report-ack',publicationKind:kind,ack:202,order:4,at:ackAt},
  ];
  const report={...identity,kind,mode,method:'GET',queuedAt:at,dispatchedAt:at,deadline:deadlineMS,admissionDeadline:at+15000,queuedOrder:2,dispatchOrder:3,ackOrder:4,order:4,
    runtimeBytes:expected?128:mode==='buffered'?64:74,rawJSONBytes:expected?128:64,httpBytes:expected?0:2,metadataBytes:expected||mode==='buffered'?0:64,base64flag:expected||mode==='streaming'?null:false,
    trailers:[],statusCode:expected?null:200,errorMessage:expected?.message??null,errorType:expected?.type??null,error:expected?{type:expected.type,message:expected.message}:null,
    deadlineExpired:expected?.class==='deadline',ack:202,acknowledgedAt:ackAt};
  const native={pid:42,binary:'/owned/bootstrap',sha256:'a'.repeat(64),bytes:100,cwd:'/owned/cwd',nativeOwnerUID:1000,nativeIdentity:{pid:42,platform:'linux'},
    closed:true,joined:true,shutdownRequested:true,termSent:true,forced:false,code:0,signal:null,unexpectedExit:false,groupEmpty:true,cwdRemoved:true,identityRefused:false,cleanupDeferred:false,listenerRefused:true};
  const snapshot={policy:'retained-strict-v1',mode,childPID:42,stopping:true,poisoned:false,joined:true,exit:{code:0,signal:null},active:null,queued:0,invocations:1,activePublication:null,responseCommitted:false,
    reports:[report],observations,failures:[],droppedReports:0,droppedFailures:0,droppedObservations:0,listenerClosed:{runtime:true,public:true},sockets:{runtime:0,public:0},
    cleanup:{termAccepted:true,forced:false,deferredOwnership:false,signalTimedOut:false,joinTimedOut:false,listenerJoinTimedOut:false,startedAt:20000,completedAt:20001},worker:clone(native)};
  const proof={...clone(native),transport:'LOCAL mock Lambda '+mode,expectedInvocationErrors:expected?[clone(expected)]:[],lambda:snapshot};
  return {snapshot,proof,expectedErrors:expected?[clone(expected)]:[],dispatch:observations[2]};
}

test('LOCAL report/proof validation accepts both transport modes without changing evidence',()=>{
  for(const mode of ['buffered','streaming']){
    const item=evidence(mode),before=clone(item);
    assert.deepEqual(assertLambdaReports(item.snapshot),[]);
    assert.strictEqual(validateLambdaProof(item.proof,{mode}),item.proof);
    assert.deepEqual(item,before);
  }
});
test('only exact external deadline and buffered overflow fields authorize invocation errors',()=>{
  for(const [mode,expected]of [['buffered',deadline],['streaming',deadline],['buffered',overflow]]){
    const item=evidence(mode,expected);
    assert.equal(assertLambdaReports(item.snapshot,{expectedErrors:item.expectedErrors}).length,1);
    validateLambdaProof(item.proof,{mode,expectedErrors:item.expectedErrors});
  }
  const item=evidence('streaming',overflow);
  assert.throws(()=>assertLambdaReports(item.snapshot,{expectedErrors:item.expectedErrors}));
});
test('a matching phrase in another JSON field never substitutes for exact errorMessage/type/path',()=>{
  const mutations=[
    report=>{report.errorMessage='different';report.error.message='different';report.note=deadline.message;},
    report=>{report.errorType='DifferentInvocationError';report.error.type=report.errorType;report.note=deadline.type;},
    report=>{report.path='/other';report.note=deadline.path;},
    report=>{report.error={type:report.errorType,message:'different',note:deadline.message};},
  ];
  for(const mutate of mutations){const item=evidence('buffered',deadline);mutate(item.snapshot.reports[0]);assert.throws(()=>assertLambdaReports(item.snapshot,{expectedErrors:item.expectedErrors}));}
});
test('persisted expected errors cannot authorize themselves or change external list bytes',()=>{
  const item=evidence('buffered',deadline);
  assert.throws(()=>validateLambdaProof(item.proof,{mode:'buffered'}));
  item.proof.expectedInvocationErrors=[{class:deadline.class,type:deadline.type,message:deadline.message,path:deadline.path}];
  assert.throws(()=>validateLambdaProof(item.proof,{mode:'buffered',expectedErrors:[deadline]}));
  for(const expectedErrors of [[{path:deadline.path,message:deadline.message}], [{...deadline,type:'Other'}], [{...deadline,class:'anything'}], [deadline,deadline], [{...deadline,extra:true}], [{...deadline,message:'same phrase elsewhere'}]]){
    assert.throws(()=>assertLambdaReports(evidence('buffered',deadline).snapshot,{expectedErrors}));
  }
});
test('task deadline requires exact actual dispatch, expired14s budget and physical ACK timing',()=>{
  for(const mode of ['buffered','streaming']){const item=evidence(mode,deadline);assert.strictEqual(assertTaskDeadline(item.snapshot,item.dispatch),item.snapshot.reports[0]);}
  const mutations=[
    item=>{item.dispatch.deadline--;item.snapshot.reports[0].deadline--;},
    item=>{item.snapshot.reports[0].deadlineExpired=false;},
    item=>{item.snapshot.reports[0].acknowledgedAt=item.dispatch.deadline-1;item.snapshot.observations[3].at=item.dispatch.deadline-1;},
    item=>{item.snapshot.reports[0].dispatchedAt++;},
    item=>{item.snapshot.reports[0].deadline++;},
    item=>{item.snapshot.reports[0].id='retained-runtime-2';},
    item=>{item.snapshot.reports[0].ack=200;},
    item=>{item.snapshot.reports[0].order=2;},
    item=>{item.snapshot.observations.splice(2,1);},
    item=>{item.snapshot.reports=[];},
  ];
  for(const mutate of mutations){const item=evidence('buffered',deadline);mutate(item);assert.throws(()=>assertTaskDeadline(item.snapshot,item.dispatch));}
  const item=evidence('buffered',deadline);assert.throws(()=>assertTaskDeadline(item.snapshot,{...item.dispatch,id:'retained-runtime-2'}));
});
test('reports must bind exact dispatch and ACK identities, paths, orders and timestamps',()=>{
  const mutations=[
    item=>{item.snapshot.reports[0].runtimeID='retained-runtime-2';},
    item=>{item.snapshot.reports[0].gatewayRequestID='retained-gateway-2';},
    item=>{item.snapshot.reports[0].mode='streaming';},
    item=>{item.snapshot.reports[0].dispatchOrder=2;},
    item=>{item.snapshot.reports[0].ackOrder=3;},
    item=>{item.snapshot.reports[0].queuedOrder=1;},
    item=>{item.snapshot.reports[0].admissionDeadline++;},
    item=>{item.snapshot.reports[0].queuedAt++;},
    item=>{item.snapshot.reports[0].acknowledgedAt=999;},
    item=>{item.snapshot.observations[3].path='/other';},
    item=>{item.snapshot.observations[3].ack=500;},
    item=>{item.snapshot.observations[3].publicationKind='error';},
    item=>{item.snapshot.observations[2].at++;},
    item=>{item.snapshot.observations[2].deadline++;},
    item=>{item.snapshot.observations[1].order=3;},
    item=>{item.snapshot.reports.push(clone(item.snapshot.reports[0]));},
    item=>{item.snapshot.invocations=2;},
  ];
  for(const mutate of mutations){const item=evidence();mutate(item);assert.throws(()=>assertLambdaReports(item.snapshot));}
});
test('a next poll cannot race an active publication and missing terminal reports fail closed',()=>{
  const item=evidence();item.snapshot.observations.splice(3,0,{kind:'poll',id:null,runtimeID:null,gatewayRequestID:null,path:null,order:4,at:1001});
  item.snapshot.observations[4].order=5;Object.assign(item.snapshot.reports[0],{ackOrder:5,order:5});
  assert.throws(()=>assertLambdaReports(item.snapshot));
  const missing=evidence();missing.snapshot.reports=[];assert.throws(()=>assertLambdaReports(missing.snapshot));
});
test('buffered body, full envelope and base64 accounting caps are independently enforced',()=>{
  const mutations=[report=>{report.httpBytes=(4<<20)+1;report.runtimeBytes=report.rawJSONBytes=6<<20;},report=>{report.runtimeBytes=report.rawJSONBytes=(6<<20)+1;},report=>{report.base64flag='false';},report=>{report.base64flag=null;},report=>{report.base64flag=true;report.httpBytes=4<<20;report.runtimeBytes=report.rawJSONBytes=4<<20;},report=>{report.rawJSONBytes--;},report=>{report.metadataBytes=1;}];
  for(const mutate of mutations){const item=evidence();mutate(item.snapshot.reports[0]);assert.throws(()=>assertLambdaReports(item.snapshot));}
  const item=evidence();Object.assign(item.snapshot.reports[0],{httpBytes:4<<20,base64flag:true,runtimeBytes:6<<20,rawJSONBytes:6<<20});assertLambdaReports(item.snapshot);
});
test('stream metadata, body, delimiter accounting and forbidden trailers have separate bounds',()=>{
  const mutations=[report=>{report.metadataBytes=report.rawJSONBytes=(16<<10)+1;report.runtimeBytes=report.metadataBytes+8+report.httpBytes;},report=>{report.httpBytes=(32<<20)+1;report.runtimeBytes=report.metadataBytes+8+report.httpBytes;},report=>{report.runtimeBytes--;},report=>{report.base64flag=false;},report=>{report.trailers=['Lambda-Runtime-Function-Error-Type','late'];},report=>{report.metadataBytes=report.rawJSONBytes=0;report.runtimeBytes=8+report.httpBytes;}];
  for(const mutate of mutations){const item=evidence('streaming');mutate(item.snapshot.reports[0]);assert.throws(()=>assertLambdaReports(item.snapshot));}
  const item=evidence('streaming');Object.assign(item.snapshot.reports[0],{metadataBytes:16<<10,rawJSONBytes:16<<10,httpBytes:32<<20,runtimeBytes:(16<<10)+8+(32<<20)});assertLambdaReports(item.snapshot);
});
test('unexpected errors, missing policy, evidence loss and coercible flags cannot qualify',()=>{
  for(const key of ['droppedReports','droppedFailures','droppedObservations'])for(const value of [1,false,undefined]){const item=evidence();item.snapshot[key]=value;assert.throws(()=>assertLambdaReports(item.snapshot));}
  for(const mutate of [s=>{s.policy='legacy';},s=>{s.failures=['native report failed'];},s=>{s.reports=Array(129).fill(s.reports[0]);},s=>{s.observations=Array(129).fill(s.observations[0]);},s=>{s.queued=33;}]){const item=evidence();mutate(item.snapshot);assert.throws(()=>assertLambdaReports(item.snapshot));}
  const item=evidence('buffered',deadline);assert.throws(()=>assertLambdaReports(item.snapshot));
});
test('LOCAL final proof requires joined worker, owned PID/artifact and both physical listener closures',()=>{
  const mutations=[
    p=>{p.transport='AWS';},p=>{p.lambda.mode='streaming';},p=>{p.lambda.joined=false;},p=>{p.lambda.active='retained-runtime-1';},p=>{p.lambda.queued=1;},p=>{p.lambda.stopping=false;},p=>{p.lambda.poisoned=true;},p=>{p.lambda.responseCommitted=true;},p=>{p.lambda.exit.code=1;},p=>{p.lambda.exit.signal='SIGTERM';},
    p=>{p.lambda.cleanup.termAccepted=false;},p=>{p.lambda.cleanup.forced=true;},p=>{p.lambda.cleanup.deferredOwnership=true;},p=>{p.lambda.cleanup.signalTimedOut=true;},p=>{p.lambda.cleanup.joinTimedOut=true;},p=>{p.lambda.cleanup.listenerJoinTimedOut=true;},p=>{p.lambda.cleanup.completedAt=19999;},
    p=>{p.lambda.listenerClosed.runtime=false;},p=>{p.lambda.listenerClosed.public=false;},p=>{p.lambda.sockets.runtime=1;},p=>{p.lambda.sockets.public=1;},
    p=>{p.lambda.childPID=43;},p=>{p.lambda.worker.pid=43;},p=>{p.nativeIdentity.pid=43;},p=>{p.lambda.worker.nativeIdentity.pid=43;},p=>{p.lambda.worker.nativeIdentity.extra='another-process';},p=>{p.lambda.worker.binary='/other/bootstrap';},p=>{delete p.binary;delete p.lambda.worker.binary;},p=>{p.lambda.worker.cwd='/other/cwd';},p=>{p.lambda.worker.closed=false;},p=>{p.closed=false;},p=>{p.identityRefused=true;},p=>{p.cleanupDeferred=true;},p=>{p.forced=true;},p=>{p.cwdRemoved=false;},p=>{p.listenerRefused=false;},
  ];
  for(const mutate of mutations){const item=evidence();mutate(item.proof);assert.throws(()=>validateLambdaProof(item.proof,{mode:'buffered'}));}
});

test('only own content-addressed module paths can be external buffered-overflow expectations',()=>{
  const module={...overflow,path:'/_nextgo/'+'a'.repeat(20)+'/module-'+'b'.repeat(64)+'.wasm'},value=evidence('buffered',module);
  assert.equal(assertLambdaReports(value.snapshot,{expectedErrors:[module]}).length,1);validateLambdaProof(value.proof,{mode:'buffered',expectedErrors:[module]});
  for(const path of [module.path.replace('module-','module-wrong-'),module.path.replace('b'.repeat(64),'b'.repeat(63)),module.path+'?query=1',module.path.replace('a'.repeat(20),'foreign-runtime')]){const expected={...module,path},bad=evidence('buffered',expected);assert.throws(()=>assertLambdaReports(bad.snapshot,{expectedErrors:[expected]}));}
  assert.throws(()=>assertLambdaReports(value.snapshot,{expectedErrors:[module,module]}));
});
