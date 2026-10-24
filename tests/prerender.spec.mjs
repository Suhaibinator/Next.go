import {test,expect} from '@playwright/test';
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');

test('build snapshots provide useful HTML and hydrate the same nodes',async({page})=>{
 let release;const gate=new Promise(resolve=>release=resolve);await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
 const response=await page.goto('/prerender');expect(response.headers()['x-nextgo-prerender']).toBe('build');await expect(page.getByRole('heading',{name:'Build once, revalidate in Go'})).toBeVisible();await expect(page.locator('[data-count]')).toHaveText('4');await expect(page.getByRole('button',{name:'Increase Prerendered counter',exact:true})).toBeDisabled();await page.evaluate(()=>{window.seedNode=document.querySelector('[data-count]');window.seedDocument=true});release();await ready(page);await page.getByRole('button',{name:'Increase Prerendered counter',exact:true}).click();await expect(page.locator('[data-count]')).toHaveText('5');expect(await page.evaluate(()=>window.seedNode===document.querySelector('[data-count]'))).toBe(true);
 await page.getByRole('link',{name:'Prerendered article',exact:true}).click();await expect(page).toHaveTitle('Prerendered article — Next.go');await expect(page.getByRole('heading',{name:'first',exact:true})).toBeVisible();await page.getByRole('button',{name:'Increase Article counter',exact:true}).click();await expect(page.locator('[data-count]')).toHaveText('2');expect(await page.evaluate(()=>window.seedDocument)).toBe(true);
 await page.goBack();await expect(page).toHaveTitle('Prerendered Go — Next.go');await expect(page.locator('[data-count]')).toHaveText('4');
});

test('unlisted dynamic paths generate on demand while closed lists return custom 404',async({page,request})=>{
 const direct=await request.get('/prerender/first');expect(direct.headers()['x-nextgo-prerender']).toBe('build');
 await page.goto('/prerender');await ready(page);await page.evaluate(()=>window.seedDocument=true);await page.getByRole('link',{name:'Generate an article on demand',exact:true}).click();await expect(page).toHaveURL(/\/prerender\/new$/);await expect(page.getByRole('heading',{name:'new',exact:true})).toBeVisible();expect(await page.evaluate(()=>window.seedDocument)).toBe(true);
 const generated=await request.get('/prerender/new');expect(generated.headers()['x-nextgo-prerender']).toBe('runtime');await page.getByRole('link',{name:'Prerender home',exact:true}).click();await page.getByRole('link',{name:'Unlisted article',exact:true}).click();await expect(page).toHaveTitle('Page not found — Next.go');await expect(page.getByRole('heading',{name:'Nothing here. Yet.'})).toBeVisible();expect(await page.evaluate(()=>window.seedDocument)).toBe(true);
});

test('queries and credentials bypass public build snapshots without poisoning them',async({request})=>{
 const initial=await request.get('/prerender');const html=await initial.text();expect(initial.headers()['x-nextgo-prerender']).toBe('build');
 for(const options of [{params:{message:'Private query'}},{headers:{cookie:'session=private'}},{headers:{authorization:'Bearer private'}}]){const response=await request.get('/prerender',options);expect(response.status()).toBe(200);expect(response.headers()['x-nextgo-prerender']).toBeUndefined();expect(response.headers()['cache-control']).toContain('no-store');if(options.params)expect(await response.text()).toContain('Private query')}
 expect(await(await request.get('/prerender')).text()).toBe(html);
});

for(const {origin,base,path}of[{origin:'http://127.0.0.1:4104',base:'/demo',path:'/demo/prerender/first'},{origin:'http://127.0.0.1:4106',base:'/slash',path:'/slash/prerender/first/'},{origin:'http://127.0.0.1:4120',base:'/shop/fr',path:'/shop/fr/prerender/first'}])test(`changed runtime scope regenerates compatible content in ${base}`,async({page})=>{
 const response=await page.goto(origin+base+'/prerender');expect(response.headers()['x-nextgo-prerender']).toBe('runtime');await ready(page);await page.evaluate(()=>window.seedDocument=true);await page.getByRole('link',{name:'Prerendered article',exact:true}).click();await expect(page).toHaveURL(origin+path);await expect(page.getByRole('heading',{name:'first',exact:true})).toBeVisible();expect(await page.evaluate(()=>window.seedDocument)).toBe(true);
});
