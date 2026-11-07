import { test, expect } from '@playwright/test';
import { installSkewDiagnostics, captureSkewDiagnostics, observeSkewPrefetch } from './skew-diagnostics.mjs';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const notes = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Field notes', exact: true });
const activate = async (request, variant) => expect((await request.post(`/_e2e/activate/${variant}`)).status()).toBe(204);

test.beforeEach(async ({ request }) => activate(request, 'old'));
test.afterEach(async ({ page }, testInfo) => captureSkewDiagnostics(page, testInfo));

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

for (const cacheState of ['cold', 'warm']) {
  test(`a mismatched speculative prefetch leaves the document alone and clicking reloads safely (${cacheState} cache)`, async ({ page, request }, testInfo) => {
    await page.goto('/'); await ready(page);
    const build = await page.locator('body').getAttribute('data-nextgo-build');
    const configuration = await page.locator('body').getAttribute('data-nextgo-configuration');
    const key = encodeURIComponent(`${testInfo.project.name}-${testInfo.repeatEachIndex}-${testInfo.retry}-${cacheState}`);
    const target = `/posts?skew-prefetch=${key}`;
    const canonical = `/posts/?skew-prefetch=${key}`;
    await installSkewDiagnostics(page, testInfo, key);
    await page.evaluate(() => { window.prefetchDeploymentDocument = true; });
    // Each case owns a fresh cache key. Disable incidental hover prefetch and
    // blur first so focus always produces the intended post-activation event.
    await notes(page).evaluate((link, href) => {
      link.blur();
      link.setAttribute('data-nextgo-prefetch', 'false');
      link.href = href;
    }, target);
    await activate(request, 'updated');
    if (cacheState === 'warm') {
      // A cached public page can bypass slot-carrier validation and return 200.
      // Prime only the server cache; the old browser document stays untouched.
      const primer = await request.get(canonical, { headers: { 'X-Nextgo-Navigate': '1' } });
      expect(primer.status()).toBe(200);
      expect(primer.headers()['x-nextgo-configuration']).not.toBe(configuration);
      await primer.body();
    }
    const prefetched = page.waitForResponse(response => {
      const url = new URL(response.url());
      return url.pathname === '/posts/' && url.searchParams.get('skew-prefetch') === decodeURIComponent(key)
        && response.headers()['content-type']?.includes('application/json');
    });
    await notes(page).evaluate(link => link.removeAttribute('data-nextgo-prefetch'));
    await notes(page).focus();
    const response = await prefetched;
    expect(response.request().isNavigationRequest()).toBe(false);
    // Observe the same reader used by Go. DevTools may report ERR_ABORTED even
    // after native EOF; a terminal protocol event is not the application body.
    const observed = await observeSkewPrefetch(page);
    expect(observed.fetches).toBe(1);
    expect(observed.ambiguous).toBe(false);
    expect(observed.eof).toBe(true);
    expect(observed.truncated).toBe(false);
    expect(observed.error).toBeNull();
    expect(observed.aborts).toBe(0);
    expect(observed.bytes).toBeGreaterThan(0);
    expect(observed.bytes).toBeLessThanOrEqual(64 * 1024);
    expect(Buffer.byteLength(observed.bodyText, 'utf8')).toBe(observed.bytes);
    expect(observed.url).toBe(response.url());
    expect(observed.status).toBe(response.status());
    for (const name of ['content-type', 'x-nextgo-configuration', 'x-nextgo-protocol', 'x-nextgo-prefetch']) {
      expect(observed.headers[name]).toBe(response.headers()[name] ?? null);
    }
    // Chromium's synchronous headers() can contain only provisional redirect
    // headers. Await the complete set before checking framework request intent.
    const requestHeaders = await response.request().allHeaders();
    expect(requestHeaders['x-nextgo-purpose']).toBe('prefetch');
    expect(requestHeaders['x-nextgo-navigate']).toBe('1');
    const envelope = JSON.parse(observed.bodyText);
    expect(envelope.configuration).not.toBe(configuration);
    expect(response.headers()['x-nextgo-configuration']).toBe(envelope.configuration);
    expect(response.headers()['x-nextgo-protocol']).toBe(String(envelope.version));
    expect(envelope.version).toBe(3);
    expect(envelope.build).toBe(build);
    expect(envelope.status).toBe(response.status());
    const carrier = requestHeaders['x-nextgo-slots'];
    if (carrier) expect(JSON.parse(carrier).configuration).toBe(configuration);
    // Cold rendering rejects an old slot carrier before invoking the page;
    // a warm public snapshot still carries the new configuration with status 200.
    expect(response.status()).toBe(cacheState === 'cold' && carrier ? 400 : 200);
    if (cacheState === 'warm') expect(response.headers()['x-nextgo-prefetch']).toBe('public');
    await expect(page).toHaveURL('http://127.0.0.1:4107/');
    expect(await page.evaluate(() => window.prefetchDeploymentDocument)).toBe(true);
    await notes(page).click();
    await expect(page).toHaveURL(`http://127.0.0.1:4107${canonical}`); await ready(page);
    expect(await page.evaluate(() => window.prefetchDeploymentDocument)).toBeUndefined();
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-configuration', envelope.configuration);
  });
}

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
