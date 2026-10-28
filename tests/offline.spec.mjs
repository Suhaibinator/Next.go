import {test as base, expect, chromium, firefox, webkit} from '@playwright/test';
import {mkdir, rm} from 'node:fs/promises';
import {join} from 'node:path';
import {readOfflineArtifacts, startOfflineFixture} from './offline-fixture.mjs';
import {observeOfflineRegistrationProof} from './offline-registration-proof-diagnostics.mjs';
import {observeOfflineUpdateFailure} from './offline-update-failure-observation.mjs';

const browserTypes = {chromium, firefox, webkit};
const controls = page => page.locator('[data-offline-controls]');
const counter = page => page.locator('[data-offline-count]');
const ready = async page => {
  await expect(controls(page)).toHaveAttribute('data-offline-controls-ready', 'true');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  await expect(page.locator('[data-nextgo-island="counter"]')).toHaveAttribute('data-nextgo-hydrated', 'true');
  await expect(page.locator('[data-offline-counter-proof]')).toHaveText('Hydrated by lazy Go');
  await expect(page.locator('[data-offline-increment]')).toBeEnabled();
};
async function cachedEntries(page) {
  return page.evaluate(async () => {
    const result = [];
    for (const name of (await caches.keys()).sort()) {
      const cache = await caches.open(name);
      result.push({name, urls: (await cache.keys()).map(request => request.url).sort()});
    }
    return result;
  });
}
async function ownedAccounting(page) {
  return page.evaluate(async () => {
    const names = (await caches.keys()).filter(name => /^nextgo-offline-v1-[a-f0-9]{64}(?:-meta|-g-[a-f0-9]{64})$/.test(name));
    let index = null;
    const generations = [];
    for (const name of names) {
      const cache = await caches.open(name), requests = await cache.keys();
      if (name.endsWith('-meta')) {
        const key = requests.find(request => new URL(request.url).pathname.endsWith('/__nextgo_offline_owner__'));
        if (!key) throw new Error('Owned metadata cache has no owner index');
        const response = await cache.match(key), text = await response.text();
        if (text.length > 16 * 1024) throw new Error('Owner index observation exceeds bound');
        const value = JSON.parse(text);
        index = {active: value.active, previous: value.previous, staging: value.staging, disabled: value.disabled, retainedClients: value.retained?.length || 0};
      } else {
        let bytes = 0;
        for (const request of requests) {const response = await cache.match(request); bytes += (await response.arrayBuffer()).byteLength;}
        generations.push({generation: name.slice(-64), entries: requests.length, physicalBytes: bytes});
      }
    }
    generations.sort((a, b) => a.generation.localeCompare(b.generation));
    return {index, generations};
  });
}
async function verifySavedPayloads(page, generation) {
  return page.evaluate(async generation => {
    const descriptor = JSON.parse(document.body.getAttribute('data-nextgo-offline'));
    const response = await fetch(descriptor.catalogURL, {cache: 'no-store'});
    if (!response.ok) throw new Error('Actual native catalog unavailable');
    const catalog = await response.json();
    if (catalog.Version !== 1 || catalog.Config.Origin !== location.origin) throw new Error('Unexpected native catalog binding');
    const expected = [
      ...catalog.Assets.map(asset => ({url: new URL(asset.URL, location.origin).href, sha256: asset.SHA256, bytes: asset.DecodedBytes})),
      ...catalog.Documents.map(document => ({url: new URL(catalog.Config.BasePath + document.Path, location.origin).href, sha256: document.SHA256, bytes: document.DecodedBytes})),
    ];
    if (!expected.length || expected.length > 192) throw new Error('Unexpected catalog entry bound');
    const names = await caches.keys(), actual = [];
    for (const entry of expected) {
      let matched;
      for (const name of names) {
        const cache = await caches.open(name), response = await cache.match(entry.url);
        if (response) {if (matched) throw new Error('Multiple saved payloads for one generation URL'); matched = response;}
      }
      if (!matched) throw new Error('Required native catalog payload is absent: ' + entry.url);
      const bytes = await matched.arrayBuffer();
      const sha256 = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(value => value.toString(16).padStart(2, '0')).join('');
      if (bytes.byteLength !== entry.bytes || sha256 !== entry.sha256) throw new Error('Saved payload does not match actual native catalog: ' + entry.url);
      actual.push({url: entry.url, sha256, bytes: bytes.byteLength});
    }
    return {payloads: actual, preparationPaths: catalog.Documents.map(document => '/_nextgo/offline/documents/' + generation + '/' + document.SHA256)};
  }, generation);
}
const test = base.extend({
  shelf: async ({}, use, info) => {
    const fixture = await startOfflineFixture(await readOfflineArtifacts());
    const profile = info.outputPath('private-persistent-profile');
    await mkdir(profile, {recursive: true});
    let context, launches = 0;
    const sessions = [], traces = [];
    const shelf = {
      ...fixture,
      async open() {
        if (context) throw new Error('Close the previous browser process before reopening its profile');
        const variant = info.project.metadata.offlineBrowser;
        const type = variant === 'webkit-touch' ? 'webkit' : variant;
        const options = {headless: true, serviceWorkers: 'allow', viewport: {width: 1100, height: 800}};
        if (variant === 'webkit-touch') Object.assign(options, {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true});
        if (type === 'chromium' && process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH) options.executablePath = process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH;
        context = await browserTypes[type].launchPersistentContext(profile, options);
        await context.tracing.start({screenshots: true, snapshots: true, sources: true});
        await context.addInitScript(() => {globalThis.offlineDocumentToken = crypto.randomUUID();});
        const page = context.pages()[0] || await context.newPage();
        sessions.push({launch: ++launches, variant, profile, pages: context.pages().length});
        return page;
      },
      async closeBrowser() {
        if (!context) return;
        const owned = context; context = undefined;
        const trace = info.outputPath(`persistent-process-${launches}.zip`);
        const publicDocuments = [];
        for (const page of owned.pages()) {
          try {publicDocuments.push(await page.evaluate(async () => {
            const body = document.body, catalogs = [];
            for (const name of await caches.keys()) {
              if (!/^nextgo-offline-v1-[a-f0-9]{64}-g-[a-f0-9]{64}$/.test(name)) continue;
              const cache = await caches.open(name);
              for (const request of await cache.keys()) if (new URL(request.url).pathname.endsWith('/__nextgo_offline_catalog__')) {
                const response = await cache.match(request), bytes = await response.arrayBuffer();
                const sha256 = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(value => value.toString(16).padStart(2, '0')).join('');
                catalogs.push({generation: name.slice(-64), bytes: bytes.byteLength, sha256});
              }
            }
            return {url: location.href, runtime: body?.getAttribute('data-nextgo-build'), hydration: body?.getAttribute('data-nextgo-hydration'), descriptor: body?.getAttribute('data-nextgo-offline'), saved: body?.getAttribute('data-nextgo-offline-saved'), lazyProof: document.querySelector('[data-offline-counter-proof]')?.textContent, catalogs};
          }));} catch (error) {publicDocuments.push({observationError: error.message});}
        }
        sessions.at(-1).publicDocuments = publicDocuments;
        let traceError;
        try {await owned.tracing.stop({path: trace}); traces.push(trace);} catch (error) {traceError = error;}
        const browser = owned.browser();
        await owned.close();
        if (browser) expect(browser.isConnected()).toBe(false);
        sessions.at(-1).closed = true;
        if (traceError) throw traceError;
      },
    };
    try {await use(shelf);} finally {
      if (info.status !== info.expectedStatus && info.project.metadata.offlineBrowser === 'firefox' &&
          info.title === 'a genuine new generation waits while the old client is open and activates only after every scoped client closes') {
        try {await observeOfflineUpdateFailure(context, info);} catch {} // Never replace the original failure.
        try {await observeOfflineRegistrationProof(context, info, fixture.origin);} catch {console.warn('Offline registration diagnostic unavailable before attachment');}
      }
      if (info.status !== info.expectedStatus && info.project.metadata.offlineBrowser === 'firefox' &&
          info.title === 'explicit Go disable reports retained charged payloads until closed clients allow explicit physical reconciliation') {
        try {await observeOfflineRegistrationProof(context, info, fixture.origin);} catch {console.warn('Offline registration diagnostic unavailable before attachment');}
      }
      let closed = false;
      try {
        const errors = [];
        try {await shelf.closeBrowser();} catch (error) {errors.push(error);}
        try {await fixture.close();} catch (error) {errors.push(error);}
        if (errors.length) throw new AggregateError(errors, 'Offline fixture cleanup failed');
        const proof = {sessions, ...fixture.snapshot()};
        expect(proof.nativePID).toBe(null); expect(proof.gatewayListening).toBe(false);
        expect(proof.processes.every(process => process.exited)).toBe(true);
        expect(proof.sessions.every(session => session.closed)).toBe(true);
        await info.attach('offline-artifact-process-wire-proof', {body: JSON.stringify(proof), contentType: 'application/json'});
        closed = true;
      } finally {
        // The profile is reused across full process restarts WITHIN a case.
        // Keep failures/interruption intact; remove only joined successful cases.
        if (closed && info.status === info.expectedStatus) {
          await rm(profile, {recursive: true, force: true});
          await Promise.all(traces.map(path => rm(path, {force: true})));
        } else {
          for (const [n, path] of traces.entries()) await info.attach(`persistent-process-${n + 1}`, {path, contentType: 'application/zip'});
          await info.attach('retained-private-profile-path', {body: profile, contentType: 'text/plain'});
        }
      }
    }
  },
});
async function enable(shelf, page) {
  await page.goto(shelf.origin + '/'); await ready(page);
  await page.locator('[data-offline-enable]').click();
  await expect(controls(page)).toHaveAttribute('data-offline-enabled', 'true');
  await expect(controls(page)).toHaveAttribute('data-offline-active-generation', /^[a-f0-9]{64}$/);
  // Installation deliberately does not claim an already open client.
  await page.reload(); await ready(page);
  const previousCompletion = await controls(page).getAttribute('data-offline-completed-operations');
  expect(previousCompletion).toMatch(/^(0|[1-9][0-9]{0,9})$/);
  expect(BigInt(previousCompletion)).toBeLessThan(1n << 32n);
  const nextCompletion = ((BigInt(previousCompletion) + 1n) % (1n << 32n)).toString();
  await page.locator('[data-offline-status-button]').click();
  // Wait for exactly this operation to settle, not an already-true controller flag.
  await expect(controls(page)).toHaveAttribute('data-offline-completed-operations', nextCompletion);
  const confirmation = await controls(page).evaluate(root => ({
    phase: root.querySelector('[data-offline-status-output]').getAttribute('data-offline-state'),
    stateError: root.getAttribute('data-offline-last-error'),
    operationError: root.getAttribute('data-offline-operation-error'),
    enabled: root.getAttribute('data-offline-enabled'),
    controlling: root.getAttribute('data-offline-controlling'),
  }));
  expect(confirmation).toEqual({phase: 'ready', stateError: '', operationError: '', enabled: 'true', controlling: 'true'});
  await expect(controls(page)).toHaveAttribute('data-offline-controlling', 'true');
  return controls(page).getAttribute('data-offline-active-generation');
}
async function saved(shelf, page, path, marker) {
  const response = await page.goto(shelf.origin + path);
  expect(response.status()).toBe(200); expect(response.fromServiceWorker()).toBe(true);
  await expect(page.locator(`[data-offline-page="${marker}"]`)).toBeVisible();
  await ready(page);
  await expect(controls(page)).toHaveAttribute('data-offline-saved-document', 'true');
  return response;
}
async function offlineFetch(page, path, options = {}) {
  return page.evaluate(async ({path, options}) => {
    try {const response = await fetch(path, options); return {status: response.status, type: response.headers.get('content-type'), text: (await response.text()).slice(0, 512)};}
    catch {return {unavailable: true};}
  }, {path, options});
}

test('useful cold and warm-reloaded server HTML precedes WASM and hydration retains existing DOM nodes', async ({shelf}, info) => {
  const page = await shelf.open(), journeys = [];
  // The second journey is a genuine warm document reload after the Go button
  // was enabled. It catches restored disabled-property state before hydration.
  for (const stage of ['cold navigation', 'warm reload']) {
    let release;
    const gate = new Promise(resolve => {release = resolve;});
    const wasmRequests = [];
    await page.route('**/*.wasm', async route => {
      wasmRequests.push(route.request().url());
      await gate; await route.continue();
    });
    try {
      const response = stage === 'cold navigation'
        ? await page.goto(shelf.origin + '/', {waitUntil: 'domcontentloaded'})
        : await page.reload({waitUntil: 'domcontentloaded'});
      expect(response.status()).toBe(200); expect(response.fromServiceWorker()).toBe(false);
      await expect.poll(() => wasmRequests.length, {message: 'actual WASM request reaches held gate'}).toBeGreaterThan(0);
      await expect(page.getByRole('heading', {name: 'A small public reading shelf.', exact: true})).toBeVisible();
      await expect(counter(page)).toHaveText('3');
      await expect(page.locator('[data-offline-counter-proof]')).toHaveText('Waiting for lazy Go');
      await expect(page.locator('[data-offline-increment]')).toBeDisabled();
      await expect(page.locator('body')).not.toHaveAttribute('data-nextgo-hydration', 'ready');
      const token = await page.evaluate(() => {
        globalThis.ssrOfflineNodes = [document.querySelector('[data-offline-controls]'), document.querySelector('[data-offline-count]'), document.querySelector('[data-offline-increment]')];
        return offlineDocumentToken;
      });
      release(); await ready(page);
      expect(await page.evaluate(() => ssrOfflineNodes.every((node, index) => node.isConnected && node === [document.querySelector('[data-offline-controls]'), document.querySelector('[data-offline-count]'), document.querySelector('[data-offline-increment]')][index]))).toBe(true);
      expect(await page.evaluate(() => offlineDocumentToken)).toBe(token);
      await page.locator('[data-offline-increment]').click(); await expect(counter(page)).toHaveText('4');
      journeys.push({stage, wasmRequests: [...wasmRequests], documentToken: token, counterAfterGoClick: '4'});
    } finally {release(); await page.unroute('**/*.wasm');}
  }
  expect(await cachedEntries(page)).toEqual([]);
  expect(await page.evaluate(async () => (await navigator.serviceWorker.getRegistrations()).length)).toBe(0);
  await info.attach('cold-and-warm-reload-held-WASM-proof', {body: JSON.stringify(journeys), contentType: 'application/json'});
});

test('explicit Go enable saves only the approved public documents and verified runtime assets', async ({shelf}, info) => {
  const page = await shelf.open(); const generation = await enable(shelf, page);
  const entries = await cachedEntries(page), urls = entries.flatMap(entry => entry.urls);
  expect(urls).toContain(shelf.origin + '/'); expect(urls).toContain(shelf.origin + '/guide');
  expect(urls.some(url => /\/module-[a-f0-9]+\.wasm$/.test(url))).toBe(true);
  expect(urls.some(url => /\.css$/.test(url))).toBe(true);
  expect(urls.filter(url => /\/api\/|\?|\/_nextgo\/actions\//.test(url))).toEqual([]);
  await page.locator('[data-offline-status-button]').click();
  await expect(controls(page)).toHaveAttribute('data-offline-active-generation', generation);
  const proof = await verifySavedPayloads(page, generation);
  const prepared = shelf.records.filter(record => record.path.startsWith('/_nextgo/offline/documents/'));
  expect([...new Set(prepared.map(record => record.path))].sort()).toEqual([...proof.preparationPaths].sort());
  for (const record of prepared) {expect(proof.preparationPaths).toContain(record.path); expect(record.method).toBe('GET'); expect(record.status).toBe(200);}
  await info.attach('saved-public-cache-inventory', {body: JSON.stringify({entries, ...proof}), contentType: 'application/json'});
});

test('closing the browser process and stopping the server still reopens saved SSR, CSS, core and lazy Go from the same private profile', async ({shelf}, info) => {
  let page = await shelf.open(); const generation = await enable(shelf, page);
  await page.locator('[data-offline-increment]').click(); await expect(counter(page)).toHaveText('4');
  await shelf.closeBrowser(); await shelf.stopNetwork();
  page = await shelf.open(); const assets = [];
  page.on('response', response => {if (/\.(?:wasm|css|js)(?:\?|$)/.test(response.url())) assets.push({url: response.url(), status: response.status(), worker: response.fromServiceWorker()});});
  await saved(shelf, page, '/', 'overview'); await expect(counter(page)).toHaveText('3');
  await expect(controls(page)).toHaveAttribute('data-offline-active-generation', generation);
  await page.locator('[data-offline-increment]').click(); await expect(counter(page)).toHaveText('4');
  expect(assets.some(asset => /\/app\.wasm$/.test(asset.url))).toBe(true);
  expect(assets.some(asset => /\/boot\.js$/.test(asset.url))).toBe(true);
  expect(assets.some(asset => /\/module-/.test(asset.url))).toBe(true);
  expect(assets.some(asset => /\.css$/.test(asset.url))).toBe(true);
  expect(assets.every(asset => asset.status === 200 && asset.worker)).toBe(true);
  expect(await page.locator('body').evaluate(node => getComputedStyle(node).fontFamily)).toContain('system-ui');
  await info.attach('saved-runtime-network-sources', {body: JSON.stringify(assets), contentType: 'application/json'});
});

test('native reading links open the saved guide and overview without a live server', async ({shelf}) => {
  let page = await shelf.open(); await enable(shelf, page); await shelf.closeBrowser(); await shelf.stopNetwork();
  page = await shelf.open(); await saved(shelf, page, '/', 'overview');
  await page.getByRole('navigation', {name: 'Offline example', exact: true}).getByRole('link', {name: 'Reading guide', exact: true}).click();
  await expect(page.locator('[data-offline-page="guide"]')).toBeVisible(); await expect(page).toHaveTitle('Reading guide — Next.go'); await ready(page);
  await expect(controls(page)).toHaveAttribute('data-offline-saved-document', 'true');
  await page.locator('[data-offline-increment]').click(); await expect(counter(page)).toHaveText('4');
  await page.getByRole('navigation', {name: 'Offline example', exact: true}).getByRole('link', {name: 'Overview', exact: true}).click();
  await ready(page); await expect(counter(page)).toHaveText('3');
});

test('API, POST, query and navigation JSON requests stay network-only and are never replayed after reconnection', async ({shelf}) => {
  const page = await shelf.open(); await enable(shelf, page);
  expect((await offlineFetch(page, '/api/health')).status).toBe(200);
  expect((await offlineFetch(page, '/api/health', {method: 'POST', body: 'public-probe'})).status).toBe(405);
  const before = shelf.records.filter(record => record.method === 'POST').length;
  await shelf.stopNetwork();
  for (const [path, options] of [['/api/health', {}], ['/api/health', {method: 'POST', body: 'must-not-replay'}], ['/guide', {headers: {'X-Nextgo-Navigate': '1'}}], ['/?query=not-saved', {}]]) {
    const result = await offlineFetch(page, path, options);
    expect(result.unavailable || result.status >= 400).toBe(true);
    expect(result.text || '').not.toContain('A small public reading shelf.');
  }
  await shelf.startNetwork('A');
  expect((await offlineFetch(page, '/api/health')).status).toBe(200);
  await page.locator('[data-offline-status-button]').click();
  expect(shelf.records.filter(record => record.method === 'POST')).toHaveLength(before);
});

test('evicting an actual required lazy WASM entry fails closed to an inert document instead of hydrating the wrong page', async ({shelf}) => {
  let page = await shelf.open(); await enable(shelf, page);
  const removed = await page.evaluate(async () => {
    for (const name of await caches.keys()) {
      const cache = await caches.open(name);
      for (const request of await cache.keys()) if (/\/module-[a-f0-9]+\.wasm$/.test(new URL(request.url).pathname)) {
        if (!await cache.delete(request)) throw new Error('Required asset deletion failed');
        return {name, url: request.url};
      }
    }
    throw new Error('No actual cached lazy WASM entry available for eviction');
  });
  expect(removed.url).toMatch(/\/module-/);
  await shelf.closeBrowser(); await shelf.stopNetwork();
  page = await shelf.open(); const response = await page.goto(shelf.origin + '/guide');
  expect(response.status()).toBe(503); expect(response.fromServiceWorker()).toBe(true);
  await expect(page.locator('[data-offline-controls]')).toHaveCount(0);
  await expect(page.locator('[data-nextgo-island]')).toHaveCount(0);
  await expect(page.locator('script[src]')).toHaveCount(0);
  await expect(page.locator('body')).not.toHaveAttribute('data-nextgo-hydration', 'ready');
});

test('a genuine new generation waits while the old client is open and activates only after every scoped client closes', async ({shelf}) => {
  let page = await shelf.open(); const oldGeneration = await enable(shelf, page);
  const document = await page.evaluate(() => {
    globalThis.waitingOfflineNodes = [document.querySelector('[data-nextgo-island="counter"]'), document.querySelector('[data-offline-count]'), document.querySelector('[data-offline-increment]')];
    return offlineDocumentToken;
  });
  await page.locator('[data-offline-increment]').click(); await expect(counter(page)).toHaveText('4');
  expect(await page.evaluate(() => waitingOfflineNodes.every((node, index) => node.isConnected && node === [document.querySelector('[data-nextgo-island="counter"]'), document.querySelector('[data-offline-count]'), document.querySelector('[data-offline-increment]')][index]))).toBe(true);
  expect(await page.evaluate(() => offlineDocumentToken)).toBe(document);
  await shelf.select('B'); await page.locator('[data-offline-update]').click();
  await expect(controls(page)).toHaveAttribute('data-offline-waiting-generation', /^[a-f0-9]{64}$/);
  const candidate = await controls(page).getAttribute('data-offline-waiting-generation'); expect(candidate).not.toBe(oldGeneration);
  await expect(controls(page)).toHaveAttribute('data-offline-active-generation', oldGeneration);
  expect(await page.evaluate(() => offlineDocumentToken)).toBe(document);
  expect(await page.evaluate(() => waitingOfflineNodes.every((node, index) => node.isConnected && node === [document.querySelector('[data-nextgo-island="counter"]'), document.querySelector('[data-offline-count]'), document.querySelector('[data-offline-increment]')][index]))).toBe(true);
  await expect(counter(page)).toHaveText('4');
  const registration = await page.evaluate(async () => {const r = await navigator.serviceWorker.getRegistration(); return {active: r.active?.state, waiting: r.waiting?.state, controller: navigator.serviceWorker.controller?.state};});
  expect(registration).toEqual({active: 'activated', waiting: 'installed', controller: 'activated'});
  await shelf.closeBrowser(); page = await shelf.open(); await page.goto(shelf.origin + '/'); await ready(page);
  await page.locator('[data-offline-status-button]').click();
  await expect(controls(page)).toHaveAttribute('data-offline-active-generation', candidate);
  await expect(controls(page)).toHaveAttribute('data-offline-waiting-generation', ''); await expect(counter(page)).toHaveText('3');
});

test('explicit Go disable reports retained charged payloads until closed clients allow explicit physical reconciliation', async ({shelf}, info) => {
  let page = await shelf.open(); await enable(shelf, page);
  const before = await cachedEntries(page), owned = before.map(entry => entry.name), beforeAccounting = await ownedAccounting(page);
  expect(owned.length).toBeGreaterThan(0);
  await page.evaluate(() => caches.open('offline-fixture-unrelated-empty-cache'));
  await page.locator('[data-offline-disable]').click();
  await expect(controls(page)).toHaveAttribute('data-offline-enabled', 'false');
  await expect(controls(page)).toHaveAttribute('data-offline-cleanup-pending', 'true');
  await page.locator('[data-offline-status-button]').click();
  expect(await page.evaluate(async () => (await navigator.serviceWorker.getRegistrations()).length)).toBe(0);
  const retained = await cachedEntries(page), retainedAccounting = await ownedAccounting(page);
  expect(retainedAccounting.generations).toEqual(beforeAccounting.generations);
  expect(retainedAccounting.generations.every(generation => generation.physicalBytes > 0)).toBe(true);
  expect(retainedAccounting.index.disabled).toBe(true);
  expect(retainedAccounting.index.active).toBe(beforeAccounting.index.active);
  expect(retainedAccounting.index.retainedClients).toBeGreaterThan(0);
  expect(retainedAccounting.index.retainedClients).toBeLessThanOrEqual(4);
  expect(retained.filter(entry => owned.includes(entry.name))).toEqual(before);
  expect(retained.some(entry => entry.name === 'offline-fixture-unrelated-empty-cache')).toBe(true);
  // Unregister does not detach this live controller or claim physical erasure.
  await expect(controls(page)).toHaveAttribute('data-offline-controlling', 'true');
  await shelf.closeBrowser(); page = await shelf.open();
  await page.goto(shelf.origin + '/'); await ready(page);
  await expect(controls(page)).toHaveAttribute('data-offline-controlling', 'false');
  const prior = shelf.records.length;
  await page.locator('[data-offline-disable]').click();
  await expect(controls(page)).toHaveAttribute('data-offline-cleanup-pending', 'false');
  await expect(controls(page)).toHaveAttribute('data-offline-enabled', 'false');
  await expect.poll(() => page.evaluate(async () => (await navigator.serviceWorker.getRegistrations()).length)).toBe(0);
  const remaining = await cachedEntries(page), reconciledAccounting = await ownedAccounting(page);
  expect(reconciledAccounting).toEqual({index: null, generations: []});
  expect(remaining.map(entry => entry.name)).toContain('offline-fixture-unrelated-empty-cache');
  expect(remaining.filter(entry => owned.includes(entry.name))).toEqual([]);
  expect(shelf.records.slice(prior).filter(record => record.path.startsWith('/_nextgo/offline/documents/'))).toEqual([]);
  await info.attach('explicit-disable-cache-accounting', {body: JSON.stringify({before, retained, remaining, beforeAccounting, retainedAccounting, reconciledAccounting}), contentType: 'application/json'});
});

// Fault ONLY the parent realm's registration wrappers. The child retains the
// browser's real ServiceWorkerContainer methods and updates the actual A/B
// artifact-backed registration. This forces a branch; it does not reproduce or
// explain the historical native empty-record failure.
async function parentEmptyRegistrationFault(page, countNativeUpdates = false) {
  await page.evaluate(countNativeUpdates => {
    const sw = navigator.serviceWorker, descriptor = JSON.parse(document.body.getAttribute('data-nextgo-offline'));
    const names = ['getRegistrations', 'getRegistration'];
    if (globalThis.offlineEmptyParentFault || names.some(name => Object.hasOwn(sw, name))) throw new Error('Parent container is already instrumented');
    const nativeList = sw.getRegistrations, nativeTarget = sw.getRegistration;
    const state = {lists: 0, lookups: 0, added: 0, removed: 0, live: new Set(), maxLive: 0, frameListeners: new Map(), nativeUpdates: null, nativeUpdateWitness: countNativeUpdates ? 'no-attempt' : 'disabled'};
    const empty = registration => registration?.scope === descriptor.scope ? {scope: registration.scope, active: null, waiting: null, installing: null} : registration;
    const list = function(...args) {
      if (this !== sw) throw new Error('Unexpected parent list receiver');
      if (++state.lists > 16) throw new Error('Parent list fault exceeds finite budget');
      return Reflect.apply(nativeList, this, args).then(values => values.map(empty));
    };
    const target = function(...args) {
      if (this !== sw) throw new Error('Unexpected parent lookup receiver');
      if (++state.lookups > 4) throw new Error('Parent target fault exceeds finite budget');
      return Reflect.apply(nativeTarget, this, args).then(empty);
    };
    const record = records => {
      if (records.length > 128) throw new Error('Unexpected frame observer volume');
      for (const record of records) {
        for (const node of record.addedNodes) if (node.nodeType === 1 && node.tagName === 'IFRAME') {state.added++; state.live.add(node); state.maxLive = Math.max(state.maxLive, state.live.size);
          if (countNativeUpdates) {
            const onLoad = () => {
            state.frameListeners.delete(node);
            // A one-call forwarding witness in this exact child realm. No
            // native object is stored in parent state or used after removal.
            try {
              const prototype = node.contentWindow.ServiceWorkerRegistration.prototype;
              const own = Object.getOwnPropertyDescriptor(prototype, 'update');
              if (!own || typeof own.value !== 'function') throw new Error('Missing native update descriptor');
              const native = own.value;
              Object.defineProperty(prototype, 'update', {...own, value: function(...args) {
                Object.defineProperty(prototype, 'update', own);
                state.nativeUpdateWitness = 'called';
                if (this.scope !== descriptor.scope || ++state.nativeUpdates !== 1) throw new Error('Unexpected native update dispatch');
                return Reflect.apply(native, this, args);
              }});
              state.nativeUpdates = 0; state.nativeUpdateWitness = 'installed';
            } catch {state.nativeWitnessUnavailable = true; state.nativeUpdateWitness = 'unavailable'; state.nativeUpdates = null;}
            };
            state.frameListeners.set(node, onLoad);
            node.addEventListener('load', onLoad, {once: true});
          }
        }
        for (const node of record.removedNodes) if (state.live.delete(node)) {state.removed++; const listener = state.frameListeners.get(node); if (listener) node.removeEventListener('load', listener); state.frameListeners.delete(node);}
      }
      state.maxLive = Math.max(state.maxLive, state.live.size);
    };
    const observer = new MutationObserver(record);
    observer.observe(document.documentElement, {childList: true});
    Object.defineProperty(sw, 'getRegistrations', {value: list, configurable: true});
    Object.defineProperty(sw, 'getRegistration', {value: target, configurable: true});
    globalThis.offlineEmptyParentFault = {sw, list, target, state, observer, record};
  }, countNativeUpdates);
}
async function retireParentEmptyRegistrationFault(page) {
  return page.evaluate(() => {
    const fault = globalThis.offlineEmptyParentFault;
    if (!fault) throw new Error('Missing owned fault');
    let proof;
    try {
      fault.record(fault.observer.takeRecords());
      if (navigator.serviceWorker !== fault.sw || fault.sw.getRegistrations !== fault.list || fault.sw.getRegistration !== fault.target) throw new Error('Fault ownership changed');
      delete fault.sw.getRegistrations; delete fault.sw.getRegistration;
      if (Object.hasOwn(fault.sw, 'getRegistrations') || Object.hasOwn(fault.sw, 'getRegistration')) throw new Error('Parent wrappers were not restored');
      proof = {forcedParentEmptyOnly: true, lists: fault.state.lists, lookups: fault.state.lookups, framesAdded: fault.state.added, framesRemoved: fault.state.removed, framesConnected: [...fault.state.live].filter(frame => frame.isConnected).length, maximumLiveFrames: fault.state.maxLive, nativeUpdateCalls: fault.state.nativeUpdates, nativeUpdateWitness: fault.state.nativeUpdateWitness, nativeWitnessUnavailable: !!fault.state.nativeWitnessUnavailable, pendingFrameListeners: fault.state.frameListeners.size};
    } finally {
      fault.observer.disconnect();
      for (const [frame, listener] of fault.state.frameListeners) frame.removeEventListener('load', listener);
      fault.state.frameListeners.clear();
      if (fault.sw.getRegistrations === fault.list) delete fault.sw.getRegistrations;
      if (fault.sw.getRegistration === fault.target) delete fault.sw.getRegistration;
      delete globalThis.offlineEmptyParentFault;
    }
    return proof;
  });
}
async function nextOfflineCompletion(page) {
  const current = await controls(page).getAttribute('data-offline-completed-operations');
  expect(current).toMatch(/^(0|[1-9][0-9]{0,9})$/);
  expect(BigInt(current)).toBeLessThan(1n << 32n);
  return ((BigInt(current) + 1n) % (1n << 32n)).toString();
}

test('Go update recovers a forced parent-empty proof through one live native realm without activating or remounting', async ({shelf}, info) => {
  let page = await shelf.open();
  const active = await enable(shelf, page), documentToken = await page.evaluate(() => offlineDocumentToken);
  await page.locator('[data-offline-increment]').click(); await expect(counter(page)).toHaveText('4');
  await page.evaluate(() => {globalThis.freshRealmOfflineNodes = [document.querySelector('[data-nextgo-island="counter"]'), document.querySelector('[data-offline-count]'), document.querySelector('[data-offline-increment]')];});
  await parentEmptyRegistrationFault(page);
  let proof;
  try {
    await shelf.select('B');
    const completion = await nextOfflineCompletion(page);
    await page.locator('[data-offline-update]').click();
    await expect(controls(page)).toHaveAttribute('data-offline-completed-operations', completion);
    await expect(controls(page)).toHaveAttribute('data-offline-operation-error', '');
    await expect(controls(page)).toHaveAttribute('data-offline-last-error', '');
    await expect(controls(page)).toHaveAttribute('data-offline-waiting-generation', /^[a-f0-9]{64}$/);
    await expect(controls(page)).toHaveAttribute('data-offline-active-generation', active);
    expect(await page.evaluate(() => offlineDocumentToken)).toBe(documentToken);
    expect(await page.evaluate(() => freshRealmOfflineNodes.every((node, index) => node.isConnected && node === [document.querySelector('[data-nextgo-island="counter"]'), document.querySelector('[data-offline-count]'), document.querySelector('[data-offline-increment]')][index]))).toBe(true);
    await expect(counter(page)).toHaveText('4');
  } finally {proof = await retireParentEmptyRegistrationFault(page);}
  expect(proof).toEqual({forcedParentEmptyOnly: true, lists: 4, lookups: 1, framesAdded: 1, framesRemoved: 1, framesConnected: 0, maximumLiveFrames: 1, nativeUpdateCalls: null, nativeUpdateWitness: 'disabled', nativeWitnessUnavailable: false, pendingFrameListeners: 0});
  const waiting = await controls(page).getAttribute('data-offline-waiting-generation'); expect(waiting).not.toBe(active);
  const native = await page.evaluate(async () => {const registration = await navigator.serviceWorker.getRegistration(); return {active: registration.active?.state, waiting: registration.waiting?.state, controller: navigator.serviceWorker.controller?.state, frames: document.querySelectorAll('iframe').length};});
  expect(native).toEqual({active: 'activated', waiting: 'installed', controller: 'activated', frames: 0});
  await info.attach('forced-parent-empty-live-realm-update', {body: JSON.stringify({proof, native, active, waiting, naturalCause: 'UNKNOWN'}), contentType: 'application/json'});
  await shelf.closeBrowser(); page = await shelf.open(); await page.goto(shelf.origin + '/'); await ready(page);
  const completion = await nextOfflineCompletion(page);
  await page.locator('[data-offline-status-button]').click();
  await expect(controls(page)).toHaveAttribute('data-offline-completed-operations', completion);
  await expect(controls(page)).toHaveAttribute('data-offline-active-generation', waiting);
  await expect(controls(page)).toHaveAttribute('data-offline-waiting-generation', '');
  await expect(counter(page)).toHaveText('3');
});

// Probe the actual native policy with the same empty frame options, then fully
// remove it before the Go operation. A removed frame is not a claim about
// instantaneous ServiceWorker client-ID erasure.
async function blankFramePolicy(page) {
  return page.evaluate(async () => {
    if (document.querySelector('iframe')) throw new Error('Policy probe requires no preexisting frames');
    const frame = document.createElement('iframe');
    frame.hidden = true; frame.inert = true; frame.tabIndex = -1; frame.setAttribute('aria-hidden', 'true'); frame.src = 'about:blank';
    let loaded = false, failed = false, violation = false, timer;
    let finish;
    const result = new Promise((resolve, reject) => {
      const inspect = () => {
        if (!loaded && !failed) return;
        let accessible = false;
        try {
          const child = frame.contentWindow, childDocument = frame.contentDocument;
          accessible = !!child && !!childDocument && child.location.href === 'about:blank' && child.origin === location.origin && childDocument.readyState === 'complete' && childDocument.body?.childNodes.length === 0 && !!child.navigator.serviceWorker;
        } catch {}
        if (accessible) resolve({nativePermitsLiveBlank: true, policyViolationObserved: violation});
        else if (violation) resolve({nativePermitsLiveBlank: false, policyViolationObserved: true});
      };
      finish = inspect;
      timer = setTimeout(() => reject(new Error('Native blank-frame policy remains unknown')), 5000);
    });
    const onLoad = () => {loaded = true; finish();};
    const onError = () => {failed = true; finish();};
    const onPolicy = event => {
      // CSP serializes non-network blocked URLs as their scheme on some
      // implementations. The sole owned iframe attempt has literal src blank.
      if (event instanceof SecurityPolicyViolationEvent && event.isTrusted && event.effectiveDirective === 'frame-src' && ['about', 'about:blank'].includes(event.blockedURI) && frame.getAttribute('src') === 'about:blank' && document.querySelectorAll('iframe').length === 1 && frame.isConnected) {violation = true; finish();}
    };
    frame.addEventListener('load', onLoad); frame.addEventListener('error', onError);
    document.addEventListener('securitypolicyviolation', onPolicy);
    try {document.documentElement.append(frame); return await result;}
    finally {
      clearTimeout(timer); frame.removeEventListener('load', onLoad); frame.removeEventListener('error', onError);
      document.removeEventListener('securitypolicyviolation', onPolicy); frame.remove();
      if (frame.isConnected || document.querySelector('iframe')) throw new Error('Native policy probe did not retire');
    }
  });
}

test('Go parent-empty update honors actual native blank-frame CSP policy with real worker state and cleanup', async ({shelf}, info) => {
  const page = await shelf.open(), active = await enable(shelf, page), documentToken = await page.evaluate(() => offlineDocumentToken);
  // No response-header or fixture-server changes. Some browsers permit the
  // initial blank realm despite denying its explicit blank navigation.
  await page.evaluate(() => {const policy = document.createElement('meta'); policy.httpEquiv = 'Content-Security-Policy'; policy.content = "frame-src 'none'"; document.head.append(policy);});
  const policy = await blankFramePolicy(page);
  await parentEmptyRegistrationFault(page, true);
  let proof, waiting;
  try {
    await shelf.select('B');
    const completion = await nextOfflineCompletion(page);
    await page.locator('[data-offline-update]').click();
    await expect(controls(page)).toHaveAttribute('data-offline-completed-operations', completion);
    if (policy.nativePermitsLiveBlank) {
      await expect(controls(page)).toHaveAttribute('data-offline-operation-error', '');
      await expect(controls(page)).toHaveAttribute('data-offline-last-error', '');
      await expect(controls(page)).toHaveAttribute('data-offline-waiting-generation', /^[a-f0-9]{64}$/);
      waiting = await controls(page).getAttribute('data-offline-waiting-generation'); expect(waiting).not.toBe(active);
    } else {
      expect(policy.policyViolationObserved).toBe(true);
      await expect(controls(page)).toHaveAttribute('data-offline-operation-error', /conflict/);
      await expect(controls(page)).toHaveAttribute('data-offline-waiting-generation', '');
    }
    await expect(controls(page)).toHaveAttribute('data-offline-active-generation', active);
    expect(await page.evaluate(() => offlineDocumentToken)).toBe(documentToken);
    await expect(counter(page)).toHaveText('3');
  } finally {proof = await retireParentEmptyRegistrationFault(page);}
  expect(proof.forcedParentEmptyOnly).toBe(true); expect(proof.lookups).toBe(1);
  expect(proof.framesAdded).toBe(1); expect(proof.framesRemoved).toBe(1); expect(proof.framesConnected).toBe(0);
  expect(proof.maximumLiveFrames).toBe(1); expect(proof.pendingFrameListeners).toBe(0);
  const native = await page.evaluate(async () => {const registration = await navigator.serviceWorker.getRegistration(); return {active: registration.active?.state, waiting: registration.waiting?.state || null, frames: document.querySelectorAll('iframe').length};});
  // Persist the bounded witness before requiring complete instrumentation. A
  // native-denied/no-attempt realm is explicit UNKNOWN for call counting, not
  // an observed zero. It cannot qualify this strict zero-dispatch assertion.
  await info.attach('forced-parent-empty-native-csp-policy', {body: JSON.stringify({policy, proof, native, active, waiting: waiting || null, nativeDeniedNoObservableUpdateAttempt: !policy.nativePermitsLiveBlank && proof.nativeUpdateWitness === 'no-attempt', naturalCause: 'UNKNOWN'}), contentType: 'application/json'});
  expect(proof.nativeWitnessUnavailable).toBe(false);
  if (policy.nativePermitsLiveBlank) {expect(proof.nativeUpdateWitness).toBe('called'); expect(proof.nativeUpdateCalls).toBe(1);}
  else {expect(proof.nativeUpdateWitness).toBe('installed'); expect(proof.nativeUpdateCalls).toBe(0);}
  expect(native).toEqual({active: 'activated', waiting: policy.nativePermitsLiveBlank ? 'installed' : null, frames: 0});
});
