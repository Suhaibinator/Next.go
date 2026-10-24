import { test, expect } from '@playwright/test';
import { adminToken, readerToken } from './access-fixture.mjs';

const origin = 'http://127.0.0.1:4134';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const article = page => page.locator('[data-preview-generation]');
async function enter(request, host = origin, prefix = '') {
  const response = await request.post(host + prefix + '/api/preview/enable', {
    headers: { Origin: host, Authorization: 'Bearer ' + adminToken }, maxRedirects: 0,
  });
  expect(response.status()).toBe(303);
  expect(response.headers().location).toBe(prefix + '/preview');
}
async function leave(request) {
  const response = await request.post(origin + '/api/preview/disable', { headers: { Origin: origin }, maxRedirects: 0 });
  expect(response.status()).toBe(303);
}

test('authorized previews arrive as private meaningful HTML without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  try {
    await enter(context.request);
    const page = await context.newPage(); const response = await page.goto(origin + '/preview');
    await expect(page.getByRole('heading', { name: 'Unpublished field note' })).toBeVisible();
    await expect(page.locator('[data-preview-banner]')).toBeVisible();
    expect(response.headers()['cache-control']).toBe('private, no-store');
    expect(response.headers()['x-robots-tag']).toBe('noindex');
    expect(await page.content()).not.toContain(adminToken);
    await page.getByRole('button', { name: 'Exit preview' }).click();
    await expect(page.getByRole('heading', { name: 'Published field note' })).toBeVisible();
  } finally { await context.close(); }
});

test('delayed WASM hydrates the original preview DOM and retains Go interaction', async ({ page, request }) => {
  await enter(page.request);
  let release; const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/*.wasm', async route => { await gate; await route.continue(); });
  try {
    await page.goto(origin + '/preview', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { name: 'Unpublished field note' })).toBeVisible();
    await page.evaluate(() => { window.previewNode = document.querySelector('[data-count]'); });
  } finally { release(); }
  await ready(page);
  expect(await page.evaluate(() => window.previewNode === document.querySelector('[data-count]'))).toBe(true);
  await page.getByRole('button', { name: 'Increase Preview votes' }).click();
  await expect(page.locator('[data-count]')).toHaveText('1');
});

test('progressive editor form enables and exits preview without exposing credentials in URLs', async ({ page }) => {
  await page.goto(origin + '/preview'); await ready(page);
  await page.getByLabel('Editor token').fill(adminToken);
  await page.getByRole('button', { name: 'Enable preview' }).click(); await ready(page);
  await expect(article(page)).toHaveAttribute('data-preview-mode', 'true');
  expect(page.url()).toBe(origin + '/preview');
  const cookie = (await page.context().cookies()).find(c => c.name === 'nextgo_draft');
  expect(cookie.httpOnly).toBe(true); expect(cookie.sameSite).toBe('Lax'); expect(cookie.path).toBe('/');
  await page.getByRole('button', { name: 'Exit preview' }).click(); await ready(page);
  await expect(article(page)).toHaveAttribute('data-preview-mode', 'false');
  expect((await page.context().cookies()).find(c => c.name === 'nextgo_draft')).toBeUndefined();
});

test('preview reloads fresh content without replacing the anonymous published cache', async ({ page, request, browser }) => {
  const anonymous = await browser.newContext();
  try {
    const published = await anonymous.request.get(origin + '/preview'); const html = await published.text();
    const generation = html.match(/data-preview-generation="(\d+)"/)[1];
    expect(published.headers()['x-nextgo-prefetch']).toBeUndefined();
    await enter(page.request); await page.goto(origin + '/preview'); await ready(page);
    const first = Number(await article(page).getAttribute('data-preview-generation'));
    await page.reload(); await ready(page);
    expect(Number(await article(page).getAttribute('data-preview-generation'))).toBeGreaterThan(first);
    const later = await (await anonymous.request.get(origin + '/preview')).text();
    expect(later.match(/data-preview-generation="(\d+)"/)[1]).toBe(generation);
    expect(later).not.toContain('Unpublished field note');
  } finally { await anonymous.close(); }
});

test('enabling preview in another tab forces a document transition before retaining layout state', async ({ page, request }) => {
  await page.goto(origin + '/preview'); await ready(page);
  await page.evaluate(() => { window.previewDocument = true; });
  await enter(page.request);
  await page.getByRole('link', { name: 'Read the architecture' }).click(); await expect(page).toHaveURL(origin + '/about'); await ready(page);
  expect(await page.evaluate(() => window.previewDocument)).toBeUndefined();
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-draft', '1');
  await expect(page.locator('[data-preview-banner]')).toBeVisible();
});

test('ending preview clears retained private layout state through a full document', async ({ page, request }) => {
  await enter(page.request); await page.goto(origin + '/preview'); await ready(page);
  await page.evaluate(() => { window.previewDocument = true; });
  await leave(page.request);
  await page.getByRole('link', { name: 'Read the architecture' }).click(); await expect(page).toHaveURL(origin + '/about'); await ready(page);
  expect(await page.evaluate(() => window.previewDocument)).toBeUndefined();
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-draft', '0');
  await expect(page.locator('[data-preview-banner]')).toHaveCount(0);
});

test('ordinary preview navigation stays in Go and never reuses public prefetch responses', async ({ page, request }) => {
  await enter(page.request); await page.goto(origin + '/preview'); await ready(page);
  await page.evaluate(() => { window.previewDocument = true; });
  await page.getByRole('link', { name: 'Read the architecture' }).click(); await expect(page).toHaveURL(origin + '/about'); await ready(page);
  expect(await page.evaluate(() => window.previewDocument)).toBe(true);
  await expect(page.locator('[data-preview-banner]')).toBeVisible();
  await page.goBack(); await ready(page);
  await expect(page.getByRole('heading', { name: 'Unpublished field note' })).toBeVisible();
  expect(await page.evaluate(() => window.previewDocument)).toBe(true);
});

test('invalid credentials, foreign origins, prefetch GETs and tampered cookies cannot enable preview', async ({ page, request }) => {
  for (const [headers, status] of [
    [{ Origin: origin, Authorization: 'Bearer wrong' }, 401],
    [{ Origin: origin, Authorization: 'Bearer ' + readerToken }, 403],
    [{ Origin: 'https://foreign.test', Authorization: 'Bearer ' + adminToken }, 403],
  ]) {
    const response = await request.post(origin + '/api/preview/enable', { headers, maxRedirects: 0 });
    expect(response.status()).toBe(status); expect(response.headers()['set-cookie']).toBeUndefined();
  }
  expect((await request.get(origin + '/api/preview/enable')).status()).toBe(405);
  expect((await request.post(origin + '/api/preview/enable', { headers: { Origin: origin, Authorization: 'Bearer ' + adminToken }, data: 'x'.repeat(5000) })).status()).toBe(413);
  await enter(page.request);
  const cookie = (await page.context().cookies()).find(c => c.name === 'nextgo_draft');
  await page.context().addCookies([{ ...cookie, value: cookie.value.slice(0, -8) + 'AAAAAAAA' }]);
  await page.goto(origin + '/preview'); await ready(page);
  await expect(article(page)).toHaveAttribute('data-preview-mode', 'false');
});

test('one host-only app cookie spans mounted locale scopes and exits to the local preview page', async ({ page, request }) => {
  const host = 'http://127.0.0.1:4135'; const prefix = '/shop/fr';
  await enter(page.request, host, prefix); await page.goto(host + prefix + '/preview'); await ready(page);
  await expect(article(page)).toHaveAttribute('data-preview-mode', 'true');
  expect((await page.context().cookies()).find(c => c.name === 'nextgo_draft').path).toBe('/shop');
  await page.getByRole('link', { name: 'Read the architecture' }).click(); await expect(page).toHaveURL(host + prefix + '/about'); await ready(page);
  await expect(page.locator('[data-preview-banner]')).toBeVisible();
  await page.getByRole('button', { name: 'Exit preview' }).click(); await ready(page);
  expect(page.url()).toBe(host + prefix + '/preview');
  await expect(article(page)).toHaveAttribute('data-preview-mode', 'false');
});
