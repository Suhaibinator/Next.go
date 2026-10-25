import { test, expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';

const origin = 'http://127.0.0.1:4185';
const actionPath = '/_nextgo/actions/view/save/v1';
const endpoint = path => path.endsWith(actionPath);
const ssrCount = page => page.locator('[data-action-view-ssr-count]');
const ssrRevision = page => page.locator('[data-action-view-ssr-revision]');
const amount = page => page.locator('[data-av-amount]');
const receipt = page => page.locator('[data-av-receipt]');
const jsonButton = page => page.locator('[data-av-json]');
const number = async locator => Number(await locator.textContent());
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const fixtureURL = (operation, key) => `${origin}/_fixture/action-view/${operation}?${new URLSearchParams({ key })}`;
const headers = (trace, extra = {}) => ({ 'X-Fixture-Action-View-Trace': trace, ...extra });
async function gate(request, key) { return (await request.get(fixtureURL('gate', key))).json(); }
async function held(request, key, phase) {
  await expect.poll(async () => { const value = await gate(request, key); return value.held && value.phase; }).toBe(phase);
  return gate(request, key);
}
async function records(request, trace) { return (await (await request.get(`${origin}/_fixture/action-view/records`)).json()).filter(record => record.trace === trace); }
async function post(page, button, expected = 200) {
  const waiting = page.waitForResponse(response => endpoint(new URL(response.url()).pathname) && response.request().method() === 'POST');
  await button.click(); const response = await waiting;
  expect(response.status()).toBe(expected);
  expect(response.headers()['x-nextgo-action-protocol']).toBe('1');
  expect(response.headers()['x-nextgo-action-revision']).toBe('v1');
  return response;
}
async function assertReceipt(page, count, revision, { applied = true, error = false, status = 200 } = {}) {
  await expect(receipt(page)).toHaveAttribute('data-confirmed', 'true');
  await expect(receipt(page)).toHaveAttribute('data-applied', String(applied));
  await expect(receipt(page)).toHaveAttribute('data-view-error', String(error));
  await expect(receipt(page)).toHaveAttribute('data-view-status', String(status));
  await expect(page.locator('[data-av-confirmed-count]')).toHaveText(String(count));
  await expect(page.locator('[data-av-confirmed-revision]')).toHaveText(String(revision));
}
async function assertFresh(page, count, revision) {
  await expect(ssrCount(page)).toHaveText(String(count)); await expect(ssrRevision(page)).toHaveText(String(revision));
  await expect(page).toHaveTitle(`Action view revision ${revision} — Next.go`);
  await expect(page.locator('meta[name="action-view-revision"]')).toHaveAttribute('content', String(revision));
}
async function assertStreamedCounter(page, value) {
  await expect(page.locator('[data-action-view-deferred]')).toHaveText(String(value));
  const region = page.getByRole('region', { name: 'Action view streamed counter', exact: true });
  await expect(region.locator('[data-count]')).toHaveText(String(value));
  await region.getByRole('button', { name: 'Increase Action view streamed counter', exact: true }).click();
  await expect(region.locator('[data-count]')).toHaveText(String(value + 1));
}

test('SSR survives delayed WASM, then one JSON POST confirms data and refreshes HTML and metadata', async ({ page, request }) => {
  const trace = randomUUID(), wasmKey = `${trace}-wasm`, key = `${trace}-result`;
  await page.setExtraHTTPHeaders(headers(trace, { 'X-Fixture-Action-View-Wasm': wasmKey }));
  try {
    expect((await page.goto('/action-view?delay=750ms#keep', { waitUntil: 'commit' })).status()).toBe(200);
    await held(request, wasmKey, 'wasm');
    await expect(page.getByRole('heading', { name: 'One action, one streamed view', exact: true })).toBeVisible();
    const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page));
    await expect(jsonButton(page)).toBeDisabled();
    await expect(page.getByRole('button', { name: 'Submit native action view form', exact: true })).toBeEnabled();
    await page.evaluate(() => { window.avDocument = true; window.avRoot = document.querySelector('[data-nextgo-island="actionview"]'); window.avCount = document.querySelector('[data-av-count]'); window.avText = window.avCount.firstChild; });
    expect((await request.post(fixtureURL('release', wasmKey))).status()).toBe(200); await ready(page);
    expect(await page.evaluate(() => window.avRoot === document.querySelector('[data-nextgo-island="actionview"]') && window.avCount === document.querySelector('[data-av-count]') && window.avText === document.querySelector('[data-av-count]').firstChild)).toBe(true);
    const history = await page.evaluate(() => window.history.length);
    await page.setExtraHTTPHeaders(headers(trace, { 'X-Fixture-Action-View-Gate': key, 'X-Fixture-Action-View-Phase': 'result' }));
    await amount(page).fill('2'); const response = await post(page, jsonButton(page));
    expect(response.headers()['content-type']).toContain('application/x-nextgo-action-view+json'); expect(response.headers()['cache-control']).toBe('private, no-store');
    const flight = await held(request, key, 'result');
    expect(flight.result.data).toEqual({ count: initial + 2, revision: revision + 1 });
    await expect(ssrCount(page)).toHaveText(String(initial));
    expect(await page.evaluate(() => window.avRoot === document.querySelector('[data-nextgo-island="actionview"]') && window.avCount === document.querySelector('[data-av-count]') && window.avText === document.querySelector('[data-av-count]').firstChild)).toBe(true);
    expect((await request.post(fixtureURL('release', key))).status()).toBe(200);
    await assertFresh(page, initial + 2, revision + 1); await assertReceipt(page, initial + 2, revision + 1); await assertStreamedCounter(page, initial + 2);
    await expect(page).toHaveURL(`${origin}/action-view?delay=750ms#keep`);
    expect(await page.evaluate(() => window.avDocument && window.history.length)).toBe(history);
    expect(await page.evaluate(initial => !window.avRoot.isConnected && window.avRoot.getAttribute('data-av-closed') === 'true' && window.avCount.firstChild === window.avText && window.avCount.textContent === String(initial), initial)).toBe(true);
    const actual = await records(request, trace);
    expect(actual.filter(record => record.method === 'GET')).toHaveLength(1);
    expect(actual.filter(record => record.method === 'POST')).toHaveLength(1);
    expect(actual.find(record => record.method === 'POST').source).toBe('/action-view?delay=750ms');
    // DevTools can discard a completed streaming response body even for an
    // equivalent native JavaScript client. Check genuine wire framing/EOF on
    // this exact flight, independently of the preceding Go UI/receipt checks.
    await expect.poll(async () => (await records(request, trace)).find(record => record.id === flight.id)?.wire?.complete).toBe(true);
    const finished = (await records(request, trace)).find(record => record.id === flight.id);
    expect(finished.status).toBe(200); expect(finished.aborted).toBe(false); expect(finished.complete).toBe(true);
    const wire = finished.wire; expect(wire.error).toBe(false); expect(wire.bytes).toBeGreaterThan(0); expect(wire.bytes).toBeLessThanOrEqual(8 << 20);
    expect(wire.frameTypes[0]).toBe('result'); expect(wire.frameTypes[1]).toBe('shell'); expect(wire.frameTypes.at(-1)).toBe('done'); expect(wire.frameTypes).toContain('chunk'); expect(wire.frameTypes.length).toBeLessThanOrEqual(131); expect(wire.versions.every(version => version === 1)).toBe(true);
  } finally { await request.post(fixtureURL('release', wasmKey)); await request.post(fixtureURL('release', key)); }
});

test('a multipart generated call returns typed data and a streamed Go counter through the same POST', async ({ page, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(headers(trace)); await page.goto('/action-view'); await ready(page);
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page)); await amount(page).fill('3');
  await page.evaluate(() => { window.avDocument = true; });
  const response = await post(page, page.locator('[data-av-multipart]'));
  expect(response.request().headers()['content-type']).toMatch(/^multipart\/form-data; boundary=/);
  expect(response.request().postData()).toContain('name="amount"');
  await assertFresh(page, initial + 3, revision + 1); await assertReceipt(page, initial + 3, revision + 1); await assertStreamedCounter(page, initial + 3);
  expect(await page.evaluate(() => window.avDocument)).toBe(true);
  const actual = await records(request, trace); expect(actual.filter(record => record.method === 'GET')).toHaveLength(1); expect(actual.filter(record => record.method === 'POST')).toHaveLength(1);
});

test('pre-confirmation validation and private action failures keep the current view and permit a valid retry', async ({ page }) => {
  await page.goto('/action-view'); await ready(page); const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page));
  const invalid = await post(page, page.locator('[data-av-invalid]'), 422);
  expect(invalid.headers()['content-type']).toContain('application/json'); expect((await invalid.json()).error.fields.amount).toBe('Enter a whole number from 1 to 5.');
  await expect(page.locator('[data-av-error]')).toHaveText('Enter a whole number from 1 to 5.');
  await expect(ssrCount(page)).toHaveText(String(initial)); await expect(receipt(page)).toHaveAttribute('data-confirmed', 'false');
  const failure = await post(page, page.locator('[data-av-fail]'), 500);
  expect((await failure.json()).error.message).toBe('The action could not be completed.');
  await expect(page.locator('[data-av-status]')).toHaveText('The action could not be completed.');
  expect(await page.content()).not.toContain('private action view storage diagnostic'); await expect(ssrRevision(page)).toHaveText(String(revision));
  await post(page, jsonButton(page)); await assertFresh(page, initial + 1, revision + 1); await assertReceipt(page, initial + 1, revision + 1);
});

test('a failed deferred view preserves confirmed data and explicit refresh never repeats that mutation', async ({ page, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(headers(trace)); await page.goto('/action-view'); await ready(page);
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page));
  await post(page, page.locator('[data-av-viewfail]'));
  await assertFresh(page, initial + 1, revision + 1); await assertReceipt(page, initial + 1, revision + 1, { error: true });
  await expect(page.locator('[data-av-status]')).toContainText('Refresh without repeating the action.');
  expect(await page.content()).not.toContain('private action view deferred diagnostic');
  expect((await records(request, trace)).filter(record => record.method === 'POST')).toHaveLength(1);
  // An ordinary confirmed mutation changes only component data. It also resets
  // this demo's failure flag; refreshing then reads that same published value.
  await post(page, page.locator('[data-av-only]'));
  await assertReceipt(page, initial + 2, revision + 2, { applied: false, status: 0 });
  await expect(ssrCount(page)).toHaveText(String(initial + 1));
  await page.locator('[data-av-refresh]').click(); await assertFresh(page, initial + 2, revision + 2); await assertStreamedCounter(page, initial + 2);
  expect((await records(request, trace)).filter(record => record.method === 'POST')).toHaveLength(2);
});

test('a transport failure after a real shell commit retains confirmed data and offers reconciliation', async ({ page, request }) => {
  const trace = randomUUID(), key = `${trace}-shell`; await page.setExtraHTTPHeaders(headers(trace)); await page.goto('/action-view?delay=750ms'); await ready(page);
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page));
  try {
    await page.setExtraHTTPHeaders(headers(trace, { 'X-Fixture-Action-View-Gate': key, 'X-Fixture-Action-View-Phase': 'shell' }));
    await post(page, jsonButton(page)); const flight = await held(request, key, 'shell');
    expect(flight.result.data).toEqual({ count: initial + 1, revision: revision + 1 });
    await assertFresh(page, initial + 1, revision + 1); await expect(page.locator('[data-action-view-fallback]')).toBeVisible();
    expect((await request.post(fixtureURL('drop', key))).status()).toBe(200);
    await assertReceipt(page, initial + 1, revision + 1, { error: true });
    await expect(page.locator('[data-av-status]')).toContainText('Refresh without repeating the action.');
    const actual = await records(request, trace); expect(actual.filter(record => record.method === 'GET')).toHaveLength(1); expect(actual.filter(record => record.method === 'POST')).toHaveLength(1);
    await page.setExtraHTTPHeaders(headers(trace)); await page.locator('[data-av-refresh]').click();
    await assertFresh(page, initial + 1, revision + 1); await assertStreamedCounter(page, initial + 1);
    expect((await records(request, trace)).filter(record => record.method === 'POST')).toHaveLength(1);
  } finally { await request.post(fixtureURL('release', key)); }
});

test('navigation cancels the exact held view flight without erasing its already confirmed Go receipt', async ({ page, request }) => {
  const trace = randomUUID(), key = `${trace}-navigate`; await page.setExtraHTTPHeaders(headers(trace)); await page.goto('/action-view?delay=750ms'); await ready(page);
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page));
  try {
    await page.setExtraHTTPHeaders(headers(trace, { 'X-Fixture-Action-View-Gate': key, 'X-Fixture-Action-View-Phase': 'shell' }));
    await post(page, jsonButton(page)); const flight = await held(request, key, 'shell'); await assertFresh(page, initial + 1, revision + 1);
    await page.evaluate(() => { window.avDocument = true; window.avLeaving = document.querySelector('[data-nextgo-island="actionview"]'); });
    await page.getByRole('link', { name: 'Leave action view', exact: true }).click(); await expect(page).toHaveURL(`${origin}/features`); await ready(page);
    await expect.poll(async () => (await records(request, trace)).some(record => record.id === flight.id && record.aborted)).toBe(true);
    expect(await page.evaluate(() => window.avDocument && !window.avLeaving.isConnected && window.avLeaving.getAttribute('data-av-closed') === 'true')).toBe(true);
    // Cancelling the view at navigation start can legitimately publish its
    // receipt while the old page is still connected and loading /features.
    // Establish the no-more-updates baseline only after that page is disposed.
    await page.evaluate(() => { window.avDetachedReceipt = window.avLeaving.querySelector('[data-av-receipt]').outerHTML; });
    await request.post(fixtureURL('release', key));
    expect((await gate(request, key)).held).toBe(false);
    await page.setExtraHTTPHeaders(headers(trace)); await page.goBack(); await expect(page).toHaveURL(`${origin}/action-view?delay=750ms`); await ready(page);
    await assertFresh(page, initial + 1, revision + 1); await assertReceipt(page, initial + 1, revision + 1, { error: true });
    expect(await page.evaluate(() => window.avLeaving.querySelector('[data-av-receipt]').outerHTML === window.avDetachedReceipt)).toBe(true);
    expect((await records(request, trace)).filter(record => record.method === 'POST')).toHaveLength(1);
  } finally { await request.post(fixtureURL('release', key)); }
});

test('a typed mutation waits behind a held view and is never posted after its source island is disposed', async ({ page, request }) => {
  const trace = randomUUID(), key = `${trace}-queued`; await page.setExtraHTTPHeaders(headers(trace)); await page.goto('/action-view?delay=750ms'); await ready(page);
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page));
  try {
    await page.setExtraHTTPHeaders(headers(trace, { 'X-Fixture-Action-View-Gate': key, 'X-Fixture-Action-View-Phase': 'result' }));
    await post(page, jsonButton(page)); const first = await held(request, key, 'result');
    await page.setExtraHTTPHeaders(headers(trace)); await amount(page).fill('2');
    await page.locator('[data-av-only]').click(); await expect(page.locator('[data-av-only]')).toBeDisabled();
    await expect(ssrCount(page)).toHaveText(String(initial)); expect((await records(request, trace)).filter(record => record.method === 'POST')).toHaveLength(1);
    expect((await request.post(fixtureURL('release', key))).status()).toBe(200);
    await assertFresh(page, initial + 1, revision + 1); await assertReceipt(page, initial + 1, revision + 1); await assertStreamedCounter(page, initial + 1);
    const actual = await records(request, trace); expect(actual.filter(record => record.method === 'POST')).toHaveLength(1);
    expect(actual.find(record => record.id === first.id).aborted).toBe(false);
  } finally { await request.post(fixtureURL('release', key)); }
});

for (const skew of ['build', 'configuration', 'protocol']) test(`obsolete ${skew} is rejected before mutation and a valid retry commits exactly once`, async ({ page, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(headers(trace)); await page.goto('/action-view'); await ready(page);
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page));
  await page.setExtraHTTPHeaders(headers(trace, { 'X-Fixture-Action-View-Skew': skew })); const response = await post(page, jsonButton(page), 409);
  expect(response.headers()['content-type']).toContain('application/json'); expect((await response.json()).error.code).toBe('view_unavailable');
  await expect(receipt(page)).toHaveAttribute('data-confirmed', 'false'); await expect(ssrCount(page)).toHaveText(String(initial));
  await page.setExtraHTTPHeaders(headers(trace)); await amount(page).fill('2'); await post(page, jsonButton(page));
  await assertFresh(page, initial + 2, revision + 1); await assertReceipt(page, initial + 2, revision + 1);
  expect((await records(request, trace)).filter(record => record.method === 'POST')).toHaveLength(2);
});

test('the same combined call preserves base path, locale, slash, query and fragment state', async ({ page, request }) => {
  const trace = randomUUID(), path = '/scoped/fr/action-view/?delay=750ms#kept'; await page.setExtraHTTPHeaders(headers(trace)); await page.goto(path); await ready(page);
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page)); const history = await page.evaluate(() => window.history.length);
  await amount(page).fill('2'); const response = await post(page, jsonButton(page));
  expect(new URL(response.url()).pathname).toBe('/scoped/fr' + actionPath);
  await assertFresh(page, initial + 2, revision + 1); await assertReceipt(page, initial + 2, revision + 1); await assertStreamedCounter(page, initial + 2);
  await expect(page).toHaveURL(origin + path); await expect(page.locator('html')).toHaveAttribute('lang', 'fr'); expect(await page.evaluate(() => window.history.length)).toBe(history);
  const actual = await records(request, trace); expect(actual.filter(record => record.method === 'GET')).toHaveLength(1); expect(actual.filter(record => record.method === 'POST')).toHaveLength(1); expect(actual.find(record => record.method === 'POST').source).toBe('/scoped/fr/action-view/?delay=750ms');
});

test('a real lazy Go executable delegates the combined action and view to the core document runtime', async ({ page, request }) => {
  const trace = randomUUID(), modules = []; await page.setExtraHTTPHeaders(headers(trace));
  page.on('response', response => { if (new URL(response.url()).pathname.endsWith('.wasm') && !new URL(response.url()).pathname.endsWith('/app.wasm')) modules.push(response); });
  await page.goto('/action-view?delay=750ms'); await ready(page); const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page));
  await expect(page.locator('[data-av-lazy-save]')).toBeEnabled();
  await expect.poll(() => modules.length).toBeGreaterThan(0);
  await page.evaluate(() => { window.avDocument = true; window.avLazy = document.querySelector('[data-nextgo-island="actionviewlazy"]'); });
  await post(page, page.locator('[data-av-lazy-save]')); await assertFresh(page, initial + 1, revision + 1); await assertReceipt(page, initial + 1, revision + 1); await assertStreamedCounter(page, initial + 1);
  expect(await page.evaluate(() => window.avDocument && !window.avLazy.isConnected && window.avLazy.getAttribute('data-av-lazy-closed') === 'true')).toBe(true);
  const actual = await records(request, trace); expect(actual.filter(record => record.method === 'GET')).toHaveLength(1); expect(actual.filter(record => record.method === 'POST')).toHaveLength(1);
});

test('a same-URL refreshed view preserves live scroll changed while its POST is pending', async ({ page, request }) => {
  const trace = randomUUID(), key = `${trace}-scroll`;
  await page.setExtraHTTPHeaders(headers(trace)); await page.goto('/action-view?delay=750ms'); await ready(page);
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page)), history = await page.evaluate(() => window.history.length);
  // The stylesheet is outside the replaced app region. It keeps a real scroll
  // range throughout the commit, including the mobile viewport configurations.
  await page.addStyleTag({ content: 'body { min-height: 4000px !important; }' });
  try {
    await page.setExtraHTTPHeaders(headers(trace, { 'X-Fixture-Action-View-Gate': key, 'X-Fixture-Action-View-Phase': 'result' }));
    const response = page.waitForResponse(value => endpoint(new URL(value.url()).pathname) && value.request().method() === 'POST');
    await jsonButton(page).dispatchEvent('click'); expect((await response).status()).toBe(200);
    await held(request, key, 'result');
    await page.evaluate(() => window.scrollTo(0, 650));
    expect(await page.evaluate(() => window.scrollY)).toBe(650);
    expect((await request.post(fixtureURL('release', key))).status()).toBe(200);
    await assertFresh(page, initial + 1, revision + 1); await assertReceipt(page, initial + 1, revision + 1);
    expect(await page.evaluate(() => window.scrollY)).toBe(650);
    expect(await page.evaluate(() => window.history.length)).toBe(history);
    const actual = await records(request, trace); expect(actual.filter(record => record.method === 'GET')).toHaveLength(1); expect(actual.filter(record => record.method === 'POST')).toHaveLength(1);
  } finally { await request.post(fixtureURL('release', key)); }
});

test('a real native transition preserves live scroll changed after its action shell was parsed', async ({ page, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(headers(trace, { 'X-Fixture-Action-View-Transitions': 'true' }));
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('/action-view?delay=750ms'); await ready(page);
  test.skip(!(await page.evaluate(() => typeof document.startViewTransition === 'function')), 'Native View Transition API unavailable in this browser.');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-transitions', 'true');
  const initial = await number(ssrCount(page)), revision = await number(ssrRevision(page)), history = await page.evaluate(() => window.history.length);
  await page.addStyleTag({ content: 'body { min-height: 4000px !important; }' });
  await page.evaluate(() => {
    const native = document.startViewTransition;
    document.startViewTransition = function (update) {
      window.avNativeRequestedAtY = window.scrollY;
      const release = new Promise(resolve => { window.avReleaseNativeUpdate = resolve; });
      // This forwards the actual browser transition and all its real promises.
      // Only callback scheduling is held; no action/view bytes or DOM patch is
      // synthesized, and the original Go update remains the sole DOM writer.
      return Reflect.apply(native, this, [function (...args) {
        const receiver = this; window.avNativeUpdateHeld = true;
        return release.then(() => Reflect.apply(update, receiver, args));
      }]);
    };
  });
  try {
    const waiting = page.waitForResponse(value => endpoint(new URL(value.url()).pathname) && value.request().method() === 'POST');
    await jsonButton(page).dispatchEvent('click'); expect((await waiting).status()).toBe(200);
    await page.waitForFunction(() => window.avNativeUpdateHeld === true, null, { polling: 10 });
    // The real shell has reached commitNavigation and captured its history
    // argument, but the native callback has not invoked the Go DOM update yet.
    const observed = await page.evaluate(() => {
      const countBeforeCommit = document.querySelector('[data-action-view-ssr-count]').textContent;
      window.scrollTo(0, 650); const liveY = window.scrollY;
      window.avReleaseNativeUpdate(); return { countBeforeCommit, liveY, requestedY: window.avNativeRequestedAtY };
    });
    expect(observed).toEqual({ countBeforeCommit: String(initial), liveY: 650, requestedY: 0 });
    await assertFresh(page, initial + 1, revision + 1); await assertReceipt(page, initial + 1, revision + 1);
    expect(await page.evaluate(() => window.scrollY)).toBe(650);
    expect(await page.evaluate(() => window.history.length)).toBe(history);
    const actual = await records(request, trace); expect(actual.filter(record => record.method === 'GET')).toHaveLength(1); expect(actual.filter(record => record.method === 'POST')).toHaveLength(1);
  } finally { await page.evaluate(() => window.avReleaseNativeUpdate?.()); }
});

test.describe('native form without JavaScript', () => {
  test.use({ javaScriptEnabled: false });
  test('native HTML confirmation works and the next direct request reads the committed value', async ({ page, request }) => {
    const trace = randomUUID(); await page.setExtraHTTPHeaders(headers(trace)); await page.goto('/action-view'); const initial = await number(ssrCount(page));
    await amount(page).fill('2'); const response = await post(page, page.getByRole('button', { name: 'Submit native action view form', exact: true }));
    expect(response.headers()['content-type']).toContain('text/html'); await expect(page.getByRole('heading', { name: 'Action completed', exact: true })).toBeVisible();
    await page.goto('/action-view'); await expect(ssrCount(page)).toHaveText(String(initial + 2)); await expect(jsonButton(page)).toBeDisabled();
    expect((await records(request, trace)).filter(record => record.method === 'POST')).toHaveLength(1);
  });
});
