import {test,expect} from '@playwright/test';
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');

test('composed build paths hydrate existing product nodes and navigate through an inherited static child',async({page})=>{
 let release;const gate=new Promise(resolve=>release=resolve);await page.route('**/app.wasm',async route=>{await gate;await route.continue()});const response=await page.goto('/market/coffee/espresso');expect(response.headers()['x-nextgo-prerender']).toBe('build');await expect(page.getByRole('heading',{name:'coffee / espresso',exact:true})).toBeVisible();await expect(page.locator('[data-catalog-category]')).toHaveAttribute('data-catalog-category','coffee');await expect(page.locator('[data-count]')).toHaveText('7');await page.evaluate(()=>{window.composedDocument=true;window.composedCounter=document.querySelector('[data-count]')});release();await ready(page);await page.getByRole('button',{name:'Increase Catalog counter',exact:true}).click();await expect(page.locator('[data-count]')).toHaveText('8');expect(await page.evaluate(()=>window.composedCounter===document.querySelector('[data-count]'))).toBe(true);
 await page.getByRole('link',{name:'Category summary',exact:true}).click();await expect(page).toHaveTitle('Category summary — Next.go');await expect(page.getByRole('heading',{name:'Summary: coffee',exact:true})).toBeVisible();expect(await page.evaluate(()=>window.composedDocument)).toBe(true);await page.goBack();await expect(page.getByRole('heading',{name:'coffee / espresso',exact:true})).toBeVisible();await expect(page.locator('[data-count]')).toHaveText('7');
});

test('parent output selects matching product lists and unlisted combinations return custom 404',async({page,request})=>{
 for(const path of ['/market/coffee','/market/coffee/filter','/market/tea/green','/market/tea/summary']){const response=await request.get(path);expect(response.status()).toBe(200);expect(response.headers()['x-nextgo-prerender']).toBe('build')}
 const bad=await request.get('/market/coffee/green');expect(bad.status()).toBe(404);
 await page.goto('/market');await ready(page);await page.evaluate(()=>window.composedDocument=true);await page.getByRole('link',{name:'Browse tea',exact:true}).click();await expect(page.getByRole('heading',{name:'Browse tea',exact:true})).toBeVisible();await page.getByRole('link',{name:'Product green',exact:true}).click();await expect(page).toHaveURL(/\/market\/tea\/green$/);await expect(page.getByRole('heading',{name:'tea / green',exact:true})).toBeVisible();expect(await page.evaluate(()=>window.composedDocument)).toBe(true);
});

for(const {origin,base,category,productURL}of[
 {origin:'http://127.0.0.1:4104',base:'/demo',category:'coffee',productURL:'/demo/market/coffee/espresso'},
 {origin:'http://127.0.0.1:4106',base:'/slash',category:'coffee',productURL:'/slash/market/coffee/espresso/'},
 {origin:'http://127.0.0.1:4120',base:'/shop/fr',category:'café',productURL:'/shop/fr/market/caf%C3%A9/espresso'},
])test(`composed parameter scopes preserve build ownership and locale inputs in ${base}`,async({page})=>{
 await page.goto(origin+base+'/market');await ready(page);await page.evaluate(()=>window.composedDocument=true);await page.getByRole('link',{name:'Browse '+category,exact:true}).click();await page.getByRole('link',{name:'Product espresso',exact:true}).click();await expect(page).toHaveURL(origin+productURL);await expect(page.getByRole('heading',{name:category+' / espresso',exact:true})).toBeVisible();expect(await page.evaluate(()=>window.composedDocument)).toBe(true);await page.getByRole('link',{name:'Category summary',exact:true}).click();await expect(page.getByRole('heading',{name:'Summary: '+category,exact:true})).toBeVisible();
});
