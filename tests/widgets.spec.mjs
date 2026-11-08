import {test as base, expect} from '@playwright/test';
import {startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';
import {startSocialMetadataStaticDeployment} from './social-metadata-static-fixture.mjs';

const test = base.extend({
  widgetTransport: ['http', {option: true}],
  native: async ({page, widgetTransport}, use, info) => {
    const mount = widgetTransport === 'http' ? '' : '/lambda-features';
    const fixture = widgetTransport === 'static'
      ? await startSocialMetadataStaticDeployment({root: process.env.NEXTGO_WIDGET_STATIC_ROOT, sha256: process.env.NEXTGO_WIDGET_STATIC_SHA256, metadataCount: 1, port: 4289,
        pages: ['lambda-features/index.html', 'lambda-features/lazy/index.html', 'lambda-features/esm/index.html', 'lambda-features/lazy-esm/index.html', 'lambda-features/late/index.html', 'lambda-features/lazy-late/index.html', 'lambda-features/failure/index.html', 'lambda-features/about/index.html', 'lambda-features/_nextgo/data/index.json']})
      : await startMetadataDeployment(widgetTransport, {binary: process.env.NEXTGO_WIDGET_BINARY, sha256: process.env.NEXTGO_WIDGET_SHA256, port: 4289,
        startupEnvironment: {NEXTGO_METADATA_BASE_PATH: mount, NEXTGO_METADATA_ASSET_PREFIX: mount ? '/lambda-assets' : ''}});
    fixture.mount = mount;
    fixture.transport = widgetTransport;
    fixture.url = path => fixture.origin + mount + path;
    await page.addInitScript(() => {
      const observer = new MutationObserver(() => {
        const fallback = document.querySelector('[data-widget-fallback]');
        if (fallback) {
          window.widgetOriginalFallback = fallback;
          window.widgetOriginalHost = fallback.closest('[data-widget-host]');
          observer.disconnect();
        }
      });
      observer.observe(document, {subtree: true, childList: true});
    });
    try { await use(fixture); } finally {
      const failures = [];
      try { await page.close(); } catch (error) { failures.push(error); }
      try { await fixture.close(); } catch (error) { failures.push(error); }
      const proof = {transport: widgetTransport, sha256: process.env.NEXTGO_WIDGET_SHA256, artifact: fixture.artifact ?? null,
        cleanup: fixture.cleanupProof, runtime: fixture.runtimeProof?.() ?? null, sourceRequests: fixture.sourceRecords.length};
      await info.attach('widget-owned-transport-proof', {body: JSON.stringify(proof), contentType: 'application/json'});
      expect(proof.sourceRequests).toBe(0);
      for (const [key, value] of Object.entries(proof.cleanup)) expect(value, key).toBe(key === 'runtimeGatewayClosed' ? widgetTransport !== 'http' : true);
      if (failures.length) throw new AggregateError(failures, 'Widget fixture cleanup failed');
    }
  },
});
const status = page => page.locator('[data-widget-status]');
const ready = async page => {
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  await expect(status(page)).toHaveText('Chart ready');
  await expect(page.locator('[data-widget-host] .uplot')).toHaveCount(1);
};
function errors(page) {
  const found = [];
  page.on('pageerror', error => found.push(error.message));
  page.on('console', message => { if (message.type() === 'error') found.push(message.text()); });
  return found;
}
const fallbackRestored = page => page.evaluate(() => window.widgetOriginalFallback === document.querySelector('[data-widget-fallback]'));

test.describe('without JavaScript', () => {
  test.use({javaScriptEnabled: false});
  test('eager and lazy pages retain readable data and native navigation', async ({page, native}) => {
    const downloads = [];
    page.on('request', request => { if (/\.wasm(?:\?|$)|uPlot-1\.6\.32\.(?:iife|esm)|chart-module-v1\.js/.test(request.url())) downloads.push(request.url()); });
    for (const path of ['/', '/lazy', '/esm', '/lazy-esm']) {
      await page.goto(native.url(path));
      await expect(page.getByRole('table', {name: 'Sample data', exact: true})).toBeVisible();
      await expect(page.locator('tbody tr')).toHaveCount(4);
      await expect(page.locator('[data-widget-fallback]')).toBeVisible();
      await expect(page.getByRole('button', {name: 'Start chart', exact: true})).toBeDisabled();
    }
    await page.getByRole('link', {name: 'Chart details', exact: true}).click();
    await expect(page).toHaveTitle('Chart details');
    expect(downloads).toEqual([]);
  });
});

for (const [label, path] of [['eager', '/'], ['lazy', '/lazy'], ['ESM eager', '/esm'], ['ESM lazy', '/lazy-esm']]) {
  test(`${label} library subtree closes and remounts with one dependency and exact SSR fallback`, async ({page, native}) => {
    const failed = errors(page), downloads = [];
    page.on('request', request => { if (request.url().includes(path.includes('esm') ? '/uPlot-1.6.32.esm.js' : '/uPlot-1.6.32.iife.min.js')) downloads.push(request.url()); });
    const response = await page.goto(native.url(path));
    if (native.transport !== 'static') {
      const policy = response.headers()['content-security-policy'];
      expect(policy).toMatch(/script-src 'nonce-[^']+' 'strict-dynamic' 'wasm-unsafe-eval'/);
      expect(policy.split(';').find(part => part.trim().startsWith('script-src'))).not.toContain("'unsafe-inline'");
    }
    await ready(page);
    expect(await page.evaluate(() => window.widgetScriptSettings)).toEqual({site: 'example.test', label: 'Go "owned" <chart> & café', beforeGo: true});
    if (!path.includes('esm')) {
      const dependency = page.locator('script[data-nextgo-dependency="uplot-1.6.32"]');
      await expect(dependency).toHaveAttribute('data-chart-library', 'uplot');
      await expect(dependency).toHaveAttribute('data-chart-label', 'Go "owned" <chart> & café');
    }
    if (path.includes('esm')) await expect(page.locator('[data-widget-host]')).toHaveAttribute('data-module-mount', '1');
    await expect(page.getByRole('table', {name: 'Sample data', exact: true})).toBeVisible();
    await page.getByRole('button', {name: 'Stop chart', exact: true}).click();
    await expect(status(page)).toHaveText('Chart stopped');
    await expect(page.locator('.uplot')).toHaveCount(0);
    expect(await fallbackRestored(page)).toBe(true);
    await page.getByRole('button', {name: 'Start chart', exact: true}).click();
    await ready(page);
    if (path.includes('esm')) {
      await expect(page.locator('[data-widget-host]')).toHaveAttribute('data-module-mount', '2');
      const document = await page.locator('html').elementHandle();
      await page.getByRole('link', {name: path === '/esm' ? 'Lazy ESM chart' : 'ESM chart', exact: true}).click();
      await ready(page);
      await expect(page.locator('[data-widget-host]')).toHaveAttribute('data-module-mount', '3');
      expect(await document.evaluate(node => node === window.document.documentElement)).toBe(true);
      expect(await page.evaluate(() => typeof window.uPlot)).toBe('undefined');
    }
    expect(downloads).toHaveLength(1);
    expect(failed).toEqual([]);
  });
}

for (const [label, path] of [['eager', '/late'], ['lazy', '/lazy-late']]) {
  test(`${label} late constructor cannot revive a removed page`, async ({page, native}) => {
    const failed = errors(page);
    await page.goto(native.url(path));
    await page.waitForFunction(() => document.querySelector('[data-widget-status]')?.textContent === 'Preparing chart');
    await page.getByRole('link', {name: 'Chart details', exact: true}).click();
    await expect(page).toHaveTitle('Chart details');
    await expect.poll(() => page.evaluate(() => !!window.widgetOriginalHost && window.widgetOriginalHost.querySelector('.uplot') === null && window.widgetOriginalHost.firstChild === window.widgetOriginalFallback)).toBe(true);
    // Observe past the deliberate 350ms external-constructor completion.
    await page.waitForTimeout(500);
    expect(await page.evaluate(() => window.widgetOriginalHost.querySelector('.uplot') === null && window.widgetOriginalHost.firstChild === window.widgetOriginalFallback)).toBe(true);
    expect(failed).toEqual([]);
  });
}

test('closing and immediately restarting waits for the previous late constructor', async ({page, native}) => {
  const failed = errors(page);
  await page.goto(native.url('/late'));
  await page.waitForFunction(() => document.querySelector('[data-widget-status]')?.textContent === 'Preparing chart');
  await page.getByRole('button', {name: 'Stop chart', exact: true}).click();
  await page.getByRole('button', {name: 'Start chart', exact: true}).click();
  await ready(page);
  await page.waitForTimeout(500);
  await expect(page.locator('.uplot')).toHaveCount(1);
  await expect(status(page)).toHaveText('Chart ready');
  expect(failed).toEqual([]);
});

test('failed native module URL stays failed without retrying or losing fallback', async ({page, native}) => {
  let downloads = 0;
  const exceptions = [];
  page.on('pageerror', error => exceptions.push(error.message));
  await page.route('**/chart-module-v1.js', async route => {
    downloads++;
    await route.fulfill({status: 404, contentType: 'text/plain', body: 'fixture unavailable'});
  });
  await page.goto(native.url('/esm'));
  await expect(status(page)).toHaveText('Chart unavailable');
  expect(await fallbackRestored(page)).toBe(true);
  for (let i = 0; i < 3; i++) {
    await page.getByRole('button', {name: 'Start chart', exact: true}).click();
    await expect(status(page)).toHaveText('Chart unavailable');
    expect(await fallbackRestored(page)).toBe(true);
  }
  expect(downloads).toBe(1);
  expect(exceptions).toEqual([]);
});

test('native transitive import finishing after navigation cannot revive its widget', async ({page, native}) => {
  const failed = errors(page);
  let release, entered;
  const held = new Promise(resolve => { release = resolve; });
  const requested = new Promise(resolve => { entered = resolve; });
  let downloads = 0;
  await page.route('**/uPlot-1.6.32.esm.js', async route => { downloads++; entered(); await held; await route.continue(); });
  try {
    await page.goto(native.url('/lazy-esm'), {waitUntil: 'domcontentloaded'});
    await requested;
    await page.getByRole('link', {name: 'Chart details', exact: true}).click();
    await expect(page).toHaveTitle('Chart details');
    release();
    await page.getByRole('link', {name: 'Chart home', exact: true}).click();
    await ready(page);
    await page.getByRole('link', {name: 'ESM chart', exact: true}).click();
    await ready(page);
    await expect(page.locator('[data-widget-host]')).toHaveAttribute('data-module-mount', '1');
    expect(await page.evaluate(() => window.widgetOriginalHost.querySelector('.uplot') === null && window.widgetOriginalHost.firstChild === window.widgetOriginalFallback)).toBe(true);
    expect(downloads).toBe(1);
    expect(failed).toEqual([]);
  } finally { release(); }
});

test('failure after real library construction destroys partial resources and keeps data', async ({page, native}) => {
  const failed = errors(page);
  await page.goto(native.url('/failure'));
  await expect(status(page)).toHaveText('Chart unavailable');
  for (let n = 0; n < 3; n++) {
    await expect(page.locator('.uplot')).toHaveCount(0);
    expect(await fallbackRestored(page)).toBe(true);
    await expect(page.getByRole('table', {name: 'Sample data', exact: true})).toBeVisible();
    await page.getByRole('button', {name: 'Start chart', exact: true}).click();
    await expect(status(page)).toHaveText('Chart unavailable');
  }
  expect(failed).toEqual([]);
});

test('failed dependency can retry without losing its SSR fallback', async ({page, native}) => {
  let downloads = 0;
  const exceptions = [];
  page.on('pageerror', error => exceptions.push(error.message));
  await page.route('**/uPlot-1.6.32.iife.min.js', async route => {
    if (++downloads === 1) await route.fulfill({status: 404, contentType: 'text/plain', body: 'fixture unavailable'});
    else await route.continue();
  });
  await page.goto(native.url('/'));
  await expect(status(page)).toHaveText('Chart unavailable');
  expect(await fallbackRestored(page)).toBe(true);
  await page.getByRole('button', {name: 'Start chart', exact: true}).click();
  await ready(page);
  expect(downloads).toBe(2);
  expect(exceptions).toEqual([]);
});

test('dependency completion after navigation does not mount a stale chart', async ({page, native}) => {
  const failed = errors(page);
  let release, entered;
  const held = new Promise(resolve => { release = resolve; });
  const requested = new Promise(resolve => { entered = resolve; });
  await page.route('**/uPlot-1.6.32.iife.min.js', async route => { entered(); await held; await route.continue(); });
  try {
    await page.goto(native.url('/'), {waitUntil: 'domcontentloaded'});
    await requested;
    await page.getByRole('link', {name: 'Chart details', exact: true}).click();
    await expect(page).toHaveTitle('Chart details');
    release();
    await expect.poll(() => page.evaluate(() => typeof window.uPlot)).toBe('function');
    expect(await page.evaluate(() => window.widgetOriginalHost.querySelector('.uplot') === null && window.widgetOriginalHost.firstChild === window.widgetOriginalFallback)).toBe(true);
    await page.getByRole('link', {name: 'Chart home', exact: true}).click();
    await ready(page);
    expect(failed).toEqual([]);
  } finally { release(); }
});
