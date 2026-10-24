import { test, expect } from '@playwright/test';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const notes = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Field notes', exact: true });
const activate = async (request, variant) => expect((await request.post(`/_e2e/activate/${variant}`)).status()).toBe(204);

test.beforeEach(async ({ request }) => activate(request, 'old'));

test('same binary with changed slash and asset policy reloads before committing incompatible HTML', async ({ page, request }) => {
  await page.goto('/'); await ready(page);
  const build = await page.locator('body').getAttribute('data-nextgo-build');
  const configuration = await page.locator('body').getAttribute('data-nextgo-configuration');
  await page.evaluate(() => { window.oldDeploymentDocument = true; });
  await activate(request, 'updated');
  await notes(page).click();
  await expect(page).toHaveURL(/\/posts\/$/); await ready(page);
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(await page.evaluate(() => window.oldDeploymentDocument)).toBeUndefined();
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-build', build);
  expect(await page.locator('body').getAttribute('data-nextgo-configuration')).not.toBe(configuration);
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-assets', '/v2-cdn');
  await expect(page.locator('script[src$="/boot.js"]')).toHaveAttribute('src', /^\/v2-cdn\/_nextgo\/.+\/boot.js$/);
});

test('a mismatched speculative prefetch leaves the document alone and clicking reloads safely', async ({ page, request }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.prefetchDeploymentDocument = true; });
  await activate(request, 'updated');
  const prefetched = page.waitForResponse(response => new URL(response.url()).pathname === '/posts/' && response.status() === 200);
  await notes(page).focus();
  const response = await prefetched;
  expect(response.request().isNavigationRequest()).toBe(false);
  await response.finished();
  await expect(page).toHaveURL('http://127.0.0.1:4107/');
  expect(await page.evaluate(() => window.prefetchDeploymentDocument)).toBe(true);
  await notes(page).click();
  await expect(page).toHaveURL(/\/posts\/$/); await ready(page);
  expect(await page.evaluate(() => window.prefetchDeploymentDocument)).toBeUndefined();
});

test('moving a mounted application recovers through a full document and retains query and fragment', async ({ page, request }) => {
  await activate(request, 'old-mounted');
  await page.goto('/old'); await ready(page);
  await page.evaluate(() => { window.oldMountDocument = true; });
  await activate(request, 'moved');
  await notes(page).evaluate(link => { link.href = '/old/posts?tag=a%26b#notes'; link.click(); });
  await expect(page).toHaveURL('http://127.0.0.1:4107/new/posts/?tag=a%26b#notes'); await ready(page);
  expect(await page.evaluate(() => window.oldMountDocument)).toBeUndefined();
  await expect(notes(page)).toHaveAttribute('href', '/new/posts/');
  await expect(page).toHaveTitle('Field notes — Next.go');
});

test('rollback to the preceding runtime policy also reloads without losing navigation', async ({ page, request }) => {
  await activate(request, 'updated');
  await page.goto('/posts/'); await ready(page);
  await page.evaluate(() => { window.newDeploymentDocument = true; });
  await activate(request, 'old');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Architecture', exact: true }).click();
  await expect(page).toHaveURL(/\/about$/); await ready(page);
  expect(await page.evaluate(() => window.newDeploymentDocument)).toBeUndefined();
  await expect(page).toHaveTitle('Architecture — Next.go');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-assets', '');
});
