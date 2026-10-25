import { test, expect } from '@playwright/test';

const origin = 'http://127.0.0.1:4171';
const dev = 'http://127.0.0.1:4173';
const starts = new WeakMap();
const policies = new WeakSet();
const unique = name => `${name}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const barrier = page => page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(resolve, 0)))));
async function records(request, page) { return (await (await request.get(origin + '/_fixture/requests')).json()).filter(record => record.id > (starts.get(page) || 0)); }
async function matching(request, page, predicate) { return (await records(request, page)).filter(predicate); }
async function found(request, page, predicate, timeout = 12_000) { let result; await expect.poll(async () => { result = (await records(request, page)).find(predicate); return !!result; }, { timeout }).toBe(true); return result; }
async function release(request, key, id) { expect((await request.post(`${origin}/_fixture/release?key=${encodeURIComponent(key)}${id ? `&id=${id}` : ''}`)).status()).toBe(200); }
async function open(page, request) {
  // Isolated Linux WebKit reports offline without an external network device,
  // although the real loopback App is reachable. Establish the connected policy
  // precondition here; explicit offline/data/visibility cases test each gate.
  if (!policies.has(page)) await policy(page);
  await page.goto('/prefetch'); await ready(page); await found(request, page, r => r.path === '/posts?vp=visible' && r.purpose === 'prefetch' && r.delivered);
}
async function candidate(page, href, options = {}) {
  return page.evaluateHandle(({ href, options }) => {
    const a = document.createElement('a'); a.href = href; a.textContent = options.label || 'Dynamic prefetch target';
    if (options.mode !== 'legacy') a.setAttribute('data-nextgo-prefetch', options.mode || 'viewport');
    a.style.cssText = `position:fixed;left:10px;top:${options.top || 160}px;z-index:1000;display:block;width:200px;height:24px;background:white`;
    (document.querySelector('[data-prefetch-demo]') || document.querySelector('main')).append(a); return a;
  }, { href, options });
}
async function policy(page, initial = {}) {
  policies.add(page);
  await page.addInitScript(initial => {
    const state = { hidden: false, online: true, saveData: false, effectiveType: '4g', ...initial };
    const connection = new EventTarget();
    Object.defineProperties(connection, { saveData: { get: () => state.saveData }, effectiveType: { get: () => state.effectiveType } });
    Object.defineProperty(navigator, 'connection', { configurable: true, get: () => connection });
    Object.defineProperty(navigator, 'onLine', { configurable: true, get: () => state.online });
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state.hidden ? 'hidden' : 'visible' });
    window.setViewportPolicy = values => { Object.assign(state, values); document.dispatchEvent(new Event('visibilitychange')); window.dispatchEvent(new Event(state.online ? 'online' : 'offline')); connection.dispatchEvent(new Event('change')); };
    window.viewportConstructors = { intersection: 0, mutation: 0 };
    window.viewportConstructorStacks = [];
    for (const [name, key] of [['IntersectionObserver', 'intersection'], ['MutationObserver', 'mutation']]) {
      const Native = window[name]; if (!Native) continue;
      const Counted = function(...args) {
        const stack = String(new Error().stack);
        // Playwright also creates a listener-removal observer after pagehide.
        // Count application Go observers, whose calls cross the WASM bridge.
        if (stack.includes('wasm_exec.js')) window.viewportConstructors[key]++;
        if (window.viewportConstructorStacks.length < 64) window.viewportConstructorStacks.push({ name, stack });
        return new Native(...args);
      };
      Counted.prototype = Native.prototype; Object.setPrototypeOf(Counted, Native); window[name] = Counted;
    }
  }, initial);
}
test.beforeEach(async ({ request, page }) => { starts.set(page, (await (await request.get(origin + '/_fixture/requests')).json()).at(-1)?.id || 0); });

test('typed links preserve useful SSR and visible, offscreen, intent and excluded policies', async ({ page, request }) => {
  await open(page, request); await expect(page).toHaveTitle('Viewport prefetch — Next.go');
  await expect(page.locator('#prefetch-visible')).toHaveAttribute('data-nextgo-prefetch', 'viewport'); await expect(page.locator('#prefetch-intent')).toHaveAttribute('data-nextgo-prefetch', 'intent'); await expect(page.locator('#prefetch-none')).toHaveAttribute('data-nextgo-prefetch', 'false'); await expect(page.locator('#prefetch-legacy')).not.toHaveAttribute('data-nextgo-prefetch');
  await page.locator('details').evaluate(element => { element.open = true; }); await barrier(page);
  for (const vp of ['offscreen', 'intent', 'legacy', 'none', 'blank', 'reload', 'replace']) expect((await matching(request, page, r => r.path === `/posts?vp=${vp}` && r.purpose === 'prefetch')).length).toBe(0);
  expect((await matching(request, page, r => r.path.startsWith('/api/health') && r.purpose === 'prefetch')).length).toBe(0);
  for (const id of ['prefetch-none', 'prefetch-api', 'prefetch-download', 'prefetch-blank', 'prefetch-reload', 'prefetch-external', 'prefetch-fragment']) await page.locator(`#${id}`).dispatchEvent('pointerover');
  for (const id of ['prefetch-intent', 'prefetch-legacy']) await page.locator(`#${id}`).dispatchEvent('focusin');
  await found(request, page, r => r.path === '/posts?vp=intent' && r.purpose === 'prefetch' && r.delivered); await found(request, page, r => r.path === '/posts?vp=legacy' && r.purpose === 'prefetch' && r.delivered);
  await page.locator('#prefetch-offscreen').scrollIntoViewIfNeeded(); await found(request, page, r => r.path === '/posts?vp=offscreen' && r.purpose === 'prefetch' && r.delivered);
  expect((await matching(request, page, r => ['/posts?vp=none', '/api/health', '/assets/style.css', '/posts?vp=blank', '/posts?vp=reload'].includes(r.path) && r.purpose === 'prefetch')).length).toBe(0);
});

test('DOM mutations retry changed targets once and navigation drops detached ownership', async ({ page, request }) => {
  await open(page, request); const first = `/about?vp=${unique('no-store')}`; const node = await candidate(page, first);
  await found(request, page, r => r.path === first && r.purpose === 'prefetch' && r.delivered);
  await node.evaluate(a => { a.style.top = '5000px'; a.append(document.createElement('span')); }); await barrier(page); await node.evaluate(a => { a.style.top = '160px'; }); await barrier(page);
  expect((await matching(request, page, r => r.path === first && r.purpose === 'prefetch')).length).toBe(1);
  const next = `/about?vp=${unique('changed')}`; await node.evaluate((a, path) => { a.href = path; }, next); await found(request, page, r => r.path === next && r.purpose === 'prefetch' && r.delivered);
  await page.evaluate(() => { window.viewportDocument = true; }); await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Architecture', exact: true }).click(); await expect(page).toHaveTitle('Architecture — Next.go');
  const detached = `/about?vp=${unique('detached')}`; await node.evaluate((a, path) => { a.href = path; }, detached); await page.evaluate(() => window.dispatchEvent(new Event('resize'))); await barrier(page);
  expect(await node.evaluate(a => a.isConnected)).toBe(false); expect((await matching(request, page, r => r.path === detached)).length).toBe(0);
  const live = `/posts?vp=${unique('after-nav')}`; await candidate(page, live); await found(request, page, r => r.path === live && r.purpose === 'prefetch' && r.delivered); expect(await page.evaluate(() => window.viewportDocument)).toBe(true);
});

test('more than32 visible targets drain through at most2 workers and removed queued links never dispatch', async ({ page, request }) => {
  await open(page, request); const key = unique('batch'); const prefix = `/posts?vp=${key}-`;
  const targets = await page.evaluateHandle(({ prefix, key }) => { const host = document.querySelector('[data-prefetch-demo]'); const nodes = []; for (let i = 0; i < 40; i++) { const a = document.createElement('a'); a.href = `${prefix}${i}&gate=${key}`; a.textContent = `batch ${i}`; a.dataset.nextgoPrefetch = 'viewport'; a.style.cssText = `position:fixed;left:10px;top:${120 + i % 5 * 25}px;display:block;width:200px;height:24px;z-index:1000`; host.append(a); nodes.push(a); } return nodes; }, { prefix, key });
  let active; await expect.poll(async () => { active = await matching(request, page, r => r.path.startsWith(prefix) && r.status === 200); return active.length; }).toBe(2); await barrier(page); expect((await matching(request, page, r => r.path.startsWith(prefix))).length).toBe(2);
  const removed = await targets.evaluate((nodes, active) => { const node = nodes.find(a => !active.includes(new URL(a.href).pathname + new URL(a.href).search)); const path = new URL(node.href).pathname + new URL(node.href).search; node.remove(); return path; }, active.map(record => record.path));
  await page.locator(`a[href="${active[0].path}"]`).dispatchEvent('pointerover'); await barrier(page); expect((await matching(request, page, r => r.path === active[0].path)).length).toBe(1);
  await release(request, key, active[0].id); await expect.poll(async () => (await matching(request, page, r => r.path.startsWith(prefix))).length).toBe(3); await barrier(page); expect((await matching(request, page, r => r.path.startsWith(prefix) && !r.delivered && !r.aborted)).length).toBeLessThanOrEqual(2);
  await release(request, key); await expect.poll(async () => (await matching(request, page, r => r.path.startsWith(prefix) && r.delivered)).length, { timeout: 20_000 }).toBe(39); expect((await matching(request, page, r => r.path === removed)).length).toBe(0);
});

test('foreground navigation cancels viewport-origin work even after intent joins and does not await its gate', async ({ page, request }) => {
  await open(page, request); const key = unique('priority'); const path = `/posts?vp=${key}&gate=${key}`; const a = await candidate(page, path);
  await found(request, page, r => r.path === path && r.purpose === 'prefetch' && r.complete); await a.evaluate(a => { a.dispatchEvent(new Event('pointerover', { bubbles: true })); a.dispatchEvent(new Event('focusin', { bubbles: true })); });
  await page.evaluate(() => { window.viewportPriorityDocument = true; }); await a.click();
  await found(request, page, r => r.path === path && r.navigate === '1' && r.purpose !== 'prefetch', 3000); await expect(page).toHaveTitle('Field notes — Next.go', { timeout: 5000 });
  await found(request, page, r => r.path === path && r.purpose === 'prefetch' && r.aborted, 3000); await release(request, key); expect(await page.evaluate(() => window.viewportPriorityDocument)).toBe(true); await expect(page).toHaveTitle('Field notes — Next.go');
});

test('hidden and page lifecycle gates cancel only viewport work and reuse observer constructors on resume', async ({ page, request }) => {
  await policy(page, { hidden: true }); await page.goto('/prefetch'); await ready(page); await barrier(page); expect((await matching(request, page, r => r.purpose === 'prefetch')).length).toBe(0);
  await page.getByRole('button', { name: 'Increase Prefetch companion', exact: true }).click(); await page.evaluate(() => window.setViewportPolicy({ hidden: false })); await found(request, page, r => r.path === '/posts?vp=visible' && r.delivered); const baseline = await page.evaluate(() => ({ ...window.viewportConstructors })); expect(baseline).toEqual({ intersection: 1, mutation: 2 });
  const key = unique('freeze'); const path = `/posts?vp=${key}&gate=${key}`; await candidate(page, path); await found(request, page, r => r.path === path && r.complete);
  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true }))); await found(request, page, r => r.path === path && r.aborted, 3000); await release(request, key);
  const next = `/posts?vp=${unique('resume')}`; await candidate(page, next); await barrier(page); expect((await matching(request, page, r => r.path === next)).length).toBe(0);
  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }))); await found(request, page, r => r.path === next && r.delivered); await found(request, page, r => r.path === path && r.delivered);
  for (let i = 0; i < 4; i++) await page.evaluate(() => { window.setViewportPolicy({ hidden: true }); window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true })); window.setViewportPolicy({ hidden: false }); window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true })); });
  await barrier(page); expect(await page.evaluate(() => window.viewportConstructors), JSON.stringify(await page.evaluate(() => window.viewportConstructorStacks))).toEqual(baseline);
  const intentKey = unique('intent-owner'); const intentPath = `/posts?vp=${intentKey}&gate=${intentKey}`; const intent = await candidate(page, intentPath, { mode: 'legacy' }); await intent.evaluate(a => a.dispatchEvent(new Event('pointerover', { bubbles: true }))); await found(request, page, r => r.path === intentPath && r.complete);
  await candidate(page, intentPath); await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pagehide', { persisted: true }))); await release(request, intentKey); await found(request, page, r => r.path === intentPath && r.delivered && !r.aborted);
  await page.evaluate(() => window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }))); await barrier(page); expect((await matching(request, page, r => r.path === intentPath && r.purpose === 'prefetch')).length).toBe(1);
});

for (const [name, state, clear] of [['offline', { online: false }, { online: true }], ['save-data', { saveData: true }, { saveData: false }], ['2g', { effectiveType: '2g' }, { effectiveType: '4g' }]]) test(`${name} pauses viewport speculation until its real capability event resumes it`, async ({ page, request }) => {
  await policy(page, state); await page.goto('/prefetch'); await ready(page); await barrier(page); expect((await matching(request, page, r => r.purpose === 'prefetch')).length).toBe(0);
  await page.evaluate(values => window.setViewportPolicy(values), clear); await found(request, page, r => r.path === '/posts?vp=visible' && r.purpose === 'prefetch' && r.delivered);
});

test('a tail after128 offscreen links and8192 nodes still prefetches a public shell and resumes fresh private data', async ({ page, request, context }) => {
  await context.addCookies([{ name: 'nextgo_partial_name', value: 'Alice', url: origin }]); await open(page, request); const key = unique('tail'); const path = `/partial/alpha?message=Viewport%20private&vp=${key}`;
  const a = await page.evaluateHandle(path => {
    const host = document.querySelector('[data-prefetch-demo]'); const fragment = document.createDocumentFragment();
    for (let i = 0; i < 9000; i++) { const span = document.createElement('span'); span.hidden = true; fragment.append(span); }
    for (let i = 0; i < 140; i++) { const link = document.createElement('a'); link.href = `/posts?vp=offscreen-leader-${i}`; link.dataset.nextgoPrefetch = 'viewport'; link.style.cssText = 'position:fixed;top:5000px;left:0'; link.textContent = 'Offscreen leader'; fragment.append(link); }
    const tail = document.querySelector('#prefetch-shell').cloneNode(true); tail.removeAttribute('id'); tail.href = path; tail.style.cssText = 'position:fixed;top:160px;left:10px;z-index:1000;display:block;width:200px;height:24px'; fragment.append(tail); host.append(fragment); return tail;
  }, path);
  const cached = await found(request, page, r => r.path === path && r.purpose === 'prefetch' && r.publicShell); expect(cached.publicShell.shell).toMatch(/^[a-f0-9]{64}$/); expect(cached.publicShell.html).not.toContain('data-partial-request-id'); expect(cached.publicShell.html).not.toContain('Alice'); expect((await matching(request, page, r => r.path.startsWith('/posts?vp=offscreen-leader'))).length).toBe(0);
  await barrier(page); await context.addCookies([{ name: 'nextgo_partial_name', value: 'Bob', url: origin }]); await a.click(); const active = await found(request, page, r => r.path === path && r.navigate === '1' && r.purpose !== 'prefetch' && r.shellFrame);
  expect(active.shellHint).toBe(cached.publicShell.shell); expect(active.shellFrame.page.shellRef).toBe(true); expect(active.shellFrame.page.html).toBe(''); await expect(page.locator('[data-partial-visitor]')).toHaveText('Bob'); await expect(page.locator('[data-partial-message]')).toHaveText('Viewport private'); await ready(page);
});

test('development disables automatic viewport work while explicit intent remains available', async ({ page }) => {
  await policy(page);
  const requests = []; page.on('request', request => { if (request.headers()['x-nextgo-purpose'] === 'prefetch') requests.push(request.url()); });
  await page.goto(dev + '/prefetch'); await ready(page); await expect(page.locator('body')).toHaveAttribute('data-nextgo-dev', 'true'); await barrier(page); expect(requests).toEqual([]);
  await page.locator('#prefetch-intent').dispatchEvent('pointerover'); await expect.poll(() => requests.some(url => url === dev + '/posts?vp=intent')).toBe(true); expect(requests.some(url => url.endsWith('/posts?vp=visible'))).toBe(false);
});
