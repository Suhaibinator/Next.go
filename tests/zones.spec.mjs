import {test, expect} from '@playwright/test';
import {createHash, randomUUID} from 'node:crypto';

const publicOrigin = 'http://127.0.0.1:4230';
const alpha = 'http://127.0.0.1:4234', beta = 'http://127.0.0.1:4235';
const nativeOrigin = 'http://127.0.0.1:4231';
const zones = [{id: 'marketing', base: '', step: 1}, {id: 'blog', base: '/blog', step: 10}];
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const counter = (page, id) => page.locator(`[data-zone-counter="${id}"]`);
const value = (page, id) => counter(page, id).locator('[data-zone-count]');
const proof = (page, id) => counter(page, id).locator('[data-zone-client]');
const control = (operation, key = '') => `${publicOrigin}/_fixture/zones/${operation}?${new URLSearchParams({key})}`;
const traceHeaders = (trace, extra = {}) => ({'X-Fixture-Zones-Trace': trace, ...extra});
async function records(request, trace) {return (await (await request.get(control('records'))).json()).filter(record => record.trace === trace);}
async function held(request, key) {await expect.poll(async () => (await (await request.get(control('gate', key))).json()).held).toBe(true);}
async function action(request, origin, base, note, headers = {}) {
  return request.post(`${origin}${base}/_nextgo/actions/preview`, {headers: {Accept: 'application/json', Origin: origin, ...headers}, form: {note}});
}
async function status(request, origin, base = '', headers = {}) {const response = await request.get(`${origin}${base}/api/status`, {headers}); expect(response.status()).toBe(200); return response.json();}
async function record(request, origin, headers = {}) {const response = await request.get(`${origin}/api/record`, {headers}); expect(response.status()).toBe(200); return response.json();}
async function cached(request, base, id) {const response = await request.get(`${publicOrigin}${base}/cached`); expect(response.status()).toBe(200); const html = await response.text(); expect(html).toContain(`data-zone-cache="${id}"`); return html.match(new RegExp(`data-zone-cache="${id}"[^>]*>([^<]*)`))?.[1];}
async function assume(page, origin, subject) {
  await page.goto(origin + '/'); await ready(page);
  const arrived = page.waitForResponse(response => response.request().isNavigationRequest() && response.request().method() === 'GET' && new URL(response.url()).pathname === '/dashboard');
  await page.locator(`[data-tenant-assume] button[value="${subject}"]`).click();
  expect((await arrived).status()).toBe(200); await ready(page);
}

for (const zone of zones) test(`${zone.id} sends useful SSR before its own real WASM hydrates the same counter nodes`, async ({browser, page, request}) => {
  const native = await browser.newContext({javaScriptEnabled: false});
  try {
    const noJS = await native.newPage(); expect((await noJS.goto(publicOrigin + (zone.base || '/'))).status()).toBe(200);
    await expect(noJS.locator(`[data-zone-page="${zone.id}"]`)).toBeVisible();
    await expect(value(noJS, zone.id)).toHaveText('1'); await expect(proof(noJS, zone.id)).toHaveText('Waiting for Go');
    await expect(counter(noJS, zone.id).locator('[data-zone-increment]')).toBeDisabled();
  } finally {await native.close();}
  const trace = randomUUID(), key = `${trace}-wasm`;
  await page.setExtraHTTPHeaders(traceHeaders(trace, {'X-Fixture-Zones-Wasm': key}));
  try {
    await page.goto(zone.base || '/', {waitUntil: 'commit'}); await held(request, key);
    await expect(value(page, zone.id)).toHaveText('1');
    await page.evaluate(id => {window.zoneSaved = document.querySelector(`[data-zone-counter="${id}"]`); window.zoneText = window.zoneSaved.querySelector('[data-zone-count]').firstChild;}, zone.id);
    expect((await request.post(control('release', key))).status()).toBe(200); await ready(page);
    await expect(proof(page, zone.id)).toHaveText(zone.id);
    expect(await page.evaluate(id => window.zoneSaved === document.querySelector(`[data-zone-counter="${id}"]`) && window.zoneText === window.zoneSaved.querySelector('[data-zone-count]').firstChild, zone.id)).toBe(true);
    await counter(page, zone.id).locator('[data-zone-increment]').click(); await expect(value(page, zone.id)).toHaveText(String(1 + zone.step));
  } finally {await request.post(control('release', key));}
});

for (const zone of zones) test(`${zone.id} stays in one Go document through local routes, query, dynamic parameters and history`, async ({page}) => {
  await page.goto((zone.base || '/') + '?start=one'); await ready(page);
  await page.evaluate(id => {window.zoneDocument = true; window.zoneLayout = document.querySelector(`[data-zone-layout="${id}"]`);}, zone.id);
  await counter(page, zone.id).locator('[data-zone-increment]').click();
  await page.getByRole('link', {name: 'Same-zone details', exact: true}).click();
  await expect(page.locator(`[data-zone-details="${zone.id}"]`)).toBeVisible();
  await page.goBack(); await expect(page.locator(`[data-zone-page="${zone.id}"]`)).toBeVisible();
  await page.getByRole('link', {name: 'Read a dynamic route', exact: true}).evaluate((node, base) => {node.href = `${base}/posts/welcome?read=two#part`;}, zone.base);
  await page.getByRole('link', {name: 'Read a dynamic route', exact: true}).click();
  await expect(page).toHaveURL(`${publicOrigin}${zone.base}/posts/welcome?read=two#part`);
  await expect(page.locator(`[data-zone-post="${zone.id}"]`)).toHaveAttribute('data-zone-slug', 'welcome');
  await page.goBack(); await expect(page).toHaveURL(`${publicOrigin}${zone.base || '/'}?start=one`);
  await page.goForward(); await expect(page.locator('[data-zone-slug]')).toHaveAttribute('data-zone-slug', 'welcome');
  await expect(value(page, zone.id)).toHaveText(String(1 + zone.step)); await expect(proof(page, zone.id)).toHaveText(zone.id);
  expect(await page.evaluate(id => window.zoneDocument && window.zoneLayout === document.querySelector(`[data-zone-layout="${id}"]`), zone.id)).toBe(true);
});

test('cross-zone anchors make real document navigations with a new Go bundle and reset state without wrong-zone intent fetches', async ({page, request}) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(traceHeaders(trace));
  await page.goto('/'); await ready(page);
  const marketingRuntime = await page.locator('body').getAttribute('data-nextgo-runtime');
  await counter(page, 'marketing').locator('[data-zone-increment]').click(); await expect(value(page, 'marketing')).toHaveText('2');
  await page.evaluate(() => {window.zoneOldDocument = 'marketing';});
  for (const target of [{path: '/blog', id: 'blog', step: 10}, {path: '/', id: 'marketing', step: 1}]) {
    const cross = page.locator('[data-zone-cross-link]'); await cross.hover(); await cross.focus();
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
    const arrived = page.waitForResponse(response => response.request().isNavigationRequest() && new URL(response.url()).pathname === target.path);
    await cross.click(); expect((await arrived).status()).toBe(200); await ready(page);
    expect(await page.evaluate(() => window.zoneOldDocument)).toBeUndefined();
    await expect(proof(page, target.id)).toHaveText(target.id); await expect(value(page, target.id)).toHaveText('1');
    const runtime = await page.locator('body').getAttribute('data-nextgo-runtime');
    if (target.id === 'blog') expect(runtime).not.toBe(marketingRuntime); else expect(runtime).toBe(marketingRuntime);
    await counter(page, target.id).locator('[data-zone-increment]').click(); await expect(value(page, target.id)).toHaveText(String(1 + target.step));
    await page.evaluate(id => {window.zoneOldDocument = id;}, target.id);
  }
  const crossFetches = (await records(request, trace)).filter(entry => (entry.navigate || entry.prefetch) && ['/', '/blog'].includes(entry.path));
  expect(crossFetches).toHaveLength(0);
});

test('direct dynamic URLs and owned custom404 never fall through into another application', async ({page, request}) => {
  for (const zone of zones) {
    const response = await page.goto(`${zone.base}/posts/ownership?direct=yes`); expect(response.status()).toBe(200); await ready(page);
    await expect(page.locator(`[data-zone-post="${zone.id}"]`)).toHaveAttribute('data-zone-slug', 'ownership');
    await expect(proof(page, zone.id)).toHaveText(zone.id);
    const missing = await request.get(`${zone.base}/posts/no-such-post`); expect(missing.status()).toBe(404);
    const html = await missing.text(); expect(html).toContain(`data-zone-not-found="${zone.id}"`); expect(html).not.toContain(`data-zone-not-found="${zone.id === 'blog' ? 'marketing' : 'blog'}"`);
  }
  const sibling = await request.get('/blogger'); expect(sibling.status()).toBe(404); expect(await sibling.text()).toContain('data-zone-not-found="marketing"');
});

test('the single native binary owns different public styles and WASM asset bags at each mount', async ({request}) => {
  const assets = [];
  for (const zone of zones) {
    const response = await request.get(zone.base || '/'); expect(response.status()).toBe(200);
    const runtime = (await response.text()).match(/data-nextgo-runtime="([^"]+)"/)?.[1]; expect(runtime).toBeTruthy();
    const wasm = await request.get(runtime + 'app.wasm'); expect(wasm.status()).toBe(200); expect(wasm.headers()['content-type']).toContain('application/wasm');
    const style = await request.get(`${zone.base}/assets/style.css`); expect(style.status()).toBe(200); expect(style.headers()['content-type']).toContain('text/css');
    assets.push({runtime, digest: createHash('sha256').update(await wasm.body()).digest('hex'), style: await style.text()});
  }
  expect(assets[0].runtime).not.toBe(assets[1].runtime); expect(assets[0].digest).not.toBe(assets[1].digest); expect(assets[0].style).not.toBe(assets[1].style);
  expect((await request.get(assets[1].runtime.replace(/^\/blog/, '') + 'app.wasm')).status()).toBe(404);
  expect((await request.get('/blog' + assets[0].runtime + 'app.wasm')).status()).toBe(404);
});

test('identical API, native action, cache key and tag names remain isolated by selected application', async ({request}) => {
  const beforeM = await status(request, publicOrigin), beforeB = await status(request, publicOrigin, '/blog');
  expect(beforeM).toMatchObject({app: 'marketing', zone: 'marketing', tenant: '', basePath: ''});
  expect(beforeB).toMatchObject({app: 'blog', zone: 'blog', tenant: '', basePath: '/blog'});
  expect(await cached(request, '', 'marketing')).toBe(`marketing:${beforeM.revision}`); expect(await cached(request, '/blog', 'blog')).toBe(`blog:${beforeB.revision}`);
  const m = await action(request, publicOrigin, '', 'public marketing preview'); expect(m.status()).toBe(200);
  expect((await m.json()).data).toMatchObject({app: 'marketing', zone: 'marketing', revision: beforeM.revision + 1});
  expect((await status(request, publicOrigin, '/blog')).revision).toBe(beforeB.revision);
  await expect.poll(() => cached(request, '', 'marketing')).toBe(`marketing:${beforeM.revision + 1}`);
  expect(await cached(request, '/blog', 'blog')).toBe(`blog:${beforeB.revision}`);
  const b = await action(request, publicOrigin, '/blog', 'public blog preview'); expect(b.status()).toBe(200);
  expect((await b.json()).data).toMatchObject({app: 'blog', zone: 'blog', revision: beforeB.revision + 1});
  expect((await status(request, publicOrigin)).revision).toBe(beforeM.revision + 1);
  await expect.poll(() => cached(request, '/blog', 'blog')).toBe(`blog:${beforeB.revision + 1}`);
  const native = await request.post(publicOrigin + '/blog/_nextgo/actions/preview', {headers: {Origin: publicOrigin}, form: {note: 'native blog redirect'}, maxRedirects: 0});
  expect(native.status()).toBe(303); expect(native.headers().location).toBe('/blog?preview=1');
});

test('actual loopback authorities select tenant context while unknown Host and forwarded selectors cannot do so', async ({page, request}) => {
  for (const [origin, id] of [[alpha, 'alpha'], [beta, 'beta']]) {
    await page.goto(origin + '/'); await ready(page); await expect(page.locator(`[data-tenant-home="${id}"]`)).toBeVisible();
    await expect(proof(page, id)).toHaveText('tenant'); await counter(page, id).locator('[data-zone-increment]').click(); await expect(value(page, id)).toHaveText('101');
    expect(await status(request, origin, '', {'X-Forwarded-Host': id === 'alpha' ? '127.0.0.1:4235' : '127.0.0.1:4234', 'X-Tenant': 'other', 'X-Nextgo-Zone': 'other'})).toMatchObject({app: 'tenant', zone: id, tenant: id, host: new URL(origin).host, basePath: ''});
  }
  expect(await status(request, publicOrigin, '', {'X-Forwarded-Host': '127.0.0.1:4234', Forwarded: 'host=127.0.0.1:4234;proto=https', 'X-Tenant': 'alpha'})).toMatchObject({app: 'marketing', zone: 'marketing', tenant: '', host: '127.0.0.1:4230'});
  // Bypass the fixture's listener/Host guard: this is the genuine aggregate
  // application's response, not a gateway-generated rejection.
  const unknown = await request.get(nativeOrigin + '/api/status', {headers: {Host: 'unmapped.example.test', 'X-Forwarded-Host': '127.0.0.1:4234'}});
  expect(unknown.status()).toBe(421); expect(unknown.headers()['cache-control']).toBe('no-store');
});

test('without a signed demo subject each selected tenant denies page, API and action independently', async ({browser}) => {
  const context = await browser.newContext({javaScriptEnabled: false});
  try {
    const page = await context.newPage();
    for (const origin of [alpha, beta]) {
      const response = await page.goto(origin + '/dashboard?subject=alice&tenant=alpha'); expect(response.status()).toBe(401);
      await expect(page.locator('[data-tenant-denial="401"]')).toBeVisible();
      const spoof = {'X-User': 'alice', 'X-Subject': 'alice', 'X-Tenant': 'alpha', 'X-Forwarded-Host': '127.0.0.1:4234'};
      expect((await context.request.get(origin + '/api/record?subject=alice&tenant=alpha', {headers: spoof})).status()).toBe(401);
      expect((await action(context.request, origin, '', 'public unauthenticated probe', spoof)).status()).toBe(401);
    }
  } finally {await context.close();}
});

test('a real signed synthetic Alice session authorizes alpha but never beta through headers or cookie tampering', async ({browser}) => {
  const context = await browser.newContext();
  try {
    const page = await context.newPage(); await assume(page, alpha, 'alice');
    await expect(page.locator('[data-tenant-dashboard="alpha"]')).toBeVisible(); await expect(page.locator('[data-tenant-authorized-subject]')).toHaveText('alice');
    const cookie = (await context.cookies(alpha)).find(value => value.name === 'nextgo-zones-demo'); expect(cookie).toBeTruthy(); expect(cookie.httpOnly).toBe(true);
    expect(await page.evaluate(() => document.cookie.includes('nextgo-zones-demo'))).toBe(false);
    const spoof = {'X-Forwarded-Host': '127.0.0.1:4234', Forwarded: 'host=127.0.0.1:4234', 'X-Tenant': 'alpha', 'X-Nextgo-Zone': 'alpha'};
    expect((await page.goto(beta + '/dashboard?tenant=alpha')).status()).toBe(403); await expect(page.locator('[data-tenant-denial="403"]')).toBeVisible();
    expect(await page.content()).not.toContain('alpha synthetic field notes');
    expect((await context.request.get(beta + '/api/record?tenant=alpha', {headers: spoof})).status()).toBe(403);
    expect((await action(context.request, beta, '', 'wrong membership probe', spoof)).status()).toBe(403);
    expect(await record(context.request, alpha, {'X-Forwarded-Host': '127.0.0.1:4235', 'X-Tenant': 'beta'})).toMatchObject({tenant: 'alpha', subject: 'alice'});
    const accepted = await action(context.request, alpha, '', 'actual alpha preview', {'X-Forwarded-Host': '127.0.0.1:4235', Forwarded: 'host=127.0.0.1:4235;proto=https', 'X-Tenant': 'beta', 'X-Nextgo-Zone': 'beta'}); expect(accepted.status()).toBe(200); expect((await accepted.json()).data).toMatchObject({tenant: 'alpha', zone: 'alpha', subject: 'alice'});
    const tampered = (cookie.value[0] === 'A' ? 'B' : 'A') + cookie.value.slice(1);
    await context.addCookies([{...cookie, value: tampered}]); expect((await context.request.get(alpha + '/api/record')).status()).toBe(401);
  } finally {await context.close();}
});

test('parallel Alice and Bob contexts read and mutate only their own tenant repositories', async ({browser}) => {
  const alice = await browser.newContext(), bob = await browser.newContext();
  try {
    const aPage = await alice.newPage(), bPage = await bob.newPage();
    await Promise.all([assume(aPage, alpha, 'alice'), assume(bPage, beta, 'bob')]);
    const [beforeA, beforeB] = await Promise.all([record(alice.request, alpha), record(bob.request, beta)]);
    expect(beforeA).toMatchObject({tenant: 'alpha', subject: 'alice', title: 'alpha synthetic field notes'}); expect(beforeB).toMatchObject({tenant: 'beta', subject: 'bob', title: 'beta synthetic field notes'});
    const [a, b] = await Promise.all([action(alice.request, alpha, '', 'alpha own preview'), action(bob.request, beta, '', 'beta own preview')]);
    expect(a.status()).toBe(200); expect(b.status()).toBe(200);
    expect((await a.json()).data).toMatchObject({tenant: 'alpha', zone: 'alpha', subject: 'alice', revision: beforeA.revision + 1});
    expect((await b.json()).data).toMatchObject({tenant: 'beta', zone: 'beta', subject: 'bob', revision: beforeB.revision + 1});
    const reads = await Promise.all(Array.from({length: 4}, () => Promise.all([record(alice.request, alpha), record(bob.request, beta)])));
    for (const [alphaRecord, betaRecord] of reads) {expect(alphaRecord).toMatchObject({tenant: 'alpha', subject: 'alice', revision: beforeA.revision + 1}); expect(betaRecord).toMatchObject({tenant: 'beta', subject: 'bob', revision: beforeB.revision + 1});}
    expect((await alice.request.get(beta + '/api/record')).status()).toBe(403); expect((await bob.request.get(alpha + '/api/record')).status()).toBe(403);
    const again = await action(alice.request, alpha, '', 'alpha only second preview'); expect(again.status()).toBe(200);
    expect((await record(bob.request, beta)).revision).toBe(beforeB.revision + 1);
  } finally {await Promise.all([alice.close(), bob.close()]);}
});
