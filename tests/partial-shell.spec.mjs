import { test, expect } from '@playwright/test';

const origin = 'http://127.0.0.1:4163';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const loaded = page => expect(page.locator('[data-partial-region]')).toBeVisible();
const count = page => page.getByRole('region', { name: 'Partial shell counter', exact: true }).locator('[data-count]');
const increment = page => page.getByRole('button', { name: 'Increase Partial shell counter', exact: true });
const target = '/partial/alpha?message=Current%20private%20value';
const isActive = (request, path = target) => request.url() === origin + path && request.headers()['x-nextgo-navigate'] === '1' && request.headers()['x-nextgo-purpose'] !== 'prefetch';
const isPrefetch = (request, path = target) => request.url() === origin + path && request.headers()['x-nextgo-purpose'] === 'prefetch';
const id = page => page.locator('[data-partial-shell-generation]');
const traceKeys = new WeakMap();
async function select(page, headers = {}) { await page.setExtraHTTPHeaders({ ...headers, 'X-Fixture-Shell-Trace': traceKeys.get(page) }); }
async function open(page, path = '/partial') { await page.goto(origin + path); await ready(page); if (path.startsWith('/partial')) await loaded(page); }
async function link(page, path = target, automatic = true) {
  await page.evaluate(({ path, automatic }) => { document.getElementById('shell-target')?.remove(); const a = document.createElement('a'); a.id = 'shell-target'; a.href = path; a.textContent = 'Open cached public shell'; if (!automatic) a.dataset.nextgoPrefetch = 'false'; document.querySelector('main').prepend(a); }, { path, automatic });
  return page.locator('#shell-target');
}
async function publicPrefetchWire(page, path) {
  const wireURL = `${origin}/_fixture/shell-wire?kind=prefetch&trace=${encodeURIComponent(traceKeys.get(page))}&url=${encodeURIComponent(path)}`;
  await expect.poll(async () => (await page.request.get(wireURL)).status()).toBe(200);
  return (await page.request.get(wireURL)).json();
}
async function prefetched(page, path = target) {
  const a = await link(page, path); const waiting = page.waitForResponse(response => isPrefetch(response.request(), path));
  await a.dispatchEvent('pointerover'); const response = await waiting;
  expect(response.status()).toBe(200); expect(response.request().headers()['x-nextgo-shell-protocol']).toBe('1');
  expect(response.headers()['x-nextgo-shell-protocol']).toBe('1'); expect(response.headers()['x-nextgo-prefetch']).toBe('shell');
  expect(Number(response.headers()['x-nextgo-shell-stale'])).toBeGreaterThan(0); expect(Number(response.headers()['x-nextgo-shell-stale'])).toBeLessThanOrEqual(30_000);
  expect(response.headers()['cache-control']).toContain('private'); expect(response.headers()['cache-control']).toContain('no-store');
  // Chromium can report ERR_ABORTED without resolving Playwright's finished()
  // promise. Check the bounded genuine wire envelope; each active use below
  // separately proves browser admission through its shell hint/reference.
  const captured = await publicPrefetchWire(page, path); expect(captured.status).toBe(response.status());
  const data = captured.data; expect(data.shell).toMatch(/^[a-f0-9]{64}$/); expect(data.shellRef || false).toBe(false);
  expect(data.html).toContain('data-partial-loading'); expect(data.html).not.toContain('data-partial-request-id');
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => setTimeout(resolve, 0))));
  return { response, data, a };
}
async function frames(response) { return (await response.text()).trim().split('\n').filter(Boolean).map(line => JSON.parse(line)); }
async function shellFrame(response) {
  expect(response.status()).toBe(200);
  const request = response.request(); const url = new URL(response.url());
  const wire = await request.frame().page().request.get(`${origin}/_fixture/shell-wire?trace=${encodeURIComponent(request.headers()['x-fixture-shell-trace'])}&url=${encodeURIComponent(url.pathname + url.search)}`);
  expect(wire.status()).toBe(200); const captured = await wire.json(); expect(captured.status).toBe(200); expect(captured.frame.type).toBe('shell'); return captured.frame.page;
}
async function control(request, operation, replica = 'first') { const response = await request.post(`${origin}/_fixture/${operation}?replica=${replica}`); expect(response.status(), await response.text()).toBe(200); }
async function unchanged(page, title) {
  await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy'); await expect(page).toHaveTitle(title);
  expect(await page.evaluate(() => window.shellOldRoot === document.getElementById('nextgo-app') && window.shellOldCounter === document.querySelector('[data-partial-shell] [data-nextgo-island]') && history.length === window.shellOldHistory)).toBe(true);
  await expect(count(page)).toHaveText('4');
}
async function remember(page) { await increment(page).click(); await page.evaluate(() => { window.shellOldRoot = document.getElementById('nextgo-app'); window.shellOldCounter = document.querySelector('[data-partial-shell] [data-nextgo-island]'); window.shellOldHistory = history.length; }); return page.title(); }
async function head(page, generation) { await expect(page).toHaveTitle(new RegExp(generation)); await expect(page.locator('meta[name="partial-shell-generation"]')).toHaveAttribute('content', generation); expect(JSON.parse(await page.locator('head script[type="application/ld+json"][data-nextgo-head]').textContent()).identifier).toBe(generation); }
test.beforeEach(async ({ page }, info) => { traceKeys.set(page, `${info.project.name}-${info.workerIndex}-${Date.now()}-${Math.random().toString(36).slice(2)}`); await select(page); });
test.afterEach(async ({ request }) => { await control(request, 'release-stream'); });

test('public prefetch skips broken regions and a matching resume still waits for current private network data', async ({ page, context, request }) => {
  await context.addCookies([{ name: 'nextgo_partial_name', value: 'Alice', url: origin }]); await open(page); const title = await remember(page);
  await select(page, { 'X-Fixture-Partial': 'broken' }); const cached = await prefetched(page);
  expect(cached.data.html).not.toContain('Alice'); expect(cached.data.html).not.toContain('Current private value');
  const old = await request.get(origin + target, { headers: { 'X-Fixture-Partial': 'broken', 'X-Nextgo-Navigate': '1', 'X-Nextgo-Purpose': 'prefetch' } }); expect(old.status()).toBe(204);
  const legacy = await request.get(origin + target, { headers: { 'X-Nextgo-Navigate': '1' } }); const legacyData = await legacy.json(); expect(Object.hasOwn(legacyData, 'shell')).toBe(false); expect(Object.hasOwn(legacyData, 'shellRef')).toBe(false);
  await context.addCookies([{ name: 'nextgo_partial_name', value: 'Bob', url: origin }]); await select(page, { 'X-Fixture-Partial': 'second' });
  let release; const gate = new Promise(resolve => { release = resolve; });
  await page.route(origin + target, async route => { if (isActive(route.request())) await gate; await route.continue(); });
  try {
    const requested = page.waitForRequest(request => isActive(request)); const waiting = page.waitForResponse(response => isActive(response.request()));
    await cached.a.click(); const current = await requested; expect(current.headers()['x-nextgo-shell']).toBe(cached.data.shell);
    await expect(page).toHaveURL(origin + '/partial'); await expect(count(page)).toHaveText('4'); expect(await page.title()).toBe(title);
    release(); const response = await waiting; const frame = await shellFrame(response); expect(frame.shell).toBe(cached.data.shell); expect(frame.shellRef).toBe(true); expect(frame.html).toBe('');
    await loaded(page); await ready(page); await expect(page.locator('[data-partial-visitor]')).toHaveText('Bob'); await expect(page.locator('[data-partial-message]')).toHaveText('Current private value'); await head(page, cached.data.meta['partial-shell-generation']);
  } finally { release(); }
});

test('acknowledged real tag invalidation replaces stale prefetched HTML and metadata with a new full shell', async ({ page, request }) => {
  await select(page, { 'X-Fixture-Partial': 'live' }); await open(page); const cached = await prefetched(page);
  expect((await request.post(origin + '/partial/control')).status()).toBe(401); expect((await request.get(origin + '/partial/control')).status()).toBe(405);
  await control(request, 'expire', 'live'); const waiting = page.waitForResponse(response => isActive(response.request())); await cached.a.click(); const response = await waiting;
  expect(response.request().headers()['x-nextgo-shell']).toBe(cached.data.shell); const frame = await shellFrame(response); expect(frame.shellRef || false).toBe(false); expect(frame.shell).not.toBe(cached.data.shell); expect(frame.html).toContain('data-partial-shell');
  await loaded(page); await ready(page); const generation = await id(page).textContent(); expect(generation).not.toBe(cached.data.meta['partial-shell-generation']); await head(page, generation);
});

for (const corruption of ['hash', 'metadata']) test(`a real resumed shell with corrupt ${corruption} cannot commit cached HTML or head and requires an explicit retry`, async ({ page }) => {
  await open(page); const title = await remember(page); const cached = await prefetched(page); let changed = false, requests = 0;
  page.on('request', request => { if (isActive(request)) requests++; });
  await page.route(origin + target, async route => {
    if (!isActive(route.request()) || changed) { await route.continue(); return; }
    const response = await route.fetch(); const body = await frames(response); const frame = body.find(frame => frame.type === 'shell').page; expect(frame.shellRef).toBe(true);
    if (corruption === 'hash') frame.shell = frame.shell === 'a'.repeat(64) ? 'b'.repeat(64) : 'a'.repeat(64); else frame.title = 'Unbound cached shell title';
    changed = true; await route.fulfill({ response, body: body.map(frame => JSON.stringify(frame)).join('\n') + '\n' });
  });
  await cached.a.click(); await expect(page.locator('#nextgo-status')).toContainText('Navigation failed'); await unchanged(page, title); expect(requests).toBe(1); await expect(page).toHaveURL(origin + '/partial');
  const waiting = page.waitForResponse(response => isActive(response.request())); await cached.a.click(); const response = await waiting; expect(response.request().headers()['x-nextgo-shell']).toBeUndefined(); const frame = await shellFrame(response); expect(frame.shellRef || false).toBe(false); await loaded(page); await ready(page); expect(requests).toBe(2);
});

test('revoking a real request grant after prefetch cannot use the public shell as an authorization shortcut', async ({ page, context }) => {
  await context.addCookies([{ name: 'nextgo_partial_grant', value: 'allowed', url: origin }]); await open(page); const path = '/partial/alpha?protected=1'; const cached = await prefetched(page, path);
  await page.evaluate(() => { window.shellAuthDocument = true; }); await context.clearCookies({ name: 'nextgo_partial_grant' });
  const decision = page.waitForResponse(response => isActive(response.request(), path)); const document = page.waitForResponse(response => response.request().isNavigationRequest() && response.url() === origin + path);
  await cached.a.click(); const denied = await decision; expect(denied.request().headers()['x-nextgo-shell']).toBe(cached.data.shell); expect(denied.status()).toBe(403); expect((await document).status()).toBe(403); await expect(page).toHaveURL(origin + path);
  expect(await page.evaluate(() => window.shellAuthDocument)).toBeUndefined(); await expect(page.locator('[data-partial-shell]')).toHaveCount(0); await expect(page.locator('meta[name="partial-shell-generation"]')).toHaveCount(0);
});

test('build skew on a real shell reference follows document recovery before cached metadata adoption', async ({ page }) => {
  await open(page); const cached = await prefetched(page); await page.evaluate(() => { window.shellSkewDocument = true; }); let changed = false;
  await page.route(origin + target, async route => {
    if (changed || !isActive(route.request())) { await route.continue(); return; }
    const response = await route.fetch(); const body = await frames(response); const frame = body.find(frame => frame.type === 'shell').page; expect(frame.shellRef).toBe(true); frame.build = 'fixture-incompatible-shell-build'; changed = true;
    await route.fulfill({ response, body: body.map(frame => JSON.stringify(frame)).join('\n') + '\n' });
  });
  const document = page.waitForResponse(response => response.request().isNavigationRequest() && response.url() === origin + target); await cached.a.click(); expect((await document).status()).toBe(200); await loaded(page); await ready(page); expect(await page.evaluate(() => window.shellSkewDocument)).toBeUndefined();
});

test('superseded resumes, query strings and history always reload private regions and dispose detached controls', async ({ page, context }) => {
  await open(page); const slow = '/partial/alpha?message=Slow%20private&delay=2s'; const cached = await prefetched(page, slow);
  await select(page, { 'X-Fixture-Partial-Gate': `resume-${test.info().project.name}-${Date.now()}` });
  const first = page.waitForResponse(response => isActive(response.request(), slow)); await cached.a.click(); expect((await first).request().headers()['x-nextgo-shell']).toBe(cached.data.shell); await expect(page).toHaveURL(origin + slow); await expect(page.locator('[data-partial-loading]')).toBeVisible(); await increment(page).click();
  await page.evaluate(() => { window.detachedShell = document.querySelector('[data-partial-shell] [data-nextgo-island]'); window.detachedShellButton = window.detachedShell.querySelector('[data-increment]'); });
  await select(page);
  await page.getByRole('link', { name: 'Partial beta', exact: true }).click(); await loaded(page); await expect(page.locator('[data-partial-region-slug]')).toHaveText('beta');
  expect(await page.evaluate(() => { window.detachedShellButton.click(); return !window.detachedShell.isConnected && window.detachedShell.querySelector('[data-count]').textContent === '4'; })).toBe(true);
  await context.addCookies([{ name: 'nextgo_partial_name', value: 'Back visitor', url: origin }]); const back = page.waitForRequest(request => isActive(request, slow)); await page.goBack(); await back; await expect(page.locator('[data-partial-visitor]')).toHaveText('Back visitor'); await expect(page.locator('[data-partial-message]')).toHaveText('Slow private');
  await page.goForward(); await expect(page.locator('[data-partial-region-slug]')).toHaveText('beta'); await expect(page.locator('[data-partial-visitor]')).toHaveText('Back visitor');
});

test('Go Refresh fences a late public prefetch instead of repopulating the next navigation hint', async ({ page }) => {
  await open(page, '/features'); const a = await link(page); const gate = `prefetch-${traceKeys.get(page)}`;
  await page.evaluate(path => {
    window.partialPrefetchRead = { bytes: 0, done: false }; const fetch = window.fetch;
    window.fetch = function (url, options) {
      return fetch.apply(this, arguments).then(response => {
        if (!String(url).endsWith(path) || new Headers(options?.headers).get('x-nextgo-purpose') !== 'prefetch') return response;
        const getReader = response.body.getReader;
        response.body.getReader = function (...args) { const reader = getReader.apply(this, args); const read = reader.read;
          reader.read = function (...args) { return read.apply(this, args).then(chunk => { window.partialPrefetchRead.bytes += chunk.value?.byteLength || 0; window.partialPrefetchRead.done = chunk.done; return chunk; }); }; return reader; };
        return response;
      });
    };
  }, target);
  await select(page, { 'X-Fixture-Partial-Gate': gate });
  const pending = page.waitForResponse(response => isPrefetch(response.request())); await a.dispatchEvent('pointerover'); expect((await pending).status()).toBe(200);
  const captured = await publicPrefetchWire(page, target); expect(captured.status).toBe(200); expect(captured.data.html).toContain('data-partial-loading');
  await expect.poll(() => page.evaluate(() => window.partialPrefetchRead)).toEqual({ bytes: 1, done: false });
  await select(page); const aborted = page.waitForEvent('requestfailed', request => isPrefetch(request));
  const refreshed = page.waitForResponse(response => new URL(response.url()).pathname === '/features' && response.request().headers()['x-nextgo-navigate'] === '1'); await page.locator('[data-refresh]').click(); await refreshed; await aborted; await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy');
  const released = await page.request.post(`${origin}/_fixture/release-region?key=${encodeURIComponent(gate)}`); expect([200, 404]).toContain(released.status());
  const replacement = await link(page, target, false);
  const waiting = page.waitForResponse(response => isActive(response.request())); await replacement.click(); const response = await waiting; expect(response.request().headers()['x-nextgo-shell']).toBeUndefined(); expect((await shellFrame(response)).shellRef || false).toBe(false); await loaded(page); await ready(page);
});

test('the bounded shell lifetime expires independently of private HTTP no-store', async ({ page }) => {
  await page.clock.install(); await open(page); const cached = await prefetched(page);
  const admitted = page.waitForResponse(response => isActive(response.request())); await cached.a.click(); const resumed = await admitted;
  expect(resumed.request().headers()['x-nextgo-shell']).toBe(cached.data.shell); expect((await shellFrame(resumed)).shellRef).toBe(true); await expect(page.locator('[data-partial-region-slug]')).toHaveText('alpha'); await ready(page);
  await page.goBack(); await expect(page).toHaveURL(origin + '/partial'); await expect(page.locator('[data-partial-region-slug]')).toHaveText('root'); await ready(page);
  cached.a = await link(page, target, false); await page.clock.fastForward(31_000);
  await cached.a.evaluate(a => a.setAttribute('data-nextgo-prefetch', 'false')); const waiting = page.waitForResponse(response => isActive(response.request())); await cached.a.click(); const response = await waiting;
  expect(response.request().headers()['x-nextgo-shell']).toBeUndefined(); const frame = await shellFrame(response); expect(frame.shellRef || false).toBe(false); expect(frame.html).toContain('data-partial-shell'); await loaded(page); await ready(page);
});

test('a cheap public prefetch bypasses saturated stream capacity while its active click must respect real admission', async ({ page, request }) => {
  await select(page, { 'X-Fixture-Partial': 'busy' }); await open(page); const title = await remember(page); await control(request, 'hold-stream', 'busy');
  const cached = await prefetched(page); const failed = page.waitForResponse(response => isActive(response.request())); await cached.a.click(); const busy = await failed; expect(busy.request().headers()['x-nextgo-shell']).toBe(cached.data.shell); expect(busy.status()).toBe(503); expect(busy.headers()['retry-after']).toBe('1'); await expect(page.locator('#nextgo-status')).toContainText('Navigation failed'); await unchanged(page, title);
  await control(request, 'release-stream'); await expect.poll(async () => (await request.head(origin + '/stream', { headers: { 'X-Fixture-Partial': 'busy' } })).status()).toBe(200);
  const waiting = page.waitForResponse(response => isActive(response.request())); await cached.a.click(); expect((await waiting).status()).toBe(200); await loaded(page); await ready(page);
});
