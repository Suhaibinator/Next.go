import { test, expect } from '@playwright/test';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const location = page => page.locator('[data-route-location]');
const nav = (page, name) => page.getByRole('navigation', { name: 'Main', exact: true }).getByRole('link', { name, exact: true });
const watch = page => page.evaluate(() => {
  window.locationDocument = true;
  window.locationOutput = document.querySelector('[data-route-location]');
  window.locationChanges = [];
  new MutationObserver(() => window.locationChanges.push(window.locationOutput.textContent))
    .observe(window.locationOutput, { childList: true, characterData: true, subtree: true });
});

test('the current route renders before WASM and hydration preserves its text node', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('/posts?tag=one&tag=two', { waitUntil: 'domcontentloaded' });
  await expect(location(page)).toHaveText('/posts?tag=one&tag=two');
  await page.evaluate(() => { window.locationText = document.querySelector('[data-route-location]').firstChild; });
  release(); await ready(page);
  expect(await page.evaluate(() => window.locationText === document.querySelector('[data-route-location]').firstChild)).toBe(true);
});

test('a retained Go subscription follows paths, queries, dynamic routes and history', async ({ page }) => {
  await page.goto('/'); await ready(page); await watch(page);
  await page.locator('[data-layout-increment]').click();
  await nav(page, 'Field notes').evaluate(link => { link.setAttribute('href', '/posts?tag=one&tag=two&q=Go%20WASM'); link.setAttribute('data-nextgo-prefetch', 'false'); });
  await nav(page, 'Field notes').click(); await expect(location(page)).toHaveText('/posts?tag=one&tag=two&q=Go%20WASM');
  await page.getByRole('link', { name: /Start with the HTML/ }).click(); await expect(location(page)).toHaveText('/posts/html-first');
  await page.goBack(); await expect(location(page)).toHaveText('/posts?tag=one&tag=two&q=Go%20WASM');
  await page.goForward(); await expect(location(page)).toHaveText('/posts/html-first');
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  expect(await page.evaluate(() => [window.locationDocument, window.locationOutput === document.querySelector('[data-route-location]')])).toEqual([true, true]);
});

test('native fragment changes, replacement and traversal publish without fetching pages', async ({ page }) => {
  await page.goto('/gallery'); await ready(page);
  // Keep this fragment/history test independent of speculative photo-link intent.
  // Every X-Nextgo-Navigate request is still counted and must remain zero.
  await page.getByRole('navigation', { name: 'Gallery photos', exact: true })
    .getByRole('link').evaluateAll(links => {
      links.forEach(link => link.setAttribute('data-nextgo-prefetch', 'false'));
    });
  await watch(page);
  let requests = 0;
  page.on('request', request => { if (request.headers()['x-nextgo-navigate'] === '1') requests++; });
  const link = page.getByRole('link', { name: 'Open coastal photo', exact: true });
  await link.evaluate(node => node.setAttribute('href', '#gallery-photos'));
  await link.click(); await expect(location(page)).toHaveText('/gallery#gallery-photos');
  const replace = page.getByRole('link', { name: 'Replace with coastal photo', exact: true });
  const length = await page.evaluate(() => history.length);
  await replace.evaluate(node => node.setAttribute('href', '#main'));
  await replace.click(); await expect(location(page)).toHaveText('/gallery#main');
  expect(await page.evaluate(() => history.length)).toBe(length);
  await page.goBack(); await expect(location(page)).toHaveText('/gallery');
  await page.goForward(); await expect(location(page)).toHaveText('/gallery#main');
  await page.evaluate(() => { window.location.hash = 'gallery-photos'; });
  await expect(location(page)).toHaveText('/gallery#gallery-photos');
  expect(requests).toBe(0);
  expect(await page.evaluate(() => window.locationChanges)).toEqual(['/gallery#gallery-photos', '/gallery#main', '/gallery', '/gallery#main', '/gallery#gallery-photos']);
});

test('loading and failed navigation do not publish a speculative route', async ({ page }) => {
  await page.goto('/'); await ready(page); await watch(page);
  let release, started;
  const gate = new Promise(resolve => { release = resolve; });
  const fetching = new Promise(resolve => { started = resolve; });
  await page.route('**/posts?pending=1', async route => { started(); await gate; await route.abort('failed'); });
  await nav(page, 'Field notes').evaluate(link => { link.setAttribute('href', '/posts?pending=1'); link.setAttribute('data-nextgo-prefetch', 'false'); });
  await nav(page, 'Field notes').click(); await fetching;
  await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
  await expect(location(page)).toHaveText('/'); expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  release(); await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(location(page)).toHaveText('/'); expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  await page.unroute('**/posts?pending=1'); await nav(page, 'Field notes').click();
  await expect(location(page)).toHaveText('/posts?pending=1');
});

test('a superseded response cannot notify retained route observers', async ({ page }) => {
  await page.goto('/'); await ready(page); await watch(page);
  let release, started, finished;
  const gate = new Promise(resolve => { release = resolve; });
  const fetching = new Promise(resolve => { started = resolve; });
  const settled = new Promise(resolve => { finished = resolve; });
  await page.route('**/posts?race=1', async route => {
    const response = await route.fetch(); started(); await gate;
    try { await route.fulfill({ response }); } finally { finished(); }
  });
  await nav(page, 'Field notes').evaluate(link => { link.setAttribute('href', '/posts?race=1'); link.setAttribute('data-nextgo-prefetch', 'false'); });
  await nav(page, 'Field notes').click(); await fetching;
  await nav(page, 'Architecture').click(); await expect(location(page)).toHaveText('/about');
  release(); await settled;
  await expect(page).toHaveURL(/\/about$/); expect(await page.evaluate(() => window.locationChanges)).toEqual(['/about']);
});

// This witness records only fixed stage/HTTP flags and owned native request
// events. It observes the existing waits; it never changes their requirements.
const locationRefreshWitnesses = new WeakMap();
function observeLocationRefresh(page) {
  const candidates = new Map(), candidateLimit = 4;
  const stages = new Set(['clicking-refresh', 'waiting-response', 'waiting-body', 'waiting-status',
    'checking-refresh-location', 'reading-history', 'clicking-replacement', 'waiting-location', 'checking-history', 'complete']);
  const state = {stage: 'clicking-refresh', responseSelected: false, bodyFinishedResolved: false,
    metadataAvailable: false, response: null, candidateOverflow: false, observerError: false};
  let selectedRequest, selectedNetwork, closed = false, saved;
  const listeners = [];
  const newNetwork = () => ({requestObserved: false, requestFinishedObserved: false, requestFailedObserved: false});
  const observe = (kind, request) => {
    if (closed) return;
    try {
      if (!request.url().endsWith('/features') || request.headers()['x-nextgo-navigate'] !== '1') return;
      let row = request === selectedRequest && selectedNetwork ? selectedNetwork : candidates.get(request);
      if (!row && candidates.size < candidateLimit) {row = newNetwork(); candidates.set(request, row);}
      if (!row) {state.candidateOverflow = true; if (request === selectedRequest) row = selectedNetwork;}
      if (row) row[kind] = true;
    } catch {state.observerError = true;}
  };
  for (const [event, kind] of [['request', 'requestObserved'], ['requestfinished', 'requestFinishedObserved'], ['requestfailed', 'requestFailedObserved']]) {
    const listener = request => observe(kind, request);
    page.on(event, listener); listeners.push([event, listener]);
  }
  const snapshot = () => ({version: 1, ...state,
    selectedNetwork: selectedNetwork ? {...selectedNetwork} : null,
    candidateRequestsRetained: candidates.size, candidateLimit,
    observationMeaning: 'False means not observed while this witness owned its listeners; unknown coverage is explicit. No native request completion or Go UI cause is inferred.'});
  const owner = {
    stage(value) {if (!closed && stages.has(value)) state.stage = value;},
    bodyFinished() {if (!closed) state.bodyFinishedResolved = true;},
    select(response) {
      if (closed) return;
      state.responseSelected = true;
      try {
        selectedRequest = response.request();
        // A bounded overflow cannot turn an unwitnessed earlier terminal event
        // into false. Later events for this exact selected object still record.
        selectedNetwork = candidates.get(selectedRequest) ||
          {requestObserved: null, requestFinishedObserved: null, requestFailedObserved: null};
        const headers = response.headers(), requestHeaders = selectedRequest.headers();
        const rawType = headers['content-type'];
        const type = typeof rawType === 'string' && rawType.length <= 256 ? rawType.split(';', 1)[0].trim().toLowerCase() : '';
        const status = response.status();
        state.response = {status: Number.isInteger(status) && status >= 0 && status <= 599 ? status : null,
          contentType: ['application/json', 'application/x-nextgo-stream+json', 'text/html'].includes(type) ? type : 'unrecognized',
          method: selectedRequest.method() === 'GET' ? 'GET' : 'other',
          navigationFlags: {protocolPresent: Object.hasOwn(headers, 'x-nextgo-protocol'), protocolV3: headers['x-nextgo-protocol'] === '3',
            streamPresent: Object.hasOwn(headers, 'x-nextgo-stream'), streamV1: headers['x-nextgo-stream'] === '1',
            metadataPresent: Object.hasOwn(headers, 'x-nextgo-metadata'), metadataV1: headers['x-nextgo-metadata'] === '1',
            configurationPresent: Object.hasOwn(headers, 'x-nextgo-configuration'), draftPresent: Object.hasOwn(headers, 'x-nextgo-draft'),
            requestStreamV1: requestHeaders['x-nextgo-stream'] === '1', requestPrefetch: requestHeaders['x-nextgo-purpose'] === 'prefetch'}};
        state.metadataAvailable = true;
      } catch {state.observerError = true;}
    },
    cleanup() {
      if (!closed) {saved = snapshot(); closed = true;}
      let removed = 0;
      for (const [event, listener] of listeners) {
        try {page.off(event, listener); removed++;} catch {} // Never replace the primary test failure.
      }
      saved.cleanup = {listenersExpected: listeners.length, listenersRemoved: removed, complete: removed === listeners.length};
      candidates.clear(); selectedRequest = undefined; selectedNetwork = undefined;
    },
    report() {return saved || snapshot();},
  };
  locationRefreshWitnesses.set(page, owner);
  return owner;
}

// A timed-out test may still be inside the original Response.finished promise.
// afterEach snapshots and releases this test's listeners before fixture close;
// the test's own finally supplies the same cleanup on ordinary completion.
test.afterEach(async ({page}, info) => {
  const owner = locationRefreshWitnesses.get(page);
  if (!owner) return;
  try {
    owner.cleanup();
    if (info.status !== info.expectedStatus) {
      const body = JSON.stringify(owner.report());
      if (Buffer.byteLength(body, 'utf8') <= 4096)
        await info.attach('location-refresh-wait-state', {body, contentType: 'application/json'});
    }
  } catch {} // Failure-only evidence must not replace the original assertion/timeout.
  finally {owner.cleanup(); locationRefreshWitnesses.delete(page);}
});

test('refresh leaves the location subscription unchanged and replacement publishes the committed URL', async ({ page }) => {
  await page.goto('/features'); await ready(page); await watch(page);
  const witness = observeLocationRefresh(page);
  try {
    const response = page.waitForResponse(r => r.url().endsWith('/features') && r.request().headers()['x-nextgo-navigate'] === '1');
    await page.getByRole('button', { name: 'Refresh server data', exact: true }).click();
    witness.stage('waiting-response');
    const selected = await response;
    witness.select(selected); witness.stage('waiting-body');
    await selected.finished(); witness.bodyFinished();
    witness.stage('waiting-status');
    await expect(page.locator('#nextgo-status')).toBeHidden();
    witness.stage('checking-refresh-location');
    expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
    witness.stage('reading-history');
    const length = await page.evaluate(() => history.length);
    witness.stage('clicking-replacement');
    await page.getByRole('button', { name: 'Replace with first field note', exact: true }).click();
    witness.stage('waiting-location');
    await expect(location(page)).toHaveText('/posts/html-first');
    witness.stage('checking-history');
    expect(await page.evaluate(() => history.length)).toBe(length);
    witness.stage('complete');
  } finally {witness.cleanup();}
});

test('denial disposes the previous layout subscription and returning mounts a fresh one', async ({ page }) => {
  await page.goto('/gallery'); await ready(page); await watch(page);
  await page.setExtraHTTPHeaders({ 'X-Demo-Photo-Deny': 'yes' });
  await page.getByRole('link', { name: 'Open coastal photo', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Access denied');
  await expect(location(page)).toHaveCount(0);
  expect(await page.evaluate(() => [window.locationOutput.isConnected, window.locationOutput.textContent, window.locationChanges])).toEqual([false, '/gallery', []]);
  await page.setExtraHTTPHeaders({}); await page.goBack(); await expect(location(page)).toHaveText('/gallery');
  await nav(page, 'Architecture').click(); await expect(location(page)).toHaveText('/about');
  expect(await page.evaluate(() => window.locationOutput.textContent)).toBe('/gallery');
});

test('custom 404 content still publishes the committed missing URL', async ({ page }) => {
  await page.goto('/'); await ready(page); await watch(page);
  await nav(page, 'Architecture').evaluate(link => { link.setAttribute('href', '/missing-location?reason=absent'); link.setAttribute('data-nextgo-prefetch', 'false'); });
  await nav(page, 'Architecture').click(); await expect(page.getByRole('heading', { level: 1 })).toHaveText('Nothing here. Yet.');
  await expect(location(page)).toHaveText('/missing-location?reason=absent');
  expect(await page.evaluate(() => window.locationDocument)).toBe(true);
});

test('intercepted modal addresses update the retained layout and dismissal restores its source', async ({ page }) => {
  await page.goto('/gallery?q=location#gallery-photos'); await ready(page); await watch(page);
  await page.getByRole('link', { name: 'Open coastal photo', exact: true }).click();
  await expect(page.getByRole('dialog')).toBeVisible(); await expect(location(page)).toHaveText('/photos/one');
  expect(await page.evaluate(() => window.locationOutput === document.querySelector('[data-route-location]'))).toBe(true);
  await page.getByRole('button', { name: 'Close photo', exact: true }).click();
  await expect(location(page)).toHaveText('/gallery?q=location#gallery-photos');
  expect(await page.evaluate(() => window.locationChanges)).toEqual(['/photos/one', '/gallery?q=location#gallery-photos']);
});

test('pending and failed history traversal keep the previously committed snapshot', async ({ page }) => {
  await page.goto('/'); await ready(page); await nav(page, 'Field notes').click();
  await expect(location(page)).toHaveText('/posts'); await watch(page);
  let release, started;
  const gate = new Promise(resolve => { release = resolve; });
  const fetching = new Promise(resolve => { started = resolve; });
  await page.route('http://127.0.0.1:4100/', async route => {
    if (route.request().headers()['x-nextgo-navigate'] !== '1') return route.continue();
    started(); await gate; await route.abort('failed');
  });
  await page.goBack(); await fetching;
  await expect(page).toHaveURL('http://127.0.0.1:4100/'); await expect(location(page)).toHaveText('/posts');
  expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  release(); await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(page).toHaveURL(/\/posts$/); await expect(location(page)).toHaveText('/posts');
  expect(await page.evaluate(() => window.locationChanges)).toEqual([]);
  await page.unroute('http://127.0.0.1:4100/'); await nav(page, 'Overview').click(); await expect(location(page)).toHaveText('/');
});
