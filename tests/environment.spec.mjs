import {test,expect} from '@playwright/test';

test('runtime configuration preserves SSR nodes and powers Go interaction',async({page})=>{
 let release;const gate=new Promise(resolve=>{release=resolve});
 await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
 await page.goto('/environment',{waitUntil:'domcontentloaded'});
 await expect(page.locator('[data-setting-message]')).toHaveText('Configured at server startup');
 await expect(page.locator('[data-setting-read]')).toBeDisabled();
 await page.evaluate(()=>{window.settingsNode=document.querySelector('[data-setting-message]');window.settingsDocument=true});
 release();await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
 expect(await page.evaluate(()=>window.settingsNode===document.querySelector('[data-setting-message]'))).toBe(true);
 await page.locator('[data-setting-read]').click();await expect(page.locator('[data-setting-result]')).toHaveText('Configured at server startup');
 await page.getByRole('link',{name:'Architecture',exact:true}).last().click();await expect(page).toHaveTitle('Architecture — Next.go');
 expect(await page.evaluate(()=>window.settingsDocument)).toBe(true);
 await page.goBack();await expect(page).toHaveTitle('Runtime configuration — Next.go');
 await page.locator('[data-setting-read]').click();await expect(page.locator('[data-setting-result]')).toHaveText('Configured at server startup');
});

test('public settings remain a frozen document snapshot',async({page})=>{
 await page.goto('/environment');await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
 await page.evaluate(()=>{const body=document.body;const manifest=JSON.parse(body.dataset.nextgoNavigation);manifest.public['demo.message']='DOM mutation';body.dataset.nextgoNavigation=JSON.stringify(manifest)});
 await page.locator('[data-setting-read]').click();await expect(page.locator('[data-setting-result]')).toHaveText('Configured at server startup');
});
