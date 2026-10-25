import {test, expect} from '@playwright/test';

const resources = '/lambda-features/resources';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const resourceRequest = request => new URL(request.url()).pathname === '/lambda-features/api/resources';

test('real Lambda SSR stays useful before lazy WASM and hydration retains existing nodes', async ({browser, page, baseURL}, info) => {
  const nativeContext = await browser.newContext({baseURL, javaScriptEnabled: false});
  try {
    const native = await nativeContext.newPage(), response = await native.goto(resources);
    expect(response.status()).toBe(200);
    expect(response.headers()['x-nextgo-fixture-lambda-mode']).toBe(info.project.metadata.mode);
    expect(response.headers()['x-nextgo-requires-server']).toBe('1');
    await expect(native.locator('[data-resource-primary]')).toContainText('Public Go resource');
    await expect(native.locator('[data-resource-refresh]')).toBeDisabled();
  } finally {await nativeContext.close();}
  let release;
  const blocked = new Promise(resolve => {release = resolve;});
  await page.route(/\/module-[a-f0-9]{64}\.wasm$/, async route => {await blocked; await route.continue();});
  const requests = [];
  page.on('request', request => {if (resourceRequest(request)) requests.push(request.url());});
  try {
    const response = await page.goto(resources);
    const policy = response.headers()['content-security-policy'];
    expect(policy).toContain("default-src 'self'");
    const nonce = /'nonce-([^']+)'/.exec(policy)?.[1]; expect(nonce).toBeTruthy();
    await expect(page.locator('[data-nextgo-island="resourcepanel"]')).toHaveAttribute('data-nextgo-hydrated', 'loading');
    await page.evaluate(() => {window.originalResource = document.querySelector('[data-resource-primary]'); window.originalText = window.originalResource.firstChild;});
    await expect(page.locator('[data-resource-primary]')).toContainText('Public Go resource');
    expect(await page.locator('script[nonce]').evaluateAll(scripts => scripts.map(script => script.nonce))).toEqual([nonce, nonce]);
    release(); await ready(page);
    expect(await page.evaluate(() => window.originalResource === document.querySelector('[data-resource-primary]') && window.originalText === window.originalResource.firstChild)).toBe(true);
    await expect(page.locator('[data-resource-status]')).toHaveText('Adopted the server seed without fetching.');
    expect(requests).toHaveLength(0);
  } finally {release();}
});

test('scoped Go resources reload, recover and dispose across Lambda navigation and history without a document reload', async ({page}) => {
  const requests = [];
  page.on('request', request => {if (resourceRequest(request)) requests.push(request.url());});
  await page.goto(resources); await ready(page);
  const original = await page.locator('[data-resource-primary]').textContent();
  await page.locator('[data-resource-reuse]').click();
  await expect(page.locator('[data-resource-status]')).toContainText('Public resource ready.');
  expect(requests).toHaveLength(0);
  await page.locator('[data-resource-refresh]').click();
  await expect(page.locator('[data-resource-status]')).toContainText('Public resource ready.');
  await expect(page.locator('[data-resource-primary]')).not.toHaveText(original);
  await expect(page.locator('[data-resource-secondary]')).toHaveText(await page.locator('[data-resource-primary]').textContent());
  expect(requests).toHaveLength(1);
  await page.locator('[data-resource-fail]').click();
  await expect(page.locator('[data-resource-status]')).toHaveText('The server request failed. Retry explicitly to recover.');
  expect(requests).toHaveLength(2);
  await page.locator('[data-resource-retry]').click();
  await expect(page.locator('[data-resource-status]')).toContainText('Public resource ready.');
  expect(requests).toHaveLength(3);
  await page.evaluate(() => {window.lambdaDocument = true; window.oldResourcePanel = document.querySelector('[data-nextgo-island="resourcepanel"]'); window.oldResourceButton = window.oldResourcePanel.querySelector('[data-resource-refresh]');});
  const navigation = page.waitForResponse(response => new URL(response.url()).pathname === '/lambda-features/about' && response.request().headers()['x-nextgo-navigate'] === '1');
  await page.getByRole('navigation', {name: 'Main'}).getByRole('link', {name: 'Architecture', exact: true}).click();
  const response = await navigation;
  expect(response.headers()['x-nextgo-protocol']).toBe('3');
  await expect(page).toHaveTitle('Architecture — Next.go'); await ready(page);
  expect(await page.evaluate(() => window.lambdaDocument && !window.oldResourcePanel.isConnected && window.oldResourcePanel.getAttribute('data-resource-closed') === 'true')).toBe(true);
  await page.evaluate(() => window.oldResourceButton.click()); expect(requests).toHaveLength(3);
  await page.goBack(); await ready(page);
  await expect(page).toHaveTitle('Scoped client resources — Next.go');
  await expect(page.locator('[data-resource-status]')).toHaveText('Adopted the server seed without fetching.');
  expect(await page.evaluate(() => window.lambdaDocument)).toBe(true);
  await page.goForward(); await expect(page).toHaveTitle('Architecture — Next.go');
  expect(await page.evaluate(() => window.lambdaDocument)).toBe(true);
});

test('actual Lambda JSON and PNG APIs, nonce CSP and sandboxed native media retain browser behavior', async ({page}, info) => {
  const response = await page.goto('/lambda-features/embeds');
  expect(response.headers()['x-nextgo-fixture-lambda-mode']).toBe(info.project.metadata.mode);
  expect(response.headers()['x-nextgo-requires-server']).toBe('1');
  await ready(page);
  await expect(page.frameLocator('#safe-frame').locator('#embed-message')).toHaveText('A local document, isolated by the browser sandbox.');
  await expect(page.frameLocator('#safe-frame').locator('#embed-referrer')).toHaveText('No referrer sent');
  expect(await page.locator('#safe-frame').evaluate(frame => frame.contentDocument === null)).toBe(true);
  await expect(page.getByRole('link', {name: 'Open Local sandboxed document', exact: true})).toBeVisible();
  await expect.poll(() => page.locator('#native-video').evaluate(video => video.readyState)).toBeGreaterThanOrEqual(1);
  await expect.poll(() => page.locator('#native-audio').evaluate(audio => audio.readyState)).toBeGreaterThanOrEqual(1);
  expect(await page.locator('#native-video').evaluate(video => video.duration)).toBeCloseTo(2, 1);
  expect(await page.locator('#native-audio').evaluate(audio => audio.duration)).toBeCloseTo(0.3, 1);
  await page.locator('#native-video').evaluate(video => {video.textTracks[0].mode = 'showing';});
  await expect.poll(() => page.locator('#native-video').evaluate(video => video.textTracks[0]?.cues?.length || 0)).toBe(1);
  const apis = await page.evaluate(async () => {
    const json = await fetch('/lambda-features/api/resources');
    const png = await fetch('/lambda-features/api/social?title=Lambda%20browser&description=Actual%20Go%20PNG');
    const bytes = new Uint8Array(await png.arrayBuffer()), view = new DataView(bytes.buffer);
    const embed = await fetch('/lambda-features/api/embed?message=%3Cscript%3Ebad()%3C%2Fscript%3E');
    return {jsonStatus: json.status, cache: json.headers.get('cache-control'), json: await json.json(), pngStatus: png.status, pngType: png.headers.get('content-type'), signature: Array.from(bytes.slice(0, 8)), width: view.getUint32(16), height: view.getUint32(20), embedCSP: embed.headers.get('content-security-policy'), embed: await embed.text()};
  });
  expect(apis.jsonStatus).toBe(200); expect(apis.cache).toBe('public, max-age=30'); expect(apis.json.message).toBe('Public Go resource');
  expect(apis.pngStatus).toBe(200); expect(apis.pngType).toContain('image/png'); expect(apis.signature).toEqual([137, 80, 78, 71, 13, 10, 26, 10]);
  expect([apis.width, apis.height]).toEqual([1200, 630]);
  expect(apis.embedCSP).toContain("default-src 'none'"); expect(apis.embed).toContain('&lt;script&gt;bad()&lt;/script&gt;'); expect(apis.embed).not.toContain('<script>');
  await page.goto('/lambda-features/social-image'); await ready(page);
  const image = page.locator('[data-social-image-example] figure img');
  await expect.poll(() => image.evaluate(element => element.complete && element.naturalWidth === 1200 && element.naturalHeight === 630)).toBe(true);
});
