// Pure trusted-IO fault witnesses. No child, listener, process probe or filesystem
// workspace is created; the actual fixture calls this same retirement procedure.
import test from 'node:test';
import assert from 'node:assert/strict';
import {retireRetainedWorkspace} from './retained-routes-fixture.mjs';

const fresh=()=>({pid:42,closed:true,joined:true,groupEmpty:null,cleanupDeferred:false,identityRefused:false,cwdRemoved:false,listenerRefused:false,code:0,signal:null,forced:false});
function fixture(proof,options={}){
  const empty=Object.hasOwn(options,'empty')?options.empty:true;
  const listener=Object.hasOwn(options,'listener')?options.listener:true;
  const {failAt,mutateAt}=options,calls=[];
  const act=name=>{
    calls.push(name);
    if(mutateAt?.name===name)mutateAt.change(proof);
    if(failAt===name)throw Error('fixed fault: '+name);
  };
  return {calls,operations:{
    groupEmpty(){act('group');return empty;},
    async observeListener(){act('listener');proof.listenerRefused=listener;},
    async verifyArtifact(){act('artifact');},
    async verifyDirectory(){act('directory');},
    async removeDirectory(){act('remove');},
    async verifyAbsent(){act('absent');}
  }};
}
const refused=/not physically retired/;

test('unjoined child or missing PID cannot start filesystem retirement',async()=>{
  for(const changes of [{closed:false},{joined:false},{pid:null},{pid:1}]){
    const p={...fresh(),...changes},f=fixture(p);
    await assert.rejects(retireRetainedWorkspace(p,f.operations),refused);
    assert.deepEqual(f.calls,[]);assert.equal(p.cleanupDeferred,true);assert.equal(p.cwdRemoved,false);
  }
});
test('kernel-present and unknown groups retain the workspace before any remove',async()=>{
  for(const empty of [false,null,undefined]){
    const p=fresh(),f=fixture(p,{empty});
    await assert.rejects(retireRetainedWorkspace(p,f.operations),refused);
    assert.deepEqual(f.calls,['group']);assert.equal(p.groupEmpty,empty===false?false:null);
    assert.equal(p.cleanupDeferred,true);assert.equal(p.cwdRemoved,false);
  }
});
test('failed group observation remains unknown and cannot remove the workspace',async()=>{
  const p=fresh(),f=fixture(p,{failAt:'group'});
  await assert.rejects(retireRetainedWorkspace(p,f.operations),/fixed fault: group/);
  assert.deepEqual(f.calls,['group']);assert.equal(p.groupEmpty,null);assert.equal(p.cleanupDeferred,true);assert.equal(p.cwdRemoved,false);
});
test('missing listener refusal retains a physically closed group workspace',async()=>{
  for(const listener of [false,null,undefined]){
    const p=fresh(),f=fixture(p,{listener});
    await assert.rejects(retireRetainedWorkspace(p,f.operations),refused);
    assert.deepEqual(f.calls,['group','listener']);assert.equal(p.groupEmpty,true);
    assert.equal(p.cleanupDeferred,true);assert.equal(p.cwdRemoved,false);
  }
});
test('previous ownership refusal is not rehabilitated by later empty state',async()=>{
  for(const changes of [{identityRefused:true},{cleanupDeferred:true}]){
    const p={...fresh(),...changes},f=fixture(p);
    await assert.rejects(retireRetainedWorkspace(p,f.operations),refused);
    assert.deepEqual(f.calls,[]);assert.equal(p.cleanupDeferred,true);assert.equal(p.cwdRemoved,false);
  }
});
test('proved physical retirement checks ownership before remove and absence after',async()=>{
  const p=fresh(),f=fixture(p);
  await retireRetainedWorkspace(p,f.operations);
  assert.deepEqual(f.calls,['group','listener','artifact','directory','remove','absent']);
  assert.equal(p.groupEmpty,true);assert.equal(p.cwdRemoved,true);assert.equal(p.cleanupDeferred,false);
});
test('physical cleanup cannot turn failed or forced execution into passing execution',async()=>{
  for(const changes of [{code:1},{forced:true},{signal:'SIGKILL'}]){
    const p={...fresh(),...changes},f=fixture(p);
    await retireRetainedWorkspace(p,f.operations);
    assert.equal(p.cwdRemoved,true);assert.equal(p.cleanupDeferred,false);
    for(const [key,value]of Object.entries(changes))assert.equal(p[key],value);
    assert.equal(Object.hasOwn(p,'status'),false);
  }
});
test('lost join proof during owned verification blocks the actual remove callback',async()=>{
  const p=fresh(),f=fixture(p,{mutateAt:{name:'directory',change:value=>{value.joined=false;}}});
  await assert.rejects(retireRetainedWorkspace(p,f.operations),refused);
  assert.deepEqual(f.calls,['group','listener','artifact','directory']);
  assert.equal(p.cleanupDeferred,true);assert.equal(p.cwdRemoved,false);
});
test('lost group proof during owned verification blocks the actual remove callback',async()=>{
  const p=fresh(),f=fixture(p,{mutateAt:{name:'directory',change:value=>{value.groupEmpty=false;}}});
  await assert.rejects(retireRetainedWorkspace(p,f.operations),refused);
  assert.deepEqual(f.calls,['group','listener','artifact','directory']);
  assert.equal(p.cleanupDeferred,true);assert.equal(p.cwdRemoved,false);
});
test('ownership, removal and absence failures preserve failed cleanup receipts',async()=>{
  const order=['group','listener','artifact','directory','remove','absent'];
  for(const failAt of order.slice(1)){
    const p=fresh(),f=fixture(p,{failAt});
    await assert.rejects(retireRetainedWorkspace(p,f.operations),new RegExp('fixed fault: '+failAt));
    assert.deepEqual(f.calls,order.slice(0,order.indexOf(failAt)+1));
    assert.equal(p.cleanupDeferred,true);assert.equal(p.cwdRemoved,false);
  }
});
