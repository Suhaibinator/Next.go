import { test, expect } from '@playwright/test';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const notes = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Field notes', exact: true });
const features = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Features', exact: true });

test('mounted SSR, dynamic URLs, APIs and asset-only origin work without JavaScript', async ({ browser, request }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto('http://127.0.0.1:4104/demo');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
  await expect(notes(page)).toHaveAttribute('href', '/demo/posts');
  await expect(page.locator('script[src$="/wasm_exec.js"]')).toHaveAttribute('src', /4105\/cdn\/_nextgo\/.+\/wasm_exec.js$/);
  await notes(page).click();
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveURL(/\/demo\/posts\/html-first$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Start with the HTML');
  await context.close();
  expect((await request.get('/api/health')).status()).toBe(404);
  expect((await request.get('/demo/api/health')).status()).toBe(200);
  const sitemap = await request.get('/demo/sitemap.xml');
  expect(await sitemap.text()).toContain('https://example.com/demo/posts');
  const missing = await request.get('/demo/missing');
  expect(missing.status()).toBe(404);
  expect(await missing.text()).toContain('Nothing here. Yet.');
  expect((await request.get('http://127.0.0.1:4105/cdn/api/health')).status()).toBe(404);
});

test('CDN WASM preserves the SSR DOM and mounted Go navigation updates styles and images', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('/demo', { waitUntil: 'domcontentloaded' });
  await page.evaluate(() => { window.mountedButton = document.querySelector('[data-increment]'); window.mountedDocument = true; });
  await expect(page.locator('[data-count]')).toHaveText(['3', '10']);
  release(); await ready(page);
  expect(await page.evaluate(() => window.mountedButton === document.querySelector('[data-increment]'))).toBe(true);
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  await notes(page).click();
  await expect(page).toHaveURL(/\/demo\/posts$/);
  await expect(page.locator('[data-route-location]')).toHaveText('/demo/posts');
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveURL(/\/demo\/posts\/html-first$/);
  await expect(page.locator('[data-route-location]')).toHaveText('/demo/posts/html-first');
  await page.goBack(); await expect(page).toHaveTitle('Field notes — Next.go');
  await features(page).click();
  await page.getByRole('link', { name: 'Image optimization and SEO', exact: true }).click();
  await expect(page).toHaveURL(/\/demo\/media$/);
  await expect(page.locator('link[rel="stylesheet"][data-nextgo-head]')).toHaveAttribute('href', 'http://127.0.0.1:4105/cdn/assets/media.css');
  await expect(page.locator('[data-resource-example]')).toHaveCSS('color', 'rgb(18, 109, 100)');
  await expect(page.locator('img.responsive-media')).toHaveAttribute('src', /^\/demo\/_nextgo\/image\?/);
  await expect.poll(() => page.locator('img.responsive-media').evaluate(image => image.naturalWidth)).toBeGreaterThan(0);
  expect(await page.evaluate(() => window.mountedDocument)).toBe(true);
});

test('mounted Go actions, refresh, prefetch and replacement retain their deployment URLs', async ({ page }) => {
  await page.goto('/demo/features'); await ready(page);
  await expect(page.locator('form')).toHaveAttribute('action', '/demo/_nextgo/actions/increment');
  const count = Number(await page.locator('[data-server-count]').textContent());
  await page.getByRole('button', { name: 'Increment on server', exact: true }).click();
  await expect(page.locator('[data-server-count]')).toHaveText(String(count + 1));
  await expect(page).toHaveURL(/\/demo\/features$/);
  await page.getByRole('button', { name: 'Prefetch field notes', exact: true }).click();
  await page.getByRole('button', { name: 'Replace with first field note', exact: true }).click();
  await expect(page).toHaveURL(/\/demo\/posts\/html-first$/);
  await expect(page).toHaveTitle('Start with the HTML — Next.go');
  await features(page).click(); await expect(page).toHaveURL(/\/demo\/features$/);
  await page.getByRole('button', { name: 'Open typed guide', exact: true }).click();
  await expect(page).toHaveURL(/\/demo\/docs\/guide\/getting-started$/);
  await expect(page.locator('[data-catchall-path]')).toHaveText('guide/getting-started');
});

test('mounted loading and errors preserve the view and reject a foreign asset subtree', async ({ page }) => {
  await page.goto('/demo'); await ready(page);
  await notes(page).evaluate(link => { link.href = '/demo/posts?delay=750ms'; link.click(); });
  await expect(page.locator('[data-posts-loading]')).toBeVisible();
  await expect(page).toHaveTitle('Field notes — Next.go');
  await features(page).click();
  await expect(page).toHaveTitle('Framework features — Next.go');
  await page.route('**/demo/media', async route => {
    if (route.request().headers()['x-nextgo-navigate'] !== '1') return route.continue();
    const response = await route.fetch();
    const data = await response.json();
    data.head.find(element => element.attrs?.rel === 'stylesheet').attrs.href = 'http://127.0.0.1:4105/other/media.css';
    await route.fulfill({ response, json: data });
  });
  await page.getByRole('link', { name: 'Image optimization and SEO', exact: true }).click();
  await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(page).toHaveURL(/\/demo\/features$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Framework features');
});
