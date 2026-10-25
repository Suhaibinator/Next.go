import {test,expect} from '@playwright/test';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';

const origin='http://127.0.0.1:4240',owned=process.env.NEXTGO_REFRESH_DEV_ROOT,framework=join(owned,'framework');
let source;
const component=(page,id='primary')=>page.locator(`[data-refresh-instance="${id}"]`);
const counter=(page,id='primary')=>component(page,id).locator('[data-refresh-count]');
const proof=(page,id='primary')=>component(page,id).locator('[data-refresh-proof]');
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
async function applied(page,state){await expect(page.locator('body')).toHaveAttribute('data-nextgo-refresh-state','ready');await expect(page.locator('body')).toHaveAttribute('data-nextgo-refresh',String(state.refreshRevision));}
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function inspection(request){const response=await request.get(origin+'/_nextgo/inspect');expect(response.status()).toBe(200);return response.json();}
async function healthy(request,predicate=()=>true){await expect.poll(async()=>{const s=await inspection(request);return s.state==='ready'&&s.serving&&!s.stale&&predicate(s);},{timeout:150_000}).toBe(true);return inspection(request);}
async function native(request){const response=await request.get(origin+'/api/status');expect(response.status()).toBe(200);const result=await response.json();expect(result.instance).toMatch(/^[a-f0-9]{32}$/);return result;}
async function manifest(request,state){const query=new URLSearchParams({build:state.buildId,generation:state.generation});const response=await request.get(origin+'/_nextgo/refresh?'+query);expect(response.status()).toBe(200);const value=await response.json();expect(value.version).toBe(1);expect(value.modules.length).toBeGreaterThan(0);return value;}
async function edit(key,text,request){const before=await inspection(request);await writeFile(join(framework,source.files[key]),text);return healthy(request,s=>s.revision>before.revision);}
function client(version,step=Number(version.slice(1))){return source.originals.counter.replace('const clientProof = "v1"',`const clientProof = "${version}"`).replace('const incrementStep = 1',`const incrementStep = ${step}`);}
async function restored(request){const before=await inspection(request);let changed=false;for(const [key,path]of Object.entries(source.files)){const target=join(framework,path);if(await readFile(target,'utf8')!==source.originals[key]){await writeFile(target,source.originals[key]);changed=true;}}
 const env=join(framework,'examples/refresh/.env.development');if(await readFile(env,'utf8').catch(error=>error.code==='ENOENT'?'':Promise.reject(error))!==''){await writeFile(env,'');changed=true;}
 return healthy(request,s=>!changed||s.revision>before.revision);
}
function documents(page){const items=[],listener=request=>{if(request.isNavigationRequest()&&request.frame()===page.mainFrame())items.push({url:request.url(),method:request.method()});};page.on('request',listener);Object.defineProperty(items,'stop',{value:()=>page.off('request',listener)});return items;}
async function exactlyDocuments(items,count,quiet=1400){await expect.poll(()=>items.length).toBe(count);await pause(quiet);expect(items).toHaveLength(count);}
async function open(page,path='/'){await page.goto(path);await ready(page);if(path.startsWith('/?')||path==='/'){await expect(proof(page)).toHaveText('v1');await expect(proof(page,'secondary')).toHaveText('v1');}}

// Observe the genuine constructor and its exact run promise. This neither
// changes exports nor resolves/replaces the promise used by the host bridge.
async function observe(page){await page.addInitScript(()=>{
 const events=[];let ctor,next=0;const patched=new WeakSet();
 Object.defineProperty(globalThis,'refreshRuntimeEvents',{value:events});
 Object.defineProperty(globalThis,'refreshDocument',{value:crypto.randomUUID()});
 Object.defineProperty(globalThis,'Go',{configurable:true,get(){return ctor;},set(value){ctor=value;if(!value?.prototype||patched.has(value.prototype))return;patched.add(value.prototype);const run=value.prototype.run;value.prototype.run=function(...args){const id=next++,runtime=this;events.push({kind:'start',id});const result=Reflect.apply(run,this,args);result.then(()=>events.push({kind:'settled',id,exitCode:runtime.__nextgoDevExitCode,timers:runtime._scheduledTimeouts instanceof Map?runtime._scheduledTimeouts.size:null}),()=>events.push({kind:'rejected',id,exitCode:runtime.__nextgoDevExitCode,timers:runtime._scheduledTimeouts instanceof Map?runtime._scheduledTimeouts.size:null}));return result;};}});
 });}
async function runtimeEvidence(page){return page.evaluate(()=>globalThis.refreshRuntimeEvents.map(record=>({...record})));}
async function assertRetirement(page,minimumSwaps){await expect.poll(async()=>{const events=await runtimeEvidence(page);return events.filter(event=>event.kind==='settled').length;}).toBeGreaterThanOrEqual(minimumSwaps);const events=await runtimeEvidence(page),starts=events.filter(event=>event.kind==='start');expect(events.filter(event=>event.kind==='rejected')).toEqual([]);expect(starts.length).toBe(2+minimumSwaps);for(let n=2;n<starts.length;n++){const retired=starts[n-1].id;const end=events.findIndex(event=>event.kind==='settled'&&event.id===retired);const begin=events.findIndex(event=>event===starts[n]);expect(end).toBeGreaterThan(-1);expect(end).toBeLessThan(begin);expect(events[end].timers).toBe(0);expect(events[end].exitCode).toBe(0);}return events;}

test.beforeAll(async()=>{source=JSON.parse(await readFile(join(owned,'originals.json'),'utf8'));expect(source.originals.counter).toContain('const clientProof = "v1"');expect(source.originals.counter).toContain('const incrementStep = 1');});
test.beforeEach(async({request,page})=>{await restored(request);await observe(page);});
test.afterEach(async({request},info)=>{await info.attach('refresh-dev-inspection',{body:JSON.stringify(await inspection(request)),contentType:'application/json'});});

test('cold SSR stays useful and lazy Go hydration preserves the existing counter nodes',async({page})=>{
 let entered=false,release;const held=new Promise(resolve=>{release=resolve;});
 await page.route('**/module-*.wasm',async route=>{entered=true;await held;await route.continue();});
 try{await page.goto('/',{waitUntil:'domcontentloaded'});await expect.poll(()=>entered).toBe(true);await expect(counter(page)).toHaveText('3');await expect(counter(page,'secondary')).toHaveText('10');await expect(page.locator('[data-refresh-intro]')).toHaveText('Public state, explicit instance identity.');await expect(component(page).locator('[data-refresh-increment]')).toBeDisabled();
  await page.evaluate(()=>{window.beforeRefreshRoot=document.querySelector('[data-refresh-instance="primary"]');window.beforeRefreshCount=window.beforeRefreshRoot.querySelector('[data-refresh-count]');window.beforeRefreshInput=window.beforeRefreshRoot.querySelector('[data-refresh-label]');});
  await component(page).locator('[data-refresh-label]').fill('Draft before WASM');release();await expect(proof(page)).toHaveText('v1');
  expect(await page.evaluate(()=>window.beforeRefreshRoot===document.querySelector('[data-refresh-instance="primary"]')&&window.beforeRefreshCount===document.querySelector('[data-refresh-instance="primary"] [data-refresh-count]')&&window.beforeRefreshInput===document.querySelector('[data-refresh-instance="primary"] [data-refresh-label]'))).toBe(true);
  await expect(component(page).locator('[data-refresh-label]')).toHaveValue('Draft before WASM');await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('4');
 }finally{release();await page.unroute('**/module-*.wasm');}
});

test('actual compiled Go replacements retain two states, DOM, focus, URL and native process while retiring each old runtime',async({page,request},info)=>{
 await open(page,'/?draft=public#counter');const loads=documents(page),before=await inspection(request),processBefore=await native(request),url=page.url(),token=await page.evaluate(()=>refreshDocument);
 await component(page).locator('[data-refresh-increment]').click();await component(page,'secondary').locator('[data-refresh-increment]').click();await component(page,'secondary').locator('[data-refresh-increment]').click();
 const input=component(page).locator('[data-refresh-label]');await input.fill('Keep this draft');await component(page,'secondary').locator('[data-refresh-enabled]').uncheck();await input.focus();
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));const history=await page.evaluate(()=>({length:history.length,state:JSON.stringify(history.state)}));
 await page.evaluate(()=>{window.keptRefreshNodes=[...document.querySelectorAll('[data-refresh-instance], [data-refresh-count], [data-refresh-label], [data-refresh-increment]')];});
 const initialManifest=await manifest(request,before);
 for(const [version,step,expected]of [['v2',2,'6'],['v3',3,'9']]){
  const current=await edit('counter',client(version,step),request);expect(current.buildId).toBe(before.buildId);expect(current.generation).toBe(before.generation);await expect(proof(page)).toHaveText(version);await expect(proof(page,'secondary')).toHaveText(version);await applied(page,current);
  expect(await page.evaluate(()=>{const current=[...document.querySelectorAll('[data-refresh-instance], [data-refresh-count], [data-refresh-label], [data-refresh-increment]')];return window.keptRefreshNodes.length===current.length&&window.keptRefreshNodes.every((node,index)=>node.isConnected&&node===current[index]);})).toBe(true);
  await expect(input).toBeFocused();await expect(input).toHaveValue('Keep this draft');await expect(counter(page,'secondary')).toHaveText('12');await expect(component(page,'secondary').locator('[data-refresh-increment]')).toBeDisabled();
  // Synthetic dispatch avoids Playwright moving focus; the handler is Go code.
  await component(page).locator('[data-refresh-increment]').dispatchEvent('click');await expect(counter(page)).toHaveText(expected);
 }
 await exactlyDocuments(loads,0);expect(page.url()).toBe(url);expect(await page.evaluate(()=>({length:history.length,state:JSON.stringify(history.state)}))).toEqual(history);expect(await page.evaluate(()=>refreshDocument)).toBe(token);const processAfter=await native(request);expect(processAfter.instance).toBe(processBefore.instance);expect(processAfter.requests).toBeGreaterThan(processBefore.requests);
 const latest=await manifest(request,await inspection(request));expect(latest.revision).toBe(initialManifest.revision+2);expect(latest.modules[0].digest).not.toBe(initialManifest.modules[0].digest);
 await info.attach('actual-go-runtime-retirement',{body:JSON.stringify(await assertRetirement(page,2)),contentType:'application/json'});
});

test('an unused opted lazy module is not downloaded on an unrelated page and navigation loads its latest code',async({page,request})=>{
 const modules=[];page.on('request',r=>{if(/\/module-[a-f0-9]+\.wasm/.test(r.url()))modules.push(r.url());});await open(page,'/details');const loads=documents(page),token=await page.evaluate(()=>refreshDocument),before=await inspection(request);expect(modules).toEqual([]);
 const after=await edit('counter',client('v2'),request);expect(after.generation).toBe(before.generation);await applied(page,after);await pause(1400);expect(modules).toEqual([]);expect(loads).toEqual([]);
 await page.getByRole('link',{name:'Return to stateful counters',exact:true}).click();await expect(proof(page)).toHaveText('v2');expect(modules).toHaveLength(1);expect(await page.evaluate(()=>refreshDocument)).toBe(token);await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('5');
});

test('a real Go compile error retains the last good view and the next valid edit recovers without discarding state',async({page,request})=>{
 await open(page);await component(page).locator('[data-refresh-increment]').click();const loads=documents(page),before=await inspection(request),server=await native(request);
 await writeFile(join(framework,source.files.counter),client('v2')+'\nthis is invalid Go\n');await expect.poll(async()=>{const s=await inspection(request);return s.state==='failed'&&s.serving&&s.stale&&s.buildId===before.buildId&&s.generation===before.generation;}).toBe(true);await expect(page.locator('#nextgo-status')).toContainText('Build failed:');
 await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('5');await exactlyDocuments(loads,0);expect((await native(request)).instance).toBe(server.instance);
 const recovered=await edit('counter',client('v3'),request);await expect(proof(page)).toHaveText('v3');await applied(page,recovered);await expect(page.locator('#nextgo-status')).not.toContainText('Build failed:');await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('8');await exactlyDocuments(loads,0);
});

for(const fault of ['network abort','actual WASM digest corruption'])test(`one-shot candidate ${fault} causes one full reload that boots the latest genuine module`,async({page,request})=>{
 await open(page);await component(page).locator('[data-refresh-increment]').click();const loads=documents(page),server=await native(request),before=await inspection(request),baseline=await manifest(request,before),oldURLs=new Set(baseline.modules.map(module=>new URL(module.url,origin).href));let faults=0;
 await page.route('**/module-*.wasm',async route=>{if(oldURLs.has(route.request().url())||faults){await route.continue();return;}faults++;if(fault==='network abort'){await route.abort('failed');return;}const actual=await route.fetch();const bytes=Buffer.from(await actual.body());expect(bytes.length).toBeGreaterThan(8);bytes[bytes.length-1]^=1;await route.fulfill({response:actual,body:bytes});});
 try{await edit('counter',client('v2'),request);await expect(proof(page)).toHaveText('v2');await exactlyDocuments(loads,1,6200);expect(faults).toBe(1);await expect(counter(page)).toHaveText('3');await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('5');expect((await native(request)).instance).toBe(server.instance);}finally{await page.unroute('**/module-*.wasm');}
});

test('a real missing state claim rejects warm restoration and reloads once with the new explicit state cell',async({page,request})=>{
 await open(page);const loads=documents(page),server=await native(request);const incompatible=client('v2').replace('client.NewRefreshState(scope, "count", p.Initial)','client.NewRefreshState(scope, "count-next", p.Initial)');expect(incompatible).not.toBe(client('v2'));
 await edit('counter',incompatible,request);await expect(proof(page)).toHaveText('v2');await exactlyDocuments(loads,1,6200);await expect(counter(page)).toHaveText('3');await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('5');expect((await native(request)).instance).toBe(server.instance);
});

test('an actual unsupported pointer cell rejects old-runtime preparation instead of migrating arbitrary application state',async({page,request})=>{
 await open(page);await component(page).locator('[data-refresh-increment]').click();const initialLoads=documents(page),initialServer=await native(request);const invalidCell=text=>text.replace('scope.On(increment, "click", func(client.Event) {','scope.On(increment, "click", func(client.Event) {\n\t\t_, _ = client.NewRefreshState(scope, "unsupported", (*int)(nil))');
 const poisoned=invalidCell(client('v2'));expect(poisoned).not.toBe(client('v2'));await edit('counter',poisoned,request);await expect(proof(page)).toHaveText('v2');await exactlyDocuments(initialLoads,0);initialLoads.stop();expect((await native(request)).instance).toBe(initialServer.instance);
 // The real Go handler attempts a type the public refresh API rejects. Ignoring
 // that error is deliberate adversarial application code, confined to the copy.
 await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('6');const loads=documents(page),server=await native(request);
 await edit('counter',invalidCell(client('v3')),request);await expect(proof(page)).toHaveText('v3');await exactlyDocuments(loads,1,6200);await expect(counter(page)).toHaveText('3');expect((await native(request)).instance).toBe(server.instance);await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('6');
});

test('native, shared, props, schema, eager, core and environment edits take the full rebuild path',async({page,request},info)=>{
 test.setTimeout(900_000);await open(page);const evidence=[];
 const boundaries=[['native',source.originals.native.replace('"native-v1"','"native-v2"')],['shared',source.originals.shared.replace('Public state, explicit instance identity.','Changed shared Go implementation.')],['props',source.originals.props.replace('\n}', '\n\tFixtureRevision string `json:"fixtureRevision"`\n}')],['schema',source.originals.schema.replaceAll('counter/v1','counter/v2')],['eager',source.originals.eager.replace('eager-v1','eager-v2')],['core',source.originals.core+'\n// Actual framework core rebuild proof.\n']];
 for(const [key,text]of boundaries){expect(text).not.toBe(source.originals[key]);const loads=documents(page),before=await inspection(request),server=await native(request),token=await page.evaluate(()=>refreshDocument);await component(page).locator('[data-refresh-increment]').click();const after=await edit(key,text,request);expect(after.generation).not.toBe(before.generation);await ready(page);await expect(proof(page)).toHaveText('v1');await exactlyDocuments(loads,1);expect(await page.evaluate(()=>refreshDocument)).not.toBe(token);const currentNative=await native(request);expect(currentNative.instance).not.toBe(server.instance);if(key==='native')expect(currentNative.source).toBe('native-v2');if(key==='shared'){await expect(page.locator('[data-refresh-intro]')).toHaveText('Changed shared Go implementation.');await expect(component(page).locator('[data-refresh-shared]')).toHaveText('Changed shared Go implementation.');}if(key==='eager')await expect(page.locator('[data-refresh-eager]')).toHaveText('eager-v2');await expect(counter(page)).toHaveText('3');evidence.push({boundary:key,buildId:after.buildId,generation:after.generation});loads.stop();}
 const loads=documents(page),before=await inspection(request),server=await native(request);await writeFile(join(framework,'examples/refresh/.env.development'),'NEXTGO_REFRESH_PUBLIC_NOTE="Changed public development note"\n');const after=await healthy(request,s=>s.revision>before.revision&&s.generation!==before.generation);await expect(page.locator('[data-refresh-public-note]')).toHaveText('Changed public development note');await exactlyDocuments(loads,1);expect((await native(request)).instance).not.toBe(server.instance);evidence.push({boundary:'environment',buildId:after.buildId,generation:after.generation});await info.attach('actual-full-rebuild-boundaries',{body:JSON.stringify(evidence),contentType:'application/json'});
});

test('a genuine managed Fetch still in flight forces one document reload instead of a warm replacement',async({page,request})=>{
 await open(page);const loads=documents(page),requests=[];page.on('request',r=>{if(new URL(r.url()).pathname==='/api/work')requests.push({method:r.method(),url:r.url()});});let release;const gate=new Promise(resolve=>{release=resolve;});let routed=false;
 await page.route('**/api/work?*',async route=>{routed=true;const actual=await route.fetch();await gate;try{await route.fulfill({response:actual});}catch(error){if(!page.isClosed()&&!/disposed|closed|canceled|cancelled|intercepted/i.test(String(error)))throw error;}});
 try{await component(page).locator('[data-refresh-work]').click();await expect(component(page).locator('[data-refresh-work-status]')).toHaveAttribute('data-state','loading');await expect.poll(()=>routed).toBe(true);await edit('counter',client('v2'),request);await expect(proof(page)).toHaveText('v2');await exactlyDocuments(loads,1,6200);await expect(counter(page)).toHaveText('3');expect(requests).toHaveLength(1);expect(requests[0].method).toBe('GET');await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('5');}finally{release();await page.unrouteAll({behavior:'wait'});}
});

test('effective alternate AssetPrefix disables warm refresh and a module edit rebuilds the native process once',async({page,request})=>{
 const before=await inspection(request);await writeFile(join(framework,'examples/refresh/.env.development'),'NEXTGO_REFRESH_ASSET_PREFIX="/refresh-assets"\n');const configured=await healthy(request,s=>s.revision>before.revision&&s.generation!==before.generation);expect(configured.refreshEnabled).toBe(false);
 const refreshRequests=[],wasmRequests=[];page.on('request',r=>{const path=new URL(r.url()).pathname;if(path==='/_nextgo/refresh')refreshRequests.push(r.url());if(path.endsWith('.wasm'))wasmRequests.push(path);});
 await open(page);await expect(page.locator('body')).not.toHaveAttribute('data-nextgo-refresh-state',/.+/);await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('4');const loads=documents(page),server=await native(request),token=await page.evaluate(()=>refreshDocument);
 const after=await edit('counter',client('v2'),request);expect(after.refreshEnabled).toBe(false);expect(after.generation).not.toBe(configured.generation);await expect(proof(page)).toHaveText('v2');await exactlyDocuments(loads,1,6200);expect(await page.evaluate(()=>refreshDocument)).not.toBe(token);expect((await native(request)).instance).not.toBe(server.instance);await expect(counter(page)).toHaveText('3');await component(page).locator('[data-refresh-increment]').click();await expect(counter(page)).toHaveText('5');
 expect(refreshRequests).toEqual([]);expect(wasmRequests.length).toBeGreaterThanOrEqual(4);expect(wasmRequests.every(path=>path.startsWith('/refresh-assets/'))).toBe(true);
 const disabled=await request.get(origin+'/_nextgo/refresh?'+new URLSearchParams({build:after.buildId,generation:after.generation}));expect(disabled.status()).toBe(204);expect(await disabled.body()).toHaveLength(0);
});
