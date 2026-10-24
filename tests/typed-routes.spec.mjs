import { test, expect } from '@playwright/test';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');

test('Go-generated catchall paths encode decoded segments and server readers recover them exactly', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  await page.evaluate(() => { window.typedDocument = true; });
  await page.locator('[data-guide-path]').fill('Go WASM/東京/%2f/what?#');
  await page.getByRole('button', { name: 'Open typed guide', exact: true }).click();
  await expect(page).toHaveURL(/\/docs\/Go%20WASM\/%E6%9D%B1%E4%BA%AC\/%252f\/what%3F%23$/);
  await expect(page.locator('[data-catchall-path]')).toHaveText('Go WASM/東京/%2f/what?#');
  expect(await page.evaluate(() => window.typedDocument)).toBe(true);
  await page.reload(); await ready(page);
  await expect(page.locator('[data-catchall-path]')).toHaveText('Go WASM/東京/%2f/what?#');
});

test('Go-generated navigation rejects invalid decoded segments without requests or view changes', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  let requests = 0;
  page.on('request', request => { if (request.headers()['x-nextgo-navigate'] === '1') requests++; });
  for (const value of ['', '.', '..', 'guide//intro', 'guide/../intro', 'guide\\intro']) {
    await page.locator('[data-guide-path]').fill(value);
    await page.getByRole('button', { name: 'Open typed guide', exact: true }).click();
    await expect(page.locator('[data-action-status]')).toContainText('Invalid guide path:');
    await expect(page).toHaveURL(/\/features$/);
  }
  expect(requests).toBe(0);
  await page.locator('[data-guide-path]').fill('guide/recovered');
  await page.getByRole('button', { name: 'Open typed guide', exact: true }).click();
  await expect(page.locator('[data-catchall-path]')).toHaveText('guide/recovered');
});

test('optional catchall generation accepts an empty slice and preserves browser history', async ({ page }) => {
  await page.goto('/features'); await ready(page);
  await page.evaluate(() => { window.typedCatalogDocument = true; });
  await page.getByRole('button', { name: 'Open catalog root', exact: true }).click();
  await expect(page).toHaveURL(/\/catalog$/); await expect(page.locator('[data-optional-path]')).toHaveText('(catalog root)');
  await page.goBack(); await expect(page).toHaveURL(/\/features$/); await ready(page);
  await page.goForward(); await expect(page.locator('[data-optional-path]')).toHaveText('(catalog root)');
  expect(await page.evaluate(() => window.typedCatalogDocument)).toBe(true);
});
