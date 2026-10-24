import { test, expect } from '@playwright/test';
import { spawn } from 'node:child_process';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const notes = page => page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Field notes', exact: true });

test('static export preserves optional native transition policy and same-document JSON navigation', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('/mounted-static/'); await ready(page);
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-transitions', 'true');
  await page.evaluate(() => {
    window.staticTransitionDocument = true;
    window.staticTransitionCalls = 0;
    window.staticTransitionSupported = typeof document.startViewTransition === 'function';
    if (!window.staticTransitionSupported) return;
    const native = document.startViewTransition.bind(document);
    document.startViewTransition = update => { window.staticTransitionCalls++; return native(update); };
  });
  await notes(page).click();
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(await page.evaluate(() => window.staticTransitionDocument)).toBe(true);
  if (await page.evaluate(() => window.staticTransitionSupported)) expect(await page.evaluate(() => window.staticTransitionCalls)).toBe(1);
  await page.goBack();
  await expect(page).toHaveTitle('Next.go — Go, all the way through');
});

test('static output serves meaningful direct-route HTML, runtime assets, and no API', async ({ browser, request }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto('http://127.0.0.1:4102/posts/html-first');
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Start with the HTML');
  await expect(page.locator('[data-posts-layout]')).toBeVisible();
  await expect(page.locator('[data-count]')).toHaveText('0');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-export', 'true');
  const build = await page.locator('body').getAttribute('data-nextgo-build');
  const wasm = await request.head(`/_nextgo/${build}/app.wasm`);
  expect(wasm.status()).toBe(200);
  expect(wasm.headers()['content-type']).toBe('application/wasm');
  expect((await request.get('/assets/style.css')).headers()['content-type']).toBe('text/css');
  expect((await request.get('/api/health')).status()).toBe(404);
  expect((await request.get('/server')).status()).toBe(404);
  const envelope = await (await request.get('/_nextgo/data/posts/html-first.json')).json();
  expect(envelope).toMatchObject({ status: 200, url: '/posts/html-first', build, document: { lang: 'en', dir: 'ltr', viewport: 'width=device-width,initial-scale=1' } });
  await expect(page.locator('meta[name="viewport"]')).toHaveCount(1);
  await context.close();
});

test('Go hydrates static SSR without replacing initial nodes', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  await page.goto('/', { waitUntil: 'domcontentloaded' });
  await expect(page.locator('[data-count]')).toHaveText(['3', '10']);
  await page.evaluate(() => {
    window.initialCounter = document.querySelector('[data-nextgo-island="counter"]');
    window.initialButton = document.querySelector('[data-increment]');
    window.initialText = document.querySelector('[data-count]').firstChild;
  });
  release();
  await ready(page);
  expect(await page.evaluate(() => window.initialCounter === document.querySelector('[data-nextgo-island="counter"]') && window.initialButton === document.querySelector('[data-increment]') && window.initialText === document.querySelector('[data-count]').firstChild)).toBe(true);
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
});

test('static JSON supports Go navigation, history, query strings, groups, and catchalls', async ({ page }) => {
  const errors = [];
  const documents = [];
  const dataRequests = [];
  page.on('pageerror', error => errors.push(error.message));
  page.on('request', request => {
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) documents.push(request.url());
    if (request.url().includes('/_nextgo/data/')) dataRequests.push(new URL(request.url()).pathname);
  });
  await page.goto('/');
  await ready(page);
  await page.evaluate(() => { window.sameStaticDocument = 'retained'; });
  await notes(page).click();
  await expect(page).toHaveTitle('Field notes — Next.go');
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveURL(/\/posts\/html-first$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Start with the HTML');
  await page.getByRole('button', { name: 'Increase Helpful votes', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText('1');
  await page.goBack();
  await expect(page).toHaveURL(/\/posts$/);
  await page.getByRole('link', { name: 'Try a slow navigation' }).click();
  await expect(page).toHaveURL(/\/posts\?delay=750ms$/);
  await expect(page).toHaveTitle('Field notes — Next.go');
  await page.goBack();
  await page.goBack();
  await expect(page).toHaveURL('http://127.0.0.1:4102/');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Architecture', exact: true }).click();
  await expect(page).toHaveTitle('Architecture — Next.go');
  for (const [path, heading] of [['/welcome', 'A route group without a URL segment'], ['/docs/guide', 'Catch-all documentation route']]) {
    await page.evaluate(path => {
      document.getElementById('static-example-link')?.remove();
      const link = document.createElement('a');
      link.id = 'static-example-link'; link.href = path; link.textContent = 'Open exported example';
      document.querySelector('main').append(link);
    }, path);
    await page.locator('#static-example-link').click();
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(heading);
  }
  await expect(page.locator('[data-catchall-path]')).toHaveText('guide');
  expect(await page.evaluate(() => window.sameStaticDocument)).toBe('retained');
  expect(documents).toEqual(['http://127.0.0.1:4102/']);
  expect(dataRequests).toContain('/_nextgo/data/posts.json');
  expect(dataRequests).toContain('/_nextgo/data/posts/html-first.json');
  expect(dataRequests).toContain('/_nextgo/data/docs/guide.json');
  expect(errors).toEqual([]);
});

test('mounted static output hydrates and navigates using separate asset and page-data prefixes', async ({ page, request }) => {
  await page.goto('/mounted-static/');
  await expect(page.getByRole('heading', { level: 1 })).toContainText('Go, all the way');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  await expect(page.locator('script[src$="/boot.js"]')).toHaveAttribute('src', /^\/mounted-cdn\/_nextgo\/.+\/boot.js$/);
  await page.evaluate(() => { window.staticMountDocument = true; });
  await page.getByRole('button', { name: 'Increase First counter', exact: true }).click();
  await expect(page.locator('[data-count]')).toHaveText(['4', '10']);
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Field notes', exact: true }).click();
  await expect(page).toHaveURL(/\/mounted-static\/posts\/$/);
  await expect(page).toHaveTitle('Field notes — Next.go');
  await page.getByRole('link', { name: /Start with the HTML/ }).click();
  await expect(page).toHaveURL(/\/mounted-static\/posts\/html-first\/$/);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Start with the HTML');
  await page.goBack();
  await expect(page).toHaveTitle('Field notes — Next.go');
  expect(await page.evaluate(() => window.staticMountDocument)).toBe(true);
  expect((await request.get('/mounted-static/_nextgo/data/posts/html-first.json')).status()).toBe(200);
  expect((await request.get('/mounted-static/api/health')).status()).toBe(404);
});

test('static navigation rejects a mixed deployment generation and reloads its document', async ({ page }) => {
  await page.goto('/'); await ready(page);
  await page.evaluate(() => { window.staticGenerationDocument = true; });
  await page.route('**/_nextgo/data/posts.json', async route => {
    const response = await route.fetch();
    const json = await response.json();
    json.configuration = 'a'.repeat(64);
    json.html = '<h1 data-mixed-export>Mixed export</h1>';
    await route.fulfill({ response, json });
  });
  await notes(page).click();
  await expect(page).toHaveTitle('Field notes — Next.go'); await ready(page);
  expect(await page.evaluate(() => window.staticGenerationDocument)).toBeUndefined();
  await expect(page.locator('[data-mixed-export]')).toHaveCount(0);
});


test('managed locale export fails clearly and preserves a previous publication', async () => {
  const output = join(process.env.NEXTGO_STATIC_E2E_ROOT, 'rejected-locales');
  await mkdir(output, { recursive: true });
  await writeFile(join(output, 'index.html'), 'previous publication');
  await writeFile(join(output, '.nextgo-export.json'), await readFile(join(process.env.NEXTGO_STATIC_E2E_ROOT, 'static', '.nextgo-export.json')));
  const child = spawn('go', ['run', './cmd/nextgo', 'export', '-out', output, '-paths', '/', './examples/demo'], {
    env: { ...process.env, NEXTGO_DEMO_LOCALES: 'true' }, stdio: ['ignore', 'pipe', 'pipe'],
  });
  let diagnostic = '';
  child.stdout.on('data', chunk => { diagnostic += String(chunk); });
  child.stderr.on('data', chunk => { diagnostic += String(chunk); });
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', resolve); });
  expect(code).not.toBe(0);
  expect(diagnostic).toContain('managed locale static export is not implemented');
  expect(await readFile(join(output, 'index.html'), 'utf8')).toBe('previous publication');
});

test('request-time rules reject static publication without replacing existing output', async () => {
  const output = join(process.env.NEXTGO_STATIC_E2E_ROOT, 'rejected-rules');
  await mkdir(output, { recursive: true });
  await writeFile(join(output, 'index.html'), 'previous rules publication');
  await writeFile(join(output, '.nextgo-export.json'), await readFile(join(process.env.NEXTGO_STATIC_E2E_ROOT, 'static', '.nextgo-export.json')));
  const child = spawn('go', ['run', './cmd/nextgo', 'export', '-out', output, '-paths', '/', './examples/demo'], {
    env: { ...process.env, NEXTGO_DEMO_LOCALES: 'false', NEXTGO_DEMO_RULES: 'true' }, stdio: ['ignore', 'pipe', 'pipe'],
  });
  let diagnostic = '';
  child.stdout.on('data', chunk => { diagnostic += String(chunk); });
  child.stderr.on('data', chunk => { diagnostic += String(chunk); });
  const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', resolve); });
  expect(code).not.toBe(0);
  expect(diagnostic).toContain('conditional/external request rules require an HTTP server');
  expect(await readFile(join(output, 'index.html'), 'utf8')).toBe('previous rules publication');
});

test('parallel slots and optimistic server actions reject static publication while unrelated pages remain exportable', async () => {
  const output = join(process.env.NEXTGO_STATIC_E2E_ROOT, 'rejected-server-dependent');
  await mkdir(output, { recursive: true });
  await writeFile(join(output, 'index.html'), 'previous server-dependent publication');
  await writeFile(join(output, '.nextgo-export.json'), await readFile(join(process.env.NEXTGO_STATIC_E2E_ROOT, 'static', '.nextgo-export.json')));
  for (const path of ['/boards', '/optimistic']) {
    const child = spawn('go', ['run', './cmd/nextgo', 'export', '-out', output, '-paths', path, './examples/demo'], {
      env: { ...process.env, NEXTGO_DEMO_LOCALES: 'false', NEXTGO_DEMO_RULES: 'false' }, stdio: ['ignore', 'pipe', 'pipe'],
    });
    let diagnostic = '';
    child.stdout.on('data', chunk => { diagnostic += String(chunk); });
    child.stderr.on('data', chunk => { diagnostic += String(chunk); });
    const code = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', resolve); });
    expect(code).not.toBe(0);
    expect(diagnostic).toContain('server actions');
    expect(await readFile(join(output, 'index.html'), 'utf8')).toBe('previous server-dependent publication');
  }
});

for (const defaultPrefixed of [true, false]) {
  test(`localized static HTML hydrates and navigates with separate language data (${defaultPrefixed ? 'prefixed' : 'bare'} default)`, async ({ page, request, browser }) => {
    const base = defaultPrefixed ? '/locale-static' : '/default-locale-static';
    const suffix = defaultPrefixed ? '' : '/';
    const publicURL = (language, path) => `${base}${language === 'en' && !defaultPrefixed ? '' : '/' + language}${path}${suffix}`;
    const french = publicURL('fr', '/translations');
    const arabic = publicURL('ar', '/translations');
    const context = await browser.newContext({ javaScriptEnabled: false });
    const plain = await context.newPage();
    await plain.goto(arabic);
    await expect(plain.locator('html')).toHaveAttribute('lang', 'ar');
    await expect(plain.locator('html')).toHaveAttribute('dir', 'rtl');
    await expect(plain.locator('[data-translated-heading]')).toHaveText('مرحبا');
    await expect(plain.getByRole('button', { name: 'Increase Go counter', exact: true })).toBeDisabled();
    await context.close();
    const documents = [], dataRequests = [];
    page.on('request', r => {
      if (r.isNavigationRequest() && r.frame() === page.mainFrame()) documents.push(r.url());
      if (r.url().includes('/_nextgo/data/')) dataRequests.push(new URL(r.url()).pathname);
    });
    let release;
    const gate = new Promise(resolve => { release = resolve; });
    await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
    await page.goto(french, { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => { window.translatedText = document.querySelector('[data-translated-heading]').firstChild; });
    release(); await ready(page);
    expect(await page.evaluate(() => window.translatedText === document.querySelector('[data-translated-heading]').firstChild)).toBe(true);
    await page.getByRole('button', { name: 'Increase Go counter', exact: true }).click();
    await expect(page.locator('[data-count]').last()).toHaveText('1');
    await page.evaluate(() => { window.languageDocument = 'fr'; });
    // Static query strings retain browser history but cannot rerun a Go loader.
    await notes(page).evaluate((link, href) => { link.href = href; }, publicURL('fr', '/posts/html-first'));
    await notes(page).click();
    await expect(page).toHaveURL('http://127.0.0.1:4102' + publicURL('fr', '/posts/html-first'));
    await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Start with the HTML');
    expect(await page.evaluate(() => window.languageDocument)).toBe('fr');
    expect(documents).toHaveLength(1);
    expect(dataRequests).toContain(base + '/fr/_nextgo/data/posts/html-first.json');
    await page.goBack();
    await expect(page.locator('[data-translated-heading]')).toHaveText('Bonjour');
    await page.locator('[data-language="ar"]').click(); await ready(page);
    await expect(page).toHaveURL('http://127.0.0.1:4102' + arabic);
    await expect(page.locator('html')).toHaveAttribute('dir', 'rtl');
    expect(await page.evaluate(() => window.languageDocument)).toBeUndefined();
    await page.locator('[data-language="en"]').click(); await ready(page);
    await expect(page).toHaveURL('http://127.0.0.1:4102' + publicURL('en', '/translations'));
    await expect(page.locator('html')).toHaveAttribute('lang', 'en');
    const englishPrefix = base + (defaultPrefixed ? '/en' : '');
    const envelope = await (await request.get(englishPrefix + '/_nextgo/data/translations.json')).json();
    expect(envelope.document.lang).toBe('en');
    expect((await request.get(base + '/fr/api/locale')).status()).toBe(404);
    expect(documents).toHaveLength(3);
  });
}

test('domain-targeted static artifacts hydrate, navigate and switch origins with isolated data', async ({ page, request, browser }) => {
  const en = 'http://127.0.0.1:4124/domain-static';
  const fr = 'http://127.0.0.1:4125/domain-static';
  const context = await browser.newContext({ javaScriptEnabled: false });
  const plain = await context.newPage();
  await plain.goto(fr + '/translations/');
  await expect(plain.locator('html')).toHaveAttribute('lang', 'fr');
  await expect(plain.locator('[data-translated-heading]')).toHaveText('Bonjour');
  await context.close();
  expect((await request.get(fr + '/translations/', { headers: { Host: 'unconfigured.example' } })).status()).toBe(421);
  const englishData = await (await request.get(en + '/_nextgo/data/translations.json')).json();
  const frenchData = await (await request.get(fr + '/_nextgo/data/translations.json')).json();
  const frenchOnEnglishHost = await (await request.get(en + '/fr/_nextgo/data/translations.json')).json();
  expect(englishData.document.lang).toBe('en'); expect(frenchData.document.lang).toBe('fr');
  expect(frenchOnEnglishHost.document.lang).toBe('fr');
  expect(frenchOnEnglishHost.configuration).not.toBe(frenchData.configuration);
  expect(englishData.configuration).not.toBe(frenchData.configuration);
  const documents = [], dataRequests = [];
  page.on('request', r => {
    if (r.isNavigationRequest() && r.frame() === page.mainFrame()) documents.push(r.url());
    if (r.url().includes('/_nextgo/data/')) dataRequests.push(r.url());
  });
  await page.goto(en + '/translations/'); await ready(page);
  await page.evaluate(() => { window.domainStaticDocument = 'en'; });
  await page.getByRole('button', { name: 'Increase Go counter', exact: true }).click();
  await expect(page.locator('[data-count]').last()).toHaveText('1');
  await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Architecture', exact: true }).click();
  await expect(page).toHaveURL(en + '/about/');
  expect(await page.evaluate(() => window.domainStaticDocument)).toBe('en');
  expect(documents).toHaveLength(1);
  expect(dataRequests).toContain(en + '/_nextgo/data/about.json');
  await page.goBack(); await expect(page.locator('[data-translated-heading]')).toHaveText('Hello');
  await expect(page.locator('[data-language="fr"]')).toHaveAttribute('href', fr + '/translations/');
  await page.locator('[data-language="fr"]').click(); await ready(page);
  await expect(page).toHaveURL(fr + '/translations/');
  await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
  await expect(page.locator('[data-translated-heading]')).toHaveText('Bonjour');
  expect(await page.evaluate(() => window.domainStaticDocument)).toBeUndefined();
  expect(documents).toHaveLength(2);
  expect((await request.get(fr + '/api/locale')).status()).toBe(404);
});


test('static documents carry runtime public configuration for Go components',async({page})=>{
 await page.goto('/environment');await ready(page);
 await page.locator('[data-setting-read]').click();await expect(page.locator('[data-setting-result]')).toHaveText('Configured at server startup');
 await page.evaluate(()=>{window.settingsStaticDocument=true});await page.getByRole('link',{name:'Architecture',exact:true}).last().click();await expect(page).toHaveTitle('Architecture — Next.go');expect(await page.evaluate(()=>window.settingsStaticDocument)).toBe(true);
 await page.goBack();await expect(page).toHaveTitle('Runtime configuration — Next.go');await page.locator('[data-setting-read]').click();await expect(page.locator('[data-setting-result]')).toHaveText('Configured at server startup');
});


test('static export retains lazy modules and uses Go navigation between exported islands',async({page})=>{
 let downloads=0;page.on('request',request=>{if(/module-[a-f0-9]{64}\.wasm$/.test(request.url()))downloads++});
 await page.goto('/lazy');await ready(page);await page.evaluate(()=>window.staticLazyDocument=true);const first=page.getByRole('region',{name:'First lazy counter',exact:true});await first.locator('[data-lazy-increment]').click();await expect(first.locator('[data-lazy-count]')).toHaveText('6');await first.locator('[data-lazy-navigate]').click();await expect(page).toHaveTitle('Lazy navigation — Next.go');await ready(page);expect(await page.evaluate(()=>window.staticLazyDocument)).toBe(true);expect(downloads).toBe(1);await page.goBack();await expect(page).toHaveTitle('Lazy Go modules — Next.go');await ready(page);expect(downloads).toBe(1);
});

test('static publication retains inert script declarations and Go-owned readiness across navigation',async({page})=>{
 await page.goto('/scripts');await ready(page);await expect(page.getByRole('region',{name:'First script widget',exact:true}).locator('[data-script-result]')).toHaveText('Ready: demo-library-v1');await page.evaluate(()=>window.staticScriptDocument=true);await page.getByRole('link',{name:'Script details',exact:true}).click();await expect(page).toHaveTitle('Script details — Next.go');await expect(page.getByRole('region',{name:'Detail script widget',exact:true}).locator('[data-script-result]')).toHaveText('Ready: demo-library-v1');expect(await page.evaluate(()=>window.staticScriptDocument)).toBe(true);expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(1);
});

test('nonce-based CSP refuses static publication and preserves the previous artifact',async()=>{
 const directory=join(process.env.NEXTGO_STATIC_E2E_ROOT,'static');const original=await readFile(join(directory,'index.html'));const cli=join(process.env.NEXTGO_STATIC_E2E_ROOT,'nextgo');const child=spawn(cli,['export','-out',directory,'-paths','/scripts','./examples/demo'],{env:{...process.env,NEXTGO_DEMO_CSP:'true'}});let output='';child.stdout.on('data',data=>output+=data);child.stderr.on('data',data=>output+=data);const code=await new Promise(resolve=>child.on('exit',resolve));expect(code).not.toBe(0);expect(output).toContain('per-request CSP nonces require dynamic rendering');expect(await readFile(join(directory,'index.html'))).toEqual(original);
});

test('composed parameters export useful product HTML and hydrate it in place',async({page,request})=>{
 const response=await request.get('http://127.0.0.1:4102/market/coffee/espresso');expect(response.status()).toBe(200);expect(await response.text()).toContain('coffee / espresso');await page.goto('http://127.0.0.1:4102/market/coffee/espresso');await ready(page);await page.evaluate(()=>{window.staticComposedDocument=true;window.staticComposedCounter=document.querySelector('[data-count]')});await page.getByRole('button',{name:'Increase Catalog counter',exact:true}).click();await expect(page.locator('[data-count]')).toHaveText('8');expect(await page.evaluate(()=>window.staticComposedCounter===document.querySelector('[data-count]'))).toBe(true);
 await page.getByRole('link',{name:'Category summary',exact:true}).click();await expect(page.getByRole('heading',{name:'Summary: coffee',exact:true})).toBeVisible();expect(await page.evaluate(()=>window.staticComposedDocument)).toBe(true);await page.getByRole('link',{name:'Back to category',exact:true}).click();await expect(page.getByRole('heading',{name:'Browse coffee',exact:true})).toBeVisible();
});

test('static publication rejects response header policies and preserves its previous artifact',async()=>{
 const directory=join(process.env.NEXTGO_STATIC_E2E_ROOT,'static');const original=await readFile(join(directory,'index.html'));const cli=join(process.env.NEXTGO_STATIC_E2E_ROOT,'nextgo');const child=spawn(cli,['export','-out',directory,'-paths','/about','./examples/demo'],{env:{...process.env,NEXTGO_DEMO_HEADER_RULES:'true'}});let output='';child.stdout.on('data',data=>output+=data);child.stderr.on('data',data=>output+=data);const code=await new Promise(resolve=>child.on('exit',resolve));expect(code).not.toBe(0);expect(output).toContain('response header rules require an HTTP server');expect(await readFile(join(directory,'index.html'))).toEqual(original);
});

test('keyed collections hydrate exported rows, update in Go and navigate using static data',async({page})=>{
 await page.goto('http://127.0.0.1:4102/keyed');await ready(page);const region=page.getByRole('region',{name:'First keyed list',exact:true});await page.evaluate(()=>{window.staticKeyedDocument=true;window.staticKeyedRow=document.querySelector('[data-list-name="First keyed list"] [data-nextgo-key="a"]')});await region.getByRole('button',{name:'Count a',exact:true}).click();await region.getByRole('button',{name:'Reverse rows',exact:true}).click();expect(await region.locator('[data-nextgo-key="a"]').evaluate(node=>node===window.staticKeyedRow)).toBe(true);await expect(region.locator('[data-nextgo-key="a"] [data-clicks]')).toHaveText('1');await region.getByRole('button',{name:'Add row',exact:true}).click();await expect(region.locator('[data-rows] > *')).toHaveCount(4);await region.getByRole('link',{name:'Next list page',exact:true}).click();await expect(page).toHaveTitle('Keyed collection details — Next.go');expect(await page.evaluate(()=>window.staticKeyedDocument)).toBe(true);
});

test('composed contexts/effects hydrate exported HTML and retain layout state through Go navigation',async({page})=>{
 await page.goto('/composition');await ready(page);const root=page.getByRole('region',{name:'Page composition',exact:true});const layout=page.getByRole('region',{name:'Retained composition',exact:true});await page.evaluate(()=>{window.staticComposition=true;window.staticCompositionValue=document.querySelector('[data-composition="Page composition"] [data-value]')});await root.getByRole('button',{name:'Increment shared provider',exact:true}).click();await expect(root.locator('[data-panel="Sibling"] [data-value]')).toHaveText('3');expect(await root.locator('[data-value]').first().evaluate(node=>node===window.staticCompositionValue)).toBe(true);await layout.getByRole('button',{name:'Increment shared provider',exact:true}).click();await root.getByRole('link',{name:'Next composition page',exact:true}).click();await expect(page).toHaveTitle('Composition details — Next.go');expect(await page.evaluate(()=>window.staticComposition)).toBe(true);await expect(layout.locator('[data-panel="Sibling"] [data-value]')).toHaveText('10');
});

test('typed controlled fields and local Go validation work without an application server',async({page})=>{
 let calls=0;page.on('request',r=>{if(r.url().includes('/_nextgo/actions/'))calls++});await page.goto('/field-preview');await ready(page);const root=page.getByRole('region',{name:'Profile editor',exact:true});await page.evaluate(()=>{window.staticFieldNode=document.querySelector('[data-field="name"]');window.staticFieldsDocument=true});await root.getByLabel('Display name',{exact:true}).fill('');await root.getByLabel('Email',{exact:true}).focus();await expect(root.locator('[data-error="name"]')).toHaveText('Enter a name from 1 to 80 bytes.');await root.getByLabel('Display name',{exact:true}).fill(' Static Grace ');await root.getByLabel('Email',{exact:true}).focus();await expect(root.locator('[data-error="name"]')).toBeEmpty();await root.getByLabel('Newsletter',{exact:true}).check();await root.getByRole('button',{name:'Apply local preview',exact:true}).click();await expect(root.getByLabel('Display name',{exact:true})).toHaveValue('Static Grace');expect(await root.getByLabel('Display name',{exact:true}).evaluate(node=>node===window.staticFieldNode)).toBe(true);await root.getByLabel('Display name',{exact:true}).fill('Unsaved');await root.getByRole('button',{name:'Reset to saved values',exact:true}).click();await expect(root.getByLabel('Display name',{exact:true})).toHaveValue('Static Grace');await expect(root.getByLabel('Newsletter',{exact:true})).toBeChecked();await root.getByRole('link',{name:'Leave editor',exact:true}).click();await expect(page).toHaveTitle('Architecture — Next.go');expect(await page.evaluate(()=>window.staticFieldsDocument)).toBe(true);expect(calls).toBe(0);
});

test('exported radio groups and multiple selects retain SSR controls and local Go state without an API',async({page})=>{
 let calls=0;page.on('request',r=>{if(r.url().includes('/_nextgo/actions/'))calls++});await page.goto('/choice-preview');await ready(page);const root=page.getByRole('region',{name:'Choice editor',exact:true});await page.evaluate(()=>{window.staticChoiceRadio=document.querySelector('[data-channel="sms"]');window.staticChoiceOption=document.querySelector('[data-topics] option[value="wasm"]');window.staticChoiceDocument=true});await root.getByLabel('SMS notifications',{exact:true}).check();await root.getByLabel('Topics',{exact:true}).selectOption(['routing','wasm']);await root.getByRole('button',{name:'Apply local choices',exact:true}).click();await expect(root.locator('[data-choice-status]')).toHaveText('Local choices applied.');await root.getByLabel('Email notifications',{exact:true}).check();await root.getByLabel('Topics',{exact:true}).selectOption([]);await root.getByRole('button',{name:'Reset confirmed choices',exact:true}).click();await expect(root.getByLabel('SMS notifications',{exact:true})).toBeChecked();await expect(root.getByLabel('Topics',{exact:true})).toHaveValues(['routing','wasm']);expect(await page.evaluate(()=>window.staticChoiceRadio===document.querySelector('[data-channel="sms"]')&&window.staticChoiceOption===document.querySelector('[data-topics] option[value="wasm"]'))).toBe(true);await root.getByRole('link',{name:'Leave choices',exact:true}).click();await expect(page).toHaveTitle('Architecture — Next.go');expect(await page.evaluate(()=>window.staticChoiceDocument)).toBe(true);expect(calls).toBe(0);
});

test('static exported file inputs read Go bytes locally and restore their confirmed selection',async({page})=>{
 let calls=0;page.on('request',r=>{if(r.url().includes('/_nextgo/actions/'))calls++});await page.goto('/file-preview');await ready(page);const root=page.getByRole('region',{name:'File editor',exact:true});await page.evaluate(()=>window.staticFileNode=document.querySelector('[data-files]'));await root.getByLabel('Files',{exact:true}).setInputFiles({name:'local.bin',mimeType:'application/octet-stream',buffer:Buffer.from([0,1,255])});await root.getByRole('button',{name:'Read files locally',exact:true}).click();await expect(root.locator('[data-file-status]')).toHaveText('Local files read.');await expect(root.locator('[data-file-receipt]')).toContainText('local.bin (3 bytes):');await root.getByLabel('Files',{exact:true}).setInputFiles([]);await root.getByRole('button',{name:'Reset to confirmed values',exact:true}).click();await expect(root.locator('[data-file-selection]')).toHaveText('local.bin');expect(await root.getByLabel('Files',{exact:true}).evaluate(node=>node===window.staticFileNode&&node.files[0].name==='local.bin')).toBe(true);expect(calls).toBe(0);
});
