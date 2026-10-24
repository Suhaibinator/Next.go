import {test,expect} from '@playwright/test';

test('a source-independent workspace build serves useful SSR and shared server data',async({browser,request})=>{
 const context=await browser.newContext({javaScriptEnabled:false});const page=await context.newPage();await page.goto('http://127.0.0.1:4139/');
 await expect(page.locator('[data-workspace-note]')).toContainText('This text was embedded from a sibling Go module.');
 await expect(page.locator('[data-workspace-label]')).toHaveText('Workspace counter');await expect(page.locator('[data-workspace-increment]')).toBeDisabled();
 await page.getByRole('link',{name:'About this workspace'}).click();await expect(page).toHaveTitle('Workspace architecture — Next.go');await context.close();
 expect(await(await request.get('/api/health')).json()).toMatchObject({ok:true,label:'Workspace counter'});
});

test('the sibling module runs in Go/WASM, preserves SSR identity and navigates without reload',async({page})=>{
 let release;const gate=new Promise(resolve=>{release=resolve});await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
 await page.goto('/',{waitUntil:'domcontentloaded'});await expect(page.locator('[data-workspace-count]')).toHaveText('0');
 await page.evaluate(()=>{window.workspaceNode=document.querySelector('[data-workspace-count]');window.workspaceDocument=true});release();await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');expect(await page.evaluate(()=>window.workspaceNode===document.querySelector('[data-workspace-count]'))).toBe(true);
 await page.locator('[data-workspace-increment]').click();await expect(page.locator('[data-workspace-count]')).toHaveText('2');
 await page.getByRole('link',{name:'About this workspace'}).click();await expect(page).toHaveTitle('Workspace architecture — Next.go');expect(await page.evaluate(()=>window.workspaceDocument)).toBe(true);
 await page.goBack();await expect(page).toHaveTitle('Go workspace — Next.go');await page.locator('[data-workspace-increment]').click();await expect(page.locator('[data-workspace-count]')).toHaveText('2');
});
