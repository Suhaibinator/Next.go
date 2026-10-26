import {test as base, expect} from '@playwright/test';
import {startMetadataStream, finalData} from './serve-metadata-stream.mjs';

const fallback = 'Loading public metadata — Next.go';
const fallbackDescription = 'Fallback metadata while the public description is loading.';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const count = page => page.getByRole('region', {name: 'Metadata stream counter', exact: true}).locator('[data-count]');
const increment = page => page.getByRole('button', {name: 'Increase Metadata stream counter', exact: true});
const waitSource = gate => expect.poll(() => Boolean(gate.entry), {message: 'actual native Memo data-source GET entered'}).toBe(true);
const test = base.extend({
  metadataOptions: [{}, {option: true}],
  native: async ({metadataOptions, page}, use, info) => {
    const startupAbort = new AbortController();
    const onPageClose = () => startupAbort.abort(new Error('Owned page closed during metadata fixture startup'));
    page.once('close', onPageClose);
    let fixture;
    try {fixture = await startMetadataStream({...metadataOptions, signal: startupAbort.signal});}
    catch (error) {if (error.metadataFixtureProof) await info.attach('metadata-startup-failure-proof', {body: JSON.stringify(error.metadataFixtureProof), contentType: 'application/json'}); throw error;}
    finally {page.off('close', onPageClose);}
    try {await use(fixture);} finally {
      const errors = [];
      try {await page.close();} catch (error) {errors.push(error);}
      try {await fixture.close();} catch (error) {errors.push(error);}
      const proof = fixture.snapshot();
      await info.attach('native-data-source-and-wire-proof', {body: JSON.stringify(proof), contentType: 'application/json'});
      expect(proof.process.closed && proof.process.joined).toBe(true);
      expect(proof.process.forced || proof.process.unexpectedExit).toBe(false);
      expect(proof.process.termSent).toBe(true); expect(proof.process.code).toBe(0); expect(proof.process.signal).toBe(null);
      expect(proof.gatewayListening || proof.sourceListening).toBe(false); expect(proof.httpJoined).toBe(true);
      expect(proof.upstreams).toBe(0); expect(proof.sockets).toBe(0);
      expect(proof.listeners.every(listener => listener.bindJoined && listener.closed && !listener.listening && !listener.failedBind && listener.errors.length === 0)).toBe(true);
      expect(proof.source.filter(entry => entry.unexpected)).toEqual([]);
      expect(proof.wire.filter(record => record.observerError)).toEqual([]);
      if (errors.length) throw new AggregateError(errors, 'metadata-stream owned cleanup failed');
    }
  },
});
async function fallbackHead(page) {
  await expect(page).toHaveTitle(fallback);
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', fallbackDescription);
}
async function finalHead(page) {
  await expect(page).toHaveTitle(finalData.title);
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', finalData.description);
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute('href', finalData.canonical);
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', finalData.socialTitle);
}
function heldSource(gate, record) {
  expect(gate.released).toBe(false);
  expect(gate.entry).toMatchObject({released: false, finished: false, canceled: false});
  expect(record.ended || record.closed).toBe(false);
  expect(record.frames.filter(frame => frame.type === 'metadata' || frame.type === 'done')).toEqual([]);
}
async function beginInitial(page, native) {
  const gate = native.arm();
  try {
    const response = await page.goto(native.origin + '/metadata-stream', {waitUntil: 'commit'});
    expect(response.status()).toBe(200); await waitSource(gate);
    await expect(page.getByRole('heading', {level: 1})).toHaveText('Metadata can finish after the body.');
    await fallbackHead(page); await ready(page);
    const record = native.wire.find(record => record.path === '/metadata-stream' && record.method === 'GET');
    heldSource(gate, record); return {gate, response, record};
  } catch (error) {gate.release(); throw error;}
}
async function completeInitial(page, native) {
  const {gate, record} = await beginInitial(page, native);
  try {heldSource(gate, record); gate.release(); await finalHead(page); await page.waitForLoadState('load');} finally {gate.release();}
}
function latestNavigation(native) {return [...native.wire].reverse().find(record => record.path === '/metadata-stream?cycle=next' && record.method === 'GET');}
async function beginNavigation(page, native) {
  const gate = native.arm();
  try {
    await page.locator('[data-metadata-refresh-link]').click(); await waitSource(gate);
    await expect(page).toHaveURL(native.origin + '/metadata-stream?cycle=next');
    await fallbackHead(page); await ready(page);
    await expect.poll(() => latestNavigation(native)?.frames.filter(frame => frame.type === 'shell').length).toBe(1);
    const record = latestNavigation(native), shell = record.frames.find(frame => frame.type === 'shell');
    expect(record.metadataRequest).toBe('1'); expect(record.headers['x-nextgo-metadata']).toBe('1');
    expect(record.headers['cache-control']).toContain('no-store');
    expect(String(record.headers.vary).toLowerCase()).toContain('x-nextgo-metadata');
    expect(shell.metadataPending).toBe(true); expect(shell.metadataOwner).toMatch(/^[a-f0-9]{32}$/);
    heldSource(gate, record); return {gate, record, shell};
  } catch (error) {gate.release(); throw error;}
}

test('initial browser receives useful fallback HTML and hydrated Go before genuine deferred metadata resolves', async ({page, native}) => {
  const {gate, response, record} = await beginInitial(page, native);
  try {
    const headers = await response.allHeaders();
    expect(headers['cache-control']).toContain('no-store');
    expect(headers['vary'].toLowerCase()).toContain('user-agent');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-metadata-version', '1');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-metadata-owner', /^[a-f0-9]{32}$/);
    await expect(count(page)).toHaveText('3'); await increment(page).click(); await expect(count(page)).toHaveText('4');
    heldSource(gate, record); gate.release(); await finalHead(page); await page.waitForLoadState('load');
    await expect(count(page)).toHaveText('4');
    await expect(page.locator('template[data-nextgo-metadata], [data-nextgo-metadata-ready]')).toHaveCount(0);
    expect(native.source).toHaveLength(1);
  } finally {gate.release();}
});

test('late head completion preserves body, resource and JSON-LD nodes plus focus, input, counter, scroll and history', async ({page, native}) => {
  const {gate, record} = await beginInitial(page, native);
  try {
    await increment(page).click(); await expect(count(page)).toHaveText('4');
    await page.locator('[data-metadata-input]').fill('Unsaved browser draft'); await page.locator('[data-metadata-input]').focus();
    const before = await page.evaluate(() => {
      const resource = [...document.head.querySelectorAll('link[rel="stylesheet"]')].find(node => new URL(node.href).pathname.endsWith('/metadata-stream.css'));
      const jsonld = [...document.head.querySelectorAll('script[type="application/ld+json"]')].find(node => node.textContent.includes('metadata-stream-jsonld-v1'));
      if (!resource || !jsonld) throw new Error('Actual frozen head companions missing');
      const counter = document.querySelector('[data-metadata-counter]');
      const island = counter.querySelector('[data-nextgo-island]'), output = counter.querySelector('[data-count]'), button = counter.querySelector('[data-increment]');
      if (!island || !output || !button) throw new Error('Actual Go counter nodes missing');
      globalThis.keptMetadataNodes = {island, output, button, body: document.querySelector('[data-metadata-stream]'), input: document.querySelector('[data-metadata-input]'), counter: document.querySelector('[data-metadata-counter]'), resource, jsonld};
      return {url: location.href, length: history.length, state: JSON.stringify(history.state), x: scrollX, y: scrollY, resourceURL: resource.href, jsonldText: jsonld.textContent};
    });
    heldSource(gate, record); gate.release(); await finalHead(page); await page.waitForLoadState('load');
    expect(await page.evaluate(before => {
      const kept = keptMetadataNodes;
      return Object.values(kept).every(node => node.isConnected) && kept.body === document.querySelector('[data-metadata-stream]') && kept.input === document.querySelector('[data-metadata-input]') && kept.counter === document.querySelector('[data-metadata-counter]') && kept.island === kept.counter.querySelector('[data-nextgo-island]') && kept.output === kept.counter.querySelector('[data-count]') && kept.button === kept.counter.querySelector('[data-increment]') && kept.jsonld.parentNode === document.head && kept.jsonld === [...document.head.querySelectorAll('script[type="application/ld+json"]')].find(node => node.textContent.includes('metadata-stream-jsonld-v1')) && kept.resource === [...document.head.querySelectorAll('link[rel="stylesheet"]')].find(node => node.href === before.resourceURL) && kept.jsonld.textContent === before.jsonldText && document.activeElement === kept.input;
    }, before)).toBe(true);
    expect(await page.evaluate(() => ({url: location.href, length: history.length, state: JSON.stringify(history.state), x: scrollX, y: scrollY}))).toEqual({url: before.url, length: before.length, state: before.state, x: before.x, y: before.y});
    await expect(page.locator('[data-metadata-input]')).toHaveValue('Unsaved browser draft'); await expect(count(page)).toHaveText('4');
    await increment(page).click(); await expect(count(page)).toHaveText('5');
    expect(await page.locator('link[href*="metadata-stream.css"]').count()).toBe(1);
    expect(await page.locator('script[type="application/ld+json"]').evaluateAll(nodes => nodes.filter(node => node.textContent.includes('metadata-stream-jsonld-v1')).length)).toBe(1);
  } finally {gate.release();}
});

test('crawler, unknown agent and HEAD requests block for final metadata from the same native loader', async ({request, native}) => {
  for (const [method, agent] of [['GET','Googlebot/2.1'], ['GET','UnknownMetadataProbe/1'], ['HEAD','Mozilla/5.0 Chrome/140.0 Safari/537.36']]) {
    const gate = native.arm(), path = '/metadata-stream?blocking=' + method + '-' + encodeURIComponent(agent);
    const responseJob = request.fetch(native.origin + path, {method, headers: {'User-Agent': agent}, timeout: 20_000}); responseJob.catch(() => {});
    try {
      await waitSource(gate);
      const record = native.wire.find(record => record.path === path); expect(record.headersReceived).toBe(false); heldSource(gate, record);
      gate.release(); const response = await responseJob; expect(response.status()).toBe(200);
      const text = await response.text();
      if (method === 'HEAD') expect(text).toBe('');
      else {expect(text).toContain(finalData.title); expect(text).not.toContain('<title>' + fallback + '</title>'); expect(text).not.toContain('data-nextgo-metadata-version');}
    } finally {gate.release(); await Promise.allSettled([responseJob]);}
  }
  expect(native.source).toHaveLength(3);
});

test.describe('initial HTML opt-out', () => {
  test.use({metadataOptions: {html: false}});
  test('disabled initial HTML streaming blocks a recognized browser until final metadata is ready', async ({page, request, native}) => {
    const agent = await page.evaluate(() => navigator.userAgent), gate = native.arm();
    const responseJob = request.get(native.origin + '/metadata-stream', {headers: {'User-Agent': agent}, timeout: 20_000}); responseJob.catch(() => {});
    try {
      await waitSource(gate); const record = native.wire.find(record => record.path === '/metadata-stream'); expect(record.headersReceived).toBe(false); heldSource(gate, record);
      gate.release(); const response = await responseJob; expect(response.status()).toBe(200);
      const text = await response.text(); expect(text).toContain(finalData.title); expect(text).not.toContain('data-nextgo-metadata-version');
    } finally {gate.release(); await Promise.allSettled([responseJob]);}
  });
});

test('negotiated Go navigation commits a fallback shell then exactly one metadata terminal before done', async ({page, native}) => {
  await completeInitial(page, native); const {gate, record, shell} = await beginNavigation(page, native);
  try {
    await increment(page).click(); await expect(count(page)).toHaveText('4');
    heldSource(gate, record); gate.release(); await finalHead(page);
    await expect.poll(() => record.ended, {message: 'actual negotiated stream EOF'}).toBe(true);
    const terminals = record.frames.filter(frame => frame.type === 'metadata'); expect(terminals).toHaveLength(1);
    expect(terminals[0]).toMatchObject({version: 1, owner: shell.metadataOwner, metadataVersion: 1, failed: false, title: finalData.title});
    expect(record.frames.findIndex(frame => frame.type === 'metadata')).toBeLessThan(record.frames.findIndex(frame => frame.type === 'done'));
    await expect(count(page)).toHaveText('4');
  } finally {gate.release();}
});

test('navigation away cancels the real pending metadata read and cannot commit its stale title', async ({page, native}) => {
  await completeInitial(page, native); const {gate, record} = await beginNavigation(page, native);
  try {
    heldSource(gate, record);
    await page.locator('[data-metadata-exit-link]').click(); await expect(page).toHaveURL(native.origin + '/about');
    await expect(page).toHaveTitle('Architecture — Next.go');
    await expect.poll(() => gate.entry.canceled, {message: 'native request cancellation reached held Memo HTTP source'}).toBe(true);
    gate.release(); await expect.poll(() => record.closed, {message: 'superseded native metadata response physically closed'}).toBe(true);
    await expect(page).toHaveTitle('Architecture — Next.go');
    await expect(page.locator('[data-metadata-stream]')).toHaveCount(0);
  } finally {gate.release();}
});

for (const mode of ['fail','invalid','readonly']) test.describe(`late ${mode}`, () => {
  test.use({metadataOptions: {mode}});
  test(`a committed ${mode} contribution keeps complete fallback with a generic failed receipt`, async ({page, native}) => {
    // Complete the real initial failure, then use its genuine sample anchor;
    // no JS-created application controls or metadata responses are used.
    const initial = await beginInitial(page, native);
    try {initial.gate.release(); await page.waitForLoadState('load'); await fallbackHead(page);} finally {initial.gate.release();}
    const {gate, record, shell} = await beginNavigation(page, native);
    try {
      await increment(page).click(); await expect(count(page)).toHaveText('4'); heldSource(gate, record); gate.release();
      await expect.poll(() => record.ended).toBe(true);
      const terminals = record.frames.filter(frame => frame.type === 'metadata'); expect(terminals).toHaveLength(1);
      expect(terminals[0]).toMatchObject({version: 1, failed: true, owner: shell.metadataOwner, metadataVersion: 1});
      expect(terminals[0].metadataKeys).toEqual(['owner','version']);
      await fallbackHead(page); await expect(count(page)).toHaveText('4');
      expect(record.lateHeader || record.setCookie).toBe(false);
      expect(await page.content()).not.toContain('private deferred metadata fixture diagnostic');
      expect(await page.content()).not.toContain('must-not-appear');
    } finally {gate.release();}
  });
});

test('missing or unsupported metadata negotiation blocks and preserves the ordinary existing stream shape', async ({page, request, native}) => {
  const agent = await page.evaluate(() => navigator.userAgent);
  for (const header of [undefined, '99']) {
    const gate = native.arm(), path = '/metadata-stream?legacy=' + (header || 'missing');
    const headers = {'User-Agent': agent, 'X-Nextgo-Navigate': '1', 'X-Nextgo-Stream': '1'}; if (header) headers['X-Nextgo-Metadata'] = header;
    const responseJob = request.get(native.origin + path, {headers, timeout: 20_000}); responseJob.catch(() => {});
    try {
      await waitSource(gate); const record = native.wire.find(record => record.path === path); expect(record.headersReceived).toBe(false); heldSource(gate, record);
      gate.release(); const response = await responseJob; expect(response.status()).toBe(200); expect(response.headers()['x-nextgo-metadata']).toBeUndefined();
      await expect.poll(() => record.ended).toBe(true);
      expect(record.frames.filter(frame => frame.type === 'metadata')).toEqual([]);
      expect(record.frames.map(frame => frame.type)).toEqual(['shell','done']);
      const shell = record.frames[0], done = record.frames[1];
      expect(shell.keys).toEqual(['page','type','version']); expect(done.keys).toEqual(['type','version']);
      expect(shell.version).toBe(1); expect(done.version).toBe(1);
      expect(shell.pageTitle).toBe(finalData.title);
      for (const frame of record.frames) {expect(frame.metadataPending).toBeUndefined(); expect(frame.metadataOwner).toBeUndefined(); expect(frame.metadataIndices).toBeUndefined(); expect(frame.metadataKeys).toBeUndefined();}
    } finally {gate.release(); await Promise.allSettled([responseJob]);}
  }
});

test('uncached speculative requests return conservative 204 without invoking the metadata data source', async ({request, native}) => {
  const response = await request.get(native.origin + '/metadata-stream?speculative=1', {headers: {'X-Nextgo-Navigate': '1', 'X-Nextgo-Stream': '1', 'X-Nextgo-Metadata': '1', 'X-Nextgo-Purpose': 'prefetch'}});
  expect(response.status()).toBe(204); expect(native.source).toEqual([]);
  expect(response.headers()['cache-control']).toContain('no-store');
});
