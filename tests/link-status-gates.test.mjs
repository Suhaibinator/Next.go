// Scheduling regressions only: no browser, HTTP, application or Go execution.
import test from 'node:test';
import assert from 'node:assert/strict';
import {holdRequests} from './link-status-gates.mjs';

function deferred(){let resolve;const promise=new Promise(done=>{resolve=done;});return {promise,resolve};}
function intercepted(){
  const started=deferred(),settle=deferred(),events=[];
  let handler,state='paused';
  const route={
    request:()=>({}),
    abort:async code=>dispatch('abort',code),
    continue:async()=>dispatch('continue'),
  };
  async function dispatch(kind,code){
    events.push(kind+'-started');started.resolve();await settle.promise;
    if(state!=='paused')throw Error('Route is already handled!');
    assert.equal(kind==='abort'?code:undefined,kind==='abort'?'failed':undefined);
    state=kind;events.push(kind+'-settled');
  }
  const page={
    async route(pattern,callback){assert.equal(pattern,'owned');handler=callback;events.push('installed');},
    async unroute(pattern,callback){
      assert.equal(pattern,'owned');assert.equal(callback,handler);events.push('unroute');
      // Model a scheduling hazard consistent with the trace; this is not an
      // observation of browser internals or a causal claim about forwarding.
      if(state==='paused')state='forwarded-by-unroute';
      handler=null;
    },
  };
  return {page,started,settle,events,dispatch:()=>handler(route),state:()=>state};
}

for(const failure of [true,false]){
  test(`gate close settles ${failure?'abort':'continue'} before removing interception`,{timeout:5000},async()=>{
    const native=intercepted(),gate=await holdRequests(native.page,'owned',()=>true,failure);
    const job=native.dispatch(),closing=gate.close();
    try{
      await native.started.promise;
      assert.strictEqual(gate.close(),closing,'close joins the same lifecycle');
      assert.equal(native.events.includes('unroute'),false,'native disposition must settle first');
    }finally{native.settle.resolve();await closing;await job;}
    const kind=failure?'abort':'continue';
    assert.equal(native.state(),kind);
    assert.deepEqual(native.events,['installed',kind+'-started',kind+'-settled','unroute']);
  });
}

test('gate close also drains unmatched continued requests before interception removal',{timeout:5000},async()=>{
  const native=intercepted(),gate=await holdRequests(native.page,'owned',()=>false,true);
  const job=native.dispatch(),closing=gate.close();
  try{
    await native.started.promise;
    assert.equal(native.events.includes('unroute'),false);
  }finally{native.settle.resolve();await closing;await job;}
  assert.equal(native.state(),'continue');
  assert.deepEqual(native.events,['installed','continue-started','continue-settled','unroute']);
});

test('gate close preserves unexpected handler failure and still removes interception',{timeout:5000},async()=>{
  let handler,removed=false;
  const page={route:async(_pattern,callback)=>{handler=callback;},unroute:async()=>{removed=true;}};
  const gate=await holdRequests(page,'owned',()=>true,true);
  const job=handler({request:()=>({}),abort:async()=>{throw Error('deliberate native disposition failure');}});
  // Observe rejection immediately; close must retain it rather than swallowing
  // it as a canceled/handled request or succeeding after removing the route.
  const failed=assert.rejects(job,/deliberate native disposition failure/);
  await assert.rejects(gate.close(),error=>error instanceof AggregateError&&error.errors.length===1&&/deliberate native disposition failure/.test(error.errors[0].message));
  await failed;assert.equal(removed,true);
});
