import { test, expect } from '@playwright/test';

const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const value=page=>page.locator('[data-optimistic-value]');
const pending=page=>page.locator('[data-optimistic-pending]');
const action='**/_nextgo/actions/optimistic-increment/v1';
const count=async page=>Number(await value(page).textContent());
const button=(page,name)=>page.getByRole('button',{name,exact:true});

test('optimistic data is useful before WASM and hydration preserves the original nodes',async({page})=>{
  let release;const gate=new Promise(resolve=>{release=resolve});
  await page.route('**/app.wasm',async route=>{await gate;await route.continue()});
  await page.goto('/optimistic',{waitUntil:'domcontentloaded'});
  await expect(page.getByRole('heading',{level:1})).toHaveText('Optimistic Go updates');
  await expect(value(page)).toHaveText(/^[0-9]+$/);await expect(pending(page)).toHaveText('No pending changes.');
  await expect(button(page,'Add one')).toBeDisabled();
  await page.evaluate(()=>{window.optimisticRoot=document.querySelector('[data-optimistic-value]');window.optimisticText=window.optimisticRoot.firstChild});
  release();await ready(page);
  expect(await page.evaluate(()=>window.optimisticRoot===document.querySelector('[data-optimistic-value]')&&window.optimisticText===window.optimisticRoot.firstChild)).toBe(true);
});

test('tentative changes render before confirmation and canonical server operations reconcile them',async({page})=>{
  await page.goto('/optimistic');await ready(page);const initial=await count(page);
  let release,started;const gate=new Promise(resolve=>{release=resolve});const sent=new Promise(resolve=>{started=resolve});
  await page.route(action,async route=>{started();await gate;await route.continue()});
  await button(page,'Add five').click();await sent;
  await expect(value(page)).toHaveText(String(initial+5));await expect(pending(page)).toHaveText('Saving 1 change…');
  await expect(value(page)).toHaveAttribute('aria-busy','true');await expect(button(page,'Cancel pending changes')).toBeEnabled();
  release();await expect(pending(page)).toHaveText('No pending changes.');await expect(value(page)).toHaveText(String(initial+2));
  await expect(value(page)).toHaveAttribute('aria-busy','false');await expect(button(page,'Cancel pending changes')).toBeDisabled();
  await page.reload();await ready(page);await expect(value(page)).toHaveText(String(initial+2));
});

test('typed transport stays sequential while optimistic state retains both intents',async({page})=>{
  await page.goto('/optimistic');await ready(page);const initial=await count(page);
  let releaseFirst,releaseSecond,startedFirst,startedSecond;let requests=0;
  const firstGate=new Promise(resolve=>{releaseFirst=resolve}),secondGate=new Promise(resolve=>{releaseSecond=resolve});
  const firstSent=new Promise(resolve=>{startedFirst=resolve}),secondSent=new Promise(resolve=>{startedSecond=resolve});
  await page.route(action,async route=>{const index=++requests,response=await route.fetch();if(index===1){startedFirst();await firstGate}else if(index===2){startedSecond();await secondGate}await route.fulfill({response})});
  try{
    await button(page,'Add one').click();await firstSent;
    await button(page,'Add five').click();await expect(pending(page)).toHaveText('Saving 2 changes…');await expect(value(page)).toHaveText(String(initial+6));expect(requests).toBe(1);
    releaseFirst();await secondSent;await expect(pending(page)).toHaveText('Saving 1 change…');await expect(value(page)).toHaveText(String(initial+6));expect(requests).toBe(2);
    releaseSecond();await expect(pending(page)).toHaveText('No pending changes.');await expect(value(page)).toHaveText(String(initial+3));
    await page.reload();await ready(page);await expect(value(page)).toHaveText(String(initial+3));
  }finally{releaseFirst();releaseSecond()}
});

test('a failed earlier intent rolls back without dropping the later successful operation',async({page})=>{
  await page.goto('/optimistic');await ready(page);const initial=await count(page);
  let releaseFirst,releaseSecond,startedFirst,startedSecond;let requests=0;
  const firstGate=new Promise(resolve=>{releaseFirst=resolve}),secondGate=new Promise(resolve=>{releaseSecond=resolve});
  const firstSent=new Promise(resolve=>{startedFirst=resolve}),secondSent=new Promise(resolve=>{startedSecond=resolve});
  await page.route(action,async route=>{const index=++requests,response=await route.fetch();if(index===1){startedFirst();await firstGate}else if(index===2){startedSecond();await secondGate}await route.fulfill({response})});
  try{
    await button(page,'Try a rejected change').click();await firstSent;await expect(value(page)).toHaveText(String(initial+1));
    await button(page,'Add five').click();await expect(pending(page)).toHaveText('Saving 2 changes…');await expect(value(page)).toHaveText(String(initial+6));expect(requests).toBe(1);
    releaseFirst();await secondSent;await expect(page.locator('[data-optimistic-error]')).toContainText('Choose an amount');
    await expect(pending(page)).toHaveText('Saving 1 change…');await expect(value(page)).toHaveText(String(initial+5));expect(requests).toBe(2);
    releaseSecond();await expect(pending(page)).toHaveText('No pending changes.');await expect(value(page)).toHaveText(String(initial+2));
  }finally{releaseFirst();releaseSecond()}
});

test('transport failure rolls back the view and a retry can succeed',async({page})=>{
  await page.goto('/optimistic');await ready(page);const initial=await count(page);
  await page.route(action,route=>route.abort('failed'));
  await button(page,'Add one').click();await expect(page.locator('[data-optimistic-error]')).toContainText('reconcile before retrying');
  await expect(value(page)).toHaveText(String(initial));await expect(pending(page)).toHaveText('No pending changes.');
  await page.unroute(action);await button(page,'Add one').click();await expect(value(page)).toHaveText(String(initial+1));
  await expect(pending(page)).toHaveText('No pending changes.');await expect(page.locator('[data-optimistic-error]')).toBeHidden();
});

test('cancellation rejects local intents and late responses cannot restore their overlays',async({page})=>{
  await page.goto('/optimistic');await ready(page);const initial=await count(page);
  let release,started,finished;const gate=new Promise(resolve=>{release=resolve});const sent=new Promise(resolve=>{started=resolve});const done=new Promise(resolve=>{finished=resolve});
  await page.route(action,async route=>{started();await gate;try{await route.abort('failed')}finally{finished()}});
  await button(page,'Add five').click();await sent;await expect(value(page)).toHaveText(String(initial+5));
  await button(page,'Cancel pending changes').click();await expect(value(page)).toHaveText(String(initial));await expect(pending(page)).toHaveText('No pending changes.');
  release();await done;await expect(value(page)).toHaveText(String(initial));
  await expect(page.locator('[data-optimistic-notice]')).toContainText('Refresh to reconcile');
});

test('an aborted response can follow a server commit and explicit refresh reconciles the true value',async({page})=>{
  await page.goto('/optimistic');await ready(page);const initial=await count(page);
  let release,committed,finished;const gate=new Promise(resolve=>{release=resolve});const saved=new Promise(resolve=>{committed=resolve});const done=new Promise(resolve=>{finished=resolve});
  await page.route(action,async route=>{const response=await route.fetch();committed();await gate;try{await route.fulfill({response})}finally{finished()}});
  await button(page,'Add one').click();await saved;
  await button(page,'Cancel pending changes').click();await expect(value(page)).toHaveText(String(initial));
  release();await done;await page.unroute(action);
  await button(page,'Refresh server value').click();await expect(value(page)).toHaveText(String(initial+1));await expect(pending(page)).toHaveText('No pending changes.');
});

test('leaving the island aborts requests, releases listeners and ignores a late confirmation',async({page})=>{
  await page.goto('/optimistic');await ready(page);
  let release,started,finished;const gate=new Promise(resolve=>{release=resolve});const sent=new Promise(resolve=>{started=resolve});const done=new Promise(resolve=>{finished=resolve});
  await page.route(action,async route=>{const response=await route.fetch();started();await gate;try{await route.fulfill({response})}finally{finished()}});
  await button(page,'Add one').click();await sent;
  await page.evaluate(()=>{window.disposedOptimistic=document.querySelector('[data-optimistic-value]');window.disposedOptimisticButton=document.querySelector('[data-optimistic-add]');window.disposedValue=window.disposedOptimistic.textContent});
  await page.getByRole('navigation',{name:'Main',exact:true}).getByRole('link',{name:'Architecture',exact:true}).click();await expect(page).toHaveURL(/\/about$/);
  release();await done;
  expect(await page.evaluate(()=>{window.disposedOptimisticButton.click();return[window.disposedOptimistic.isConnected,window.disposedOptimistic.textContent===window.disposedValue]})).toEqual([false,true]);
  await expect(page.locator('#nextgo-status')).toBeHidden();
});


test('optimistic Go mutations preserve mounted, slash and locale deployment scopes',async({page})=>{
  for(const path of ['http://127.0.0.1:4104/demo/optimistic','http://127.0.0.1:4106/slash/optimistic/','http://127.0.0.1:4120/shop/fr/optimistic','http://127.0.0.1:4122/domain/optimistic']){
    await page.goto(path);await ready(page);const initial=await count(page);
    await page.evaluate(()=>{window.optimisticPrefixDocument=true});
    await button(page,'Add five').click();await expect(pending(page)).toHaveText('No pending changes.');
    await expect(value(page)).toHaveText(String(initial+2));await expect(page).toHaveURL(path);
    expect(await page.evaluate(()=>window.optimisticPrefixDocument)).toBe(true);
    await page.reload();await ready(page);await expect(value(page)).toHaveText(String(initial+2));
  }
});


test('the pending-intent limit disables new changes and cancellation releases its capacity',async({page})=>{
  await page.goto('/optimistic');await ready(page);const initial=await count(page);
  let release,started,finished;let requests=0;const gate=new Promise(resolve=>{release=resolve}),sent=new Promise(resolve=>{started=resolve}),done=new Promise(resolve=>{finished=resolve});
  await page.route(action,async route=>{requests++;started();await gate;try{await route.abort('failed')}finally{finished()}});
  try{
    for(let i=0;i<8;i++)await button(page,'Add one').click();await sent;
    await expect(pending(page)).toHaveText('Saving 8 changes…');await expect(value(page)).toHaveText(String(initial+8));
    await expect(button(page,'Add one')).toBeDisabled();await expect(button(page,'Add five')).toBeDisabled();
    await button(page,'Add one').evaluate(node=>node.click());expect(requests).toBe(1);
    await button(page,'Cancel pending changes').click();release();await done;
    await expect(pending(page)).toHaveText('No pending changes.');await expect(value(page)).toHaveText(String(initial));await expect(button(page,'Add one')).toBeEnabled();expect(requests).toBe(1);
  }finally{release()}
});


test('progressive optimistic-demo forms work without JavaScript at root and scoped deployments',async({browser})=>{
  const context=await browser.newContext({javaScriptEnabled:false});
  try{
    const page=await context.newPage();
    for(const path of ['http://127.0.0.1:4100/optimistic','http://127.0.0.1:4104/demo/optimistic','http://127.0.0.1:4106/slash/optimistic/','http://127.0.0.1:4120/shop/fr/optimistic']){
      await page.goto(path);const initial=await count(page);
      await expect(button(page,'Add one')).toBeDisabled();
      await button(page,'Submit one on the server').click();await expect(page).toHaveURL(path);
      await expect(value(page)).toHaveText(String(initial+1));await expect(pending(page)).toHaveText('No pending changes.');
    }
  }finally{await context.close()}
});
