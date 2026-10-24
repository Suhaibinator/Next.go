import { test, expect } from '@playwright/test';
const origin = 'http://127.0.0.1:4129';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');

test('conditional query rewrite serves useful direct HTML and canonical redirect retains Go navigation', async ({ page, request }) => {
 const response = await request.get(origin+'/policy/rules/choose?post=html-first');
 expect(response.status()).toBe(200); expect(await response.text()).toContain('Start with the HTML'); expect(response.headers()['cache-control']).toBe('private, no-store');
 await page.goto(origin+'/policy/rules'); await ready(page);
 await page.evaluate(() => { window.ruleDocument = true; });
 await page.getByRole('link', { name: 'Choose a post by query', exact: true }).click();
 await expect(page).toHaveURL(origin+'/policy/rules/choose?post=html-first');
 await expect(page).toHaveTitle('Start with the HTML — Next.go');
 expect(await page.evaluate(() => window.ruleDocument)).toBe(true);
 await page.goBack(); await expect(page).toHaveTitle('Request rules — Next.go');
 await page.getByRole('link', { name: 'Conditional redirect to Go islands', exact: true }).click();
 await expect(page).toHaveURL(origin+'/policy/posts/go-islands?go=yes');
 await expect(page).toHaveTitle('Small islands, explicit state — Next.go');
 expect(await page.evaluate(() => window.ruleDocument)).toBe(true);
});

test('header and cookie conditions choose destination without reusing an earlier prefetched view', async ({ page, context }) => {
 await page.goto(origin+'/policy/rules'); await ready(page);
 const cookie = page.getByRole('link', { name: 'Choose a post by cookie', exact: true });
 // The fallback also has a sensitive source, so it cannot become a public Go cache entry.
 await cookie.focus();
 await context.addCookies([{ name: 'nextgo_variant', value: 'go', url: origin }]);
 await cookie.click(); await expect(page).toHaveTitle('Small islands, explicit state — Next.go');
 await page.goBack(); await expect(page).toHaveTitle('Request rules — Next.go');
 await page.setExtraHTTPHeaders({ 'X-Demo-Route': 'go' });
 await page.getByRole('link', { name: 'Choose a post by header', exact: true }).click();
 await expect(page).toHaveTitle('Small islands, explicit state — Next.go');
 await page.goBack(); await expect(page).toHaveTitle('Request rules — Next.go');
 await page.getByRole('link', { name: 'Choose architecture by host', exact: true }).click();
 await expect(page).toHaveTitle('Architecture — Next.go');
});

test('external rewrites preserve methods/query/bodies and strip credentials, control headers and response cookies', async ({ request }) => {
 const response = await request.post(origin+'/policy/rules/remote/echo?label=x%26y', { data: 'Go request body', headers: { Cookie: 'private-cookie=secret', Authorization: 'Bearer private-token', 'X-Nextgo-Navigate': '1', 'X-Forwarded-Host': 'spoofed.test' } });
 expect(response.status()).toBe(200); const echo = await response.json();
 expect(echo).toMatchObject({ method: 'POST', url: '/echo?label=x%26y', host: '127.0.0.1:4130', body: 'Go request body' });
 expect(echo.headers.cookie).toBeUndefined(); expect(echo.headers.authorization).toBeUndefined(); expect(echo.headers['x-nextgo-navigate']).toBeUndefined(); expect(echo.headers['x-forwarded-host']).toBeUndefined();
 expect(response.headers()['x-nextgo-protocol']).toBeUndefined(); expect(response.headers()['set-cookie']).toBeUndefined();
 const redirect = await request.get(origin+'/policy/rules/remote/redirect', { maxRedirects: 0 }); expect(redirect.status()).toBe(307); expect(redirect.headers().location).toBe('/policy/rules/remote/document');
});

test('external rewrite links use a full document boundary rather than adopting upstream navigation data', async ({ page }) => {
 const documents = []; page.on('request', r => { if (r.isNavigationRequest() && r.frame() === page.mainFrame()) documents.push(r.url()); });
 await page.goto(origin+'/policy/rules'); await ready(page); await page.evaluate(() => { window.ruleDocument = true; });
 await page.getByRole('link', { name: 'Open proxied document', exact: true }).click();
 await expect(page).toHaveURL(origin+'/policy/rules/remote/document'); await expect(page).toHaveTitle('Proxy upstream'); await expect(page.getByRole('heading')).toHaveText('Proxied document');
 expect(await page.evaluate(() => window.ruleDocument)).toBeUndefined(); expect(documents).toHaveLength(2);
});

test('timed out external request leaves Go interaction available in the current page', async ({ page }) => {
 await page.goto(origin+'/policy/'); await ready(page);
 const result = await page.evaluate(async () => { const response = await fetch('/policy/rules/remote/slow'); return { status: response.status, text: await response.text() }; });
 expect(result.status).toBe(504); expect(result.text).toBe('Upstream request failed\n');
 await page.getByRole('button', { name: 'Increase First counter', exact: true }).click(); await expect(page.locator('[data-count]')).toHaveText(['4','10']);
});

test('configured external redirects also retain the browser full-document boundary', async ({ page }) => {
 await page.goto(origin+'/policy/rules'); await ready(page); await page.evaluate(() => { window.ruleDocument = true; });
 await page.getByRole('link', { name: 'Open external redirect', exact: true }).click();
 await expect(page).toHaveURL('http://127.0.0.1:4130/document?go=yes'); await expect(page).toHaveTitle('Proxy upstream');
 expect(await page.evaluate(() => window.ruleDocument)).toBeUndefined();
});
