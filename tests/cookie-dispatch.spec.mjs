import { test, expect } from '@playwright/test';
import { randomUUID } from 'node:crypto';
import { readerToken, adminToken } from './access-fixture.mjs';

const origin = 'http://127.0.0.1:4194';
const loginPath = '/_nextgo/actions/session/login/v1', logoutPath = '/_nextgo/actions/session/logout/v1';
const optimisticPath = '/_nextgo/actions/optimistic-increment/v1', viewPath = '/_nextgo/actions/view/save/v1';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const role = page => page.locator('[data-session-role]');
const credential = page => page.locator('[data-session-credential]');
const receipt = page => page.locator('[data-av-receipt]');
const numeric = async locator => Number(await locator.textContent());
const extra = (trace, fields = {}) => ({ 'X-Fixture-Cookie-Trace': trace, ...fields });
const control = (operation, key) => `${origin}/_fixture/cookie/${operation}?${new URLSearchParams({ key })}`;
async function gate(request, key) { return (await request.get(control('gate', key))).json(); }
async function held(request, key, phase = 'body') {
  await expect.poll(async () => { const value = await gate(request, key); return value.held && value.phase; }).toBe(phase);
  return gate(request, key);
}
async function records(request, trace) { return (await (await request.get(`${origin}/_fixture/cookie/records`)).json()).filter(record => record.trace === trace); }
function posts(actual, suffix) { return actual.filter(record => record.method === 'POST' && (!suffix || new URL(record.path, origin).pathname.endsWith(suffix))); }
function assertResponseProtocol(response, expected = 200) {
  expect(response.status()).toBe(expected); expect(response.headers()['x-nextgo-action-protocol']).toBe('1'); expect(response.headers()['x-nextgo-action-revision']).toBe('v1');
}
function assertWireProtocol(record, expected) {
  expect(!!record).toBe(true); expect(record.status).toBe(expected); expect(record.protocolOne).toBe(true); expect(record.revisionOne).toBe(true);
}
async function submit(page, button, suffix, expected = 200, wire) {
  const waiting = page.waitForResponse(response => new URL(response.url()).pathname.endsWith(suffix) && response.request().method() === 'POST');
  await button.click(); const response = await waiting;
  if (expected === 303) {
    // WebKit's native redirect inspector omits custom protocol headers even
    // from allHeaders(). Assert the actual upstream fields for every engine,
    // then independently check browser status/location, cookies and fresh HTML.
    expect(response.status()).toBe(303);
    assertWireProtocol(posts(await records(wire.request, wire.trace), suffix).at(-1), 303);
  } else assertResponseProtocol(response, expected);
  return response;
}
async function session(page, expected) {
  await expect(role(page)).toHaveText(expected); await expect(page).toHaveTitle(`Cookie session: ${expected} — Next.go`);
  await expect(page.locator('meta[name="cookie-session-role"]')).toHaveAttribute('content', expected);
  await expect(page.locator('[data-session-panel-role]')).toHaveText(expected);
  await expect(page.locator('[data-session-admin-report]')).toHaveCount(expected === 'administrator' ? 1 : 0);
}
async function login(page, token, expected, selector = '[data-session-login]') {
  await credential(page).fill(token); const response = await submit(page, page.locator(selector), loginPath);
  if (selector === '[data-session-login]') await session(page, expected);
  return response;
}
async function scopedCookie(context, url, path) {
  const cookie = (await context.cookies(url)).find(value => /^nextgo_demo_session_[a-f0-9]{12}$/.test(value.name) && value.path === path);
  expect(!!cookie).toBe(true);
  expect(cookie.httpOnly).toBe(true); expect(cookie.sameSite).toBe('Lax'); expect(cookie.secure).toBe(false);
  const now = Date.now() / 1000; expect(cookie.expires).toBeGreaterThan(now); expect(cookie.expires).toBeLessThanOrEqual(now + 905);
  return cookie;
}
function decodeFrames(text) { return text.trim().split('\n').map(line => JSON.parse(line)); }

test('guest SSR preserves its DOM through delayed WASM and a single cookie action returns fresh authorized HTML', async ({ page, context, request }) => {
  const trace = randomUUID(), wasm = `${trace}-wasm`; await context.addCookies([{ name: 'preferences', value: 'dark', url: origin }]);
  await page.setExtraHTTPHeaders(extra(trace, { 'X-Fixture-Cookie-Wasm': wasm }));
  try {
    const initial = await page.goto('/cookie-session', { waitUntil: 'commit' }); expect(initial.status()).toBe(200);
    expect(initial.headers()['cache-control']).toBe('private, no-store'); expect(initial.headers()['x-robots-tag']).toBe('noindex');
    await held(request, wasm, 'wasm'); await expect(page.getByRole('heading', { name: 'Signed cookie session', exact: true })).toBeVisible();
    await expect(role(page)).toHaveText('anonymous'); await expect(page.locator('[data-session-admin-report]')).toHaveCount(0);
    await expect(page.locator('[data-session-login]')).toBeEnabled(); await expect(page.locator('[data-session-login-only]')).toBeDisabled();
    await page.evaluate(() => { window.cookieDocument = true; window.cookieRoot = document.querySelector('[data-nextgo-island="sessionpanel"]'); window.cookieRole = window.cookieRoot.querySelector('[data-session-panel-role]'); window.cookieRoleText = window.cookieRole.firstChild; });
    await credential(page).fill(adminToken); expect((await request.post(control('release', wasm))).status()).toBe(200); await ready(page);
    expect(await page.evaluate(() => window.cookieRoot === document.querySelector('[data-nextgo-island="sessionpanel"]') && window.cookieRole === document.querySelector('[data-session-panel-role]') && window.cookieRoleText === window.cookieRole.firstChild)).toBe(true);
    await expect(credential(page)).toHaveValue(adminToken); const history = await page.evaluate(() => window.history.length);
    await page.setExtraHTTPHeaders(extra(trace)); const response = await submit(page, page.locator('[data-session-login]'), loginPath);
    await session(page, 'administrator'); await expect(page.locator('[data-session-status]')).toHaveText('Signed in as administrator.');
    expect(response.headers()['content-type']).toContain('application/x-nextgo-action-view+json'); expect(response.headers()['cache-control']).toBe('private, no-store');
    await expect(credential(page)).toHaveValue('');
    expect(await page.evaluate(() => window.cookieDocument && !window.cookieRoot.isConnected && window.cookieRoot.getAttribute('data-session-closed') === 'true' && window.cookieRoot.querySelector('[data-session-credential]').value === '')).toBe(true);
    expect(await page.evaluate(() => window.history.length)).toBe(history);
    const actualCookie = await scopedCookie(context, origin + '/cookie-session', '/');
    expect(await page.evaluate(name => document.cookie.includes(name + '='), actualCookie.name)).toBe(false);
    const wire = decodeFrames(await response.text()); expect(wire[0].type).toBe('result'); expect(wire[0].result.data).toEqual({ role: 'administrator' });
    expect(Object.keys(wire[0].result).sort()).toEqual(['data', 'redirect']); expect(wire[1].type).toBe('shell'); expect(wire.at(-1).type).toBe('done');
    const props = await page.locator('[data-nextgo-island="sessionpanel"]').getAttribute('data-nextgo-props');
    expect(Object.keys(JSON.parse(props)).sort()).toEqual(['Enabled', 'LoginAction', 'LogoutAction', 'Role']);
    const actual = await records(request, trace); expect(posts(actual)).toHaveLength(1); expect(actual.filter(record => record.method === 'GET')).toHaveLength(1); expect(posts(actual)[0].hasSetCookie).toBe(true);
  } finally { await request.post(control('release', wasm)); }
});

test('one logout POST clears the actual cookie and removes administrator HTML without a second page request', async ({ page, context, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(extra(trace)); await page.goto('/cookie-session'); await ready(page); await login(page, adminToken, 'administrator');
  const active = await scopedCookie(context, origin + '/cookie-session', '/');
  const before = await records(request, trace); const response = await submit(page, page.locator('[data-session-logout]'), logoutPath);
  await session(page, 'anonymous'); await expect(credential(page)).toHaveValue('');
  expect((await context.cookies(origin)).some(cookie => cookie.name === active.name)).toBe(false);
  expect(response.headers()['cache-control']).toBe('private, no-store');
  const after = await records(request, trace); expect(posts(after)).toHaveLength(posts(before).length + 1); expect(after.filter(record => record.method === 'GET')).toHaveLength(before.filter(record => record.method === 'GET').length);
  expect(posts(after).at(-1).hasSetCookie).toBe(true); expect(decodeFrames(await response.text())[0].result.data).toEqual({ role: 'anonymous' });
});

test('bad credentials and role or bearer spoofing cannot create a signed cookie or protected page', async ({ page, context, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(extra(trace, { 'X-Role': 'administrator', Authorization: 'Bearer ' + adminToken }));
  await page.goto('/cookie-session?role=administrator&admin=true'); await ready(page); await session(page, 'anonymous');
  await credential(page).fill('incorrect-demo-credential-' + 'x'.repeat(32)); const response = await submit(page, page.locator('[data-session-login]'), loginPath, 401);
  expect((await response.json()).error.code).toBe('unauthorized');
  await session(page, 'anonymous'); expect((await context.cookies(origin)).some(cookie => cookie.name.startsWith('nextgo_demo_session_'))).toBe(false);
  const actual = await records(request, trace); expect(posts(actual)).toHaveLength(1); expect(posts(actual)[0].hasSetCookie).toBe(false);
});

test('a tampered signed cookie is anonymous and a valid reader login cannot reveal administrator data', async ({ page, context, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(extra(trace)); await page.goto('/cookie-session'); await ready(page); await login(page, adminToken, 'administrator');
  const original = await scopedCookie(context, origin + '/cookie-session', '/');
  const tampered = original.value.slice(0, -1) + (original.value.endsWith('A') ? 'B' : 'A');
  await context.addCookies([{ ...original, value: tampered }]); await page.reload(); await ready(page); await session(page, 'anonymous');
  await login(page, readerToken, 'reader'); await expect(page.locator('[data-session-admin-report]')).toHaveCount(0);
  const reader = await scopedCookie(context, origin + '/cookie-session', '/');
  const duplicate = await request.get(origin + '/cookie-session', { headers: { Cookie: `${reader.name}=${reader.value}; ${reader.name}=${reader.value}` } });
  expect(duplicate.status()).toBe(200); expect(await duplicate.text()).toContain('data-session-role="">anonymous</output>');
});

test('ordinary typed login sets a cookie while its server snapshot stays unchanged until explicit refresh', async ({ page, context, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(extra(trace)); await page.goto('/cookie-session'); await ready(page);
  const response = await login(page, readerToken, 'reader', '[data-session-login-only]');
  expect(response.headers()['content-type']).toContain('application/json'); expect((await response.json()).data).toEqual({ role: 'reader' });
  await expect(page.locator('[data-session-panel-role]')).toHaveText('reader'); await expect(page.locator('[data-session-status]')).toHaveText('Signed in without refreshing. Refresh to read the server-authorized page.');
  await expect(role(page)).toHaveText('anonymous'); await expect(page).toHaveTitle('Cookie session: anonymous — Next.go'); await expect(credential(page)).toHaveValue('');
  await scopedCookie(context, origin + '/cookie-session', '/');
  expect((await records(request, trace)).filter(record => record.method === 'GET')).toHaveLength(1);
  await page.locator('[data-session-refresh]').click(); await session(page, 'reader');
  const actual = await records(request, trace); expect(posts(actual)).toHaveLength(1); expect(actual.filter(record => record.method === 'GET')).toHaveLength(2);
});

test('locale scope owns its cookie path and rejects a root token replay even under the scoped cookie name', async ({ page, context, request }) => {
  const trace = randomUUID(), path = '/scoped/fr/cookie-session/'; await page.setExtraHTTPHeaders(extra(trace));
  await page.goto('/cookie-session'); await ready(page); await login(page, adminToken, 'administrator'); const rootCookie = await scopedCookie(context, origin + '/cookie-session', '/');
  await page.goto(path); await ready(page); await session(page, 'anonymous'); await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  const response = await login(page, readerToken, 'reader'); expect(new URL(response.url()).pathname).toBe('/scoped/fr' + loginPath);
  await expect(page).toHaveURL(origin + path); const scoped = await scopedCookie(context, origin + path, '/scoped/fr/'); expect(scoped.name === rootCookie.name).toBe(false);
  await context.addCookies([{ ...scoped, value: rootCookie.value }]); await page.reload(); await ready(page); await session(page, 'anonymous');
  await login(page, readerToken, 'reader');
  await page.goto('/cookie-session'); await ready(page); await session(page, 'administrator');
  await submit(page, page.locator('[data-session-logout]'), logoutPath); await session(page, 'anonymous');
  await page.goto(path); await ready(page); await session(page, 'reader');
});

test('a process started after login verifies the same signed cookie using only the shared key and host scope', async ({ page, context, request }) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(extra(trace)); await page.goto('/cookie-session'); await ready(page); await login(page, adminToken, 'administrator');
  const before = await scopedCookie(context, origin + '/cookie-session', '/');
  expect((await request.post(`${origin}/_fixture/cookie/start-cold`)).status()).toBe(200);
  await page.setExtraHTTPHeaders(extra(trace, { 'X-Fixture-Cookie-Replica': 'cold' })); await page.reload(); await ready(page); await session(page, 'administrator');
  const after = await scopedCookie(context, origin + '/cookie-session', '/'); expect(before.name === after.name && before.value === after.value).toBe(true);
  expect(posts(await records(request, trace))).toHaveLength(1);
});

test('typed FIFO keeps two optimistic intents while the second POST waits for the first physical response', async ({ page, request }) => {
  const trace = randomUUID(), firstKey = `${trace}-first`, secondKey = `${trace}-second`; await page.setExtraHTTPHeaders(extra(trace)); await page.goto('/optimistic'); await ready(page);
  const value = page.locator('[data-optimistic-value]'), pending = page.locator('[data-optimistic-pending]'), initial = await numeric(value);
  try {
    await page.setExtraHTTPHeaders(extra(trace, { 'X-Fixture-Cookie-Gate': firstKey }));
    const firstResponse = page.waitForResponse(response => new URL(response.url()).pathname.endsWith(optimisticPath) && response.request().method() === 'POST');
    void firstResponse.catch(() => {});
    await page.getByRole('button', { name: 'Add one', exact: true }).click();
    const first = await held(request, firstKey); assertWireProtocol(posts(await records(request, trace), optimisticPath).find(record => record.id === first.id), 200);
    await page.setExtraHTTPHeaders(extra(trace, { 'X-Fixture-Cookie-Gate': secondKey }));
    await page.getByRole('button', { name: 'Add five', exact: true }).click();
    await expect(pending).toHaveText('Saving 2 changes…'); await expect(value).toHaveText(String(initial + 6));
    expect(posts(await records(request, trace), optimisticPath)).toHaveLength(1);
    expect((await request.post(control('release', firstKey))).status()).toBe(200); assertResponseProtocol(await firstResponse); const second = await held(request, secondKey);
    await expect(pending).toHaveText('Saving 1 change…'); await expect(value).toHaveText(String(initial + 6));
    const actual = posts(await records(request, trace), optimisticPath); expect(actual).toHaveLength(2); expect(actual[1].id).toBe(second.id); expect(actual[1].outstandingBefore).toBe(0);
    expect((await request.post(control('release', secondKey))).status()).toBe(200);
    await expect(pending).toHaveText('No pending changes.'); await expect(value).toHaveText(String(initial + 3));
    await page.reload(); await ready(page); await expect(value).toHaveText(String(initial + 3)); expect(posts(await records(request, trace), optimisticPath)).toHaveLength(2);
  } finally { await request.post(control('release', firstKey)); await request.post(control('release', secondKey)); }
});

test('a lazy combined call and eager typed call share one FIFO and disposed queued work never posts', async ({ page, request }) => {
  const trace = randomUUID(), key = `${trace}-lazy`; await page.setExtraHTTPHeaders(extra(trace)); await page.goto('/action-view?delay=750ms'); await ready(page);
  const initial = await numeric(page.locator('[data-action-view-ssr-count]')), revision = await numeric(page.locator('[data-action-view-ssr-revision]'));
  await expect(page.locator('[data-av-lazy-save]')).toBeEnabled();
  try {
    await page.setExtraHTTPHeaders(extra(trace, { 'X-Fixture-Cookie-Gate': key, 'X-Fixture-Cookie-Phase': 'result' })); await submit(page, page.locator('[data-av-lazy-save]'), viewPath); await held(request, key, 'result');
    await page.setExtraHTTPHeaders(extra(trace)); await page.locator('[data-av-amount]').fill('2'); await page.locator('[data-av-only]').click();
    await expect(page.locator('[data-nextgo-island="actionview"]')).toHaveAttribute('aria-busy', 'true');
    expect(posts(await records(request, trace), viewPath)).toHaveLength(1);
    expect((await request.post(control('release', key))).status()).toBe(200);
    await expect(page.locator('[data-action-view-ssr-count]')).toHaveText(String(initial + 1)); await expect(page.locator('[data-action-view-ssr-revision]')).toHaveText(String(revision + 1));
    await expect(receipt(page)).toHaveAttribute('data-confirmed', 'true'); await expect(receipt(page)).toHaveAttribute('data-applied', 'true'); await expect(receipt(page)).toHaveAttribute('data-view-error', 'false');
    await expect(page.locator('[data-av-confirmed-count]')).toHaveText(String(initial + 1));
    await expect(page.locator('[data-action-view-deferred]')).toHaveText(String(initial + 1));
    expect(posts(await records(request, trace), viewPath)).toHaveLength(1);
  } finally { await request.post(control('release', key)); }
});

test('cancelling eight optimistic intents drops seven queued calls and reconciles only the one already committed POST', async ({ page, request }) => {
  const trace = randomUUID(), key = `${trace}-cancel`; await page.setExtraHTTPHeaders(extra(trace)); await page.goto('/optimistic'); await ready(page);
  const value = page.locator('[data-optimistic-value]'), pending = page.locator('[data-optimistic-pending]'), initial = await numeric(value);
  try {
    await page.setExtraHTTPHeaders(extra(trace, { 'X-Fixture-Cookie-Gate': key })); await page.getByRole('button', { name: 'Add one', exact: true }).click(); const first = await held(request, key);
    assertWireProtocol(posts(await records(request, trace), optimisticPath).find(record => record.id === first.id), 200);
    for (let index = 1; index < 8; index++) await page.getByRole('button', { name: 'Add one', exact: true }).click();
    await expect(pending).toHaveText('Saving 8 changes…'); await expect(value).toHaveText(String(initial + 8)); await expect(page.getByRole('button', { name: 'Add one', exact: true })).toBeDisabled();
    expect(posts(await records(request, trace), optimisticPath)).toHaveLength(1);
    await page.getByRole('button', { name: 'Cancel pending changes', exact: true }).click(); await expect(pending).toHaveText('No pending changes.'); await expect(value).toHaveText(String(initial));
    await expect.poll(async () => (await records(request, trace)).some(record => record.id === first.id && record.aborted)).toBe(true);
    await page.setExtraHTTPHeaders(extra(trace)); await page.getByRole('button', { name: 'Refresh server value', exact: true }).click();
    await expect(value).toHaveText(String(initial + 1)); await expect(pending).toHaveText('No pending changes.'); expect(posts(await records(request, trace), optimisticPath)).toHaveLength(1);
  } finally { await request.post(control('release', key)); }
});

test.describe('native signed-cookie forms without JavaScript', () => {
  test.use({ javaScriptEnabled: false });
  test('login and logout use real 303 redirects at root and scoped locale paths', async ({ page, context, request }) => {
    const trace = randomUUID(); await page.setExtraHTTPHeaders(extra(trace));
    for (const path of ['/cookie-session', '/scoped/fr/cookie-session/']) {
      await page.goto(path); await session(page, 'anonymous'); await credential(page).fill(adminToken);
      const login = await submit(page, page.locator('[data-session-login]'), loginPath, 303, { request, trace }); expect(login.headers().location).toBe(path);
      await expect(page).toHaveURL(origin + path); await session(page, 'administrator'); await expect(credential(page)).toHaveValue('');
      const cookie = await scopedCookie(context, origin + path, path.startsWith('/scoped') ? '/scoped/fr/' : '/');
      const logout = await submit(page, page.locator('[data-session-logout]'), logoutPath, 303, { request, trace }); expect(logout.headers().location).toBe(path);
      await expect(page).toHaveURL(origin + path); await session(page, 'anonymous'); expect((await context.cookies(origin + path)).some(value => value.name === cookie.name)).toBe(false);
    }
    expect(posts(await records(request, trace))).toHaveLength(4);
  });
});
