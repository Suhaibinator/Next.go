import { test, expect } from '@playwright/test';

const publicURL = 'http://127.0.0.1:4149';
const cacheURL = 'http://127.0.0.1:4150';
const token = 'nextgo-shared-page-browser-public-fixture-token';
const path = slug => `/shared-pages/${slug}`;
const generation = page => page.locator('[data-shared-page-generation]');
const region = (page, which) => page.getByRole('region', { name: `Shared ${which} counter`, exact: true });
const count = (page, which) => region(page, which).locator('[data-count]');
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const generationFromHTML = html => html.match(/data-shared-page-generation="">([a-f0-9]{32})<\/code>/)?.[1];
const replicaHeaders = replica => ({ 'X-Fixture-Replica': replica });
async function cacheOperation(request, operation, values) {
  const response = await request.post(cacheURL, { headers: { Authorization: `Bearer ${token}` }, data: { Version: 3, Operation: operation, ...values } });
  expect(response.status()).toBe(200);
  return response.json();
}
async function markStale(request, selector = { Kind: 'tag', Value: 'shared-pages' }, window = 30_000_000_000) {
  return cacheOperation(request, 'mark-stale', { Namespace: 'demo', Selector: selector, Window: window });
}
function metaContent(html, name) {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const element = html.match(new RegExp(`<meta\\b(?=[^>]*\\bname="${escaped}")[^>]*>`))?.[0];
  return element?.match(/\bcontent="([^"]*)"/)?.[1];
}
async function snapshot(request, slug = 'alpha', replica = 'a', extra = {}) {
  const response = await request.get(`${publicURL}${path(slug)}`, { ...extra, headers: { ...replicaHeaders(replica), ...extra.headers } });
  const html = await response.text();
  const id = generationFromHTML(html);
  if (response.status() === 200) {
    expect(id).toMatch(/^[a-f0-9]{32}$/);
    expect(metaContent(html, 'shared-page-generation')).toBe(id);
    expect(html).not.toContain(token);
  }
  return { response, html, id };
}
async function expectHead(page, id) {
  await expect(page).toHaveTitle(new RegExp(id));
  await expect(page.locator('meta[name="shared-page-generation"]')).toHaveCount(1);
  await expect(page.locator('meta[name="shared-page-generation"]')).toHaveAttribute('content', id);
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', new RegExp(id));
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', new RegExp(id));
  await expect(page.locator('meta[property="og:description"]')).toHaveAttribute('content', new RegExp(id));
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
  await expect(page.locator('meta[name="viewport"]')).toHaveAttribute('content', /viewport-fit=cover/);
  await expect(page.locator('meta[name="theme-color"]')).toHaveAttribute('content', '#115e59');
  const structured = page.locator('head script[type="application/ld+json"][data-nextgo-head]');
  await expect(structured).toHaveCount(1);
  expect(JSON.parse(await structured.textContent())).toMatchObject({ '@context': 'https://schema.org', '@type': 'WebPage', identifier: id });
}
async function selectedSnapshot(request, slug = 'alpha') {
  const scopeResponse = await request.get(`${publicURL}/_fixture/cache-scope?path=${encodeURIComponent(path(slug))}`);
  expect(scopeResponse.status()).toBe(200);
  const scope = await scopeResponse.json();
  const selected = await cacheOperation(request, 'begin-stale', { ...scope, LeaseFor: 1_000_000_000 });
  const view = selected.View;
  if (view.Lease) await cacheOperation(request, 'release', { ID: scope.ID, Token: view.Lease });
  const stored = view.Found ? JSON.parse(Buffer.from(view.Value, 'base64').toString('utf8')) : undefined;
  return { busy: view.Busy, id: stored?.meta?.['shared-page-generation'] };
}
async function refreshedSnapshot(request, old, slug = 'alpha') {
  let selected;
  // Observe publication directly; an app read must not hide a broken refresh
  // worker by taking over the expired lease and rendering its own replacement.
  await expect.poll(async () => {
    selected = await selectedSnapshot(request, slug);
    return !selected.busy && typeof selected.id === 'string' && selected.id !== old;
  }).toBe(true);
  return selected.id;
}
async function postAction(request, action, slug = 'alpha', replica = 'a') {
  const response = await request.post(`${publicURL}/_nextgo/actions/${action}`, {
    maxRedirects: 0, headers: { ...replicaHeaders(replica), Origin: publicURL, Accept: 'text/html' }, form: { slug },
  });
  expect(response.status()).toBe(303);
  expect(response.headers().location).toBe(path(slug));
}

test.beforeEach(async ({ request }) => {
  await cacheOperation(request, 'expire', { Namespace: 'demo', Selector: { Kind: 'all', Value: '' } });
  expect((await request.post(`${publicURL}/_fixture/reset`)).status()).toBe(200);
});

test('cached body/head hydrate in place on separate replicas with independent Go counter state', async ({ page, browser }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  let otherContext;
  try {
    const first = await page.goto(path('alpha'), { waitUntil: 'domcontentloaded' });
    expect(first.status()).toBe(200); expect(first.headers()['x-nextgo-cache']).toBe('miss');
    const id = await generation(page).textContent();
    await expectHead(page, id);
    await expect(count(page, 'first')).toHaveText('3'); await expect(count(page, 'second')).toHaveText('10');
    await page.evaluate(() => {
      window.cachedSection = document.querySelector('[data-shared-page]');
      window.cachedIsland = document.querySelector('[aria-label="Shared first counter"]').closest('[data-nextgo-island]');
      window.cachedButton = window.cachedIsland.querySelector('[data-increment]');
      window.cachedText = window.cachedIsland.querySelector('[data-count]').firstChild;
    });
    otherContext = await browser.newContext({ extraHTTPHeaders: replicaHeaders('b') });
    const other = await otherContext.newPage();
    const second = await other.goto(`${publicURL}${path('alpha')}`);
    expect(second.status()).toBe(200); expect(second.headers()['x-nextgo-cache']).toBe('fresh');
    await expect(generation(other)).toHaveText(id); await expectHead(other, id); await ready(other);
    release(); await ready(page);
    expect(await page.evaluate(() => window.cachedSection === document.querySelector('[data-shared-page]') && window.cachedIsland === document.querySelector('[aria-label="Shared first counter"]').closest('[data-nextgo-island]') && window.cachedButton === window.cachedIsland.querySelector('[data-increment]') && window.cachedText === window.cachedIsland.querySelector('[data-count]').firstChild)).toBe(true);
    await region(page, 'first').getByRole('button', { name: 'Increase Shared first counter', exact: true }).click();
    await region(page, 'second').getByRole('button', { name: 'Decrease Shared second counter', exact: true }).click();
    await expect(count(page, 'first')).toHaveText('4'); await expect(count(page, 'second')).toHaveText('9');
    await expect(count(other, 'first')).toHaveText('3'); await expect(count(other, 'second')).toHaveText('10');
    await region(other, 'first').getByRole('button', { name: 'Increase Shared first counter', exact: true }).click();
    await expect(count(other, 'first')).toHaveText('4'); await expect(count(page, 'second')).toHaveText('9');
  } finally { release(); await otherContext?.close(); }
});

test('Go navigation reuses the frozen generation and resets page islands while retaining the layout', async ({ page }) => {
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  await page.goto(path('alpha')); await ready(page);
  const id = await generation(page).textContent();
  await page.locator('[data-layout-increment]').click();
  await region(page, 'first').getByRole('button', { name: 'Increase Shared first counter', exact: true }).click();
  await page.evaluate(() => {
    window.sharedPageDocument = 'retained'; window.sharedPageHeader = document.querySelector('header');
    window.oldSharedIsland = document.querySelector('[aria-label="Shared first counter"]').closest('[data-nextgo-island]');
    window.oldSharedButton = window.oldSharedIsland.querySelector('[data-increment]');
  });
  await page.getByRole('link', { name: 'Leave shared page', exact: true }).click();
  await expect(page).toHaveURL(`${publicURL}/shared-pages`);
  await expect(page.locator('meta[name="shared-page-generation"]')).toHaveCount(0);
  await expect(page.locator('head script[type="application/ld+json"][data-nextgo-head]')).toHaveCount(0);
  await expect(page.locator('meta[name="theme-color"]')).toHaveCount(0);
  await page.getByRole('link', { name: 'Open shared alpha', exact: true }).click();
  await expect(generation(page)).toHaveText(id); await expectHead(page, id);
  await expect(count(page, 'first')).toHaveText('3'); await expect(count(page, 'second')).toHaveText('10');
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  expect(await page.evaluate(() => {
    window.oldSharedButton.click();
    return window.sharedPageDocument === 'retained' && window.sharedPageHeader === document.querySelector('header') && !window.oldSharedIsland.isConnected && window.oldSharedIsland.querySelector('[data-count]').textContent === '4';
  })).toBe(true);
  await region(page, 'first').getByRole('button', { name: 'Increase Shared first counter', exact: true }).click();
  await expect(count(page, 'first')).toHaveText('4'); expect(errors).toEqual([]);
});

test('acknowledged path expiration replaces alpha on another replica without expiring beta', async ({ page, request }) => {
  const beta = await snapshot(request, 'beta');
  await page.goto(path('alpha')); await ready(page);
  const old = await generation(page).textContent();
  await page.getByRole('button', { name: 'Expire page path', exact: true }).click();
  await ready(page); await expect(generation(page)).not.toHaveText(old);
  const current = await generation(page).textContent(); await expectHead(page, current);
  expect((await snapshot(request, 'alpha', 'b')).id).toBe(current);
  expect((await snapshot(request, 'beta', 'b')).id).toBe(beta.id);
});

test('acknowledged group-tag expiration retires both route snapshots', async ({ request }) => {
  const alpha = await snapshot(request, 'alpha'); const beta = await snapshot(request, 'beta', 'b');
  await postAction(request, 'shared-page-tag-expire');
  const nextAlpha = await snapshot(request, 'alpha', 'b'); const nextBeta = await snapshot(request, 'beta');
  expect(nextAlpha.id).not.toBe(alpha.id); expect(nextBeta.id).not.toBe(beta.id);
  expect(nextAlpha.response.headers()['x-nextgo-cache']).toBe('miss');
  expect(nextBeta.response.headers()['x-nextgo-cache']).toBe('miss');
  expect((await snapshot(request, 'alpha')).id).toBe(nextAlpha.id);
});

test('Cookie, Authorization and GET bodies bypass shared storage without poisoning its public generation', async ({ request }) => {
  const initial = await snapshot(request);
  const before = await (await request.get(`${publicURL}/_fixture/stats`)).json();
  for (const options of [{ headers: { Cookie: 'private=1' } }, { headers: { Authorization: 'Bearer private-fixture-identity' } }, { data: 'request-body' }]) {
    const response = await request.fetch(`${publicURL}${path('alpha')}`, { method: 'GET', ...options, headers: { ...replicaHeaders('b'), ...options.headers } });
    expect(response.status()).toBe(200);
    const html = await response.text(); expect(generationFromHTML(html)).not.toBe(initial.id);
    expect(response.headers()['cache-control']).toContain('no-store');
    expect(response.headers()['x-nextgo-cache']).toBeUndefined();
    const failed = await request.fetch(`${publicURL}${path('alpha')}`, { method: 'GET', ...options, headers: { ...replicaHeaders('fail'), ...options.headers } });
    expect(failed.status()).toBe(500); expect(await failed.text()).not.toContain(initial.id);
  }
  const after = await (await request.get(`${publicURL}/_fixture/stats`)).json();
  expect(after.pageReads).toBe(before.pageReads);
  const publicRead = await snapshot(request, 'alpha', 'fail');
  expect(publicRead.id).toBe(initial.id); expect(publicRead.response.headers()['x-nextgo-cache']).toBe('fresh');
});

test('query variants have separate snapshots while retaining consistent body and head data', async ({ request }) => {
  const plain = await snapshot(request);
  const read = async variant => {
    const response = await request.get(`${publicURL}${path('alpha')}?variant=${variant}`, { headers: replicaHeaders('b') });
    expect(response.status()).toBe(200); const html = await response.text(); const id = generationFromHTML(html);
    expect(id).toMatch(/^[a-f0-9]{32}$/); expect(metaContent(html, 'shared-page-generation')).toBe(id); return id;
  };
  const first = await read('one'); const second = await read('two');
  expect(first).not.toBe(plain.id); expect(second).not.toBe(first); expect(second).not.toBe(plain.id);
  expect(await read('one')).toBe(first); expect((await snapshot(request)).id).toBe(plain.id);
});

test('profile stale marking delivers the old complete snapshot before a bounded refresh publishes its replacement', async ({ page, request }) => {
  await page.goto(path('alpha')); await ready(page); const old = await generation(page).textContent();
  const redirected = page.waitForResponse(response => response.url() === `${publicURL}${path('alpha')}` && response.request().isNavigationRequest() && response.request().method() === 'GET');
  await page.getByRole('button', { name: 'Mark page stale', exact: true }).click();
  expect((await redirected).headers()['x-nextgo-cache']).toBe('stale');
  await expect(generation(page)).toHaveText(old); await expectHead(page, old); await ready(page);
  const next = await refreshedSnapshot(request, old);
  expect((await snapshot(request, 'alpha', 'b')).id).toBe(next);
  await expect(generation(page)).toHaveText(old); await expectHead(page, old);
  await page.getByRole('link', { name: 'Leave shared page', exact: true }).click();
  await page.getByRole('link', { name: 'Open shared alpha', exact: true }).click();
  await expect(generation(page)).toHaveText(next); await expectHead(page, next);
});

test('a failing render retains last-good content only until hard expiry and a healthy replica repairs it', async ({ page, request }) => {
  const initial = await snapshot(request);
  await page.setExtraHTTPHeaders(replicaHeaders('fail'));
  const cached = await page.goto(path('alpha')); expect(cached.headers()['x-nextgo-cache']).toBe('fresh');
  await expect(generation(page)).toHaveText(initial.id); await expectHead(page, initial.id); await ready(page);
  await markStale(request);
  const stale = await page.reload(); expect(stale.headers()['x-nextgo-cache']).toBe('stale');
  await expect(generation(page)).toHaveText(initial.id); await expectHead(page, initial.id); await ready(page);
  await expect.poll(() => selectedSnapshot(request)).toEqual({ busy: false, id: initial.id });
  const retained = await snapshot(request, 'alpha', 'fail');
  expect(retained.id).toBe(initial.id); expect(retained.response.headers()['x-nextgo-cache']).toBe('stale');
  await expect.poll(() => selectedSnapshot(request)).toEqual({ busy: false, id: initial.id });
  await markStale(request, { Kind: 'prefix', Value: path('alpha') }, 1_000_000);
  await expect.poll(async () => (await snapshot(request, 'alpha', 'fail')).response.status()).toBe(500);
  const failed = await page.reload(); expect(failed.status()).toBe(500);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Something went wrong');
  await expect(page.locator('meta[name="shared-page-generation"]')).toHaveCount(0);
  expect(await page.content()).not.toContain(initial.id);
  await page.setExtraHTTPHeaders(replicaHeaders('b'));
  const repaired = await page.goto(path('alpha')); expect(repaired.status()).toBe(200);
  await ready(page); await expect(generation(page)).not.toHaveText(initial.id);
  const next = await generation(page).textContent(); await expectHead(page, next);
  expect((await snapshot(request)).id).toBe(next);
});

test.describe('cached pages without JavaScript', () => {
  test.use({ javaScriptEnabled: false });
  test('native forms return a new complete snapshot while counters remain useful server HTML', async ({ page, request }) => {
    const beta = await snapshot(request, 'beta');
    await page.goto(path('alpha')); const old = await generation(page).textContent();
    await expectHead(page, old); await expect(count(page, 'first')).toHaveText('3'); await expect(count(page, 'second')).toHaveText('10');
    await expect(region(page, 'first').getByRole('button', { name: 'Increase Shared first counter', exact: true })).toBeDisabled();
    await page.getByRole('button', { name: 'Expire page path', exact: true }).click();
    await expect(generation(page)).not.toHaveText(old); const next = await generation(page).textContent();
    await expectHead(page, next); expect((await snapshot(request, 'alpha', 'b')).id).toBe(next);
    expect((await snapshot(request, 'beta', 'b')).id).toBe(beta.id);
  });
});
