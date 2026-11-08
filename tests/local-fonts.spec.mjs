import {test as base, expect} from '@playwright/test';
import {createHash} from 'node:crypto';
import {startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';
import {startLocalFontsStaticDeployment} from './local-fonts-static-fixture.mjs';

const test = base.extend({
  fontTransport: ['http', {option: true}],
  native: async ({page, fontTransport}, use, info) => {
    const fixture = fontTransport === 'static' ? await startLocalFontsStaticDeployment() : await startMetadataDeployment(fontTransport, {
      binary: process.env.NEXTGO_E2E_SERVER_BINARY,
      sha256: process.env.NEXTGO_METADATA_STREAM_SERVER_SHA256,
      port: 4282,
    });
    fixture.fontPath = (fontTransport === 'http' ? '' : '/lambda-features') + '/local-fonts';
    try {await use(fixture);} finally {
      const errors = [];
      try {await page.close();} catch (error) {errors.push(error);}
      try {await fixture.close();} catch (error) {errors.push(error);}
      const proof = {transport: fontTransport, binary: process.env.NEXTGO_E2E_SERVER_BINARY,
        sha256: process.env.NEXTGO_METADATA_STREAM_SERVER_SHA256,
        cleanup: fixture.cleanupProof, artifact: fixture.artifact ?? null, runtime: fixture.runtimeProof?.() ?? null, sourceRequests: fixture.sourceRecords.length};
      await info.attach('local-fonts-process-and-transport-proof', {body: JSON.stringify(proof), contentType: 'application/json'});
      expect(proof.sourceRequests).toBe(0);
      for (const [key, value] of Object.entries(proof.cleanup)) expect(value, key).toBe(key === 'runtimeGatewayClosed' ? fontTransport !== 'http' : true);
      if (errors.length) throw new AggregateError(errors, 'local-fonts owned fixture cleanup failed');
    }
  },
});
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
async function fontsDecoded(page) {
  const proof = await page.evaluate(async () => {
    await document.fonts.ready;
    const fonts = [...document.fonts].filter(face => face.family.includes('Nextgo Fixture')).map(face => ({family: face.family.replaceAll('"', ''), status: face.status}));
    const canvas = document.createElement('canvas'), context = canvas.getContext('2d');
    const widths = ['Nextgo Fixture', 'Nextgo Fixture WOFF'].map(family => {context.font = `72px "${family}"`; return context.measureText('GO').width;});
    return {fonts, widths};
  });
  expect(proof.fonts).toHaveLength(2);
  expect(proof.fonts.every(font => font.status === 'loaded')).toBe(true);
  for (const width of proof.widths) expect(width).toBeCloseTo(2 * 760 * 72 / 1000, 1);
  return proof;
}

test.describe('without JavaScript', () => {
  test.use({javaScriptEnabled: false});
  test('both original font formats decode from native HTML without application JavaScript', async ({page, native}, info) => {
    const wasm = []; page.on('request', request => {if (/\.wasm(?:\?|$)/.test(request.url())) wasm.push(request.url());});
    const response = await page.goto(native.origin + native.fontPath);
    expect(response.status()).toBe(200); expect(response.headers()['x-nextgo-requires-server']).not.toBe('1');
    await expect(page.locator('[data-font-woff2]')).toHaveText('GO GO');
    await expect(page.locator('[data-font-woff]')).toHaveText('GO GO');
    const proof = await fontsDecoded(page);
    await info.attach('actual-original-font-decode-proof', {body: JSON.stringify(proof), contentType: 'application/json'});
    expect(wasm).toEqual([]);
  });
});

test('Go navigation owns font head links and decodes both formats again on history return', async ({page, native}) => {
  await page.goto(native.origin + native.fontPath); await ready(page); await fontsDecoded(page);
  const links = page.locator('head link[href*="/_nextgo/local-fonts/"]');
  await expect(links).toHaveCount(3);
  await page.evaluate(() => {globalThis.fontFixtureDocument = document;});
  await page.locator('[data-local-fonts-exit]').click(); await expect(page).toHaveTitle('Architecture — Next.go'); await ready(page);
  await expect(links).toHaveCount(0); expect(await page.evaluate(() => fontFixtureDocument === document)).toBe(true);
  await page.goBack(); await expect(page).toHaveTitle('Frozen local fonts — Next.go'); await ready(page);
  await expect(links).toHaveCount(3); await fontsDecoded(page);
  expect(await page.evaluate(() => fontFixtureDocument === document)).toBe(true);
});

test('actual CSS and font bytes match URL hashes, MIME, HEAD and transport header contracts', async ({page, request, native, fontTransport}) => {
  await page.goto(native.origin + native.fontPath);
  const urls = await page.locator('head link[href*="/_nextgo/local-fonts/"]').evaluateAll(links => links.map(link => link.href));
  expect(urls).toHaveLength(3);
  for (const url of urls) {
    const response = await request.get(url, {headers: {'Accept-Encoding': 'identity'}}); expect(response.status()).toBe(200);
    const body = await response.body(), headers = response.headers(), digest = createHash('sha256').update(body).digest('hex');
    expect(new URL(url).pathname.split('/').at(-1).split('.')[0]).toBe(digest);
    const extension = new URL(url).pathname.split('.').at(-1);
    expect(headers['content-type']).toBe(extension === 'css' ? 'text/css; charset=utf-8' : 'font/' + extension);
    expect(headers['x-content-type-options']).toBe('nosniff');
    if (fontTransport === 'static') {
      // Plain static hosting owns headers; this fixture deliberately uses its
      // existing no-cache policy and adds no synthetic framework validators.
      expect(headers['cache-control']).toBe('no-cache');
    } else {
      expect(headers.etag).toBe('"' + digest + '"'); expect(headers['cache-control']).toContain('immutable');
      expect(headers['access-control-allow-origin']).toBe('*');
      const conditional = await request.get(url, {headers: {'Accept-Encoding': 'identity', 'If-None-Match': 'W/"' + digest + '"'}});
      expect(conditional.status()).toBe(304); expect((await conditional.body()).length).toBe(0);
    }
    const head = await request.head(url, {headers: {'Accept-Encoding': 'identity'}}); expect(head.status()).toBe(200); expect((await head.body()).length).toBe(0); expect(head.headers()['content-length']).toBe(String(body.length));
    const rejected = await request.post(url); expect(rejected.status()).toBe(405); expect(rejected.headers().allow).toBe('GET, HEAD');
  }
});
