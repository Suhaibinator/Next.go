import { test, expect } from '@playwright/test';

const origin = 'http://127.0.0.1:4163';
const shell = page => page.locator('[data-partial-shell]');
const generation = page => page.locator('[data-partial-shell-generation]');
const count = (page, name) => page.getByRole('region', { name: `Partial ${name} counter`, exact: true }).locator('[data-count]');
const increment = (page, name) => page.getByRole('button', { name: `Increase Partial ${name} counter`, exact: true });
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const region = page => expect(page.locator('[data-partial-region]')).toBeVisible();
async function head(page, id) {
  expect(id).toMatch(/^[a-f0-9]{32}$/); await expect(page).toHaveTitle(new RegExp(id));
  await expect(page.locator('meta[name="partial-shell-generation"]')).toHaveAttribute('content', id);
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', new RegExp(id));
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', new RegExp(id));
  expect(JSON.parse(await page.locator('head script[type="application/ld+json"][data-nextgo-head]').textContent()).identifier).toBe(id);
}
function partialResponse(response) {
  expect(response.status()).toBe(200); expect(response.headers()['x-nextgo-partial']).toBe('1');
  expect(response.headers()['x-nextgo-prerender']).toBe('build');
  expect(response.headers()['cache-control']).toContain('private'); expect(response.headers()['cache-control']).toContain('no-store');
}

test('a genuine build shell survives broken static providers and hydrates before request content arrives', async ({ page, request }) => {
  let release; const gate = new Promise(resolve => { release = resolve; });
  const key = `${test.info().project.name}-${Date.now()}`;
  await page.setExtraHTTPHeaders({ 'X-Fixture-Partial-Gate': key });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  try {
    const response = await page.goto('/partial?message=Private%20hello', { waitUntil: 'commit' }); partialResponse(response);
    await expect(page.locator('[data-partial-loading]')).toBeVisible(); await expect(count(page, 'shell')).toHaveText('3');
    await expect(increment(page, 'shell')).toBeDisabled(); const id = await generation(page).textContent(); await head(page, id);
    await page.evaluate(() => { window.partialShell = document.querySelector('[data-partial-shell]'); window.partialHeading = document.querySelector('h1'); window.partialButton = document.querySelector('[aria-label="Increase Partial shell counter"]'); window.partialText = document.querySelector('[data-partial-shell] [data-count]').firstChild; });
    release(); await ready(page); await expect(page.locator('[data-partial-loading]')).toBeVisible();
    await increment(page, 'shell').click(); await expect(count(page, 'shell')).toHaveText('4');
    expect((await request.post(`${origin}/_fixture/release-region?key=${encodeURIComponent(key)}`)).status()).toBe(200);
    await region(page); await ready(page); await expect(page.locator('[data-partial-message]')).toHaveText('Private hello');
    await expect(count(page, 'request')).toHaveText('10'); await increment(page, 'request').click(); await expect(count(page, 'request')).toHaveText('11'); await expect(count(page, 'shell')).toHaveText('4');
    expect(await page.evaluate(() => window.partialShell === document.querySelector('[data-partial-shell]') && window.partialHeading === document.querySelector('h1') && window.partialButton === document.querySelector('[aria-label="Increase Partial shell counter"]') && window.partialText === document.querySelector('[data-partial-shell] [data-count]').firstChild)).toBe(true);
  } finally { release(); }
});

test('private query, cookie and current process are refreshed without changing the public shell', async ({ page, context }) => {
  const message = '<img src=x onerror="window.partialInjection=true"> & private';
  await context.addCookies([{ name: 'nextgo_partial_name', value: 'Alice', url: origin }]);
  partialResponse(await page.goto(`/partial/alpha?message=${encodeURIComponent(message)}`)); await region(page); await ready(page);
  const id = await generation(page).textContent(); const requestID = await page.locator('[data-partial-request-id]').textContent(); const process = await page.locator('[data-partial-region-process]').textContent(); await head(page, id);
  await expect(page.locator('[data-partial-message]')).toHaveText(message); await expect(page.locator('[data-partial-visitor]')).toHaveText('Alice'); await expect(page.locator('[data-partial-region] img')).toHaveCount(0);
  await context.addCookies([{ name: 'nextgo_partial_name', value: 'Bob', url: origin }]); await page.setExtraHTTPHeaders({ 'X-Fixture-Partial': 'second' });
  partialResponse(await page.goto('/partial/alpha?message=Different%20private%20request')); await region(page); await ready(page);
  await expect(generation(page)).toHaveText(id); await head(page, id); await expect(page.locator('[data-partial-message]')).toHaveText('Different private request'); await expect(page.locator('[data-partial-visitor]')).toHaveText('Bob');
  await expect(page.locator('[data-partial-request-id]')).not.toHaveText(requestID); await expect(page.locator('[data-partial-region-process]')).not.toHaveText(process);
  expect(await page.evaluate(() => window.partialInjection)).toBeUndefined();
  const secondID = await page.locator('[data-partial-request-id]').textContent(); partialResponse(await page.reload()); await region(page); await expect(page.locator('[data-partial-request-id]')).not.toHaveText(secondID);
});

test('dynamic shells support direct access, soft navigation, current params and fresh history regions', async ({ page, context }) => {
  partialResponse(await page.goto('/partial')); await region(page); await ready(page); const root = await generation(page).textContent();
  await page.locator('[data-layout-increment]').click(); await increment(page, 'shell').click();
  await page.evaluate(() => { window.partialDocument = true; window.partialHeader = document.querySelector('header'); });
  await page.getByRole('link', { name: 'Partial alpha', exact: true }).click(); await region(page); await expect(page).toHaveURL(`${origin}/partial/alpha`);
  const alpha = await generation(page).textContent(); await head(page, alpha); await expect(page.locator('[data-partial-region-slug]')).toHaveText('alpha'); await expect(page.locator('[data-partial-region-prop]')).toHaveText('alpha'); await expect(page.locator('[data-partial-region-path]')).toHaveText('/partial/alpha'); await expect(count(page, 'shell')).toHaveText('3');
  await page.getByRole('link', { name: 'Partial beta', exact: true }).click(); await region(page); await expect(page).toHaveURL(`${origin}/partial/beta`); const beta = await generation(page).textContent(); await head(page, beta);
  await context.addCookies([{ name: 'nextgo_partial_name', value: 'History visitor', url: origin }]);
  await page.goBack(); await expect(generation(page)).toHaveText(alpha); await expect(page.locator('[data-partial-visitor]')).toHaveText('History visitor'); await head(page, alpha);
  await page.goBack(); await expect(generation(page)).toHaveText(root); await expect(page.locator('[data-partial-visitor]')).toHaveText('History visitor'); await head(page, root);
  await expect(page.locator('[data-layout-count]')).toHaveText('1'); expect(await page.evaluate(() => window.partialDocument && window.partialHeader === document.querySelector('header'))).toBe(true);
  await page.goForward(); await expect(generation(page)).toHaveText(alpha); await region(page);
});

test('request-region failures stay generic and a repaired Go retry completes the same build shell', async ({ page }) => {
  await page.goto('/partial'); await region(page); await ready(page);
  await page.setExtraHTTPHeaders({ 'X-Fixture-Partial': 'broken' }); await page.getByRole('link', { name: 'Partial alpha', exact: true }).click();
  const retry = page.getByRole('button', { name: 'Retry loading content', exact: true }); await expect(retry).toBeVisible(); const id = await generation(page).textContent(); await head(page, id);
  expect(await page.content()).not.toContain('private partial region provider'); await increment(page, 'shell').click(); await expect(count(page, 'shell')).toHaveText('4');
  await page.setExtraHTTPHeaders({ 'X-Fixture-Partial': 'first' }); await retry.click(); await region(page); await ready(page); await expect(generation(page)).toHaveText(id); await head(page, id);
  await increment(page, 'request').click(); await expect(count(page, 'request')).toHaveText('11');
});

test('a malformed request-region stream fails closed and retries its real server output', async ({ page }) => {
  await page.goto('/partial'); await region(page); await ready(page);
  let changed = false;
  await page.route('**/partial/alpha', async route => {
    if (changed || route.request().headers()['x-nextgo-stream'] !== '1') { await route.continue(); return; }
    const response = await route.fetch(); partialResponse(response);
    const frames = (await response.text()).trimEnd().split('\n').map(line => JSON.parse(line));
    const chunk = frames.find(frame => frame.type === 'chunk'); expect(chunk).toBeTruthy(); chunk.html = '<nextgo-layout data-partial-forged="true">must remain inert</nextgo-layout>'; changed = true;
    await route.fulfill({ response, body: frames.map(frame => JSON.stringify(frame)).join('\n') + '\n' });
  });
  await page.getByRole('link', { name: 'Partial alpha', exact: true }).click(); const retry = page.getByRole('button', { name: 'Retry loading content', exact: true }); await expect(retry).toBeVisible();
  await expect(page.locator('[data-partial-forged]')).toHaveCount(0); await expect(shell(page)).toBeVisible();
  await retry.click(); await region(page); await ready(page); await expect(page.locator('[data-partial-region-slug]')).toHaveText('alpha');
});

test('invalid region inputs cannot poison a known shell or leak provider diagnostics', async ({ page }) => {
  partialResponse(await page.goto('/partial/alpha?delay=3s')); await ready(page); await expect(page.getByText('Unable to load content.', { exact: true })).toBeVisible();
  const id = await generation(page).textContent(); expect(await page.content()).not.toContain('private partial delay'); await increment(page, 'shell').click(); await expect(count(page, 'shell')).toHaveText('4');
  await page.getByRole('link', { name: 'Partial alpha', exact: true }).click(); await region(page); await ready(page); await expect(generation(page)).toHaveText(id);
});

test.describe('public shell without JavaScript', () => {
  test.use({ javaScriptEnabled: false });
  test('useful build HTML and metadata remain visible with an accessible region fallback', async ({ page }) => {
    partialResponse(await page.goto('/partial/beta?message=Private%20no%20script')); const id = await generation(page).textContent(); await head(page, id);
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('A reusable public shell: beta'); await expect(page.locator('[data-partial-loading]')).toBeVisible(); await expect(page.locator('[data-partial-region]')).toHaveCount(0); await expect(count(page, 'shell')).toHaveText('3'); await expect(increment(page, 'shell')).toBeDisabled();
  });
});
