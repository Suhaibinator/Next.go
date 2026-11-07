// PROSPECTIVE SOURCE ONLY. These cases require the composed framework, an
// independently prebuilt examples/merge binary and the unchanged native owner.
// Every application response comes from that binary. Gates delay/abort actual
// requests; no route.fulfill, fabricated envelope or client runtime shim exists.
import {test, expect} from './merge-fixture.mjs';
import {holdRequests} from './link-status-gates.mjs';

const recovery = 'Could not update this component. Reload or navigate to recover.';
const panel = (page, name) => page.locator(`[data-merge-panel="${name}"]`);
const part = (page, name, field) => panel(page, name).locator(`[data-merge-${field}]`);
const ordinary = page => page.locator('[data-merge-ordinary]');
const status = page => page.locator('#nextgo-status');
async function health(page) {
  const response = await page.request.get('/api/health'); expect(response.ok()).toBe(true); return response.json();
}
async function control(page, value) {
  const response = await page.request.post('/api/control', {data: value}); expect(response.status()).toBe(200);
}
async function ready(page, query) {
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  for (const name of ['A', 'B']) {
    await expect(panel(page, name)).toHaveAttribute('data-merge-kind', 'client');
    if (query !== undefined) await expect(part(page, name, 'query')).toHaveText(query);
    await expect(part(page, name, 'increment')).toBeEnabled();
  }
}
async function start(page, native, suffix = '?q=a') {
  await page.goto(native.origin + '/' + suffix); await ready(page, new URL(native.origin + '/' + suffix).searchParams.get('q') || '');
  await page.evaluate(() => { globalThis.mergeDocument = document; });
}
async function nativeLink(page, href) {
  // Mutate an existing real server anchor, then use the native document click
  // path. This avoids Playwright focusing/scolling a link in history assertions.
  await page.evaluate(value => {
    const link = document.querySelector('nav[aria-label="Merge example"] a');
    if (!link) throw Error('Actual demo navigation anchor missing');
    link.setAttribute('href', value); link.click();
  }, href);
}
async function navigate(page, href, query) { await nativeLink(page, href); await expect(page).toHaveURL(new URL(href, page.url()).href); await ready(page, query); }
async function revision(page) { return Number(await part(page, 'B', 'revision').textContent()); }
async function refresh(page, from = 'B') {
  const before = await revision(page), loads = (await health(page)).pageLoads;
  await part(page, from, 'refresh').click();
  await expect.poll(() => revision(page)).toBeGreaterThan(before);
  expect((await health(page)).pageLoads).toBeGreaterThan(loads);
}
async function coherent(page) {
  const a = await part(page, 'A', 'revision').textContent();
  expect(Number(a)).toBeGreaterThan(0); await expect(part(page, 'B', 'revision')).toHaveText(a);
  await expect(part(page, 'A', 'coherent')).toHaveText('coherent'); await expect(part(page, 'B', 'coherent')).toHaveText('coherent');
}
async function capture(page) {
  await page.evaluate(() => {
    globalThis.mergeDocument ||= document;
    globalThis.mergeRefs = Object.fromEntries(['A', 'B'].map(name => {
      const panel = document.querySelector(`[data-merge-panel="${name}"]`), root = panel.closest('[data-nextgo-client="1"]');
      const count = panel.querySelector('[data-merge-count]'), file = panel.querySelector('[data-merge-file]');
      return [name, {panel, root, view: panel.parentElement, carrier: root.parentElement, count, text: count.firstChild,
        draft: panel.querySelector('[data-merge-draft]'), scroll: panel.querySelector('[data-merge-scroll]'), file, selected: file.files[0]}];
    }));
  });
}
async function retained(page, names = ['A', 'B']) {
  expect(await page.evaluate(names => names.every(name => {
    const saved = mergeRefs[name], current = document.querySelector(`[data-merge-panel="${name}"]`);
    return document === mergeDocument && current === saved.panel && current.parentElement === saved.view &&
      current.closest('[data-nextgo-client="1"]') === saved.root && saved.root.parentElement === saved.carrier &&
      saved.carrier.parentElement === document.getElementById('nextgo-app') &&
      getComputedStyle(saved.carrier).getPropertyValue('--merge-direct-child').trim() === 'yes' &&
      current.querySelector('[data-merge-count]') === saved.count && saved.count.firstChild === saved.text &&
      current.querySelector('[data-merge-draft]') === saved.draft && current.querySelector('[data-merge-scroll]') === saved.scroll &&
      current.querySelector('[data-merge-file]') === saved.file && saved.file.files[0] === saved.selected;
  }), names)).toBe(true);
}
async function detached(page, name) {
  expect(await page.evaluate(name => !mergeRefs[name].root.isConnected && !mergeRefs[name].draft.isConnected, name)).toBe(true);
}
async function navigationError(page) {
  await expect(status(page)).toBeVisible(); await expect(status(page)).toContainText('Navigation failed. Try again or reload.');
  await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy', 'true');
}
function actualEnvelope(page, query) {
  return page.waitForResponse(response => {
    const request = response.request(), url = new URL(response.url());
    return url.origin === 'http://127.0.0.1:4188' && url.pathname === '/' && url.searchParams.get('q') === query &&
      request.headers()['x-nextgo-navigate'] === '1' && request.headers()['x-nextgo-purpose'] !== 'prefetch';
  }).then(async response => { expect(response.status()).toBe(200); return response.json(); });
}
async function exactFallback(page, name, envelope) {
  expect(typeof envelope.html).toBe('string');
  expect(await page.evaluate(({name, html}) => {
    const parsed = new DOMParser().parseFromString(html, 'text/html');
    const expected = parsed.querySelector(`[data-merge-panel="${name}"]`), current = document.querySelector(`[data-merge-panel="${name}"]`);
    const expectedRoot = expected.closest('[data-nextgo-client="1"]'), currentRoot = current.closest('[data-nextgo-client="1"]');
    return expected.isEqualNode(current) && expectedRoot.childNodes.length === currentRoot.childNodes.length &&
      [...expectedRoot.childNodes].every((node, i) => node.isEqualNode(currentRoot.childNodes[i]));
  }, {name, html: envelope.html})).toBe(true);
}
function gate(page, query, failure = false) {
  return holdRequests(page, '**/*', request => {
    const url = new URL(request.url());
    return url.origin === 'http://127.0.0.1:4188' && url.pathname === '/' && url.searchParams.get('q') === query &&
      request.headers()['x-nextgo-navigate'] === '1' && request.headers()['x-nextgo-purpose'] !== 'prefetch';
  }, failure);
}
function bootGate(page, failure = false) {
  return holdRequests(page, '**/*', request => {
    const url = new URL(request.url());
    return url.origin === 'http://127.0.0.1:4188' && url.pathname === '/boot' && url.searchParams.get('step') === 'arrive' &&
      request.headers()['x-nextgo-navigate'] === '1' && request.headers()['x-nextgo-purpose'] !== 'prefetch';
  }, failure);
}
async function boundBootStart(page, native, held) {
  await page.goto(native.origin + '/boot?step=start', {waitUntil: 'domcontentloaded'}); await held.waitForEntry();
  await expect(page).toHaveTitle('Boot start'); await expect(ordinary(page).locator('[data-ordinary-caption]')).toHaveText('Ordinary initial boot start');
  await expect(ordinary(page).locator('[data-ordinary-increment]')).toBeEnabled();
  await expect(ordinary(page).locator('..')).toHaveAttribute('data-nextgo-hydrated', 'true');
  await page.evaluate(() => {
    globalThis.mergeBootDocument = document; globalThis.mergeBootRoot = document.querySelector('[data-merge-ordinary]').closest('[data-nextgo-island]');
    globalThis.mergeBootCount = mergeBootRoot.querySelector('[data-ordinary-count]');
  });
  await ordinary(page).locator('[data-ordinary-increment]').click(); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('3');
  // Initial bare Mount has returned and the old useful counter really works.
  // Its ready/cleanup tail must not clear the newer actual request's loading.
  await expect(status(page)).toBeVisible(); await expect(status(page)).toHaveText('Loading…');
  await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
  await expect(page.locator('[data-nextgo-client]')).toHaveCount(0); await expect(page.locator('body')).not.toHaveAttribute('data-nextgo-merge', /.+/);
}

test('bare ordinary initial Mount navigation commits arriving SSR only after useful boot binding and keeps newer status authority', async ({page, native}) => {
  const held = await bootGate(page);
  try {
    await boundBootStart(page, native, held); held.release();
    await expect(page).toHaveURL(native.origin + '/boot?step=arrive'); await expect(page).toHaveTitle('Boot arrive');
    await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', 'Ordinary initial boot arrive');
    await expect(ordinary(page).locator('[data-ordinary-caption]')).toHaveText('Ordinary initial boot arrive');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    await expect(ordinary(page).locator('..')).toHaveAttribute('data-nextgo-hydrated', 'true'); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('2');
    expect(await page.evaluate(() => document === mergeBootDocument && !mergeBootRoot.isConnected && mergeBootCount.textContent === '3')).toBe(true);
    await ordinary(page).locator('[data-ordinary-increment]').click(); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('3');
    await expect(status(page)).toBeHidden(); await expect(page.locator('#nextgo-app')).not.toHaveAttribute('aria-busy', 'true');
    expect(await ordinary(page).locator('..').evaluate(node => JSON.parse(node.getAttribute('data-nextgo-props')).boot !== true)).toBe(true);
  } finally { await held.close(); }
});

test('bare ordinary initial Mount navigation network failure preserves initialized old counter and the newer failure status', async ({page, native}) => {
  const held = await bootGate(page, true);
  try {
    await boundBootStart(page, native, held); held.release(); await navigationError(page);
    await expect(page).toHaveURL(native.origin + '/boot?step=start'); await expect(page).toHaveTitle('Boot start');
    await expect(ordinary(page).locator('[data-ordinary-caption]')).toHaveText('Ordinary initial boot start');
    expect(await page.evaluate(() => document === mergeBootDocument && mergeBootRoot === document.querySelector('[data-merge-ordinary]').closest('[data-nextgo-island]') && mergeBootRoot.isConnected)).toBe(true);
    await ordinary(page).locator('[data-ordinary-increment]').click(); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('4');
    await expect(ordinary(page).locator('..')).toHaveAttribute('data-nextgo-hydrated', 'true'); await navigationError(page);
  } finally { await held.close(); }
});

test('useful SSR precedes WASM; ClientView then query preserves exact roots child controls File and direct ancestry', async ({page, native}) => {
  const core = await holdRequests(page, '**/*.wasm', () => true);
  try {
    await page.goto(native.origin + '/?q=a', {waitUntil: 'domcontentloaded'}); await core.waitForEntry();
    await expect(page.getByRole('heading', {level: 1})).toHaveText('Fresh Go server output and local drafts');
    await expect(part(page, 'A', 'price')).toHaveText('11.00'); await expect(part(page, 'B', 'price')).toHaveText('16.00');
    for (const name of ['A', 'B']) { await expect(panel(page, name)).toHaveAttribute('data-merge-kind', 'fallback'); await expect(part(page, name, 'increment')).toBeDisabled(); }
    await page.evaluate(() => {
      globalThis.mergeDocument = document;
      globalThis.mergeSSR = [...document.querySelectorAll('[data-merge-panel]')].map(panel => ({panel, root: panel.closest('[data-nextgo-client="1"]')}));
    });
    core.release(); await ready(page, 'a');
    expect(await page.evaluate(() => mergeSSR.every((old, i) => {
      const current = document.querySelector(`[data-merge-panel="${i ? 'B' : 'A'}"]`);
      return old.root === current.closest('[data-nextgo-client="1"]') && !old.panel.isConnected;
    }))).toBe(true);
    await part(page, 'A', 'increment').click(); await part(page, 'A', 'draft').fill('Kept local draft');
    await part(page, 'A', 'file').setInputFiles({name: 'local.txt', mimeType: 'text/plain', buffer: Buffer.from('real selected bytes')});
    await capture(page); const before = (await health(page)).pageLoads;
    await navigate(page, '?q=b', 'b'); expect((await health(page)).pageLoads).toBeGreaterThan(before);
    await retained(page); await coherent(page); await expect(part(page, 'A', 'count')).toHaveText('3'); await expect(part(page, 'B', 'count')).toHaveText('2');
    await expect(part(page, 'A', 'draft')).toHaveValue('Kept local draft');
    for (const name of ['A', 'B']) await expect(part(page, name, 'mount')).toHaveText('1');
    expect(await part(page, 'A', 'file').evaluate(node => node.files[0].name)).toBe('local.txt');
    expect(await part(page, 'A', 'file').evaluate(node => node.files[0].text())).toBe('real selected bytes');
  } finally { await core.close(); }
});

test('query Back and Forward retain latest draft while restoring each entry focus selection window and inner scroll', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('draft at A'); await capture(page);
  await page.evaluate(() => {
    mergeRefs.A.draft.focus(); mergeRefs.A.draft.setSelectionRange(2, 6); mergeRefs.A.scroll.scrollTop = 90; window.scrollTo(0, 140);
  });
  await page.waitForFunction(() => history.state?.nextgoY === scrollY && scrollY > 0);
  const a = await page.evaluate(() => ({window: scrollY, inner: mergeRefs.A.scroll.scrollTop})); expect(a.inner).toBeGreaterThan(0);
  await navigate(page, '?q=b', 'b'); await part(page, 'A', 'draft').fill('latest shared draft');
  await page.evaluate(() => { mergeRefs.B.draft.focus(); mergeRefs.B.draft.setSelectionRange(0, 0); mergeRefs.A.scroll.scrollTop = 35; window.scrollTo(0, 280); });
  await page.waitForFunction(() => history.state?.nextgoY === scrollY && scrollY > 0);
  const b = await page.evaluate(() => ({window: scrollY, inner: mergeRefs.A.scroll.scrollTop}));
  await page.goBack(); await ready(page, 'a'); await retained(page); await expect(part(page, 'A', 'draft')).toHaveValue('latest shared draft');
  await expect(part(page, 'A', 'draft')).toBeFocused();
  expect(await page.evaluate(() => ({window: scrollY, inner: mergeRefs.A.scroll.scrollTop, start: document.activeElement.selectionStart, end: document.activeElement.selectionEnd}))).toEqual({...a, start: 2, end: 6});
  await page.goForward(); await ready(page, 'b'); await retained(page); await expect(part(page, 'B', 'draft')).toBeFocused();
  expect(await page.evaluate(() => ({window: scrollY, inner: mergeRefs.A.scroll.scrollTop}))).toEqual(b);
  await expect(part(page, 'A', 'draft')).toHaveValue('latest shared draft'); await coherent(page);
});

test('changed Refresh commits server skeleton head and live props; same JSON Refresh still publishes a shared newer revision', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('Refresh keeps me'); await part(page, 'B', 'increment').click(); await capture(page);
  const beforeTitle = await page.title(), beforeGeneration = await page.locator('[data-merge-server]').getAttribute('data-generation');
  await control(page, {version: '2'}); await refresh(page); await ready(page, 'a'); await retained(page); await coherent(page);
  await expect(part(page, 'A', 'price')).toHaveText('21.00'); await expect(part(page, 'B', 'price')).toHaveText('26.00');
  await expect(part(page, 'A', 'version')).toHaveText('2'); await expect(part(page, 'B', 'count')).toHaveText('3'); await expect(part(page, 'A', 'draft')).toHaveValue('Refresh keeps me');
  expect(await page.title()).not.toBe(beforeTitle);
  expect(await page.locator('[data-merge-server]').getAttribute('data-generation')).not.toBe(beforeGeneration);
  await expect(page.locator('[data-merge-server]')).toContainText('version 2; query a');
  await expect(page.locator('meta[name="description"]')).toHaveAttribute('content', /Fresh public server description \d+/);
  await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', await page.title());
  const same = await page.evaluate(() => mergeRefs.A.root.getAttribute('data-nextgo-props'));
  const title = await page.title(), description = await page.locator('meta[name="description"]').getAttribute('content');
  await refresh(page); await retained(page); await coherent(page);
  expect(await page.evaluate(() => mergeRefs.A.root.getAttribute('data-nextgo-props'))).toBe(same);
  expect(await page.title()).not.toBe(title); expect(await page.locator('meta[name="description"]').getAttribute('content')).not.toBe(description);
  for (const name of ['A', 'B']) await expect(part(page, name, 'mount')).toHaveText('1');
});

test('equal editor keys under distinct declared parents stay independent; changing only A key resets only A', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('A keyed'); await part(page, 'B', 'draft').fill('B independent');
  await part(page, 'B', 'increment').click(); await capture(page); await navigate(page, '?q=b', 'b'); await retained(page);
  await expect(part(page, 'A', 'draft')).toHaveValue('A keyed'); await expect(part(page, 'B', 'draft')).toHaveValue('B independent');
  await navigate(page, '?q=c&keyed=1', 'c');
  await detached(page, 'A'); await retained(page, ['B']); await expect(part(page, 'A', 'count')).toHaveText('2'); await expect(part(page, 'A', 'draft')).toHaveValue('');
  await expect(part(page, 'A', 'mount')).toHaveText('2'); await expect(part(page, 'B', 'mount')).toHaveText('1'); await expect(part(page, 'B', 'count')).toHaveText('3'); await expect(part(page, 'B', 'draft')).toHaveValue('B independent');
  expect(await page.evaluate(() => document === mergeDocument)).toBe(true);
});

test('schema mismatch replaces exactly B while A remains live and stateful', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('A survives B schema'); await part(page, 'A', 'increment').click(); await capture(page);
  await control(page, {schema: '2'}); await navigate(page, '?q=b', 'b'); await retained(page, ['A']); await detached(page, 'B');
  await expect(part(page, 'A', 'count')).toHaveText('3'); await expect(part(page, 'A', 'draft')).toHaveValue('A survives B schema');
  await expect(part(page, 'A', 'mount')).toHaveText('1'); await expect(part(page, 'B', 'mount')).toHaveText('2');
  await part(page, 'B', 'increment').click(); await expect(part(page, 'B', 'count')).toHaveText('3');
});

test('props validation failure shows the exact fresh fallback while a healthy independent sibling retains state and recovery works', async ({page, native}) => {
  await start(page, native); await part(page, 'B', 'draft').fill('B survives validation'); await capture(page);
  await control(page, {version: '2', reject: 'A'}); const received = actualEnvelope(page, 'b'); await nativeLink(page, '?q=b');
  await expect(page).toHaveURL(native.origin + '/?q=b'); await expect(panel(page, 'A')).toHaveAttribute('data-merge-kind', 'fallback');
  await expect(part(page, 'A', 'price')).toHaveText('21.00'); await expect(part(page, 'A', 'query')).toHaveText('b'); await expect(part(page, 'A', 'increment')).toBeDisabled();
  await exactFallback(page, 'A', await received);
  await detached(page, 'A'); await retained(page, ['B']); await expect(part(page, 'B', 'draft')).toHaveValue('B survives validation');
  await part(page, 'B', 'increment').click(); await expect(part(page, 'B', 'count')).toHaveText('3'); await expect(status(page)).toHaveText(recovery);
  await control(page, {reject: 'none'}); await refresh(page); await ready(page, 'b'); await retained(page, ['B']);
  await part(page, 'A', 'increment').click(); await expect(part(page, 'A', 'count')).toHaveText('3');
});

test('postcommit effect failure disposes the failed child and installs fresh fallback without losing a healthy sibling', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('old failed draft'); await part(page, 'B', 'draft').fill('healthy B'); await capture(page);
  await control(page, {version: '2', effectFail: 'A'}); const received = actualEnvelope(page, 'b'); await nativeLink(page, '?q=b');
  await expect(page).toHaveURL(native.origin + '/?q=b'); await expect(panel(page, 'A')).toHaveAttribute('data-merge-kind', 'fallback');
  await expect(part(page, 'A', 'price')).toHaveText('21.00'); await expect(part(page, 'A', 'query')).toHaveText('b'); await expect(part(page, 'A', 'draft')).toHaveValue('');
  await exactFallback(page, 'A', await received);
  expect(await page.evaluate(() => !mergeRefs.A.view.isConnected && !mergeRefs.A.draft.isConnected)).toBe(true);
  await retained(page, ['B']); await expect(status(page)).toHaveText(recovery); await expect(part(page, 'B', 'draft')).toHaveValue('healthy B');
  await part(page, 'B', 'increment').click(); await expect(part(page, 'B', 'count')).toHaveText('3');
  await page.evaluate(() => mergeRefs.A.panel.querySelector('[data-merge-increment]').click());
  expect(await page.evaluate(() => mergeRefs.A.count.textContent)).toBe('2');
  await control(page, {effectFail: 'none'}); await refresh(page); await ready(page, 'b'); await retained(page, ['B']);
  await part(page, 'A', 'increment').click(); await expect(part(page, 'A', 'count')).toHaveText('3');
});

test('a genuine held B response superseded by C never commits B or a ghost history entry', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('survives supersession'); await capture(page);
  const held = await gate(page, 'b');
  try {
    await nativeLink(page, '?q=b'); await held.waitForEntry(); await expect(status(page)).toHaveText('Loading…');
    await expect(page.locator('#nextgo-app')).toHaveAttribute('aria-busy', 'true');
    await expect(part(page, 'A', 'query')).toHaveText('a'); await expect(page).toHaveURL(native.origin + '/?q=a');
    await navigate(page, '?q=c', 'c'); await held.close(); await retained(page); await coherent(page);
    await page.goBack(); await ready(page, 'a'); await expect(page).toHaveURL(native.origin + '/?q=a');
    await retained(page); await expect(part(page, 'A', 'draft')).toHaveValue('survives supersession');
  } finally { await held.close(); }
});

test('real navigation transport failure preserves useful accepted state and a genuine retry recovers', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('network keeps draft'); await capture(page); const title = await page.title();
  const held = await gate(page, 'b', true);
  try { await nativeLink(page, '?q=b'); await held.waitForEntry(); held.release(); await navigationError(page); }
  finally { await held.close(); }
  await expect(page).toHaveURL(native.origin + '/?q=a'); expect(await page.title()).toBe(title); await retained(page);
  await part(page, 'A', 'increment').click(); await expect(part(page, 'A', 'count')).toHaveText('3');
  await navigate(page, '?q=b', 'b'); await expect(part(page, 'A', 'draft')).toHaveValue('network keeps draft');
  expect(await page.evaluate(() => document === mergeDocument)).toBe(true);
});

test('real Go props effect starts failing newer C only after accepted B sibling effects and new ordinary Mount complete', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('B remains authoritative'); await capture(page);
  await control(page, {version: '2', navigate: 'C'}); const held = await gate(page, 'c', true);
  try {
    await nativeLink(page, '?q=b&ordinary=1'); await held.waitForEntry(); await ready(page, 'b'); await coherent(page);
    await expect(page).toHaveURL(native.origin + '/?q=b&ordinary=1'); await expect(ordinary(page).locator('[data-ordinary-increment]')).toBeEnabled();
    await expect(part(page, 'B', 'price')).toHaveText('26.00'); await expect(page.locator('[data-merge-server]')).toContainText('version 2; query b'); await retained(page);
    await ordinary(page).locator('[data-ordinary-increment]').click(); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('3');
    const title = await page.title(); held.release(); await navigationError(page);
    expect(await page.title()).toBe(title); await expect(page).toHaveURL(native.origin + '/?q=b&ordinary=1'); await retained(page);
    await part(page, 'B', 'increment').click(); await expect(part(page, 'B', 'count')).toHaveText('3');
    await expect(part(page, 'A', 'draft')).toHaveValue('B remains authoritative');
  } finally { await held.close(); }
});

test('fresh partition and denial retire old DOM and data authority with ordinary same-document recovery', async ({page, native}) => {
  await start(page, native); await part(page, 'A', 'draft').fill('private old draft'); await capture(page);
  await control(page, {partition: '2'}); await navigate(page, '?q=b', 'b');
  await detached(page, 'A'); await detached(page, 'B'); await expect(part(page, 'A', 'draft')).toHaveValue('');
  await capture(page); await control(page, {deny: true}); await nativeLink(page, '?q=c');
  await expect(page.getByRole('heading', {level: 1})).toHaveText('Access denied'); await expect(page.locator('[data-merge-panel]')).toHaveCount(0);
  await detached(page, 'A'); await detached(page, 'B'); expect(await page.evaluate(() => document === mergeDocument)).toBe(true);
  await page.evaluate(() => mergeRefs.A.panel.querySelector('[data-merge-increment]').click()); expect(await page.evaluate(() => mergeRefs.A.count.textContent)).toBe('2');
  await control(page, {deny: false}); await page.getByRole('link', {name: 'Return to public demo'}).click(); await ready(page, 'a');
  await expect(part(page, 'A', 'draft')).toHaveValue(''); await part(page, 'A', 'increment').click(); await expect(part(page, 'A', 'count')).toHaveText('3');
  expect(await page.evaluate(() => document === mergeDocument)).toBe(true);
});

test('fresh ordinary SSR component remounts and hydrates while adjacent compatible ClientView roots keep controls and state', async ({page, native}) => {
  await start(page, native, '?q=a&ordinary=1'); await part(page, 'A', 'draft').fill('retained client draft'); await capture(page);
  await expect(ordinary(page).locator('[data-ordinary-increment]')).toBeEnabled(); await ordinary(page).locator('[data-ordinary-increment]').click(); await ordinary(page).locator('[data-ordinary-draft]').fill('ordinary resets');
  await page.evaluate(() => { globalThis.mergeOrdinary = document.querySelector('[data-merge-ordinary]'); });
  await navigate(page, '?q=b&ordinary=1', 'b'); await retained(page); await coherent(page);
  expect(await page.evaluate(() => !mergeOrdinary.isConnected)).toBe(true);
  await expect(ordinary(page).locator('[data-ordinary-caption]')).toHaveText('Ordinary fresh version 1 query b'); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('2'); await expect(ordinary(page).locator('[data-ordinary-draft]')).toHaveValue('');
  await ordinary(page).locator('[data-ordinary-increment]').click(); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('3'); await expect(part(page, 'A', 'draft')).toHaveValue('retained client draft');
});

test('repeated and empty raw query values remain server-authoritative through navigation Refresh and direct dynamic access', async ({page, native}) => {
  await start(page, native, '?q=a&q=&q=two'); await capture(page);
  for (const name of ['A', 'B']) await expect(part(page, name, 'raw-query')).toHaveText('q=a&q=&q=two');
  await refresh(page); await retained(page); await coherent(page);
  await navigate(page, '?q=b&q=&q=last', 'b'); await retained(page);
  for (const name of ['A', 'B']) await expect(part(page, name, 'raw-query')).toHaveText('q=b&q=&q=last');
  // A genuine independent direct request proves the generated dynamic route,
  // initial HTML and loader; it deliberately opens a new document.
  await page.goto(native.origin + '/products/tea?q=direct'); await ready(page, 'direct');
  await expect(part(page, 'A', 'product')).toHaveText('tea'); await expect(page).toHaveTitle(/^Merge tea · direct · server \d+$/);
});

test('generated nested layouts emit independent merge authority and query Refresh keep stationary roots with fresh layout text and head', async ({page, native}) => {
  await start(page, native, 'layouts/products/tea?q=a');
  await part(page, 'A', 'increment').click(); await part(page, 'A', 'draft').fill('Local nested layout draft'); await capture(page);
  await expect(page.locator('[data-merge-layout="outer"]')).toHaveAttribute('data-layout-query', 'a');
  await expect(page.locator('[data-merge-layout="inner"]')).toHaveAttribute('data-layout-query', 'a');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-merge', /.+/); await expect(page.locator('body')).not.toHaveAttribute('data-nextgo-retention', /.+/);
  await page.evaluate(() => {
    globalThis.mergeLayoutNodes = [...document.querySelectorAll('nextgo-layout,nextgo-outlet,[data-merge-layout]')];
    globalThis.mergeLayoutLeaf = mergeRefs.A.carrier.parentElement;
  });
  const received = page.waitForResponse(response => new URL(response.url()).pathname === '/layouts/products/tea' && new URL(response.url()).search === '?q=b' && response.request().headers()['x-nextgo-navigate'] === '1');
  await navigate(page, '?q=b', 'b'); await coherent(page);
  const envelope = await (await received).json(); expect(envelope.retention).toBeUndefined(); expect(envelope.merge?.version).toBe(1);
  expect(envelope.merge.owners.filter(owner => owner.kind === 'layout').length).toBe(2);
  await expect(page.locator('[data-layout-fresh="outer"]')).toHaveText('Outer layout query b'); await expect(page.locator('[data-layout-fresh="inner"]')).toHaveText('Inner layout query b');
  const identity = () => page.evaluate(() => {
    const now = [...document.querySelectorAll('nextgo-layout,nextgo-outlet,[data-merge-layout]')];
    return document === mergeDocument && now.length === mergeLayoutNodes.length && now.every((node, i) => node === mergeLayoutNodes[i] && node.isConnected) &&
      ['A', 'B'].every(name => {
        const saved = mergeRefs[name], current = document.querySelector(`[data-merge-panel="${name}"]`);
        return current === saved.panel && current.parentElement === saved.view && current.closest('[data-nextgo-client="1"]') === saved.root &&
          saved.root.parentElement === saved.carrier && saved.carrier.parentElement === mergeLayoutLeaf && current.querySelector('[data-merge-draft]') === saved.draft && saved.count.firstChild === saved.text;
      });
  });
  expect(await identity()).toBe(true); await expect(part(page, 'A', 'count')).toHaveText('3'); await expect(part(page, 'A', 'draft')).toHaveValue('Local nested layout draft');
  await expect(page).toHaveTitle(/^Merge tea · b · server \d+$/); const title = await page.title();
  await control(page, {version: '2'}); await refresh(page); await ready(page, 'b'); await coherent(page);
  expect(await identity()).toBe(true); await expect(part(page, 'A', 'price')).toHaveText('21.00'); await expect(part(page, 'B', 'price')).toHaveText('26.00');
  expect(await page.title()).not.toBe(title); await expect(page.locator('meta[property="og:title"]')).toHaveAttribute('content', await page.title());
  await expect(part(page, 'A', 'draft')).toHaveValue('Local nested layout draft'); for (const name of ['A', 'B']) await expect(part(page, name, 'mount')).toHaveText('1');
});

test('sole ordinary initial-only Props source cold Mounts exact same SSR on query and Refresh instead of reusing a stale V1 handle', async ({page, native}) => {
  await page.goto(native.origin + '/plain-props?q=a'); await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  await expect(page.locator('[data-nextgo-island]')).toHaveCount(1); await expect(page.locator('[data-nextgo-client]')).toHaveCount(0);
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-retention', /.+/); await expect(page.locator('body')).not.toHaveAttribute('data-nextgo-merge', /.+/);
  const sourceRevision = () => ordinary(page).locator('[data-ordinary-props-revision]');
  await expect(ordinary(page).locator('[data-ordinary-read]')).toBeEnabled(); const a = Number(await sourceRevision().textContent()); expect(a).toBeGreaterThan(0);
  await ordinary(page).locator('[data-ordinary-read]').click(); await expect(sourceRevision()).toHaveText(String(a)); await expect(ordinary(page).locator('[data-ordinary-props-location]')).toHaveText('?q=a');
  await ordinary(page).locator('[data-ordinary-increment]').click(); await ordinary(page).locator('[data-ordinary-draft]').fill('Ordinary source must reset');
  await page.evaluate(() => { globalThis.mergePlainDocument = document; globalThis.mergePlainOld = document.querySelector('[data-merge-ordinary]').closest('[data-nextgo-island]'); globalThis.mergePlainOldRevision = mergePlainOld.querySelector('[data-ordinary-props-revision]').textContent; });
  await ordinary(page).locator('[data-ordinary-next]').click(); await expect(page).toHaveURL(native.origin + '/plain-props?q=b');
  await expect.poll(async () => Number(await sourceRevision().textContent())).toBeGreaterThan(a);
  expect(await page.evaluate(() => document === mergePlainDocument && !mergePlainOld.isConnected)).toBe(true);
  await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('2'); await expect(ordinary(page).locator('[data-ordinary-draft]')).toHaveValue('');
  await ordinary(page).locator('[data-ordinary-read]').click(); await expect(ordinary(page).locator('[data-ordinary-props-location]')).toHaveText('?q=b');
  await page.evaluate(() => mergePlainOld.querySelector('[data-ordinary-read]').click()); expect(await page.evaluate(() => mergePlainOld.querySelector('[data-ordinary-props-revision]').textContent === mergePlainOldRevision)).toBe(true);
  const b = Number(await sourceRevision().textContent()); await ordinary(page).locator('[data-ordinary-increment]').click(); await ordinary(page).locator('[data-ordinary-draft]').fill('Refresh must reset too');
  await page.evaluate(() => { globalThis.mergePlainBeforeRefresh = document.querySelector('[data-merge-ordinary]').closest('[data-nextgo-island]'); });
  await ordinary(page).locator('[data-ordinary-refresh]').click(); await expect.poll(async () => Number(await sourceRevision().textContent())).toBeGreaterThan(b);
  await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('2'); await expect(ordinary(page).locator('[data-ordinary-draft]')).toHaveValue('');
  expect(await page.evaluate(() => document === mergePlainDocument && !mergePlainBeforeRefresh.isConnected)).toBe(true);
  await ordinary(page).locator('[data-ordinary-read]').click(); await expect(ordinary(page).locator('[data-ordinary-props-location]')).toHaveText('?q=b');
  expect(Number(await sourceRevision().textContent())).toBeGreaterThan(b); await ordinary(page).locator('[data-ordinary-increment]').click(); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('3');
  // An ordinary-only Props page has no MergeDescriptor. Recognized denial
  // must still retire its source/events and permit fresh ordinary recovery.
  // The deterministic core test separately holds a callback across revocation;
  // this browser sequence makes no claim to observe that held-callback timing.
  await page.evaluate(() => {
    globalThis.mergePlainBeforeDenial = document.querySelector('[data-merge-ordinary]').closest('[data-nextgo-island]');
    globalThis.mergePlainDeniedRevision = mergePlainBeforeDenial.querySelector('[data-ordinary-props-revision]').textContent;
    const link = mergePlainBeforeDenial.querySelector('[data-ordinary-next]'); link.setAttribute('href', '/?q=denied');
  });
  await control(page, {deny: true}); await ordinary(page).locator('[data-ordinary-next]').click();
  await expect(page.getByRole('heading', {level: 1})).toHaveText('Access denied'); await expect(page).toHaveTitle('Access denied');
  expect(await page.evaluate(() => document === mergePlainDocument && !mergePlainBeforeDenial.isConnected)).toBe(true);
  await page.evaluate(() => { mergePlainBeforeDenial.querySelector('[data-ordinary-increment]').click(); mergePlainBeforeDenial.querySelector('[data-ordinary-read]').click(); });
  expect(await page.evaluate(() => ({count: mergePlainBeforeDenial.querySelector('[data-ordinary-count]').textContent,
    sourceUnchanged: mergePlainBeforeDenial.querySelector('[data-ordinary-props-revision]').textContent === mergePlainDeniedRevision}))).toEqual({count: '3', sourceUnchanged: true});
  await control(page, {deny: false}); await page.getByRole('link', {name: 'Return to public demo'}).evaluate(link => link.setAttribute('href', '/plain-props?q=recovered'));
  await page.getByRole('link', {name: 'Return to public demo'}).click(); await expect(page).toHaveURL(native.origin + '/plain-props?q=recovered');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready'); await expect(ordinary(page).locator('[data-ordinary-read]')).toBeEnabled();
  await ordinary(page).locator('[data-ordinary-read]').click(); await expect(ordinary(page).locator('[data-ordinary-props-location]')).toHaveText('?q=recovered');
  await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('2'); await expect(ordinary(page).locator('[data-ordinary-draft]')).toHaveValue('');
  expect(await page.evaluate(() => document === mergePlainDocument && !document.getElementById('nextgo-app').hidden && !document.getElementById('nextgo-app').hasAttribute('inert'))).toBe(true);
  await ordinary(page).locator('[data-ordinary-increment]').click(); await expect(ordinary(page).locator('[data-ordinary-count]')).toHaveText('3');
});


test('actual lazy ordinary Go source revokes loaded and download-pending owners and recovers fresh same-document scopes', async ({page, native}, info) => {
  const lazy = () => page.locator('[data-lazy-ordinary]');
  const field = name => lazy().locator(`[data-lazy-${name}]`);
  const moduleURL = value => { const url = new URL(value); return url.origin === native.origin && /\/module-[a-f0-9]{64}\.wasm$/.test(url.pathname); };
  const {createHash} = await import('node:crypto');
  let moduleRequests = 0, moduleResponses = 0; const responses = [], moduleAssets = [];
  const verifyModule = async (response, phase) => {
    expect(response.status()).toBe(200); expect(response.headers()['content-type']?.split(';')[0]).toBe('application/wasm'); expect(await response.finished()).toBeNull();
    const bytes = await response.body(), digest = createHash('sha256').update(bytes).digest('hex');
    expect(bytes.length).toBeGreaterThan(0); expect(bytes.length).toBeLessThanOrEqual(8 << 20);
    const expected = /\/module-([a-f0-9]{64})\.wasm$/.exec(new URL(response.url()).pathname); expect(expected).not.toBeNull(); expect(digest).toBe(expected[1]);
    const wasmMagic = bytes.subarray(0, 8).toString('hex'); expect(wasmMagic).toBe('0061736d01000000');
    const identity = await page.evaluate(() => ({build: document.body.dataset.nextgoBuild, buildPrefix: document.body.dataset.nextgoRuntime, hostABI: globalThis.__nextgoModuleHost?.version}));
    expect(identity.build).toMatch(/^[a-f0-9]{20}$/); expect(identity.buildPrefix).toBe(`/_nextgo/${identity.build}/`); expect(identity.hostABI).toBe(6);
    expect(new URL(response.url()).pathname).toBe(`${identity.buildPrefix}module-${digest}.wasm`);
    moduleAssets.push({phase, url: response.url(), status: response.status(), mime: response.headers()['content-type'].split(';')[0], bytes: bytes.length, sha256: digest, complete: true, wasmMagic, hostABI: identity.hostABI, buildPrefix: identity.buildPrefix});
  };
  const requested = request => { if (moduleURL(request.url())) moduleRequests++; };
  const responded = response => { if (moduleURL(response.url())) { moduleResponses++; if (responses.length < 4) responses.push(response); } };
  page.on('request', requested); page.on('response', responded);
  const readyLazy = async query => {
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    await expect(lazy().locator('..')).toHaveAttribute('data-nextgo-hydrated', 'true');
    await expect(field('increment')).toBeEnabled(); await expect(field('read')).toBeEnabled();
    expect(await page.evaluate(() => globalThis.__nextgoModuleHost?.version)).toBe(6);
    await field('read').click(); await expect(field('query')).toHaveText(query);
    await expect(field('location')).toHaveText(`?q=${query}`); expect(Number(await field('revision').textContent())).toBeGreaterThan(0);
    await expect(field('caption')).toHaveText(`Fresh lazy ordinary source query ${query}`);
  };
  const deny = async () => {
    await control(page, {deny: true});
    const denied = page.waitForResponse(response => {
      const url = new URL(response.url()); return url.origin === native.origin && url.pathname === '/' && url.search === '?q=denied' && response.request().headers()['x-nextgo-navigate'] === '1';
    });
    await field('deny').click(); expect((await denied).status()).toBe(403);
    await expect(page.getByRole('heading', {level: 1})).toHaveText('Access denied'); await expect(page).toHaveTitle('Access denied');
  };
  const recover = async query => {
    await control(page, {deny: false}); const link = page.getByRole('link', {name: 'Return to public demo'});
    await link.evaluate((node, href) => node.setAttribute('href', href), `/lazy-props?q=${query}`); await link.click();
    await expect(page).toHaveURL(native.origin + `/lazy-props?q=${query}`); await readyLazy(query);
    await expect(field('count')).toHaveText('2'); await expect(field('draft')).toHaveValue('');
    expect(await page.evaluate(() => !document.getElementById('nextgo-app').hidden && !document.getElementById('nextgo-app').hasAttribute('inert'))).toBe(true);
  };
  try {
    // This is a genuine generated lazy package and actual browser module GET.
    // No core-only ordinary component or mocked mount substitutes for it.
    await page.goto(native.origin + '/lazy-props?q=a'); await readyLazy('a');
    await expect(page.locator('[data-nextgo-client]')).toHaveCount(0);
    await expect.poll(() => moduleResponses).toBe(1); expect(moduleRequests).toBe(1);
    await verifyModule(responses[0], 'initial');
    await field('increment').click(); await expect(field('count')).toHaveText('3'); await field('draft').fill('Lazy old private draft');
    const mounted = Number(await field('mount').textContent()); expect(mounted).toBeGreaterThan(0);
    await page.evaluate(() => {
      globalThis.mergeLazyDocument = document; globalThis.mergeLazyOld = document.querySelector('[data-lazy-ordinary]').closest('[data-nextgo-island]');
      globalThis.mergeLazyOldCount = mergeLazyOld.querySelector('[data-lazy-count]'); globalThis.mergeLazyOldButton = mergeLazyOld.querySelector('[data-lazy-increment]');
      globalThis.mergeLazyOldRevision = mergeLazyOld.querySelector('[data-lazy-revision]').textContent;
    });
    await deny();
    expect(await page.evaluate(() => document === mergeLazyDocument && !mergeLazyOld.isConnected)).toBe(true);
    await page.evaluate(() => { mergeLazyOldButton.click(); mergeLazyOld.querySelector('[data-lazy-read]').click(); });
    expect(await page.evaluate(() => ({count: mergeLazyOldCount.textContent, revisionUnchanged: mergeLazyOld.querySelector('[data-lazy-revision]').textContent === mergeLazyOldRevision}))).toEqual({count: '3', revisionUnchanged: true});
    await recover('recovered');
    expect(Number(await field('mount').textContent())).toBeGreaterThan(mounted);
    expect(await page.evaluate(() => document === mergeLazyDocument && document.querySelector('[data-lazy-ordinary]').closest('[data-nextgo-island]') !== mergeLazyOld)).toBe(true);
    await field('increment').click(); await expect(field('count')).toHaveText('3'); expect(moduleRequests).toBe(1);

    // A deliberate fresh document gives a genuinely unstarted module runtime.
    // Delay its real network request, deny while the proxy is pending, then
    // release actual bytes. This is not a held authored-callback timing claim.
    const held = await holdRequests(page, '**/*', request => moduleURL(request.url()));
    try {
      await page.goto(native.origin + '/lazy-props?q=pending', {waitUntil: 'domcontentloaded'}); await held.waitForEntry();
      await expect(lazy().locator('..')).toHaveAttribute('data-nextgo-hydrated', 'loading'); await expect(field('increment')).toBeDisabled();
      await expect(field('count')).toHaveText('2'); await expect(field('caption')).toHaveText('Fresh lazy ordinary source query pending');
      await page.evaluate(() => { globalThis.mergeLazyPendingDocument = document; globalThis.mergeLazyPendingOld = document.querySelector('[data-lazy-ordinary]').closest('[data-nextgo-island]'); });
      await deny(); expect(await page.evaluate(() => document === mergeLazyPendingDocument && !mergeLazyPendingOld.isConnected)).toBe(true);
      held.release(); await expect.poll(() => moduleResponses).toBe(2); expect(moduleRequests).toBe(2);
      await verifyModule(responses[1], 'held-recovery'); expect(moduleAssets[1].sha256).toBe(moduleAssets[0].sha256); expect(moduleAssets[1].bytes).toBe(moduleAssets[0].bytes);
      await recover('after-pending');
      // The canceled pending owner must never have called the actual module
      // Mount: this fresh runtime's first successful owner has mount number1.
      await expect(field('mount')).toHaveText('1');
      expect(await page.evaluate(() => document === mergeLazyPendingDocument && document.querySelector('[data-lazy-ordinary]').closest('[data-nextgo-island]') !== mergeLazyPendingOld && mergeLazyPendingOld.getAttribute('data-nextgo-hydrated') !== 'true')).toBe(true);
      await page.evaluate(() => { const button = mergeLazyPendingOld.querySelector('[data-lazy-increment]'); button.disabled = false; button.click(); });
      expect(await page.evaluate(() => mergeLazyPendingOld.querySelector('[data-lazy-count]').textContent)).toBe('2');
      await field('increment').click(); await expect(field('count')).toHaveText('3'); await field('read').click(); await expect(field('query')).toHaveText('after-pending'); expect(moduleRequests).toBe(2);
    } finally { await held.close(); }
  } finally {
    page.off('request', requested); page.off('response', responded);
    await info.attach('merge-lazy-module-proof', {body: JSON.stringify({version: 1, abi: 6, records: moduleAssets}), contentType: 'application/json'});
  }
});
