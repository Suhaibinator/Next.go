import { test, expect } from '@playwright/test';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const notes = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Field notes', exact: true });
const features = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Features', exact: true });

test('slash policy canonicalizes direct URLs and keeps file and service URLs stable', async ({ request, browser }) => {
  for (const [input, location] of [['/slash', '/slash/'], ['/slash/posts?q=go', '/slash/posts/?q=go'], ['/slash/posts/a%3Fb', '/slash/posts/a%3Fb/'], ['/slash/api/health', '/slash/api/health/'], ['/slash/sitemap.xml/', '/slash/sitemap.xml']]) {
    const response = await request.get(input, { maxRedirects: 0 });
    expect(response.status()).toBe(308);
    expect(response.headers().location).toBe(location);
  }
  expect((await request.get('/slash/api/health/')).status()).toBe(200);
  expect(await (await request.get('/slash/sitemap.xml')).text()).toContain('https://example.com/slash/posts/');
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto('http://127.0.0.1:4106/slash/posts/html-first');
  await expect(page).toHaveURL(/\/slash\/posts\/html-first\/$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Start with the HTML');
  await expect(notes(page)).toHaveAttribute('href', '/slash/posts/');
  await context.close();
});

test('slash navigation preserves the document, hydration, history and loading states', async ({ page }) => {
  const errors = [];
  page.on('pageerror', error => errors.push(error.message));
  await page.goto('/slash/'); await ready(page);
  await page.evaluate(() => { window.slashDocument = true; });
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  await notes(page).evaluate(link => { link.href += '?delay=750ms'; link.click(); });
  await expect(page.locator('[data-posts-loading]')).toBeVisible();
  await expect(page).toHaveURL(/\/slash\/posts\/\?delay=750ms$/);
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveURL(/\/slash\/posts\/html-first\/$/);
  await expect(page).toHaveTitle('Start with the HTML — Next.go');
  await page.goBack(); await expect(page).toHaveTitle('Field notes — Next.go');
  await page.goForward(); await expect(page).toHaveTitle('Start with the HTML — Next.go');
  expect(await page.evaluate(() => window.slashDocument)).toBe(true);
  expect(errors).toEqual([]);
});

test('slash mode keeps actions, refresh and prefetch working and excludes APIs from navigation', async ({ page }) => {
  await page.goto('/slash/features/'); await ready(page);
  await expect(page.locator('form')).toHaveAttribute('action', '/slash/_nextgo/actions/increment');
  const count = Number(await page.locator('[data-server-count]').textContent());
  await page.getByRole('button', { name: 'Increment on server', exact: true }).click();
  await expect(page.locator('[data-server-count]')).toHaveText(String(count + 1));
  await page.getByRole('button', { name: 'Prefetch field notes', exact: true }).click();
  await page.getByRole('button', { name: 'Replace with first field note', exact: true }).click();
  await expect(page).toHaveURL(/\/slash\/posts\/html-first\/$/);
  await features(page).click(); await expect(page).toHaveTitle('Framework features — Next.go');
  await page.evaluate(() => {
    const link = document.createElement('a'); link.href = '/slash/api/health/'; link.id = 'health-link'; link.textContent = 'Health endpoint';
    document.querySelector('main').append(link);
  });
  const documentRequest = page.waitForRequest(request => request.isNavigationRequest() && request.url().endsWith('/slash/api/health/'));
  await page.locator('#health-link').click();
  expect((await documentRequest).headers()['x-nextgo-navigate']).toBeUndefined();
  await expect(page.locator('body')).toContainText('"ok":true');
});
