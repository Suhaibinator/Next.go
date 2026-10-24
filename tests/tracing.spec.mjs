import { test, expect } from '@playwright/test';

test('traced production keeps SSR nodes, Go events and same-document navigation', async ({ page, request }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.setExtraHTTPHeaders({ Authorization: 'Bearer private-probe-browser', traceparent: '00-0123456789abcdef0123456789abcdef-0123456789abcdef-01' });
  const documents = [];
  page.on('request', r => { if (r.isNavigationRequest() && r.frame() === page.mainFrame()) documents.push(r.url()); });
  await page.goto('http://127.0.0.1:4126/?secret=private-probe-query', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-count]')).toHaveText(['3', '10']);
  await page.evaluate(() => { window.tracedInitialNode = document.querySelector('[data-count]'); window.tracedDocument = 'retained'; });
  release(); await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  expect(await page.evaluate(() => window.tracedInitialNode === document.querySelector('[data-count]'))).toBe(true);
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Field notes', exact: true }).click();
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveURL('http://127.0.0.1:4126/posts/html-first');
  expect(await page.evaluate(() => window.tracedDocument)).toBe('retained'); expect(documents).toHaveLength(1);
  await expect.poll(async () => (await (await request.get('http://127.0.0.1:4127/_e2e/traces')).json()).some(r => r.name === 'nextgo.request' && r.route === '/posts/:slug' && r.status === 200)).toBe(true);
  const records = await (await request.get('http://127.0.0.1:4127/_e2e/traces')).json();
  expect(JSON.stringify(records)).not.toContain('private-probe');
  expect(records.every(r => r.traceId !== '0123456789abcdef0123456789abcdef')).toBe(true);
});

test('traced HTTP still flushes an early shell and hydrates streamed islands', async ({ page, request }) => {
  await page.goto('http://127.0.0.1:4126/stream', { waitUntil: 'commit' });
  await expect(page.locator('[data-stream-loading]')).toBeVisible();
  await expect(page.locator('[data-stream-loaded]')).toBeVisible();
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  const button = page.getByRole('button', { name: 'Increase Streamed counter', exact: true });
  await expect(button).toBeEnabled(); await button.click();
  await expect(page.locator('[data-count]')).toHaveText('8');
  await expect.poll(async () => (await (await request.get('http://127.0.0.1:4127/_e2e/traces')).json()).some(r => r.name === 'nextgo.render' && r.route === '/stream' && r.status === 200 && r.bytes > 0)).toBe(true);
});
