import {test, expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';
import {holdRequests, holdPage, installStreamHold} from './link-status-gates.mjs';

const ready = async page => {
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  await expect(page.locator('[data-nextgo-island="lazylinkhint"]')).toHaveAttribute('data-nextgo-hydrated', 'true');
};
const menu = (page, kind) => page.getByRole('navigation', {name: `${kind} link feedback`, exact: true});
const link = (page, kind, index) => menu(page, kind).locator(`[data-link-hint-link="${index}"]`);
const pending = (page, kind, index, value) => expect(link(page, kind, index)).toHaveAttribute('data-link-pending', String(value));
async function open(page) { await page.goto('/link-status'); await ready(page); await page.evaluate(() => { window.linkStatusDocument = true; }); }
async function closeAll(...owned) {
  const outcomes = await Promise.allSettled(owned.map(item => item.close()));
  const failures = outcomes.filter(outcome => outcome.status === 'rejected');
  if (failures.length) throw new AggregateError(failures.map(outcome => outcome.reason), 'link-status fixture cleanup failed');
}
async function captureSource(page, kind, index, key = 'source') {
  await link(page, kind, index).evaluate((anchor, {key, kind}) => {
    window.linkCaptures ||= {};
    const root = anchor.closest('[data-nextgo-island]'), hint = anchor.querySelector('[data-link-hint]');
    const capture = {kind, root, anchor, hint, changes: [], overflow: false};
    capture.collect = records => {
      for (const record of records) {
        if (capture.changes.length >= 64) { capture.overflow = true; break; }
        capture.changes.push({before: record.oldValue, after: anchor.getAttribute('data-link-pending')});
      }
    };
    capture.observer = new MutationObserver(capture.collect);
    capture.observer.observe(anchor, {attributes: true, attributeFilter: ['data-link-pending'], attributeOldValue: true});
    window.linkCaptures[key] = capture;
  }, {key, kind});
}
async function sameSource(page, key = 'source') {
  expect(await page.evaluate(key => {
    const saved = window.linkCaptures[key];
    const root = document.querySelector(`[data-nextgo-island="${saved.kind === 'Eager' ? 'linkhint' : 'lazylinkhint'}"]`);
    return saved.root.isConnected && saved.anchor.isConnected && saved.hint.isConnected && saved.root === root && saved.anchor === root.querySelector(`[data-link-hint-link="${saved.anchor.getAttribute('data-link-hint-link')}"]`) && saved.hint === saved.anchor.querySelector('[data-link-hint]');
  }, key)).toBe(true);
}
async function noPendingPublication(page, key = 'source') {
  const value = await page.evaluate(key => {
    const saved = window.linkCaptures[key]; saved.collect(saved.observer.takeRecords());
    return {overflow: saved.overflow, changes: saved.changes, current: saved.anchor.getAttribute('data-link-pending')};
  }, key);
  expect(value.overflow).toBe(false);
  expect(value.current).toBe('false');
  expect(value.changes.some(change => change.before === 'true' || change.after === 'true')).toBe(false);
}

test('meaningful ordinary links render before WASM and eager/lazy hydration preserves actual anchors and hints', async ({page}) => {
  const wasm = await holdRequests(page, '**/app.wasm', () => true);
  try {
    await page.goto('/link-status', {waitUntil: 'domcontentloaded'}); await wasm.waitForEntry();
    await expect(page.getByRole('heading', {name: 'Choose a page', exact: true})).toBeVisible();
    for (const kind of ['Eager', 'Lazy']) {
      await expect(link(page, kind, 0)).toHaveAttribute('href', '/link-status/one');
      await captureSource(page, kind, 0, kind);
    }
    wasm.release(); await ready(page);
    for (const kind of ['Eager', 'Lazy']) await sameSource(page, kind);
  } finally { await wasm.close(); }
});

for (const kind of ['Eager', 'Lazy']) {
  test(`${kind} tracks only the clicked anchor through a retained-layout commit`, async ({page}) => {
    await open(page); await captureSource(page, kind, 0);
    const held = await holdPage(page, '/link-status/one');
    try {
      await link(page, kind, 0).click(); await held.waitForEntry();
      await pending(page, kind, 0, true); await pending(page, kind, 1, false);
      await pending(page, kind === 'Eager' ? 'Lazy' : 'Eager', 0, false);
      held.release(); await expect(page).toHaveURL(/\/link-status\/one$/);
      await expect(page.getByRole('heading', {name: 'First link page', exact: true})).toBeVisible();
      await pending(page, kind, 0, false); await sameSource(page);
      expect(await page.evaluate(() => window.linkStatusDocument)).toBe(true);
    } finally { await held.close(); }
  });

  test(`${kind} latest click owns pending and older completion cannot clear it`, async ({page}) => {
    await open(page);
    const first = await holdPage(page, '/link-status/one'), second = await holdPage(page, '/link-status/two');
    try {
      await link(page, kind, 0).click(); await first.waitForEntry(); await pending(page, kind, 0, true);
      await link(page, kind, 2).click(); await second.waitForEntry();
      await pending(page, kind, 0, false); await pending(page, kind, 2, true);
      first.release(); await first.close(); await pending(page, kind, 2, true);
      second.release(); await expect(page).toHaveURL(/\/link-status\/two$/); await pending(page, kind, 2, false);
    } finally { await closeAll(first, second); }
  });

  test(`${kind} network failure settles its hint, retains the view and permits retry`, async ({page}) => {
    await open(page);
    const held = await holdPage(page, '/link-status/one', true);
    try {
      await link(page, kind, 0).click(); await held.waitForEntry(); await pending(page, kind, 0, true);
      held.release(); await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
      await pending(page, kind, 0, false); await expect(page).toHaveURL(/\/link-status$/);
    } finally { await held.close(); }
    await link(page, kind, 0).click(); await expect(page).toHaveURL(/\/link-status\/one$/); await pending(page, kind, 0, false);
  });

  test(`${kind} a proven complete cache hit skips pending without another page request`, async ({page}, info) => {
    await open(page);
    // The native public cache is shared across engines. Give this case a fresh
    // full-URI generation, then reuse that exact anchor/target without clearing it.
    const target = '/link-status/one?link-cache=' + encodeURIComponent(`${info.project.name}-${kind}-${randomUUID()}`);
    const absoluteTarget = new URL(target, page.url()).href;
    await link(page, kind, 0).evaluate((node, href) => node.setAttribute('href', href), target);
    await captureSource(page, kind, 0, 'cacheTarget');
    let requests = 0; const responses = [];
    const isFirst = request => request.url() === absoluteTarget && request.headers()['x-nextgo-navigate'] === '1';
    const onRequest = request => { if (isFirst(request)) requests++; };
    const onResponse = response => { if (isFirst(response.request())) responses.push(response); };
    page.on('request', onRequest); page.on('response', onResponse);
    try {
      const firstNavigationStartedAt = performance.now();
      await link(page, kind, 0).click(); await expect(page).toHaveURL(absoluteTarget); await pending(page, kind, 0, false);
      await sameSource(page, 'cacheTarget'); await expect(link(page, kind, 0)).toHaveAttribute('href', target);
      await expect.poll(() => responses.length, {message: 'real public navigation response observed'}).toBe(1);
      const first = responses[0], headers = await first.allHeaders();
      expect(first.status()).toBe(200); expect(headers['content-type']).toMatch(/^application\/json(?:;|$)/);
      expect(headers['x-nextgo-prefetch']).toBe('public'); expect(headers['cache-control']).toContain('public');
      const controls = headers['cache-control'].split(',').map(value => value.trim().toLowerCase());
      expect(controls).toContain('public');
      expect(controls.some(value => /^(?:private|no-store|no-cache)(?:=|$)/.test(value))).toBe(false);
      expect((headers.vary || '').split(',').map(value => value.trim())).not.toContain('*');
      for (const forbidden of ['set-cookie', 'x-nextgo-stale-millis', 'x-nextgo-shell', 'x-nextgo-shell-protocol', 'x-nextgo-partial', 'x-nextgo-request-regions']) expect(Object.hasOwn(headers, forbidden), forbidden).toBe(false);
      const maxAges = controls.filter(value => /^max-age(?:=|$)/.test(value));
      expect(maxAges).toHaveLength(1); expect(maxAges[0]).toMatch(/^max-age=\d+$/); expect(headers.age).toMatch(/^\d+$/);
      const maxAge = Number(maxAges[0].slice('max-age='.length)), age = Number(headers.age);
      expect(Number.isSafeInteger(maxAge) && maxAge > 0 && Number.isSafeInteger(age)).toBe(true);
      // Match the legacy browser's 30-second cap and Age deduction, including
      // conservative elapsed time from before the click. Public alone is insufficient.
      const remainingMillis = () => (Math.min(30, maxAge) - age) * 1000 - (performance.now() - firstNavigationStartedAt);
      expect(remainingMillis()).toBeGreaterThan(10_000);
      await link(page, kind, 2).click(); await expect(page).toHaveURL(/\/link-status\/two$/); await pending(page, kind, 2, false);
      await sameSource(page, 'cacheTarget'); await expect(link(page, kind, 0)).toHaveAttribute('href', target);
      await captureSource(page, kind, 0); const before = requests;
      expect(remainingMillis()).toBeGreaterThan(10_000);
      await link(page, kind, 0).click(); await expect(page).toHaveURL(absoluteTarget); await pending(page, kind, 0, false);
      expect(requests).toBe(before); await noPendingPublication(page); await sameSource(page);
      await sameSource(page, 'cacheTarget'); await expect(link(page, kind, 0)).toHaveAttribute('href', target);
      expect(await page.evaluate(() => window.linkStatusDocument)).toBe(true);
    } finally { page.off('request', onRequest); page.off('response', onResponse); }
  });

  test(`${kind} target and modified native clicks never select a pending link`, async ({page, context}) => {
    await open(page); await captureSource(page, kind, 0);
    const popups = []; const onPage = popup => popups.push(popup); context.on('page', onPage);
    try {
      await link(page, kind, 0).evaluate(node => node.setAttribute('target', '_blank'));
      await link(page, kind, 0).click(); await expect.poll(() => popups.length, {message: 'native target opened its page'}).toBe(1);
      await expect(popups[0]).toHaveURL(/\/link-status\/one$/); await popups[0].close();
      await expect(page).toHaveURL(/\/link-status$/); await noPendingPublication(page); await sameSource(page);
      await link(page, kind, 0).evaluate(node => node.removeAttribute('target'));
      await link(page, kind, 0).click({modifiers: ['ControlOrMeta']});
      await expect.poll(() => popups.length, {message: 'native modified click opened its page'}).toBe(2);
      await expect(popups[1]).toHaveURL(/\/link-status\/one$/);
      await expect(page).toHaveURL(/\/link-status$/); await noPendingPublication(page); await sameSource(page);
    } finally { context.off('page', onPage); await Promise.all(popups.filter(popup => !popup.isClosed()).map(popup => popup.close())); }
  });

  test(`${kind} unmount removes the observer before its old anchor is reconnected and clicked`, async ({page}) => {
    await open(page); await captureSource(page, kind, 0);
    await page.getByRole('navigation', {name: 'Main', exact: true}).getByRole('link', {name: 'Architecture', exact: true}).click();
    await expect(page).toHaveURL(/\/about$/);
    await expect(page.getByRole('heading', {level: 1})).toContainText('A little framework.');
    expect(await page.evaluate(() => window.linkCaptures.source.root.isConnected)).toBe(false);
    const held = await holdPage(page, '/link-status/one');
    try {
      // Reconnect inside the actual page content that navigation replaces.
      // Outer main and its owned outlet intentionally survive shared layouts.
      const placement = await page.evaluate(() => {
        const saved = window.linkCaptures.source;
        const articles = document.querySelectorAll('#nextgo-app article.prose');
        if (articles.length !== 1) throw new Error('Expected the actual About page article');
        const article = articles[0], outlet = article.closest('nextgo-outlet[data-nextgo-outlet]');
        const layout = outlet?.closest('nextgo-layout[data-nextgo-layout]');
        if (!outlet || !layout || !outlet.isConnected || !outlet.contains(article) || !outlet.getAttribute('data-nextgo-outlet') || outlet.getAttribute('data-nextgo-outlet') !== layout.getAttribute('data-nextgo-layout')) throw new Error('About article must belong to its actual layout outlet');
        saved.disposalArticle = article; saved.disposalOutlet = outlet; saved.disposalLayout = layout;
        article.append(saved.root);
        return {parentIsArticle: saved.root.parentNode === article, insideOutlet: outlet.contains(saved.root), nearestOutlet: saved.root.closest('nextgo-outlet[data-nextgo-outlet]') === outlet, connected: saved.root.isConnected};
      });
      expect(placement).toEqual({parentIsArticle: true, insideOutlet: true, nearestOutlet: true, connected: true});
      // Preserve the disposed hydrated nodes; do not mount a replacement scope.
      await page.evaluate(() => {
        const saved = window.linkCaptures.source;
        if (saved.root.parentNode !== saved.disposalArticle || !saved.disposalOutlet.contains(saved.root)) throw new Error('Disposed fixture left its replaceable outlet before click');
        saved.anchor.click();
      });
      await held.waitForEntry();
      await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
      await noPendingPublication(page);
      expect(await page.evaluate(() => window.linkCaptures.source.hint.textContent)).toBe('');
      held.release(); await expect(page).toHaveURL(/\/link-status\/one$/);
      await noPendingPublication(page);
      expect(await page.evaluate(() => window.linkCaptures.source.root.isConnected)).toBe(false);
      expect(await page.evaluate(() => {
        const saved = window.linkCaptures.source;
        return {articleConnected: saved.disposalArticle.isConnected, outletConnected: saved.disposalOutlet.isConnected, layoutConnected: saved.disposalLayout.isConnected, oldRootStillInsideOutlet: saved.disposalOutlet.contains(saved.root)};
      })).toEqual({articleConnected: false, outletConnected: true, layoutConnected: true, oldRootStillInsideOutlet: false});
    } finally { await held.close(); }
  });
}

test('a fragment cancels the pending source without selecting a new link', async ({page}) => {
  await open(page); const held = await holdPage(page, '/link-status/one');
  try {
    await link(page, 'Eager', 0).click(); await held.waitForEntry(); await pending(page, 'Eager', 0, true);
    await link(page, 'Lazy', 1).evaluate(node => node.setAttribute('href', '#main')); await link(page, 'Lazy', 1).click();
    await expect(page).toHaveURL(/\/link-status#main$/); await pending(page, 'Eager', 0, false); await pending(page, 'Lazy', 1, false);
    held.release(); await held.close(); await expect(page).toHaveURL(/\/link-status#main$/);
  } finally { await held.close(); }
});

test('a genuine streamed shell settles its link before deferred content completes', async ({page}) => {
  await open(page);
  const stream = await installStreamHold(page, '/link-status/stream'), held = await holdPage(page, '/link-status/stream');
  try {
    await link(page, 'Lazy', 3).click(); await held.waitForEntry(); await pending(page, 'Lazy', 3, true); held.release();
    await expect(page).toHaveURL(/\/link-status\/stream$/); await pending(page, 'Lazy', 3, false);
    await expect(page.locator('[data-link-content-pending]')).toBeVisible(); await expect(page.locator('[data-link-content-ready]')).toHaveCount(0);
    const evidence = await page.evaluate(() => ({shell: window.linkStreamHold.state.shell, build: document.body.getAttribute('data-nextgo-build'), configuration: document.body.getAttribute('data-nextgo-configuration')}));
    expect(evidence.shell).toMatchObject({type: 'shell', version: 1, pageVersion: 3, status: 200, title: 'Deferred link page — Next.go', protocolHeader: '3', build: evidence.build, configuration: evidence.configuration, configurationHeader: evidence.configuration});
    expect(new URL(evidence.shell.url, page.url()).pathname).toBe('/link-status/stream');
    await stream.release(); await expect(page.locator('[data-link-content-ready]')).toBeVisible(); await pending(page, 'Lazy', 3, false);
    await expect.poll(() => page.evaluate(() => { const state = window.linkStreamHold.state; return state.done && state.eof && state.releasedLock && !state.canceled; }), {message: 'genuine stream reached EOF and released reader'}).toBe(true);
  } finally { await closeAll(held, stream); }
});
