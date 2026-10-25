import {test,expect} from '@playwright/test';
import {readFile} from 'node:fs/promises';
import {join} from 'node:path';
import {publicOrigin,ports,sha,readBounded} from './independent-zones-fixture.mjs';

const owned=process.env.NEXTGO_INDEPENDENT_ZONES_ROOT;
let fixture,controlInfo;
const home=zone=>zone==='blog'?'/blog':'/';
const api=zone=>zone==='blog'?'/blog/api/state':'/api/state';
const action=zone=>(zone==='blog'?'/blog':'')+'/_nextgo/actions/preview/v1';
const island=(page,id='primary')=>page.locator(`[data-independent-instance="${id}"]`);
const count=(page,id='primary')=>island(page,id).locator('[data-independent-count]');
const proof=(page,id='primary')=>island(page,id).locator('[data-independent-proof]');
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function control(request,input){const response=await request[input?'post':'get'](controlInfo.url,{headers:{'X-Fixture-Key':controlInfo.key},...(input?{data:input}:{})});expect(response.status()).toBe(200);return response.json();}
async function state(request,zone='alpha',options={}){const response=await request.get(publicOrigin+api(zone),options);expect(response.status()).toBe(200);const value=await response.json();expect(value.zone).toBe(zone);expect(value.instance).toMatch(/^[a-f0-9]{32}$/);return value;}
async function ready(page,zone='alpha',version=zone==='blog'?'B1':'A1'){await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');await expect(page.locator(`[data-independent-zone="${zone}"]`)).toBeVisible();await expect(proof(page)).toHaveText(version);await expect(proof(page,'secondary')).toHaveText(version);}
async function open(page,zone='alpha',version){await page.goto(home(zone));await ready(page,zone,version);}
async function doc(page){return page.evaluate(()=>window.independentDocument);}
function documents(page){const records=[];page.on('request',request=>{if(request.isNavigationRequest()&&request.frame()===page.mainFrame())records.push({path:new URL(request.url()).pathname,method:request.method()});});return records;}
async function bytes(request,url){const response=await request.get(url,{headers:{'Accept-Encoding':'identity'}});expect(response.status()).toBe(200);const value=await response.body();expect(value.length).toBeLessThanOrEqual(8<<20);return {url:response.url(),bytes:value.length,sha256:sha(value),data:value};}
async function typed(page,request,zone,version){const before=await state(request,zone);await island(page).locator('[data-independent-call]').click();await expect(island(page).locator('[data-independent-status]')).toHaveText('Public preview confirmed. Nothing durable was saved.');const data=JSON.parse(await island(page).locator('[data-independent-action-result]').textContent());expect(data).toEqual({zone,nativeVersion:version,instance:before.instance,revision:before.revision+1,applied:1});expect((await state(request,zone)).revision).toBe(before.revision+1);return data;}
async function reset(request){return control(request,{snapshot:'I1',relay:'pass',dropNext:false,clear:true});}

test.beforeAll(async()=>{fixture=JSON.parse(await readFile(join(owned,'fixture.json'),'utf8'));controlInfo=JSON.parse(await readFile(join(owned,'control.json'),'utf8'));});
test.beforeEach(async({page,request})=>{await reset(request);await page.addInitScript(()=>{Object.defineProperty(window,'independentDocument',{value:crypto.randomUUID()});});});
test.afterEach(async({request},info)=>{await info.attach('independent-real-wire-metadata',{body:JSON.stringify(await control(request)),contentType:'application/json'});});

test('A1 SSR is useful before lazy WASM and hydration preserves both existing counter roots',async({page})=>{
 let entered=false,release;const held=new Promise(resolve=>{release=resolve;});await page.route('**/module-*.wasm',async route=>{entered=true;await held;await route.continue();});
 try{expect((await page.goto('/',{waitUntil:'domcontentloaded'})).status()).toBe(200);await expect.poll(()=>entered).toBe(true);await expect(page).toHaveTitle('alpha independent release');await expect(page.locator('[data-independent-native]')).toHaveText('alpha-v1');await expect(count(page)).toHaveText('3');await expect(count(page,'secondary')).toHaveText('10');await expect(island(page).locator('[data-independent-increment]')).toBeDisabled();
  await page.evaluate(()=>{window.independentInitial=[...document.querySelectorAll('[data-independent-instance], [data-independent-count], [data-independent-increment]')];});release();await ready(page);
  expect(await page.evaluate(()=>{const now=[...document.querySelectorAll('[data-independent-instance], [data-independent-count], [data-independent-increment]')];return now.length===window.independentInitial.length&&now.every((node,i)=>node===window.independentInitial[i]);})).toBe(true);
  await island(page).locator('[data-independent-increment]').click();await expect(count(page)).toHaveText('4');await island(page,'secondary').locator('[data-independent-increment]').click();await expect(count(page,'secondary')).toHaveText('11');await expect(count(page)).toHaveText('4');
 }finally{release();await page.unrouteAll({behavior:'wait'});}
});

test('alpha same-zone navigation retains the document/layout and restores query/history while direct URLs render independently',async({page})=>{
 await page.goto('/?draft=public');await ready(page);const token=await doc(page),loads=documents(page);await page.evaluate(()=>{window.independentLayout=document.querySelector('[data-independent-layout="alpha"]');});
 await page.getByRole('link',{name:'alpha about',exact:true}).click();await expect(page.locator('[data-independent-about="alpha"]')).toBeVisible();await expect(page).toHaveTitle('alpha release ownership');expect(await doc(page)).toBe(token);expect(await page.evaluate(()=>window.independentLayout===document.querySelector('[data-independent-layout="alpha"]'))).toBe(true);
 await page.goBack();await ready(page);expect(new URL(page.url()).search).toBe('?draft=public');await page.goForward();await expect(page.locator('[data-independent-about="alpha"]')).toBeVisible();expect(loads).toEqual([]);
 expect((await page.goto('/about?direct=public')).status()).toBe(200);await expect(page).toHaveTitle('alpha release ownership');expect(await doc(page)).not.toBe(token);
});

test('blog dynamic loaders support natural Go links, query values, titles, history and direct access without a SPA reload',async({page})=>{
 await open(page,'blog');const token=await doc(page),loads=documents(page);await page.getByRole('link',{name:'Read the welcome note',exact:true}).click();await expect(page.locator('[data-independent-post="welcome"]')).toBeVisible();await expect(page.locator('[data-independent-query]')).toHaveText('home');await expect(page).toHaveTitle('Blog: welcome');expect(new URL(page.url()).search).toBe('?source=home');expect(await doc(page)).toBe(token);
 await page.goBack();await ready(page,'blog');await page.getByRole('link',{name:'Read the ownership note',exact:true}).click();await expect(page).toHaveTitle('Blog: ownership');await expect(page.locator('[data-independent-post="ownership"]')).toBeVisible();expect(loads).toEqual([]);
 expect((await page.goto('/blog/posts/welcome?source=direct')).status()).toBe(200);await expect(page.locator('[data-independent-query]')).toHaveText('direct');await expect(page).toHaveTitle('Blog: welcome');expect(await doc(page)).not.toBe(token);
});

test('cross-zone intent avoids peer prefetch and links load a new document and its real Go bundle',async({page})=>{
 await open(page);await island(page).locator('[data-independent-increment]').click();const token=await doc(page),requests=[];page.on('request',request=>{if(new URL(request.url()).pathname==='/blog')requests.push({navigation:request.isNavigationRequest(),headers:request.headers()});});
 await page.locator('[data-independent-cross]').hover();await pause(600);expect(requests).toEqual([]);await page.locator('[data-independent-cross]').click();await ready(page,'blog');expect(await doc(page)).not.toBe(token);await expect(count(page)).toHaveText('3');expect(requests).toHaveLength(1);expect(requests[0].navigation).toBe(true);expect(requests[0].headers['x-nextgo-navigate']).toBeUndefined();
 const blogToken=await doc(page);await island(page).locator('[data-independent-increment]').click();await expect(count(page)).toHaveText('4');await page.locator('[data-independent-cross]').click();await ready(page);expect(await doc(page)).not.toBe(blogToken);await expect(count(page)).toHaveText('3');
});

test('real A1 to A2 cutover and rollback keep B1 binary, native process and live browser state unchanged',async({page,context,request},info)=>{
 const peer=await context.newPage();try{await open(page);await peer.goto(publicOrigin+'/blog');await ready(peer,'blog');await island(peer).locator('[data-independent-increment]').click();await island(peer).locator('[data-independent-increment]').click();const peerToken=await doc(peer),peerLoads=documents(peer),B1=await state(request,'blog'),binaryBefore=sha(await readBounded(fixture.binaryPaths.B1,256<<20));expect(binaryBefore).toBe(fixture.hashes.B1);await peer.evaluate(()=>{window.peerCounter=document.querySelector('[data-independent-instance="primary"]');});
  const A1=await state(request);await control(request,{snapshot:'I2'});expect((await state(request)).nativeVersion).toBe('alpha-v2');await page.goto('/');await ready(page,'alpha','A2');await island(page).locator('[data-independent-increment]').click();await expect(count(page)).toHaveText('5');
  await expect(count(peer)).toHaveText('5');expect(await doc(peer)).toBe(peerToken);expect(await peer.evaluate(()=>window.peerCounter===document.querySelector('[data-independent-instance="primary"]'))).toBe(true);expect(peerLoads).toEqual([]);expect((await state(request,'blog')).instance).toBe(B1.instance);await typed(peer,request,'blog','blog-v1');
  await control(request,{snapshot:'rollback'});await page.goto('/');await ready(page);expect((await state(request)).instance).toBe(A1.instance);await island(page).locator('[data-independent-increment]').click();await expect(count(page)).toHaveText('4');await expect(count(peer)).toHaveText('5');expect((await state(request,'blog')).instance).toBe(B1.instance);expect(sha(await readBounded(fixture.binaryPaths.B1,256<<20))).toBe(binaryBefore);expect(peerLoads).toEqual([]);
  await info.attach('unchanged-peer-artifact',{body:JSON.stringify({binarySHA256:binaryBefore,instanceRetained:true,liveDocumentRetained:true}),contentType:'application/json'});
 }finally{await peer.close();}
});

test('old A1 lazy WASM, CSS and public bytes stay pinned after A2 cutover and retained A2 assets survive rollback',async({page,request})=>{
 let entered=false,release,heldURL;const held=new Promise(resolve=>{release=resolve;});await page.route('**/module-*.wasm',async route=>{entered=true;heldURL=route.request().url();await held;await route.continue();});
 try{await page.goto('/',{waitUntil:'domcontentloaded'});await expect.poll(()=>entered).toBe(true);const initial=await page.evaluate(()=>({css:[...document.querySelectorAll('link[rel="stylesheet"]')].map(node=>node.href),asset:document.querySelector('[data-independent-asset]').href}));const old=[await bytes(request,heldURL),await bytes(request,initial.asset),...await Promise.all(initial.css.map(url=>bytes(request,url)))];expect(old.every(value=>new URL(value.url).pathname.startsWith(fixture.releases.A1.release.assetPrefix+'/'))).toBe(true);
  await control(request,{snapshot:'I2'});release();await ready(page);await expect(page.locator('[data-independent-native]')).toHaveText('alpha-v1');await island(page).locator('[data-independent-increment]').click();await expect(count(page)).toHaveText('4');for(const value of old)expect((await bytes(request,value.url)).sha256).toBe(value.sha256);
  const A2asset=await bytes(request,publicOrigin+fixture.releases.A2.release.assetPrefix+'/assets/release.txt');expect(A2asset.data.toString()).toBe('alpha original asset A2\n');expect(A2asset.sha256).not.toBe(old[1].sha256);await control(request,{snapshot:'rollback'});expect((await bytes(request,A2asset.url)).sha256).toBe(A2asset.sha256);for(const value of old)expect((await bytes(request,value.url)).sha256).toBe(value.sha256);
  expect((await request.get(publicOrigin+'/_nextgo/releases/unknown/assets/release.txt')).status()).toBe(404);const wrong=publicOrigin+fixture.releases.A2.release.assetPrefix+'/_nextgo/'+fixture.releases.A1.release.runtimeID+'/app.wasm';expect((await request.get(wrong)).status()).toBe(404);
 }finally{release();await page.unrouteAll({behavior:'wait'});}
});

test('direct custom 404s belong to their selected zone and never fall through to the alpha parent',async({page,request})=>{
 expect((await page.goto('/blog/posts/missing')).status()).toBe(404);await expect(page.locator('[data-independent-not-found="blog"]')).toBeVisible();await expect(page).toHaveTitle('blog route not found');await expect(page.locator('[data-independent-not-found="alpha"]')).toHaveCount(0);
 expect((await page.goto('/missing')).status()).toBe(404);await expect(page.locator('[data-independent-not-found="alpha"]')).toBeVisible();await expect(page).toHaveTitle('alpha route not found');expect((await request.get(publicOrigin+'/blog/api/missing')).status()).toBe(404);expect((await request.get(publicOrigin+'/blog/_nextgo/releases/unknown/assets/release.txt')).status()).toBe(404);
});

test('active A1, A2 and B1 run their own real typed JSON calls and native no-JavaScript form POSTs',async({page,browser,request})=>{
 test.setTimeout(90_000);const native=await browser.newContext({javaScriptEnabled:false});try{for(const [snapshot,zone,clientVersion,nativeVersion]of [['I1','alpha','A1','alpha-v1'],['I2','alpha','A2','alpha-v2'],['I2','blog','B1','blog-v1']]){
  await control(request,{snapshot});await open(page,zone,clientVersion);await typed(page,request,zone,nativeVersion);const before=await state(request,zone),document=await native.newPage();try{await document.goto(publicOrigin+home(zone));await expect(proof(document)).toHaveText('Waiting for Go');await document.locator('[data-independent-form] input[name="amount"]').fill('2');const responsePromise=document.waitForResponse(response=>new URL(response.url()).pathname===action(zone)&&response.status()===303);await document.locator('[data-independent-form] button').click();const response=await responsePromise;expect(response.headers().location).toBe(zone==='blog'?'/blog?preview=1':'/?preview=1');await expect(document.locator('[data-independent-receipt]')).toHaveText('Preview flag: 1');expect((await state(request,zone)).revision).toBe(before.revision+2);}finally{await document.close();}
 }}finally{await native.close();}
});

test('an old A1 Go caller remains compatible with the unchanged v1 contract and receives actual active A2 data',async({page,request})=>{
 await open(page);const token=await doc(page);await control(request,{snapshot:'I2',clear:true});await typed(page,request,'alpha','alpha-v2');expect(await doc(page)).toBe(token);await expect(proof(page)).toHaveText('A1');await expect(page.locator('[data-independent-native]')).toHaveText('alpha-v1');
});

test('actual current Go calls with an injected wrong contract revision or protocol fail before mutation',async({page,request})=>{
 await control(request,{snapshot:'I2'});await open(page,'alpha','A2');const token=await doc(page),oldReceipt=await island(page).locator('[data-independent-action-result]').textContent();
 for(const [header,value]of [['x-nextgo-action-revision','v0'],['x-nextgo-action-protocol','0']]){
  const before=await state(request);await page.route('**/_nextgo/actions/preview/v1',route=>route.continue({headers:{...route.request().headers(),[header]:value}}));
  try{const responsePromise=page.waitForResponse(response=>new URL(response.url()).pathname===action('alpha')&&response.request().method()==='POST');await island(page).locator('[data-independent-call]').click();expect((await responsePromise).status()).toBe(409);await expect(island(page).locator('[data-independent-status]')).toHaveText('Preview outcome unconfirmed. Reload before retrying.');await expect(island(page).locator('[data-independent-call]')).toBeEnabled();expect((await state(request)).revision).toBe(before.revision);expect(await island(page).locator('[data-independent-action-result]').textContent()).toBe(oldReceipt);}finally{await page.unrouteAll({behavior:'wait'});}
 }
 expect(await doc(page)).toBe(token);
});

test('a real A2-to-A1 backend miswire fails its Go expectation before either application can mutate',async({page,request})=>{
 const beforeA1=await state(request);await control(request,{snapshot:'I2'});await open(page,'alpha','A2');const beforeA2=await state(request);await control(request,{relay:'miswire',clear:true});const responsePromise=page.waitForResponse(response=>new URL(response.url()).pathname===action('alpha')&&response.request().method()==='POST');await island(page).locator('[data-independent-call]').click();expect((await responsePromise).status()).toBe(502);await expect(island(page).locator('[data-independent-status]')).toHaveText('Preview outcome unconfirmed. Reload before retrying.');const wire=await control(request);expect(wire.records.filter(item=>item.role==='relay'&&item.method==='POST'&&item.path===action('alpha'))).toMatchObject([{status:409,target:'A1'}]);
 await control(request,{relay:'pass'});expect((await state(request)).revision).toBe(beforeA2.revision);await control(request,{snapshot:'I1'});expect((await state(request)).revision).toBe(beforeA1.revision);
});

test('a genuinely committed POST whose upstream reply is dropped is not replayed even with Idempotency-Key',async({page,request})=>{
 await control(request,{snapshot:'I2'});await open(page,'alpha','A2');const before=await state(request),initialReceipt=await island(page).locator('[data-independent-action-result]').textContent();await control(request,{clear:true,dropNext:true});await page.route('**/_nextgo/actions/preview/v1',route=>route.continue({headers:{...route.request().headers(),'Idempotency-Key':'independent-fixture-public-key'}}));
 try{await island(page).locator('[data-independent-call]').click();await expect(island(page).locator('[data-independent-status]')).toHaveText('Preview outcome unconfirmed. Reload before retrying.');expect(await island(page).locator('[data-independent-action-result]').textContent()).toBe(initialReceipt);expect((await state(request)).revision).toBe(before.revision+1);await pause(600);const wire=await control(request),posts=wire.records.filter(item=>item.role==='relay'&&item.method==='POST'&&item.path===action('alpha'));expect(posts).toHaveLength(1);expect(posts[0]).toMatchObject({target:'A2',status:200,complete:true,dropAfterActualCommit:true,idempotencyKeyPresent:true});expect((await state(request)).revision).toBe(before.revision+1);}finally{await page.unrouteAll({behavior:'wait'});}
});

test('real ingress supplies compatibility identity while explicit backend Hosts and trusted public origin remain separate',async({page,request})=>{
 const rejected=await request.get(`http://127.0.0.1:${ports.A1}/api/state`);expect(rejected.status()).toBe(409);const wrongHost=await request.get(`http://127.0.0.1:${ports.A1}/api/state`,{headers:{Host:'127.0.0.1:4260'}});expect(wrongHost.status()).toBe(421);
 const wrongIngress=await request.get(`http://127.0.0.1:${ports.I1}/api/state`,{headers:{Host:'attacker.example'}});expect(wrongIngress.status()).toBe(421);
 const actual=await state(request,'alpha',{headers:{Forwarded:'host=attacker.example;proto=https','X-Forwarded-Host':'attacker.example','X-Forwarded-Proto':'https','X-Nextgo-Zone-Release':'forged','X-Nextgo-Zone-ID':'forged','X-Nextgo-Zone-Protocol':'99','X-Nextgo-Zone-Topology':'forged'}});expect(actual.host).toBe('127.0.0.1:4260');expect(actual.publicOrigin).toBe(publicOrigin);expect(actual.basePath).toBe('');
 await control(request,{snapshot:'I2'});await open(page,'alpha','A2');await typed(page,request,'alpha','alpha-v2');const blog=await state(request,'blog');expect(blog.host).toBe('127.0.0.1:4260');expect(blog.basePath).toBe('/blog');expect(blog.publicOrigin).toBe(publicOrigin);
});
