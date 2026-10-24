import { test, expect } from '@playwright/test';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');

test('generated Go paths honor prefixed and domain-based locale deployment', async ({ page }) => {
  for (const origin of ['http://127.0.0.1:4120/shop/fr', 'http://127.0.0.1:4122/domain']) {
    await page.goto(origin+'/features'); await ready(page);
    await page.evaluate(() => { window.typedLocaleDocument = true; });
    await page.locator('[data-guide-path]').fill('Go WASM/東京/%2f');
    await page.getByRole('button', { name: 'Open typed guide', exact: true }).click();
    await expect(page).toHaveURL(origin+'/docs/Go%20WASM/%E6%9D%B1%E4%BA%AC/%252f');
    await expect(page.locator('[data-catchall-path]')).toHaveText('Go WASM/東京/%2f');
    await page.goBack(); await expect(page).toHaveURL(origin+'/features');
    await page.getByRole('button', { name: 'Open catalog root', exact: true }).click();
    await expect(page).toHaveURL(origin+'/catalog'); await expect(page.locator('[data-optional-path]')).toHaveText('(catalog root)');
    expect(await page.evaluate(() => window.typedLocaleDocument)).toBe(true);
    await expect(page.locator('html')).toHaveAttribute('lang','fr');
  }
});

test('locale negotiation, direct SSR, API context and shared assets work before WASM', async ({ browser, request }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto('http://127.0.0.1:4120/shop/fr/translations');
  await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  await expect(page.locator('[data-translated-heading]')).toHaveText('Bonjour');
  await expect(page.locator('[data-count]')).toHaveText('0');
  await expect(page.locator('[data-increment]')).toBeDisabled();
  await page.goto('http://127.0.0.1:4120/shop/ar/translations');
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  await expect(page.locator('[data-translated-heading]')).toHaveText('مرحبا');
  await page.goto('http://127.0.0.1:4120/shop/fr/posts/html-first');
  await expect(page.locator('[data-posts-layout]')).toBeVisible();
  await page.goto('http://127.0.0.1:4120/shop/fr/unknown');
  await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  await context.close();
  const redirect = await request.get('http://127.0.0.1:4120/shop/translations?q=one', { maxRedirects: 0, headers: { 'Accept-Language': 'fr-CA,en;q=0.5' } });
  expect(redirect.status()).toBe(307); expect(redirect.headers().location).toBe('/shop/fr/translations?q=one');
  const preferred = await request.get('http://127.0.0.1:4120/shop/', { maxRedirects: 0, headers: { 'Accept-Language': 'en', Cookie: 'nextgo_locale=ar' } });
  expect(preferred.headers().location).toBe('/shop/ar');
  expect(await (await request.get('http://127.0.0.1:4120/shop/fr/api/locale')).json()).toMatchObject({ locale: 'fr', locales: ['en', 'fr', 'ar'] });
  expect(await (await request.get('http://127.0.0.1:4120/shop/api/locale', { headers: { 'Accept-Language': 'ar' } })).json()).toMatchObject({ locale: 'en' });
  expect((await request.get('http://127.0.0.1:4120/shop/assets/style.css')).status()).toBe(200);
});

test('Go hydration preserves translated SSR; same-language navigation retains layouts and language switches reload', async ({ page }) => {
  await page.route('**/app.wasm', async route => { await new Promise(r => setTimeout(r, 200)); await route.continue(); });
  await page.goto('http://127.0.0.1:4120/shop/fr/translations');
  await page.evaluate(() => { window.localeNode = document.querySelector('[data-count]'); window.localeDocument = 'fr'; });
  await ready(page);
  await expect(page.locator('[data-route-location]')).toHaveText('/shop/fr/translations');
  expect(await page.evaluate(() => window.localeNode === document.querySelector('[data-count]'))).toBe(true);
  await page.locator('[data-increment]').click(); await expect(page.locator('[data-count]')).toHaveText('1');
  await page.locator('[data-layout-increment]').click();
  const layout = await page.locator('[data-layout-increment]').textContent();
  await page.locator('[data-locale-next]').click();
  await expect(page.locator('[data-translated-body]')).toHaveText('Deuxième étape');
  await expect(page.locator('[data-route-location]')).toHaveText('/shop/fr/translations?view=second');
  expect(await page.evaluate(() => window.localeDocument)).toBe('fr');
  expect(await page.locator('[data-layout-increment]').textContent()).toBe(layout);
  await page.locator('[data-language="ar"]').click(); await ready(page);
  await expect(page.locator('html')).toHaveAttribute('lang', 'ar');
  await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
  expect(await page.evaluate(() => window.localeDocument)).toBeUndefined();
  await expect(page.locator('[data-count]')).toHaveText('0');
  await page.goBack(); await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  await expect(page).toHaveURL(/\/shop\/fr\/translations\?view=second$/);
});

test('configured locale domains switch origins and negotiated redirects cannot bounce on destination preferences', async ({ page, request }) => {
  await page.goto('http://127.0.0.1:4121/domain/translations'); await ready(page);
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await page.evaluate(() => { window.localeDomainDocument = true; });
  await expect(page.locator('[data-language="fr"]')).toHaveAttribute('href', 'http://127.0.0.1:4122/domain/translations');
  await page.locator('[data-language="fr"]').click(); await ready(page);
  await expect(page).toHaveURL('http://127.0.0.1:4122/domain/translations');
  await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  expect(await page.evaluate(() => window.localeDomainDocument)).toBeUndefined();
  const redirect = await request.get('http://127.0.0.1:4121/domain', { maxRedirects: 0, headers: { 'Accept-Language': 'fr' } });
  expect(redirect.headers().location).toBe('http://127.0.0.1:4122/domain/fr');
  const landing = await request.get(redirect.headers().location, { maxRedirects: 0, headers: { Cookie: 'nextgo_locale=en' } });
  expect(landing.status()).toBe(200); expect(landing.headers()['content-language']).toBe('fr');
  expect((await request.get('http://127.0.0.1:4121/domain/translations', { headers: { Host: 'unknown.example' } })).status()).toBe(421);
});
