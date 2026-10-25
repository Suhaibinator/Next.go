import { test, expect } from '@playwright/test';

const origin = 'http://127.0.0.1:4181';
const endpoint = '/_nextgo/actions/generated/add/v1';
const count = page => page.locator('[data-generated-count]');
const revision = page => page.locator('[data-generated-revision]');
const amount = page => page.getByLabel('Generated amount', { exact: true });
const status = page => page.locator('[data-generated-status]');
const jsonButton = page => page.getByRole('button', { name: 'Save with generated Go caller', exact: true });
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
async function number(locator) { return Number(await locator.textContent()); }
async function submit(page, button, expected = 200) {
  const waiting = page.waitForResponse(response => new URL(response.url()).pathname === endpoint && response.request().method() === 'POST');
  await button.click(); const response = await waiting;
  expect(response.status()).toBe(expected); expect(response.headers()['x-nextgo-action-protocol']).toBe('1'); expect(response.headers()['x-nextgo-action-revision']).toBe('v1');
  return response;
}

test('generated JSON caller hydrates existing SSR nodes and updates confirmed state without a reload', async ({ page, request }) => {
  const key = `generated-wasm-${test.info().project.name}`;
  await page.setExtraHTTPHeaders({ 'X-Fixture-Generated-Wasm': key });
  try {
    expect((await page.goto('/generated-actions', { waitUntil: 'commit' })).status()).toBe(200);
    await expect.poll(async () => (await (await request.get(`${origin}/_fixture/gate?key=${key}`)).json()).held).toBe(true);
    await expect(page.getByRole('heading', { name: 'Generated Go actions', exact: true })).toBeVisible();
    const initial = await number(count(page)), version = await number(revision(page));
    await expect(jsonButton(page)).toBeDisabled(); await expect(page.getByRole('button', { name: 'Save with native generated form', exact: true })).toBeEnabled();
    await page.evaluate(() => { window.generatedDocument = true; window.generatedRoot = document.querySelector('[data-nextgo-island="generatedaction"]'); window.generatedCount = document.querySelector('[data-generated-count]'); window.generatedText = window.generatedCount.firstChild; window.generatedButton = document.querySelector('[data-generated-json]'); });
    expect((await request.post(`${origin}/_fixture/release?key=${key}`)).status()).toBe(200); await ready(page);
    await amount(page).fill('2'); const response = await submit(page, jsonButton(page)); const body = await response.json();
    expect(body.data).toEqual({ count: initial + 2, revision: version + 1 });
    await expect(count(page)).toHaveText(String(initial + 2)); await expect(revision(page)).toHaveText(String(version + 1)); await expect(status(page)).toHaveText('Saved with generated json caller.');
    expect(await page.evaluate(() => window.generatedDocument && window.generatedRoot === document.querySelector('[data-nextgo-island="generatedaction"]') && window.generatedCount === document.querySelector('[data-generated-count]') && window.generatedText === document.querySelector('[data-generated-count]').firstChild && window.generatedButton === document.querySelector('[data-generated-json]'))).toBe(true);
  } finally { await request.post(`${origin}/_fixture/release?key=${key}`); }
});

test('generated multipart caller reaches the same native contract and returns typed output', async ({ page }) => {
  await page.goto('/generated-actions'); await ready(page); const initial = await number(count(page));
  await amount(page).fill('3'); await page.evaluate(() => { window.generatedDocument = true; });
  const response = await submit(page, page.getByRole('button', { name: 'Save with generated multipart caller', exact: true }));
  expect(response.request().headers()['content-type']).toMatch(/^multipart\/form-data; boundary=/);
  expect(response.request().postData()).toContain('name="amount"'); expect((await response.json()).data.count).toBe(initial + 3);
  await expect(count(page)).toHaveText(String(initial + 3)); await expect(status(page)).toHaveText('Saved with generated multipart caller.'); expect(await page.evaluate(() => window.generatedDocument)).toBe(true);
});

test('validation and private native errors preserve confirmed state and allow a valid retry', async ({ page }) => {
  await page.goto('/generated-actions'); await ready(page); const initial = await number(count(page)), version = await number(revision(page));
  await amount(page).fill('0'); const invalid = await submit(page, jsonButton(page), 422);
  expect((await invalid.json()).error.fields.amount).toBe('Enter a whole number from 1 to 5.');
  await expect(page.locator('[data-generated-error]')).toHaveText('Enter a whole number from 1 to 5.');
  await amount(page).fill('1'); const failure = await submit(page, page.getByRole('button', { name: 'Try a private server failure', exact: true }), 500);
  expect((await failure.json()).error.message).toBe('The action could not be completed.'); await expect(status(page)).toHaveText('The action could not be completed.');
  expect(await page.content()).not.toContain('private generated demo storage detail'); await expect(count(page)).toHaveText(String(initial)); await expect(revision(page)).toHaveText(String(version));
  await submit(page, jsonButton(page)); await expect(count(page)).toHaveText(String(initial + 1)); await expect(page.locator('[data-generated-error]')).toBeEmpty();
});

test('declared revalidation expires cached SSR data and refreshes page metadata', async ({ page, request }) => {
  await page.goto('/generated-actions'); await ready(page);
  const version = await number(revision(page)), load = await page.locator('[data-generated-ssr-load]').textContent();
  const warm = await request.get(`${origin}/generated-actions`);
  expect(await warm.text()).toContain(`data-generated-ssr-load="">${load}</output>`);
  await amount(page).fill('1'); await submit(page, jsonButton(page)); await expect(revision(page)).toHaveText(String(version + 1));
  await expect(page.locator('[data-generated-ssr-revision]')).toHaveText(String(version));
  await page.evaluate(() => { window.generatedDocument = true; });
  await page.getByRole('button', { name: 'Refresh generated server snapshot', exact: true }).click();
  await expect(page.locator('[data-generated-ssr-revision]')).toHaveText(String(version + 1));
  await expect(page.locator('[data-generated-ssr-load]')).not.toHaveText(load);
  await expect(page).toHaveTitle(`Generated actions revision ${version + 1} — Next.go`);
  await expect(page.locator('meta[name="generated-action-revision"]')).toHaveAttribute('content', String(version + 1)); expect(await page.evaluate(() => window.generatedDocument)).toBe(true);
});

test('unmount cancels a generated call without updating its detached island', async ({ page, request }) => {
  const key = `generated-flight-${test.info().project.name}`;
  await page.goto('/generated-actions'); await ready(page); const initial = await number(count(page));
  await page.evaluate(() => { window.generatedDetached = document.querySelector('[data-nextgo-island="generatedaction"]'); });
  await page.setExtraHTTPHeaders({ 'X-Fixture-Generated-Action': key }); await jsonButton(page).click();
  await expect.poll(async () => (await (await request.get(`${origin}/_fixture/gate?key=${key}`)).json()).held).toBe(true);
  const flight = await (await request.get(`${origin}/_fixture/gate?key=${key}`)).json();
  await page.getByRole('link', { name: 'Try existing manual actions', exact: true }).click(); await expect(page).toHaveURL(`${origin}/features`); await ready(page);
  expect(await page.evaluate(() => window.generatedDetached.getAttribute('data-generated-closed'))).toBe('true');
  await expect.poll(async () => (await (await request.get(`${origin}/_fixture/records`)).json()).some(record => record.id === flight.id && record.path === endpoint && record.aborted)).toBe(true);
  await request.post(`${origin}/_fixture/release?key=${key}`);
  expect(await page.evaluate(() => window.generatedDetached.querySelector('[data-generated-count]').textContent)).toBe(String(initial));
  await expect(page.getByRole('heading', { name: 'Framework features', exact: true })).toBeVisible();
});

test('automatic generated registration preserves existing manually configured action handlers', async ({ page }) => {
  await page.goto('/generated-actions'); await ready(page);
  await page.getByRole('link', { name: 'Try existing manual actions', exact: true }).click(); await ready(page);
  const initial = await number(page.locator('[data-server-count]'));
  const waiting = page.waitForResponse(response => new URL(response.url()).pathname === '/_nextgo/actions/increment' && response.request().method() === 'POST');
  await page.getByRole('button', { name: 'Increment without refreshing page', exact: true }).click(); expect((await waiting).status()).toBe(200);
  await expect(page.locator('[data-server-count]')).toHaveText(String(initial + 1));
});

test.describe('generated native form without JavaScript', () => {
  test.use({ javaScriptEnabled: false });
  test('the generated path submits a native form and returns fresh HTML through a303 redirect', async ({ page }) => {
    await page.goto('/generated-actions'); const initial = await number(count(page)), version = await number(revision(page)); await amount(page).fill('2');
    const waiting = page.waitForResponse(response => new URL(response.url()).pathname === endpoint && response.request().method() === 'POST');
    await page.getByRole('button', { name: 'Save with native generated form', exact: true }).click(); const response = await waiting;
    expect(response.status()).toBe(303); expect(response.headers().location).toBe('/generated-actions');
    await expect(page).toHaveURL(`${origin}/generated-actions`); await expect(count(page)).toHaveText(String(initial + 2));
    await expect(page).toHaveTitle(`Generated actions revision ${version + 1} — Next.go`); await expect(jsonButton(page)).toBeDisabled();
  });
});
