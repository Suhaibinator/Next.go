// Pure data mutation tests only. Synthetic byte records here are not runtime
// evidence and never stand in for a real application or HTTP exchange.
import test from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {gzipSync} from 'node:zlib';
import {selectZIPAssets,validateZIPExchange,expectedAssetErrors,validateZIPModuleProof,validateZIPRuntimeReceipt} from './retained-routes-lambda-assets.mjs';
const sha=data=>createHash('sha256').update(data).digest('hex'),wasm=Buffer.from('0061736d0100000001020304','hex'),shim=Buffer.from('compiler matching runtime'),runtime='1'.repeat(20),prefix='/_nextgo/'+runtime+'/';
function fixture(app='merge'){
  const values={'app.wasm':wasm,'wasm_exec.js':shim,'boot.js':Buffer.from('minimal bootstrap'),css:Buffer.from('body{}')};if(app==='merge')values['module-'+sha(wasm)+'.wasm']=wasm;
  const assets=Object.entries(values).map(([name,data])=>{const path=name==='css'?'/assets/'+app+'.css':prefix+name;return {embeddedPath:name==='css'?'public/'+app+'.css':'_nextgo/'+runtime+'/'+name,defaultRequestPath:path,bytes:data.length,sha256:sha(data)};});
  return {app,values,artifact:{sha256:'a'.repeat(64),runtimeId:runtime,assets}};
}
function wire(data,encoding='identity',mime='application/wasm'){
  const bytes=encoding==='gzip'?gzipSync(data):data;return {status:200,data:bytes,headers:{'content-type':mime,'content-length':String(bytes.length),vary:'Accept-Encoding',...(encoding==='gzip'?{'content-encoding':'gzip'}:{})},complete:true,closed:true,requestClosed:true,responseClosed:true,socketClosed:true};
}
function baseline(){const source=fixture(),selected=selectZIPAssets('merge',source.artifact,sha(shim)),assets=Object.fromEntries(selected.descriptors.map(value=>[value.name,{...value}]));return {...selected,assets,modules:Object.values(assets).filter(value=>value.name.startsWith('module-'))};}
function moduleProof(value){const module=value.modules[0];return {version:1,abi:6,records:['initial','held-recovery'].map(phase=>({phase,url:'http://127.0.0.1:4188'+module.path,status:200,mime:'application/wasm',bytes:module.bytes,sha256:module.sha256,complete:true,wasmMagic:'0061736d01000000',hostABI:6,buildPrefix:value.prefix}))};}

test('ZIP inventory requires both actual app identities and own generated module hash',()=>{
  for(const app of ['retained','merge']){const value=fixture(app),result=selectZIPAssets({id:app},value.artifact,sha(shim));assert.equal(result.app,app);assert.equal(result.descriptors.length,app==='merge'?5:4);assert.equal(result.artifactSHA256,value.artifact.sha256);assert.equal(result.prefix,prefix);}
  for(const mutate of [value=>value.assets.pop(),value=>value.assets.push({...value.assets[0]}),value=>{value.assets.at(-1).sha256='b'.repeat(64);},value=>{value.assets[0].defaultRequestPath='/_nextgo/foreign/app.wasm';},value=>{value.assets[0].embeddedPath='../escape';},value=>{value.assets[1].sha256='b'.repeat(64);}]){const value=fixture();mutate(value.artifact);assert.throws(()=>selectZIPAssets('merge',value.artifact,sha(shim)));}
  const noModule=fixture('retained');assert.throws(()=>selectZIPAssets('merge',noModule.artifact,sha(shim)));assert.throws(()=>selectZIPAssets('unreviewed',fixture().artifact,sha(shim)));
});
test('ZIP exchange independently binds MIME physical joins gzip and own decoded inventory',()=>{
  const value=fixture(),descriptor=selectZIPAssets('merge',value.artifact,sha(shim)).descriptors[0];
  for(const encoding of ['identity','gzip']){const result=validateZIPExchange(wire(wasm,encoding),descriptor,encoding);assert.equal(result.sha256,sha(wasm));assert.equal(result.bytes,wasm.length);assert.equal(result.closed,true);assert.equal(result.decodedSHA256,descriptor.sha256);}
  for(const mutate of [value=>{value.closed=false;},value=>{value.socketClosed=false;},value=>{value.responseClosed=false;},value=>{value.requestClosed=false;},value=>{value.complete=false;},value=>{value.status=502;},value=>{value.headers['content-type']='text/plain';},value=>{delete value.headers.vary;},value=>{value.headers['content-length']='01';},value=>{value.data=Buffer.from('wrong');value.headers['content-length']=String(value.data.length);}]){const value=wire(wasm,'gzip');mutate(value);assert.throws(()=>validateZIPExchange(value,descriptor,'gzip'));}
  assert.throws(()=>validateZIPExchange(wire(wasm,'identity'),descriptor,'gzip'));assert.throws(()=>validateZIPExchange(wire(wasm,'gzip'),descriptor,'identity'));
});
test('Only exact oversized buffered WASM identities declare independent genuine runtime errors',()=>{
  const value=baseline();value.assets['app.wasm'].bytes=(4<<20)+1;value.modules[0].bytes=(4<<20)+2;
  const errors=expectedAssetErrors(value,'buffered');assert.equal(errors.length,2);assert.deepEqual(errors.map(item=>item.path),[value.assets['app.wasm'].path,value.modules[0].path]);assert.equal(new Set(errors.map(item=>item.path)).size,2);assert.ok(errors.every(item=>item.message==='HTTP response exceeds buffered Lambda payload limit'&&item.type==='NextGoInvocationError'&&item.class==='buffered-overflow'));
  assert.deepEqual(expectedAssetErrors(value,'streaming'),[]);value.assets['app.wasm'].bytes=4<<20;value.modules[0].bytes=4<<20;assert.deepEqual(expectedAssetErrors(value,'buffered'),[]);assert.throws(()=>expectedAssetErrors(value,'native'));
});
test('Both real lazy browser downloads must match own ZIP baseline rather than original CGO1 artifact',()=>{
  const value=baseline(),proof=moduleProof(value);assert.deepEqual(validateZIPModuleProof(proof,value),{sha256:sha(wasm),bytes:wasm.length,abi:6,buildPrefix:prefix,downloads:2});
  for(const mutate of [record=>{record.sha256='c'.repeat(64);},record=>{record.bytes++;},record=>{record.url='http://127.0.0.1:4188/_nextgo/'+'2'.repeat(20)+'/module-'+record.sha256+'.wasm';},record=>{record.hostABI=5;},record=>{record.phase='other';},record=>{record.complete=false;},record=>{record.status=502;},record=>{record.wasmMagic='0000000000000000';}]){const changed=moduleProof(value);mutate(changed.records[1]);assert.throws(()=>validateZIPModuleProof(changed,value));}
  assert.throws(()=>validateZIPModuleProof(proof,{...value,app:'retained'}));assert.throws(()=>validateZIPModuleProof(proof,{...value,modules:[]}));assert.throws(()=>validateZIPModuleProof({...proof,records:proof.records.slice(0,1)},value));
});

test('Actual Runtime receipt accounts for full buffered envelope and streamed prelude separately',()=>{
  const value=wire(wasm,'gzip'),count=value.data.length;
  const buffered={kind:'response',ack:202,mode:'buffered',statusCode:200,httpBytes:count,runtimeBytes:512,rawJSONBytes:512,metadataBytes:0,base64flag:true};
  assert.equal(validateZIPRuntimeReceipt(buffered,value,'buffered'),buffered);
  for(const mutate of [record=>{record.statusCode=500;},record=>{record.base64flag=false;},record=>{record.httpBytes++;},record=>{record.runtimeBytes=(6<<20)+1;},record=>{record.rawJSONBytes=1;},record=>{record.metadataBytes=1;},record=>{record.ack=500;}]){const changed={...buffered};mutate(changed);assert.throws(()=>validateZIPRuntimeReceipt(changed,value,'buffered'));}
  const streamed={kind:'response',ack:202,mode:'streaming',statusCode:200,httpBytes:count,runtimeBytes:32+8+count,rawJSONBytes:32,metadataBytes:32,base64flag:null};
  assert.equal(validateZIPRuntimeReceipt(streamed,value,'streaming'),streamed);
  for(const mutate of [record=>{record.runtimeBytes--;},record=>{record.rawJSONBytes++;},record=>{record.base64flag=true;},record=>{record.metadataBytes=17<<10;}]){const changed={...streamed};mutate(changed);assert.throws(()=>validateZIPRuntimeReceipt(changed,value,'streaming'));}
  const failure={kind:'error',ack:202,mode:'buffered',statusCode:null,httpBytes:0,runtimeBytes:120,rawJSONBytes:120,metadataBytes:0,base64flag:null,errorMessage:'HTTP response exceeds buffered Lambda payload limit',errorType:'NextGoInvocationError'};
  assert.equal(validateZIPRuntimeReceipt(failure,{...value,status:502},'buffered','error'),failure);
  assert.throws(()=>validateZIPRuntimeReceipt({...failure,errorMessage:'unrelated error'},{...value,status:502},'buffered','error'));assert.throws(()=>validateZIPRuntimeReceipt(failure,value,'streaming','error'));
});
