import {test, expect} from '@playwright/test';

const base = '/metadata-site';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const navigation = page => page.getByRole('navigation', {name: 'Metadata pages'});
const icons = page => page.locator('head link[rel="icon"][data-nextgo-head]');
const apple = page => page.locator('head link[rel="apple-touch-icon"][data-nextgo-head]');
const manifests = page => page.locator('head link[rel="manifest"][data-nextgo-head]');
const count = page => page.locator('[data-count]');
function observeAssets(page) {
  const responses = [];
  page.on('response', response => {
    if (/\/(?:assets|_nextgo)\//.test(new URL(response.url()).pathname)) responses.push({url: response.url(), status: response.status()});
  });
  return responses;
}
function expectUsefulAssets(responses) {
  expect(responses.filter(response => response.status >= 400)).toEqual([]);
  expect(responses.some(response => response.url.endsWith('/assets/style.css') && response.status === 200)).toBe(true);
  expect(responses.some(response => response.url.endsWith('/app.wasm') && response.status === 200)).toBe(true);
}
async function defaults(page) {
  await expect(icons(page)).toHaveCount(2);
  expect(await icons(page).evaluateAll(nodes => nodes.map(node => node.getAttribute('href')).sort())).toEqual([`${base}/favicon.ico`, `${base}/icon.png`]);
  await expect(apple(page)).toHaveAttribute('href', `${base}/apple-icon.png`);
  await expect(manifests(page)).toHaveCount(1);
  await expect(manifests(page)).toHaveAttribute('href', `${base}/manifest.webmanifest`);
}

test('native SSR publishes global mounted head defaults and useful resource links without JavaScript', async ({browser, baseURL}) => {
  const context = await browser.newContext({javaScriptEnabled: false, baseURL});
  const page = await context.newPage();
  const response = await page.goto(base);
  expect(response.status()).toBe(200);
  await expect(page.getByRole('heading', {name: 'Small files. Clear ownership.'})).toBeVisible();
  await defaults(page);
  await expect(page.getByRole('link', {name: 'manifest.webmanifest', exact: true})).toHaveAttribute('href', `${base}/manifest.webmanifest`);
  await expect(page.getByRole('button', {name: 'Increase retained count'})).toBeDisabled();
  await expect(count(page)).toHaveText('2');
  await expect(page.locator('link[rel="stylesheet"]')).toHaveAttribute('href', 'http://127.0.0.1:4206/metadata-assets/assets/style.css');
  await context.close();
});

test('all managed resources implement GET HEAD ETag validation at one global mount', async ({request}) => {
  for (const [name, type] of [['robots.txt', 'text/plain'], ['sitemap.xml', 'application/xml'], ['manifest.webmanifest', 'application/manifest+json'], ['icon.png', 'image/png'], ['apple-icon.png', 'image/png'], ['favicon.ico', 'image/x-icon']]) {
    const response = await request.get(`${base}/${name}`, {headers: {'Accept-Encoding': 'identity', Cookie: 'private=ignored', 'Accept-Language': 'fr'}});
    expect(response.status(), name).toBe(200);
    expect(response.headers()['content-type'], name).toContain(type);
    expect(response.headers()['x-content-type-options'], name).toBe('nosniff');
    expect(response.headers()['cache-control'], name).toBe('public, max-age=0, must-revalidate');
    const bytes = await response.body();
    expect(bytes.length, name).toBeGreaterThan(0);
    const etag = response.headers().etag;
    expect(etag, name).toMatch(/^"[a-f0-9]{64}"$/);
    const head = await request.head(`${base}/${name}`, {headers: {'Accept-Encoding': 'identity'}});
    expect(head.status(), name).toBe(200);
    expect((await head.body()).length, name).toBe(0);
    expect(head.headers().etag, name).toBe(etag);
    expect(head.headers()['content-length'], name).toBe(String(bytes.length));
    const conditional = await request.get(`${base}/${name}`, {headers: {'If-None-Match': `W/${etag}`}});
    expect(conditional.status(), name).toBe(304);
    expect((await conditional.body()).length, name).toBe(0);
  }
  expect(await (await request.get(`${base}/robots.txt`)).text()).toContain('Sitemap: https://example.test/metadata-site/sitemap.xml');
  expect(await (await request.get(`${base}/sitemap.xml`)).text()).toContain('https://example.test/metadata-site/about');
  const manifest = await (await request.get(`${base}/manifest.webmanifest`)).json();
  expect(manifest).toMatchObject({start_url: './', scope: './', icons: [{src: 'icon.png', sizes: '32x32', type: 'image/png'}]});
  expect((await request.get('/robots.txt')).status()).toBe(404);
  expect((await request.get('http://127.0.0.1:4206/metadata-assets/robots.txt')).status()).toBe(404);
  // The framework owns canonical slash redirects before route dispatch. Inspect
  // that response itself rather than Playwright's automatically followed PNG.
  const slash = await request.get(`${base}/icon.png/`, {maxRedirects: 0});
  expect(slash.status()).toBe(308);
  expect(slash.headers().location).toBe(`${base}/icon.png`);
  expect(slash.headers()['content-type']).toContain('text/html');
});

test('CDN Go WASM adopts the SSR counter and default links stay unique across inherited navigation', async ({page}) => {
  const assets = observeAssets(page);
  let release;
  const gate = new Promise(resolve => {release = resolve;});
  await page.route('**/app.wasm', async route => {await gate; await route.continue();});
  try {
    await page.goto(base, {waitUntil: 'domcontentloaded'});
    await expect(count(page)).toHaveText('2');
    await page.evaluate(() => {window.metadataCount = document.querySelector('[data-count]'); window.metadataDocument = true;});
    await defaults(page);
    release(); await ready(page);
    expectUsefulAssets(assets);
    expect(await page.evaluate(() => window.metadataCount === document.querySelector('[data-count]'))).toBe(true);
    await page.getByRole('button', {name: 'Increase retained count'}).click();
    await expect(count(page)).toHaveText('3');
    await navigation(page).getByRole('link', {name: 'About', exact: true}).click();
    await expect(page.getByRole('heading', {name: 'One application, shared defaults.'})).toBeVisible();
    await defaults(page); await expect(count(page)).toHaveText('3');
    expect(await page.evaluate(() => window.metadataDocument && window.metadataCount === document.querySelector('[data-count]'))).toBe(true);
    expectUsefulAssets(assets);
  } finally {release();}
});

test('soft navigation replaces and clears icon and manifest groups then history restores them', async ({page}) => {
  await page.goto(base); await ready(page);
  await page.evaluate(() => {window.metadataHistoryDocument = true;});
  await page.getByRole('button', {name: 'Increase retained count'}).click();
  await navigation(page).getByRole('link', {name: 'Explicit metadata', exact: true}).click();
  await expect(page.getByRole('heading', {name: 'An explicit choice wins.'})).toBeVisible();
  await expect(icons(page)).toHaveCount(1);
  await expect(icons(page)).toHaveAttribute('href', `${base}/apple-icon.png?explicit=1`);
  await expect(apple(page)).toHaveCount(0);
  await expect(manifests(page)).toHaveAttribute('href', `${base}/manifest.webmanifest?explicit=1`);
  await navigation(page).getByRole('link', {name: 'Disabled metadata', exact: true}).click();
  await expect(page.getByRole('heading', {name: 'An empty choice clears defaults.'})).toBeVisible();
  await expect(icons(page)).toHaveCount(0); await expect(apple(page)).toHaveCount(0); await expect(manifests(page)).toHaveCount(0);
  await expect(count(page)).toHaveText('3');
  await page.goBack();
  await expect(page.getByRole('heading', {name: 'An explicit choice wins.'})).toBeVisible();
  await expect(manifests(page)).toHaveAttribute('href', `${base}/manifest.webmanifest?explicit=1`);
  await page.goBack();
  await expect(page.getByRole('heading', {name: 'Small files. Clear ownership.'})).toBeVisible();
  await defaults(page); await expect(count(page)).toHaveText('3');
  await page.goForward();
  await expect(page.getByRole('heading', {name: 'An explicit choice wins.'})).toBeVisible();
  await expect(icons(page)).toHaveCount(1);
  expect(await page.evaluate(() => window.metadataHistoryDocument)).toBe(true);
});

test('an optional separately served static export publishes metadata and updates its head in Go', async ({page, request}) => {
  const configured = process.env.NEXTGO_METADATA_STATIC_URL;
  test.skip(!configured, 'Supply NEXTGO_METADATA_STATIC_URL for a separately built and served static export.');
  const root = new URL(configured.endsWith('/') ? configured : configured+'/');
  const assets = observeAssets(page);
  await page.goto(root.href); await ready(page);
  expectUsefulAssets(assets);
  await expect(manifests(page)).toHaveCount(1);
  const manifestURL = await manifests(page).getAttribute('href');
  const manifest = await request.get(new URL(manifestURL, root).href);
  expect(manifest.status()).toBe(200);
  expect(await manifest.json()).toMatchObject({start_url: './', scope: './'});
  for (const name of ['robots.txt', 'sitemap.xml', 'icon.png', 'apple-icon.png', 'favicon.ico']) {
    expect((await request.get(new URL(name, root).href)).status(), name).toBe(200);
  }
  await page.getByRole('button', {name: 'Increase retained count'}).click();
  await navigation(page).getByRole('link', {name: 'Disabled metadata', exact: true}).click();
  await expect(manifests(page)).toHaveCount(0); await expect(icons(page)).toHaveCount(0); await expect(count(page)).toHaveText('3');
  expectUsefulAssets(assets);
});
