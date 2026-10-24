import { test, expect } from '@playwright/test';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const nav = (page, name) => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name, exact: true });

test.beforeEach(async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.addInitScript(() => {
    window.vt = { supported: typeof document.startViewTransition === 'function', calls: 0, skips: 0, finished: 0, failedReady: 0, types: [] };
    if (!window.vt.supported) return;
    const native = document.startViewTransition.bind(document);
    document.startViewTransition = update => {
      window.vt.calls++;
      const transition = native(async () => {
        if (window.holdNextTransition) {
          window.holdNextTransition = false;
          await new Promise(resolve => { window.releaseTransition = resolve; });
        }
        return update();
      });
      const skip = transition.skipTransition.bind(transition);
      transition.skipTransition = () => { window.vt.skips++; skip(); };
      transition.ready.then(() => {}, () => { window.vt.failedReady++; });
      transition.finished.then(() => { window.vt.finished++; }, () => { window.vt.finished++; });
      queueMicrotask(() => window.vt.types.push(transition.types ? [...transition.types] : []));
      return transition;
    };
  });
});

test('native navigation retains SSR hydration and commits title, focus, history and layout state together', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('http://127.0.0.1:4128/', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-transitions', 'true');
  await expect(page.locator('[data-count]')).toHaveText(['3', '10']);
  await page.evaluate(() => { window.initialCount = document.querySelector('[data-count]'); window.documentMarker = 'same'; });
  release(); await ready(page);
  expect(await page.evaluate(() => window.initialCount === document.querySelector('[data-count]'))).toBe(true);
  await page.locator('[data-layout-increment]').click();
  await nav(page, 'Field notes').click();
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  await expect(page.locator('#main')).toBeFocused();
  await page.goBack();
  await expect(page).toHaveTitle('Next.go — Go, all the way through');
  expect(await page.evaluate(() => window.documentMarker)).toBe('same');
  const stats = await page.evaluate(() => window.vt);
  if (stats.supported) { expect(stats.calls).toBe(2); expect(stats.types[0]).toContain('navigation'); }
});

test('Go state updates use native transitions and explicit navigation opt-outs remain immediate', async ({ page }) => {
  await page.goto('http://127.0.0.1:4128/transitions'); await ready(page);
  await page.getByRole('button', { name: 'Animate count', exact: true }).click();
  await expect(page.locator('[data-transition-count]')).toHaveText('1');
  if (await page.evaluate(() => window.vt.supported)) {
    await expect.poll(() => page.evaluate(() => window.vt.finished)).toBe(1);
    const types = await page.evaluate(() => window.vt.types[0]);
    // Types are a newer extension; browsers without it still animate.
    if (types.length) expect(types).toContain('counter');
  }
  await page.evaluate(() => { window.oldTransitionCount = document.querySelector('[data-transition-count]'); window.oldTransitionButton = document.querySelector('[data-transition-increment]'); });
  const before = await page.evaluate(() => window.vt.calls);
  await page.getByRole('button', { name: 'Go navigation without animation' }).click();
  await expect(page).toHaveURL('http://127.0.0.1:4128/about');
  expect(await page.evaluate(() => window.vt.calls)).toBe(before);
  expect(await page.evaluate(() => { window.oldTransitionButton.click(); return window.oldTransitionCount.textContent; })).toBe('1');
  await page.goto('http://127.0.0.1:4128/transitions'); await ready(page);
  await page.getByRole('link', { name: 'Field notes without animation', exact: true }).click();
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(await page.evaluate(() => window.vt.calls)).toBe(0);
});

test('streamed documents retain transition ownership and Go navigation after early hydration', async ({ page }) => {
  await page.goto('http://127.0.0.1:4128/stream', { waitUntil: 'commit' });
  await expect(page.locator('[data-stream-loading]')).toBeVisible();
  await expect(page.locator('[data-stream-loaded]')).toBeVisible();
  await ready(page);
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-transitions', 'true');
  await page.evaluate(() => { window.streamTransitionDocument = true; });
  await page.getByRole('button', { name: 'Increase Streamed counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText('8');
  await nav(page, 'Architecture').click();
  await expect(page).toHaveURL('http://127.0.0.1:4128/about');
  expect(await page.evaluate(() => window.streamTransitionDocument)).toBe(true);
  await expect(page.locator('#nextgo-status')).toBeHidden();
});

test('animated Go Refresh and Replace preserve their scroll, layout and history contracts', async ({ page }) => {
  await page.goto('http://127.0.0.1:4128/features'); await ready(page);
  await page.locator('[data-layout-increment]').click();
  await page.evaluate(() => {
    window.transitionHistory = history.length;
    window.transitionLayout = document.querySelector('[data-layout-increment]');
    window.scrollTo(0, 400);
    window.transitionScroll = scrollY;
    document.querySelector('[data-refresh]').click();
  });
  await expect.poll(() => page.locator('#nextgo-app').getAttribute('aria-busy')).toBe(null);
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  expect(await page.evaluate(() => window.transitionLayout === document.querySelector('[data-layout-increment]'))).toBe(true);
  expect(await page.evaluate(() => history.length)).toBe(await page.evaluate(() => window.transitionHistory));
  await expect.poll(() => page.evaluate(() => scrollY)).toBe(await page.evaluate(() => window.transitionScroll));
  await page.locator('[data-replace]').evaluate(button => button.click());
  await expect(page).toHaveURL('http://127.0.0.1:4128/posts/html-first');
  expect(await page.evaluate(() => history.length)).toBe(await page.evaluate(() => window.transitionHistory));
  await expect(page.locator('[data-layout-count]')).toHaveText('1');
  if (await page.evaluate(() => window.vt.supported)) {
    expect(await page.evaluate(() => window.vt.calls)).toBe(2);
    const types = await page.evaluate(() => window.vt.types[0]);
    if (types.length) expect(types).toContain('refresh');
  }
});

test('reduced motion bypasses navigation and component animation and can stop an active animation', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('http://127.0.0.1:4128/transitions'); await ready(page);
  await page.getByRole('button', { name: 'Animate count', exact: true }).click();
  await expect(page.locator('[data-transition-count]')).toHaveText('1');
  await nav(page, 'Architecture').click();
  await expect(page).toHaveURL('http://127.0.0.1:4128/about');
  expect(await page.evaluate(() => window.vt.calls)).toBe(0);
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.addStyleTag({ content: '::view-transition-group(*),::view-transition-old(*),::view-transition-new(*){animation-duration:5s!important}' });
  await nav(page, 'Field notes').evaluate(a => a.click());
  await expect(page).toHaveTitle('Field notes — Next.go');
  if (await page.evaluate(() => window.vt.supported)) {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect.poll(() => page.evaluate(() => window.vt.skips)).toBeGreaterThan(0);
    await expect.poll(() => page.evaluate(() => window.vt.finished)).toBe(1);
  }
});

for (const mode of ['missing', 'throws']) test(`${mode} browser animation API uses ordinary Go navigation`, async ({ page }) => {
  await page.addInitScript(mode => { document.startViewTransition = mode === 'missing' ? undefined : () => { throw new Error('unsupported snapshot'); }; }, mode);
  await page.goto('http://127.0.0.1:4128/transitions'); await ready(page);
  await page.getByRole('button', { name: 'Animate count', exact: true }).click();
  await expect(page.locator('[data-transition-count]')).toHaveText('1');
  await page.getByRole('link', { name: 'Animated architecture link' }).click();
  await expect(page).toHaveURL('http://127.0.0.1:4128/about');
  await expect(page.locator('#nextgo-status')).toBeHidden();
});

test('superseding a snapshot keeps late callbacks callable without stale DOM or history commits', async ({ page }) => {
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto('http://127.0.0.1:4128/'); await ready(page);
  await page.evaluate(() => { window.holdNextTransition = true; });
  await nav(page, 'Field notes').evaluate(a => a.click());
  if (await page.evaluate(() => window.vt.supported)) {
    await expect.poll(() => page.evaluate(() => typeof window.releaseTransition)).toBe('function');
    await expect(page).toHaveTitle('Next.go — Go, all the way through');
    await nav(page, 'Architecture').evaluate(a => a.click());
    await expect(page).toHaveURL('http://127.0.0.1:4128/about');
    await page.evaluate(() => window.releaseTransition());
    await expect.poll(() => page.evaluate(() => window.vt.finished)).toBe(2);
    await expect(page).toHaveTitle('Architecture — Next.go');
    await page.goBack();
    await expect(page).toHaveTitle('Next.go — Go, all the way through');
  } else {
    await expect(page).toHaveTitle('Field notes — Next.go');
  }
  expect(errors).toEqual([]);
});

test('network failure never starts a transition or discards useful current HTML', async ({ page }) => {
  await page.goto('http://127.0.0.1:4128/'); await ready(page);
  await page.route('**/about', async route => {
    if (route.request().headers()['x-nextgo-navigate']) await route.abort('failed');
    else await route.continue();
  });
  await nav(page, 'Architecture').evaluate(a => a.click());
  await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
  expect(await page.evaluate(() => window.vt.calls)).toBe(0);
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
});

test('unmount cancels a Go component update held inside a native snapshot', async ({ page }) => {
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto('http://127.0.0.1:4128/transitions'); await ready(page);
  await page.evaluate(() => {
    window.oldTransitionCount = document.querySelector('[data-transition-count]');
    window.oldTransitionButton = document.querySelector('[data-transition-increment]');
    window.holdNextTransition = true;
  });
  await page.getByRole('button', { name: 'Animate count', exact: true }).click();
  if (await page.evaluate(() => window.vt.supported)) {
    await expect.poll(() => page.evaluate(() => typeof window.releaseTransition)).toBe('function');
    await nav(page, 'Architecture').evaluate(a => a.click());
    await expect(page).toHaveURL('http://127.0.0.1:4128/about');
    await page.evaluate(() => window.releaseTransition());
    await expect.poll(() => page.evaluate(() => window.vt.finished)).toBe(2);
    expect(await page.evaluate(() => { window.oldTransitionButton.click(); return window.oldTransitionCount.textContent; })).toBe('0');
  } else {
    await expect(page.locator('[data-transition-count]')).toHaveText('1');
  }
  expect(errors).toEqual([]);
});

test('a slow snapshot falls back within a bounded wait and a late callback cannot repeat its commit', async ({ page }) => {
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto('http://127.0.0.1:4128/'); await ready(page);
  await page.evaluate(() => { window.holdNextTransition = true; window.transitionStartHistory = history.length; });
  await nav(page, 'Field notes').evaluate(a => a.click());
  await expect(page).toHaveURL('http://127.0.0.1:4128/posts');
  if (await page.evaluate(() => window.vt.supported)) {
    await page.evaluate(() => window.releaseTransition());
    await expect.poll(() => page.evaluate(() => window.vt.finished)).toBe(1);
  }
  expect(await page.evaluate(() => history.length - window.transitionStartHistory)).toBe(1);
  await expect(page).toHaveTitle('Field notes — Next.go');
  await expect(page.locator('#nextgo-status')).toBeHidden();
  expect(errors).toEqual([]);
});

test('duplicate CSS names skip animation while navigation still commits and settles cleanly', async ({ page }) => {
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  await page.goto('http://127.0.0.1:4128/transitions'); await ready(page);
  await page.addStyleTag({ content: 'h1,nav{view-transition-name:duplicate-test-name!important}' });
  await page.getByRole('link', { name: 'Animated architecture link' }).evaluate(a => a.click());
  await expect(page).toHaveURL('http://127.0.0.1:4128/about');
  await expect(page.locator('#nextgo-status')).toBeHidden();
  if (await page.evaluate(() => window.vt.supported)) {
    await expect.poll(() => page.evaluate(() => window.vt.finished)).toBe(1);
    expect(await page.evaluate(() => window.vt.failedReady)).toBe(1);
  }
  expect(errors).toEqual([]);
});
