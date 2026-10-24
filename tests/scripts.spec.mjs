import {test,expect} from '@playwright/test';
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const widget=(page,name='First script widget')=>page.getByRole('region',{name,exact:true});
const loaded=(page,name)=>expect(widget(page,name).locator('[data-script-result]')).toHaveText('Ready: demo-library-v1');

test('optional dependency loading preserves SSR while Go runs, deduplicates aliases and crosses WASM runtimes',async({page})=>{
 let release;const gate=new Promise(resolve=>release=resolve);let requests=0;
 await page.route('**/vendor-demo.js',async route=>{requests++;await gate;await route.continue()});
 await page.goto('/scripts');await ready(page);await expect(widget(page).locator('[data-script-result]')).toHaveText('Waiting for trusted library');await page.evaluate(()=>{window.scriptDocument=true;window.scriptNode=document.querySelector('[data-script-result]')});
 await page.getByRole('button',{name:'Increase Independent Go counter',exact:true}).click();await expect(page.getByRole('region',{name:'Independent Go counter',exact:true}).locator('[data-count]')).toHaveText('3');release();
 await loaded(page);await loaded(page,'Second script widget');await expect(page.getByRole('region',{name:'Lazy Go script consumer',exact:true}).locator('[data-lazy-proof]')).toHaveText('Shared library ready');expect(requests).toBe(1);expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(1);expect(await page.evaluate(()=>window.scriptNode===document.querySelector('[data-script-result]'))).toBe(true);
 await widget(page).locator('[data-script-increment]').click();await expect(widget(page).locator('[data-script-count]')).toHaveText('1');await expect(widget(page,'Second script widget').locator('[data-script-count]')).toHaveText('0');
});

test('navigation reuses loaded code and disposes callbacks and listeners from detached widgets',async({page})=>{
 await page.goto('/scripts');await ready(page);await loaded(page);await page.evaluate(()=>{window.scriptDocument=true;window.oldScriptWidget=document.querySelector('[data-nextgo-island="scriptwidget"]');window.oldScriptButton=window.oldScriptWidget.querySelector('[data-script-increment]')});await widget(page).locator('[data-script-increment]').click();await page.getByRole('link',{name:'Script details',exact:true}).click();await expect(page).toHaveTitle('Script details — Next.go');await loaded(page,'Detail script widget');expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(1);expect(await page.evaluate(()=>{window.oldScriptButton.click();return window.oldScriptWidget.querySelector('[data-script-count]').textContent})).toBe('1');await page.goBack();await expect(page).toHaveTitle('Managed scripts — Next.go');await loaded(page);expect(await page.evaluate(()=>window.scriptDocument)).toBe(true);expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(1);
});

test('unmounting during download suppresses the old ready callback',async({page})=>{
 let release;const gate=new Promise(resolve=>release=resolve);await page.route('**/vendor-demo.js',async route=>{await gate;await route.continue()});await page.goto('/scripts');await ready(page);await page.evaluate(()=>window.pendingScriptResult=document.querySelector('[data-script-result]'));await page.getByRole('navigation',{name:'Main'}).getByRole('link',{name:'Architecture',exact:true}).click();await expect(page).toHaveTitle('Architecture — Next.go');release();await page.waitForFunction(()=>window.NextgoDemoVendorLoads===1);expect(await page.evaluate(()=>window.pendingScriptResult.textContent)).toBe('Waiting for trusted library');
});

test('a failed request reports an error and explicit Go retry recovers without duplicate execution',async({page})=>{
 let requests=0;await page.route('**/vendor-demo.js',route=>++requests===1?route.abort():route.continue());await page.goto('/scripts');await expect(widget(page).locator('[data-script-result]')).toContainText('Library failed');await widget(page).getByRole('button',{name:'Retry library',exact:true}).click();await loaded(page);expect(requests).toBe(2);expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(1);
});

test('integrity failures never execute the dependency and keep the Go counter usable',async({page})=>{
 await page.goto('/scripts?mode=integrity');await ready(page);await expect(widget(page).locator('[data-script-result]')).toContainText('Library failed');expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(undefined);await page.getByRole('button',{name:'Increase Independent Go counter',exact:true}).click();await expect(page.getByRole('region',{name:'Independent Go counter',exact:true}).locator('[data-count]')).toHaveText('3');
});

test('lazy-onload waits for document load and an idle callback',async({page})=>{
 await page.addInitScript(()=>{window.scriptIdle=[];window.requestIdleCallback=callback=>{window.scriptIdle.push(callback);return window.scriptIdle.length};window.cancelIdleCallback=()=>{}});let requests=0;page.on('request',request=>{if(request.url().endsWith('/vendor-demo.js'))requests++});await page.goto('/scripts?mode=lazy');await ready(page);await page.waitForFunction(()=>window.scriptIdle.length>0);expect(requests).toBe(0);await expect(widget(page).locator('[data-script-result]')).toHaveText('Waiting for trusted library');await page.evaluate(()=>{for(const callback of window.scriptIdle.splice(0))callback({didTimeout:false,timeRemaining:()=>20})});await loaded(page);expect(requests).toBe(1);
});

test('module dependencies use the same readiness and deduplication contract',async({page})=>{
 await page.goto('/scripts?mode=module');await ready(page);await expect(widget(page).locator('[data-script-result]')).toHaveText('Ready: demo-module-v1');await expect(widget(page,'Second script widget').locator('[data-script-result]')).toHaveText('Ready: demo-module-v1');expect(await page.evaluate(()=>window.NextgoDemoModuleLoads)).toBe(1);
});

test('script widgets arriving through streamed navigation mount after their dependencies',async({page})=>{
 await page.goto('/');await ready(page);await page.evaluate(()=>{window.scriptDocument=true;const link=document.createElement('a');link.href='/scripts/stream';link.id='stream-script-test';link.textContent='Stream script';document.querySelector('main').prepend(link)});await page.locator('#stream-script-test').click();await expect(page).toHaveTitle('Streamed scripts — Next.go');await loaded(page,'Streamed script widget');await widget(page,'Streamed script widget').locator('[data-script-increment]').click();await expect(widget(page,'Streamed script widget').locator('[data-script-count]')).toHaveText('1');expect(await page.evaluate(()=>window.scriptDocument)).toBe(true);
});

test('an ID conflict is rejected before navigation changes the current page',async({page})=>{
 await page.goto('/scripts');await ready(page);await loaded(page);await page.evaluate(()=>{window.scriptDocument=true;const link=document.createElement('a');link.id='conflicting-script';link.href='/scripts?mode=conflict';link.textContent='Conflict';document.querySelector('main').prepend(link)});await page.locator('#conflicting-script').click();await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');await expect(page).toHaveTitle('Managed scripts — Next.go');await expect(page).toHaveURL(/\/scripts$/);expect(await page.evaluate(()=>window.NextgoDemoModuleLoads)).toBe(undefined);await widget(page).locator('[data-script-increment]').click();await expect(widget(page).locator('[data-script-count]')).toHaveText('1');
});

test('strict nonce CSP permits Go, managed scripts, early order and navigation without permitting injected inline code',async({page,request})=>{
 await page.addInitScript(()=>{window.scriptCSPViolations=[];document.addEventListener('securitypolicyviolation',event=>window.scriptCSPViolations.push(event.violatedDirective))});
 const response=await page.goto('http://127.0.0.1:4142/safe/scripts');expect(response.headers()['cache-control']).toBe('private, no-store');expect(response.headers()['content-security-policy']).toContain("'wasm-unsafe-eval'");await ready(page);await loaded(page);await expect(page.getByRole('region',{name:'Lazy Go script consumer',exact:true}).locator('[data-lazy-proof]')).toHaveText('Shared library ready');expect(await page.evaluate(()=>window.NextgoDemoEarlyOrder)).toBe('ordered');
 const nonce=await page.locator('script[data-nextgo-bootstrap]').evaluate(node=>node.nonce);expect(nonce.length).toBeGreaterThanOrEqual(16);expect(await page.locator('script[data-nextgo-dependency]').evaluateAll((nodes,nonce)=>nodes.every(node=>node.nonce===nonce),nonce)).toBe(true);expect(await page.locator('style').evaluateAll((nodes,nonce)=>nodes.every(node=>node.nonce===nonce),nonce)).toBe(true);expect(await page.locator('script[type="application/ld+json"]').evaluateAll((nodes,nonce)=>nodes.every(node=>node.nonce===nonce),nonce)).toBe(true);expect(await page.evaluate(()=>window.scriptCSPViolations)).toEqual([]);
 await page.evaluate(()=>window.scriptDocument=true);await page.getByRole('link',{name:'Script details',exact:true}).click();await expect(page).toHaveTitle('Script details — Next.go');await loaded(page,'Detail script widget');expect(await page.evaluate(()=>window.scriptDocument)).toBe(true);expect(await page.locator('script[data-nextgo-bootstrap]').evaluate(node=>node.nonce)).toBe(nonce);
 await page.route('http://127.0.0.1:4142/safe/scripts',async route=>{const response=await route.fetch();const body=(await response.text()).replace('</head>','<script>window.untrustedScriptRan=true</script></head>');await route.fulfill({response,body})});await page.goto('http://127.0.0.1:4142/safe/scripts');await ready(page);await expect.poll(()=>page.evaluate(()=>window.scriptCSPViolations.length)).toBeGreaterThan(0);expect(await page.evaluate(()=>window.untrustedScriptRan)).toBe(undefined);
 const second=await request.get('http://127.0.0.1:4142/safe/scripts');expect(second.headers()['content-security-policy']).not.toBe(response.headers()['content-security-policy']);
});

for(const{origin,base,path}of[{origin:'http://127.0.0.1:4104',base:'/demo',path:'/demo/scripts/details'},{origin:'http://127.0.0.1:4106',base:'/slash',path:'/slash/scripts/details/'},{origin:'http://127.0.0.1:4120',base:'/shop/fr',path:'/shop/fr/scripts/details'}])test(`script dependencies resolve asset and navigation ownership in ${base}`,async({page})=>{
 await page.goto(origin+base+'/scripts');await ready(page);await loaded(page);await page.getByRole('link',{name:'Script details',exact:true}).click();await expect(page).toHaveURL(origin+path);await loaded(page,'Detail script widget');expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(1);
});

test('after-interactive demand promotes an already pending idle dependency',async({page})=>{
 await page.addInitScript(()=>{window.scriptIdle=[];window.requestIdleCallback=callback=>{window.scriptIdle.push(callback);return window.scriptIdle.length};window.cancelIdleCallback=()=>{}});await page.goto('/scripts?mode=lazy');await ready(page);await page.waitForFunction(()=>window.scriptIdle.length>0);await page.getByRole('link',{name:'Script details',exact:true}).click();await expect(page).toHaveTitle('Script details — Next.go');await loaded(page,'Detail script widget');expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(1);
});

test('independent dependencies obey the four-download admission bound',async({page})=>{
 let release;const gate=new Promise(resolve=>release=resolve);let requests=0;await page.route(/\/vendor-demo\.js(?:\?.*)?$/,async route=>{requests++;await gate;await route.continue()});await page.goto('/scripts?mode=concurrent');await ready(page);await expect.poll(()=>requests).toBe(4);await page.waitForTimeout(100);expect(requests).toBe(4);release();await expect(page.locator('[data-script-result]')).toHaveText(Array(8).fill('Ready: demo-library-v1'));expect(requests).toBe(7);expect(await page.evaluate(()=>window.NextgoDemoVendorLoads)).toBe(7);
});

test('malformed script navigation metadata is rejected and a repaired response can retry',async({page})=>{
 await page.goto('/scripts');await ready(page);await loaded(page);await page.route('**/scripts/details',async route=>{if(route.request().headers()['x-nextgo-navigate']!=='1'){await route.continue();return}const response=await route.fetch();const data=await response.json();const declaration=JSON.stringify({id:'bad',src:'javascript:window.badScript=true'}).replaceAll('"','&quot;');data.html=data.html.replace(/data-nextgo-script="[^"]*"/,`data-nextgo-script="${declaration}"`);await route.fulfill({response,json:data})});await page.getByRole('link',{name:'Script details',exact:true}).click();await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');await expect(page).toHaveURL(/\/scripts$/);expect(await page.evaluate(()=>window.badScript)).toBe(undefined);await page.unroute('**/scripts/details');await page.getByRole('link',{name:'Script details',exact:true}).click();await loaded(page,'Detail script widget');
});

test('a nonce-protected streamed document loads Go and its later dependency without policy violations',async({page})=>{
 await page.addInitScript(()=>{window.scriptCSPViolations=[];document.addEventListener('securitypolicyviolation',event=>window.scriptCSPViolations.push(event.violatedDirective))});const response=await page.goto('http://127.0.0.1:4142/safe/scripts/stream');expect(response.headers()['cache-control']).toBe('private, no-store');await ready(page);await loaded(page,'Streamed script widget');expect(await page.evaluate(()=>window.scriptCSPViolations)).toEqual([]);const nonce=await page.locator('script[data-nextgo-bootstrap]').evaluate(node=>node.nonce);expect(await page.locator('script[data-nextgo-dependency]').evaluateAll((nodes,nonce)=>nodes.every(node=>node.nonce===nonce),nonce)).toBe(true);
});
