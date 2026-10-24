import { test, expect, chromium } from '@playwright/test';
import { writeFile } from 'node:fs/promises';
import { adminToken } from './access-fixture.mjs';

// DevTools snapshot/evaluation traffic can evict a frozen renderer. Keep this
// native lifecycle qualification free of tracing and request interception.
test.use({ trace: 'off' });

for (const enabled of [false, true]) {
  test(`native Chromium history ${enabled ? 'revalidates private preview under browser cache policy' : 'preserves published Go state through actual BFCache'}`, async () => {
    // Playwright disables BFCache by default. Use full Chromium with that
    // argument removed, and require a real persisted pageshow rather than a skip.
    const browser = await chromium.launch({ channel: 'chromium', headless: true,
      ignoreDefaultArgs: ['--disable-back-forward-cache'],
      args: ['--enable-features=CacheControlNoStoreEnterBackForwardCache:level/restore-unless-cookie-change'],
      ...(process.env.PLAYWRIGHT_CHROMIUM_FULL_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_FULL_EXECUTABLE_PATH } : {}) });
    try {
      const context = await browser.newContext({ viewport: { width: 1000, height: 400 } }); const page = await context.newPage();
      const responses = []; page.on('response', r => responses.push({ url: r.url(), cache: r.headers()['cache-control'], status: r.status() }));
      const origin = enabled ? 'http://127.0.0.1:4136' : 'http://127.0.0.1:4134';
      if (enabled) expect((await context.request.post(origin + '/api/preview/enable', { headers: { Origin: origin, Authorization: 'Bearer ' + adminToken }, maxRedirects: 0 })).status()).toBe(303);
      await page.goto(origin + '/preview'); await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
      await page.getByRole('button', { name: 'Increase Preview votes' }).click();
      await page.evaluate(() => {
        window.nativeRestoreNode = document.querySelector('[data-count]'); window.nativeRestores = 0;
        window.nativeFocus = document.activeElement; scrollTo(0, 150); window.nativeScroll = scrollY;
        addEventListener('pageshow', event => { if (event.persisted) { window.nativeRestores++; sessionStorage.setItem('nativePersisted', 'yes'); } });
      });
      let probes = 0; page.on('request', r => { if (r.url() === origin + '/_nextgo/draft') probes++; });
      await page.goto('http://localhost:4100/about'); await expect(page).toHaveTitle('Architecture — Next.go');
      await page.evaluate(() => history.back());
      await expect(page).toHaveURL(origin + '/preview');
      await expect(page.locator('[data-count]')).toBeVisible();
      const restored = await page.evaluate(() => window.nativeRestores);
      const reasons = await page.evaluate(() => performance.getEntriesByType('navigation')[0]?.notRestoredReasons?.toJSON?.());
      const diagnostics = { responses, reasons, persisted: await page.evaluate(() => sessionStorage.getItem('nativePersisted')) };
      if (!enabled && restored !== 1) {
        const path = test.info().outputPath('required-bfcache-failure.json');
        await writeFile(path, JSON.stringify(diagnostics, null, 2));
        await test.info().attach('required-bfcache-failure', { path, contentType: 'application/json' });
      }
      if (!enabled || restored === 1) {
        // At least the published case must exercise real native restoration;
        // an unavailable BFCache cannot make that conformance test pass.
        expect(restored, JSON.stringify(diagnostics)).toBe(1);
        expect(await page.evaluate(() => window.nativeRestoreNode === document.querySelector('[data-count]'))).toBe(true);
        await expect(page.locator('[data-count]')).toHaveText('1'); expect(probes).toBe(1);
        expect(await page.evaluate(() => document.activeElement === window.nativeFocus)).toBe(true);
        expect(await page.evaluate(() => Math.abs(scrollY - window.nativeScroll))).toBeLessThanOrEqual(2);
      } else {
        // Browsers own eligibility. Preserve no-store; require a fresh server
        // response and correct Go initialization when private caching is denied.
        expect(restored).toBeUndefined(); expect(probes).toBe(0);
        expect(responses.filter(r => r.url === origin + '/preview' && r.status === 200 && r.cache === 'private, no-store')).toHaveLength(2);
        expect(JSON.stringify(reasons)).toContain('response-cache-control-no-store');
        await expect(page.locator('[data-count]')).toHaveText('0');
        await expect(page.locator('body')).toHaveAttribute('data-nextgo-draft', '1');
        await test.info().attach('private-bfcache-policy', { body: JSON.stringify(reasons), contentType: 'application/json' });
      }
      await page.getByRole('button', { name: 'Increase Preview votes' }).click();
      await expect(page.locator('[data-count]')).toHaveText(!enabled || restored === 1 ? '2' : '1');
    } finally { await browser.close(); }
  });
}

test('native history after preview-cookie removal cannot reveal the prior private page', async () => {
  const browser = await chromium.launch({ channel: 'chromium', headless: true,
    ignoreDefaultArgs: ['--disable-back-forward-cache'],
    args: ['--enable-features=CacheControlNoStoreEnterBackForwardCache:level/restore-unless-cookie-change'],
    ...(process.env.PLAYWRIGHT_CHROMIUM_FULL_EXECUTABLE_PATH ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_FULL_EXECUTABLE_PATH } : {}) });
  try {
    const context = await browser.newContext(); const page = await context.newPage(); const origin = 'http://127.0.0.1:4136';
    expect((await context.request.post(origin + '/api/preview/enable', { headers: { Origin: origin, Authorization: 'Bearer ' + adminToken }, maxRedirects: 0 })).status()).toBe(303);
    await page.goto(origin + '/preview'); await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    await expect(page.getByRole('heading', { name: 'Unpublished field note' })).toBeVisible();
    await page.evaluate(() => { window.priorPrivatePage = true; });
    await page.goto('http://localhost:4100/about'); await context.clearCookies();
    await page.evaluate(() => history.back());
    await expect(page).toHaveURL(origin + '/preview');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-draft', '0');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    await expect(page.getByRole('heading', { name: 'Published field note' })).toBeVisible();
    expect(await page.evaluate(() => window.priorPrivatePage)).toBeUndefined();
    await expect(page.locator('[data-preview-banner]')).toHaveCount(0);
  } finally { await browser.close(); }
});
