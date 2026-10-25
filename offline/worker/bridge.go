// Package worker implements the separate managed service-worker runtime.
package worker

// ScriptTemplate embeds the matching SDK shim and frozen native descriptor.
const ScriptTemplate = `"use strict";
__NEXTGO_OFFLINE_SHIM__
(function(){
const D=__NEXTGO_OFFLINE_DESCRIPTOR__, C=JSON.parse(D.catalogJSON), NS=D.namespace;
const B={descriptorJSON:JSON.stringify(D)}, pending=new Map(), handles=new Map(), locks=new Map(), physical=new Set(), originals=new Map();
let goDispatch=null,goResume=null,bootPromise=null,exited=false,bootFailed=false,vmAttempted=false,bootstrapNetworkUsed=false,repairUsed=false,serial=0,handleSerial=0,events=0;
const limit=Math.max(1,Math.min(32,C.Config.Budget.InFlight)),queue=Math.max(1,Math.min(64,C.Config.Budget.Queue));
const documents=new Set();
const exact=new Set(C.Assets.map(function(a){return a.URL;}));
C.Documents.forEach(function(d){const url=C.Config.Origin+C.Config.BasePath+(d.Path==="/"&&C.Config.BasePath!==""?"":d.Path);exact.add(url);documents.add(url);});
function fallback(){return new Response("Offline saved document unavailable. Reconnect to load the application.",{status:503,headers:{"Content-Type":"text/plain; charset=utf-8","Cache-Control":"no-store","X-Nextgo-Offline-Fallback":"unavailable"}});}
function id(){serial++;if(serial>Number.MAX_SAFE_INTEGER)throw Error("sequence-exhausted");return String(serial);}
function sha(bytes){return crypto.subtle.digest("SHA-256",bytes).then(function(b){return Array.from(new Uint8Array(b)).map(function(n){return n.toString(16).padStart(2,"0");}).join("");});}
function recordKey(name){return C.Config.Origin+C.Config.BasePath+"/_nextgo/offline/_records/"+NS.slice("nextgo-offline-v1-".length)+"/"+name;}
function ownedName(name){return name===NS+"-meta"||name.startsWith(NS+"-g-")&&/^[a-f0-9]{64}$/.test(name.slice((NS+"-g-").length));}
async function names(){const all=(await caches.keys()).filter(function(n){return n===NS||n.startsWith(NS+"-");});if(all.length>4||all.some(function(n){return !ownedName(n);}))throw Error("corrupt-storage");return all;}
async function readBytes(response,max){
 if(!response||response.type==="opaque"||response.status!==200||!Number.isSafeInteger(max)||max<1||max>33554432)throw Error("invalid-response");
 const reader=response.body&&response.body.getReader();if(!reader)throw Error("missing-body");let n=0;const chunks=[];
 try{for(;;){const part=await reader.read();if(part.done)break;n+=part.value.byteLength;if(n>max)throw Error("decoded-limit");chunks.push(part.value);}}
 finally{try{await reader.cancel();}catch(_){}reader.releaseLock();}
 const bytes=new Uint8Array(n);let at=0;for(const chunk of chunks){bytes.set(chunk,at);at+=chunk.byteLength;}return bytes;
}
function track(p){physical.add(p);p.then(function(){physical.delete(p);},function(){physical.delete(p);});return p;}
async function deadlineFetch(url,consume){const controller=new AbortController(),timer=setTimeout(function(){controller.abort();},30000);let response;try{response=await fetch(url,{credentials:"omit",redirect:"error",cache:"no-store",signal:controller.signal});return await consume(response);}finally{if(response&&response.body&&!response.bodyUsed){controller.abort();try{await response.body.cancel();}catch(_){}}clearTimeout(timer);}}
async function retire(){if(exited)return;exited=true;goDispatch=null;goResume=null;
 // Go return is logical termination. Physical promises retain locks and event
 // admission until their cache/body/hash operations have actually returned.
 for(const original of originals.values()){if(original.cancel)await original.cancel();}
 while(physical.size){await Promise.allSettled(Array.from(physical));}
 const held=Array.from(locks.values());for(const lock of held)lock.release();await Promise.allSettled(held.map(function(lock){return lock.done;}));locks.clear();handles.clear();
 for(const p of pending.values())p.reject(Error("go-exited"));pending.clear();
}
async function observed(response,url,max,keep){const bytes=await readBytes(response,max);const h=await sha(bytes);const type=response.headers.get("Content-Type")||"";let handle=0;
 if(keep){if(handles.size>=32)throw Error("handle-limit");handle=++handleSerial;handles.set(handle,{bytes:bytes,type:type});}
 return {found:true,handle:handle,observation:{URL:url,SHA256:h,DecodedBytes:bytes.byteLength,ContentType:type},admission:response.headers.get("X-Nextgo-Offline-Admission")||"",document:response.headers.get("X-Nextgo-Offline-Document")||"",runtimeID:response.headers.get("X-Nextgo-Build")||"",configurationID:response.headers.get("X-Nextgo-Configuration")||"",generation:response.headers.get("X-Nextgo-Offline-Generation")||"",cacheControl:response.headers.get("Cache-Control")||"",vary:response.headers.get("Vary")||""};
}
async function boot(allowNetwork){
 if(exited)throw Error("go-exited");if(bootPromise){if(allowNetwork&&bootFailed&&!vmAttempted&&!bootstrapNetworkUsed&&!repairUsed){repairUsed=true;bootPromise=null;}else return bootPromise;}bootFailed=false;
 bootPromise=(async function(){
  if(await sha(new TextEncoder().encode(D.catalogJSON))!==D.catalogSHA256)throw Error("catalog-integrity");
  const assets=C.Assets.filter(function(a){return a.Kind==="worker-wasm";});if(assets.length!==1)throw Error("worker-inventory");const a=assets[0];let response=null;
  if(!navigator.locks||typeof navigator.locks.request!=="function")throw Error("locking-unavailable");
  const lockAbort=new AbortController(),lockTimer=setTimeout(function(){lockAbort.abort();},10000);try{response=await navigator.locks.request(NS,{mode:"exclusive",signal:lockAbort.signal},async function(){for(const name of await names()){if(name===NS+"-meta")continue;const cache=await caches.open(name);const hit=await cache.match(a.URL);if(hit)return hit;}return null;});}finally{clearTimeout(lockTimer);}
  let bytes=null;if(response){try{if((response.headers.get("Content-Type")||"")!==a.ContentType)throw Error("worker-mime");const candidate=await readBytes(response,a.DecodedBytes);if(candidate.byteLength!==a.DecodedBytes||await sha(candidate)!==a.SHA256)throw Error("worker-integrity");bytes=candidate;}catch(_){if(!allowNetwork)throw Error("worker-integrity");}}
  if(!bytes){if(!allowNetwork||bootstrapNetworkUsed)throw Error("bootstrap-unavailable");bootstrapNetworkUsed=true;bytes=await deadlineFetch(a.URL,async function(r){if((r.headers.get("Content-Type")||"")!==a.ContentType)throw Error("worker-mime");return readBytes(r,a.DecodedBytes);});if(bytes.byteLength!==a.DecodedBytes||await sha(bytes)!==a.SHA256)throw Error("worker-integrity");}
  vmAttempted=true;
  const go=new Go();go.env={GOMAXPROCS:"1"};let readyResolve,readyReject,readyTimer,initExpired=false;const ready=new Promise(function(resolve,reject){readyResolve=resolve;readyReject=reject;});B.register=function(dispatch,resume){if(goDispatch||exited||initExpired)throw Error("duplicate-go");goDispatch=dispatch;goResume=resume;clearTimeout(readyTimer);readyResolve();};
  go.exit=function(){if(go._scheduledTimeouts instanceof Map){for(const timer of go._scheduledTimeouts.values())clearTimeout(timer);go._scheduledTimeouts.clear();}void retire();};
  const instance=await WebAssembly.instantiate(bytes,go.importObject);readyTimer=setTimeout(function(){initExpired=true;readyReject(Error("initialization-timeout"));},30000);const run=go.run(instance.instance);run.then(function(){clearTimeout(readyTimer);go.exit();readyReject(Error("go-exited"));},function(){clearTimeout(readyTimer);go.exit();readyReject(Error("go-exited"));});await ready;
 })().catch(function(error){bootFailed=true;throw error;});return bootPromise;
}
function dispatch(kind,payload,source,allowNetwork,original){return boot(allowNetwork).then(function(){return new Promise(function(resolve,reject){const token=id();pending.set(token,{resolve:resolve,reject:reject});if(original){payload.token=token;originals.set(token,original);}let accepted=false;try{accepted=goDispatch(kind,token,JSON.stringify(payload),source||"")===true;}catch(_){}if(!accepted){pending.delete(token);if(original)originals.delete(token);reject(Error("capacity"));}});});}
B.finish=function(token,text){const p=pending.get(token);if(!p)return;pending.delete(token);try{p.resolve(JSON.parse(text));}catch(_){p.reject(Error("invalid-go-result"));}};
async function activeRole(){
 const active=self.registration.active;if(!active||!["activating","activated"].includes(active.state))return {active:false};
 const nonce=crypto.randomUUID(),channel=new MessageChannel();
 return new Promise(function(resolve){let settled=false;const finish=function(value){if(settled)return;settled=true;clearTimeout(timer);channel.port1.onmessage=null;channel.port1.close();channel.port2.close();resolve({active:value});};const timer=setTimeout(function(){finish(false);},5000);
 channel.port1.onmessage=function(e){const r=e.data;finish(!!r&&r.kind==="nextgo-offline-role"&&r.version===1&&r.nonce===nonce&&r.namespace===NS&&r.scope===D.client.scope&&r.scriptURL===D.client.workerURL&&r.generation===D.generation);};try{active.postMessage({kind:"nextgo-offline-role",version:1,nonce:nonce},[channel.port2]);}catch(_){finish(false);}});
}
async function operation(op,a){
 switch(op){
 case "lock":{if(a.namespace!==NS||!navigator.locks||exited)throw Error("locking-unavailable");return new Promise(function(resolve,reject){const token=crypto.randomUUID();let done;done=navigator.locks.request(NS,{mode:"exclusive"},function(){return new Promise(function(release){locks.set(token,{release:release,done:done});resolve({token:token});});});done.catch(reject);done.finally(function(){locks.delete(token);}).catch(function(){});});}
 case "unlock":{const lock=locks.get(a.token);if(!lock)throw Error("unknown-lock");lock.release();await lock.done;locks.delete(a.token);return {};}
 case "role":return activeRole();
 case "clock":return {now:Math.floor(Date.now()/1000)};
 case "clients":{const controlled=await self.clients.matchAll({type:"window",includeUncontrolled:false}),visible=await self.clients.matchAll({type:"window",includeUncontrolled:true});if(controlled.length>256||visible.length>256)throw Error("client-limit");return {controlled:controlled.length,clientIDs:controlled.map(function(c){return c.id;}),visibleIDs:visible.map(function(c){return c.id;})};}
 case "token":return {token:crypto.randomUUID()};
 case "list":return {names:await names()};
 case "create":if(!ownedName(a.Cache))throw Error("cache-owner");await caches.open(a.Cache);return {};
 case "remove":if(!ownedName(a.Cache))throw Error("cache-owner");await caches.delete(a.Cache);return {};
 case "record":{if(!ownedName(a.Cache)||a.Max>65536)throw Error("record-owner");if(!await caches.has(a.Cache))return {found:false};const hit=await(await caches.open(a.Cache)).match(a.Key);if(!hit)return {found:false};const bytes=await readBytes(hit,a.Max);return {found:true,text:new TextDecoder("utf-8",{fatal:true}).decode(bytes)};}
 case "writeRecord":{if(!ownedName(a.Cache)||typeof a.Text!=="string"||new TextEncoder().encode(a.Text).byteLength>65536)throw Error("record-limit");await(await caches.open(a.Cache)).put(a.Key,new Response(a.Text,{headers:{"Content-Type":"application/json; charset=utf-8","Cache-Control":"no-store"}}));return {};}
 case "keys":{if(!ownedName(a.Cache))throw Error("cache-owner");if(!await caches.has(a.Cache))return {keys:[]};const keys=await(await caches.open(a.Cache)).keys();if(keys.length>162)throw Error("entry-limit");return {keys:keys.map(function(r){return r.url;})};}
 case "readEntry":{if(!ownedName(a.Cache))throw Error("cache-owner");if(!await caches.has(a.Cache))return {found:false};const hit=await(await caches.open(a.Cache)).match(a.URL);if(!hit)return {found:false};return observed(hit,a.URL,a.Max,a.Keep);}
 case "fetchCatalog":{if(a.URL!==D.client.catalogURL||a.Max!==65536)throw Error("catalog-url");return deadlineFetch(a.URL,async function(response){const bytes=await readBytes(response,a.Max);return {text:new TextDecoder("utf-8",{fatal:true}).decode(bytes)};});}
 case "fetchEntry":{const entry=a.Entry;if(!exact.has(entry.URL))throw Error("inventory-url");if(a.Document){if(a.Source!==C.Config.Origin+C.Config.BasePath+"/_nextgo/offline/documents/"+D.generation+"/"+entry.SHA256||!documents.has(entry.URL))throw Error("prepared-source");}else if(a.Source!==entry.URL)throw Error("asset-source");return deadlineFetch(a.Source,function(response){return observed(response,entry.URL,entry.DecodedBytes,true);});}
 case "network":{const original=originals.get(a.Token);if(!original)throw Error("request-token");return networkOriginal(original);}
 case "put":{if(!ownedName(a.Cache))throw Error("cache-owner");const h=handles.get(a.Handle);if(!h)throw Error("handle-missing");try{await(await caches.open(a.Cache)).put(a.URL,new Response(h.bytes,{headers:{"Content-Type":h.type,"Cache-Control":"no-store"}}));}finally{handles.delete(a.Handle);}return {};}
 case "release":handles.delete(a.Handle);return {};
 default:throw Error("unknown-operation");
 }
}
B.perform=function(token,op,args){if(exited)return;let a;try{a=JSON.parse(args);}catch(_){if(goResume&&!exited)goResume(token,"",false);return;}
 const work=track(Promise.resolve().then(function(){return operation(op,a);}));work.then(function(r){if(goResume&&!exited)goResume(token,JSON.stringify(r),true);},function(){if(goResume&&!exited)goResume(token,"",false);});
};
async function networkOriginal(original){
 if(original.started)throw Error("request-already-forwarded");original.started=true;
 const controller=new AbortController();let complete;const bodyDone=new Promise(function(resolve){complete=resolve;});original.bodyDone=track(bodyDone);
 const abort=function(){controller.abort();};original.request.signal.addEventListener("abort",abort,{once:true});if(original.request.signal.aborted)controller.abort();const timer=setTimeout(abort,30000);
 let reader,settled=false,canceling=false,cancelPromise=null;const finish=function(){if(settled)return;settled=true;clearTimeout(timer);original.request.signal.removeEventListener("abort",abort);complete();};
 function cancelBody(reason){if(cancelPromise)return cancelPromise;canceling=true;cancelPromise=Promise.resolve().then(async function(){try{if(reader)await reader.cancel(reason);}catch(_){}finally{if(reader){try{reader.releaseLock();}catch(_){}}finish();}});return cancelPromise;}
 original.cancel=function(){const done=cancelBody();controller.abort();return done;};
 try{const response=await fetch(new Request(original.document?original.request.clone():original.request,original.document?{signal:controller.signal,cache:"no-store"}:{signal:controller.signal}));
  // Navigation fetch uses redirect:manual. Preserve its opaque redirect object
  // instead of attempting to manufacture an impossible status-zero Response.
  if(response.type==="opaqueredirect"||response.type==="opaque"){finish();if(handles.size>=32)throw Error("handle-limit");const handle=++handleSerial;handles.set(handle,{response:response});return {handle:handle};}
  let body=null;
  if(response.body){reader=response.body.getReader();body=new ReadableStream({async pull(sink){try{const part=await reader.read();if(canceling)return;if(part.done){reader.releaseLock();sink.close();finish();}else sink.enqueue(part.value);}catch(error){if(canceling)return;sink.error(error);finish();}},async cancel(reason){await cancelBody(reason);}});}else finish();
  if(handles.size>=32){await cancelBody();throw Error("handle-limit");}const handle=++handleSerial;handles.set(handle,{response:new Response(body,{status:response.status,statusText:response.statusText,headers:response.headers})});return {handle:handle};
 }catch(error){await cancelBody();throw error;}
}

function responseFor(decision){if(decision.kind==="network"){const h=handles.get(decision.handle);if(!h||!h.response)return fallback();handles.delete(decision.handle);return h.response;}if(decision.kind==="cached"){const h=handles.get(decision.handle);if(!h)return fallback();handles.delete(decision.handle);const headers={"Content-Type":h.type,"Cache-Control":"no-store","X-Nextgo-Build":decision.runtimeID,"X-Nextgo-Configuration":decision.configurationID};if(decision.saved){headers["X-Nextgo-Offline-Saved"]="1";headers["X-Nextgo-Offline-Generation"]=decision.generation;}return new Response(h.bytes,{headers:headers});}return fallback();}
function admit(){if(events>=limit||pending.size>=queue||exited)return false;events++;return true;}
function done(){events--;}
self.addEventListener("fetch",function(event){
 const r=event.request,spa=["X-Nextgo-Navigate","X-Nextgo-Action","X-Nextgo-Action-Protocol","X-Nextgo-Partial","X-Nextgo-Protocol","X-Nextgo-Stream","X-Nextgo-Purpose","X-Nextgo-Shell","X-Nextgo-Shell-Protocol","X-Nextgo-Slots","X-Nextgo-Draft"].some(function(name){return r.headers.has(name);});
 // This check precedes admission, boot and respondWith: unsafe and framework
 // protocol requests retain their single ordinary browser network attempt.
 if(r.method!=="GET"||!exact.has(r.url)||r.cache==="no-store"||spa||r.headers.has("Range")||r.headers.has("Authorization")||r.headers.has("Cookie")||documents.has(r.url)&&r.mode!=="navigate")return;
 if(!admit()){event.respondWith(fallback());event.waitUntil(Promise.resolve());return;}
 const original={request:r,document:documents.has(r.url),started:false,bodyDone:null};
 const task=dispatch("fetch",{url:r.url,clientID:event.clientId||"",method:r.method,mode:r.mode,destination:r.destination,cache:r.cache,authorization:false,cookie:false,spa:false,range:false},"",false,original).then(function(d){if(d.kind==="pass")return networkOriginal(original).then(function(reply){return responseFor({kind:"network",handle:reply.handle});});return responseFor(d);},function(){if(!original.started)return networkOriginal(original).then(function(reply){return responseFor({kind:"network",handle:reply.handle});},fallback);return fallback();});
 event.respondWith(task);event.waitUntil(task.then(async function(){if(original.bodyDone)await original.bodyDone;},async function(){if(original.bodyDone)await original.bodyDone;}).finally(function(){for(const pair of originals){if(pair[1]===original)originals.delete(pair[0]);}done();}));
});
self.addEventListener("message",function(event){
 const data=event.data;
 if(data&&data.kind==="nextgo-offline-role"&&data.version===1&&typeof data.nonce==="string"&&data.nonce.length<=64&&event.ports.length===1){event.ports[0].postMessage({kind:"nextgo-offline-role",version:1,nonce:data.nonce,namespace:NS,scope:D.client.scope,scriptURL:self.location.href,generation:D.generation});event.ports[0].close();return;}
 if(event.ports.length!==1||!data||typeof data!=="object"||Array.isArray(data)||Object.keys(data).length>8)return;
 for(const k of Object.keys(data))if(typeof data[k]!=="string"&&typeof data[k]!=="number")return;
 const text=JSON.stringify(data);if(text.length>4096)return;const port=event.ports[0],source=event.source;
 if(!source||source.type!=="window"||typeof source.url!=="string")return;
 if(!admit()){port.close();return;}
 const task=dispatch("message",data,source.url,data.command==="enable"||data.command==="disable").then(function(reply){port.postMessage(reply);port.close();},function(){port.close();}).finally(done);
 event.waitUntil(task);
});
function lifecycle(event,kind){if(!admit()){event.waitUntil(Promise.reject(Error("capacity")));return;}const task=dispatch(kind,{},"",true).then(function(result){if(!result.ok)throw Error("lifecycle-failed");}).finally(done);event.waitUntil(task);}
self.addEventListener("install",function(event){lifecycle(event,"install");});
self.addEventListener("activate",function(event){lifecycle(event,"activate");});
globalThis.__nextgoOfflineBridge=B;
})();
`
