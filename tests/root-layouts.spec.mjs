import {test as base,expect} from '@playwright/test';
import {startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';
import {startSocialMetadataStaticDeployment} from './social-metadata-static-fixture.mjs';
const test=base.extend({rootTransport:['http',{option:true}],native:async({page,rootTransport},use,info)=>{
 const mount=rootTransport==='http'?'':'/lambda-features';
 const fixture=rootTransport==='static'?await startSocialMetadataStaticDeployment({root:process.env.NEXTGO_ROOTS_STATIC_ROOT,sha256:process.env.NEXTGO_ROOTS_STATIC_SHA256,metadataCount:0,port:4287,
 pages:['lambda-features/index.html','lambda-features/guide/index.html','lambda-features/dashboard/index.html','lambda-features/dashboard/settings/index.html','lambda-features/plain/index.html','lambda-features/_nextgo/data/index.json']}):await startMetadataDeployment(rootTransport,{binary:process.env.NEXTGO_ROOTS_BINARY,sha256:process.env.NEXTGO_ROOTS_SHA256,port:4287,startupEnvironment:{NEXTGO_METADATA_BASE_PATH:mount,NEXTGO_METADATA_ASSET_PREFIX:mount?'/lambda-assets':''}});
 fixture.mount=mount;fixture.home=fixture.origin+mount+'/';
 try{await use(fixture);}finally{
  const failures=[];try{await page.close();}catch(e){failures.push(e);}try{await fixture.close();}catch(e){failures.push(e);}
  const proof={transport:rootTransport,binary:process.env.NEXTGO_ROOTS_BINARY,sha256:process.env.NEXTGO_ROOTS_SHA256,artifact:fixture.artifact??null,cleanup:fixture.cleanupProof,runtime:fixture.runtimeProof?.()??null,sourceRequests:fixture.sourceRecords.length};
  await info.attach('root-layouts-owned-transport-proof',{body:JSON.stringify(proof),contentType:'application/json'});expect(proof.sourceRequests).toBe(0);
  for(const [key,value]of Object.entries(proof.cleanup))expect(value,key).toBe(key==='runtimeGatewayClosed'?rootTransport!=='http':true);
  if(failures.length)throw new AggregateError(failures,'root layouts cleanup failed');
 }
}});
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const root=page=>page.locator('body').getAttribute('data-nextgo-root');
const mark=page=>page.evaluate(()=>{globalThis.rootTestDocument=document;globalThis.rootTestCounter=document.querySelector('[data-count]');});
const retained=page=>page.evaluate(()=>globalThis.rootTestDocument===document&&globalThis.rootTestCounter===document.querySelector('[data-count]'));
test.describe('without JavaScript',()=>{test.use({javaScriptEnabled:false});test('all three roots provide useful native HTML without WASM',async({page,native})=>{
 const wasm=[];page.on('request',request=>{if(/\.wasm(?:\?|$)/.test(request.url()))wasm.push(request.url());});const roots=[];
 for(const [path,title,layout]of [['/','Marketing home','marketing'],['/dashboard','Workspace dashboard','workspace'],['/plain','Implicit document root','implicit']]){
  const response=await page.goto(native.origin+native.mount+path);expect(response.status()).toBe(200);await expect(page).toHaveTitle(title);await expect(page.locator('[data-root-layout]')).toHaveAttribute('data-root-layout',layout);roots.push(await root(page));
 }
 expect(new Set(roots).size).toBe(3);for(const value of roots)expect(value).toMatch(/^[a-f0-9]{64}$/);expect(wasm).toEqual([]);
});});
test('same-root navigation retains Go layout state and node identity through history',async({page,native})=>{
 await page.goto(native.home);await ready(page);await mark(page);const marketing=await root(page);
 await page.getByRole('button',{name:'Increase retained count'}).click();await expect(page.locator('[data-count]')).toHaveText('3');
 await page.getByRole('link',{name:'Marketing guide',exact:true}).click();await expect(page).toHaveTitle('Marketing guide');await ready(page);expect(await retained(page)).toBe(true);expect(await root(page)).toBe(marketing);await expect(page.locator('[data-count]')).toHaveText('3');
 await page.goBack();await expect(page).toHaveTitle('Marketing home');expect(await retained(page)).toBe(true);
 await page.goto(native.origin+native.mount+'/dashboard');await ready(page);await mark(page);const workspace=await root(page);expect(workspace).not.toBe(marketing);
 await page.getByRole('button',{name:'Increase retained count'}).click();await expect(page.locator('[data-count]')).toHaveText('41');
 await page.getByRole('link',{name:'Dashboard settings',exact:true}).click();await expect(page).toHaveTitle('Workspace settings');expect(await retained(page)).toBe(true);expect(await root(page)).toBe(workspace);await expect(page.locator('[data-count]')).toHaveText('41');
 await page.goBack();await expect(page).toHaveTitle('Workspace dashboard');expect(await retained(page)).toBe(true);
});
test('ordinary links cross explicit and implicit roots through native documents without SPA fetches',async({page,native})=>{
 await page.goto(native.home);await ready(page);await mark(page);const marketing=await root(page),requests=[];
 page.on('request',request=>{if(request.headers()['x-nextgo-navigate']==='1')requests.push(request.url());});
 await page.getByRole('link',{name:'Open dashboard',exact:true}).click();await expect(page).toHaveTitle('Workspace dashboard');await ready(page);expect(await retained(page)).toBe(false);expect(await root(page)).not.toBe(marketing);await expect(page.locator('[data-count]')).toHaveText('40');
 await page.getByRole('link',{name:'Marketing home',exact:true}).click();await expect(page).toHaveTitle('Marketing home');await ready(page);await mark(page);
 await page.getByRole('link',{name:'Open implicit root',exact:true}).click();await expect(page).toHaveTitle('Implicit document root');await ready(page);expect(await retained(page)).toBe(false);expect(await root(page)).not.toBe(marketing);expect(requests).toEqual([]);
});
test('Go Navigate and Replace create native root transitions with correct history',async({page,native})=>{
 await page.goto(native.home);await ready(page);await mark(page);
 await page.getByRole('button',{name:'Navigate to workspace',exact:true}).click();await expect(page).toHaveTitle('Workspace dashboard');await ready(page);expect(await retained(page)).toBe(false);
 await page.goBack();await expect(page).toHaveTitle('Marketing home');await ready(page);
 await page.goto(native.origin+native.mount+'/plain');await ready(page);await page.getByRole('link',{name:'Marketing home',exact:true}).click();await expect(page).toHaveTitle('Marketing home');await ready(page);
 await page.getByRole('button',{name:'Replace with workspace',exact:true}).click();await expect(page).toHaveTitle('Workspace dashboard');await ready(page);
 await page.goBack();await expect(page).toHaveTitle('Implicit document root');
});
test('conditional rewrite rejects another root envelope before replacing the document',async({page,native})=>{
 await page.goto(native.home);await ready(page);await mark(page);const marketing=await root(page),requests=[];
 page.on('request',request=>{if(request.headers()['x-nextgo-navigate']==='1')requests.push(request.url());});
 await page.getByRole('link',{name:'Conditional dashboard rewrite',exact:true}).click();await expect(page).toHaveTitle('Workspace dashboard');await ready(page);
 expect(await retained(page)).toBe(false);expect(await root(page)).not.toBe(marketing);await expect(page.locator('[data-count]')).toHaveText('40');expect(new URL(page.url()).pathname).toBe(native.mount+'/masked');expect(new URL(page.url()).search).toBe('?other=1');expect(requests.length).toBeGreaterThan(0);
});
