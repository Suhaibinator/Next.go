import {test, expect} from '@playwright/test';
import {randomUUID} from 'node:crypto';

const home = '/forms-demo/';
const root = (page, instance = 'primary') => page.locator(`[data-fv-instance="${instance}"]`);
const field = (scope, name) => scope.locator(`[data-fv-field="${name}"]`);
const indices = scope => scope.locator('[data-fv-option-indices]');
const phase = (scope, name) => scope.locator(`[data-fv-validation="${name}"]`);
const error = (scope, name) => scope.locator(`[data-fv-error="${name}"]`);
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const isPreview = response => new URL(response.url()).pathname === '/forms-demo/api/preview' && response.request().method() === 'POST';
const control = (operation, key = '') => `/_fixture/form-validation/${operation}?${new URLSearchParams({key})}`;
const headers = (trace, extra = {}) => ({'X-Fixture-Form-Trace': trace, ...extra});
async function records(request, trace) {return (await (await request.get(control('records'))).json()).filter(record => record.trace === trace);}
async function held(request, key, kind) {
  await expect.poll(async () => {const value = await (await request.get(control('gate', key))).json(); return value.held && value.kind;}).toBe(kind);
  return (await request.get(control('gate', key))).json();
}
async function select(scope, selected) {await scope.locator('[data-fv-topics]').selectOption(selected.map(index => ({index})));}
async function selected(scope) {return scope.locator('[data-fv-topics]').evaluate(node => [...node.options].flatMap((option, index) => option.selected ? [index] : []));}
async function preview(page, scope) {
  const waiting = page.waitForResponse(isPreview);
  await scope.locator('[data-fv-submit]').click();
  const response = await waiting;
  expect(response.status()).toBe(200);
  expect(response.headers()['content-type']).toContain('application/json');
  return response.json();
}
async function trigger(scope, name, value) {
  // One event-loop turn prevents automation IPC latency from accidentally
  // crossing the sample's debounce interval before the explicit Check action.
  await scope.evaluate((node, {name, value}) => {
    if (value !== undefined) {
      const input = node.querySelector(`[data-fv-field="${name}"]`);
      input.value = value; input.dispatchEvent(new InputEvent('input', {bubbles: true, inputType: 'insertText', data: value}));
    }
    node.querySelector(`[data-fv-check="${name}"]`).click();
  }, {name, value});
}
async function aborted(request, trace, id) {await expect.poll(async () => (await records(request, trace)).find(record => record.id === id)?.aborted).toBe(true);}
async function valid(scope, name) {await expect(phase(scope, name)).toHaveAttribute('data-state', 'valid'); await expect(error(scope, name)).toBeEmpty();}

test('native forms render useful controls and submit empty radio presence and repeated values without JavaScript', async ({browser, baseURL}) => {
  const context = await browser.newContext({javaScriptEnabled: false, baseURL});
  try {
    const page = await context.newPage();
    expect((await page.goto(home)).status()).toBe(200);
    await expect(field(root(page), 'name')).toHaveValue('Ada');
    await expect(root(page).locator('[data-fv-radio="empty"]')).toBeChecked();
    await expect(root(page, 'secondary').locator('[data-fv-radio="empty"]')).not.toBeChecked();
    await expect(root(page).locator('[data-fv-check="name"]')).toBeDisabled();
    const first = page.waitForResponse(isPreview);
    await root(page).locator('[data-fv-native]').click();
    expect((await first).status()).toBe(200);
    await expect(page.locator('[data-fv-receipt]')).toBeVisible();
    await expect(page.locator('[data-fv-native-channel]')).toHaveAttribute('data-present', 'true');
    await expect(page.locator('[data-fv-native-channel]')).toHaveText('Selected empty value');
    await expect(page.locator('[data-fv-native-values]')).toHaveText('["notes"]');
    await page.goto(home);
    await select(root(page, 'secondary'), [0, 1, 3]);
    const second = page.waitForResponse(isPreview);
    await root(page, 'secondary').locator('[data-fv-native]').click();
    expect((await second).status()).toBe(200);
    await expect(page.locator('[data-fv-native-channel]')).toHaveAttribute('data-present', 'false');
    await expect(page.locator('[data-fv-native-channel]')).toHaveText('No selection');
    await expect(page.locator('[data-fv-native-values]')).toHaveText('["notes","notes",""]');
  } finally {await context.close();}
});

test('delayed genuine WASM adopts preinput and the exact duplicate option nodes without replacing SSR', async ({page, request}) => {
  const trace = randomUUID(), key = `${trace}-wasm`;
  await page.setExtraHTTPHeaders(headers(trace, {'X-Fixture-Form-Wasm': key}));
  try {
    await page.goto(home, {waitUntil: 'commit'});
    await held(request, key, 'wasm');
    await expect(field(root(page), 'name')).toHaveValue('Ada');
    await page.evaluate(() => {
      const scope = document.querySelector('[data-fv-instance="primary"]');
      window.fvBefore = {scope, input: scope.querySelector('[data-fv-field="name"]'), select: scope.querySelector('[data-fv-topics]')};
      window.fvBefore.options = [...window.fvBefore.select.options];
      window.fvBefore.input.value = ' Grace ';
      scope.querySelector('[data-fv-radio="email"]').checked = true;
      [...window.fvBefore.select.options].forEach((option, index) => {option.selected = index === 1;});
    });
    expect((await request.post(control('release', key))).status()).toBe(200);
    await ready(page);
    expect(await page.evaluate(() => {
      const before = window.fvBefore, scope = document.querySelector('[data-fv-instance="primary"]');
      return scope === before.scope && scope.querySelector('[data-fv-field="name"]') === before.input && scope.querySelector('[data-fv-topics]') === before.select && [...before.select.options].every((option, index) => option === before.options[index]);
    })).toBe(true);
    await expect(field(root(page), 'name')).toHaveValue(' Grace ');
    await expect(root(page).locator('[data-fv-channel-state]')).toHaveText('Selected email');
    await expect(indices(root(page))).toHaveText('[1]');
    expect(await selected(root(page))).toEqual([1]);
    expect((await records(request, trace)).filter(record => record.path.endsWith('/api/validate'))).toHaveLength(0);
    const result = await preview(page, root(page));
    expect(result.value.topics).toEqual([1]);
    expect(result.value.channel).toEqual({Value: 'email', Selected: true});
    await expect(field(root(page), 'name')).toHaveValue('Grace');
    await expect(root(page).locator('[data-fv-confirmed-indices]')).toHaveText('[1]');
    expect(await page.evaluate(() => window.fvBefore.options[1] === document.querySelector('[data-fv-instance="primary"] [data-fv-topics]').options[1] && window.fvBefore.options[1].selected && !window.fvBefore.options[0].selected)).toBe(true);
  } finally {await request.post(control('release', key));}
});

test('Go distinguishes a selected empty radio value from an unselected group in real JSON previews', async ({page}) => {
  await page.goto(home); await ready(page);
  const scope = root(page);
  await expect(scope.locator('[data-fv-channel-state]')).toHaveText('Selected empty value');
  await scope.locator('[data-fv-clear-radio]').click();
  await expect(scope.locator('[data-fv-channel-state]')).toHaveText('No selection');
  expect(await scope.locator('[data-fv-form]').evaluate(node => new FormData(node).has('channel'))).toBe(false);
  const absent = await preview(page, scope);
  expect(absent.value.channel).toEqual({Value: '', Selected: false});
  await scope.locator('[data-fv-radio="empty"]').check();
  await expect(scope.locator('[data-fv-channel-state]')).toHaveText('Selected empty value');
  expect(await scope.locator('[data-fv-form]').evaluate(node => new FormData(node).getAll('channel'))).toEqual(['']);
  const empty = await preview(page, scope);
  expect(empty.value.channel).toEqual({Value: '', Selected: true});
});

test('equal-valued options retain exact index identity through Go confirmation and reset while native POST repeats values', async ({page}) => {
  await page.goto(home); await ready(page);
  await page.evaluate(() => {window.fvOptions = [...document.querySelector('[data-fv-instance="primary"] [data-fv-topics]').options];});
  const scope = root(page);
  await select(scope, [1]); await expect(indices(scope)).toHaveText('[1]');
  expect((await preview(page, scope)).value.topics).toEqual([1]);
  await select(scope, [0, 1]); await expect(indices(scope)).toHaveText('[0,1]');
  await expect(scope.locator('[data-fv-option-values]')).toHaveText('["notes","notes"]');
  expect((await preview(page, scope)).value.topics).toEqual([0, 1]);
  await select(scope, [2]); await scope.locator('[data-fv-reset]').click();
  await expect(indices(scope)).toHaveText('[0,1]'); expect(await selected(scope)).toEqual([0, 1]);
  expect(await page.evaluate(() => [...document.querySelector('[data-fv-instance="primary"] [data-fv-topics]').options].every((node, index) => node === window.fvOptions[index]))).toBe(true);
  expect(await scope.locator('[data-fv-form]').evaluate(node => new FormData(node).getAll('topics'))).toEqual(['notes', 'notes']);
  const native = page.waitForResponse(isPreview);
  await scope.locator('[data-fv-native]').click(); expect((await native).status()).toBe(200);
  await expect(page.locator('[data-fv-native-values]')).toHaveText('["notes","notes"]');
});

test('two real forms isolate native radio grouping, duplicate choices and resets', async ({page}) => {
  await page.goto(home); await ready(page);
  const first = root(page), second = root(page, 'secondary');
  await first.locator('[data-fv-radio="email"]').check();
  await second.locator('[data-fv-radio="empty"]').check();
  await expect(first.locator('[data-fv-radio="email"]')).toBeChecked();
  await expect(second.locator('[data-fv-radio="empty"]')).toBeChecked();
  await select(first, [0, 1]); await select(second, [1, 3]);
  await first.locator('[data-fv-reset]').click();
  await expect(indices(first)).toHaveText('[0]'); await expect(indices(second)).toHaveText('[1,3]');
  await expect(first.locator('[data-fv-radio="empty"]')).toBeChecked();
  await expect(second.locator('[data-fv-radio="empty"]')).toBeChecked();
  await expect(field(second, 'name')).toHaveValue('Lin');
});

test('local invalid fields render accessible errors and prevent the preview POST', async ({page, request}) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(headers(trace));
  await page.goto(home); await ready(page);
  const scope = root(page);
  // Capture also covers autofill-style property changes without input events.
  // No debounced check was scheduled; this explicit local action does no I/O.
  await scope.evaluate(node => {node.querySelector('[data-fv-field="name"]').value = ''; node.querySelector('[data-fv-field="code"]').value = '1';});
  await scope.locator('[data-fv-local-validate]').click();
  await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'invalid');
  await expect(phase(scope, 'code')).toHaveAttribute('data-state', 'invalid');
  // Validate evaluates the model without touching fields. Bound alerts are
  // intentionally hidden until a field is touched or a real submit occurs.
  await expect(error(scope, 'name')).toBeEmpty(); await expect(error(scope, 'code')).toBeEmpty();
  expect((await records(request, trace)).filter(record => record.path.endsWith('/api/validate'))).toHaveLength(0);
  await scope.locator('[data-fv-submit]').click();
  await expect(error(scope, 'name')).toHaveText('Use a public name between 2 and 40 bytes.');
  await expect(error(scope, 'code')).toHaveText('Use letters or hyphens for the public code.');
  await expect(field(scope, 'name')).toHaveAttribute('aria-invalid', 'true');
  expect((await records(request, trace)).filter(record => record.path.endsWith('/api/preview'))).toHaveLength(0);
  await expect(error(root(page, 'secondary'), 'name')).toBeEmpty();
});

test('real asynchronous validation exposes pending, known rejection and unavailable errors with explicit retry', async ({page, request}) => {
  const trace = randomUUID(), key = `${trace}-pending`;
  await page.setExtraHTTPHeaders(headers(trace)); await page.goto(home); await ready(page);
  const scope = root(page);
  try {
    await page.setExtraHTTPHeaders(headers(trace, {'X-Fixture-Form-Gate': key}));
    await trigger(scope, 'name', 'reserved');
    const flight = await held(request, key, 'validation');
    await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'pending');
    await expect(scope.locator('[data-fv-submit]')).toBeDisabled();
    await expect(error(scope, 'name')).toBeEmpty();
    expect((await records(request, trace)).find(record => record.id === flight.id)).toMatchObject({field: 'name', value: 'reserved', status: 200});
    expect((await request.post(control('release', key))).status()).toBe(200);
    await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'invalid');
    await expect(error(scope, 'name')).toHaveText('This public name is reserved.');
    await page.setExtraHTTPHeaders(headers(trace));
    await trigger(scope, 'name', 'offline');
    await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'error');
    await expect(phase(scope, 'name')).toHaveText('Check unavailable; retry explicitly.');
    expect((await records(request, trace)).filter(record => record.value === 'offline' && record.status === 503)).toHaveLength(1);
    await scope.locator('[data-fv-retry="name"]').evaluate(node => node.click());
    await expect.poll(async () => (await records(request, trace)).filter(record => record.value === 'offline' && record.status === 503).length).toBe(2);
    await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'error');
    await trigger(scope, 'name', 'Available'); await valid(scope, 'name');
    expect((await records(request, trace)).filter(record => record.path.endsWith('/api/validate'))).toHaveLength(4);
  } finally {await request.post(control('release', key));}
});

test('a newer validation cancels the exact held old request without publishing its field error', async ({page, request}) => {
  const trace = randomUUID(), key = `${trace}-older`;
  await page.setExtraHTTPHeaders(headers(trace)); await page.goto(home); await ready(page);
  const scope = root(page);
  try {
    await page.setExtraHTTPHeaders(headers(trace, {'X-Fixture-Form-Gate': key}));
    await trigger(scope, 'name', 'reserved'); const old = await held(request, key, 'validation');
    await page.setExtraHTTPHeaders(headers(trace));
    await trigger(scope, 'name', 'Current');
    await aborted(request, trace, old.id); await valid(scope, 'name');
    await request.post(control('release', key));
    await expect(field(scope, 'name')).toHaveValue('Current'); await expect(error(scope, 'name')).toBeEmpty();
    const actual = (await records(request, trace)).filter(record => record.path.endsWith('/api/validate'));
    expect(actual.map(record => record.value)).toEqual(['reserved', 'Current']);
    expect(actual[1].status).toBe(200);
  } finally {await request.post(control('release', key));}
});

test('unchanged local errors override a held success and reset cancels a later pending check', async ({page, request}) => {
  const trace = randomUUID(), firstKey = `${trace}-local`, resetKey = `${trace}-reset`;
  await page.setExtraHTTPHeaders(headers(trace)); await page.goto(home); await ready(page);
  const scope = root(page);
  try {
    await page.setExtraHTTPHeaders(headers(trace, {'X-Fixture-Form-Gate': firstKey}));
    await trigger(scope, 'name', ''); const first = await held(request, firstKey, 'validation');
    await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'pending');
    // The value is unchanged: explicit synchronous validation must fence the
    // already-started success, rather than relying only on input revisions.
    await scope.locator('[data-fv-local-validate]').click();
    await expect(error(scope, 'name')).toHaveText('Use a public name between 2 and 40 bytes.');
    await aborted(request, trace, first.id);
    await request.post(control('release', firstKey));
    await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'invalid');
    await scope.locator('[data-fv-reset]').click();
    await expect(field(scope, 'name')).toHaveValue('Ada'); await expect(error(scope, 'name')).toBeEmpty();
    await page.setExtraHTTPHeaders(headers(trace, {'X-Fixture-Form-Gate': resetKey}));
    await trigger(scope, 'name', 'reserved'); const second = await held(request, resetKey, 'validation');
    await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'pending');
    await scope.locator('[data-fv-reset]').click();
    await aborted(request, trace, second.id);
    await page.setExtraHTTPHeaders(headers(trace)); await trigger(scope, 'code'); await valid(scope, 'code');
    await request.post(control('release', resetKey));
    await expect(phase(scope, 'name')).toHaveAttribute('data-state', 'idle');
    await expect(field(scope, 'name')).toHaveValue('Ada'); await expect(error(scope, 'name')).toBeEmpty();
  } finally {await request.post(control('release', firstKey)); await request.post(control('release', resetKey));}
});

test('navigation aborts a held validator and detached controls cannot issue requests or update old markup', async ({page, request}) => {
  const trace = randomUUID(), key = `${trace}-unmount`;
  await page.setExtraHTTPHeaders(headers(trace)); await page.goto(home); await ready(page);
  try {
    await page.evaluate(() => {window.fvDocument = true; window.fvOld = document.querySelector('[data-fv-instance="primary"]');});
    await page.setExtraHTTPHeaders(headers(trace, {'X-Fixture-Form-Gate': key}));
    await trigger(root(page), 'name', 'reserved'); const old = await held(request, key, 'validation');
    await page.getByRole('link', {name: 'Leave forms', exact: true}).click();
    await expect(page).toHaveTitle('Form lifecycle — Next.go'); await ready(page);
    await aborted(request, trace, old.id);
    expect(await page.evaluate(() => window.fvDocument && !window.fvOld.isConnected && window.fvOld.getAttribute('data-fv-closed') === 'true')).toBe(true);
    // Snapshot only after disposal; connected callbacks may legitimately have
    // updated the old view while navigation was still pending.
    await page.evaluate(() => {window.fvOldHTML = window.fvOld.outerHTML; const input = window.fvOld.querySelector('[data-fv-field="name"]'); input.value = 'Detached'; input.dispatchEvent(new InputEvent('input', {bubbles: true})); window.fvOld.querySelector('[data-fv-check="name"]').click();});
    await request.post(control('release', key));
    await page.setExtraHTTPHeaders(headers(trace));
    await page.getByRole('link', {name: 'Return to form previews', exact: true}).click(); await ready(page);
    await trigger(root(page), 'code'); await valid(root(page), 'code');
    expect(await page.evaluate(() => window.fvOld.outerHTML === window.fvOldHTML)).toBe(true);
    expect((await records(request, trace)).filter(record => record.value === 'Detached')).toHaveLength(0);
    await expect(field(root(page), 'name')).toHaveValue('Ada');
  } finally {await request.post(control('release', key));}
});

test('a burst of real input events debounces into one latest public validation request', async ({page, request}) => {
  const trace = randomUUID(), key = `${trace}-debounce`;
  await page.setExtraHTTPHeaders(headers(trace)); await page.goto(home); await ready(page);
  try {
    expect((await records(request, trace)).filter(record => record.path.endsWith('/api/validate'))).toHaveLength(0);
    await page.setExtraHTTPHeaders(headers(trace, {'X-Fixture-Form-Gate': key}));
    await root(page).evaluate(node => {
      const input = node.querySelector('[data-fv-field="name"]');
      for (const value of ['Earlier', 'reserved', 'Final']) {input.value = value; input.dispatchEvent(new InputEvent('input', {bubbles: true, inputType: 'insertText', data: value}));}
    });
    const flight = await held(request, key, 'validation');
    await expect(phase(root(page), 'name')).toHaveAttribute('data-state', 'pending');
    const actual = (await records(request, trace)).filter(record => record.path.endsWith('/api/validate'));
    expect(actual).toHaveLength(1); expect(actual[0]).toMatchObject({id: flight.id, field: 'name', value: 'Final', status: 200});
    expect((await request.post(control('release', key))).status()).toBe(200);
    await valid(root(page), 'name'); await expect(field(root(page), 'name')).toHaveValue('Final');
  } finally {await request.post(control('release', key));}
});

test('reset and unmount retire scheduled timers while a fresh independent debounce still works', async ({page, request}) => {
  const trace = randomUUID(); await page.setExtraHTTPHeaders(headers(trace));
  await page.goto(home); await ready(page);
  await page.evaluate(() => {
    window.fvFetchOriginal = window.fetch; window.fvFetchStarts = []; window.fvFetchOverflow = false;
    window.fetch = function(...args) {
      const url = new URL(args[0] instanceof Request ? args[0].url : String(args[0]), location.href);
      if (url.pathname === '/forms-demo/api/validate') {
        if (window.fvFetchStarts.length === 32) window.fvFetchOverflow = true;
        else window.fvFetchStarts.push({value: (url.searchParams.get('value') || '').slice(0, 128), closed: window.fvScheduled?.getAttribute('data-fv-closed') === 'true'});
      }
      // Passive invocation ownership only: identical arguments, AbortSignal,
      // native promise and response bytes reach the real application server.
      return Reflect.apply(window.fvFetchOriginal, this, args);
    };
  });
  try {
    await root(page).evaluate(node => {
      const input = node.querySelector('[data-fv-field="name"]');
      input.value = 'Scheduled-reset'; input.dispatchEvent(new InputEvent('input', {bubbles: true}));
      node.querySelector('[data-fv-reset]').click();
    });
    await expect(field(root(page), 'name')).toHaveValue('Ada');
    await page.evaluate(() => {
      window.fvScheduled = document.querySelector('[data-fv-instance="primary"]');
      const input = window.fvScheduled.querySelector('[data-fv-field="name"]');
      input.value = 'Scheduled-unmount'; input.dispatchEvent(new InputEvent('input', {bubbles: true}));
      document.querySelector('a[href="/forms-demo/details"]').click();
    });
    await expect(page).toHaveTitle('Form lifecycle — Next.go'); await ready(page);
    expect(await page.evaluate(() => !window.fvScheduled.isConnected && window.fvScheduled.getAttribute('data-fv-closed') === 'true')).toBe(true);
    await page.getByRole('link', {name: 'Return to form previews', exact: true}).click(); await ready(page);
    await root(page, 'secondary').evaluate(node => {const input = node.querySelector('[data-fv-field="code"]'); input.value = 'FRESH'; input.dispatchEvent(new InputEvent('input', {bubbles: true}));});
    // Code's350ms debounce was scheduled later than name's250ms timer. This
    // genuine completion crosses the old deadline without an arbitrary sleep.
    await valid(root(page, 'secondary'), 'code');
    const actual = (await records(request, trace)).filter(record => record.path.endsWith('/api/validate'));
    expect(actual.filter(record => record.value === 'Scheduled-reset')).toHaveLength(0);
    expect(actual.filter(record => record.value === 'FRESH')).toHaveLength(1);
    // An API request can arrive at the proxy after disposal even when Fetch
    // began while connected. Observe ownership at native Fetch invocation.
    const starts = await page.evaluate(() => ({values: window.fvFetchStarts, overflow: window.fvFetchOverflow}));
    expect(starts.overflow).toBe(false);
    expect(starts.values.filter(value => value.value === 'Scheduled-reset')).toHaveLength(0);
    expect(starts.values.filter(value => value.value === 'Scheduled-unmount' && value.closed)).toHaveLength(0);
    expect(starts.values.filter(value => value.value === 'FRESH')).toHaveLength(1);
    await expect(phase(root(page), 'name')).toHaveAttribute('data-state', 'idle');
  } finally {await page.evaluate(() => {window.fetch = window.fvFetchOriginal;});}
});
