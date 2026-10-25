import {test, expect} from '@playwright/test';

const route = '/safe-embeds/embeds';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');

test('server HTML has an opaque sandbox, no referrer, a visible fallback and native media without JavaScript', async ({browser, baseURL}) => {
  const context = await browser.newContext({javaScriptEnabled: false, baseURL});
  const page = await context.newPage();
  const response = await page.goto(route);
  expect(response.headers()['content-security-policy']).toContain("default-src 'self'");
  expect(response.headers()['x-nextgo-requires-server']).toBe('1');
  const frame = page.locator('#safe-frame');
  await expect(frame).toHaveAttribute('sandbox', '');
  await expect(frame).toHaveAttribute('title', 'Local sandboxed document');
  await expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer');
  await expect(frame).toHaveAttribute('allow', /camera 'none'.*microphone 'none'.*fullscreen 'none'/);
  await expect(page.frameLocator('#safe-frame').getByRole('heading', {name: 'Local embedded document'})).toBeVisible();
  await expect(page.frameLocator('#safe-frame').locator('#embed-referrer')).toHaveText('No referrer sent');
  await expect(page.getByRole('link', {name: 'Open Local sandboxed document', exact: true})).toBeVisible();
  await expect(page.locator('#native-video')).toHaveAttribute('controls', '');
  await expect(page.locator('#native-video track')).toHaveAttribute('srclang', 'en');
  await expect(page.locator('#native-audio')).toHaveAttribute('aria-label', 'Short 440 hertz tone');
  await context.close();
});

test('native media decodes local assets and captions while the sandbox isolates its same-origin document', async ({page}, testInfo) => {
  const requests = [];
  page.on('request', request => requests.push(request.url()));
  await page.goto(route);
  await ready(page);
  expect(await page.locator('#safe-frame').evaluate(frame => frame.contentDocument === null)).toBe(true);
  await expect(page.frameLocator('#safe-frame').locator('#embed-message')).toHaveText('A local document, isolated by the browser sandbox.');
  await expect.poll(() => page.locator('#native-video').evaluate(video => video.readyState)).toBeGreaterThanOrEqual(1);
  await expect.poll(() => page.locator('#native-audio').evaluate(audio => audio.readyState)).toBeGreaterThanOrEqual(1);
  expect(await page.locator('#native-video').evaluate(video => video.duration)).toBeCloseTo(2, 1);
  expect(await page.locator('#native-audio').evaluate(audio => audio.duration)).toBeCloseTo(0.3, 1);
  // Native caption preferences differ by engine; enabling captions loads the
  // track even when the browser's user preference leaves a default track off.
  await page.locator('#native-video').evaluate(video => {video.textTracks[0].mode = 'showing';});
  await expect.poll(() => page.locator('#native-video').evaluate(video => video.textTracks[0]?.cues?.length || 0)).toBe(1);
  await expect(page.locator('#native-video')).toHaveAttribute('playsinline', '');
  expect(await page.locator('#native-video').evaluate(video => ({autoplay: video.autoplay, controls: video.controls, muted: video.muted, defaultMuted: video.defaultMuted}))).toEqual({autoplay: false, controls: true, muted: false, defaultMuted: false});
  // Firefox can retain the standard HTML attribute without exposing its IDL
  // property. Check reflection where supported, with a fresh native control as
  // independent evidence of browser capability rather than application state.
  const inline = await page.locator('#native-video').evaluate(video => ({supported: 'playsInline' in video, value: video.playsInline, freshNativeSupported: 'playsInline' in document.createElement('video')}));
  expect(inline.supported).toBe(inline.freshNativeSupported);
  if (inline.supported) expect(inline.value).toBe(true);
  else expect(inline.value).toBeUndefined();
  await testInfo.attach('native-inline-capability', {body: JSON.stringify(inline), contentType: 'application/json'});
  // WebKit reports same-origin blob requests for its native media controls.
  // Compare origins so those browser-owned resources are not foreign network.
  expect(requests.filter(url => new URL(url).origin !== new URL(page.url()).origin)).toEqual([]);
  expect(requests.some(url => url.includes('/embed-assets/assets/embeds-demo.'))).toBe(true);
});

test('soft navigation removes the frame and restores safe native elements through history', async ({page}) => {
  await page.goto(route);
  await ready(page);
  await page.evaluate(() => {window.embedDocument = true; window.oldEmbeddedFrame = document.querySelector('#safe-frame');});
  await page.getByRole('link', {name: 'Responsive images →', exact: true}).click();
  await expect(page).toHaveTitle('Responsive media — Next.go');
  expect(await page.evaluate(() => window.oldEmbeddedFrame.isConnected)).toBe(false);
  expect(await page.evaluate(() => window.embedDocument)).toBe(true);
  await page.goBack();
  await expect(page).toHaveTitle('Safe embeds — Next.go');
  await expect(page.frameLocator('#safe-frame').locator('#embed-referrer')).toHaveText('No referrer sent');
  await expect(page.locator('#native-video')).toHaveAttribute('controls', '');
  expect(await page.evaluate(() => window.embedDocument)).toBe(true);
});
