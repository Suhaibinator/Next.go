import { test, expect } from '@playwright/test';
import { token, cacheURL, firstURL, secondURL } from './shared-cache-fixture.mjs';

const generation = page => page.locator('[data-shared-generation]');
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const expire = page => page.getByRole('button', { name: 'Expire shared generation', exact: true });
const cacheState = page => page.locator('[data-shared-cache-state]');
const snapshotID = { Namespace: 'demo', Schema: 'demo-shared-v1', Key: 'snapshot' };
const snapshotPolicy = { FreshFor: 15_000_000_000, ExpireAfter: 60_000_000_000, Tags: ['demo-shared'], Paths: ['/shared-cache'] };
const generationFromHTML = html => html.match(/data-shared-generation="">([a-f0-9]{32})<\/code>/)?.[1];
async function cacheOperation(request, operation, values) {
  const result = await request.post(cacheURL, { headers: { Authorization: `Bearer ${token}` }, data: { Version: 3, Operation: operation, ...values } });
  expect(result.status()).toBe(200);
  return result.json();
}
async function markStale(request, window = 30_000_000_000) {
  return cacheOperation(request, 'mark-stale', { Namespace: 'demo', Selector: { Kind: 'tag', Value: 'demo-shared' }, Window: window });
}
async function warmGeneration(request) {
  const response = await request.get(`${firstURL}/shared-cache`);
  expect(response.status()).toBe(200);
  const value = generationFromHTML(await response.text());
  expect(value).toMatch(/^[a-f0-9]{32}$/);
  return value;
}
async function replicaGeneration(request) {
  const response = await request.get(`${secondURL}/shared-cache`);
  expect(response.status()).toBe(200);
  const value = generationFromHTML(await response.text());
  expect(value).toMatch(/^[a-f0-9]{32}$/);
  return value;
}
async function selectedCacheSnapshot(request) {
  // A read-only peek is deliberately absent from the cache contract. A busy
  // owner is observed without a new lease; otherwise retire our probe lease.
  const result = await cacheOperation(request, 'begin-stale', { ID: snapshotID, Policy: snapshotPolicy, LeaseFor: 1_000_000_000 });
  const view = result.View;
  if (view.Lease) await cacheOperation(request, 'release', { ID: snapshotID, Token: view.Lease });
  return { busy: view.Busy, generation: view.Found ? JSON.parse(Buffer.from(view.Value, 'base64').toString('utf8')).Generation : undefined };
}
async function refreshedGeneration(request, old) {
  let next;
  // Poll the backend directly so an ordinary app read cannot hide a broken
  // completion worker by taking over its expired lease and loading the value.
  await expect.poll(async () => {
    const snapshot = await selectedCacheSnapshot(request);
    next = snapshot.generation;
    return !snapshot.busy && typeof next === 'string' && next !== old;
  }).toBe(true);
  return next;
}
test.beforeAll(async ({ request }) => {
  await expect.poll(async () => { try { return (await request.get(`${secondURL}/api/health`)).status(); } catch { return 0; } }).toBe(200);
});
test.beforeEach(async ({ request }) => {
  await cacheOperation(request, 'expire', { Namespace: 'demo', Selector: { Kind: 'all', Value: '' } });
});

test('shared generation is useful before WASM and hydration preserves its nodes', async ({ page, request }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  try {
    const response = await page.goto('/shared-cache', { waitUntil: 'domcontentloaded' });
    expect(response.status()).toBe(200); expect(response.headers()['cache-control']).toContain('no-store');
    const first = await generation(page).textContent(); expect(first).toMatch(/^[a-f0-9]{32}$/);
    await page.evaluate(() => { window.sharedNode = document.querySelector('[data-shared-generation]'); window.sharedText = window.sharedNode.firstChild; });
    const replica = await request.get(`${secondURL}/shared-cache`);
    expect(replica.status()).toBe(200); expect(await replica.text()).toContain(`data-shared-generation="">${first}</code>`);
    release(); await ready(page);
    expect(await page.evaluate(() => window.sharedNode === document.querySelector('[data-shared-generation]') && window.sharedText === window.sharedNode.firstChild)).toBe(true);
    expect(await page.content()).not.toContain(token);
  } finally { release(); }
});

test('native expiration reaches the other replica and Go navigation preserves the document', async ({ page, request }) => {
  const browserRequests = [];
  page.on('request', request => browserRequests.push(request.url()));
  await page.goto('/shared-cache'); await ready(page);
  const old = await generation(page).textContent();
  await expire(page).click(); await ready(page);
  await expect(generation(page)).not.toHaveText(old);
  const current = await generation(page).textContent();
  const other = await request.get(`${secondURL}/shared-cache`);
  expect(await other.text()).toContain(`data-shared-generation="">${current}</code>`);
  await page.evaluate(() => { window.sharedDocument = 'same'; window.sharedHeader = document.querySelector('header'); });
  await page.getByRole('link', { name: 'Compare the process-local page cache', exact: true }).click();
  await expect(page).toHaveURL(`${firstURL}/cache`);
  await page.getByRole('link', { name: 'Try shared data across two app processes', exact: true }).click();
  await expect(generation(page)).toHaveText(current);
  expect(await page.evaluate(() => window.sharedDocument === 'same' && window.sharedHeader === document.querySelector('header'))).toBe(true);
  await page.goBack(); await expect(page).toHaveURL(`${firstURL}/cache`);
  await page.goForward(); await expect(generation(page)).toHaveText(current);
  expect(browserRequests.some(url => new URL(url).origin === cacheURL)).toBe(false);
});

test('stale SSR stays visible while completion work publishes a generation shared by the other replica', async ({ page, request }) => {
  const old = await warmGeneration(request);
  await markStale(request);
  const response = await page.goto('/shared-cache?refresh=background');
  expect(response.status()).toBe(200);
  expect(response.headers()['x-demo-shared-cache']).toBe('stale');
  await expect(generation(page)).toHaveText(old);
  await expect(cacheState(page)).toHaveText('stale');
  await ready(page);
  await page.evaluate(() => { window.sharedRefreshDocument = 'retained'; window.sharedRefreshHeader = document.querySelector('header'); });
  const next = await refreshedGeneration(request, old);
  expect(await replicaGeneration(request)).toBe(next);
  // Adoption is an explicit navigation; background work does not replace the
  // already delivered DOM or mutate the selected generation in place.
  await expect(generation(page)).toHaveText(old);
  await page.getByRole('link', { name: 'Compare the process-local page cache', exact: true }).click();
  await expect(page).toHaveURL(`${firstURL}/cache`);
  await page.getByRole('link', { name: 'Try shared data across two app processes', exact: true }).click();
  await expect(generation(page)).toHaveText(next);
  await expect(cacheState(page)).toHaveText('fresh');
  expect(await page.evaluate(() => window.sharedRefreshDocument === 'retained' && window.sharedRefreshHeader === document.querySelector('header'))).toBe(true);
});

test('failed background work retains last-good data and the same Go document can recover explicitly', async ({ page, request }) => {
  const old = await warmGeneration(request);
  await markStale(request);
  const response = await page.goto('/shared-cache?refresh=background&fail=1');
  expect(response.status()).toBe(200);
  expect(response.headers()['x-demo-shared-cache']).toBe('stale');
  await expect(generation(page)).toHaveText(old);
  await expect(cacheState(page)).toHaveText('stale');
  await ready(page);
  await expect.poll(() => selectedCacheSnapshot(request)).toEqual({ busy: false, generation: old });
  const retained = await request.get(`${secondURL}/shared-cache?refresh=background&fail=1`);
  expect(retained.status()).toBe(200);
  expect(retained.headers()['x-demo-shared-cache']).toBe('stale');
  expect(generationFromHTML(await retained.text())).toBe(old);
  await expect.poll(() => selectedCacheSnapshot(request)).toEqual({ busy: false, generation: old });
  await page.evaluate(() => { window.sharedFailureDocument = 'retained'; });
  await page.getByRole('link', { name: 'Compare the process-local page cache', exact: true }).click();
  await page.getByRole('link', { name: 'Try shared data across two app processes', exact: true }).click();
  await expect(generation(page)).not.toHaveText(old);
  const repaired = await generation(page).textContent();
  await expect(cacheState(page)).toHaveText(/^(miss|fresh)$/);
  expect(await page.evaluate(() => window.sharedFailureDocument)).toBe('retained');
  expect(await replicaGeneration(request)).toBe(repaired);
});

test('hard expiry refuses stale data during a loader failure and a later request repairs the cache', async ({ page, request }) => {
  const old = await warmGeneration(request);
  await markStale(request, 1_000_000);
  await expect.poll(async () => (await request.get(`${firstURL}/shared-cache?refresh=background&fail=1`)).status()).toBe(500);
  const failed = await page.goto('/shared-cache?refresh=background&fail=1');
  expect(failed.status()).toBe(500);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Something went wrong');
  expect(await page.content()).not.toContain(old);
  expect(await page.content()).not.toContain(token);
  const repaired = await page.goto('/shared-cache?refresh=background');
  expect(repaired.status()).toBe(200);
  await ready(page);
  await expect(generation(page)).not.toHaveText(old);
  await expect(cacheState(page)).toHaveText('miss');
  expect(await replicaGeneration(request)).toBe(await generation(page).textContent());
});

test.describe('native shared data without JavaScript', () => {
  test.use({ javaScriptEnabled: false });
  test('ordinary POST returns fresh SSR shared with another instance', async ({ page, request }) => {
    await page.goto('/shared-cache');
    const old = await generation(page).textContent();
    await expire(page).click();
    await expect(generation(page)).not.toHaveText(old);
    const current = await generation(page).textContent();
    const other = await request.get(`${secondURL}/shared-cache`);
    expect(other.status()).toBe(200); expect(await other.text()).toContain(`data-shared-generation="">${current}</code>`);
    expect(await page.content()).not.toContain(token);
  });

  test('native stale marking returns last-good SSR before the other instance observes refresh', async ({ page, request }) => {
    await page.goto('/shared-cache');
    const old = await generation(page).textContent();
    await page.getByRole('button', { name: 'Mark shared generation stale', exact: true }).click();
    await expect(page).toHaveURL(`${firstURL}/shared-cache?refresh=background`);
    await expect(generation(page)).toHaveText(old);
    await expect(cacheState(page)).toHaveText('stale');
    const refreshed = await refreshedGeneration(request, old);
    expect(await replicaGeneration(request)).toBe(refreshed);
    await expect(generation(page)).toHaveText(old);
    expect(await page.content()).not.toContain(token);
  });
});
