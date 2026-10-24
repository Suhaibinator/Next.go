import {test,expect} from '@playwright/test';
const origin='http://127.0.0.1:4144';
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');

test('conditional response headers accompany useful SSR and node-preserving Go hydration',async({page})=>{
 let release;const gate=new Promise(resolve=>release=resolve);await page.route('**/app.wasm',async route=>{await gate;await route.continue()});const response=await page.goto(origin+'/headers/rules/headers/article?variant=blue');expect(response.headers()['x-demo-stage']).toBe('selected');expect(response.headers()['x-demo-slug']).toBe('article');expect(response.headers()['x-demo-variant']).toBe('blue');expect(response.headers()['cache-control']).toBe('private, no-store');expect(response.headers().link).toContain('/docs/base');expect(response.headers().link).toContain('/docs/selected');await expect(page.getByRole('heading',{name:'Response headers from Go'})).toBeVisible();await expect(page.locator('[data-count]')).toHaveText('2');await page.evaluate(()=>window.headerCounter=document.querySelector('[data-count]'));release();await ready(page);await page.getByRole('button',{name:'Increase Header rules counter',exact:true}).click();await expect(page.locator('[data-count]')).toHaveText('3');expect(await page.evaluate(()=>window.headerCounter===document.querySelector('[data-count]'))).toBe(true);
});

test('navigation keeps Go running and receives the destination header policy',async({page})=>{
 await page.goto(origin+'/headers/rules/headers/article?variant=blue');await ready(page);await page.evaluate(()=>window.headerDocument=true);const responsePromise=page.waitForResponse(r=>r.url().includes('/rules/headers/next?variant=green')&&r.request().headers()['x-nextgo-navigate']==='1');await page.getByRole('link',{name:'Next header page',exact:true}).click();const response=await responsePromise;expect(response.headers()['x-demo-slug']).toBe('next');expect(response.headers()['x-demo-variant']).toBe('green');expect(response.headers()['cache-control']).toBe('private, no-store');await expect(page.locator('[data-header-slug]')).toHaveText('next');expect(await page.evaluate(()=>window.headerDocument)).toBe(true);await page.goBack();await expect(page.locator('[data-header-slug]')).toHaveText('article');
});

test('misses and cookie negation remain private and API handlers cannot override privacy',async({request})=>{
 for(const options of [{},{headers:{cookie:'demo_header_skip=yes'}}]){const response=await request.get(origin+'/headers/rules/headers/article?variant=blue',options);expect(response.status()).toBe(200);expect(response.headers()['x-demo-stage']).toBe(options.headers?'base':'selected');expect(response.headers()['cache-control']).toBe('private, no-store');expect(response.headers()['x-nextgo-prefetch']).toBeUndefined()}
 const miss=await request.get(origin+'/headers/rules/headers/article');expect(miss.headers()['x-demo-stage']).toBe('base');expect(miss.headers()['cache-control']).toBe('private, no-store');
 for(const options of [{},{headers:{'X-Demo-Region':'eu-west'}}]){const response=await request.get(origin+'/headers/api/header-policy',options);expect(response.status()).toBe(200);expect(response.headers()['cache-control']).toBe('private, no-store');expect(response.headers()['x-demo-region']).toBe(options.headers?'eu-west':undefined)}
});

test('invalid captured values fail before partial headers or page rendering',async({request})=>{
 const response=await request.get(origin+'/headers/rules/headers/article?variant=blue&raw=%0D%0AInjected%3Ayes');expect(response.status()).toBe(400);expect(response.headers()['x-demo-stage']).toBeUndefined();expect(response.headers().injected).toBeUndefined();expect(await response.text()).toBe('Invalid response header rule\n');expect(response.headers()['cache-control']).toBe('private, no-store');
});

test('separate asset mount applies source rules and retains anonymous WASM delivery',async({page,request})=>{
 const asset=await request.get(origin+'/header-assets/assets/style.css');expect(asset.status()).toBe(200);expect(asset.headers()['x-demo-asset']).toBe('style.css');expect(asset.headers()['access-control-allow-origin']).toBe('*');await page.goto(origin+'/headers/rules/headers/article');await ready(page);await page.getByRole('button',{name:'Increase Header rules counter',exact:true}).click();await expect(page.locator('[data-count]')).toHaveText('3');
});

test('locale/slash navigation uses logical sources and canonical redirects retain headers',async({page,request})=>{
 const root='http://127.0.0.1:4145';const redirect=await request.get(root+'/site/FR/rules/headers/article?variant=blue',{maxRedirects:0});expect(redirect.status()).toBe(308);expect(redirect.headers()['x-demo-stage']).toBe('selected');await page.goto(root+'/site/fr/rules/headers/article/?variant=blue');await ready(page);await page.evaluate(()=>window.headerDocument=true);await page.getByRole('link',{name:'Next header page',exact:true}).click();await expect(page).toHaveURL(root+'/site/fr/rules/headers/next/?variant=green');await expect(page.locator('[data-header-slug]')).toHaveText('next');expect(await page.evaluate(()=>window.headerDocument)).toBe(true);
});
