import { test, expect } from '@playwright/test';
const ready=page=>expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration','ready');
const endpoint='**/_nextgo/actions/optimistic-increment/v1';
const value=page=>page.locator('[data-optimistic-value]');

for(const poison of ['missing protocol','wrong revision','unknown output','duplicate output','wrong output type','unsafe redirect']){
  test(`typed mutations reject ${poison} without adopting an incompatible result`,async({page})=>{
    await page.goto('/optimistic');await ready(page);const initial=await value(page).textContent();
    await page.route(endpoint,async route=>{
      const response=await route.fetch();const headers={...response.headers()};let body=await response.text();
      if(poison==='missing protocol')delete headers['x-nextgo-action-protocol'];
      if(poison==='wrong revision')headers['x-nextgo-action-revision']='v0';
      if(poison==='unknown output')body=JSON.stringify({data:{applied:1,count:999,secret:true}});
      if(poison==='duplicate output')body='{"data":{"applied":1,"applied":2,"count":999}}';
      if(poison==='wrong output type')body=JSON.stringify({data:{applied:'wrong',count:999}});
      if(poison==='unsafe redirect')body=JSON.stringify({data:{applied:1,count:999},redirect:'https://foreign.test/'});
      await route.fulfill({response,headers,body});
    });
    await page.getByRole('button',{name:'Add one',exact:true}).click();
    await expect(page.locator('[data-optimistic-error]')).toContainText('incompatible mutation response');
    await expect(value(page)).toHaveText(initial);await expect(page.locator('[data-optimistic-pending]')).toHaveText('No pending changes.');
    await expect(page.locator('[data-optimistic-notice]')).toContainText('Refresh to reconcile');
  });
}

test('typed POST redirects never forward their body to a foreign endpoint',async({page,request})=>{
  const probe=await request.post('http://127.0.0.1:4132/_nextgo/actions/optimistic-increment/v1',{data:{amount:1},maxRedirects:0});
  expect(probe.status()).toBe(307);expect(probe.headers().location).toBe('http://127.0.0.1:4133/typed-forward-probe');
  await page.goto('http://127.0.0.1:4132/optimistic');await ready(page);const initial=await value(page).textContent();
  const baseline=await (await request.get('http://127.0.0.1:4132/_e2e/forwarded')).json();
  // A control request proves this browser really can forward the JSON body.
  // Otherwise a CORS/preflight failure could hide a missing redirect policy.
  const control=await page.evaluate(async()=>{
    const response=await fetch('/_nextgo/actions/optimistic-increment/v1',{method:'POST',credentials:'same-origin',redirect:'follow',headers:{'Content-Type':'application/json','X-Nextgo-Action-Protocol':'1','X-Nextgo-Action-Revision':'v1'},body:JSON.stringify({amount:1})});
    return response.text();
  });
  expect(control).toBe('unexpected forwarded request');
  const before=await (await request.get('http://127.0.0.1:4132/_e2e/forwarded')).json();expect(before.forwarded).toBe(baseline.forwarded+1);
  await page.getByRole('button',{name:'Add one',exact:true}).click();await expect(page.locator('[data-optimistic-error]')).toContainText('mutation transport failed');
  await expect(value(page)).toHaveText(initial);
  const after=await (await request.get('http://127.0.0.1:4132/_e2e/forwarded')).json();expect(after.forwarded).toBe(before.forwarded);
});

test('an injected document base cannot move a typed mutation away from the application origin',async({page})=>{
  await page.goto('/optimistic');await ready(page);const initial=Number(await value(page).textContent());let target='';
  await page.route(endpoint,async route=>{target=route.request().url();await route.continue()});
  await page.evaluate(()=>{const base=document.createElement('base');base.href='https://foreign.test/';document.head.prepend(base)});
  await page.getByRole('button',{name:'Add one',exact:true}).click();await expect(value(page)).toHaveText(String(initial+1));
  await expect(page.locator('[data-optimistic-pending]')).toHaveText('No pending changes.');expect(target).toBe('http://127.0.0.1:4100/_nextgo/actions/optimistic-increment/v1');
});
