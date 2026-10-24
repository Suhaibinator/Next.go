import { test, expect } from '@playwright/test';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');

test('generated independent starter serves SSR, dynamic URLs, API and custom 404 without JavaScript', async ({ browser, request }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto('http://127.0.0.1:4103/');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Hello from Go.');
  await expect(page.locator('[data-count]')).toHaveText('3');
  await expect(page.getByRole('button', { name: 'Increase counter', exact: true })).toBeDisabled();
  await page.goto('http://127.0.0.1:4103/hello/Ada%20Lovelace');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Hello, Ada Lovelace!');
  expect(await (await request.get('/api/health')).json()).toEqual({ ok: true });
  const missing = await request.get('/missing');
  expect(missing.status()).toBe(404); expect(await missing.text()).toContain('Page not found');
  await context.close();
});

test('generated starter preserves hydration DOM and navigates with Go without reload', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('/', { waitUntil: 'domcontentloaded' });
  await page.evaluate(() => { window.starterRoot = document.querySelector('[data-nextgo-island]'); window.starterText = document.querySelector('[data-count]').firstChild; window.starterDocument = true; });
  release(); await ready(page);
  expect(await page.evaluate(() => window.starterRoot === document.querySelector('[data-nextgo-island]') && window.starterText === document.querySelector('[data-count]').firstChild)).toBe(true);
  await page.getByRole('button', { name: 'Increase counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText('4');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'About', exact: true }).click();
  await expect(page).toHaveTitle('About my Go application');
  expect(await page.evaluate(() => window.starterDocument)).toBe(true);
  await page.goBack(); await expect(page).toHaveTitle('My Go application');
  await expect(page.locator('[data-count]')).toHaveText('3');
  await page.getByRole('link', { name: 'Try a dynamic route', exact: true }).click();
  await expect(page).toHaveTitle('Hello developer');
  expect(await page.evaluate(() => window.starterDocument)).toBe(true);
});
