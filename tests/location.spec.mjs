import { test, expect } from '@playwright/test';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const location = page => page.locator('[data-route-location]');
const nav = (page, name) => page.getByRole('navigation', { name: 'Main', exact: true }).getByRole('link', { name, exact: true });
const watch = page => page.evaluate(() => {
  window.locationDocument = true;
  window.locationOutput = document.querySelector('[data-route-location]');
  window.locationChanges = [];
  new MutationObserver(() => window.locationChanges.push(window.locationOutput.textContent))
    .observe(window.locationOutput, { childList: true, characterData: true, subtree: true });
});

test('the current route renders before WASM and hydration preserves its text node', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('/posts?tag=one&tag=two', { waitUntil: 'domcontentloaded' });
  await expect(location(page)).toHaveText('/posts?tag=one&tag=two');
  await page.evaluate(() => { window.locationText = document.querySelector('[data-route-location]').firstChild; });
  release(); await ready(page);
  expect(await page.evaluate(() => window.locationText === document.querySelector('[data-route-location]').firstChild)).toBe(true);
});

test('a retained Go subscription follows paths, queries, dynamic routes and history', async ({ page }) => {
  await page.goto('/'); await ready(page); await watch(page);
  await page.locator('[data-layout-increment]').click();
  await nav(page, 'Field notes').evaluate(link => { link.setAttribute('href', '/posts?tag=one&tag=two&q=Go%20WASM'); link.setAttribute('data-nextgo-prefetch', 'false'); });
  await nav(page, 'Field notes').click(); await expect(location(page)).toHaveText('/posts?tag=one&tag=two&q=Go%20WASM');
  await page.getByRole('link', { name: /Start with the HTML/ }).click(); await expect(location(page)).toHaveText('/posts/html-first');
  await page.goBack(); await expect(location(page)).toHaveText('/posts?tag=one&tag=two&q=Go%20WASM');
  await page.goForward(); await expect(location(page)).toHaveText('/posts/html-first');
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  expect(await page.evaluate(() => [window.locationDocument, window.locationOutput === document.querySelector('[data-route-location]')])).toEqual([true, true]);
});

test('native fragment changes, replacement and traversal publish without fetching pages', async ({ page }) => {
  await page.goto('/gallery'); await ready(page);
  // Keep this fragment/history test independent of speculative photo-link intent.
  // Every X-Nextgo-Navigate request is still counted and must remain zero.
  await page.getByRole('navigation', { name: 'Gallery photos', exact: true })
    .getByRole('link').evaluateAll(links => {
      links.forEach(link => link.setAttribute('data-nextgo-prefetch', 'false'));
    });
  await watch(page);
  let requests = 0;
  page.on('request', request => { if (request.headers()['x-nextgo-navigate'] === '1') requests++; });
  const link = page.getByRole('link', { name: 'Open coastal photo', exact: true });
  await link.evaluate(node => node.setAttribute('href', '#gallery-photos'));
  await link.click(); await expect(location(page)).toHaveText('/gallery#gallery-photos');
  const replace = page.getByRole('link', { name: 'Replace with coastal photo', exact: true });
  const length = await page.evaluate(() => history.length);
  await replace.evaluate(node => node.setAttribute('href', '#main'));
  await replace.click(); await expect(location(page)).toHaveText('/gallery#main');
  expect(await page.evaluate(() => history.length)).toBe(length);
  await page.goBack(); await expect(location(page)).toHaveText('/gallery');
  await page.goForward(); await expect(location(page)).toHaveText('/gallery#main');
  await page.evaluate(() => { window.location.hash = 'gallery-photos'; });
  await expect(location(page)).toHaveText('/gallery#gallery-photos');
  expect(requests).toBe(0);
  expect(await page.evaluate(() => window.locationChanges)).toEqual(['/gallery#gallery-photos', '/gallery#main', '/gallery', '/gallery#main', '/gallery#gallery-photos']);
});

test('loading and failed navigation do not publish a speculative route', async ({ page }) => {
  await page.goto('/'); await ready(page); await watch(page);
  let release, started;
  const gate = new Promise(resolve => { release = resolve; });
  const fetching = new Promise(resolve => { started = resolve; });
  await page.route('**/posts?pending=1', async route => { started(); await gate; await route.abort('failed'); });
  await nav(page, 'Field notes').evaluate(link => { link.setAttribute('href', '/posts?pending=1'); link.setAttribute('data-nextgo-prefetch', 'false'); });
  await nav(page, 'Field notes').click(); await fetching;
  await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
  await expect(location(page)).toHaveText('/'); expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  release(); await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(location(page)).toHaveText('/'); expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  await page.unroute('**/posts?pending=1'); await nav(page, 'Field notes').click();
  await expect(location(page)).toHaveText('/posts?pending=1');
});

test('a superseded response cannot notify retained route observers', async ({ page }) => {
  await page.goto('/'); await ready(page); await watch(page);
  let release, started, finished;
  const gate = new Promise(resolve => { release = resolve; });
  const fetching = new Promise(resolve => { started = resolve; });
  const settled = new Promise(resolve => { finished = resolve; });
  await page.route('**/posts?race=1', async route => {
    const response = await route.fetch(); started(); await gate;
    try { await route.fulfill({ response }); } finally { finished(); }
  });
  await nav(page, 'Field notes').evaluate(link => { link.setAttribute('href', '/posts?race=1'); link.setAttribute('data-nextgo-prefetch', 'false'); });
  await nav(page, 'Field notes').click(); await fetching;
  await nav(page, 'Architecture').click(); await expect(location(page)).toHaveText('/about');
  release(); await settled;
  await expect(page).toHaveURL(/\/about$/); expect(await page.evaluate(() => window.locationChanges)).toEqual(['/about']);
});

test('refresh leaves the location subscription unchanged and replacement publishes the committed URL', async ({ page }) => {
  await page.goto('/features'); await ready(page); await watch(page);
  const response = page.waitForResponse(r => r.url().endsWith('/features') && r.request().headers()['x-nextgo-navigate'] === '1');
  await page.getByRole('button', { name: 'Refresh server data', exact: true }).click();
  await (await response).finished(); await expect(page.locator('#nextgo-status')).toBeHidden();
  expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  const length = await page.evaluate(() => history.length);
  await page.getByRole('button', { name: 'Replace with first field note', exact: true }).click();
  await expect(location(page)).toHaveText('/posts/html-first');
  expect(await page.evaluate(() => history.length)).toBe(length);
});

test('denial disposes the previous layout subscription and returning mounts a fresh one', async ({ page }) => {
  await page.goto('/gallery'); await ready(page); await watch(page);
  await page.setExtraHTTPHeaders({ 'X-Demo-Photo-Deny': 'yes' });
  await page.getByRole('link', { name: 'Open coastal photo', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Access denied');
  await expect(location(page)).toHaveCount(0);
  expect(await page.evaluate(() => [window.locationOutput.isConnected, window.locationOutput.textContent, window.locationChanges])).toEqual([false, '/gallery', []]);
  await page.setExtraHTTPHeaders({}); await page.goBack(); await expect(location(page)).toHaveText('/gallery');
  await nav(page, 'Architecture').click(); await expect(location(page)).toHaveText('/about');
  expect(await page.evaluate(() => window.locationOutput.textContent)).toBe('/gallery');
});

test('custom 404 content still publishes the committed missing URL', async ({ page }) => {
  await page.goto('/'); await ready(page); await watch(page);
  await nav(page, 'Architecture').evaluate(link => { link.setAttribute('href', '/missing-location?reason=absent'); link.setAttribute('data-nextgo-prefetch', 'false'); });
  await nav(page, 'Architecture').click(); await expect(page.getByRole('heading', { level: 1 })).toHaveText('Nothing here. Yet.');
  await expect(location(page)).toHaveText('/missing-location?reason=absent');
  expect(await page.evaluate(() => window.locationDocument)).toBe(true);
});

test('intercepted modal addresses update the retained layout and dismissal restores its source', async ({ page }) => {
  await page.goto('/gallery?q=location#gallery-photos'); await ready(page); await watch(page);
  await page.getByRole('link', { name: 'Open coastal photo', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible(); await expect(location(page)).toHaveText('/photos/one');
  expect(await page.evaluate(() => window.locationOutput === document.querySelector('[data-route-location]'))).toBe(true);
  await page.getByRole('button', { name: 'Close photo', exact: true }).click();
  await expect(location(page)).toHaveText('/gallery?q=location#gallery-photos');
  expect(await page.evaluate(() => window.locationChanges)).toEqual(['/photos/one', '/gallery?q=location#gallery-photos']);
});

test('pending and failed history traversal keep the previously committed snapshot', async ({ page }) => {
  await page.goto('/'); await ready(page); await nav(page, 'Field notes').click();
  await expect(location(page)).toHaveText('/posts'); await watch(page);
  let release, started;
  const gate = new Promise(resolve => { release = resolve; });
  const fetching = new Promise(resolve => { started = resolve; });
  await page.route('http://127.0.0.1:4100/', async route => {
    if (route.request().headers()['x-nextgo-navigate'] !== '1') return route.continue();
    started(); await gate; await route.abort('failed');
  });
  await page.goBack(); await fetching;
  await expect(page).toHaveURL('http://127.0.0.1:4100/'); await expect(location(page)).toHaveText('/posts');
  expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  release(); await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(page).toHaveURL(/\/posts$/); await expect(location(page)).toHaveText('/posts');
  expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  await page.unroute('http://127.0.0.1:4100/'); await nav(page, 'Overview').click(); await expect(location(page)).toHaveText('/');
});
