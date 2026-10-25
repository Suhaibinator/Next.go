import {test,expect} from '@playwright/test';
import {readFile,writeFile} from 'node:fs/promises';
import {join} from 'node:path';
import {createHash,randomUUID} from 'node:crypto';

const origin='http://127.0.0.1:4236', moved='http://localhost:4236';
const owned=process.env.NEXTGO_ZONES_DEV_ROOT, app=join(owned,'application');
const counterFile=join(app,'marketing/components/counter/client_js.go'), styleFile=join(app,'marketing/public/style.css'), manifestFile=join(app,'zones.json'), envFile=join(app,'.env.development');
const sha=bytes=>createHash('sha256').update(bytes).digest('hex');
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const component=(page,zone)=>page.locator(`[data-zone-counter="${zone}"]`);
const count=(page,zone)=>component(page,zone).locator('[data-zone-count]');
async function inspection(request){const response=await request.get(origin+'/_nextgo/inspect');expect(response.status()).toBe(200);return response.json();}
async function waitReady(request,predicate=()=>true){await expect.poll(async()=>{const s=await inspection(request);return s.state==='ready'&&s.serving&&predicate(s);},{timeout:120_000}).toBe(true);return inspection(request);}
async function waitFailed(request,prior){await expect.poll(async()=>{const s=await inspection(request);return s.state==='failed'&&s.serving&&s.stale&&s.buildId===prior.buildId&&s.generation===prior.generation;}).toBe(true);}
async function wasm(page,request){const url=await page.locator('body').getAttribute('data-nextgo-runtime');const target=new URL(url+'app.wasm',page.url()).href;const response=await request.get(target,{headers:{'Accept-Encoding':'identity'}});expect(response.status()).toBe(200);const bytes=await response.body();expect(bytes.subarray(0,4).toString('hex')).toBe('0061736d');return {url:target,rawBytes:bytes.length,sha256:sha(bytes)};}
async function stableLoads(loads,expected){await expect.poll(loads).toBe(expected);await new Promise(resolve=>setTimeout(resolve,1300));expect(loads()).toBe(expected);}
function documents(page){let count=0;page.on('request',r=>{if(r.isNavigationRequest()&&r.frame()===page.mainFrame()&&r.method()==='GET')count++;});return ()=>count;}
async function update(path,text){if(await readFile(path,'utf8').catch(error=>error.code==='ENOENT'?'':Promise.reject(error))!==text){await writeFile(path,text);return true;}return false;}

test('actual zones dev rebuilds Go WASM once, keeps last good zones, recovers manifests and global Host settings',async({browser,page,context,request},testInfo)=>{
 const originals=JSON.parse(await readFile(join(owned,'originals.json'),'utf8'));
 const manifest=JSON.parse(originals.manifest), evidence={startedAt:new Date().toISOString(),events:[]};
 await update(counterFile,originals.counter);await update(styleFile,originals.style);await update(manifestFile,originals.manifest);await update(envFile,'');
 await waitReady(request);
 const native=await browser.newContext({javaScriptEnabled:false});
 try{for(const zone of [{id:'marketing',path:'/'},{id:'blog',path:'/blog'}]){
  const document=await native.newPage();expect((await document.goto(origin+zone.path)).status()).toBe(200);
  await expect(document.locator(`[data-zone-page="${zone.id}"]`)).toBeVisible();await expect(count(document,zone.id)).toHaveText('1');
  await expect(component(document,zone.id).locator('[data-zone-client]')).toHaveText('Waiting for Go');await expect(component(document,zone.id).locator('[data-zone-increment]')).toBeDisabled();
 }}finally{await native.close();}
 const blog=await context.newPage();let preview;
 try{
  await page.goto('/');await ready(page);await expect(count(page,'marketing')).toHaveText('1');
  await component(page,'marketing').locator('[data-zone-increment]').click();await expect(count(page,'marketing')).toHaveText('2');
  const documentToken=randomUUID();await page.evaluate(value=>{window.devDocument=value;window.devLayout=document.querySelector('[data-zone-layout="marketing"]');},documentToken);
  await page.getByRole('link',{name:'Same-zone details',exact:true}).click();await expect(page.locator('[data-zone-details="marketing"]')).toBeVisible();
  expect(await page.evaluate(()=>window.devDocument)).toBe(documentToken);
  expect(await page.evaluate(()=>window.devLayout===document.querySelector('[data-zone-layout="marketing"]'))).toBe(true);
  await page.goBack();await expect(page.locator('[data-zone-page="marketing"]')).toBeVisible();
  await page.getByRole('link',{name:'Visit the blog zone',exact:true}).click();await ready(page);await expect(page.locator('[data-zone-page="blog"]')).toBeVisible();expect(await page.evaluate(()=>window.devDocument)).toBeUndefined();
  await component(page,'blog').locator('[data-zone-increment]').click();await expect(count(page,'blog')).toHaveText('11');
  await page.goto('/');await ready(page);await blog.goto(origin+'/blog');await ready(blog);
  const marketingLoads=documents(page),blogLoads=documents(blog);
  await stableLoads(marketingLoads,0);await stableLoads(blogLoads,0);
  evidence.initialMarketingWasm=await wasm(page,request);evidence.initialBlogWasm=await wasm(blog,request);
  let current=await inspection(request);evidence.events.push({phase:'initial',inspection:current});

  // This changes actual client Go code, creating a different WASM bundle.
  expect(originals.counter).toContain('state.Set(state.Get() + 1)');
  const doubled=originals.counter.replace('state.Set(state.Get() + 1)','state.Set(state.Get() + 2)');
  let m=marketingLoads(),b=blogLoads();await writeFile(counterFile,doubled);
  current=await waitReady(request,s=>s.generation!==current.generation);await ready(page);await ready(blog);
  await stableLoads(marketingLoads,m+1);await stableLoads(blogLoads,b+1);
  await expect(count(page,'marketing')).toHaveText('1');await component(page,'marketing').locator('[data-zone-increment]').click();await expect(count(page,'marketing')).toHaveText('3');
  evidence.doubledMarketingWasm=await wasm(page,request);expect(evidence.doubledMarketingWasm.sha256).not.toBe(evidence.initialMarketingWasm.sha256);
  for(const saved of [evidence.initialMarketingWasm,evidence.initialBlogWasm]){const old=await request.get(saved.url,{headers:{'Accept-Encoding':'identity'}});expect(old.status()).toBe(200);expect(sha(await old.body())).toBe(saved.sha256);}
  evidence.events.push({phase:'client-rebuilt',inspection:current,marketingLoads:marketingLoads(),blogLoads:blogLoads()});

  m=marketingLoads();b=blogLoads();await writeFile(counterFile,doubled+'\nthis is not valid Go\n');await waitFailed(request,current);
  await expect(page.locator('#nextgo-status')).toContainText('Build failed:');
  await component(page,'marketing').locator('[data-zone-increment]').click();await expect(count(page,'marketing')).toHaveText('5');
  await component(blog,'blog').locator('[data-zone-increment]').click();await expect(count(blog,'blog')).toHaveText('11');
  await stableLoads(marketingLoads,m);await stableLoads(blogLoads,b);
  expect((await request.get(origin+'/api/status')).status()).toBe(200);expect((await request.get(origin+'/blog/api/status')).status()).toBe(200);
  evidence.events.push({phase:'compile-failed-last-good',inspection:await inspection(request)});

  const recovered=originals.counter.replace('state.Set(state.Get() + 1)','state.Set(state.Get() + 3)');await writeFile(counterFile,recovered);
  current=await waitReady(request,s=>s.generation!==current.generation);await ready(page);await ready(blog);
  await stableLoads(marketingLoads,m+1);await stableLoads(blogLoads,b+1);
  await component(page,'marketing').locator('[data-zone-increment]').click();await expect(count(page,'marketing')).toHaveText('4');await expect(count(blog,'blog')).toHaveText('1');
  evidence.events.push({phase:'compile-recovered',inspection:current});

  m=marketingLoads();b=blogLoads();await writeFile(styleFile,originals.style+'\n:root { --zones-development-proof: rebuilt; }\n');
  current=await waitReady(request,s=>s.generation!==current.generation);await ready(page);await ready(blog);
  await stableLoads(marketingLoads,m+1);await stableLoads(blogLoads,b+1);
  await expect.poll(()=>page.evaluate(()=>getComputedStyle(document.documentElement).getPropertyValue('--zones-development-proof').trim())).toBe('rebuilt');
  expect(await (await request.get(origin+'/assets/style.css')).text()).toContain('--zones-development-proof');
  expect(await (await request.get(origin+'/blog/assets/style.css')).text()).not.toContain('--zones-development-proof');
  evidence.events.push({phase:'public-assets-rebuilt',inspection:current});

  m=marketingLoads();b=blogLoads();await writeFile(manifestFile,'{"version":1,"zones":[');await waitFailed(request,current);
  await component(page,'marketing').locator('[data-zone-increment]').click();await expect(count(page,'marketing')).toHaveText('4');
  await stableLoads(marketingLoads,m);await stableLoads(blogLoads,b);
  const expanded={...manifest,zones:[...manifest.zones,{id:'preview',host:'127.0.0.1:4236',basePath:'/preview',app:'./blog'}]};await writeFile(manifestFile,JSON.stringify(expanded,null,2)+'\n');
  current=await waitReady(request,s=>s.generation!==current.generation);await ready(page);await ready(blog);
  await stableLoads(marketingLoads,m+1);await stableLoads(blogLoads,b+1);
  expect(current.graph.nodes.filter(n=>n.kind==='zone').map(n=>n.name).sort()).toEqual(['blog','marketing','preview']);
  preview=await context.newPage();await preview.goto(origin+'/preview');await ready(preview);await expect(preview.locator('[data-zone-context]')).toContainText('zone=preview');
  const previewAsset=await wasm(preview,request);expect((await (await request.get(origin+'/preview/api/status')).json()).zone).toBe('preview');await preview.close();preview=undefined;
  evidence.events.push({phase:'manifest-recovered-added-zone',inspection:current});
  m=marketingLoads();b=blogLoads();await writeFile(manifestFile,originals.manifest);
  current=await waitReady(request,s=>s.generation!==current.generation);await ready(page);await ready(blog);await stableLoads(marketingLoads,m+1);await stableLoads(blogLoads,b+1);
  expect((await request.get(origin+'/preview')).status()).toBe(404);
  const retainedPreview=await request.get(previewAsset.url,{headers:{'Accept-Encoding':'identity'}});expect(retainedPreview.status()).toBe(200);expect(sha(await retainedPreview.body())).toBe(previewAsset.sha256);
  const wrongMount=new URL(previewAsset.url);wrongMount.pathname=wrongMount.pathname.replace('/preview/','/wrong/');expect((await request.get(wrongMount.href)).status()).not.toBe(200);
  evidence.events.push({phase:'removed-zone-retained-assets',inspection:current});

  m=marketingLoads();b=blogLoads();const beforeHost=current,oldBlogAsset=await wasm(blog,request);
  await writeFile(envFile,"NEXTGO_ZONE_HOSTS='MALFORMED_PRIVATE_FIXTURE_VALUE\n");await waitFailed(request,current);
  await expect(page.locator('#nextgo-status')).toContainText('Build failed:');await stableLoads(marketingLoads,m);await stableLoads(blogLoads,b);
  expect((await request.get(origin+'/blog/api/status')).status()).toBe(200);
  await blog.close();
  await writeFile(envFile,`NEXTGO_ZONE_HOSTS='${JSON.stringify({marketing:'127.0.0.1:4236',blog:'localhost:4236'})}'\n`);
  current=await waitReady(request,s=>s.generation!==beforeHost.generation);await ready(page);await stableLoads(marketingLoads,m+1);expect(current.buildId).toBe(beforeHost.buildId);
  const movedBlog=await context.newPage();try{await movedBlog.goto(moved+'/blog');await ready(movedBlog);await component(movedBlog,'blog').locator('[data-zone-increment]').click();await expect(count(movedBlog,'blog')).toHaveText('11');expect((await (await request.get(moved+'/blog/api/status')).json()).host).toBe('localhost:4236');}finally{await movedBlog.close();}
  const retainedHost=await request.get(oldBlogAsset.url,{headers:{'Accept-Encoding':'identity'}});expect(retainedHost.status()).toBe(200);expect(sha(await retainedHost.body())).toBe(oldBlogAsset.sha256);
  expect((await request.get(origin+'/blog/api/status')).status()).toBe(404);
  expect((await request.get(origin+'/_nextgo/inspect',{headers:{Origin:'http://attacker.example','Sec-Fetch-Site':'cross-site'}})).status()).toBe(403);
  evidence.events.push({phase:'dotenv-host-recovered-without-compilation',inspection:current});
 }finally{
  await preview?.close();await blog.close().catch(()=>{});
  const beforeReset=await inspection(request).catch(()=>undefined);
  const restored=await Promise.all([update(counterFile,originals.counter),update(styleFile,originals.style),update(manifestFile,originals.manifest),update(envFile,'')]);
  await waitReady(request,s=>!restored.some(Boolean)||!beforeReset||s.generation!==beforeReset.generation).catch(()=>{});
  evidence.completedAt=new Date().toISOString();await writeFile(join(owned,`browser-evidence-${testInfo.project.name}.json`),JSON.stringify(evidence,null,2)+'\n');
  await testInfo.attach('zones-development-evidence',{body:JSON.stringify(evidence,null,2),contentType:'application/json'});
 }
});
