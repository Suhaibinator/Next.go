import { test, expect } from '@playwright/test';
import { readFile, writeFile, mkdir, rm } from 'node:fs/promises';
import { join } from 'node:path';

test('route additions and removals refresh generated helpers without duplicate reloads', async ({ page, request }) => {
  test.setTimeout(120_000);
  const root = join(process.env.NEXTGO_E2E_ROOT, 'development/examples/demo');
  const folder = join(root,'app/typed_preview');
  const generated = join(root,'routes/nextgo_gen.go');
  await page.goto('/about'); await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
  let reloads = 0; page.on('domcontentloaded',()=>reloads++);
  try {
    await mkdir(folder);
    await writeFile(join(folder,'page.go'),`//go:build !js
package preview
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui";"github.com/Suhaibinator/Next.go/examples/demo/routes")
func Page(c *nextgo.Context)(nextgo.Page,error){return nextgo.Page{Title:"Typed development",Body:ui.Text(routes.TypedPreview())},nil}
`);
    await expect.poll(async()=> (await readFile(generated,'utf8')).includes('func TypedPreview()'),{timeout:45_000}).toBe(true);
    await expect.poll(()=>reloads,{timeout:45_000}).toBe(1);
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
    const response=await request.get('/typed_preview'); expect(response.status()).toBe(200); expect(await response.text()).toContain('/typed_preview');
    // Observe multiple watch/debounce periods: generated output must not
    // schedule a second successful build after this route change.
    await page.waitForTimeout(2000); expect(reloads).toBe(1);
    await rm(folder,{recursive:true});
    await expect.poll(async()=> (await readFile(generated,'utf8')).includes('func TypedPreview()'),{timeout:45_000}).toBe(false);
    await expect.poll(()=>reloads,{timeout:45_000}).toBe(2);
    await expect.poll(async()=> (await request.get('/typed_preview')).status(),{timeout:45_000}).toBe(404);
  } finally { await rm(folder,{recursive:true,force:true}); }
});

test('development rebuilds, reloads, retains previous assets, and recovers from compilation errors', async ({ page, request }) => {
  test.setTimeout(120_000);
  const source = join(process.env.NEXTGO_E2E_ROOT, 'development/examples/demo/app/about/page.go');
  const original = await readFile(source, 'utf8');
  await page.goto('/about');
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  const oldBuild = await page.locator('body').getAttribute('data-nextgo-build');
  try {
    await writeFile(source, original.replace('Architecture — Next.go', 'Rebuilt architecture — Next.go'));
    await expect(page).toHaveTitle('Rebuilt architecture — Next.go', { timeout: 45_000 });
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    expect((await request.get(`/_nextgo/${oldBuild}/app.wasm`)).status()).toBe(200);
    await page.evaluate(() => { window.marker = 'last-successful-build'; });
    await writeFile(source, original + '\nthis is intentionally invalid Go\n');
    await expect(page.locator('#nextgo-status')).toContainText('Build failed', { timeout: 45_000 });
    expect(await page.evaluate(() => window.marker)).toBe('last-successful-build');
    await expect(page).toHaveTitle('Rebuilt architecture — Next.go');
    expect((await request.get('/api/health')).status()).toBe(200);
    await writeFile(source, original);
    await expect(page).toHaveTitle('Architecture — Next.go', { timeout: 45_000 });
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
  } finally {
    await writeFile(source, original);
  }
});

test('mounted development reloads and retains assets at a separate public prefix', async ({ page, request }) => {
  test.setTimeout(120_000);
  const root = join(process.env.NEXTGO_E2E_ROOT, 'development/examples/demo/app');
  const configPath = join(root, 'config.go');
  const pagePath = join(root, 'about/page.go');
  const config = await readFile(configPath, 'utf8');
  const source = await readFile(pagePath, 'utf8');
  try {
    await writeFile(configPath, config.replace('config.BasePath = os.Getenv("NEXTGO_DEMO_BASE_PATH")', 'config.BasePath = os.Getenv("NEXTGO_DEMO_BASE_PATH")\n config.BasePath = "/mounted"').replace('config.AssetPrefix = os.Getenv("NEXTGO_DEMO_ASSET_PREFIX")', 'config.AssetPrefix = os.Getenv("NEXTGO_DEMO_ASSET_PREFIX")\n config.AssetPrefix = "/dev-cdn"'));
    await expect.poll(async () => (await request.get('/mounted/api/health')).status(), { timeout: 45_000 }).toBe(200);
    await page.goto('/mounted/about');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    const build = await page.locator('body').getAttribute('data-nextgo-build');
    await writeFile(pagePath, source.replace('Architecture — Next.go', 'Mounted rebuild — Next.go'));
    await expect(page).toHaveTitle('Mounted rebuild — Next.go', { timeout: 45_000 });
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    expect((await request.get(`/dev-cdn/_nextgo/${build}/app.wasm`)).status()).toBe(200);
    await writeFile(pagePath, source + '\ninvalid mounted Go\n');
    await expect(page.locator('#nextgo-status')).toContainText('Build failed', { timeout: 45_000 });
    expect((await request.get('/mounted/api/health')).status()).toBe(200);
    await writeFile(pagePath, source);
    await expect(page).toHaveTitle('Architecture — Next.go', { timeout: 45_000 });
  } finally {
    await writeFile(pagePath, source);
    await writeFile(configPath, config);
    await expect.poll(async () => (await request.get('/api/health')).status(), { timeout: 45_000 }).toBe(200);
  }
});

test('locale development reloads retain language and recover from a failed build', async ({ page, request }) => {
  test.setTimeout(120_000);
  const root = join(process.env.NEXTGO_E2E_ROOT, 'development/examples/demo/app');
  const configPath = join(root, 'config.go');
  const pagePath = join(root, 'translations/page.go');
  const config = await readFile(configPath, 'utf8');
  const source = await readFile(pagePath, 'utf8');
  try {
    await writeFile(configPath, config.replace('config.TrailingSlash = os.Getenv("NEXTGO_DEMO_TRAILING_SLASH")', 'config.Locales = &nextgo.LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true, Detect: true}\n config.TrailingSlash = os.Getenv("NEXTGO_DEMO_TRAILING_SLASH")'));
    await expect.poll(async () => (await request.get('/fr/api/locale')).status(), { timeout: 45_000 }).toBe(200);
    await page.goto('/fr/translations');
    await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
    const build = await page.locator('body').getAttribute('data-nextgo-build');
    await writeFile(pagePath, source.replace('Bonjour', 'Salut'));
    await expect(page).toHaveTitle('Salut — Next.go', { timeout: 45_000 });
    await expect(page.locator('html')).toHaveAttribute('lang', 'fr');
    expect((await request.get(`/_nextgo/${build}/app.wasm`)).status()).toBe(200);
    await writeFile(pagePath, source + '\ninvalid locale build\n');
    await expect(page.locator('#nextgo-status')).toContainText('Build failed', { timeout: 45_000 });
    await expect(page).toHaveTitle('Salut — Next.go');
    await writeFile(pagePath, source);
    await expect(page).toHaveTitle('Bonjour — Next.go', { timeout: 45_000 });
    await expect(page).toHaveURL(/\/fr\/translations$/);
  } finally { await writeFile(pagePath, source); await writeFile(configPath, config); await expect.poll(async()=>{const response=await request.get('/environment',{maxRedirects:0});return response.status()===200&&!response.headers()['content-language']},{timeout:45_000}).toBe(true); }
});

test('environment edits reload unchanged assets, reconnect safely, and retain last good settings on errors',async({page,request})=>{
 test.setTimeout(180_000);
 const envPath=join(process.env.NEXTGO_E2E_ROOT,'development/examples/demo/.env.development.local');
 const privateValue='synthetic-private-config-never-public-59';
 await expect.poll(async()=>{const response=await request.get('/environment',{maxRedirects:0});return response.status()===200&&!response.headers()['content-language']},{timeout:45_000}).toBe(true);
 await page.goto('/environment');await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
 const build=await page.locator('body').getAttribute('data-nextgo-build');
 const generation=await page.locator('body').getAttribute('data-nextgo-dev-generation');
 let reloads=0;page.on('domcontentloaded',()=>reloads++);
 try{
  await writeFile(envPath,`NEXTGO_DEMO_PUBLIC_MESSAGE="Hello from file"\nSERVER_ONLY=${privateValue}\nAWS_LAMBDA_RUNTIME_API=must-not-select-lambda\n`);
  await expect(page.locator('[data-setting-message]')).toHaveText('Hello from file',{timeout:45_000});
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
  expect(await page.locator('body').getAttribute('data-nextgo-build')).toBe(build);
  const currentGeneration=await page.locator('body').getAttribute('data-nextgo-dev-generation');expect(currentGeneration).not.toBe(generation);
  await page.locator('[data-setting-read]').click();await expect(page.locator('[data-setting-result]')).toHaveText('Hello from file');
  expect(await page.content()).not.toContain(privateValue);
  const wasm=await request.get(`/_nextgo/${build}/app.wasm`);expect(wasm.status()).toBe(200);expect((await wasm.body()).includes(Buffer.from(privateValue))).toBe(false);
  // A disconnected old document must reload on reconnect even with the same asset ID.
  // Read and cancel the event stream without leaving a connection behind.
  const result=await page.evaluate(async({build,generation})=>{const response=await fetch(`/_nextgo/events?build=${build}&generation=${generation}`);const reader=response.body.getReader();let result='';while(!result.includes('event: reload')){const {value,done}=await reader.read();if(done)break;result+=new TextDecoder().decode(value)}await reader.cancel();return result},{build,generation});
  expect(result).toContain('event: reload');
  await writeFile(envPath,`NEXTGO_DEMO_PUBLIC_MESSAGE="unclosed ${privateValue}\n`);
  await expect(page.locator('#nextgo-status')).toContainText('Build failed',{timeout:45_000});
  await expect(page.locator('[data-setting-message]')).toHaveText('Hello from file');
  expect((await request.get('/api/health')).status()).toBe(200);
  expect(await page.locator('#nextgo-status').textContent()).not.toContain(privateValue);
  await writeFile(envPath,'NEXTGO_DEMO_PUBLIC_MESSAGE="Recovered configuration"\n');
  await expect(page.locator('[data-setting-message]')).toHaveText('Recovered configuration',{timeout:45_000});
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
  await rm(envPath);await expect(page.locator('[data-setting-message]')).toHaveText('Configured at server startup',{timeout:45_000});
  await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
  await page.waitForTimeout(1500);expect(reloads).toBe(3);
 }finally{await rm(envPath,{force:true})}
});


test('lazy module rebuilds retain old assets and recover from invalid component configuration',async({page,request})=>{
 test.setTimeout(120_000);
 const folder=join(process.env.NEXTGO_E2E_ROOT,'development/examples/demo/components/lazycounter');const configPath=join(folder,'component.json');const clientPath=join(folder,'client_js.go');const config=await readFile(configPath,'utf8');const source=await readFile(clientPath,'utf8');
 let moduleURL;page.on('request',req=>{if(/module-[a-f0-9]{64}\.wasm$/.test(req.url()))moduleURL=req.url()});
 await page.goto('/lazy');await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');const oldModule=moduleURL;expect(oldModule).toBeTruthy();await page.evaluate(()=>window.lastLazyBuild=true);
 try {
  await writeFile(configPath,'{"loading":"invalid"}');await expect(page.locator('#nextgo-status')).toContainText('Build failed',{timeout:45_000});expect(await page.evaluate(()=>window.lastLazyBuild)).toBe(true);const first=page.getByRole('region',{name:'First lazy counter',exact:true});await first.locator('[data-lazy-increment]').click();await expect(first.locator('[data-lazy-count]')).toHaveText('6');
  await writeFile(clientPath,source.replace('lazy-client-only-proof-7d91','rebuilt-lazy-component'));await writeFile(configPath,config);
  await expect(first.locator('[data-lazy-proof]')).toHaveText('rebuilt-lazy-component',{timeout:45_000});await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');expect(moduleURL).not.toBe(oldModule);expect((await request.get(oldModule)).status()).toBe(200);expect(await page.evaluate(()=>window.lastLazyBuild)).toBe(undefined);
 }finally {await writeFile(clientPath,source);await writeFile(configPath,config);await expect(page.getByRole('region',{name:'First lazy counter',exact:true}).locator('[data-lazy-proof]')).toHaveText('lazy-client-only-proof-7d91',{timeout:45_000})}
});

test('a public dependency edit rebuilds and reloads the Go widget with fresh library bytes',async({page})=>{
 test.setTimeout(120_000);const source=join(process.env.NEXTGO_E2E_ROOT,'development/examples/demo/public/vendor-demo.js');const original=await readFile(source,'utf8');await page.goto('/scripts');await expect(page.getByRole('region',{name:'First script widget',exact:true}).locator('[data-script-result]')).toHaveText('Ready: demo-library-v1');await page.evaluate(()=>window.dependencyBeforeReload=true);
 try{await writeFile(source,original.replace('demo-library-v1','rebuilt-demo-library'));await expect(page.getByRole('region',{name:'First script widget',exact:true}).locator('[data-script-result]')).toHaveText('Ready: rebuilt-demo-library',{timeout:45_000});expect(await page.evaluate(()=>window.dependencyBeforeReload)).toBe(undefined);expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(1)}finally{await writeFile(source,original);await expect(page.getByRole('region',{name:'First script widget',exact:true}).locator('[data-script-result]')).toHaveText('Ready: demo-library-v1',{timeout:45_000})}
});

test('parent parameter edits rebuild child paths and generator failures retain the last application',async({page,request})=>{
 test.setTimeout(120_000);
 const file=join(process.env.NEXTGO_E2E_ROOT,'development/examples/demo/app/market/param_category/staticparams.go');const original=await readFile(file,'utf8');const changed=original.replace('"filter"','"decaf"');expect(changed).not.toBe(original);
 await page.goto('/market/coffee');await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');await expect(page.getByRole('link',{name:'Product filter',exact:true})).toBeVisible();
 try{
  await writeFile(file,changed);await expect(page.getByRole('link',{name:'Product decaf',exact:true})).toBeVisible({timeout:45_000});await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');expect((await request.get('/market/coffee/filter')).status()).toBe(404);expect((await request.get('/market/coffee/decaf')).status()).toBe(200);await page.evaluate(()=>window.lastComposedBuild=true);
  const failed=changed.replace('return rows, nil','return nil, context.Canceled');expect(failed).not.toBe(changed);await writeFile(file,failed);await expect(page.locator('#nextgo-status')).toContainText('Build failed',{timeout:45_000});await expect(page.getByRole('link',{name:'Product decaf',exact:true})).toBeVisible();expect((await request.get('/market/coffee/decaf')).status()).toBe(200);expect(await page.evaluate(()=>window.lastComposedBuild)).toBe(true);
  await writeFile(file,original);await expect(page.getByRole('link',{name:'Product filter',exact:true})).toBeVisible({timeout:45_000});await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');expect((await request.get('/market/coffee/decaf')).status()).toBe(404);
 }finally{await writeFile(file,original)}
});
