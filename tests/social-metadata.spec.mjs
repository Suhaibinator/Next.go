import {test as base, expect} from '@playwright/test';
import {createHash} from 'node:crypto';
import {startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';
import {startSocialMetadataStaticDeployment} from './social-metadata-static-fixture.mjs';

const test = base.extend({
  socialTransport: ['http', {option: true}],
  native: async ({page, socialTransport}, use, info) => {
    const mount = socialTransport === 'http' ? '' : '/lambda-features';
    const fixture = socialTransport === 'static' ? await startSocialMetadataStaticDeployment() : await startMetadataDeployment(socialTransport, {
      binary: process.env.NEXTGO_SOCIAL_METADATA_BINARY, sha256: process.env.NEXTGO_SOCIAL_METADATA_SHA256, port: 4283,
      startupEnvironment: {NEXTGO_METADATA_BASE_PATH: mount, NEXTGO_METADATA_ASSET_PREFIX: mount ? '/lambda-assets' : ''},
    });
    fixture.mount = mount;
    try {await use(fixture);} finally {
      const errors = [];
      try {await page.close();} catch (error) {errors.push(error);}
      try {await fixture.close();} catch (error) {errors.push(error);}
      const proof = {transport: socialTransport, binary: process.env.NEXTGO_SOCIAL_METADATA_BINARY, sha256: process.env.NEXTGO_SOCIAL_METADATA_SHA256,
        cleanup: fixture.cleanupProof, artifact: fixture.artifact ?? null, runtime: fixture.runtimeProof?.() ?? null, sourceRequests: fixture.sourceRecords.length};
      await info.attach('social-metadata-process-and-transport-proof', {body: JSON.stringify(proof), contentType: 'application/json'});
      expect(proof.sourceRequests).toBe(0);
      for (const [key,value] of Object.entries(proof.cleanup)) expect(value,key).toBe(key === 'runtimeGatewayClosed' ? socialTransport !== 'http' : true);
      if (errors.length) throw new AggregateError(errors, 'social metadata fixture cleanup failed');
    }
  },
});
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const og = page => page.locator('head meta[property="og:image"]');
const twitter = page => page.locator('head meta[name="twitter:image"]');
const nav = page => page.getByRole('navigation',{name:'Metadata pages'});
async function defaults(page,mount) {
  await expect(og(page)).toHaveCount(1); await expect(og(page)).toHaveAttribute('content','https://example.test'+mount+'/opengraph-image.png');
  await expect(twitter(page)).toHaveCount(1); await expect(twitter(page)).toHaveAttribute('content','https://example.test'+mount+'/twitter-image.png');
  await expect(page.locator('meta[property="og:image:width"]')).toHaveAttribute('content','1200');
  await expect(page.locator('meta[property="og:image:height"]')).toHaveAttribute('content','630');
  await expect(page.locator('meta[property="og:image:type"]')).toHaveAttribute('content','image/png');
  await expect(page.locator('meta[property="og:image:alt"]')).toHaveAttribute('content','Original Go bitmap card: Small files. Clear ownership.');
  await expect(page.locator('meta[name="twitter:image:alt"]')).toHaveAttribute('content','Original Go bitmap card for the public metadata example.');
  await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute('content','summary_large_image');
}

test.describe('without JavaScript',() => {
  test.use({javaScriptEnabled:false});
  test('generated public PNGs decode from initial HTML with trusted social metadata and no application JavaScript',async({page,native},info) => {
    const wasm=[];page.on('request',request => {if (/\.wasm(?:\?|$)/.test(request.url())) wasm.push(request.url());});
    const response=await page.goto(native.origin+(native.mount||'/'));expect(response.status()).toBe(200);
    expect(response.headers()['x-nextgo-requires-server']).not.toBe('1');
    await defaults(page,native.mount);
    const previews=page.locator('[data-social-metadata-preview], [data-social-metadata-twitter]');
    await expect(previews).toHaveCount(2);
    await expect.poll(() => previews.evaluateAll(images => images.every(image => image.complete && image.naturalWidth===1200 && image.naturalHeight===630))).toBe(true);
    const proof=await previews.evaluateAll(images => images.map(image=>({src:image.getAttribute('src'),width:image.naturalWidth,height:image.naturalHeight,complete:image.complete})));
    await info.attach('native-html-generated-png-decode',{body:JSON.stringify(proof),contentType:'application/json'});
    expect(wasm).toEqual([]);
  });
});

test('Go navigation replaces and clears social image groups and restores defaults without remounting the layout',async({page,native}) => {
  await page.goto(native.origin+(native.mount||'/'));await ready(page);await defaults(page,native.mount);
  await page.evaluate(()=>{globalThis.socialMetadataDocument=document;globalThis.socialMetadataCounter=document.querySelector('[data-count]');});
  await page.getByRole('button',{name:'Increase retained count'}).click();await expect(page.locator('[data-count]')).toHaveText('3');
  await nav(page).getByRole('link',{name:'About',exact:true}).click();await expect(page.getByRole('heading',{name:'One application, shared defaults.'})).toBeVisible();await defaults(page,native.mount);
  await nav(page).getByRole('link',{name:'Explicit metadata',exact:true}).click();
  await expect(og(page)).toHaveCount(1);await expect(og(page)).toHaveAttribute('content','https://example.test/explicit-social.png');
  await expect(twitter(page)).toHaveCount(1);await expect(twitter(page)).toHaveAttribute('content','https://example.test/explicit-twitter.png');
  await expect(page.locator('meta[property="og:image:alt"]')).toHaveAttribute('content','Explicit public image');
  await expect(page.locator('meta[property="og:image:type"]')).toHaveCount(0);await expect(page.locator('meta[name="twitter:image:alt"]')).toHaveCount(0);
  await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute('content','summary');
  await nav(page).getByRole('link',{name:'Disabled metadata',exact:true}).click();await expect(page.getByRole('heading',{name:'An empty choice clears defaults.'})).toBeVisible();
  await expect(og(page)).toHaveCount(0);await expect(twitter(page)).toHaveCount(0);await expect(page.locator('meta[property^="og:image:"],meta[name="twitter:image:alt"],meta[name="twitter:card"]')).toHaveCount(0);
  await page.goBack();await expect(og(page)).toHaveAttribute('content','https://example.test/explicit-social.png');
  await page.goBack();await defaults(page,native.mount);await expect(page.locator('[data-count]')).toHaveText('3');
  expect(await page.evaluate(()=>socialMetadataDocument===document&&socialMetadataCounter===document.querySelector('[data-count]'))).toBe(true);
});

test('published PNG and alt-text bytes have matching hashes, MIME and transport-owned validators',async({page,request,native,socialTransport}) => {
  await page.goto(native.origin+(native.mount||'/'));
  for (const name of ['opengraph-image.png','twitter-image.png','opengraph-image.alt.txt','twitter-image.alt.txt']) {
    const url=native.origin+native.mount+'/'+name, response=await request.get(url,{headers:{'Accept-Encoding':'identity'}});
    expect(response.status()).toBe(200);const data=await response.body(), headers=response.headers(), hash=createHash('sha256').update(data).digest('hex');
    expect(headers['content-type']).toBe(name.endsWith('.png')?'image/png':'text/plain; charset=utf-8');expect(headers['x-content-type-options']).toBe('nosniff');
    if(name.endsWith('.png')) {expect(data.subarray(0,8).toString('hex')).toBe('89504e470d0a1a0a');expect(data.readUInt32BE(16)).toBe(1200);expect(data.readUInt32BE(20)).toBe(630);}
    else expect(data.toString('utf8').trim()).toContain('Original Go bitmap card');
    if(socialTransport==='static') expect(headers['cache-control']).toBe('no-cache');
    else {
      expect(headers.etag).toBe('"'+hash+'"');expect(headers['cache-control']).toBe('public, max-age=0, must-revalidate');
      const conditional=await request.get(url,{headers:{'Accept-Encoding':'identity','If-None-Match':'W/"'+hash+'"'}});expect(conditional.status()).toBe(304);expect((await conditional.body()).length).toBe(0);
    }
    const head=await request.head(url,{headers:{'Accept-Encoding':'identity'}});expect(head.status()).toBe(200);expect((await head.body()).length).toBe(0);expect(head.headers()['content-length']).toBe(String(data.length));
    const method=await request.post(url);expect(method.status()).toBe(405);expect(method.headers().allow).toBe('GET, HEAD');
  }
});
