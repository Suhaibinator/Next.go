import { test, expect } from '@playwright/test';

for (const split of ['before-island', 'inside-island', 'before-completion']) {
  test(`split parser ${split} keeps incomplete deferred HTML inert until its completed chunk arrives`, async ({ page, request }) => {
    const key = `${test.info().project.name}-${split}-${Date.now()}`;
    const origin = 'http://127.0.0.1:4160';
    const release = async () => expect((await request.post(`${origin}/_fixture/release?key=${encodeURIComponent(key)}`)).status()).toBe(200);
    const errors = [];
    page.on('console', message => { if (message.type() === 'error') errors.push(message.text()); });
    await page.goto(`${origin}/stream?parser-split=${split}&key=${encodeURIComponent(key)}`, { waitUntil: 'commit' });
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    await expect(page.locator('[data-stream-loading]')).toBeVisible();
    await page.evaluate(() => { window.parserApp = document.getElementById('nextgo-app'); window.parserHeading = document.querySelector('h1'); window.parserFallback = document.querySelector('[data-stream-loading]'); });
    await release();
    await expect.poll(() => page.evaluate(() => !!document.querySelector('template[data-nextgo-chunk]') || !window.parserFallback.isConnected)).toBe(true);
    // A task after a rendered frame lets both parser insertion and its mutation
    // observer run. The incomplete template must remain outside the live tree.
    await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => setTimeout(resolve, 0))));
    expect(await page.evaluate(() => window.parserFallback.isConnected)).toBe(true);
    await expect(page.locator('[data-stream-loaded]')).toHaveCount(0);
    await release();
    await expect(page.locator('[data-stream-loaded]')).toBeVisible();
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    const counter = page.getByRole('button', { name: 'Increase Streamed counter', exact: true });
    await expect(counter).toBeEnabled(); await counter.click();
    await expect(page.locator('[data-count]')).toHaveText('8');
    expect(await page.evaluate(() => window.parserApp === document.getElementById('nextgo-app') && window.parserHeading === document.querySelector('h1'))).toBe(true);
    await expect(page.locator('[data-stream-loading]')).toHaveCount(0);
    expect(errors).toEqual([]);
  });
}
