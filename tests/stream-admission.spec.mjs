import { test, expect } from '@playwright/test';

const origin = 'http://127.0.0.1:4161';
const control = 'http://127.0.0.1:4162';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
async function operation(request, action) { const response = await request.post(`${control}/_fixture/${action}`); expect(response.status(), await response.text()).toBe(200); }
async function capacityAvailable(request) { await expect.poll(async () => (await request.head(`${origin}/stream`)).status()).toBe(200); }
function saturated(response) {
  expect(response.status()).toBe(503);
  expect(response.headers()['retry-after']).toBe('1');
  expect(response.headers()['cache-control']).toContain('private');
  expect(response.headers()['cache-control']).toContain('no-store');
}
test.afterEach(async ({ request }) => { await operation(request, 'release'); });

test('real initial stream saturation rejects every representation while prefetch bypasses and reload recovers', async ({ page, request }) => {
  await operation(request, 'hold');
  const overloaded = await page.goto(`${origin}/stream`);
  saturated(overloaded);
  await expect(page.locator('[data-stream-loaded]')).toHaveCount(0);
  for (const headers of [{ 'X-Nextgo-Navigate': '1' }, { 'X-Nextgo-Navigate': '1', 'X-Nextgo-Stream': '1' }]) saturated(await request.get(`${origin}/stream`, { headers }));
  saturated(await request.head(`${origin}/stream`));
  for (const headers of [{ 'X-Nextgo-Navigate': '1', 'X-Nextgo-Purpose': 'prefetch' }, { 'X-Nextgo-Navigate': '1', 'X-Nextgo-Stream': '1', 'X-Nextgo-Purpose': 'prefetch' }]) {
    const skipped = await request.get(`${origin}/stream`, { headers });
    expect(skipped.status()).toBe(204); expect((await skipped.body()).length).toBe(0);
  }
  await operation(request, 'release'); await capacityAvailable(request);
  const recovered = await page.reload(); expect(recovered.status()).toBe(200);
  await expect(page.locator('[data-stream-loaded]')).toBeVisible(); await ready(page);
  await page.getByRole('button', { name: 'Increase Streamed counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText('8');
});

for (const negotiated of [false, true]) {
  test(`a real ${negotiated ? 'negotiated' : 'initial'} holder makes Go streaming navigation recoverable without losing the current view`, async ({ page, request }) => {
    await page.goto(origin); await ready(page);
    await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
    await page.locator('[data-layout-increment]').click();
    await page.evaluate(() => {
      window.admissionApp = document.getElementById('nextgo-app'); window.admissionHeader = document.querySelector('header'); window.admissionCounter = document.querySelector('[data-nextgo-island="counter"]'); window.admissionHistory = history.length;
      const link = document.createElement('a'); link.id = 'admission-stream-link'; link.href = '/stream'; link.textContent = 'Open admitted stream'; link.setAttribute('data-nextgo-prefetch', 'false'); document.querySelector('main').prepend(link);
    });
    const title = await page.title();
    await operation(request, `hold?negotiated=${negotiated}`);
    const failed = page.waitForResponse(response => new URL(response.url()).pathname === '/stream' && response.request().headers()['x-nextgo-stream'] === '1');
    await page.locator('#admission-stream-link').click(); saturated(await failed);
    await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
    await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy');
    await expect(page).toHaveURL(`${origin}/`); await expect(page).toHaveTitle(title);
    await expect(page.locator('[data-count]')).toHaveText(['4', '10']); await expect(page.locator('[data-layout-count]')).toHaveText('1');
    expect(await page.evaluate(() => window.admissionApp === document.getElementById('nextgo-app') && window.admissionHeader === document.querySelector('header') && window.admissionCounter === document.querySelector('[data-nextgo-island="counter"]') && window.admissionHistory === history.length)).toBe(true);
    // The retained Go handlers remain usable while the other request owns capacity.
    await page.getByRole('button', { name: 'Increase First counter', exact: true }).click(); await expect(page.locator('[data-count]')).toHaveText(['5', '10']);
    await operation(request, 'release'); await capacityAvailable(request);
    const retry = page.waitForResponse(response => new URL(response.url()).pathname === '/stream' && response.request().headers()['x-nextgo-stream'] === '1');
    await page.locator('#admission-stream-link').click(); expect((await retry).status()).toBe(200);
    await expect(page.locator('[data-stream-loaded]')).toBeVisible(); await ready(page);
    expect(await page.evaluate(() => window.admissionApp === document.getElementById('nextgo-app') && window.admissionHeader === document.querySelector('header'))).toBe(true);
    await expect(page.locator('[data-layout-count]')).toHaveText('1');
    await page.getByRole('button', { name: 'Increase Streamed counter', exact: true }).click(); await expect(page.locator('[data-count]')).toHaveText('8');
  });
}

test('a real busy response with incompatible build ownership follows document recovery', async ({ page, request }) => {
  await page.goto(origin); await ready(page);
  await page.evaluate(() => {
    window.admissionOwnership = 'old document';
    const link = document.createElement('a'); link.id = 'admission-stream-link'; link.href = '/stream'; link.textContent = 'Open admitted stream'; link.setAttribute('data-nextgo-prefetch', 'false'); document.querySelector('main').prepend(link);
  });
  await operation(request, 'hold');
  let inspected = false;
  await page.route('**/stream', async route => {
    if (route.request().headers()['x-nextgo-navigate'] !== '1') { await route.continue(); return; }
    const response = await route.fetch(); saturated(response);
    expect(response.headers()['x-nextgo-stream-admission']).toBe('busy');
    const body = await response.json(); body.build = 'fixture-incompatible-build'; inspected = true;
    await route.fulfill({ response, json: body });
  });
  const document = page.waitForResponse(response => response.request().isNavigationRequest() && new URL(response.url()).pathname === '/stream');
  await page.locator('#admission-stream-link').click(); saturated(await document);
  await expect(page).toHaveURL(`${origin}/stream`);
  expect(inspected).toBe(true); expect(await page.evaluate(() => window.admissionOwnership)).toBeUndefined();
  await expect(page.locator('[data-stream-loaded]')).toHaveCount(0);
});
