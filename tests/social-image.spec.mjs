import {test, expect} from '@playwright/test';

const root = '/social-cards';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const card = page => page.locator('[data-social-image-example] figure img');
const query = (title, description) => new URLSearchParams({title, description}).toString();

test('mounted server HTML renders readable PNG pixels with complete image metadata and separate asset placement', async ({page, request}, testInfo) => {
  await page.addInitScript(() => {window.socialCSPViolations = []; document.addEventListener('securitypolicyviolation', event => window.socialCSPViolations.push(event.violatedDirective));});
  const response = await page.goto(`${root}/social-image`);
  expect(response.headers()['x-nextgo-requires-server']).toBe('1');
  await ready(page);
  await expect(page.getByRole('heading', {name: 'Useful words. Shareable pixels.'})).toBeVisible();
  await expect(page.locator('link[rel="stylesheet"][href$="/social-image.css"]')).toHaveAttribute('href', '/card-assets/assets/social-image.css');
  await expect.poll(() => card(page).evaluate(image => image.complete && image.naturalWidth === 1200 && image.naturalHeight === 630)).toBe(true);
  await expect(page.locator('meta[property="og:image"]')).toHaveAttribute('content', /^https:\/\/example\.com\/social-cards\/api\/social\?/);
  await expect(page.locator('meta[property="og:image:width"]')).toHaveAttribute('content', '1200');
  await expect(page.locator('meta[property="og:image:height"]')).toHaveAttribute('content', '630');
  await expect(page.locator('meta[name="twitter:card"]')).toHaveAttribute('content', 'summary_large_image');
  expect(await page.locator('meta[name="twitter:image"]').getAttribute('content')).toBe(await page.locator('meta[property="og:image"]').getAttribute('content'));
  const src = await card(page).getAttribute('src');
  expect(src).toMatch(/^\/social-cards\/api\/social\?/);
  const png = await request.get(src);
  expect(png.status()).toBe(200);
  expect(png.headers()['content-type']).toBe('image/png');
  const bytes = await png.body();
  expect([...bytes.subarray(0, 8)]).toEqual([137, 80, 78, 71, 13, 10, 26, 10]);
  expect(bytes.readUInt32BE(16)).toBe(1200);
  expect(bytes.readUInt32BE(20)).toBe(630);
  expect(bytes.length).toBeGreaterThan(1000);
  const pixels = await card(page).evaluate(image => {
    const canvas = document.createElement('canvas'); canvas.width = image.naturalWidth; canvas.height = image.naturalHeight;
    const context = canvas.getContext('2d'); context.drawImage(image, 0, 0);
    const data = context.getImageData(0, 0, canvas.width, canvas.height).data;
    let title = 0, mint = 0, background = 0;
    for (let i = 0; i < data.length; i += 4) {
      if (data[i] === 244 && data[i + 1] === 250 && data[i + 2] === 248) title++;
      if (data[i] === 121 && data[i + 1] === 234 && data[i + 2] === 185) mint++;
      if (data[i] === 9 && data[i + 1] === 32 && data[i + 2] === 28) background++;
    }
    return {title, mint, background};
  });
  expect(pixels.title).toBeGreaterThan(5000);
  expect(pixels.mint).toBeGreaterThan(10000);
  expect(pixels.background).toBeGreaterThan(300000);
  expect(await page.evaluate(() => window.socialCSPViolations)).toEqual([]);
  // Playwright's screenshot caret-hiding stylesheet can itself trigger WebKit
  // CSP reports; check application behavior before screenshot instrumentation.
  await card(page).screenshot({path: testInfo.outputPath('social-card-readable.png')});
});

test('real PNG API supports HEAD and ETags while invalid public queries fail without images', async ({request}) => {
  const src = `${root}/api/social?${query('Ship useful pixels', 'Native Go composition. Public content only.')}`;
  const first = await request.get(src);
  expect(first.status()).toBe(200);
  expect(first.headers()['cache-control']).toBe('public, max-age=0, must-revalidate');
  const etag = first.headers().etag;
  expect(etag).toMatch(/^"[a-f0-9]{64}"$/);
  const head = await request.head(src);
  expect(head.status()).toBe(200);
  expect(head.headers().etag).toBe(etag);
  expect((await head.body()).length).toBe(0);
  const cached = await request.get(src, {headers: {'If-None-Match': etag}});
  expect(cached.status()).toBe(304);
  expect((await cached.body()).length).toBe(0);
  const changed = await request.get(`${root}/api/social?${query('Different words', 'A changed card.')}`);
  expect(changed.status()).toBe(200);
  expect(changed.headers().etag).not.toBe(etag);
  for (const suffix of ['title=%F0%9F%8C%BF', 'title=a&title=b', 'unknown=field']) {
    const bad = await request.get(`${root}/api/social?${suffix}`);
    expect(bad.status()).toBe(400);
    expect(bad.headers()['content-type']).toMatch(/^text\/plain/);
    expect(bad.headers()['cache-control']).toBe('no-store');
  }
});

test('soft navigation updates escaped public text, generated image metadata and stylesheet then restores history', async ({page}) => {
  const title = 'Cards <script> & "Go"';
  const description = 'Escaped public words become pixels, never executable markup.';
  const destination = `${root}/social-image?${query(title, description)}`;
  await page.goto(`${root}/media`);
  await ready(page);
  await page.evaluate(href => {window.socialDocument = true; const link = document.createElement('a'); link.id = 'social-card-navigation'; link.href = href; link.textContent = 'Inspect generated card'; document.querySelector('main').prepend(link);}, destination);
  await page.locator('#social-card-navigation').click();
  await expect(page.getByRole('heading', {name: 'Useful words. Shareable pixels.'})).toBeVisible();
  expect(await page.evaluate(() => window.socialDocument)).toBe(true);
  await expect(page.locator('#social-title')).toHaveValue(title);
  await expect(card(page)).toHaveAttribute('alt', `${title} — ${description}`);
  await expect.poll(() => card(page).evaluate(image => image.complete && image.naturalWidth === 1200)).toBe(true);
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', title);
  await expect(page.locator('meta[property="og:description"]')).toHaveAttribute('content', description);
  await expect(page.locator('link[rel="stylesheet"][href="/card-assets/assets/social-image.css"]')).toHaveCount(1);
  expect(await page.locator('script').evaluateAll(nodes => nodes.some(node => node.textContent.includes('Cards <script>')))).toBe(false);
  await page.goBack();
  await expect(page).toHaveTitle('Responsive media — Next.go');
  await expect(page.locator('link[rel="stylesheet"][href$="/social-image.css"]')).toHaveCount(0);
  expect(await page.evaluate(() => window.socialDocument)).toBe(true);
});
