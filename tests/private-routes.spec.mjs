import {test as base,expect} from '@playwright/test';
import {startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';
import {startSocialMetadataStaticDeployment} from './social-metadata-static-fixture.mjs';

const test=base.extend({privateTransport:['http',{option:true}],native:async({page,privateTransport},use,info)=>{
  const mount=privateTransport==='http'?'':'/lambda-features';
  const fixture=privateTransport==='static'?await startSocialMetadataStaticDeployment({root:process.env.NEXTGO_PRIVATE_STATIC_ROOT,sha256:process.env.NEXTGO_PRIVATE_STATIC_SHA256,metadataCount:0,port:4286,
    pages:['lambda-features/routing-private/index.html','lambda-features/about/index.html','lambda-features/_public/index.html','lambda-features/_nextgo/data/routing-private.json','lambda-features/_nextgo/data/_public.json']}):await startMetadataDeployment(privateTransport,{binary:process.env.NEXTGO_PRIVATE_BINARY,sha256:process.env.NEXTGO_PRIVATE_SHA256,port:4286});
  fixture.mount=mount;fixture.path=mount+'/routing-private';
  try{await use(fixture);}finally{
    const failures=[];try{await page.close();}catch(e){failures.push(e);}try{await fixture.close();}catch(e){failures.push(e);}
    const proof={transport:privateTransport,binary:process.env.NEXTGO_PRIVATE_BINARY,sha256:process.env.NEXTGO_PRIVATE_SHA256,artifact:fixture.artifact??null,cleanup:fixture.cleanupProof,runtime:fixture.runtimeProof?.()??null,sourceRequests:fixture.sourceRecords.length};
    await info.attach('private-routes-owned-transport-proof',{body:JSON.stringify(proof),contentType:'application/json'});expect(proof.sourceRequests).toBe(0);
    for(const [key,value]of Object.entries(proof.cleanup))expect(value,key).toBe(key==='runtimeGatewayClosed'?privateTransport!=='http':true);
    if(failures.length)throw new AggregateError(failures,'private routes fixture cleanup failed');
  }
}});
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
async function useful(page){await expect(page.locator('[data-private-helper-message]')).toHaveText('Imported from private_helpers; never registered as a route.');await expect(page.locator('[data-underscore-helper-message]')).toHaveText('Imported from _native; native helper bytes remain build inputs.');}
test.describe('without JavaScript',()=>{test.use({javaScriptEnabled:false});test('explicitly imported native helpers render useful public HTML without becoming routes',async({page,native})=>{
  const wasm=[];page.on('request',request=>{if(/\.wasm(?:\?|$)/.test(request.url()))wasm.push(request.url());});const response=await page.goto(native.origin+native.path);expect(response.status()).toBe(200);await useful(page);expect(wasm).toEqual([]);
});});
test('private URLs and source files stay absent from routes and publication',async({page,request,native})=>{
  await page.goto(native.origin+native.path);const manifest=JSON.parse(await page.locator('body').getAttribute('data-nextgo-navigation'));
  expect(manifest.routes.some(route=>route.pattern==='/routing-private')).toBe(true);expect(manifest.routes.some(route=>route.pattern==='/_public')).toBe(true);
  expect(manifest.routes.some(route=>/private_helpers|_native|slot_ignored/.test(route.pattern))).toBe(false);
  for(const path of ['/private_helpers','/private_helpers/data.go','/private_helpers/slot_ignored','/_native','/_native/data.go']){const response=await request.get(native.origin+native.mount+path);expect(response.status(),path).toBe(404);}
});
test('Go navigation and literal underscore routes work with native-only helper imports',async({page,native})=>{
  await page.goto(native.origin+native.path);await ready(page);await useful(page);await page.evaluate(()=>{globalThis.privateRouteDocument=document;});
  await page.locator('[data-private-literal-link]').click();await expect(page.locator('[data-private-literal-route]')).toHaveText('A literal_ directory exposes this intentional underscore URL.');await ready(page);
  expect(await page.evaluate(()=>privateRouteDocument===document)).toBe(true);
  await page.goBack();await useful(page);await ready(page);expect(await page.evaluate(()=>privateRouteDocument===document)).toBe(true);
  await page.locator('[data-private-routing-exit]').click();await expect(page).toHaveTitle('Architecture — Next.go');await ready(page);
  await page.goBack();await useful(page);expect(await page.evaluate(()=>privateRouteDocument===document)).toBe(true);
});
