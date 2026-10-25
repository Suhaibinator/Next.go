import {test, expect} from '@playwright/test';
import {base, note} from './refresh-production-fixture.mjs';

const mode = process.env.NEXTGO_REFRESH_TRANSPORT || 'http', isStatic = mode === 'static';
const section = (page, id = 'primary') => page.locator(`[data-refresh-instance="${id}"]`);
const count = (page, id) => section(page, id).locator('[data-refresh-count]');
async function ready(page) {
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  await expect(page.locator('[data-refresh-eager]')).toHaveText('eager-v1');
}
async function ordinary(page) {
  expect(await page.evaluate(() => ({bridge: typeof globalThis.__nextgoDevRuntime, development: !!JSON.parse(document.body.dataset.nextgoNavigation).development, devBody: document.body.hasAttribute('data-nextgo-dev')}))).toEqual({bridge: 'undefined', development: false, devBody: false});
  const status = page.locator('#nextgo-status');
  await expect(status).toHaveCount(1); await expect(status).toHaveAttribute('role', 'status'); await expect(status).toHaveAttribute('aria-live', 'polite'); await expect(status).toBeHidden();
}

test('useful native HTML becomes ordinary Go islands on the exact original nodes', async ({browser, page, baseURL}) => {
  const native = await browser.newContext({javaScriptEnabled: false, baseURL});
  try {
    const initial = await native.newPage(); expect((await initial.goto(base)).status()).toBe(200);
    await expect(count(initial, 'primary')).toHaveText('3'); await expect(count(initial, 'secondary')).toHaveText('10');
    await expect(section(initial).locator('[data-refresh-increment]')).toBeDisabled();
    await expect(initial.locator('[data-refresh-eager]')).toHaveText('Shared page chrome from the server');
  } finally {await native.close();}
  let release; const gate = new Promise(done => {release = done;});
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  await page.route(/\.wasm(?:\?|$)/, async route => {await gate; await route.continue();});
  try {
    expect((await page.goto(base, {waitUntil: 'commit'})).status()).toBe(200);
    await expect(count(page, 'primary')).toHaveText('3'); await expect(section(page).locator('[data-refresh-proof]')).toHaveText('Waiting for lazy Go');
    await page.evaluate(() => {
      const root = document.querySelector('[data-refresh-instance="primary"]');
      window.refreshBefore = {root, count: root.querySelector('[data-refresh-count]'), label: root.querySelector('[data-refresh-label]')};
      window.refreshBefore.label.value = 'Typed before WASM';
    });
  } finally {release();}
  await ready(page); await expect(section(page).locator('[data-refresh-proof]')).toHaveText('v1'); await expect(section(page, 'secondary').locator('[data-refresh-proof]')).toHaveText('v1');
  expect(await page.evaluate(() => {const before = window.refreshBefore, root = document.querySelector('[data-refresh-instance="primary"]'); return root === before.root && root.querySelector('[data-refresh-count]') === before.count && root.querySelector('[data-refresh-label]') === before.label;})).toBe(true);
  await expect(section(page).locator('[data-refresh-title]')).toHaveText('Typed before WASM');
  await section(page).locator('[data-refresh-increment]').click(); await expect(count(page, 'primary')).toHaveText('4'); await expect(count(page, 'secondary')).toHaveText('10');
  await ordinary(page); expect(errors).toEqual([]);
});

test('ordinary state events and SPA navigation retain the layout and reset page islands', async ({page}) => {
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  await page.goto(base); await ready(page); await expect(section(page).locator('[data-refresh-proof]')).toHaveText('v1');
  await section(page).locator('[data-refresh-label]').fill('Public production draft'); await expect(section(page).locator('[data-refresh-title]')).toHaveText('Public production draft');
  await section(page).locator('[data-refresh-enabled]').uncheck(); await expect(section(page).locator('[data-refresh-increment]')).toBeDisabled();
  await section(page).locator('[data-refresh-enabled]').check(); await section(page).locator('[data-refresh-increment]').click(); await expect(count(page, 'primary')).toHaveText('4');
  await section(page, 'secondary').locator('[data-refresh-increment]').click(); await expect(count(page, 'secondary')).toHaveText('11');
  await page.evaluate(() => {window.refreshDocumentToken = 'ordinary-production-document'; window.refreshLayout = document.querySelector('[data-refresh-eager]');});
  let reloads = 0; page.on('request', request => {if (request.isNavigationRequest() && request.frame() === page.mainFrame()) reloads++;});
  await page.getByRole('link', {name: 'Refresh boundaries', exact: true}).click();
  await expect(page.locator('[data-refresh-details]')).toBeVisible(); await expect(page).toHaveTitle('Refresh boundaries');
  expect(await page.evaluate(() => window.refreshDocumentToken)).toBe('ordinary-production-document'); expect(await page.evaluate(() => window.refreshLayout === document.querySelector('[data-refresh-eager]'))).toBe(true);
  await page.goBack(); await expect(section(page).locator('[data-refresh-proof]')).toHaveText('v1');
  await expect(count(page, 'primary')).toHaveText('3'); await expect(count(page, 'secondary')).toHaveText('10');
  expect(reloads).toBe(0); await ordinary(page); expect(errors).toEqual([]);
});

test('real assets and server or static capabilities work with development refresh inactive', async ({page, request}) => {
  const errors = [], failedAssets = [], refreshRequests = [], wasm = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('request', request => {if (/\/_nextgo\/(?:refresh|events)(?:\?|$)/.test(new URL(request.url()).pathname)) refreshRequests.push(request.url());});
  page.on('response', response => {const path = new URL(response.url()).pathname; if (/\.(?:wasm|css|js)$/.test(path)) {if (response.status() >= 400) failedAssets.push([path, response.status()]); if (path.endsWith('.wasm')) wasm.push(path);}});
  await page.goto(base); await ready(page); await expect(section(page).locator('[data-refresh-proof]')).toHaveText('v1'); await ordinary(page);
  await expect(page.locator('[data-refresh-public-note]')).toHaveText(note);
  expect(wasm.some(path => path.endsWith('/app.wasm'))).toBe(true); expect(wasm.some(path => /\/module-[a-f0-9]{64}\.wasm$/.test(path))).toBe(true);
  for (const path of ['/_nextgo/refresh', '/_nextgo/events', `${base}/_nextgo/refresh`, `${base}/_nextgo/events`]) {
    const denied = await request.get(path); expect(denied.status()).toBe(404); expect(denied.headers()['content-type'] || '').not.toContain('text/event-stream');
  }
  if (isStatic) {
    await expect(page.locator('[data-refresh-work]')).toHaveCount(0); await expect(page.locator('[data-refresh-server-instance]')).toHaveCount(0);
    expect((await request.get(`${base}/api/status`)).status()).toBe(404); expect((await request.get(`${base}/api/work?delay=0`)).status()).toBe(404);
    expect(await page.locator('a').evaluateAll(nodes => nodes.some(node => /\/api\/(status|work)/.test(node.getAttribute('href') || '')))).toBe(false);
  } else {
    const first = await (await request.get(`${base}/api/status`)).json(); expect(first.instance).toMatch(/^[a-f0-9]{32}$/); expect(first.source).toBe('native-v1');
    await expect(page.locator('[data-refresh-server-instance]')).toHaveText(first.instance); await expect(page.locator('[data-refresh-work]')).toHaveCount(2);
    const second = await (await request.get(`${base}/api/status`)).json(); expect(second.instance).toBe(first.instance); expect(second.requests).toBe(first.requests + 1);
    expect(await (await request.get(`${base}/api/work?delay=0`)).json()).toEqual({complete: true});
    // Exercise the actual lazy Go worker and managed Fetch, not just its API.
    const work = section(page).locator('[data-refresh-work]'), status = section(page).locator('[data-refresh-work-status]');
    await work.click(); await expect(status).toHaveAttribute('data-state', 'loading'); await expect(work).toBeDisabled();
    await section(page).locator('[data-refresh-increment]').click(); await expect(count(page, 'primary')).toHaveText('4');
    await expect(status).toHaveAttribute('data-state', 'ready'); await expect(status).toHaveText('Public work complete.'); await expect(work).toBeEnabled();
  }
  expect(refreshRequests).toEqual([]); expect(failedAssets).toEqual([]); expect(errors).toEqual([]);
});
