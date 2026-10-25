//go:build !js

package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This isolated bridge test uses actual promises, Response bodies and hashing;
// browser lifecycle/CacheStorage qualification remains a separate requirement.
func TestBridgeAdmissionAndPhysicalExit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the isolated bridge promise test")
	}
	body := []byte("worker-fixture")
	digest := sha256.Sum256(body)
	catalog := map[string]any{"Config": map[string]any{"Origin": "https://example.test", "BasePath": "/reader", "Budget": map[string]any{"InFlight": 1, "Queue": 1}}, "Assets": []any{map[string]any{"URL": "https://example.test/reader/_nextgo/aaaaaaaaaaaaaaaaaaaa/worker-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.wasm", "Kind": "worker-wasm", "SHA256": hex.EncodeToString(digest[:]), "DecodedBytes": len(body), "ContentType": "application/wasm"}}, "Documents": []any{map[string]any{"Path": "/"}}}
	raw, _ := json.Marshal(catalog)
	catalogDigest := sha256.Sum256(raw)
	descriptor := map[string]any{"catalogJSON": string(raw), "catalogSHA256": hex.EncodeToString(catalogDigest[:]), "namespace": "nextgo-offline-v1-" + strings.Repeat("a", 64), "generation": strings.Repeat("c", 64), "client": map[string]any{"scope": "https://example.test/reader", "workerURL": "https://example.test/reader/_nextgo/offline/sw.js", "catalogURL": "https://example.test/reader/_nextgo/offline/catalog"}}
	encoded, _ := json.Marshal(descriptor)
	if strings.Count(ScriptTemplate, "__NEXTGO_OFFLINE_SHIM__") != 1 || strings.Count(ScriptTemplate, "__NEXTGO_OFFLINE_DESCRIPTOR__") != 1 {
		t.Fatal("build placeholders must each occur exactly once")
	}
	script := strings.Replace(ScriptTemplate, "__NEXTGO_OFFLINE_SHIM__", "", 1)
	script = strings.Replace(script, "__NEXTGO_OFFLINE_DESCRIPTOR__", string(encoded), 1)
	input, _ := json.Marshal(map[string]any{"script": script, "descriptor": descriptor, "body": string(body)})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", bridgePromiseTest)
	cmd.Stdin = bytes.NewReader(input)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bridge promise ownership: %v\n%s", err, output)
	}
}

const bridgePromiseTest = `
import vm from 'node:vm';
import {webcrypto} from 'node:crypto';
import assert from 'node:assert/strict';
let input='';for await(const chunk of process.stdin)input+=chunk;
const {script,descriptor:D,body}=JSON.parse(input),C=JSON.parse(D.catalogJSON),listeners=new Map(),resumes=[];
let held=0,releaseWrite=null,goInstance=null,deferWrite=false,fetches=0,liveTransport=null;
const stores=new Map();
function cache(name){if(!stores.has(name))stores.set(name,new Map());const entries=stores.get(name);return {async match(key){const r=entries.get(key);return r?r.clone():undefined;},async keys(){return [...entries.keys()].map(url=>({url}));},async put(key,response){if(deferWrite){await new Promise(resolve=>{releaseWrite=resolve;});}entries.set(key,response.clone());}};}
const ns=D.namespace,gen=ns+'-g-'+D.generation;cache(gen);stores.get(gen).set(C.Assets[0].URL,new Response(body,{headers:{'Content-Type':'application/wasm'}}));
const cacheStorage={async keys(){return [...stores.keys()];},async has(name){return stores.has(name);},async open(name){return cache(name);},async delete(name){return stores.delete(name);}};
const jobs=[];const port=()=>({postMessage(){},close(){}});
const self={location:{href:D.client.workerURL},registration:{active:null},clients:{async matchAll(){return [];}},addEventListener(name,fn){listeners.set(name,fn);}};
let context;
class Go{constructor(){goInstance=this;this.env={};this.importObject={};this._scheduledTimeouts=new Map();}run(){context.__nextgoOfflineBridge.register(()=>false,(...args)=>resumes.push(args));return new Promise(()=>{});}}
context=vm.createContext({self,navigator:{locks:{request(name,options,fn){return Promise.resolve().then(()=>{held++;return fn();}).finally(()=>held--);}}},caches:cacheStorage,crypto:webcrypto,Go,WebAssembly:{async instantiate(){return {instance:{}};}},Response,Request,Headers,ReadableStream,TextEncoder,TextDecoder,AbortController,MessageChannel,setTimeout,clearTimeout,console,fetch:async(request)=>{fetches++;if(liveTransport)return liveTransport(request);throw Error('offline');}});
vm.runInContext(script,context);
function request(overrides={}){return {url:'https://example.test/reader',method:'GET',mode:'navigate',destination:'document',cache:'default',headers:new Headers(),...overrides};}
// Unsafe/protocol/neighbour requests must bypass BEFORE any boot or interception.
for(const r of [request({method:'POST'}),request({method:'HEAD'}),request({cache:'no-store'}),request({url:'https://example.test/reader2'}),request({url:'https://example.test/reader?secret=1'}),request({mode:'cors'}),request({headers:new Headers({'X-Nextgo-Navigate':'1'})}),request({headers:new Headers({'X-Nextgo-Action-Protocol':'1'})}),request({headers:new Headers({'Range':'bytes=0-1'})}),request({headers:new Headers({'Authorization':'secret'})})]){
 let intercepted=false,extended=false;listeners.get('fetch')({request:r,respondWith(){intercepted=true;},waitUntil(){extended=true;}});assert.equal(intercepted,false);assert.equal(extended,false);
}
assert.equal(goInstance,null);assert.equal(fetches,0);
// An admitted message owns a synchronous waitUntil through asynchronous boot.
let extended=false;listeners.get('message')({data:{version:1,command:'status'},source:{type:'window',url:'https://example.test/reader'},ports:[port()],waitUntil(p){extended=true;jobs.push(p);}});assert.equal(extended,true);
await Promise.allSettled(jobs);assert.ok(goInstance);assert.equal(held,0);
const bridge=context.__nextgoOfflineBridge;
// A failed Go admission delegates only the original safe GET. Stream ownership
// remains charged until the browser consumes its body; headers stay intact.
let liveRequest=null;
liveTransport=async r=>{liveRequest=r;return new Response('live-native',{headers:{'X-Fixture':'native'}});};
async function fetchEvent(request){let response,completion;listeners.get('fetch')({request,clientId:'view',respondWith(p){response=p;},waitUntil(p){completion=p;}});return {response:await response,completion};}
const live=await fetchEvent(new Request(C.Assets[0].URL,{headers:{'X-Original':'preserved'},credentials:'include'}));
assert.equal(live.response.headers.get('X-Fixture'),'native');assert.equal(await live.response.text(),'live-native');await live.completion;assert.equal(liveRequest.headers.get('X-Original'),'preserved');assert.equal(liveRequest.credentials,'include');
// Navigation manual redirects cannot be reconstructed as status-zero responses.
const opaque=Response.redirect('https://example.test/reader',302);Object.defineProperty(opaque,'type',{value:'opaqueredirect'});liveTransport=async()=>opaque;
const redirect=await fetchEvent(new Request(C.Assets[0].URL));assert.equal(redirect.response,opaque);await redirect.completion;
async function until(fn){for(let n=0;n<100&&!fn();n++)await new Promise(r=>setTimeout(r,1));assert.ok(fn(),'bounded promise observation');}
bridge.perform('lock','lock',JSON.stringify({namespace:ns}));await until(()=>resumes.some(r=>r[0]==='lock'));assert.equal(held,1);
deferWrite=true;bridge.perform('write','writeRecord',JSON.stringify({Cache:ns+'-meta',Key:'https://example.test/record',Text:'{}'}));await until(()=>releaseWrite!==null);
goInstance.exit(1);await new Promise(r=>setTimeout(r,5));assert.equal(held,1,'exit must retain the lock through physical cache completion');
releaseWrite();await until(()=>held===0);assert.equal(resumes.some(r=>r[0]==='write'),false,'no Go callbacks after exit');
assert.equal(fetches,2);
`
