import { test, expect } from '@playwright/test';
import { adminToken } from './access-fixture.mjs';

const origin = 'http://127.0.0.1:4134';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const thawed = page => expect(page.locator('body')).not.toHaveAttribute('data-nextgo-frozen', '');
async function lifecycle(page, name, navigating = false) {
  try { await page.evaluate(name => dispatchEvent(new PageTransitionEvent(name, { persisted: true })), name); }
  catch (error) { if (!navigating || !String(error).includes('Execution context was destroyed')) throw error; }
}
async function preview(page) {
  const response = await page.request.post(origin + '/api/preview/enable', { headers: { Origin: origin, Authorization: 'Bearer ' + adminToken }, maxRedirects: 0 });
  expect(response.status()).toBe(303);
  await page.goto(origin + '/preview'); await ready(page);
}

// These deterministic event-contract tests supplement the separate strict
// native BFCache suite. Dispatching an event does not simulate browser freezing.
test('restore keeps existing Go state hidden until the verified session probe completes', async ({ page }) => {
  await preview(page);
  await page.getByRole('button', { name: 'Increase Preview votes' }).click();
  await page.evaluate(() => { window.restoreNode = document.querySelector('[data-count]'); });
  let release; const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/_nextgo/draft', async route => { await gate; await route.continue(); });
  try {
    await lifecycle(page, 'pagehide'); await lifecycle(page, 'pageshow', true);
    await expect(page.locator('[data-count]')).toBeHidden();
    await expect(page.locator('#nextgo-status')).toHaveText('Checking preview access…');
  } finally { release(); }
  await thawed(page);
  expect(await page.evaluate(() => window.restoreNode === document.querySelector('[data-count]'))).toBe(true);
  await expect(page.locator('[data-count]')).toHaveText('1');
  await page.getByRole('button', { name: 'Increase Preview votes' }).click();
  await expect(page.locator('[data-count]')).toHaveText('2');
});

test('a restored expired or cleared preview reloads before revealing the private tree', async ({ page }) => {
  await preview(page); await page.evaluate(() => { window.privateRestore = true; });
  await page.context().clearCookies();
  await lifecycle(page, 'pagehide'); await lifecycle(page, 'pageshow');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-draft', '0'); await ready(page);
  expect(await page.evaluate(() => window.privateRestore)).toBeUndefined();
  await expect(page.getByRole('heading', { name: 'Published field note' })).toBeVisible();
});

for (const field of ['x-nextgo-protocol', 'x-nextgo-build', 'x-nextgo-configuration', 'x-nextgo-draft']) {
  test(`restore rejects an incompatible ${field} before adopting retained content`, async ({ page }) => {
    await preview(page); await page.evaluate(() => { window.privateRestore = true; });
    await page.route('**/_nextgo/draft', async route => {
      const response = await route.fetch(); const headers = { ...response.headers() }; delete headers[field];
      await route.fulfill({ response, headers });
    });
    const reloaded = page.waitForNavigation({ waitUntil: 'load' });
    await lifecycle(page, 'pagehide'); await lifecycle(page, 'pageshow', true);
    await reloaded; await thawed(page); await ready(page);
    expect(await page.evaluate(() => window.privateRestore)).toBeUndefined();
    await expect(page.getByRole('heading', { name: 'Unpublished field note' })).toBeVisible();
  });
}

test('failed restore verification stays private and retries without duplicating island listeners', async ({ page }) => {
  await preview(page);
  await page.getByRole('button', { name: 'Increase Preview votes' }).click();
  let attempts = 0;
  await page.route('**/_nextgo/draft', route => ++attempts <= 2 ? route.abort() : route.continue());
  await lifecycle(page, 'pagehide'); await lifecycle(page, 'pageshow');
  for (let i = 0; i < 2; i++) {
    await expect(page.locator('[data-count]')).toBeHidden();
    await page.getByRole('button', { name: 'Retry verification' }).click();
  }
  await thawed(page); expect(attempts).toBe(3);
  await page.getByRole('button', { name: 'Increase Preview votes' }).click();
  await expect(page.locator('[data-count]')).toHaveText('2');
  await expect(page.getByRole('button', { name: 'Retry verification' })).toHaveCount(0);
});

test('a superseded restore cannot reveal a tree before the current check finishes', async ({ page }) => {
  await preview(page);
  let attempts = 0, releaseFirst, releaseSecond, firstDone;
  const first = new Promise(resolve => { releaseFirst = resolve; });
  const second = new Promise(resolve => { releaseSecond = resolve; });
  const settled = new Promise(resolve => { firstDone = resolve; });
  await page.route('**/_nextgo/draft', async route => {
    const index = ++attempts;
    try {
      const response = await route.fetch(); await (index === 1 ? first : second);
      await route.fulfill({ response });
    } catch { /* superseded fetch was aborted */ }
    finally { if (index === 1) firstDone(); }
  });
  await lifecycle(page, 'pageshow'); await expect.poll(() => attempts).toBe(1);
  await lifecycle(page, 'pagehide'); await lifecycle(page, 'pageshow');
  try {
    await expect.poll(() => attempts).toBe(2);
    releaseFirst(); await settled;
    await expect(page.locator('[data-count]')).toBeHidden();
  } finally { releaseFirst(); releaseSecond(); }
  await thawed(page);
});

test('a restore before Go startup uses the bootstrap full-document fallback', async ({ page }) => {
  let requests = 0;
  expect((await page.request.post(origin + '/api/preview/enable', { headers: { Origin: origin, Authorization: 'Bearer ' + adminToken }, maxRedirects: 0 })).status()).toBe(303);
  await page.route('**/app.wasm', route => ++requests === 1 ? route.abort() : route.continue());
  await page.goto(origin + '/preview');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'error');
  await page.evaluate(() => {
    window.beforeGoRestore = true;
    const input = document.createElement('input'); input.id = '__nextgoFreeze'; document.querySelector('#nextgo-app').append(input);
  });
  await lifecycle(page, 'pagehide'); await expect(page.locator('body')).toHaveAttribute('data-nextgo-frozen', '');
  await lifecycle(page, 'pageshow', true);
  await ready(page);
  expect(await page.evaluate(() => window.beforeGoRestore)).toBeUndefined();
  await expect(page.getByRole('heading', { name: 'Unpublished field note' })).toBeVisible();
});

test('mounted locale restore probes use the current application scope', async ({ page }) => {
  await page.goto('http://127.0.0.1:4135/shop/fr/preview'); await ready(page);
  const probe = page.waitForRequest('http://127.0.0.1:4135/shop/fr/_nextgo/draft');
  await lifecycle(page, 'pagehide'); await lifecycle(page, 'pageshow');
  expect((await probe).method()).toBe('GET'); await thawed(page);
  await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
});

test('restore failure inside an open modal offers a keyboard-accessible retry and retains modal state', async ({ page }) => {
  await page.goto(origin + '/gallery'); await ready(page);
  await page.getByRole('link', { name: 'Open coastal photo', exact: true }).click();
  const modal = page.locator('dialog'); await expect(modal).toBeVisible();
  await page.getByRole('button', { name: 'Increase Modal photo counter', exact: true }).click();
  await page.evaluate(() => { window.restoreModal = document.querySelector('dialog'); });
  let attempts = 0;
  await page.route('**/_nextgo/draft', route => ++attempts === 1 ? route.abort() : route.continue());
  await lifecycle(page, 'pagehide'); await lifecycle(page, 'pageshow');
  const retry = page.getByRole('button', { name: 'Retry verification' });
  await expect(retry).toBeVisible(); await retry.focus(); await expect(retry).toBeFocused({ timeout: 3000 });
  await page.keyboard.press('Enter'); await thawed(page); await expect(modal).toBeVisible();
  expect(await page.evaluate(() => window.restoreModal === document.querySelector('dialog'))).toBe(true);
  await expect(modal.locator('[data-count]')).toHaveText('21');
  expect(await page.evaluate(() => document.getElementById('nextgo-status').parentElement === document.body)).toBe(true);
  await page.getByRole('button', { name: 'Close photo', exact: true }).click(); await expect(modal).toHaveCount(0);
});
