import { test, expect } from '@playwright/test';

const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const galleryCount = page => page.getByRole('region', { name: 'Gallery counter', exact: true }).locator('[data-count]');
const openCoast = page => page.getByRole('link', { name: 'Open coastal photo', exact: true });

for (const mode of ['initial document','pushed source']) {
  test(`replace-open dismissal restores its background from a ${mode} without visiting an unrelated entry`, async ({page})=>{
    if(mode==='pushed source'){
      await page.goto('/');await ready(page);await page.getByRole('navigation',{name:'Main',exact:true}).getByRole('link',{name:'Gallery',exact:true}).click();await expect(page).toHaveURL(/\/gallery$/);
    }else{
      await page.goto('/about');await ready(page);await page.goto('/gallery?q=replace');await ready(page);
    }
    const source=page.url();await page.getByRole('button',{name:'Increase Gallery counter',exact:true}).click();
    await page.evaluate(()=>{window.returnDocument=true;window.returnHistoryLength=history.length;window.returnPops=0;addEventListener('popstate',()=>window.returnPops++);});
    const replace=page.getByRole('link',{name:'Replace with coastal photo',exact:true});await replace.focus();await replace.click();
    const modal=page.getByRole('dialog',{name:'Coastal light',exact:true});await expect(modal).toBeVisible();
    expect(await page.evaluate(()=>history.length===window.returnHistoryLength)).toBe(true);
    await modal.getByRole('button',{name:'Close photo',exact:true}).click();await expect(page).toHaveURL(source);await expect(page.locator('dialog')).toHaveCount(0);
    await expect(galleryCount(page)).toHaveText('11');await expect(replace).toBeFocused();
    expect(await page.evaluate(()=>[window.returnDocument,window.returnPops,history.length===window.returnHistoryLength])).toEqual([true,0,true]);
    await page.goBack();await expect(page).toHaveURL(mode==='pushed source'?/\/$/:/\/about$/);
  });
}

test('pushed modal dismissal returns through history and restores the original source query and fragment',async({page})=>{
  await page.goto('/gallery?q=fragment#gallery-photos');await ready(page);const source=page.url();
  await page.evaluate(()=>{window.fragmentReturns=0;addEventListener('popstate',()=>window.fragmentReturns++);});
  await openCoast(page).click();await expect(page.getByRole('dialog')).toBeVisible();await page.getByRole('button',{name:'Close photo',exact:true}).click();
  await expect(page).toHaveURL(source);await expect(page.locator('dialog')).toHaveCount(0);expect(await page.evaluate(()=>window.fragmentReturns)).toBe(1);
});

test('unknown history ownership uses a source replacement instead of an unverified back traversal',async({page})=>{
  await page.goto('/gallery?q=unknown');await ready(page);const source=page.url();await openCoast(page).click();await expect(page.getByRole('dialog')).toBeVisible();
  await page.evaluate(()=>{window.unknownPops=0;addEventListener('popstate',()=>window.unknownPops++);history.replaceState({...history.state,nextgoEntry:'f'.repeat(32)},'',location.href)});
  await page.getByRole('button',{name:'Close photo',exact:true}).click();await expect(page).toHaveURL(source);await expect(page.locator('dialog')).toHaveCount(0);
  expect(await page.evaluate(()=>window.unknownPops)).toBe(0);
});

test('failed replace dismissal preserves the modal and Go state until a successful retry',async({page})=>{
  await page.goto('/gallery');await ready(page);await page.getByRole('link',{name:'Replace with coastal photo',exact:true}).click();await expect(page.getByRole('dialog')).toBeVisible();
  await page.route('**/gallery',route=>route.abort('failed'));
  await page.getByRole('button',{name:'Close photo',exact:true}).click();await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');await expect(page.getByRole('dialog')).toBeVisible();
  await page.getByRole('button',{name:'Increase Modal photo counter',exact:true}).click();await expect(page.getByRole('dialog').locator('[data-count]')).toHaveText('21');
  await page.unroute('**/gallery');await page.getByRole('button',{name:'Close photo',exact:true}).click();await expect(page).toHaveURL(/\/gallery$/);await expect(page.locator('dialog')).toHaveCount(0);
});

test('declarative replacement also replaces same-page fragment entries without fetching a page',async({page})=>{
  await page.goto('/gallery');await ready(page);const before=await page.evaluate(()=>history.length);
  const link=page.getByRole('link',{name:'Replace with coastal photo',exact:true});await link.evaluate(node=>node.setAttribute('href','#gallery-photos'));
  let navigations=0;page.on('request',r=>{if(r.headers()['x-nextgo-navigate']==='1')navigations++});
  await link.click();await expect(page).toHaveURL(/\/gallery#gallery-photos$/);expect(await page.evaluate(()=>history.length)).toBe(before);expect(navigations).toBe(0);
});

test('refreshing an unchanged open modal preserves both Go states, native modality and its opener', async ({page}) => {
  await page.goto('/gallery?q=refresh'); await ready(page);
  await page.getByRole('button',{name:'Increase Gallery counter',exact:true}).click();
  await openCoast(page).focus(); await openCoast(page).click();
  const modal=page.getByRole('dialog',{name:'Coastal light',exact:true}); await expect(modal).toBeVisible();
  await page.getByRole('button',{name:'Increase Modal photo counter',exact:true}).click();
  await page.evaluate(()=>{window.refreshedModal=document.querySelector('dialog');window.refreshedGallery=document.querySelector('[aria-label="Gallery counter"]');window.refreshHistoryLength=history.length;});
  for(let i=0;i<2;i++){
    const response=page.waitForResponse(r=>r.url().endsWith('/photos/one')&&r.request().headers()['x-nextgo-navigate']==='1');
    await page.getByRole('button',{name:'Refresh photo',exact:true}).click(); await (await response).finished(); await expect(page.locator('#nextgo-status')).toBeHidden();
    expect(await page.evaluate(()=>window.refreshedModal===document.querySelector('dialog')&&window.refreshedGallery===document.querySelector('[aria-label="Gallery counter"]')&&history.length===window.refreshHistoryLength)).toBe(true);
    expect(await modal.evaluate(node=>node.matches(':modal'))).toBe(true);
    await expect(modal.locator('[data-count]')).toHaveText('21');
  }
  await modal.getByRole('button',{name:'Close photo',exact:true}).click(); await expect(page.locator('dialog')).toHaveCount(0);
  await expect(openCoast(page)).toBeFocused(); await expect(galleryCount(page)).toHaveText('11');
});

test('modal refresh revalidates changed server output and disposes only the changed modal islands', async ({page}) => {
  await page.goto('/gallery'); await ready(page); await page.getByRole('button',{name:'Increase Gallery counter',exact:true}).click(); await openCoast(page).click();
  await expect(page.getByRole('dialog',{name:'Coastal light',exact:true})).toBeVisible();
  await page.getByRole('button',{name:'Increase Modal photo counter',exact:true}).click();
  await page.evaluate(()=>{window.changedModalCount=document.querySelector('dialog [data-count]');window.changedModalButton=document.querySelector('dialog [data-increment]');});
  await page.setExtraHTTPHeaders({'X-Demo-Photo-Version':'updated'});
  await page.getByRole('button',{name:'Refresh photo',exact:true}).click();
  await expect(page.getByText('Updated on the server with the current request.',{exact:true})).toBeVisible();
  await expect(page.getByRole('dialog').locator('[data-count]')).toHaveText('20');
  expect(await page.evaluate(()=>{window.changedModalButton.click();return window.changedModalCount.textContent})).toBe('21');
  await page.getByRole('button',{name:'Close photo',exact:true}).click(); await expect(galleryCount(page)).toHaveText('11');
});

for(const header of ['X-Demo-Gallery-Deny','X-Demo-Photo-Deny']){
  test(`refreshing an active modal reauthorizes ${header.includes('Gallery')?'background':'photo'} data before retaining DOM`,async({page})=>{
    await page.goto('/gallery');await ready(page);await openCoast(page).click();await expect(page.getByRole('dialog')).toBeVisible();
    await page.evaluate(()=>{window.refreshProtected=[...document.querySelectorAll('[data-increment]')].map(button=>({button,count:button.closest('[data-nextgo-island]').querySelector('[data-count]'),value:button.closest('[data-nextgo-island]').querySelector('[data-count]').textContent}));});
    await page.setExtraHTTPHeaders({[header]:'yes'});await page.getByRole('button',{name:'Refresh photo',exact:true}).click();
    await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');await expect(page.locator('dialog')).toHaveCount(0);
    expect(await page.evaluate(()=>window.refreshProtected.every(item=>{item.button.click();return item.count.textContent===item.value}))).toBe(true);
  });
}

test('direct photo URLs render useful standalone HTML before WASM and never open a contextual modal', async ({ page }) => {
  let release;
  const gate = new Promise(resolve => { release = resolve; });
  await page.route('**/app.wasm', async route => { await gate; await route.continue(); });
  const response = await page.goto('/photos/one?shared=yes', { waitUntil: 'domcontentloaded' });
  expect(response.status()).toBe(200);
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Coastal light');
  await expect(page.getByText('Standalone photo page', { exact: true })).toBeVisible();
  await expect(page.locator('dialog')).toHaveCount(0);
  release();
  await ready(page);
  await expect(page).toHaveTitle('Coastal light');
});

test('soft interception preserves background Go state, contains focus, closes with history and reopens forward', async ({ page }) => {
  await page.goto('/gallery?q=background');
  await ready(page);
  await page.getByRole('button', { name: 'Increase Gallery counter', exact: true }).click();
  await expect(galleryCount(page)).toHaveText('11');
  await page.evaluate(() => { window.galleryDocument = true; window.galleryNode = document.querySelector('[aria-label="Gallery counter"]'); });
  await openCoast(page).focus();
  await openCoast(page).click();
  const modal = page.getByRole('dialog', { name: 'Coastal light', exact: true });
  await expect(modal).toBeVisible();
  await expect(page).toHaveURL(/\/photos\/one$/);
  await expect(modal.getByRole('button', { name: 'Close photo', exact: true })).toBeFocused();
  expect(await modal.evaluate(node => node.matches(':modal'))).toBe(true);
  expect(await page.evaluate(() => window.galleryDocument && window.galleryNode === document.querySelector('[aria-label="Gallery counter"]'))).toBe(true);
  expect(await page.locator('[aria-label="Gallery counter"] [data-count]').textContent()).toBe('11');
  await page.keyboard.press('Shift+Tab');
  expect(await page.evaluate(() => !!document.activeElement.closest('dialog'))).toBe(true);
  await page.evaluate(() => { window.oldModal = document.querySelector('dialog'); window.oldModalButton = window.oldModal.querySelector('[data-increment]'); window.oldModalCount = window.oldModal.querySelector('[data-count]'); });
  await modal.getByRole('button', { name: 'Close photo', exact: true }).click();
  await expect(page).toHaveURL(/\/gallery\?q=background$/);
  await expect(page.locator('dialog')).toHaveCount(0);
  await expect(galleryCount(page)).toHaveText('11');
  await expect(openCoast(page)).toBeFocused();
  expect(await page.evaluate(() => { window.oldModalButton.click(); return [window.oldModal.open, window.oldModalCount.textContent]; })).toEqual([false, '20']);
  await page.goForward();
  await expect(modal).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page).toHaveURL(/\/gallery\?q=background$/);
  await expect(page.locator('dialog')).toHaveCount(0);
  await expect(galleryCount(page)).toHaveText('11');
});

test('modal navigation preserves the current view on a network failure and disposes changed modal islands', async ({ page }) => {
  await page.goto('/gallery'); await ready(page); await openCoast(page).click();
  await expect(page.getByRole('dialog', { name: 'Coastal light', exact: true })).toBeVisible();
  await page.route('**/photos/two', route => route.abort('failed'));
  await page.getByRole('link', { name: 'Next photo', exact: true }).click();
  await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');
  await expect(page).toHaveURL(/\/photos\/one$/);
  await page.getByRole('button', { name: 'Increase Modal photo counter', exact: true }).click();
  await page.evaluate(() => { window.staleModalCount = document.querySelector('dialog [data-count]'); window.staleModalButton = document.querySelector('dialog [data-increment]'); });
  await page.unroute('**/photos/two');
  await page.getByRole('link', { name: 'Next photo', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Mountain air', exact: true })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Modal photo counter', exact: true }).locator('[data-count]')).toHaveText('20');
  expect(await page.evaluate(() => { window.staleModalButton.click(); return window.staleModalCount.textContent; })).toBe('21');
  await page.reload(); await ready(page);
  await expect(page.getByText('Standalone photo page', { exact: true })).toBeVisible();
  await expect(page.getByRole('heading', { level: 1 })).toHaveText('Mountain air');
  await expect(page.locator('dialog')).toHaveCount(0);
});

for (const header of ['X-Demo-Gallery-Deny', 'X-Demo-Photo-Deny']) {
  test(`interception reauthorizes current ${header.includes('Gallery') ? 'background' : 'photo'} access and releases protected state after denial`, async ({ page }) => {
    await page.goto('/gallery'); await ready(page);
    await page.evaluate(() => { window.protectedGalleryCount = document.querySelector('[aria-label="Gallery counter"] [data-count]'); window.protectedGalleryButton = document.querySelector('[aria-label="Gallery counter"] [data-increment]'); });
    await page.setExtraHTTPHeaders({ [header]: 'yes' });
    await openCoast(page).click();
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('Access denied');
    await expect(page.locator('dialog')).toHaveCount(0);
    await expect(page.locator('[aria-label="Gallery counter"]')).toHaveCount(0);
    expect(await page.evaluate(() => { window.protectedGalleryButton.click(); return window.protectedGalleryCount.textContent; })).toBe('10');
  });
}
