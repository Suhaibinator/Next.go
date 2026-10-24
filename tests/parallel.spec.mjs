import { test, expect } from '@playwright/test';
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const board = (page,name) => page.getByRole('navigation', {name:'Board tabs'}).getByRole('link',{name,exact:true});
const count = (page,label) => page.getByRole('region',{name:label,exact:true}).locator('[data-count]');
const selection = (page,label) => page.locator(`output[aria-label="${label}"]`);

test('selected segments render before WASM and preserve their original text nodes during hydration',async({page})=>{
 let release;const gate=new Promise(resolve=>{release=resolve});await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
 await page.goto('/boards/reports/summary?q=ignored',{waitUntil:'domcontentloaded'});
 await expect(selection(page,'Board selections children')).toHaveText('reports');await expect(selection(page,'Report selections children')).toHaveText('index');await expect(selection(page,'Report selections reportdetail')).toHaveText('summary');
 await page.evaluate(()=>{window.selectionText=document.querySelector('output[aria-label="Board selections children"]').firstChild});
 release();await ready(page);expect(await page.evaluate(()=>window.selectionText===document.querySelector('output[aria-label="Board selections children"]').firstChild)).toBe(true);
});

test('retained layout selection observers follow independent branches, history, and island cleanup',async({page})=>{
 await page.goto('/boards/chart');await ready(page);await expect(selection(page,'Board selections children')).toHaveText('chart');await expect(selection(page,'Board selections analytics')).toHaveText('chart');
 await page.evaluate(()=>{window.boardSelection=document.querySelector('section[aria-label="Board selections"]');window.selectionDocument=true});
 await board(page,'Drill').click();await expect(page).toHaveURL(/\/boards\/drill$/);await expect(selection(page,'Board selections children')).toHaveText('chart');await expect(selection(page,'Board selections analytics')).toHaveText('chart');
 await board(page,'Reports').click();await expect(selection(page,'Board selections children')).toHaveText('reports');await expect(selection(page,'Report selections children')).toHaveText('index');
 expect(await page.evaluate(()=>window.boardSelection===document.querySelector('section[aria-label="Board selections"]'))).toBe(true);
 await board(page,'Summary').click();await expect(selection(page,'Report selections reportdetail')).toHaveText('summary');await expect(selection(page,'Report selections children')).toHaveText('index');
 await page.goBack();await expect(selection(page,'Report selections reportdetail')).toHaveText('index');await page.goForward();await expect(selection(page,'Report selections reportdetail')).toHaveText('summary');
 await page.setExtraHTTPHeaders({'X-Demo-Report-Summary-Deny':'yes'});await board(page,'Activity').click();await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');
 const disposed=await page.evaluate(()=>window.boardSelection.querySelector('[data-selection-updates]').textContent);
 await page.setExtraHTTPHeaders({});await page.goBack();await expect(selection(page,'Report selections reportdetail')).toHaveText('summary');
 expect(await page.evaluate(()=>[window.selectionDocument,window.boardSelection.querySelector('[data-selection-updates]').textContent])).toEqual([true,disposed]);
});

test('malformed selection metadata is rejected before a navigation commit and can be retried',async({page})=>{
 await page.goto('/boards/chart');await ready(page);await page.evaluate(()=>{window.validSelection=document.querySelector('output[aria-label="Board selections children"]')});
 await page.route('**/boards/settings',async route=>{if(!route.request().headers()['x-nextgo-navigate'])return route.continue();const response=await route.fetch();const data=await response.json();data.html=data.html.replace(/data-nextgo-selection="[^"]+"/,'data-nextgo-selection="invalid"');await route.fulfill({response,json:data})});
 await board(page,'Settings').click();await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');await expect(page).toHaveURL(/\/boards\/chart$/);await expect(page).toHaveTitle('Chart board — Next.go');
 expect(await page.evaluate(()=>window.validSelection===document.querySelector('output[aria-label="Board selections children"]'))).toBe(true);
 await page.getByRole('button',{name:'Increase Primary chart',exact:true}).click();await expect(count(page,'Primary chart')).toHaveText('13');
 await page.unroute('**/boards/settings');await board(page,'Settings').click();await expect(selection(page,'Board selections children')).toHaveText('settings');await expect(selection(page,'Board selections analytics')).toHaveText('chart');
});

test('invalid initial selection metadata reports a hydration error without changing SSR and navigation recovers',async({page})=>{
 await page.route('**/boards/chart',async route=>{const response=await route.fetch();const html=await response.text();await route.fulfill({response,body:html.replace(/data-nextgo-selection="[^"]+"/,'data-nextgo-selection="invalid"')})});
 await page.goto('/boards/chart');await expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','error');await expect(page.locator('#nextgo-status')).toContainText('Invalid selection snapshot');
 await expect(selection(page,'Board selections children')).toHaveText('chart');await expect(count(page,'Primary chart')).toHaveText('12');await expect(page.locator('[data-nextgo-hydrated]')).toHaveCount(0);
 await board(page,'Settings').click();await ready(page);await expect(selection(page,'Board selections children')).toHaveText('settings');await page.getByRole('button',{name:'Increase Team settings',exact:true}).click();await expect(count(page,'Team settings')).toHaveText('8');
});

test('parallel branches render meaningful defaults before WASM and hydrate the same nodes',async({page})=>{
 let release;const gate=new Promise(resolve=>{release=resolve});await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
 await page.goto('/boards/settings',{waitUntil:'domcontentloaded'});await expect(count(page,'Default analytics')).toHaveText('2');await expect(count(page,'Team settings')).toHaveText('7');
 await page.evaluate(()=>{window.parallelNode=document.querySelector('nextgo-slot');window.parallelText=document.querySelector('[data-count]').firstChild});
 release();await ready(page);expect(await page.evaluate(()=>window.parallelNode===document.querySelector('nextgo-slot')&&window.parallelText===document.querySelector('[data-count]').firstChild)).toBe(true);
});
test('soft navigation retains unmatched slot identity and Go state while replacing and cleaning the matched branch',async({page})=>{
 await page.goto('/boards/chart');await ready(page);
 await page.getByRole('button',{name:'Increase Chart analytics',exact:true}).click();await expect(count(page,'Chart analytics')).toHaveText('6');
 await page.evaluate(()=>{window.chartSlot=document.querySelector('[data-nextgo-slot-name="analytics"]');window.oldTeam=document.querySelector('[data-nextgo-slot-name="team"]');window.oldTeamCount=window.oldTeam.querySelector('[data-count]');window.oldTeamButton=window.oldTeam.querySelector('[data-increment]');window.boardDocument=true});
 await board(page,'Settings').click();await expect(page).toHaveTitle('Settings board — Next.go');await expect(count(page,'Chart analytics')).toHaveText('6');await expect(count(page,'Team settings')).toHaveText('7');
 expect(await page.evaluate(()=>window.chartSlot===document.querySelector('[data-nextgo-slot-name="analytics"]'))).toBe(true);expect(await page.evaluate(()=>{window.oldTeamButton.click();return window.oldTeamCount.textContent})).toBe('3');
 await page.goBack();await expect(page).toHaveTitle('Chart board — Next.go');await expect(count(page,'Default team')).toHaveText('3');
 await page.goForward();await expect(page).toHaveTitle('Settings board — Next.go');await expect(count(page,'Team settings')).toHaveText('7');expect(await page.evaluate(()=>window.boardDocument)).toBe(true);
 await page.reload();await ready(page);await expect(count(page,'Default analytics')).toHaveText('2');
});
test('retention rechecks authorization and drops all protected slot DOM after denial',async({page})=>{
 await page.goto('/boards/chart');await ready(page);await page.evaluate(()=>{window.deniedSlot=document.querySelector('[data-nextgo-slot-name="analytics"]');window.deniedButton=window.deniedSlot.querySelector('[data-increment]')});
 await page.setExtraHTTPHeaders({'X-Demo-Slot-Deny':'yes'});await board(page,'Settings').click();await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');await expect(page.locator('nextgo-slot')).toHaveCount(0);
 expect(await page.evaluate(()=>{window.deniedButton.click();return window.deniedSlot.querySelector('[data-count]').textContent})).toBe('5');
});
test('malformed slot responses leave the current DOM interactive and a repaired retry works',async({page})=>{
 await page.goto('/boards/chart');await ready(page);
 await page.route('**/boards/settings',async route=>{if(!route.request().headers()['x-nextgo-navigate'])return route.continue();const response=await route.fetch();const data=await response.json();data.html=data.html.replace(/data-nextgo-slot-hash="[^"]+"/,'data-nextgo-slot-hash="invalid"');await route.fulfill({response,json:data})});
 await board(page,'Settings').click();await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');await page.getByRole('button',{name:'Increase Chart analytics',exact:true}).click();await expect(count(page,'Chart analytics')).toHaveText('6');
 await page.unroute('**/boards/settings');await board(page,'Settings').click();await expect(page).toHaveTitle('Settings board — Next.go');await expect(count(page,'Chart analytics')).toHaveText('6');
});

test('slot-only direct loads use primary defaults before WASM',async({page})=>{
 let release;const gate=new Promise(resolve=>{release=resolve});await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
 await page.goto('/boards/activity',{waitUntil:'domcontentloaded'});await expect(page).toHaveTitle('Default board — Next.go');await expect(count(page,'Primary default')).toHaveText('1');await expect(count(page,'Team activity')).toHaveText('9');
 await page.evaluate(()=>{window.primaryDefault=document.querySelector('[data-nextgo-slot-name="children"]')});release();await ready(page);
 expect(await page.evaluate(()=>window.primaryDefault===document.querySelector('[data-nextgo-slot-name="children"]'))).toBe(true);
 await page.getByRole('button',{name:'Increase Primary default',exact:true}).click();await expect(count(page,'Primary default')).toHaveText('2');
});
test('slot-only navigation retains the primary page and its Go state with history and deterministic reload',async({page})=>{
 await page.goto('/boards/chart');await ready(page);await page.getByRole('button',{name:'Increase Primary chart',exact:true}).click();await expect(count(page,'Primary chart')).toHaveText('13');
 await page.evaluate(()=>{window.primarySlot=document.querySelector('[data-nextgo-slot-name="children"]');window.primaryCounter=window.primarySlot.querySelector('[data-count]');window.primaryButton=window.primarySlot.querySelector('[data-increment]');window.primaryDocument=true});
 await board(page,'Activity').click();await expect(page).toHaveURL(/\/boards\/activity$/);await expect(page).toHaveTitle('Chart board — Next.go');await expect(count(page,'Primary chart')).toHaveText('13');await expect(count(page,'Team activity')).toHaveText('9');
 expect(await page.evaluate(()=>window.primarySlot===document.querySelector('[data-nextgo-slot-name="children"]')&&window.primaryCounter===document.querySelector('[data-nextgo-slot-name="children"] [data-count]'))).toBe(true);
 await board(page,'Settings').click();await expect(page).toHaveTitle('Settings board — Next.go');expect(await page.evaluate(()=>{window.primaryButton.click();return window.primaryCounter.textContent})).toBe('13');
 await page.goBack();await expect(page).toHaveTitle('Chart board — Next.go');await expect(count(page,'Primary chart')).toHaveText('12');await expect(count(page,'Team activity')).toHaveText('9');
 await page.goForward();await expect(page).toHaveTitle('Settings board — Next.go');expect(await page.evaluate(()=>window.primaryDocument)).toBe(true);
 await board(page,'Activity').click();await expect(page).toHaveURL(/\/boards\/activity$/);await expect(page).toHaveTitle('Settings board — Next.go');await page.reload();await ready(page);await expect(page).toHaveTitle('Default board — Next.go');await expect(count(page,'Primary default')).toHaveText('1');
});
test('retained primary output is revalidated and stale listeners are released when server content changes',async({page})=>{
 await page.goto('/boards/chart');await ready(page);await page.getByRole('button',{name:'Increase Primary chart',exact:true}).click();
 await page.evaluate(()=>{window.stalePrimaryCount=document.querySelector('[data-nextgo-slot-name="children"] [data-count]');window.stalePrimaryButton=document.querySelector('[data-nextgo-slot-name="children"] [data-increment]')});
 await page.setExtraHTTPHeaders({'X-Demo-Primary-Version':'updated'});await board(page,'Activity').click();await expect(count(page,'Primary chart')).toHaveText('20');
 expect(await page.evaluate(()=>{window.stalePrimaryButton.click();return window.stalePrimaryCount.textContent})).toBe('13');
 await page.getByRole('button',{name:'Increase Primary chart',exact:true}).click();await expect(count(page,'Primary chart')).toHaveText('21');
});
test('retained primary authorization is rerun before any old document state can survive',async({page})=>{
 await page.goto('/boards/chart');await ready(page);await page.evaluate(()=>{window.protectedPrimary=document.querySelector('[data-nextgo-slot-name="children"] [data-count]');window.protectedPrimaryButton=document.querySelector('[data-nextgo-slot-name="children"] [data-increment]')});
 await page.setExtraHTTPHeaders({'X-Demo-Primary-Deny':'yes'});await board(page,'Activity').click();await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');await expect(page.locator('nextgo-slot')).toHaveCount(0);
 expect(await page.evaluate(()=>{window.protectedPrimaryButton.click();return window.protectedPrimary.textContent})).toBe('12');
});

test('history from an incompatible deployment falls back to current deterministic slot defaults',async({page})=>{
 await page.addInitScript(()=>{window.addEventListener('popstate',event=>{if(window.staleSlotHistory&&event.state)event.state.nextgoSlots=JSON.stringify({configuration:'f'.repeat(64),selections:{}})})});
 await page.goto('/boards/chart');await ready(page);await page.evaluate(()=>{window.sameSlotDocument=true});await board(page,'Activity').click();await expect(page).toHaveURL(/\/boards\/activity$/);await expect(page).toHaveTitle('Chart board — Next.go');
 await board(page,'Settings').click();await expect(page).toHaveTitle('Settings board — Next.go');await page.evaluate(()=>{window.staleSlotHistory=true});
 await page.goBack();await expect(page).toHaveURL(/\/boards\/activity$/);await expect(page).toHaveTitle('Default board — Next.go');await expect(count(page,'Primary default')).toHaveText('1');await expect(count(page,'Default analytics')).toHaveText('2');await expect(count(page,'Team activity')).toHaveText('9');
 expect(await page.evaluate(()=>window.sameSlotDocument)).toBe(true);await expect(page.locator('#nextgo-status')).toBeHidden();
});

test('named default layouts persist across matching branches and default history restoration',async({page})=>{
 await page.goto('/boards');await ready(page);await expect(count(page,'Analytics shell')).toHaveText('40');await page.getByRole('button',{name:'Increase Analytics shell',exact:true}).click();
 await page.evaluate(()=>{window.defaultLayout=document.querySelector('[data-analytics-shell]');window.defaultCount=document.querySelector('[aria-label="Default analytics"] [data-count]');window.defaultButton=document.querySelector('[aria-label="Default analytics"] [data-increment]')});
 await board(page,'Chart').click();await expect(page).toHaveTitle('Chart board — Next.go');await expect(count(page,'Analytics shell')).toHaveText('41');await expect(count(page,'Chart analytics')).toHaveText('5');
 expect(await page.evaluate(()=>window.defaultLayout===document.querySelector('[data-analytics-shell]'))).toBe(true);expect(await page.evaluate(()=>{window.defaultButton.click();return window.defaultCount.textContent})).toBe('2');
 await page.goBack();await expect(page).toHaveTitle('Parallel boards — Next.go');await expect(count(page,'Default analytics')).toHaveText('2');await expect(count(page,'Analytics shell')).toHaveText('41');
 await page.goForward();await expect(page).toHaveTitle('Chart board — Next.go');await expect(count(page,'Analytics shell')).toHaveText('41');expect(await page.evaluate(()=>window.defaultLayout===document.querySelector('[data-analytics-shell]'))).toBe(true);
});
test('named defaults use local error boundaries and recover without replacing their layout islands',async({page})=>{
 await page.goto('/boards');await ready(page);await page.getByRole('button',{name:'Increase Analytics shell',exact:true}).click();await expect(count(page,'Analytics shell')).toHaveText('41');
 await page.evaluate(()=>{window.errorShell=document.querySelector('[data-analytics-shell]');window.errorDocument=true});await page.setExtraHTTPHeaders({'X-Demo-Default-Failure':'error'});
 const response=page.waitForResponse(r=>r.url().endsWith('/boards/settings')&&r.request().headers()['x-nextgo-navigate']==='1');await board(page,'Settings').click();expect((await response).status()).toBe(500);await expect(page.getByRole('alert')).toHaveText('Analytics could not be loaded. Navigate again to retry.');await expect(count(page,'Analytics shell')).toHaveText('41');await expect(count(page,'Team settings')).toHaveText('7');
 expect(await page.evaluate(()=>window.errorShell===document.querySelector('[data-analytics-shell]'))).toBe(true);
 await page.setExtraHTTPHeaders({});await board(page,'Overview').click();await expect(page).toHaveTitle('Parallel boards — Next.go');await expect(count(page,'Default analytics')).toHaveText('2');await expect(count(page,'Analytics shell')).toHaveText('41');expect(await page.evaluate(()=>window.errorDocument)).toBe(true);
});
test('named defaults render local not-found HTML inside their own layout',async({page})=>{
 const response=await page.goto('/boards/settings');expect(response.status()).toBe(200);await ready(page);
 await page.setExtraHTTPHeaders({'X-Demo-Default-Failure':'missing'});const missing=await page.reload();expect(missing.status()).toBe(404);await ready(page);
 await expect(page.getByText('Analytics is unavailable for this selection.',{exact:true})).toBeVisible();await expect(count(page,'Analytics shell')).toHaveText('40');await expect(count(page,'Team settings')).toHaveText('7');await expect(page).toHaveTitle('Settings board — Next.go');
});
test('named default metadata reauthorizes current requests and disposes retained layouts after denial',async({page})=>{
 await page.goto('/boards');await ready(page);await page.evaluate(()=>{window.deniedDefaultCounter=document.querySelector('[aria-label="Default analytics"] [data-count]');window.deniedDefaultButton=document.querySelector('[aria-label="Default analytics"] [data-increment]');window.deniedShellCounter=document.querySelector('[aria-label="Analytics shell"] [data-count]');window.deniedShellButton=document.querySelector('[aria-label="Analytics shell"] [data-increment]')});
 await page.setExtraHTTPHeaders({'X-Demo-Analytics-Metadata-Deny':'yes'});await board(page,'Settings').click();await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');await expect(page.locator('nextgo-slot')).toHaveCount(0);
 expect(await page.evaluate(()=>{window.deniedDefaultButton.click();window.deniedShellButton.click();return [window.deniedDefaultCounter.textContent,window.deniedShellCounter.textContent]})).toEqual(['2','40']);
});

test('nested slot-only URLs render complete defaults before WASM and hydrate their original nodes',async({page})=>{
 let release;const gate=new Promise(resolve=>{release=resolve});await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
 await page.goto('/boards/drill',{waitUntil:'domcontentloaded'});await expect(count(page,'Primary default')).toHaveText('1');await expect(count(page,'Default analytics')).toHaveText('2');await expect(count(page,'Detail drill')).toHaveText('15');await expect(count(page,'Detail shell')).toHaveText('60');
 await page.evaluate(()=>{window.nestedNodes=[...document.querySelectorAll('nextgo-slot')];window.nestedTexts=[...document.querySelectorAll('[data-count]')].map(n=>n.firstChild)});release();await ready(page);
 expect(await page.evaluate(()=>window.nestedNodes.every(n=>n.isConnected)&&window.nestedTexts.every(n=>n.isConnected)&&[...document.querySelectorAll('nextgo-slot')].every(n=>(n.getAttribute('data-nextgo-slot-parent')||'')===(n.parentElement.closest('nextgo-slot')?.getAttribute('data-nextgo-slot')||'')))).toBe(true);
});
test('nested matching and unmatched branches retain independent Go state through history',async({page})=>{
 await page.goto('/boards/chart');await ready(page);for(const label of ['Primary chart','Chart analytics','Analytics shell','Detail default','Detail shell'])await page.getByRole('button',{name:'Increase '+label,exact:true}).click();
 await page.evaluate(()=>{window.nestedDocument=true;window.oldDetailCounter=document.querySelector('[aria-label="Detail default"] [data-count]');window.oldDetailButton=document.querySelector('[aria-label="Detail default"] [data-increment]')});
 await board(page,'Drill').click();await expect(page).toHaveURL(/\/boards\/drill$/);await expect(count(page,'Primary chart')).toHaveText('13');await expect(count(page,'Chart analytics')).toHaveText('6');await expect(count(page,'Analytics shell')).toHaveText('41');await expect(count(page,'Detail drill')).toHaveText('15');await expect(count(page,'Detail shell')).toHaveText('61');
 expect(await page.evaluate(()=>{window.oldDetailButton.click();return window.oldDetailCounter.textContent})).toBe('12');await page.getByRole('button',{name:'Increase Detail drill',exact:true}).click();
 await board(page,'Settings').click();await expect(page).toHaveTitle('Settings board — Next.go');await expect(count(page,'Chart analytics')).toHaveText('6');await expect(count(page,'Detail drill')).toHaveText('16');await expect(count(page,'Detail shell')).toHaveText('61');
 await page.goBack();await expect(page).toHaveTitle('Chart board — Next.go');await expect(count(page,'Chart analytics')).toHaveText('6');await expect(count(page,'Detail drill')).toHaveText('16');await page.goBack();await expect(count(page,'Detail default')).toHaveText('11');await expect(count(page,'Detail shell')).toHaveText('61');expect(await page.evaluate(()=>window.nestedDocument)).toBe(true);
});
test('a revalidated nested child changes without replacing its retained parent page or layouts',async({page})=>{
 await page.goto('/boards/drill');await ready(page);for(const label of ['Default analytics','Analytics shell','Detail drill','Detail shell'])await page.getByRole('button',{name:'Increase '+label,exact:true}).click();
 await page.evaluate(()=>{window.retainedAnalytics=document.querySelector('[aria-label="Default analytics"]');window.changedDetailCount=document.querySelector('[aria-label="Detail drill"] [data-count]');window.changedDetailButton=document.querySelector('[aria-label="Detail drill"] [data-increment]')});
 await page.setExtraHTTPHeaders({'X-Demo-Detail-Version':'updated'});await board(page,'Settings').click();await expect(page).toHaveTitle('Settings board — Next.go');await expect(count(page,'Default analytics')).toHaveText('3');await expect(count(page,'Analytics shell')).toHaveText('41');await expect(count(page,'Detail drill')).toHaveText('25');await expect(count(page,'Detail shell')).toHaveText('61');
 expect(await page.evaluate(()=>window.retainedAnalytics===document.querySelector('[aria-label="Default analytics"]'))).toBe(true);expect(await page.evaluate(()=>{window.changedDetailButton.click();return window.changedDetailCount.textContent})).toBe('16');
});
test('nested denial reauthorizes retained leaves and disposes every ancestor scope',async({page})=>{
 await page.goto('/boards/drill');await ready(page);await page.evaluate(()=>{window.deniedNested=[...document.querySelectorAll('[data-increment]')].map(b=>({b,count:b.closest('[data-nextgo-island]').querySelector('[data-count]'),value:b.closest('[data-nextgo-island]').querySelector('[data-count]').textContent}))});
 await page.setExtraHTTPHeaders({'X-Demo-Detail-Deny':'yes'});await board(page,'Settings').click();await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');await expect(page.locator('nextgo-slot')).toHaveCount(0);
 expect(await page.evaluate(()=>window.deniedNested.every(item=>{item.b.click();return item.count.textContent===item.value}))).toBe(true);
});
test('named slots inside a retained implicit primary preserve their page and leaf Go state',async({page})=>{
 await page.goto('/boards/reports');await ready(page);await page.getByRole('button',{name:'Increase Report page',exact:true}).click();await page.getByRole('button',{name:'Increase Report detail',exact:true}).click();
 await board(page,'Drill').click();await expect(page).toHaveURL(/\/boards\/drill$/);await expect(page).toHaveTitle('Reports board — Next.go');await expect(count(page,'Report page')).toHaveText('22');await expect(count(page,'Report detail')).toHaveText('32');await expect(count(page,'Detail drill')).toHaveText('15');
 await page.setExtraHTTPHeaders({'X-Demo-Report-Deny':'yes'});await board(page,'Activity').click();await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');await expect(page.locator('nextgo-slot')).toHaveCount(0);
});
test('a malformed nested parent is rejected before altering live DOM or history',async({page})=>{
 await page.goto('/boards/drill');await ready(page);await page.evaluate(()=>{window.validNested=document.querySelector('[aria-label="Detail drill"]')});
 await page.route('**/boards/settings',async route=>{if(!route.request().headers()['x-nextgo-navigate'])return route.continue();const response=await route.fetch();const data=await response.json();data.html=data.html.replace(/data-nextgo-slot-name="detail" data-nextgo-slot-parent="[^"]*"/,'data-nextgo-slot-name="detail" data-nextgo-slot-parent="'+'f'.repeat(64)+'"');await route.fulfill({response,json:data})});
 await board(page,'Settings').click();await expect(page.locator('#nextgo-status')).toContainText('Navigation failed');await expect(page).toHaveURL(/\/boards\/drill$/);expect(await page.evaluate(()=>window.validNested===document.querySelector('[aria-label="Detail drill"]'))).toBe(true);await page.getByRole('button',{name:'Increase Detail drill',exact:true}).click();await expect(count(page,'Detail drill')).toHaveText('16');
});

test('direct URLs compose both primary defaults without replacing SSR nodes during hydration',async({page})=>{
 let release;const gate=new Promise(resolve=>{release=resolve});await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
 await page.goto('/boards/reports/summary',{waitUntil:'domcontentloaded'});await expect(page).toHaveTitle('Default report — Next.go');await expect(count(page,'Report default')).toHaveText('25');await expect(count(page,'Report summary')).toHaveText('41');await expect(count(page,'Reports shell')).toHaveText('70');await expect(count(page,'Primary default')).toHaveCount(0);
 await page.evaluate(()=>{window.primaryChainNodes=[...document.querySelectorAll('[data-count]')]});release();await ready(page);expect(await page.evaluate(()=>window.primaryChainNodes.every(node=>node.isConnected))).toBe(true);
});
test('independent ancestor primary selections and layouts retain Go state across nested and outer slot URLs',async({page})=>{
 await page.goto('/boards/reports');await ready(page);for(const label of ['Report page','Report detail','Reports shell'])await page.getByRole('button',{name:'Increase '+label,exact:true}).click();
 await page.evaluate(()=>{window.chainDocument=true;window.chainPage=document.querySelector('[aria-label="Report page"]');window.chainOldDetail=document.querySelector('[aria-label="Report detail"] [data-count]');window.chainOldButton=document.querySelector('[aria-label="Report detail"] [data-increment]')});
 await board(page,'Summary').click();await expect(page).toHaveURL(/\/boards\/reports\/summary$/);await expect(page).toHaveTitle('Reports board — Next.go');await expect(count(page,'Report page')).toHaveText('22');await expect(count(page,'Report summary')).toHaveText('41');await expect(count(page,'Reports shell')).toHaveText('71');
 expect(await page.evaluate(()=>{window.chainOldButton.click();return window.chainOldDetail.textContent})).toBe('32');await page.getByRole('button',{name:'Increase Report summary',exact:true}).click();
 await board(page,'Drill').click();await expect(page).toHaveURL(/\/boards\/drill$/);await expect(page).toHaveTitle('Reports board — Next.go');await expect(count(page,'Report page')).toHaveText('22');await expect(count(page,'Report summary')).toHaveText('42');await expect(count(page,'Reports shell')).toHaveText('71');expect(await page.evaluate(()=>window.chainPage===document.querySelector('[aria-label="Report page"]'))).toBe(true);
 await page.goBack();await expect(page).toHaveURL(/\/boards\/reports\/summary$/);await expect(count(page,'Report page')).toHaveText('22');await expect(count(page,'Report summary')).toHaveText('42');await page.goBack();await expect(page).toHaveURL(/\/boards\/reports$/);await expect(count(page,'Report detail')).toHaveText('31');await expect(count(page,'Report page')).toHaveText('22');await expect(count(page,'Reports shell')).toHaveText('71');expect(await page.evaluate(()=>window.chainDocument)).toBe(true);
});
test('retained primary chains reauthorize nested side branches before preserving ancestor state',async({page})=>{
 await page.goto('/boards/reports');await ready(page);await board(page,'Summary').click();await expect(page).toHaveURL(/\/boards\/reports\/summary$/);
 await page.evaluate(()=>{window.deniedChain=[...document.querySelectorAll('[data-increment]')].map(b=>({b,count:b.closest('[data-nextgo-island]').querySelector('[data-count]'),value:b.closest('[data-nextgo-island]').querySelector('[data-count]').textContent}))});
 await page.setExtraHTTPHeaders({'X-Demo-Report-Summary-Deny':'yes'});await board(page,'Drill').click();await expect(page.getByRole('heading',{level:1})).toHaveText('Access denied');await expect(page.locator('nextgo-slot')).toHaveCount(0);expect(await page.evaluate(()=>window.deniedChain.every(item=>{item.b.click();return item.count.textContent===item.value}))).toBe(true);
});
