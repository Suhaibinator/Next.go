import {test, expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';

const route = '/resource-demo/resources';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const panel = page => page.locator('[data-nextgo-island="resourcepanel"]');
const status = page => page.locator('[data-resource-status]');
const resourceRequest = request => new URL(request.url()).pathname === '/resource-demo/api/resources';
const records = async request => (await request.get('/_fixture/resources/records')).json();

// Delaying the real module does not replace its bytes or issue a resource GET.
test('native public SSR stays useful without JavaScript and is adopted without a request after delayed lazy WASM', async ({browser, page, baseURL}) => {
  const context = await browser.newContext({javaScriptEnabled: false, baseURL});
  const native = await context.newPage();
  const response = await native.goto(route);
  expect(response.status()).toBe(200);
  await expect(native.locator('[data-resource-primary]')).toContainText('Public Go resource');
  await expect(native.locator('[data-resource-refresh]')).toBeDisabled();
  await expect(native.getByRole('link', {name: 'Read the public JSON endpoint'})).toHaveAttribute('href', '/resource-demo/api/resources');
  await context.close();
  let release;
  const blocked = new Promise(resolve => {release = resolve;});
  await page.route(/\/module-[a-f0-9]{64}\.wasm$/, async route => {await blocked; await route.continue();});
  const requests = [];
  page.on('request', request => {if (resourceRequest(request)) requests.push(request.url());});
  try {
    const document = await page.goto(route);
    expect(document.headers()['content-security-policy']).toContain("default-src 'self'");
    await expect(panel(page)).toHaveAttribute('data-nextgo-hydrated', 'loading');
    await page.evaluate(() => {window.resourceNode = document.querySelector('[data-resource-primary]'); window.resourceText = window.resourceNode.firstChild;});
    await expect(page.locator('[data-resource-primary]')).toContainText('Public Go resource');
    release();
    await ready(page);
    expect(await page.evaluate(() => window.resourceNode === document.querySelector('[data-resource-primary]') && window.resourceText === window.resourceNode.firstChild)).toBe(true);
    await expect(status(page)).toHaveText('Adopted the server seed without fetching.');
    expect(requests).toHaveLength(0);
  } finally {release();}
});

test('two lazy scoped views reuse a public seed then deduplicate one real mounted HTTP reload across the host epoch', async ({page}) => {
  const requests = [];
  page.on('request', request => {if (resourceRequest(request)) requests.push(request.url());});
  await page.goto(route); await ready(page);
  const initial = await page.locator('[data-resource-primary]').textContent();
  await page.locator('[data-resource-reuse]').click();
  await expect(status(page)).toContainText('Public resource ready.');
  await expect(page.locator('[data-resource-secondary]')).toHaveText(initial);
  expect(requests).toHaveLength(0);
  const response = page.waitForResponse(response => resourceRequest(response.request()));
  await page.locator('[data-resource-refresh]').click();
  expect((await response).status()).toBe(200);
  await expect(status(page)).toContainText('Public resource ready.');
  const current = await page.locator('[data-resource-primary]').textContent();
  expect(current).not.toBe(initial);
  await expect(page.locator('[data-resource-secondary]')).toHaveText(current);
  expect(requests).toHaveLength(1);
  await page.locator('[data-resource-reuse]').click();
  await expect(status(page)).toContainText('Public resource ready.');
  expect(requests).toHaveLength(1);
});

test('a real server failure remains local and retries once only after an explicit user action', async ({page}) => {
  const requests = [];
  page.on('request', request => {if (resourceRequest(request)) requests.push(request.url());});
  await page.goto(route); await ready(page);
  const failure = page.waitForResponse(response => resourceRequest(response.request()) && response.status() === 503);
  await page.locator('[data-resource-fail]').click(); await failure;
  await expect(status(page)).toHaveText('The server request failed. Retry explicitly to recover.');
  await expect(page.locator('[data-resource-retry]')).toBeEnabled();
  expect(requests).toHaveLength(1);
  await expect(page.getByRole('heading', {name: 'Scoped Go async resources'})).toBeVisible();
  await page.locator('[data-resource-retry]').click();
  await expect(status(page)).toContainText('Public resource ready.');
  expect(requests).toHaveLength(2);
  await expect(page.locator('[data-resource-secondary]')).toHaveText(await page.locator('[data-resource-primary]').textContent());
});

test('navigation disposes a lazy resource, aborts its held real HTTP body and suppresses detached controls', async ({page, request}) => {
  const key = randomUUID();
  await page.goto(route); await ready(page);
  await page.evaluate(() => {window.resourceDocument = true; window.oldResource = document.querySelector('[data-nextgo-island="resourcepanel"]'); window.oldResourceButton = window.oldResource.querySelector('[data-resource-refresh]');});
  await page.setExtraHTTPHeaders({'X-Resource-Gate': key});
  try {
    await page.locator('[data-resource-refresh]').click();
    await expect.poll(async () => (await records(request)).find(value => value.key === key)?.held).toBe(true);
    await expect(status(page)).toHaveText('Loading public data…');
    await page.getByRole('navigation', {name: 'Main'}).getByRole('link', {name: 'Architecture', exact: true}).click();
    await expect(page).toHaveTitle('Architecture — Next.go'); await ready(page);
    await expect.poll(async () => (await records(request)).find(value => value.key === key)?.aborted).toBe(true);
    expect(await page.evaluate(() => window.resourceDocument && !window.oldResource.isConnected && window.oldResource.getAttribute('data-resource-closed') === 'true')).toBe(true);
    const count = (await records(request)).filter(value => value.key === key).length;
    await page.evaluate(() => window.oldResourceButton.click());
    expect((await records(request)).filter(value => value.key === key)).toHaveLength(count);
    await page.setExtraHTTPHeaders({});
    await page.goBack(); await ready(page);
    await expect(status(page)).toHaveText('Adopted the server seed without fetching.');
    expect(await page.evaluate(() => window.resourceDocument)).toBe(true);
  } finally {await request.post(`/_fixture/resources/release?key=${key}`);}
});

test('a real mutation during delayed lazy mounting rejects the old seed and later navigation adopts a fresh seed', async ({page}) => {
  let release;
  const blocked = new Promise(resolve => {release = resolve;});
  await page.route(/\/module-[a-f0-9]{64}\.wasm$/, async route => {await blocked; await route.continue();});
  const resourceRequests = [], mutationRequests = [];
  page.on('request', request => {
    if (resourceRequest(request)) resourceRequests.push(request.url());
    if (request.method() === 'POST' && new URL(request.url()).pathname.endsWith('/_nextgo/actions/generated/add/v1')) mutationRequests.push(request.url());
  });
  try {
    await page.goto(route);
    await expect(panel(page)).toHaveAttribute('data-nextgo-hydrated', 'loading');
    await expect(page.locator('[data-resource-primary]')).toContainText('Public Go resource');
    // Exercise the real core mutation RPC/HTTP transport while this module is
    // unavailable. No resource request or response bytes are mocked.
    const result = await page.evaluate(async () => {
      window.resourceLateDocument = true;
      const metadata = {Method: 'POST', Headers: {'Content-Type': 'application/json', 'Accept': 'application/json', 'X-Nextgo-Action-Protocol': '1', 'X-Nextgo-Action-Revision': 'v1'}, MaxBytes: 2048};
      const body = new TextEncoder().encode(JSON.stringify({amount: 1, fail: false}));
      const validate = raw => {
        if (raw === null) return '';
        const response = JSON.parse(raw);
        if (response.StatusCode !== 200) return 'Expected a successful real mutation';
        const value = JSON.parse(atob(response.Body));
        return Number.isInteger(value.data?.count) ? '' : 'Missing typed mutation result';
      };
      return JSON.parse(await window.__nextgoModuleHost.mutation('/resource-demo/_nextgo/actions/generated/add/v1', JSON.stringify(metadata), body, validate, new AbortController().signal));
    });
    expect(result.PreError || '').toBe('');
    expect(mutationRequests).toHaveLength(1);
    expect(resourceRequests).toHaveLength(0);
    release(); await ready(page);
    await expect(status(page)).toHaveText('Resource invalidated. No automatic request was sent.');
    await expect(page.locator('[data-resource-primary]')).toHaveText('Waiting for an explicit reload');
    await expect(page.locator('[data-resource-secondary]')).toHaveText('Waiting for an explicit reload');
    expect(resourceRequests).toHaveLength(0);
    await page.locator('[data-resource-reuse]').click();
    await expect(status(page)).toContainText('Public resource ready.');
    expect(resourceRequests).toHaveLength(1);
    await page.getByRole('navigation', {name: 'Main'}).getByRole('link', {name: 'Architecture', exact: true}).click();
    await expect(page).toHaveTitle('Architecture — Next.go'); await ready(page);
    await page.goBack(); await ready(page);
    await expect(status(page)).toHaveText('Adopted the server seed without fetching.');
    expect(await page.evaluate(() => window.resourceLateDocument)).toBe(true);
    expect(resourceRequests).toHaveLength(1);
  } finally {release();}
});
