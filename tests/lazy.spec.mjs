import {test,expect} from '@playwright/test';
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const island=(page,name)=>page.getByRole('region',{name,exact:true});
const moduleURL=/\/module-[a-f0-9]{64}\.wasm(?:\?.*)?$/;

test('lazy download preserves useful SSR and node identity while eager Go is already interactive',async({page})=>{
 let release;const blocked=new Promise(resolve=>release=resolve);let downloads=0;
 await page.route(moduleURL,async route=>{downloads++;await blocked;await route.continue()});
 await page.goto('/lazy');
 const first=island(page,'First lazy counter');await expect(first.locator('[data-lazy-count]')).toHaveText('5');await expect(first.locator('[data-lazy-increment]')).toBeDisabled();
 await page.evaluate(()=>{window.lazyNode=document.querySelector('[data-lazy-count]');window.lazyDocument=true});
 await page.getByRole('button',{name:'Increase Eager companion',exact:true}).click();await expect(island(page,'Eager companion').locator('[data-count]')).toHaveText('4');
 release();await ready(page);expect(downloads).toBe(1);expect(await page.evaluate(()=>window.lazyNode===document.querySelector('[data-lazy-count]'))).toBe(true);
 await first.locator('[data-lazy-increment]').click();await expect(first.locator('[data-lazy-count]')).toHaveText('6');await expect(island(page,'Second lazy counter').locator('[data-lazy-count]')).toHaveText('10');await expect(first.locator('[data-lazy-location]')).toHaveText('/lazy');
});

test('core bundle excludes lazy application code and a page without lazy islands never downloads it',async({page,request})=>{
 let downloads=0;page.on('request',req=>{if(moduleURL.test(req.url()))downloads++});await page.goto('/');await ready(page);expect(downloads).toBe(0);
 const scripts=await page.locator('script[src]').evaluateAll(nodes=>nodes.map(n=>n.src));
 const runtime=scripts.find(src=>src.endsWith('/boot.js'));expect(runtime).toBeTruthy();
 const core=await request.get(runtime.replace(/boot\.js$/,'app.wasm'));expect(core.ok()).toBe(true);expect((await core.body()).includes(Buffer.from('lazy-client-only-proof-7d91'))).toBe(false);
 await page.evaluate(()=>{const link=document.createElement('a');link.href='/lazy';link.id='lazy-test-link';link.textContent='Lazy';document.querySelector('main').prepend(link)});await page.locator('#lazy-test-link').click();await ready(page);await expect(island(page,'First lazy counter').locator('[data-lazy-proof]')).toHaveText('lazy-client-only-proof-7d91');expect(downloads).toBe(1);
});

test('lazy navigation shares the router, history and location, reuses bytes, and cleans detached listeners',async({page})=>{
 let downloads=0;page.on('request',req=>{if(moduleURL.test(req.url()))downloads++});await page.goto('/lazy?initial=yes');await ready(page);
 await page.evaluate(()=>{window.lazyDocument=true;window.lazyDetached=document.querySelector('[data-nextgo-island="lazycounter"]');window.lazyOldButton=window.lazyDetached.querySelector('[data-lazy-increment]')});
 const first=island(page,'First lazy counter');await expect(first.locator('[data-lazy-location]')).toHaveText('/lazy?initial=yes');await first.locator('[data-lazy-increment]').click();await first.locator('[data-lazy-navigate]').click();await expect(page).toHaveTitle('Lazy navigation — Next.go');await ready(page);expect(await page.evaluate(()=>window.lazyDocument)).toBe(true);
 await expect(island(page,'Detail lazy counter').locator('[data-lazy-location]')).toHaveText('/lazy/details');expect(downloads).toBe(1);
 expect(await page.evaluate(()=>{window.lazyOldButton.click();return window.lazyDetached.querySelector('[data-lazy-count]').textContent})).toBe('6');
 await page.goBack();await expect(page).toHaveTitle('Lazy Go modules — Next.go');await ready(page);await expect(island(page,'First lazy counter').locator('[data-lazy-count]')).toHaveText('5');expect(downloads).toBe(1);
});

test('failed module downloads keep SSR, deduplicate retry, then recover on demand',async({page})=>{
 let calls=0;await page.route(moduleURL,async route=>{calls++;if(calls===1)await route.abort();else await route.continue()});await page.goto('/lazy');await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','error');await expect(page.getByRole('button',{name:'Retry component',exact:true})).toHaveCount(1);await expect(island(page,'First lazy counter').locator('[data-lazy-count]')).toHaveText('5');
 await page.getByRole('button',{name:'Retry component',exact:true}).click();await ready(page);await expect(page.locator('#nextgo-status')).toBeHidden();await island(page,'First lazy counter').locator('[data-lazy-increment]').click();await expect(island(page,'First lazy counter').locator('[data-lazy-count]')).toHaveText('6');expect(calls).toBe(2);
});

test('digest corruption is rejected before Go starts and can be retried',async({page})=>{
 let calls=0;await page.route(moduleURL,async route=>{calls++;if(calls===1){const response=await route.fetch();const body=await response.body();body[body.length-1]^=1;await route.fulfill({response,body})}else await route.continue()});await page.goto('/lazy');await expect(page.getByRole('button',{name:'Retry component',exact:true})).toBeVisible();await expect(island(page,'First lazy counter').locator('[data-lazy-proof]')).toHaveText('Server-rendered island');await page.getByRole('button',{name:'Retry component',exact:true}).click();await ready(page);expect(calls).toBe(2);
});

test('superseded module loading never mounts detached islands',async({page})=>{
 let release;const blocked=new Promise(resolve=>release=resolve);await page.route(moduleURL,async route=>{await blocked;await route.continue()});await page.goto('/lazy');await expect(page.locator('[data-nextgo-island="lazycounter"]').first()).toHaveAttribute('data-nextgo-hydrated','loading');await page.evaluate(()=>{window.lazyDetached=document.querySelector('[data-nextgo-island="lazycounter"]');window.lazyDocument=true});await page.getByRole('navigation',{name:'Main'}).getByRole('link',{name:'Architecture',exact:true}).click();await expect(page).toHaveTitle('Architecture — Next.go');release();await ready(page);await page.waitForTimeout(500);expect(await page.evaluate(()=>window.lazyDetached.querySelector('[data-lazy-increment]').disabled)).toBe(true);expect(await page.evaluate(()=>window.lazyDocument)).toBe(true);
});

test('an island arriving in a streamed navigation chunk loads and mounts its own module',async({page})=>{
 await page.goto('/');await ready(page);await page.evaluate(()=>{window.lazyDocument=true;const link=document.createElement('a');link.id='lazy-stream-link';link.href='/lazy/stream';link.textContent='Stream lazy';document.querySelector('main').prepend(link)});await page.locator('#lazy-stream-link').click();await expect(page).toHaveTitle('Streamed lazy module — Next.go');await expect(island(page,'Streamed lazy counter').locator('[data-lazy-increment]')).toBeEnabled();await ready(page);await island(page,'Streamed lazy counter').locator('[data-lazy-increment]').click();await expect(island(page,'Streamed lazy counter').locator('[data-lazy-count]')).toHaveText('8');expect(await page.evaluate(()=>window.lazyDocument)).toBe(true);
});


test('a failed lazy mount cleans callbacks already attached to the SSR tree',async({page})=>{
 let release;const blocked=new Promise(resolve=>release=resolve);await page.route(moduleURL,async route=>{await blocked;await route.continue()});
 await page.goto('/lazy');await page.evaluate(()=>{for(const root of document.querySelectorAll('[data-nextgo-island="lazycounter"]')){const props=JSON.parse(root.getAttribute('data-nextgo-props'));props.target='';root.setAttribute('data-nextgo-props',JSON.stringify(props))}});release();
 await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','error');
 expect(await page.evaluate(()=>{const root=document.querySelector('[data-nextgo-island="lazycounter"]');root.querySelector('[data-lazy-increment]').dispatchEvent(new MouseEvent('click'));return root.querySelector('[data-lazy-count]').textContent})).toBe('5');
 await expect(island(page,'First lazy counter').locator('[data-lazy-increment]')).toBeDisabled();
});

test('leaving a failed island removes retry listeners before another page commits',async({page})=>{
 let downloads=0;await page.route(moduleURL,async route=>{downloads++;await route.abort()});await page.goto('/lazy');await expect(page.getByRole('button',{name:'Retry component',exact:true})).toBeVisible();await page.evaluate(()=>window.oldLazyRetry=document.querySelector('[data-nextgo-lazy-retry]'));
 await page.getByRole('navigation',{name:'Main'}).getByRole('link',{name:'Architecture',exact:true}).click();await expect(page).toHaveTitle('Architecture — Next.go');await ready(page);await expect(page.getByRole('button',{name:'Retry component',exact:true})).toHaveCount(0);await page.evaluate(()=>window.oldLazyRetry.click());await page.waitForTimeout(100);expect(downloads).toBe(1);
});


for(const {origin,base,path}of [{origin:'http://127.0.0.1:4104',base:'/demo',path:'/demo/lazy/details'},{origin:'http://127.0.0.1:4106',base:'/slash',path:'/slash/lazy/details/'},{origin:'http://127.0.0.1:4120',base:'/shop/fr',path:'/shop/fr/lazy/details'}])test(`lazy runtime resolves assets and router paths in ${base}`,async({page})=>{
 await page.goto(origin+base+'/lazy');await ready(page);await page.evaluate(()=>window.scopedLazyDocument=true);const first=island(page,'First lazy counter');await first.locator('[data-lazy-increment]').click();await expect(first.locator('[data-lazy-count]')).toHaveText('6');await first.locator('[data-lazy-navigate]').click();await expect(page).toHaveURL(origin+path);await ready(page);await expect(island(page,'Detail lazy counter').locator('[data-lazy-location]')).toHaveText(path);expect(await page.evaluate(()=>window.scopedLazyDocument)).toBe(true);
});
