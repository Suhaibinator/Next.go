import {test as base, expect} from '@playwright/test';
import {startMetadataStream, finalData} from './serve-metadata-stream.mjs';

const fallbackTitle = 'Loading public metadata — Next.go';
const finalTitle = 'Inherited metadata cleared — Next.go';
const description = 'Inherited public description awaiting an explicit clear.';
const canonical = 'https://example.com/metadata-clear?phase=inherited';
const robots = 'noindex, nofollow';
const count = page => page.getByRole('region', {name: 'Metadata stream counter', exact: true}).locator('[data-count]');
const increment = page => page.getByRole('button', {name: 'Increase Metadata stream counter', exact: true});
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const waitSource = gate => expect.poll(() => Boolean(gate.entry), {message: 'actual native inherited-metadata source GET'}).toBe(true);

// Reuse the unchanged real-native/held-JSON/proxy fixture. No application HTML
// or stream frames are synthesized by this test, and there is no build fallback.
const test = base.extend({
  native: async ({page}, use, info) => {
    const startupAbort = new AbortController();
    const onPageClose = () => startupAbort.abort(new Error('Owned scalar-clear page closed during startup'));
    page.once('close', onPageClose);
    let fixture;
    try {fixture = await startMetadataStream({signal: startupAbort.signal});}
    catch (error) {if (error.metadataFixtureProof) await info.attach('metadata-clear-startup-failure-proof', {body: JSON.stringify(error.metadataFixtureProof), contentType: 'application/json'}); throw error;}
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
      expect(proof.listeners).toHaveLength(2);
      expect(proof.listeners.every(listener => listener.bindJoined && listener.closed && !listener.listening && !listener.failedBind && listener.errors.length === 0)).toBe(true);
      expect(proof.source.filter(entry => entry.unexpected)).toEqual([]);
      expect(proof.wire.filter(record => record.observerError)).toEqual([]);
      if (errors.length) throw new AggregateError(errors, 'metadata-clear owned cleanup failed');
    }
  },
});
async function fallbackHead(page) {
  await expect(page).toHaveTitle(fallbackTitle);
  await expect(page.locator('head meta[name="description"]')).toHaveCount(1);
  await expect(page.locator('head meta[name="description"]')).toHaveAttribute('content', description);
  await expect(page.locator('head link[rel="canonical"]')).toHaveAttribute('href', canonical);
  await expect(page.locator('head meta[name="robots"]')).toHaveAttribute('content', robots);
}
async function finalHead(page) {
  await expect(page).toHaveTitle(finalTitle);
  await expect(page.locator('head meta[name="description"]')).toHaveCount(1);
  await expect(page.locator('head meta[name="description"]')).toHaveAttribute('content', '');
  await expect(page.locator('head link[rel="canonical"]')).toHaveCount(0);
  await expect(page.locator('head meta[name="robots"]')).toHaveCount(0);
  await expect(page.locator('head meta[property="og:title"]')).toHaveAttribute('content', finalData.socialTitle);
  await expect(page.locator('head meta[name="twitter:title"]')).toHaveAttribute('content', finalData.socialTitle);
}
function heldSource(gate, record) {
  expect(gate.released).toBe(false);
  expect(gate.entry).toMatchObject({released: false, finished: false, canceled: false});
  expect(record).toBeTruthy(); expect(record.ended || record.closed).toBe(false);
  expect(record.frames.filter(frame => frame.type === 'metadata' || frame.type === 'done')).toEqual([]);
}
async function beginInitial(page, native) {
  const gate = native.arm();
  try {
    const response = await page.goto(native.origin + '/metadata-clear', {waitUntil: 'commit'});
    expect(response.status()).toBe(200); await waitSource(gate);
    await expect(page.locator('[data-metadata-clear]')).toBeVisible();
    await fallbackHead(page); await ready(page);
    const record = native.wire.find(record => record.path === '/metadata-clear' && record.method === 'GET');
    heldSource(gate, record); return {gate, response, record};
  } catch (error) {gate.release(); throw error;}
}
async function captureLocalState(page) {
  await expect(count(page)).toHaveText('3'); await increment(page).click(); await expect(count(page)).toHaveText('4');
  await page.locator('[data-metadata-input]').fill('Retained scalar-clear draft'); await page.locator('[data-metadata-input]').focus();
  return page.evaluate(() => {
    const head = document.head, counter = document.querySelector('[data-metadata-counter]');
    const resource = [...head.querySelectorAll('link[rel="stylesheet"]')].find(node => new URL(node.href).pathname.endsWith('/metadata-stream.css'));
    const jsonld = [...head.querySelectorAll('script[type="application/ld+json"]')].find(node => node.textContent.includes('metadata-stream-jsonld-v1'));
    const island = counter?.querySelector('[data-nextgo-island]'), output = counter?.querySelector('[data-count]'), button = counter?.querySelector('[data-increment]');
    if (!resource || !jsonld || !island || !output || !button) throw new Error('Actual frozen companions or Go counter nodes missing');
    globalThis.keptClearNodes = {body: document.querySelector('[data-metadata-clear]'), input: document.querySelector('[data-metadata-input]'), counter, island, output, button, resource, jsonld, description: head.querySelector('meta[name="description"]'), canonical: head.querySelector('link[rel="canonical"]'), robots: head.querySelector('meta[name="robots"]')};
    return {url: location.href, length: history.length, state: JSON.stringify(history.state), x: scrollX, y: scrollY, resourceURL: resource.href, jsonldText: jsonld.textContent};
  });
}
async function retainedAfterClear(page, before) {
  await finalHead(page);
  expect(await page.evaluate(before => {
    const kept = keptClearNodes, head = document.head;
    return kept.body === document.querySelector('[data-metadata-clear]') && kept.body.isConnected &&
      kept.input === document.querySelector('[data-metadata-input]') && document.activeElement === kept.input &&
      kept.counter === document.querySelector('[data-metadata-counter]') && kept.island === kept.counter.querySelector('[data-nextgo-island]') &&
      kept.output === kept.counter.querySelector('[data-count]') && kept.button === kept.counter.querySelector('[data-increment]') &&
      kept.description === head.querySelector('meta[name="description"]') && kept.description.parentNode === head && kept.description.content === '' &&
      !kept.canonical.isConnected && !kept.robots.isConnected &&
      kept.resource.parentNode === head && kept.resource === [...head.querySelectorAll('link[rel="stylesheet"]')].find(node => node.href === before.resourceURL) &&
      kept.jsonld.parentNode === head && kept.jsonld.textContent === before.jsonldText &&
      kept.jsonld === [...head.querySelectorAll('script[type="application/ld+json"]')].find(node => node.textContent.includes('metadata-stream-jsonld-v1'));
  }, before)).toBe(true);
  expect(await page.evaluate(() => ({url: location.href, length: history.length, state: JSON.stringify(history.state), x: scrollX, y: scrollY}))).toEqual({url: before.url, length: before.length, state: before.state, x: before.x, y: before.y});
  await expect(page.locator('[data-metadata-input]')).toHaveValue('Retained scalar-clear draft'); await expect(count(page)).toHaveText('4');
  await increment(page).click(); await expect(count(page)).toHaveText('5');
  await expect(page.locator('head link[href*="metadata-stream.css"]')).toHaveCount(1);
  expect(await page.locator('head script[type="application/ld+json"]').evaluateAll(nodes => nodes.filter(node => node.textContent.includes('metadata-stream-jsonld-v1')).length)).toBe(1);
}

test('initial deferred metadata clears inherited scalars while retaining real Go controls and head companions', async ({page, native}) => {
  const {gate, response, record} = await beginInitial(page, native);
  try {
    const headers = await response.allHeaders(); expect(headers['cache-control']).toContain('no-store');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-metadata-version', '1');
    const before = await captureLocalState(page); heldSource(gate, record);
    gate.release(); await retainedAfterClear(page, before); await page.waitForLoadState('load');
    await expect.poll(() => record.ended).toBe(true); await expect.poll(() => gate.entry.finished).toBe(true);
    expect(native.source).toHaveLength(1);
    await expect(page.locator('template[data-nextgo-metadata], [data-nextgo-metadata-ready]')).toHaveCount(0);
  } finally {gate.release();}
});

test('negotiated Go navigation clears inherited scalars without replacing the committed page or reloading the document', async ({page, native}) => {
  const initial = await beginInitial(page, native);
  try {heldSource(initial.gate, initial.record); initial.gate.release(); await finalHead(page); await page.waitForLoadState('load');}
  finally {initial.gate.release();}
  const historyBefore = await page.evaluate(() => {globalThis.scalarClearDocument = document; return history.length;});
  const gate = native.arm();
  try {
    await page.locator('[data-metadata-clear-refresh-link]').click(); await waitSource(gate);
    await expect(page).toHaveURL(native.origin + '/metadata-clear?cycle=next'); await fallbackHead(page); await ready(page);
    expect(await page.evaluate(() => scalarClearDocument === document)).toBe(true);
    expect(await page.evaluate(() => history.length)).toBe(historyBefore + 1);
    const record = [...native.wire].reverse().find(record => record.path === '/metadata-clear?cycle=next' && record.method === 'GET');
    await expect.poll(() => record?.frames.filter(frame => frame.type === 'shell').length).toBe(1);
    const shell = record.frames.find(frame => frame.type === 'shell');
    expect(record.metadataRequest).toBe('1'); expect(record.headers['x-nextgo-metadata']).toBe('1'); expect(record.headers['cache-control']).toContain('no-store');
    expect(shell.metadataPending).toBe(true); expect(shell.metadataOwner).toMatch(/^[a-f0-9]{32}$/);
    const before = await captureLocalState(page); heldSource(gate, record);
    gate.release(); await retainedAfterClear(page, before);
    await expect.poll(() => record.ended, {message: 'actual scalar-clear navigation stream EOF'}).toBe(true);
    await expect.poll(() => gate.entry.finished).toBe(true);
    const metadata = record.frames.filter(frame => frame.type === 'metadata'); expect(metadata).toHaveLength(1);
    expect(metadata[0]).toMatchObject({version: 1, metadataVersion: 1, owner: shell.metadataOwner, title: finalTitle, failed: false});
    expect(record.frames.filter(frame => frame.type === 'done')).toHaveLength(1);
    expect(record.frames.findIndex(frame => frame.type === 'metadata')).toBeLessThan(record.frames.findIndex(frame => frame.type === 'done'));
    expect(native.source).toHaveLength(2);
  } finally {gate.release();}
});
