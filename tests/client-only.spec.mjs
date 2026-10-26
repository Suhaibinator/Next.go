import {test as base, expect} from '@playwright/test';
import {startMetadataStream} from './serve-metadata-stream.mjs';
import {holdRequests} from './link-status-gates.mjs';

// The existing fixture starts a pinned production executable and forwards its
// actual responses. This route never calls its optional JSON data source.
const test = base.extend({
  native: async ({page}, use, info) => {
    const startupAbort = new AbortController();
    const onPageClose = () => startupAbort.abort(new Error('Owned client-only page closed during startup'));
    page.once('close', onPageClose);
    let fixture;
    try {fixture = await startMetadataStream({signal: startupAbort.signal});}
    catch (error) {if (error.metadataFixtureProof) await info.attach('client-only-startup-failure-proof', {body: JSON.stringify(error.metadataFixtureProof), contentType: 'application/json'}); throw error;}
    finally {page.off('close', onPageClose);}
    try {await use(fixture);} finally {
      const errors = [];
      try {await page.close();} catch (error) {errors.push(error);}
      try {await fixture.close();} catch (error) {errors.push(error);}
      const proof = fixture.snapshot();
      await info.attach('client-only-native-process-and-wire-proof', {body: JSON.stringify(proof), contentType: 'application/json'});
      expect(proof.process.closed && proof.process.joined).toBe(true);
      expect(proof.process.forced || proof.process.unexpectedExit).toBe(false);
      expect(proof.process.termSent).toBe(true); expect(proof.process.code).toBe(0); expect(proof.process.signal).toBe(null);
      expect(proof.gatewayListening || proof.sourceListening).toBe(false); expect(proof.httpJoined).toBe(true);
      expect(proof.upstreams).toBe(0); expect(proof.sockets).toBe(0);
      expect(proof.listeners).toHaveLength(2);
      expect(proof.listeners.every(listener => listener.bindJoined && listener.closed && !listener.listening && !listener.failedBind && listener.errors.length === 0)).toBe(true);
      expect(proof.source).toEqual([]); expect(proof.wire.filter(record => record.observerError)).toEqual([]);
      if (errors.length) throw new AggregateError(errors, 'client-only owned cleanup failed');
    }
  },
});
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const root = (page, kind, index = 0) => page.locator(`[data-nextgo-island="${kind === 'eager' ? 'clientpanel' : 'clientpanellazy'}"]`).nth(index);
const count = (page, kind, index = 0) => root(page, kind, index).locator('[data-client-panel-count]');
const increment = (page, kind, index = 0) => root(page, kind, index).locator('[data-client-panel-increment]');
const companion = page => page.getByRole('region', {name: 'Hydrated companion', exact: true});
const moduleURL = /\/module-[a-f0-9]{64}\.wasm(?:\?.*)?$/;
async function closeAll(...gates) {
  const outcomes = await Promise.allSettled(gates.map(gate => gate.close()));
  const errors = outcomes.filter(outcome => outcome.status === 'rejected');
  if (errors.length) throw new AggregateError(errors.map(outcome => outcome.reason), 'client-only request gates failed');
}
async function captureFallbacks(page) {
  await page.evaluate(() => {
    globalThis.clientBoundaryDocument = document;
    globalThis.clientBoundaryOriginals = [...document.querySelectorAll('[data-nextgo-island="clientpanel"], [data-nextgo-island="clientpanellazy"]')].map(root => ({root, fallback: root.querySelector('[data-client-panel-fallback]'), input: root.querySelector('[data-client-panel-draft]')}));
    const companion = [...document.querySelectorAll('[aria-label="Hydrated companion"]')][0];
    globalThis.clientBoundaryCompanion = {root: companion.closest('[data-nextgo-island]'), output: companion.querySelector('[data-count]'), button: companion.querySelector('[data-increment]')};
  });
}

test('delayed WASM and lazy bytes preserve useful fallbacks while Go mounts explicit views inside stable boundaries', async ({page, native}) => {
  const core = await holdRequests(page, '**/app.wasm', () => true);
  const lazy = await holdRequests(page, moduleURL, () => true);
  try {
    await page.goto(native.origin + '/client-only', {waitUntil: 'domcontentloaded'}); await core.waitForEntry();
    await expect(page.getByRole('heading', {level: 1})).toHaveText('Choose browser rendering explicitly.');
    await expect(page.locator('[data-client-panel-fallback]')).toHaveCount(4);
    await expect(page.locator('[data-client-panel]')).toHaveCount(0);
    await expect(companion(page).locator('[data-count]')).toHaveText('2'); await captureFallbacks(page);
    core.release(); await lazy.waitForEntry();
    await expect(root(page, 'eager')).toHaveAttribute('data-nextgo-hydrated', 'true');
    await expect(root(page, 'eager', 1)).toHaveAttribute('data-nextgo-hydrated', 'true');
    await expect(root(page, 'lazy').locator('[data-client-panel-fallback]')).toBeVisible();
    await expect(root(page, 'lazy', 1).locator('[data-client-panel-fallback]')).toBeVisible();
    expect(await page.evaluate(() => clientBoundaryOriginals.every(saved => saved.root.isConnected) && clientBoundaryOriginals.slice(0, 2).every(saved => !saved.fallback.isConnected) && clientBoundaryOriginals.slice(2).every(saved => saved.fallback.isConnected))).toBe(true);
    await expect(count(page, 'eager')).toHaveText('3'); await increment(page, 'eager').click(); await expect(count(page, 'eager')).toHaveText('4');
    await expect(count(page, 'eager', 1)).toHaveText('7');
    await companion(page).locator('[data-increment]').click(); await expect(companion(page).locator('[data-count]')).toHaveText('3');
    lazy.release(); await ready(page); await expect(page.locator('[data-client-panel-fallback]')).toHaveCount(0);
    await expect(count(page, 'lazy')).toHaveText('5'); await increment(page, 'lazy').click(); await expect(count(page, 'lazy')).toHaveText('6');
    await expect(count(page, 'lazy', 1)).toHaveText('9');
    expect(await page.evaluate(() => {
      const current = [...document.querySelectorAll('[data-nextgo-island="clientpanel"], [data-nextgo-island="clientpanellazy"]')];
      const ordinary = document.querySelector('[aria-label="Hydrated companion"]');
      return clientBoundaryDocument === document && clientBoundaryOriginals.every((saved, index) => saved.root === current[index] && !saved.fallback.isConnected && !saved.input.isConnected) && clientBoundaryCompanion.root === ordinary.closest('[data-nextgo-island]') && clientBoundaryCompanion.output === ordinary.querySelector('[data-count]') && clientBoundaryCompanion.button === ordinary.querySelector('[data-increment]');
    })).toBe(true);
  } finally {await closeAll(core, lazy);}
});

for (const failure of ['error', 'panic']) test(`${failure} after eager and lazy setup restores exact fallback nodes, drafts and focus and removes new listeners`, async ({page, native}) => {
  const core = await holdRequests(page, '**/app.wasm', () => true);
  const lazy = await holdRequests(page, moduleURL, () => true);
  try {
    await page.goto(native.origin + '/client-only?fail=' + failure, {waitUntil: 'domcontentloaded'}); await core.waitForEntry(); await captureFallbacks(page);
    await page.evaluate(() => {
      globalThis.clientBoundaryRollbacks = clientBoundaryOriginals.filter(saved => JSON.parse(saved.root.getAttribute('data-nextgo-props')).failure).map(saved => {
        const capture = {...saved, views: [], overflow: false};
        capture.collect = records => {
          for (const record of records) for (const node of record.addedNodes) {
            if (node.nodeType !== 1) continue;
            const view = node.matches('[data-client-panel]') ? node : node.querySelector('[data-client-panel]');
            if (!view || capture.views.includes(view)) continue;
            if (capture.views.length >= 4) {capture.overflow = true; continue;}
            capture.views.push(view);
          }
        };
        capture.observer = new MutationObserver(capture.collect); capture.observer.observe(saved.root, {childList: true, subtree: true});
        return capture;
      });
    });
    const eagerInput = root(page, 'eager').locator('[data-client-panel-draft]');
    await eagerInput.fill('Retained eager fallback draft'); await eagerInput.focus(); core.release(); await lazy.waitForEntry();
    await expect(root(page, 'eager')).toHaveAttribute('data-nextgo-hydrated', 'error');
    await expect(eagerInput).toHaveValue('Retained eager fallback draft'); await expect(eagerInput).toBeFocused();
    await expect(count(page, 'eager', 1)).toHaveText('7');
    const lazyInput = root(page, 'lazy').locator('[data-client-panel-draft]');
    await lazyInput.fill('Retained lazy fallback draft'); await lazyInput.focus(); lazy.release();
    await expect(root(page, 'lazy')).toHaveAttribute('data-nextgo-hydrated', 'error');
    await expect(lazyInput).toHaveValue('Retained lazy fallback draft'); await expect(lazyInput).toBeFocused();
    await expect(root(page, 'lazy', 1)).toHaveAttribute('data-nextgo-hydrated', 'true'); await expect(count(page, 'lazy', 1)).toHaveText('9');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'error');
    await expect(page.locator('#nextgo-status')).toContainText(failure === 'error' ? 'client panel demonstration rejected its mount' : 'client panel demonstration mount panic');
    expect(await page.evaluate(() => clientBoundaryRollbacks.every(saved => {
      saved.collect(saved.observer.takeRecords()); saved.observer.disconnect();
      if (saved.overflow || saved.views.length !== 1 || saved.fallback !== saved.root.querySelector('[data-client-panel-fallback]') || saved.input !== saved.root.querySelector('[data-client-panel-draft]') || !saved.fallback.isConnected) return false;
      const inserted = saved.views[0], output = inserted.querySelector('[data-client-panel-count]'), button = inserted.querySelector('[data-client-panel-increment]');
      if (inserted.isConnected || !output || !button) return false;
      const before = output.textContent; button.click(); return output.textContent === before;
    }))).toBe(true);
    await increment(page, 'eager', 1).click(); await expect(count(page, 'eager', 1)).toHaveText('8');
    await increment(page, 'lazy', 1).click(); await expect(count(page, 'lazy', 1)).toHaveText('10');
  } finally {await closeAll(core, lazy);}
});

test('Go navigation disposes eager and lazy browser-view listeners and remounts independent state on return', async ({page, native}) => {
  let modules = 0; page.on('request', request => {if (moduleURL.test(request.url())) modules++;});
  await page.goto(native.origin + '/client-only'); await ready(page);
  await increment(page, 'eager').click(); await expect(count(page, 'eager')).toHaveText('4');
  await increment(page, 'lazy').click(); await expect(count(page, 'lazy')).toHaveText('6');
  await page.evaluate(() => {
    globalThis.clientBoundaryDocument = document;
    globalThis.clientBoundaryDetached = [...document.querySelectorAll('[data-nextgo-island="clientpanel"], [data-nextgo-island="clientpanellazy"]')].map(root => ({root, output: root.querySelector('[data-client-panel-count]'), button: root.querySelector('[data-client-panel-increment]')}));
  });
  await page.locator('[data-client-only-exit]').click(); await expect(page).toHaveTitle('Architecture — Next.go'); await ready(page);
  expect(await page.evaluate(() => clientBoundaryDocument === document && clientBoundaryDetached.every(saved => {const before = saved.output.textContent; saved.button.click(); return !saved.root.isConnected && saved.output.textContent === before;}))).toBe(true);
  await page.goBack(); await expect(page).toHaveTitle('Explicit client-only views — Next.go'); await ready(page);
  await expect(count(page, 'eager')).toHaveText('3'); await expect(count(page, 'eager', 1)).toHaveText('7');
  await expect(count(page, 'lazy')).toHaveText('5'); await expect(count(page, 'lazy', 1)).toHaveText('9'); expect(modules).toBe(1);
  expect(await page.evaluate(() => clientBoundaryDocument === document && clientBoundaryDetached.every(saved => !saved.root.isConnected))).toBe(true);
});

test('a superseded lazy download never replaces detached fallbacks and the returned page mounts normally', async ({page, native}) => {
  const lazy = await holdRequests(page, moduleURL, () => true);
  try {
    await page.goto(native.origin + '/client-only'); await lazy.waitForEntry();
    await expect(root(page, 'eager')).toHaveAttribute('data-nextgo-hydrated', 'true');
    await expect(root(page, 'lazy')).toHaveAttribute('data-nextgo-hydrated', 'loading');
    await page.evaluate(() => {
      globalThis.clientBoundaryDocument = document;
      globalThis.clientBoundaryAbandoned = [...document.querySelectorAll('[data-nextgo-island="clientpanellazy"]')].map(root => ({root, fallback: root.querySelector('[data-client-panel-fallback]'), input: root.querySelector('[data-client-panel-draft]')}));
    });
    await page.locator('[data-client-only-exit]').click(); await expect(page).toHaveTitle('Architecture — Next.go'); await ready(page);
    lazy.release(); await page.goBack(); await expect(page).toHaveTitle('Explicit client-only views — Next.go'); await ready(page);
    await expect(count(page, 'lazy')).toHaveText('5'); await increment(page, 'lazy').click(); await expect(count(page, 'lazy')).toHaveText('6');
    expect(await page.evaluate(() => clientBoundaryDocument === document && clientBoundaryAbandoned.every(saved => !saved.root.isConnected && saved.root.querySelector('[data-client-panel-fallback]') === saved.fallback && saved.root.querySelector('[data-client-panel-draft]') === saved.input && !saved.root.querySelector('[data-client-panel]')))).toBe(true);
  } finally {await lazy.close();}
});
