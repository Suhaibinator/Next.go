import { test, expect } from '@playwright/test';
import { readerToken, adminToken } from './access-fixture.mjs';
import { createServer, request as proxyRequest } from 'node:http';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const notes = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Field notes', exact: true });
const architecture = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Architecture', exact: true });

test('shared-cache example has useful unconfigured SSR and ordinary Go navigation', async ({ page, request }) => {
  const response = await request.get('/shared-cache');
  expect(response.status()).toBe(200);
  expect(response.headers()['cache-control']).toContain('no-store');
  expect(await response.text()).toContain('data-shared-disabled');
  await page.goto('/cache'); await ready(page);
  await page.evaluate(() => { window.sharedCacheDocument = 'retained'; window.sharedCacheHeader = document.querySelector('header'); });
  await page.getByRole('link', { name: 'Try shared data across two app processes', exact: true }).click();
  await expect(page).toHaveURL(/\/shared-cache$/);
  await expect(page).toHaveTitle('Shared Go cache — Next.go');
  await expect(page.locator('[data-shared-disabled]')).toBeVisible();
  expect(await page.evaluate(() => window.sharedCacheDocument === 'retained' && window.sharedCacheHeader === document.querySelector('header'))).toBe(true);
  await page.goBack();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Server cache lifecycle');
});

test('meaningful SSR without JavaScript, direct dynamic routes, API, and assets', async ({ browser, request }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto('http://127.0.0.1:4100/');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
  await expect(page.locator('[data-count]')).toHaveText(['3', '10']);
  await notes(page).click();
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveURL(/\/posts\/html-first$/);
  await expect(page.locator('[data-posts-layout]')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Start with the HTML');
  await context.close();
  expect((await request.get('/api/health')).ok()).toBeTruthy();
  expect(await (await request.get('/api/health')).json()).toMatchObject({ ok: true });
  expect((await request.get('/assets/style.css')).headers()['content-type']).toContain('text/css');
  const missing = await request.get('/posts/no-such-post');
  expect(missing.status()).toBe(404);
  expect(await missing.text()).toContain('Nothing here. Yet.');
});

test('hydration preserves existing elements and text nodes; Go counters have independent state', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('/', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-count]')).toHaveText(['3', '10']);
  await page.evaluate(() => {
    window.savedRoot = document.querySelector('[data-nextgo-island="counter"]');
    window.savedButton = document.querySelector('[data-increment]');
    window.savedText = document.querySelector('[data-count]').firstChild;
    window.marker = 'same-document';
  });
  release();
  await ready(page);
  expect(await page.evaluate(() => window.savedRoot === document.querySelector('[data-nextgo-island="counter"]') && window.savedButton === document.querySelector('[data-increment]') && window.savedText === document.querySelector('[data-count]').firstChild)).toBe(true);
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  await page.getByRole('button', { name: 'Decrease Second counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '9']);
  await page.getByRole('region', { name: 'First counter', exact: true }).getByRole('button', { name: 'Reset' }).click();
  await expect(page.locator('[data-count]')).toHaveText(['3', '9']);
});

test('navigation, metadata, history, scroll, dynamic routes, and island disposal', async ({ page }) => {
  const errors = [];
  page.on('pageerror', err => errors.push(err.message));
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.marker = 'survives'; window.oldIsland = document.querySelector('[data-nextgo-island="counter"]'); window.oldButton = document.querySelector('[data-increment]'); });
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await page.evaluate(() => window.scrollTo(0, 500));
  const scrollY = await page.evaluate(() => window.scrollY);
  // DOM click avoids Playwright scrolling the top navigation into view first.
  await notes(page).evaluate(el => el.click());
  await expect(page).toHaveURL(/\/posts$/);
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(await page.evaluate(() => window.marker)).toBe('survives');
  await expect(page.locator('meta[name=description]')).toHaveAttribute('content', 'Notes on HTML, Go islands, and file-based routing.');
  expect(await page.locator('meta[data-nextgo-meta]').count()).toBe(0);
  // Detached controls must have no remaining Go callbacks.
  expect(await page.evaluate(() => { window.oldButton.click(); return window.oldIsland.querySelector('[data-count]').textContent; })).toBe('4');
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveURL(/\/posts\/html-first$/);
  await expect(page.locator('[data-posts-layout]')).toBeVisible();
  await page.getByRole('button', { name: 'Increase Helpful votes', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText('1');
  await page.goBack(); await expect(page).toHaveTitle('Field notes — Next.go');
  await page.goBack(); await expect(page).toHaveTitle('Next.go — Go, all the way through');
  await expect(page.locator('[data-count]')).toHaveText(['3', '10']);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(scrollY);
  await page.goForward(); await expect(page).toHaveTitle('Field notes — Next.go');
  expect(await page.evaluate(() => window.marker)).toBe('survives');
  expect(errors).toEqual([]);
});

test('loading, query strings, cancellation, recoverable network failures, and server errors', async ({ page }) => {
  await page.goto('/posts'); await ready(page);
  // Gate the first response: another engine can legitimately warm the shared
  // public server cache, so a loader's delay is not a deterministic UI clock.
  let releaseLoading;
  const loadingGate = new Promise(resolve => { releaseLoading = resolve; });
  await page.route('**/posts?delay=750ms', async route => { await loadingGate; await route.continue(); });
  await page.getByRole('link', { name: 'Try a slow navigation' }).click();
  await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
  await expect(page.locator('#nextgo-status')).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('A few field notes.');
  releaseLoading();
  await expect(page).toHaveURL(/delay=750ms/);
  await expect(page.locator('#nextgo-status')).toBeHidden();
  await page.unroute('**/posts?delay=750ms');
  // Supersede a deliberately slow request before it can commit.
  await page.getByRole('link', { name: 'Try a slow navigation' }).evaluate(el => { el.href = '/posts?delay=2s'; el.click(); });
  await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
  await expect(page.locator('#nextgo-status')).toBeVisible();
  await architecture(page).click();
  await expect(page).toHaveTitle('Architecture — Next.go');
  await page.waitForTimeout(2200);
  await expect(page).toHaveURL(/\/about$/);
  await page.route('**/posts', route => route.abort());
  await notes(page).click();
  await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(page).toHaveURL(/\/about$/);
  await expect(page.getByRole('heading', { level: 1 })).toContainText('A little framework.');
  await page.unroute('**/posts');
  await notes(page).click(); await expect(page).toHaveTitle('Field notes — Next.go');
  await page.getByRole('link', { name: 'Try a server error' }).click();
  await expect(page).toHaveTitle('Server error');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Unable to load field notes');
  await expect(page.locator('[data-posts-error]')).toBeVisible();
  await page.goBack(); await expect(page).toHaveTitle('Field notes — Next.go');
});

test('navigation renders custom 404 and modified/external/fragment links keep normal behavior', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.marker = 'same'; const link = document.createElement('a'); link.href = '/missing-page'; link.textContent = 'Missing page'; document.querySelector('main').prepend(link); });
  await page.getByRole('link', { name: 'Missing page' }).click();
  await expect(page).toHaveTitle('Page not found — Next.go');
  expect(await page.evaluate(() => window.marker)).toBe('same');
  await page.getByRole('link', { name: 'Back to the overview →' }).click();
  await expect(page).toHaveTitle('Next.go — Go, all the way through');
  await page.evaluate(() => { const a = document.createElement('a'); a.href = '#interactive'; a.textContent = 'Jump to counter'; document.querySelector('main').prepend(a); });
  await page.getByRole('link', { name: 'Jump to counter' }).click();
  await expect(page).toHaveURL(/#interactive$/);
  const intercepted = await page.evaluate(() => {
    const results = [];
    for (const setup of [a => { a.href = 'https://go.dev'; }, a => { a.href = '/posts'; a.target = '_blank'; }, a => { a.href = '/assets/style.css'; a.download = ''; }, a => { a.href = '/api/health'; }]) {
      const a = document.createElement('a'); setup(a); document.body.append(a);
      const e = new MouseEvent('click', { bubbles: true, cancelable: true });
      // Stop the browser's eventual default action after observing our document listener.
      const observe = ev => { results.push(ev.defaultPrevented); ev.preventDefault(); };
      window.addEventListener('click', observe, { once: true }); a.dispatchEvent(e); a.remove();
    }
    const e = new MouseEvent('click', { bubbles: true, cancelable: true, ctrlKey: true });
    const a = document.createElement('a'); a.href = '/posts'; document.body.append(a);
    window.addEventListener('click', ev => { results.push(ev.defaultPrevented); ev.preventDefault(); }, { once: true }); a.dispatchEvent(e); a.remove();
    return results;
  });
  expect(intercepted).toEqual([false, false, false, false, false]);
});

test('a build-version mismatch falls back to a complete document navigation', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.marker = 'old-runtime'; });
  await page.route('**/posts', async route => {
    if (route.request().headers()['x-nextgo-navigate'] === '1') {
      const response = await route.fetch();
      const envelope = await response.json();
      envelope.build = 'different-deployment';
      await route.fulfill({ response, json: envelope });
    } else {
      await route.continue();
    }
  });
  await notes(page).click();
  await expect(page).toHaveTitle('Field notes — Next.go');
  await ready(page);
  expect(await page.evaluate(() => window.marker)).toBeUndefined();
});

test('mount mismatch leaves the original markup intact and WASM load failure keeps SSR useful', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('/', { waitUntil: 'domcontentloaded' });
  await page.evaluate(() => { window.savedRoot = document.querySelector('[data-nextgo-island="counter"]'); window.savedRoot.querySelector('[data-count]').textContent = 'mismatch'; });
  release();
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'error');
  await expect(page.locator('#nextgo-status')).toContainText('does not match');
  expect(await page.evaluate(() => window.savedRoot === document.querySelector('[data-nextgo-island="counter"]'))).toBe(true);
  await page.unroute('**/app.wasm');
  await page.route('**/app.wasm', route => route.abort());
  await page.reload({ waitUntil: 'domcontentloaded' });
  await expect(page.locator('#nextgo-status')).toContainText('Interactive features could not start');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
  await notes(page).click();
  await expect(page).toHaveTitle('Field notes — Next.go');
});

test('same-document history cancels an in-flight navigation and restores fragment scroll', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => {
    window.marker = 'same-document';
    const link = document.createElement('a');
    link.href = '#interactive'; link.textContent = 'Jump to counter';
    document.querySelector('main').prepend(link);
  });
  await page.getByRole('link', { name: 'Jump to counter' }).click();
  await expect(page).toHaveURL(/#interactive$/);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBeGreaterThan(0);
  const fragmentScroll = await page.evaluate(() => window.scrollY);
  let release;
  let requested;
  const gate = new Promise(resolve => { release = resolve; });
  const started = new Promise(resolve => { requested = resolve; });
  await page.route('**/posts', async route => {
    const response = await route.fetch();
    requested();
    await gate;
    await route.fulfill({ response });
  });
  await notes(page).evaluate(el => el.click());
  await started;
  await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
  await expect(page.locator('#nextgo-status')).toBeVisible();
  const canceled = page.waitForEvent('requestfailed', { predicate: request => request.url().endsWith('/posts') });
  await page.evaluate(() => history.back());
  await canceled;
  await expect(page).toHaveURL('http://127.0.0.1:4100/');
  await expect(page.locator('#nextgo-status')).toBeHidden();
  await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy');
  release();
  await page.evaluate(() => history.forward());
  await expect(page).toHaveURL(/#interactive$/);
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(fragmentScroll);
  await expect(page).toHaveTitle('Next.go — Go, all the way through');
  expect(await page.evaluate(() => window.marker)).toBe('same-document');
});

test('malformed percent escapes in fragments do not break a successful navigation', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await notes(page).evaluate(el => { el.href = '/posts#%ZZ'; el.click(); });
  await expect(page).toHaveURL(/\/posts#%ZZ$/);
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('#nextgo-status')).toBeHidden();
  await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy');
  await architecture(page).click();
  await expect(page).toHaveTitle('Architecture — Next.go');
});

test('redirected page links commit the canonical URL and preserve query and fragment', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.marker = 'same-document'; });
  await notes(page).evaluate(el => { el.href = '/posts/?from=redirect#notes'; el.click(); });
  await expect(page).toHaveURL('http://127.0.0.1:4100/posts?from=redirect#notes');
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('#nextgo-status')).toBeHidden();
  expect(await page.evaluate(() => window.marker)).toBe('same-document');
  await page.goBack();
  await expect(page).toHaveURL('http://127.0.0.1:4100/');
  await expect(page).toHaveTitle('Next.go — Go, all the way through');
});

test('Go intent prefetch deduplicates pending requests and reuses explicitly public pages', async ({ page }) => {
  await page.goto('/'); await ready(page);
  let count = 0;
  let release;
  let started;
  const gate = new Promise(resolve => { release = resolve; });
  const requested = new Promise(resolve => { started = resolve; });
  await page.route('**/posts?prefetch=public', async route => {
    count++;
    const response = await route.fetch();
    started();
    await gate;
    await route.fulfill({ response, headers: { ...response.headers(), 'cache-control': 'public, max-age=30', 'x-nextgo-prefetch': 'public' } });
  });
  await notes(page).evaluate(el => {
    el.href = '/posts?prefetch=public';
    el.dispatchEvent(new PointerEvent('pointerover', { bubbles: true }));
    el.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
    el.dispatchEvent(new PointerEvent('pointerover', { bubbles: true }));
  });
  await requested;
  expect(count).toBe(1);
  await notes(page).evaluate(el => el.click());
  await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
  await expect(page.locator('#nextgo-status')).toBeVisible();
  release();
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(count).toBe(1);
  await page.goBack(); await ready(page);
  await expect(page).toHaveTitle('Next.go — Go, all the way through');
  await notes(page).evaluate(el => { el.href = '/posts?prefetch=public'; el.click(); });
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(count).toBe(1);
});

test('prefetch never reuses no-store, private, personalized, or non-opted-in responses', async ({ page }) => {
  for (const policy of [
    { 'cache-control': 'public, max-age=30' },
    { 'x-nextgo-prefetch': 'public', 'cache-control': 'no-store' },
    { 'x-nextgo-prefetch': 'public', 'cache-control': 'private, max-age=30' },
    { 'x-nextgo-prefetch': 'public', 'cache-control': 'public, max-age=30', vary: '*' },
  ]) {
    await page.goto('/'); await ready(page);
    let count = 0;
    await page.route('**/posts?prefetch=unsafe', async route => {
      count++;
      const response = await route.fetch();
      const headers = { ...response.headers() };
      delete headers['x-nextgo-prefetch'];
      await route.fulfill({ response, headers: { ...headers, ...policy } });
    });
    await notes(page).evaluate(el => { el.href = '/posts?prefetch=unsafe'; el.dispatchEvent(new FocusEvent('focusin', { bubbles: true })); });
    await expect.poll(() => count).toBe(1);
    await notes(page).evaluate(el => el.click());
    await expect(page).toHaveTitle('Field notes — Next.go');
    expect(count).toBe(2);
    await page.unroute('**/posts?prefetch=unsafe');
  }
});

test('intent prefetch respects opt-outs and normal non-page links', async ({ page }) => {
  await page.goto('/'); await ready(page);
  let count = 0;
  await page.route('**/*', async route => {
    if (route.request().headers()['x-nextgo-navigate'] === '1') count++;
    await route.continue();
  });
  await page.evaluate(() => {
    for (const setup of [
      a => { a.href = '/posts'; a.dataset.nextgoPrefetch = 'false'; },
      a => { a.href = '/posts'; a.dataset.nextgoReload = ''; },
      a => { a.href = '#interactive'; },
      a => { a.href = 'https://go.dev'; },
      a => { a.href = '/api/health'; },
      a => { a.href = '/assets/style.css'; },
      a => { a.href = '/posts'; a.target = '_blank'; },
      a => { a.href = '/posts'; a.download = ''; },
    ]) {
      const anchor = document.createElement('a'); setup(anchor); document.body.append(anchor);
      anchor.dispatchEvent(new PointerEvent('pointerover', { bubbles: true }));
      anchor.dispatchEvent(new FocusEvent('focusin', { bubbles: true }));
      anchor.remove();
    }
  });
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  expect(count).toBe(0);
});

test('Go streaming bridge hydrates appended chunks once and discards chunks after navigation', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => {
    const island = document.querySelector('[data-nextgo-island="counter"]');
    window.oldStreamIsland = island;
    window.oldStreamButton = island.querySelector('[data-increment]');
    const replacement = island.cloneNode(true);
    replacement.removeAttribute('data-nextgo-hydrated');
    const boundary = document.createElement('div');
    boundary.dataset.nextgoBoundary = 'test[stream]';
    island.before(boundary); boundary.append(island);
    const chunk = document.createElement('template');
    chunk.dataset.nextgoChunk = 'test[stream]';
    chunk.content.append(replacement);
    document.body.append(chunk);
  });
  await expect.poll(() => page.evaluate(() => window.oldStreamIsland.isConnected)).toBe(false);
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  expect(await page.evaluate(() => { window.oldStreamButton.click(); return window.oldStreamIsland.querySelector('[data-count]').textContent; })).toBe('3');
  await expect(page.locator('template[data-nextgo-chunk]')).toHaveCount(0);
  await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  await page.evaluate(() => {
    const boundary = document.createElement('div'); boundary.dataset.nextgoBoundary = 'test[stream]'; boundary.textContent = 'new route';
    document.querySelector('main').append(boundary);
    const chunk = document.createElement('template'); chunk.dataset.nextgoChunk = 'test[stream]'; chunk.innerHTML = '<p>stale streamed content</p>';
    document.body.append(chunk);
  });
  await expect(page.locator('template[data-nextgo-chunk]')).toHaveCount(0);
  await expect(page.locator('[data-nextgo-boundary="test[stream]"]')).toHaveText('new route');
});

test('structured rich head updates on navigation and rejects unsafe head before touching islands', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.route('**/posts?head=rich', async route => {
    const response = await route.fetch();
    const json = await response.json();
    json.head = [
      { tag: 'link', attrs: { rel: 'canonical', href: 'https://example.com/notes' } },
      { tag: 'meta', attrs: { property: 'og:title', content: 'Notes <Go> & "WASM"' } },
      { tag: 'link', attrs: { rel: 'alternate', href: 'https://example.com/fr/notes', hreflang: 'fr' } },
    ];
    await route.fulfill({ response, json });
  });
  await notes(page).evaluate(el => { el.href = '/posts?head=rich'; el.click(); });
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('link[rel=canonical]')).toHaveAttribute('href', 'https://example.com/notes');
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', 'Notes <Go> & "WASM"');
  await expect(page.locator('link[hreflang=fr]')).toHaveCount(1);
  await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  await expect(page.locator('link[hreflang=fr]')).toHaveCount(0);
  await page.goto('/'); await ready(page);
  await page.route('**/posts?head=unsafe', async route => {
    const response = await route.fetch();
    const json = await response.json();
    json.head = [{ tag: 'meta', attrs: { 'http-equiv': 'refresh', content: '0;url=https://example.com' } }];
    await route.fulfill({ response, json });
  });
  await notes(page).evaluate(el => { el.href = '/posts?head=unsafe'; el.click(); });
  await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(page).toHaveURL('http://127.0.0.1:4100/');
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]').first()).toHaveText('4');
});

test('matching persistent layout outlets retain island identity and dispose diverging content', async ({ page }) => {
  await page.goto('/'); await ready(page);
  const layoutMarkup = await page.evaluate(() => {
    document.body.dataset.nextgoPreserveLayouts = 'true';
    const root = document.getElementById('nextgo-app');
    const counter = root.querySelector('[data-nextgo-island="counter"]');
    window.sharedLayoutCounter = counter;
    window.disposedPageCounter = root.querySelectorAll('[data-nextgo-island="counter"]')[1];
    window.disposedPageButton = window.disposedPageCounter.querySelector('[data-increment]');
    const markup = counter.outerHTML;
    const layout = document.createElement('nextgo-layout'); layout.dataset.nextgoLayout = 'test-shared-layout';
    const outlet = document.createElement('nextgo-outlet'); outlet.dataset.nextgoOutlet = 'test-shared-layout';
    counter.remove();
    while (root.firstChild) outlet.append(root.firstChild);
    layout.append(counter, outlet); root.append(layout);
    return markup;
  });
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await page.route('**/posts?persistent=layout', async route => {
    const response = await route.fetch();
    const json = await response.json();
    json.html = `<nextgo-layout data-nextgo-layout="test-shared-layout">${layoutMarkup}<nextgo-outlet data-nextgo-outlet="test-shared-layout">${json.html}</nextgo-outlet></nextgo-layout>`;
    await route.fulfill({ response, json });
  });
  await notes(page).evaluate(el => { el.href = '/posts?persistent=layout'; el.click(); });
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(await page.evaluate(() => window.sharedLayoutCounter.isConnected)).toBe(true);
  await expect(page.getByRole('region', { name: 'First counter', exact: true }).locator('[data-count]')).toHaveText('4');
  expect(await page.evaluate(() => { window.disposedPageButton.click(); return window.disposedPageCounter.querySelector('[data-count]').textContent; })).toBe('10');
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.getByRole('region', { name: 'First counter', exact: true }).locator('[data-count]')).toHaveText('5');
  await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  expect(await page.evaluate(() => window.sharedLayoutCounter.isConnected)).toBe(false);
});

test('route loading UI renders in the status region while the current app stays intact', async ({ page }) => {
  await page.goto('/'); await ready(page);
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  const configuration = await page.locator('body').getAttribute('data-nextgo-configuration');
  await page.route('**/_nextgo/loading?**', route => route.fulfill({ status: 200, contentType: 'text/html', headers: { 'X-Nextgo-Configuration': configuration }, body: '<p data-route-loading>Fetching field notes…</p>' }));
  await page.route('**/posts?loading=fallback', async route => {
    const response = await route.fetch();
    await gate;
    await route.fulfill({ response });
  });
  await notes(page).evaluate(el => { el.href = '/posts?loading=fallback'; el.click(); });
  await expect(page.locator('#nextgo-status [data-route-loading]')).toHaveText('Fetching field notes…');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
  release();
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('#nextgo-status')).toBeHidden();
});

test('static export navigation fetches JSON files and retains requested query and fragment', async ({ page, request }) => {
  await page.goto('/'); await ready(page);
  const response = await request.get('/posts', { headers: { 'X-Nextgo-Navigate': '1' } });
  const json = await response.json();
  let requests = 0;
  await page.route('**/_nextgo/data/posts.json', async route => {
    requests++;
    expect(route.request().headers()['x-nextgo-navigate']).toBeUndefined();
    await route.fulfill({ status: 200, contentType: 'application/json', json });
  });
  await page.evaluate(() => { document.body.dataset.nextgoExport = 'true'; window.exportMarker = 'same-document'; });
  await notes(page).evaluate(el => { el.href = '/posts?source=static#notes'; el.click(); });
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page).toHaveURL('http://127.0.0.1:4100/posts?source=static#notes');
  expect(await page.evaluate(() => window.exportMarker)).toBe('same-document');
  expect(requests).toBe(1);
});

test('named profiles reuse Go prefetch independently of HTTP caching and action dispatch and settlement invalidate it', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  const before = Number(await page.locator('[data-server-count]').textContent());
  const path = `/profiles?scenario=actions-${Date.now()}`;
  await page.evaluate(path => {
    window.profileMarker = 'same-document';
    const a = document.createElement('a'); a.id = 'profile-link'; a.href = path; a.textContent = 'Profile case'; document.querySelector('main').append(a);
  }, path);
  let profileRequests = 0;
  await page.route(`**${path}`, async route => {
    const response = await route.fetch(); profileRequests++;
    expect(response.headers()['cache-control']).toBe('public, max-age=0, must-revalidate');
    expect(Number(response.headers()['x-nextgo-stale-millis'])).toBeGreaterThan(0);
    await route.fulfill({ response });
  });
  const link = page.locator('#profile-link');
  await link.dispatchEvent('pointerover');
  await expect.poll(() => profileRequests).toBe(1);
  let releaseAction;
  const held = new Promise(resolve => { releaseAction = resolve; });
  let accepted;
  const writeAccepted = new Promise(resolve => { accepted = resolve; });
  await page.route('**/_nextgo/actions/increment', async route => {
    const response = await route.fetch(); accepted(response.status());
    await held; await route.fulfill({ response });
  });
  try {
    await page.locator('[data-action-only]').click();
    expect(await writeAccepted).toBe(200);
    // Dispatch must clear the preceding prefetch while the action is pending.
    await link.dispatchEvent('pointerover');
    await expect.poll(() => profileRequests).toBe(2);
    releaseAction();
    await expect(page.locator('[data-action-status]')).toContainText('Saved without refreshing');
    await expect(page.locator('[data-server-count]')).toHaveText(String(before));
    // Settlement must also discard data filled while the mutation was pending.
    await link.evaluate(a => a.setAttribute('data-nextgo-prefetch', 'false'));
    await link.click();
    await expect(page).toHaveTitle(`Profiled snapshot ${before + 1} — Next.go`);
    await expect(page.locator('[data-server-count]')).toHaveText(String(before + 1));
    expect(profileRequests).toBe(3);
    expect(await page.evaluate(() => window.profileMarker)).toBe('same-document');
  } finally { releaseAction(); }
});

test('slow profile responses cannot receive a new browser lifetime after their original deadline', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  const path = `/profiles?scenario=slow-${Date.now()}`;
  await page.evaluate(path => {
    window.profileSlowMarker = 'same-document';
    const a = document.createElement('a'); a.id = 'profile-slow'; a.href = path; a.textContent = 'Slow profile'; document.querySelector('main').append(a);
  }, path);
  let requests = 0;
  await page.route(`**${path}`, async route => {
    const response = await route.fetch(); requests++;
    await new Promise(resolve => setTimeout(resolve, 250));
    await route.fulfill({ response, headers: { ...response.headers(), 'x-nextgo-stale-millis': '100' } });
  });
  const link = page.locator('#profile-slow');
  const loaded = page.waitForResponse(response => response.url().endsWith(path));
  await link.dispatchEvent('pointerover');
  await (await loaded).finished();
  await link.evaluate(a => a.setAttribute('data-nextgo-prefetch', 'false'));
  await link.click();
  await expect(page).toHaveURL(new RegExp('/profiles\\?scenario=slow-'));
  await expect(page.locator('[data-action-only]')).toBeEnabled();
  expect(requests).toBe(2);
  expect(await page.evaluate(() => window.profileSlowMarker)).toBe('same-document');
});

test('the non-expiring default profile supports SSR, bounded Go reuse and on-demand refresh', async ({ page, request }) => {
  const path = `/evergreen?scenario=default-${Date.now()}`;
  const initial = await page.goto(path); await ready(page);
  expect(initial.headers()['cache-control']).toBe('public, max-age=0, must-revalidate');
  expect(Number(initial.headers()['x-nextgo-stale-millis'])).toBeGreaterThan(0);
  expect(Number(initial.headers()['x-nextgo-stale-millis'])).toBeLessThanOrEqual(300000);
  const before = Number(await page.locator('[data-server-count]').textContent());
  await expect(page.locator('[data-evergreen-publish]')).toBeVisible();
  await page.evaluate(() => { window.evergreenDocument = 'retained'; });
  const publish = await request.post('/_nextgo/actions/evergreen-publish', {
    headers: { Origin: new URL(page.url()).origin }, data: {},
  });
  expect(publish.status()).toBe(200);
  expect((await publish.json()).data.count).toBe(before + 1);
  const response = page.waitForResponse(r => r.url().endsWith(path) && r.request().headers()['x-nextgo-navigate'] === '1');
  await page.locator('[data-refresh]').click();
  expect((await (await response).json()).title).toBe(`Evergreen snapshot ${before} — Next.go`);
  await expect.poll(async () => {
    const r = await request.get(path, { headers: { 'X-Nextgo-Navigate': '1' } });
    return (await r.json()).title;
  }).toBe(`Evergreen snapshot ${before + 1} — Next.go`);
  await page.locator('[data-refresh]').click();
  await expect(page).toHaveTitle(`Evergreen snapshot ${before + 1} — Next.go`);
  await page.locator('[data-action-increment]').click();
  await expect(page).toHaveTitle(`Evergreen snapshot ${before + 2} — Next.go`);
  expect(await page.evaluate(() => window.evergreenDocument)).toBe('retained');
});

test('on-demand stale publication retains the view and refreshes its Go snapshot', async ({ page, request }) => {
  const path = `/profiles?scenario=marked-${Date.now()}`;
  await page.goto(path); await ready(page);
  const before = Number(await page.locator('[data-server-count]').textContent());
  await page.evaluate(() => { window.markedSnapshotDocument = 'same'; });
  await expect(page.locator('[data-publish-stale]')).toBeVisible();
  const publish = await request.post('/_nextgo/actions/cache-publish-stale', {
    headers: { Origin: new URL(page.url()).origin }, data: {},
  });
  expect(publish.status()).toBe(200);
  expect((await publish.json()).data.count).toBe(before + 1);
  const response = page.waitForResponse(r => r.url().endsWith(path) && r.request().headers()['x-nextgo-navigate'] === '1');
  await page.locator('[data-refresh]').click();
  const stale = await (await response).json();
  expect(stale.title).toBe(`Profiled snapshot ${before} — Next.go`);
  await expect(page.locator('[data-server-count]')).toHaveText(String(before));
  await expect.poll(async () => {
    const r = await request.get(path, { headers: { 'X-Nextgo-Navigate': '1' } });
    return (await r.json()).title;
  }).toBe(`Profiled snapshot ${before + 1} — Next.go`);
  await page.locator('[data-refresh]').click();
  await expect(page.locator('[data-server-count]')).toHaveText(String(before + 1));
  expect(await page.evaluate(() => window.markedSnapshotDocument)).toBe('same');
});

test('server lifetimes return a stale Go view, refresh its matching snapshot, and publish mutations immediately', async ({ page, request }) => {
  const path = `/cache?scenario=${Date.now()}`;
  await page.goto(path); await ready(page);
  const before = Number(await page.locator('[data-server-count]').textContent());
  await page.evaluate(() => { window.lifetimeMarker = 'same-document'; });
  const source = await request.post('/_nextgo/actions/cache-source-increment', {
    headers: { Origin: new URL(page.url()).origin }, data: {},
  });
  expect(source.status()).toBe(200);
  // Cross the configured one-second fresh window while staying below hard expiry.
  await page.waitForTimeout(1200);
  const refreshed = page.waitForResponse(response => response.url().endsWith(path) && response.request().headers()['x-nextgo-navigate'] === '1');
  await page.locator('[data-refresh]').click();
  const staleResponse = await refreshed;
  expect(staleResponse.headers()['cache-control']).toBe('no-store');
  expect(staleResponse.headers()['x-nextgo-prefetch']).toBeUndefined();
  const stale = await staleResponse.json();
  expect(stale.title).toBe(`Cached snapshot ${before} — Next.go`);
  await expect(page.locator('[data-server-count]')).toHaveText(String(before));
  await expect.poll(async () => {
    const response = await request.get(path, { headers: { 'X-Nextgo-Navigate': '1' } });
    return (await response.json()).title;
  }).toBe(`Cached snapshot ${before + 1} — Next.go`);
  await page.locator('[data-refresh]').click();
  await expect(page.locator('[data-server-count]')).toHaveText(String(before + 1));
  await expect(page).toHaveTitle(`Cached snapshot ${before + 1} — Next.go`);
  await page.locator('[data-action-increment]').click();
  await expect(page.locator('[data-server-count]')).toHaveText(String(before + 2));
  await expect(page).toHaveTitle(`Cached snapshot ${before + 2} — Next.go`);
  expect(await page.evaluate(() => window.lifetimeMarker)).toBe('same-document');
});

test('Go server actions refresh data and invalidate prefetch while preserving layouts and history', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  const initial = Number(await page.locator('[data-server-count]').textContent());
  await page.evaluate(() => { window.actionMarker = 'same-document'; window.layoutRoot = document.querySelector('[data-nextgo-island="layoutstate"]'); });
  await page.locator('[data-layout-increment]').click();
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  const historyLength = await page.evaluate(() => history.length);
  let prefetchCount = 0;
  await page.route('**/posts', async route => {
    const response = await route.fetch();
    prefetchCount++;
    await route.fulfill({ response, headers: { ...response.headers(), 'cache-control': 'public, max-age=30', 'x-nextgo-prefetch': 'public' } });
  });
  await notes(page).dispatchEvent('pointerover');
  await expect.poll(() => prefetchCount).toBe(1);
  const action = page.waitForResponse(response => response.url().endsWith('/_nextgo/actions/increment') && response.request().method() === 'POST');
  await page.locator('[data-action-increment]').click();
  expect((await action).status()).toBe(200);
  await expect(page.locator('[data-server-count]')).toHaveText(String(initial + 1));
  expect(await page.evaluate(() => history.length)).toBe(historyLength);
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  expect(await page.evaluate(() => window.layoutRoot === document.querySelector('[data-nextgo-island="layoutstate"]'))).toBe(true);
  const refresh = page.waitForResponse(response => new URL(response.url()).pathname === '/features' && response.request().headers()['x-nextgo-navigate'] === '1');
  await page.locator('[data-refresh]').click(); await refresh;
  await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy');
  expect(await page.evaluate(() => history.length)).toBe(historyLength);
  await page.locator('[data-navigate]').click();
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(prefetchCount).toBe(2);
  expect(await page.evaluate(() => window.actionMarker)).toBe('same-document');
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  await expect(notes(page)).toHaveAttribute('aria-current', 'page');
  await page.goBack(); await expect(page).toHaveTitle('Framework features — Next.go');
  await expect(page.locator('[data-server-count]')).toHaveText(String(initial + 1));
});

test('initial streaming shell stays intact as deferred content arrives and hydrates', async ({ page }) => {
  await page.goto('/stream', { waitUntil: 'commit' });
  await expect(page.locator('[data-stream-loading]')).toBeVisible();
  await page.evaluate(() => { window.streamApp = document.getElementById('nextgo-app'); window.streamHeading = document.querySelector('h1'); });
  await expect(page.locator('[data-stream-loaded]')).toBeVisible();
  await ready(page);
  expect(await page.evaluate(() => window.streamApp === document.getElementById('nextgo-app') && window.streamHeading === document.querySelector('h1'))).toBe(true);
  await page.getByRole('button', { name: 'Increase Streamed counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText('8');
  await expect(page.locator('[data-stream-loading]')).toHaveCount(0);
});

test('sample metadata and image information update and clear during navigation', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  await expect(page.locator('link[rel=canonical]')).toHaveAttribute('href', 'https://example.com/features');
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', 'Go across the stack');
  await page.getByRole('link', { name: 'Image optimization and SEO' }).click();
  await expect(page).toHaveTitle('Responsive media — Next.go');
  await expect(page.locator('link[rel=canonical]')).toHaveAttribute('href', 'https://example.com/media');
  await expect(page.locator('meta[property="og:image"]')).toHaveAttribute('content', /\/_nextgo\/image\?/);
  await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute('content', 'summary_large_image');
  await expect(page.locator('img.responsive-media')).toHaveAttribute('width', '960');
  await expect(page.locator('img.responsive-media')).toHaveAttribute('srcset', /w=960/);
  await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  await expect(page.locator('link[rel=canonical]')).toHaveCount(0);
  await expect(page.locator('meta[property="og:image"]')).toHaveCount(0);
});

test('oversized navigation responses fail without replacing or disabling the current view', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.sizeLimitApp = document.getElementById('nextgo-app'); window.sizeLimitCounter = document.querySelector('[data-nextgo-island="counter"]'); });
  await page.route('**/posts?oversized=1', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: ' '.repeat((8 << 20) + 1),
  }));
  await notes(page).evaluate(el => { el.href = '/posts?oversized=1'; el.click(); });
  await expect(page.locator('#nextgo-status')).toContainText('response exceeds byte limit');
  await expect(page).toHaveURL('http://127.0.0.1:4100/');
  expect(await page.evaluate(() => window.sizeLimitApp === document.getElementById('nextgo-app') && window.sizeLimitCounter === document.querySelector('[data-nextgo-island="counter"]'))).toBe(true);
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  await architecture(page).evaluate(el => el.click());
  await expect(page).toHaveTitle('Architecture — Next.go');
  expect(errors).toEqual([]);
});

test('a canceled fetch can settle late without stale navigation or released Go callbacks', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
  await page.goto('/'); await ready(page);
  await page.evaluate(() => {
    const originalFetch = window.fetch;
    window.fetch = (input, init) => {
      const url = new URL(typeof input === 'string' ? input : input.url, location.href);
      if (url.pathname === '/posts' && url.search === '?cancel=late') {
        // Deliberately ignore AbortSignal so Go's retained Promise callbacks are
        // exercised after navigation has already canceled the request context.
        return new Promise(resolve => {
          window.finishCanceledFetch = async () => {
            const response = await originalFetch(input, { ...init, signal: undefined });
            resolve(response);
          };
          window.delayedFetchStarted = true;
        });
      }
      return originalFetch(input, init);
    };
  });
  await notes(page).evaluate(el => { el.href = '/posts?cancel=late'; el.click(); });
  await expect.poll(() => page.evaluate(() => window.delayedFetchStarted)).toBe(true);
  await architecture(page).evaluate(el => el.click());
  await expect(page).toHaveTitle('Architecture — Next.go');
  await page.evaluate(async () => {
    await window.finishCanceledFetch();
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  });
  await expect(page).toHaveURL('http://127.0.0.1:4100/about');
  await expect(page).toHaveTitle('Architecture — Next.go');
  await page.locator('[data-layout-increment]').click();
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  await expect(page.locator('#nextgo-status')).toBeHidden();
  expect(errors).toEqual([]);
});

test('Refresh bypasses a genuinely populated HTTP cache after invalidating Go prefetch data', async ({ page, browserName }) => {
  test.skip(browserName !== 'chromium', 'HTTP cache-hit proof uses Chromium CDP; refresh behavior is exercised in every engine separately.');
  await page.goto('/features'); await ready(page);
  const session = await page.context().newCDPSession(page);
  await session.send('Network.enable');
  await session.send('Network.setCacheDisabled', { cacheDisabled: false });
  const cached = new Set();
  const seen = [];
  session.on('Network.requestServedFromCache', event => cached.add(event.requestId));
  session.on('Network.responseReceived', event => {
    if (new URL(event.response.url).search === '?http-cache=refresh') {
      seen.push({ id: event.requestId, disk: event.response.fromDiskCache });
    }
  });
  const warm = () => page.evaluate(async () => {
    const response = await fetch('/posts?http-cache=refresh', { headers: { 'X-Nextgo-Navigate': '1' }, cache: 'force-cache' });
    await response.arrayBuffer();
    return response.headers.get('cache-control');
  });
  expect(await warm()).toContain('public');
  await warm();
  await expect.poll(() => seen.length).toBe(2);
  expect(seen[1].disk || cached.has(seen[1].id)).toBeTruthy();
  await notes(page).evaluate(el => {
    el.href = '/posts?http-cache=refresh';
    el.dispatchEvent(new PointerEvent('pointerover', { bubbles: true }));
  });
  await expect.poll(() => seen.length).toBe(3);
  const refresh = page.waitForResponse(response => new URL(response.url()).pathname === '/features' && response.request().headers()['x-nextgo-navigate'] === '1');
  await page.locator('[data-refresh]').click(); await refresh;
  await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy');
  await notes(page).evaluate(el => el.click());
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect.poll(() => seen.length).toBe(4);
  for (const response of seen.slice(2)) {
    expect(Boolean(response.disk || cached.has(response.id))).toBe(false);
  }
  await session.detach();
});

// Use real HTTP redirects: WebKit cannot synthesize redirect status through
// Playwright route.fulfill. Forwarding bytes also preserves gzip WASM behavior.
async function redirectFixture(match, payload) {
  let foreignURL;
  let foreignRequests = 0;
  const origin = createServer((req, res) => {
    if (match(req.url)) { res.writeHead(302, { location: foreignURL }); res.end(); return; }
    const upstream = proxyRequest({ hostname: '127.0.0.1', port: 4100, path: req.url, method: req.method, headers: req.headers }, response => {
      res.writeHead(response.statusCode, response.headers); response.pipe(res);
    });
    upstream.on('error', () => { if (!res.headersSent) res.writeHead(502); res.end(); });
    req.pipe(upstream);
  });
  await new Promise(resolve => origin.listen(0, '127.0.0.1', resolve));
  const originURL = `http://127.0.0.1:${origin.address().port}`;
  const foreign = createServer((req, res) => {
    res.setHeader('Access-Control-Allow-Origin', originURL);
    res.setHeader('Access-Control-Allow-Headers', req.headers['access-control-request-headers'] || 'X-Nextgo-Navigate, Cache-Control');
    if (req.method === 'OPTIONS') { res.writeHead(204); res.end(); return; }
    foreignRequests++;
    payload(req, res);
  });
  await new Promise(resolve => foreign.listen(0, '127.0.0.1', resolve));
  foreignURL = `http://127.0.0.1:${foreign.address().port}/payload`;
  return { originURL, count: () => foreignRequests, close: async () => {
    await Promise.all([origin, foreign].map(server => new Promise(resolve => server.close(resolve))));
  } };
}

test('navigation rejects a foreign CORS response after a same-origin redirect', async ({ page }) => {
  let build, configuration;
  const fixture = await redirectFixture(url => url === '/posts?foreign=1', (req, res) => {
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ version: 3, build, configuration, url: '/posts', status: 200, title: 'Forged foreign page', html: '<h1>Forged foreign page</h1><img src="/missing-probe" onerror="window.foreignExecuted=true">' }));
  });
  try {
    await page.goto(fixture.originURL); await ready(page);
    build = await page.locator('body').getAttribute('data-nextgo-build');
    configuration = await page.locator('body').getAttribute('data-nextgo-configuration');
    await notes(page).evaluate(el => { el.href = '/posts?foreign=1'; el.click(); });
    await expect(page.locator('#nextgo-status')).toContainText('outside the application origin');
    expect(fixture.count()).toBeGreaterThan(0);
    await expect(page).toHaveURL(fixture.originURL + '/');
    await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
    expect(await page.evaluate(() => window.foreignExecuted)).toBeUndefined();
    await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
    await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  } finally { await fixture.close(); }
});

test('loading fallback rejects a foreign CORS HTML response', async ({ page }) => {
  let configuration;
  const fixture = await redirectFixture(url => url.startsWith('/_nextgo/loading?'), (req, res) => {
    res.setHeader('Content-Type', 'text/html');
    res.setHeader('X-Nextgo-Configuration', configuration);
    res.end('<p data-foreign-loading>Forged loading</p><img src="/missing-probe" onerror="window.foreignExecuted=true">');
  });
  try {
    await page.goto(fixture.originURL); await ready(page);
    configuration = await page.locator('body').getAttribute('data-nextgo-configuration');
    await notes(page).evaluate(el => { el.href = '/posts?delay=1500ms'; el.click(); });
    await expect.poll(fixture.count).toBeGreaterThan(0);
    await expect(page.locator('[data-foreign-loading]')).toHaveCount(0);
    expect(await page.evaluate(() => window.foreignExecuted)).toBeUndefined();
    await expect(page).toHaveTitle('Field notes — Next.go');
  } finally { await fixture.close(); }
});

test('page stylesheet resources apply during navigation and leave with the page', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  await page.evaluate(() => { window.resourceDocument = document; });
  await page.evaluate(() => { const a = document.createElement('a'); a.href='/media'; a.textContent='Media resources'; document.body.appendChild(a); a.click(); });
  await expect(page).toHaveTitle('Responsive media — Next.go');
  await expect(page.locator('head link[data-nextgo-head][rel=stylesheet][href="/assets/media.css"]')).toHaveCount(1);
  await expect(page.locator('[data-resource-example]')).toHaveCSS('color', 'rgb(18, 109, 100)');
  await architecture(page).evaluate(el => el.click());
  await expect(page).toHaveTitle('Architecture — Next.go');
  await expect(page.locator('head link[href="/assets/media.css"]')).toHaveCount(0);
  expect(await page.evaluate(() => window.resourceDocument === document)).toBe(true);
});

test('file route handlers outside API use normal navigation and method dispatch', async ({ page, request }) => {
  const direct = await request.get('/downloads/quarterly%20report');
  expect(direct.status()).toBe(200);
  expect(direct.headers()['content-type']).toContain('text/plain');
  expect(await direct.text()).toContain('Name: quarterly report');
  const unsupported = await request.post('/downloads/report');
  expect(unsupported.status()).toBe(405);
  expect(unsupported.headers().allow).toBe('GET, HEAD, OPTIONS');
  const options = await request.fetch('/downloads/report', { method: 'OPTIONS' });
  expect(options.status()).toBe(204);
  await page.goto('/features'); await ready(page);
  await page.evaluate(() => { window.endpointOldDocument = true; });
  const navigationHeaders = [];
  page.on('request', req => { if (new URL(req.url()).pathname.startsWith('/downloads/')) navigationHeaders.push(req.headers()['x-nextgo-navigate']); });
  await page.getByRole('link', { name: 'HTTP route: /downloads/report', exact: true }).click();
  await expect(page).toHaveURL(/\/downloads\/report$/);
  await expect(page.locator('body')).toContainText('Generated by a Go route handler. Name: report');
  expect(await page.evaluate(() => window.endpointOldDocument)).toBeUndefined();
  expect(navigationHeaders).toEqual([undefined]);
});

test('segment templates retain state for deeper and query navigation and reset on a new child', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/templates/a'); await ready(page);
  await page.evaluate(() => {
    window.savedTemplate = document.querySelector('[data-template-example]');
    window.savedTemplateIsland = window.savedTemplate.querySelector('[data-nextgo-island]');
    window.savedTemplateButton = window.savedTemplate.querySelector('[data-increment]');
  });
  await page.getByRole('button', { name: 'Increase Template counter', exact: true }).click();
  await page.locator('[data-layout-increment]').click();
  await expect(page.locator('[data-count]')).toHaveText('1');
  const routes = page.getByRole('navigation', { name: 'Template routes', exact: true });
  await routes.getByRole('link', { name: 'Deeper in A', exact: true }).click();
  await expect(page).toHaveURL(/\/templates\/a\/details$/);
  expect(await page.evaluate(() => window.savedTemplate === document.querySelector('[data-template-example]') && window.savedTemplateIsland === document.querySelector('[data-template-example] [data-nextgo-island]'))).toBe(true);
  await expect(page.locator('[data-count]')).toHaveText('1');
  await routes.getByRole('link', { name: 'Query in A', exact: true }).click();
  await expect(page).toHaveURL(/\/templates\/a\?q=second$/);
  await expect(page.locator('[data-template-route]')).toHaveText('/templates/a?q=second');
  await expect(page.locator('[data-count]')).toHaveText('1');
  await routes.getByRole('link', { name: 'Child B', exact: true }).click();
  await expect(page).toHaveURL(/\/templates\/b$/);
  await expect(page.locator('[data-count]')).toHaveText('0');
  expect(await page.evaluate(() => window.savedTemplate !== document.querySelector('[data-template-example]'))).toBe(true);
  expect(await page.evaluate(() => { window.savedTemplateButton.click(); return window.savedTemplateIsland.querySelector('[data-count]').textContent; })).toBe('1');
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  await page.goBack(); await expect(page).toHaveURL(/\/templates\/a\?q=second$/);
  await expect(page.locator('[data-count]')).toHaveText('0');
  expect(errors).toEqual([]);
});

test('keyboard navigation moves focus, announces the page, and refresh retains connected focus', async ({ page, browserName }) => {
  await page.goto('/'); await ready(page);
  await notes(page).focus();
  await page.keyboard.press('Enter');
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('main')).toBeFocused();
  await expect(page.locator('#nextgo-announcer')).toHaveText('Field notes — Next.go');
  await expect(page.locator('#nextgo-announcer')).toHaveAttribute('aria-live', 'polite');
  await page.keyboard.press('Tab');
  if (process.platform === 'darwin' && browserName === 'webkit' && await page.evaluate(() => document.activeElement === document.body)) {
    // Safari's default Mac keyboard mode uses Option-Tab for links. Preserve
    // the user's OS preference while exercising actual keyboard focus order.
    await page.locator('main').focus();
    await page.keyboard.press('Alt+Tab');
  }
  expect(await page.evaluate(() => document.activeElement.tagName)).toBe('A');
  await page.evaluate(() => { const a = document.createElement('a'); a.href='/templates/a'; document.body.appendChild(a); a.click(); });
  await expect(page.locator('h1[data-nextgo-focus]')).toBeFocused();
  await expect(page.locator('#nextgo-announcer')).toHaveText('Segment templates — Next.go');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Features', exact: true }).click();
  await expect(page).toHaveTitle('Framework features — Next.go');
  const layoutButton = page.locator('[data-layout-increment]');
  await layoutButton.focus();
  const response = page.waitForResponse(res => new URL(res.url()).pathname === '/features' && res.request().headers()['x-nextgo-navigate'] === '1');
  await page.locator('[data-refresh]').evaluate(el => el.click());
  await response;
  await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy', 'true');
  await expect(layoutButton).toBeFocused();
});

test('typed JSON-LD is inert in SSR and navigation, clears on exit, and rejects executable scripts', async ({ page }) => {
  await page.goto('/media'); await ready(page);
  const structured = page.locator('head script[type="application/ld+json"][data-nextgo-head]');
  await expect(structured).toHaveCount(1);
  expect(JSON.parse(await structured.textContent())).toMatchObject({ '@context': 'https://schema.org', '@type': 'WebPage', name: 'Responsive media — Next.go' });
  await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  await expect(structured).toHaveCount(0);
  const payload = '</script><img src=x onerror="window.structuredExecuted=true">';
  await page.route('**/posts?jsonld=safe', async route => {
    const response = await route.fetch();
    const json = await response.json();
    json.head = [{ tag: 'script', attrs: { type: 'application/ld+json' }, text: JSON.stringify({ '@type': 'Thing', name: payload }) }];
    await route.fulfill({ response, json });
  });
  await notes(page).evaluate(el => { el.href='/posts?jsonld=safe'; el.click(); });
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(JSON.parse(await structured.textContent()).name).toBe(payload);
  expect(await page.evaluate(() => window.structuredExecuted)).toBeUndefined();
  await expect(page.locator('head img')).toHaveCount(0);
  await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  await expect(structured).toHaveCount(0);
  let invalidIndex = 0;
  for (const item of [
    { tag: 'script', attrs: { type: 'text/javascript' }, text: 'window.structuredExecuted=true' },
    { tag: 'script', attrs: { type: 'application/ld+json', src: '/evil.js' }, text: '{}' },
    { tag: 'script', attrs: { type: 'application/ld+json' }, text: '{invalid}' },
  ]) {
    const invalidPath = `/posts?jsonld=invalid${invalidIndex++}`;
    await page.route('**' + invalidPath, async route => {
      const response = await route.fetch(); const json = await response.json(); json.head=[item];
      await route.fulfill({ response, json });
    });
    await notes(page).evaluate((el, href) => { el.href=href; el.click(); }, invalidPath);
    await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
    await expect(page).toHaveURL(/\/about$/);
    await expect(structured).toHaveCount(0);
    expect(await page.evaluate(() => window.structuredExecuted)).toBeUndefined();
    await page.unroute('**' + invalidPath);
    // Retry exactly the failed URL after repair; a malformed public head must
    // not have entered the Go prefetch cache.
    await notes(page).evaluate((el, href) => { el.href=href; el.click(); }, invalidPath);
    await expect(page).toHaveTitle('Field notes — Next.go');
    await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  }
});

test('unmarked same-origin JSON is not adopted as page HTML', async ({ page }) => {
  await page.goto('/'); await ready(page);
  const build = await page.locator('body').getAttribute('data-nextgo-build');
  const requests = [];
  page.on('request', request => { if (new URL(request.url()).pathname === '/opaque-json') requests.push({ navigation: request.isNavigationRequest(), header: request.headers()['x-nextgo-navigate'] }); });
  await page.route('**/opaque-json', route => route.fulfill({ contentType: 'application/json', json: { version: 3, build, status: 200, url: '/opaque-json', title: 'Untrusted envelope', html: '<img data-echo-html src=x onerror="window.echoExecuted=true">' } }));
  await page.evaluate(() => { window.oldPageDocument = true; const a=document.createElement('a'); a.href='/opaque-json'; document.body.appendChild(a); a.click(); });
  await expect(page).toHaveURL(/\/opaque-json$/);
  expect(await page.evaluate(() => window.oldPageDocument)).toBeUndefined();
  expect(await page.evaluate(() => window.echoExecuted)).toBeUndefined();
  await expect(page.locator('[data-echo-html]')).toHaveCount(0);
  expect(requests.some(request => request.header === '1')).toBe(true);
  expect(requests.some(request => request.navigation)).toBe(true);
});

test('unsupported navigation protocol falls back to a complete document', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.protocolDocument = true; });
  await page.route('**/posts?protocol=old', async route => {
    if (route.request().headers()['x-nextgo-navigate'] !== '1') { await route.continue(); return; }
    const response = await route.fetch(); const json = await response.json(); json.version = 999;
    await route.fulfill({ response, json });
  });
  await notes(page).evaluate(el => { el.href='/posts?protocol=old'; el.click(); });
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(await page.evaluate(() => window.protocolDocument)).toBeUndefined();
  await ready(page);
});

test('Go programmatic prefetch reuses public responses and Replace does not add history', async ({ page, request }) => {
  await page.goto('/'); await ready(page);
  // Other cases share the process-local ten-second /posts generation. Expire
  // it explicitly so this reuse assertion cannot land at its hard deadline.
  const published = await request.post('/_nextgo/actions/increment', {
    headers: { Origin: new URL(page.url()).origin }, data: { amount: 1 },
  });
  expect(published.status()).toBe(200);
  await page.evaluate(() => { window.routerControlDocument = 'retained'; });
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Features', exact: true }).click();
  await expect(page).toHaveTitle('Framework features — Next.go');
  let requests = 0;
  await page.route('**/posts', async route => {
    if (route.request().headers()['x-nextgo-navigate'] === '1') requests++;
    await route.continue();
  });
  await page.getByRole('button', { name: 'Prefetch field notes', exact: true }).click();
  await expect.poll(() => requests).toBe(1);
  await page.getByRole('button', { name: 'Navigate to field notes', exact: true }).click();
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(requests).toBe(1);
  await page.goBack();
  await expect(page).toHaveTitle('Framework features — Next.go');
  const length = await page.evaluate(() => history.length);
  await page.getByRole('button', { name: 'Replace with first field note', exact: true }).click();
  await expect(page).toHaveURL(/\/posts\/html-first$/);
  await expect(page).toHaveTitle('Start with the HTML — Next.go');
  expect(await page.evaluate(() => history.length)).toBe(length);
  expect(await page.evaluate(() => window.routerControlDocument)).toBe('retained');
  await page.goBack();
  await expect(page).toHaveURL('http://127.0.0.1:4100/');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
});

test('Go Replace uses replacement history for a full-document protocol fallback', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Features', exact: true }).click();
  await expect(page).toHaveTitle('Framework features — Next.go');
  await page.evaluate(() => { window.beforeReplaceReload = true; });
  await page.route('**/posts/html-first', async route => {
    if (route.request().headers()['x-nextgo-navigate'] !== '1') return route.continue();
    const response = await route.fetch();
    const data = await response.json();
    await route.fulfill({ response, json: { ...data, version: 999 } });
  });
  const length = await page.evaluate(() => history.length);
  await page.getByRole('button', { name: 'Replace with first field note', exact: true }).click();
  await expect(page).toHaveURL(/\/posts\/html-first$/);
  await ready(page);
  expect(await page.evaluate(() => window.beforeReplaceReload)).toBeUndefined();
  expect(await page.evaluate(() => history.length)).toBe(length);
  await page.goBack();
  await expect(page).toHaveURL('http://127.0.0.1:4100/');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
});

test('redirected back navigation canonicalizes the traversed entry without pushing history', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.redirectPopDocument = 'retained'; });
  await architecture(page).click();
  await expect(page).toHaveTitle('Architecture — Next.go');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Features', exact: true }).click();
  await expect(page).toHaveTitle('Framework features — Next.go');
  await page.route('**/about', async route => {
    if (route.request().headers()['x-nextgo-navigate'] !== '1') return route.continue();
    const response = await route.fetch({ url: 'http://127.0.0.1:4100/posts/html-first' });
    await route.fulfill({ response });
  });
  const length = await page.evaluate(() => history.length);
  await page.goBack();
  await expect(page).toHaveURL(/\/posts\/html-first$/);
  await expect(page).toHaveTitle('Start with the HTML — Next.go');
  expect(await page.evaluate(() => history.length)).toBe(length);
  expect(await page.evaluate(() => window.redirectPopDocument)).toBe('retained');
  await page.goForward();
  await expect(page).toHaveURL(/\/features$/);
  await expect(page).toHaveTitle('Framework features — Next.go');
});

test('Go Replace updates a same-page fragment without fetching, adding history, or losing focus', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  await page.evaluate(() => {
    // Scrolling to/from the fragment can put an unrelated link under the touch
    // browser's pointer. Isolate Replace's no-request contract from intent
    // prefetch; continue counting every navigation request below.
    document.querySelectorAll('a').forEach(link => link.setAttribute('data-nextgo-prefetch', 'false'));
    const target = document.createElement('h2');
    target.id = 'go-replace-fragment';
    target.textContent = 'Replacement fragment';
    document.querySelector('#nextgo-app').appendChild(target);
    document.querySelector('[data-replace]').setAttribute('data-route-target', '#go-replace-fragment');
    window.replaceFragmentNode = target;
  });
  let pageRequests = 0;
  page.on('request', request => { if (request.headers()['x-nextgo-navigate'] === '1') pageRequests++; });
  const length = await page.evaluate(() => history.length);
  await page.getByRole('button', { name: 'Replace with first field note', exact: true }).click();
  await expect(page).toHaveURL(/\/features#go-replace-fragment$/);
  expect(await page.evaluate(() => document.activeElement === window.replaceFragmentNode)).toBe(true);
  expect(await page.evaluate(() => history.length)).toBe(length);
  expect(pageRequests).toBe(0);
  await page.locator('[data-replace]').evaluate(button => button.setAttribute('data-route-target', 'https://user:secret@127.0.0.1:4100/about'));
  await page.getByRole('button', { name: 'Replace with first field note', exact: true }).click();
  await expect(page.locator('[data-action-status]')).toContainText('same-origin page URL without credentials');
  await expect(page).toHaveURL(/\/features#go-replace-fragment$/);
  expect(pageRequests).toBe(0);
});

for (const variant of ['missing', 'different', 'header', 'old-protocol']) {
  test(`incompatible ${variant} navigation configuration loads a fresh document`, async ({ page }) => {
    await page.goto('/'); await ready(page);
    await page.evaluate(() => { window.configurationDocument = true; });
    await page.route(`**/posts?configuration=${variant}`, async route => {
      if (route.request().headers()['x-nextgo-navigate'] !== '1') return route.continue();
      const response = await route.fetch();
      const json = await response.json();
      const headers = { ...response.headers() };
      if (variant === 'missing') delete json.configuration;
      if (variant === 'different') json.configuration = 'a'.repeat(64);
      if (variant === 'header') headers['x-nextgo-configuration'] = 'a'.repeat(64);
      if (variant === 'old-protocol') { json.version = 1; headers['x-nextgo-protocol'] = '1'; }
      json.html = '<h1 data-incompatible-page>Incompatible page</h1>';
      await route.fulfill({ response, headers, json });
    });
    await notes(page).evaluate((link, variant) => { link.href = `/posts?configuration=${variant}`; link.click(); }, variant);
    await expect(page).toHaveTitle('Field notes — Next.go'); await ready(page);
    expect(await page.evaluate(() => window.configurationDocument)).toBeUndefined();
    await expect(page.locator('[data-incompatible-page]')).toHaveCount(0);
  });
}

test('loading content from an incompatible deployment is ignored', async ({ page }) => {
  await page.goto('/'); await ready(page);
  let supplied;
  const loadingSupplied = new Promise(resolve => { supplied = resolve; });
  await page.route('**/_nextgo/loading?*', async route => {
    await route.fulfill({ contentType: 'text/html', headers: { 'X-Nextgo-Configuration': 'a'.repeat(64) }, body: '<p data-incompatible-loading>Incompatible loading content</p>' });
    supplied();
  });
  await notes(page).evaluate(link => { link.href = '/posts?delay=750ms'; link.click(); });
  await loadingSupplied;
  await expect(page.locator('[data-incompatible-loading]')).toHaveCount(0);
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('[data-incompatible-loading]')).toHaveCount(0);
});

test('an inconsistent initial configuration preserves SSR islands and ordinary link fallback', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('/', { waitUntil: 'domcontentloaded' });
  await page.evaluate(() => {
    window.configurationSSRNode = document.querySelector('[data-increment]');
    window.configurationLocationText = document.querySelector('[data-route-location]').firstChild;
    window.configurationSSRDocument = true;
    document.body.dataset.nextgoConfiguration = 'a'.repeat(64);
  });
  release(); await ready(page);
  await expect(page.locator('#nextgo-status')).toContainText('Incompatible document configuration');
  expect(await page.evaluate(() => window.configurationSSRNode === document.querySelector('[data-increment]'))).toBe(true);
  await expect(page.locator('[data-route-location]')).toHaveText('/');
  expect(await page.evaluate(() => window.configurationLocationText === document.querySelector('[data-route-location]').firstChild)).toBe(true);
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  await notes(page).click(); await expect(page).toHaveTitle('Field notes — Next.go'); await ready(page);
  expect(await page.evaluate(() => window.configurationSSRDocument)).toBeUndefined();
});

test('access boundaries return useful private 401 HTML without JavaScript and APIs enforce the same roles', async ({ browser, request }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  const response = await page.goto('http://127.0.0.1:4100/restricted');
  expect(response.status()).toBe(401);
  expect(response.headers()['www-authenticate']).toBe('Bearer realm="nextgo-demo"');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Sign in required');
  await expect(page.locator('[data-private-report]')).toHaveCount(0);
  await expect(page.locator('meta[name="robots"]')).toHaveAttribute('content', 'noindex');
  await expect(page.getByRole('navigation', { name: 'Main' })).toBeVisible();
  await context.close();
  for (const [token, status] of [['', 401], [readerToken, 403], [adminToken, 200]]) {
    for (const path of ['/restricted', '/api/report']) {
      const response = await request.get(path, { headers: token ? { Authorization: `Bearer ${token}` } : {} });
      expect(response.status()).toBe(status);
      expect(response.headers()['cache-control']).toBe('private, no-store');
      expect(response.headers()['x-nextgo-prefetch']).toBeUndefined();
      const content = await response.text();
      expect(content.includes('Only an authorized server request')).toBe(status === 200);
      if (token) expect(content).not.toContain(token);
    }
  }
  expect((await request.get('/api/report?role=administrator&token=' + adminToken)).status()).toBe(401);
});

for (const [role, token, status, title] of [['anonymous', '', 401, 'Sign in required — Next.go'], ['reader', readerToken, 403, 'Access denied — Next.go'], ['administrator', adminToken, 200, 'Private deployment report — Next.go']]) {
  test(`Go navigation enforces ${role} access and denied actions never mutate the report`, async ({ browser }) => {
    const context = await browser.newContext({ extraHTTPHeaders: token ? { Authorization: `Bearer ${token}` } : {} });
    try {
      const page = await context.newPage();
      await page.goto('http://127.0.0.1:4100/features'); await ready(page);
      await page.evaluate(() => { window.accessDocument = true; });
      const navigated = page.waitForResponse(response => new URL(response.url()).pathname === '/restricted' && response.request().headers()['x-nextgo-navigate'] === '1');
      await page.getByRole('link', { name: 'Authorization boundaries', exact: true }).click();
      expect((await navigated).status()).toBe(status);
      await expect(page).toHaveTitle(title);
      expect(await page.evaluate(() => window.accessDocument)).toBe(true);
      await expect(page.locator('[data-private-report]')).toHaveCount(status === 200 ? 1 : 0);
      await expect(page.locator('meta[name="robots"]')).toHaveAttribute('content', 'noindex');
      const inspectReviews = async () => {
        const response = await context.request.get('http://127.0.0.1:4100/restricted', { headers: { Authorization: `Bearer ${adminToken}` } });
        expect(response.status()).toBe(200);
        return Number((await response.text()).match(/data-report-reviews="">(\d+)</)[1]);
      };
      const before = await inspectReviews();
      const mutation = await context.request.post('http://127.0.0.1:4100/_nextgo/actions/review-report', { headers: { Origin: 'http://127.0.0.1:4100' }, data: {} });
      expect(mutation.status()).toBe(status);
      const result = await mutation.json();
      if (status === 200) expect(result.data.reviews).toBeGreaterThan(0);
      else expect(result.error.code).toBe(status === 401 ? 'unauthorized' : 'forbidden');
      expect(await inspectReviews()).toBe(before + (status === 200 ? 1 : 0));
      await page.goBack(); await expect(page).toHaveTitle('Framework features — Next.go');
      await expect(page.locator('meta[name="robots"]')).toHaveAttribute('content', 'index,follow');
    } finally { await context.close(); }
  });
}

test('a role change disposes protected content and retained layout state on a Go denial navigation', async ({ browser }) => {
  const context = await browser.newContext({ extraHTTPHeaders: { Authorization: `Bearer ${adminToken}` } });
  try {
    const page = await context.newPage();
    await page.goto('http://127.0.0.1:4100/restricted'); await ready(page);
    await page.evaluate(() => {
      window.protectedReportNode = document.querySelector('[data-private-report]');
      window.protectedLayoutNode = document.querySelector('nextgo-layout');
      window.roleChangeDocument = true;
    });
    await context.setExtraHTTPHeaders({ Authorization: `Bearer ${readerToken}` });
    await page.evaluate(() => {
      const link = document.createElement('a'); link.href = '/restricted?check=role-change'; document.querySelector('main').append(link); link.click();
    });
    await expect(page).toHaveTitle('Access denied — Next.go');
    expect(await page.evaluate(() => window.roleChangeDocument)).toBe(true);
    expect(await page.evaluate(() => window.protectedReportNode.isConnected || window.protectedLayoutNode.isConnected)).toBe(false);
    await expect(page.locator('[data-private-report]')).toHaveCount(0);
  } finally { await context.close(); }
});

test('metadata providers render inherited dynamic head in initial HTML without JavaScript', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  try {
    const page = await context.newPage();
    await page.goto('http://127.0.0.1:4100/posts/html-first');
    await expect(page).toHaveTitle('Start with the HTML — Next.go');
    await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', 'https://example.com/posts/html-first');
    await expect(page.locator('meta[property="og:site_name"]')).toHaveAttribute('content', 'Next.go field notes');
    await expect(page.locator('meta[property="og:type"]')).toHaveAttribute('content', 'article');
    await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', 'Start with the HTML');
    await expect(page.locator('meta[name="description"]')).toHaveCount(1);
    await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', 'A useful page should arrive before its runtime.');
  } finally { await context.close(); }
});

test('Go navigation resolves parent and dynamic metadata once per head and clears it on exit', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.metadataDocument = true; });
  await notes(page).click();
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', 'https://example.com/posts');
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveTitle('Start with the HTML — Next.go');
  await expect(page.locator('link[rel="canonical"]')).toHaveCount(1);
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', 'https://example.com/posts/html-first');
  await expect(page.locator('meta[property="og:site_name"]')).toHaveCount(1);
  await expect(page.locator('meta[property="og:site_name"]')).toHaveAttribute('content', 'Next.go field notes');
  await page.getByRole('link', { name: '← All field notes' }).click();
  await page.getByRole('link', { name: /Small islands, explicit state/ }).click();
  await expect(page).toHaveTitle('Small islands, explicit state — Next.go');
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', 'Small islands, explicit state');
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', 'https://example.com/posts/go-islands');
  await page.goBack(); await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('meta[property="og:title"]')).toHaveCount(0);
  await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  await expect(page.locator('meta[property="og:site_name"]')).toHaveCount(0);
  await expect(page.locator('link[rel="canonical"]')).toHaveCount(0);
  expect(await page.evaluate(() => window.metadataDocument)).toBe(true);
});

test('document language, RTL, themed viewport and title template are present before WASM', async ({ browser }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  try {
    const page = await context.newPage();
    await page.goto('http://127.0.0.1:4100/locale?lang=ar');
    await expect(page).toHaveTitle('مرحبا — Next.go');
    await expect(page.locator('html')).toHaveAttribute('lang', 'ar');
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('مرحبا');
    await expect(page.locator('header')).toHaveAttribute('lang', 'en');
    await expect(page.locator('meta[name="viewport"]')).toHaveCount(1);
    await expect(page.locator('meta[name="viewport"]')).toHaveAttribute('content', /viewport-fit=cover/);
    await expect(page.locator('meta[name="theme-color"]')).toHaveCount(2);
    await expect(page.locator('meta[name="theme-color"][media]')).toHaveAttribute('media', '(prefers-color-scheme: dark)');
  } finally { await context.close(); }
});

test('Go navigation and history update and clear language, direction, viewport and theme metadata', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  await page.evaluate(() => { window.documentControlsPage = true; });
  await page.getByRole('link', { name: 'Document language and viewport', exact: true }).click();
  await expect(page).toHaveTitle('Bonjour — Next.go');
  await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
  const languages = page.getByRole('navigation', { name: 'Document language' });
  await expect(languages.locator('[aria-current="page"]')).toHaveCount(1);
  await languages.getByRole('link', { name: 'العربية', exact: true }).click();
  await expect(page).toHaveTitle('مرحبا — Next.go');
  await expect(page.locator('html')).toHaveAttribute('lang', 'ar');
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  await expect(languages.getByRole('link', { name: 'العربية', exact: true })).toHaveAttribute('aria-current', 'page');
  await expect(languages.locator('[aria-current="page"]')).toHaveCount(1);
  await page.goBack(); await expect(page).toHaveTitle('Bonjour — Next.go');
  await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  await architecture(page).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await expect(page.locator('html')).toHaveAttribute('dir', 'ltr');
  await expect(page.locator('meta[name="viewport"]')).toHaveAttribute('content', 'width=device-width,initial-scale=1');
  await expect(page.locator('meta[name="theme-color"]')).toHaveCount(0);
  await expect(page.locator('meta[name="color-scheme"]')).toHaveCount(0);
  expect(await page.evaluate(() => window.documentControlsPage)).toBe(true);
});

for (const variant of ['document', 'head-viewport', 'named-viewport']) {
  test(`malformed ${variant} cannot replace current document controls or poison a retry`, async ({ page }) => {
    await page.goto('/features'); await ready(page);
    await page.route('**/locale?lang=fr', async route => {
      if (route.request().headers()['x-nextgo-navigate'] !== '1') return route.continue();
      const response = await route.fetch(); const json = await response.json();
      if (variant === 'document') json.document.dir = 'sideways';
      if (variant === 'head-viewport') json.head.push({ tag: 'meta', attrs: { name: 'viewport', content: 'width=1' } });
      if (variant === 'named-viewport') json.meta.viewport = 'width=1';
      await route.fulfill({ response, json });
    });
    await page.getByRole('link', { name: 'Document language and viewport', exact: true }).click();
    await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
    await expect(page).toHaveTitle('Framework features — Next.go');
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    await expect(page.locator('meta[name="viewport"]')).toHaveAttribute('content', 'width=device-width,initial-scale=1');
    await page.unroute('**/locale?lang=fr');
    await page.getByRole('link', { name: 'Document language and viewport', exact: true }).click();
    await expect(page).toHaveTitle('Bonjour — Next.go');
    await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  });
}
