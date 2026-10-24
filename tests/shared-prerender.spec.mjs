import { test, expect } from '@playwright/test';

const origin = 'http://127.0.0.1:4155';
const cacheURL = 'http://127.0.0.1:4156';
const token = 'nextgo-shared-prerender-public-fixture-token';
const route = slug => `/shared-prerender/${slug}`;
const headers = replica => ({ 'X-Fixture-Replica': replica });
const generation = page => page.locator('[data-shared-prerender-generation]');
const region = (page, which) => page.getByRole('region', { name: `Prerender ${which} counter`, exact: true });
const count = (page, which) => region(page, which).locator('[data-count]');
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const fromHTML = html => html.match(/data-shared-prerender-generation="">([a-f0-9]{32})<\/code>/)?.[1];
async function control(request, action, values = {}) {
  const response = await request.post(`${origin}/_fixture/${action}`, { data: values, timeout: 150_000 });
  expect(response.status(), await response.text()).toBe(200); return response.json();
}
async function cacheOperation(request, operation, values) {
  const response = await request.post(cacheURL, { headers: { Authorization: `Bearer ${token}` }, data: { Version: 3, Operation: operation, Namespace: 'demo', ...values } });
  expect(response.status()).toBe(200); return response.json();
}
async function read(request, slug = 'alpha', replica = 'fail', options = {}) {
  const response = await request.get(`${origin}${route(slug)}`, { ...options, headers: { ...headers(replica), ...options.headers } });
  const html = await response.text(); const id = fromHTML(html);
  if (response.status() === 200) { expect(id).toMatch(/^[a-f0-9]{32}$/); expect(html).not.toContain(token); }
  return { response, html, id };
}
async function head(page, id) {
  await expect(page).toHaveTitle(new RegExp(id));
  await expect(page.locator('meta[name="shared-prerender-generation"]')).toHaveCount(1);
  await expect(page.locator('meta[name="shared-prerender-generation"]')).toHaveAttribute('content', id);
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', new RegExp(id));
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', new RegExp(id));
  await expect(page.locator('meta[property="og:description"]')).toHaveAttribute('content', new RegExp(id));
  await expect(page.locator('html')).toHaveAttribute('lang', 'en'); await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
  const structured = page.locator('head script[type="application/ld+json"][data-nextgo-head]');
  await expect(structured).toHaveCount(1); expect(JSON.parse(await structured.textContent())).toMatchObject({ '@type': 'WebPage', identifier: id });
}
async function expire(request, kind = 'tag', value = 'shared-prerender') {
  return cacheOperation(request, 'expire', { Selector: { Kind: kind, Value: value } });
}
test.beforeEach(async ({ request }) => { await control(request, 'reset'); });

test('real online build seeds useful failed-source HTML and hydration preserves island nodes', async ({ page, browser }) => {
  let release; const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async intercepted => { await gate; await intercepted.continue(); });
  let otherContext;
  try {
    const response = await page.goto(route('alpha'), { waitUntil: 'domcontentloaded' });
    expect(response.status()).toBe(200); expect(response.headers()['x-nextgo-prerender']).toBe('build');
    const id = await generation(page).textContent(); await head(page, id);
    await expect(count(page, 'first')).toHaveText('3'); await expect(count(page, 'second')).toHaveText('10');
    await page.evaluate(() => {
      window.seedSection = document.querySelector('[data-shared-prerender]');
      window.seedIsland = document.querySelector('[aria-label="Prerender first counter"]').closest('[data-nextgo-island]');
      window.seedButton = window.seedIsland.querySelector('[data-increment]'); window.seedText = window.seedIsland.querySelector('[data-count]').firstChild;
    });
    const touch = test.info().project.name.endsWith('touch') ? { viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true } : {};
    otherContext = await browser.newContext({ extraHTTPHeaders: headers('healthy'), ...touch });
    const other = await otherContext.newPage(); const otherResponse = await other.goto(`${origin}${route('alpha')}`);
    expect(otherResponse.status()).toBe(200); expect(otherResponse.headers()['x-nextgo-prerender']).toBe('build');
    await expect(generation(other)).toHaveText(id); await head(other, id); await ready(other);
    release(); await ready(page);
    expect(await page.evaluate(() => window.seedSection === document.querySelector('[data-shared-prerender]') && window.seedIsland === document.querySelector('[aria-label="Prerender first counter"]').closest('[data-nextgo-island]') && window.seedButton === window.seedIsland.querySelector('[data-increment]') && window.seedText === window.seedIsland.querySelector('[data-count]').firstChild)).toBe(true);
    await region(page, 'first').getByRole('button', { name: 'Increase Prerender first counter', exact: true }).click();
    await region(page, 'second').getByRole('button', { name: 'Decrease Prerender second counter', exact: true }).click();
    await expect(count(page, 'first')).toHaveText('4'); await expect(count(page, 'second')).toHaveText('9');
    await expect(count(other, 'first')).toHaveText('3'); await expect(count(other, 'second')).toHaveText('10');
  } finally { release(); await otherContext?.close(); }
});

test('client navigation and history keep seeded head ownership and reset page-local counters', async ({ page, request }) => {
  const alpha = await read(request); const beta = await read(request, 'beta');
  expect(alpha.response.headers()['x-nextgo-prerender']).toBe('build'); expect(beta.response.headers()['x-nextgo-prerender']).toBe('build');
  await page.goto(route('alpha')); await ready(page);
  await page.locator('[data-layout-increment]').click();
  await region(page, 'first').getByRole('button', { name: 'Increase Prerender first counter', exact: true }).click();
  await page.evaluate(() => {
    window.seedDocument = 'retained'; window.seedHeader = document.querySelector('header');
    window.oldSeedIsland = document.querySelector('[aria-label="Prerender first counter"]').closest('[data-nextgo-island]'); window.oldSeedButton = window.oldSeedIsland.querySelector('[data-increment]');
  });
  await page.getByRole('link', { name: 'Open beta', exact: true }).click();
  await expect(page).toHaveURL(`${origin}${route('beta')}`); await expect(generation(page)).toHaveText(beta.id); await head(page, beta.id);
  await expect(count(page, 'first')).toHaveText('3');
  await page.goBack(); await expect(generation(page)).toHaveText(alpha.id); await head(page, alpha.id);
  await expect(count(page, 'first')).toHaveText('3'); await expect(page.locator('[data-layout-count]')).toHaveText('1');
  expect(await page.evaluate(() => { window.oldSeedButton.click(); return window.seedDocument === 'retained' && window.seedHeader === document.querySelector('header') && !window.oldSeedIsland.isConnected && window.oldSeedIsland.querySelector('[data-count]').textContent === '4'; })).toBe(true);
  await page.goForward(); await expect(generation(page)).toHaveText(beta.id); await head(page, beta.id);
});

test('unlisted fallback needs a healthy source, then its runtime generation is shared with the failed replica', async ({ page, request }) => {
  expect((await read(request, 'new')).response.status()).toBe(500);
  await page.setExtraHTTPHeaders(headers('healthy'));
  const response = await page.goto(route('new')); expect(response.status()).toBe(200); expect(response.headers()['x-nextgo-prerender']).toBe('runtime');
  const id = await generation(page).textContent(); await head(page, id); await ready(page);
  await region(page, 'first').getByRole('button', { name: 'Increase Prerender first counter', exact: true }).click(); await expect(count(page, 'first')).toHaveText('4');
  const same = await read(request, 'new'); expect(same.response.status()).toBe(200); expect(same.id).toBe(id); expect(same.response.headers()['x-nextgo-prerender']).toBe('runtime');
});

test('durable authority and old-artifact application restarts preserve the same seeded generation', async ({ page, request }) => {
  await page.goto(route('alpha')); await ready(page); const id = await generation(page).textContent(); await head(page, id);
  await control(request, 'restart-cache');
  const afterCache = await read(request); expect(afterCache.response.status()).toBe(200); expect(afterCache.id).toBe(id); expect(afterCache.response.headers()['x-nextgo-prerender']).toBe('build');
  await control(request, 'restart-apps');
  const response = await page.reload(); expect(response.status()).toBe(200); expect(response.headers()['x-nextgo-prerender']).toBe('build');
  await expect(generation(page)).toHaveText(id); await head(page, id); await ready(page);
});

test('acknowledged expiration survives both restarts and an old artifact cannot resurrect either seed', async ({ page, request }) => {
  const alpha = await read(request); const beta = await read(request, 'beta');
  const action = await request.post(`${origin}/_nextgo/actions/shared-prerender-tag-expire`, { maxRedirects: 0, headers: { Origin: origin, Accept: 'text/html' }, form: { slug: 'alpha' } });
  expect(action.status()).toBe(303); expect(action.headers().location).toBe(route('alpha'));
  await control(request, 'restart-all');
  const missingAlpha = await read(request); const missingBeta = await read(request, 'beta');
  expect(missingAlpha.response.status()).toBe(500); expect(missingBeta.response.status()).toBe(500);
  expect(missingAlpha.html).not.toContain(alpha.id); expect(missingBeta.html).not.toContain(beta.id);
  await page.setExtraHTTPHeaders(headers('healthy')); const recovered = await page.goto(route('alpha'));
  expect(recovered.status()).toBe(200); expect(recovered.headers()['x-nextgo-prerender']).toBe('runtime');
  await ready(page); await expect(generation(page)).not.toHaveText(alpha.id); const current = await generation(page).textContent(); await head(page, current);
  expect((await read(request)).id).toBe(current);
});

test('query, credential and request-body variants bypass public build data without poisoning it', async ({ request }) => {
  const seeded = await read(request); expect(seeded.response.status()).toBe(200);
  for (const options of [{ headers: { Cookie: 'private=1' } }, { headers: { Authorization: 'Bearer private-prerender-identity' } }, { data: 'request-body' }]) {
    const failed = await request.fetch(`${origin}${route('alpha')}`, { method: 'GET', ...options });
    expect(failed.status()).toBe(500); expect(await failed.text()).not.toContain(seeded.id);
  }
  const query = await request.get(`${origin}${route('alpha')}?private=1`); expect(query.status()).toBe(500); expect(await query.text()).not.toContain(seeded.id);
  const same = await read(request); expect(same.response.status()).toBe(200); expect(same.id).toBe(seeded.id); expect(same.response.headers()['x-nextgo-prerender']).toBe('build');
});

test('a stale seed can regenerate through bounded healthy completion work without a blocking-reader substitute', async ({ page, request }) => {
  const seeded = await read(request); expect(seeded.response.status()).toBe(200);
  await cacheOperation(request, 'mark-stale', { Selector: { Kind: 'tag', Value: 'shared-prerender' }, Window: 30_000_000_000 });
  const stale = await read(request, 'alpha', 'healthy'); expect(stale.response.status()).toBe(200); expect(stale.response.headers()['x-nextgo-cache']).toBe('stale'); expect(stale.id).toBe(seeded.id);
  // Only the failing source is polled after scheduling. It cannot render a
  // replacement itself, so a changed generation proves the healthy worker ran.
  let replacement;
  await expect.poll(async () => { replacement = await read(request); return replacement.response.status() === 200 && replacement.id !== seeded.id; }).toBe(true);
  expect(replacement.response.headers()['x-nextgo-prerender']).toBe('runtime');
  const response = await page.goto(route('alpha')); expect(response.status()).toBe(200);
  await expect(generation(page)).toHaveText(replacement.id); await head(page, replacement.id); await ready(page);
});

test('zero-match invalidation between build and runtime revokes that artifact reservation', async ({ request }) => {
  await control(request, 'reset', { deferStartup: true });
  const invalidated = await expire(request, 'key', 'absent-before-runtime'); expect(invalidated.Count).toBe(0);
  await control(request, 'start-apps');
  expect((await read(request)).response.status()).toBe(500); expect((await read(request, 'beta')).response.status()).toBe(500);
  const runtime = await read(request, 'alpha', 'healthy'); expect(runtime.response.status()).toBe(200); expect(runtime.response.headers()['x-nextgo-prerender']).toBe('runtime');
});

test.describe('native seeded forms without JavaScript', () => {
  test.use({ javaScriptEnabled: false });
  test('path expiration returns fresh healthy HTML and preserves the other build path', async ({ page, request }) => {
    const beta = await read(request, 'beta');
    await page.goto(route('alpha')); const old = await generation(page).textContent(); await head(page, old);
    await expect(count(page, 'first')).toHaveText('3'); await expect(count(page, 'second')).toHaveText('10');
    await expect(region(page, 'first').getByRole('button', { name: 'Increase Prerender first counter', exact: true })).toBeDisabled();
    await page.setExtraHTTPHeaders(headers('healthy'));
    await page.getByRole('button', { name: 'Expire prerender path', exact: true }).click();
    await expect(generation(page)).not.toHaveText(old); const current = await generation(page).textContent(); await head(page, current);
    const shared = await read(request); expect(shared.id).toBe(current); expect(shared.response.headers()['x-nextgo-prerender']).toBe('runtime');
    const preserved = await read(request, 'beta'); expect(preserved.id).toBe(beta.id); expect(preserved.response.headers()['x-nextgo-prerender']).toBe('build');
  });
});
