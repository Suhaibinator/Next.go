import {test as base,expect} from '@playwright/test';
import {createHash} from 'node:crypto';
import {startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';
import {startSocialMetadataStaticDeployment} from './social-metadata-static-fixture.mjs';

const test=base.extend({
  variantTransport:['http',{option:true}],
  native:async({page,variantTransport},use,info)=>{
    const mount=variantTransport==='http'?'':'/lambda-features';
    const fixture=variantTransport==='static'?await startSocialMetadataStaticDeployment({root:process.env.NEXTGO_VARIANTS_STATIC_ROOT,sha256:process.env.NEXTGO_VARIANTS_STATIC_SHA256,port:4285,metadataCount:4,
      pages:['lambda-features/index.html','lambda-features/clear/index.html','lambda-features/_nextgo/data/index.json','lambda-features/_nextgo/data/clear.json']}):await startMetadataDeployment(variantTransport,{binary:process.env.NEXTGO_VARIANTS_BINARY,sha256:process.env.NEXTGO_VARIANTS_SHA256,port:4285,startupEnvironment:{NEXTGO_METADATA_BASE_PATH:mount,NEXTGO_METADATA_ASSET_PREFIX:mount?'/lambda-assets':''}});
    fixture.mount=mount;
    try{await use(fixture);}finally{
      const failures=[];try{await page.close();}catch(e){failures.push(e);}try{await fixture.close();}catch(e){failures.push(e);}
      const proof={transport:variantTransport,binary:process.env.NEXTGO_VARIANTS_BINARY,sha256:process.env.NEXTGO_VARIANTS_SHA256,artifact:fixture.artifact??null,cleanup:fixture.cleanupProof,runtime:fixture.runtimeProof?.()??null,sourceRequests:fixture.sourceRecords.length};
      await info.attach('social-variants-owned-transport-proof',{body:JSON.stringify(proof),contentType:'application/json'});
      expect(proof.sourceRequests).toBe(0);for(const [key,value]of Object.entries(proof.cleanup))expect(value,key).toBe(key==='runtimeGatewayClosed'?variantTransport!=='http':true);
      if(failures.length)throw new AggregateError(failures,'social variant cleanup failed');
    }
  },
});
const og=page=>page.locator('head meta[property="og:image"]');
const twitter=page=>page.locator('head meta[name="twitter:image"]');
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
async function defaults(page,mount){
  await expect(og(page)).toHaveCount(1);await expect(og(page)).toHaveAttribute('content','https://example.test'+mount+'/opengraph-image.jpg');
  await expect(twitter(page)).toHaveCount(1);await expect(twitter(page)).toHaveAttribute('content','https://example.test'+mount+'/twitter-image.gif');
  await expect(page.locator('meta[property="og:image:type"]')).toHaveAttribute('content','image/jpeg');
  await expect(page.locator('meta[property="og:image:width"]')).toHaveAttribute('content','320');await expect(page.locator('meta[property="og:image:height"]')).toHaveAttribute('content','180');
  await expect(page.locator('meta[property="og:image:alt"]')).toHaveAttribute('content','Original mint checkerboard JPEG.');
  await expect(page.locator('meta[name="twitter:image:alt"]')).toHaveAttribute('content','Original two-frame mint checkerboard GIF.');
}
test.describe('without JavaScript',()=>{
  test.use({javaScriptEnabled:false});
  test('original JPEG and GIF decode from native HTML without application JavaScript',async({page,native},info)=>{
    const wasm=[];page.on('request',request=>{if(/\.wasm(?:\?|$)/.test(request.url()))wasm.push(request.url());});
    const response=await page.goto(native.origin+(native.mount||'/'));expect(response.status()).toBe(200);expect(response.headers()['x-nextgo-requires-server']).not.toBe('1');
    await defaults(page,native.mount);const images=page.locator('[data-variant-jpeg],[data-variant-gif]');await expect(images).toHaveCount(2);
    await expect.poll(()=>images.evaluateAll(nodes=>nodes.every(n=>n.complete&&n.naturalWidth===320&&n.naturalHeight===180))).toBe(true);
    const proof=await images.evaluateAll(nodes=>nodes.map(n=>({src:n.getAttribute('src'),width:n.naturalWidth,height:n.naturalHeight,complete:n.complete})));
    await info.attach('actual-jpeg-gif-browser-decode',{body:JSON.stringify(proof),contentType:'application/json'});expect(wasm).toEqual([]);
  });
});
test('Go navigation clears and restores image metadata while retaining layout state',async({page,native})=>{
  await page.goto(native.origin+(native.mount||'/'));await ready(page);await defaults(page,native.mount);
  await page.evaluate(()=>{globalThis.variantDocument=document;globalThis.variantCounter=document.querySelector('[data-count]');});
  await page.getByRole('button',{name:'Increase retained count'}).click();await expect(page.locator('[data-count]')).toHaveText('3');
  await page.getByRole('link',{name:'Clear image defaults',exact:true}).click();await expect(page.getByRole('heading',{name:'The public files remain available.'})).toBeVisible();
  await expect(og(page)).toHaveCount(0);await expect(twitter(page)).toHaveCount(0);await expect(page.locator('meta[property^="og:image:"],meta[name="twitter:image:alt"],meta[name="twitter:card"]')).toHaveCount(0);
  await page.goBack();await defaults(page,native.mount);await expect(page.locator('[data-count]')).toHaveText('3');
  expect(await page.evaluate(()=>variantDocument===document&&variantCounter===document.querySelector('[data-count]'))).toBe(true);
});
test('JPEG GIF and alt files preserve raw identities and host-owned HTTP policy',async({page,request,native,variantTransport})=>{
  await page.goto(native.origin+(native.mount||'/'));
  for(const [name,type]of [['opengraph-image.jpg','image/jpeg'],['twitter-image.gif','image/gif'],['opengraph-image.alt.txt','text/plain; charset=utf-8'],['twitter-image.alt.txt','text/plain; charset=utf-8']]){
    const url=native.origin+native.mount+'/'+name,response=await request.get(url,{headers:{'Accept-Encoding':'identity'}});expect(response.status()).toBe(200);
    const data=await response.body(),headers=response.headers(),hash=createHash('sha256').update(data).digest('hex');expect(headers['content-type']).toBe(type);expect(headers['x-content-type-options']).toBe('nosniff');
    if(type==='image/jpeg')expect(data.subarray(0,2).toString('hex')).toBe('ffd8');
    if(type==='image/gif'){expect(data.subarray(0,6).toString()).toMatch(/^GIF8[79]a$/);expect(data.readUInt16LE(6)).toBe(320);expect(data.readUInt16LE(8)).toBe(180);}
    if(variantTransport==='static')expect(headers['cache-control']).toBe('no-cache');else{
      expect(headers.etag).toBe('"'+hash+'"');expect(headers['cache-control']).toBe('public, max-age=0, must-revalidate');
      const conditional=await request.get(url,{headers:{'If-None-Match':'W/"'+hash+'"','Accept-Encoding':'identity'}});expect(conditional.status()).toBe(304);expect((await conditional.body()).length).toBe(0);
    }
    const head=await request.head(url,{headers:{'Accept-Encoding':'identity'}});expect(head.status()).toBe(200);expect((await head.body()).length).toBe(0);expect(head.headers()['content-length']).toBe(String(data.length));
    const rejected=await request.post(url);expect(rejected.status()).toBe(405);expect(rejected.headers().allow).toBe('GET, HEAD');
  }
});
