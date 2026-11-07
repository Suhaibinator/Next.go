import {test as base, expect} from '@playwright/test';
import {artifactFromEnvironment, startStaticShellHost} from './static-shell-host.mjs';

const test = base.extend({
  staticHost:async ({page}, use, info) => {
    const leaseMS = Number(process.env.NEXTGO_STATIC_SHELL_LEASE_MS);
    let host;
    try {host = await startStaticShellHost(artifactFromEnvironment(info.project.metadata.staticShellVariant), {leaseMS});}
    catch (error) {
      if (error.staticShellProof) await info.attach('static-listener-startup-failure-proof', {body:JSON.stringify(error.staticShellProof), contentType:'application/json'});
      throw error;
    }
    try {await use(host);} finally {
      const errors = [];
      const pageClose = page.close().catch(error => {errors.push(error);});
      try {await host.close({join:pageClose});} catch (error) {errors.push(error);}
      const proof = host.snapshot();
      await info.attach('actual-static-files-and-owned-listener-proof', {body:JSON.stringify(proof, null, 2), contentType:'application/json'});
      expect(proof.listening).toBe(false);
      expect(proof.sockets).toBe(0);
      expect(proof.responseStreams).toBe(0);
      expect(proof.handlers).toBe(0);
      expect(proof.fileOperations).toEqual([]);
      expect(proof.fileCloseErrors).toEqual([]);
      expect(proof.overflow).toBe(0);
      expect(proof.listenerErrors).toEqual([]);
      expect(proof.expired).toBe(false);
      expect(proof.closure).toMatchObject({serverCallbackJoined:true, socketJoinsComplete:true, handlerJoinsComplete:true, additionalJoinRequested:true, additionalJoinComplete:true, timedOut:false, failed:false, listening:false, sockets:0, responseStreams:0, handlers:0, fileOperations:[], fileCloseErrors:[], probe:{started:true,closeJoined:true,result:'ECONNREFUSED'}, outerControllerTerminationRequired:false});
      if (errors.length) throw new AggregateError(errors, 'owned static fixture cleanup failed');
    }
  },
});
const ready = page => expect(page.locator('body')).toHaveAttribute('data-nextgo-hydration', 'ready');
const appURL = (host, path) => host.origin + host.artifact.base + path;
const cafe = '/workspace/notes/caf%C3%A9?tag=one&tag=two#details';
const rest = page => page.locator('[data-shell-rest]');
const count = page => page.locator('[data-shell-count]');
const increment = page => page.locator('[data-shell-increment]');
async function mounted(page, host, expectedRest) {
  await ready(page);
  await expect(page.locator('[data-shell-view]')).toHaveAttribute('data-shell-component', host.artifact.mode);
  await expect(rest(page)).toHaveText(expectedRest);
  await expect(page.locator('[data-shell-mount-rest]')).toHaveText(expectedRest);
}

test('direct unexported UTF-8 URL retains native no-JS fallback and actually mounts eager or lazy Go at that URL', async ({page, browser, staticHost:host}, info) => {
  const use = info.project.use;
  const context = await browser.newContext({javaScriptEnabled:false, viewport:use.viewport, isMobile:use.isMobile, hasTouch:use.hasTouch});
  try {
    const plain = await context.newPage();
    const response = await plain.goto(appURL(host, cafe));
    expect(response.status()).toBe(200);
    await expect(plain).toHaveURL(appURL(host, cafe));
    await expect(plain.locator('[data-shell-fallback]')).toBeVisible();
    await expect(plain.getByRole('heading', {level:1})).toHaveText('Public workspace');
    await plain.locator('[data-shell-fallback-input]').fill('Native input works without Go');
    await expect(plain.locator('[data-shell-fallback-input]')).toHaveValue('Native input works without Go');
    await expect(plain.locator('[data-shell-view]')).toHaveCount(0);
    await expect(plain.getByRole('link', {name:'Home', exact:true})).toHaveAttribute('href', host.artifact.base || '/');
    await plain.getByRole('link', {name:'Home', exact:true}).click();
    await expect(plain.locator('[data-shell-home]')).toBeVisible();
  } finally {await context.close();}
  const start = host.records.length;
  await page.goto(appURL(host, cafe));
  await mounted(page, host, 'notes/café');
  await expect(page.locator('[data-shell-query]')).toHaveText('{"tag":["one","two"]}');
  await expect(page.locator('[data-shell-hash]')).toHaveText('details');
  await increment(page).click();
  await expect(count(page)).toHaveText('1');
  const requests = host.records.slice(start);
  expect(requests.some(entry => entry.file?.endsWith('/app.wasm') && entry.status === 200)).toBe(true);
  if (host.artifact.mode === 'workspacelazy') expect(requests.some(entry => /\/module-[a-f0-9]{64}\.wasm$/.test(entry.file || '') && entry.status === 200)).toBe(true);
});

test('actual Go navigation uses immutable shell bytes, committed params, remounted state and ordinary history', async ({page, staticHost:host}) => {
  const documents = [], requests = [];
  page.on('request', request => {
    requests.push(new URL(request.url()).pathname);
    if (request.isNavigationRequest() && request.frame() === page.mainFrame()) documents.push(request.url());
  });
  await page.goto(host.origin + (host.artifact.base || '/') );
  await ready(page);
  await page.evaluate(() => {globalThis.staticShellDocument = document;});
  await page.locator('[data-shell-enter]').click();
  await mounted(page, host, 'notes/café');
  await expect(page).toHaveURL(appURL(host, cafe));
  await increment(page).click(); await expect(count(page)).toHaveText('1');
  // An ordinary fragment link uses Go's same-page location observer, preserving state.
  await page.locator('[data-shell-fragment-link]').evaluate(link => {link.href = '#second';});
  await page.locator('[data-shell-fragment-link]').click();
  await expect(page.locator('[data-shell-hash]')).toHaveText('second');
  await expect(count(page)).toHaveText('1');
  await page.locator('[data-shell-link="board"]').click();
  await mounted(page, host, 'boards/team'); await expect(count(page)).toHaveText('0');
  await expect(page.locator('[data-shell-query]')).toHaveText('{"sort":["recent"]}');
  await page.goBack(); await mounted(page, host, 'notes/café');
  await expect(page.locator('[data-shell-hash]')).toHaveText('second');
  await expect(count(page)).toHaveText('0');
  await page.goForward(); await mounted(page, host, 'boards/team');
  await page.locator('[data-shell-replace]').click();
  await mounted(page, host, 'notes/replaced');
  await expect(page.locator('[data-shell-query]')).toHaveText('{"tag":["replace"]}');
  await page.goBack(); await mounted(page, host, 'notes/café');
  // Query-only commits remount while fragment-only commits above keep state.
  await increment(page).click();
  await page.locator('[data-shell-link="note"]').evaluate(link => {link.href = link.href.replace('tag=one&tag=two', 'tag=query-only');});
  await page.locator('[data-shell-link="note"]').click();
  await mounted(page, host, 'notes/café');
  await expect(page.locator('[data-shell-query]')).toHaveText('{"tag":["query-only"]}');
  await expect(count(page)).toHaveText('0');
  await page.locator('[data-shell-link="missing"]').click();
  await mounted(page, host, 'missing/record');
  await expect(page.locator('[data-shell-screen]')).toContainText('still has HTTP 200');
  await page.locator('[data-shell-link="home"]').click(); await ready(page);
  await expect(page.locator('[data-shell-home]')).toBeVisible();
  await expect(page.locator('[data-shell-view]')).toHaveCount(0);
  await page.locator('[data-shell-enter]').click(); await mounted(page, host, 'notes/café');
  await increment(page).click(); await expect(count(page)).toHaveText('1');
  expect(await page.evaluate(() => globalThis.staticShellDocument === document)).toBe(true);
  expect(documents).toHaveLength(1);
  expect(requests).toContain(host.artifact.descriptor.envelopeURL);
  expect(requests.some(path => path.includes('/_nextgo/data/workspace/'))).toBe(false);
});

test('immutable miss, digest and MIME failures preserve the current Go view while host denials stay terminal', async ({page, request, staticHost:host}) => {
  await page.goto(appURL(host, cafe)); await mounted(page, host, 'notes/café');
  await increment(page).click(); await expect(count(page)).toHaveText('1');
  await page.evaluate(() => {globalThis.staticShellView = document.querySelector('[data-shell-view]'); globalThis.staticShellDocument = document;});
  for (const fault of ['missing','corrupt','mime']) {
    host.setEnvelopeFault(fault);
    try {
      const report = page.waitForEvent('console', {predicate:message => message.type() === 'error' && /Next\.go:.*Navigation failed/.test(message.text())});
      await page.locator('[data-shell-link="board"]').click();
      await report;
      await expect(page).toHaveURL(appURL(host, cafe));
      await expect(rest(page)).toHaveText('notes/café');
      await expect(count(page)).toHaveText('1');
      expect(await page.evaluate(() => globalThis.staticShellView === document.querySelector('[data-shell-view]') && globalThis.staticShellDocument === document)).toBe(true);
      expect(host.records.some(entry => entry.file === host.artifact.envelopeName && entry.fault === fault)).toBe(true);
    } finally {host.setEnvelopeFault(null);}
  }
  await increment(page).click(); await expect(count(page)).toHaveText('2');
  const rejected = [
    '/unknown-outside', '/api/missing', '/_nextgo/missing.wasm', '/assets/missing.css',
    '/workspace/missing.json', '/workspace/report%2EJSON', '/workspace/report.json/',
    '/workspace/missing.WASM', '/workspace/missing.css', '/workspace/missing.js',
    '/workspace/missing.mjs', '/workspace/missing.cjs', '/workspace/missing.map',
    '/workspace/bad%2Ftail', '/work%73pace/unexported', '/workspace-neighbor/unexported',
  ];
  const replies = await Promise.all(rejected.map(path => request.get(appURL(host, path), {headers:{Accept:'text/html'}})));
  for (const response of replies) {expect([400,404]).toContain(response.status()); expect(response.headers()['content-type']).not.toContain('text/html');}
  if (host.artifact.base) {
    const miss = await request.get(host.origin + host.artifact.base + '-neighbor/workspace/notes/a', {headers:{Accept:'text/html'}});
    expect(miss.status()).toBe(404);
    const alias = await request.get(host.origin + host.artifact.base.replace(/.$/, char => '%' + char.charCodeAt(0).toString(16)) + '/workspace/notes/a', {headers:{Accept:'text/html'}});
    expect(alias.status()).toBe(404);
  }
  // Exact-file precedence cannot introduce encoded shell/framework prefixes.
  const actualCore = '/' + [...host.artifact.inventory.keys()].find(name => name.endsWith('/app.wasm'));
  const exactAliases = [
    host.artifact.base + '/work%73pace/index.html',
    actualCore.replace('/_nextgo/', '/%5fnextgo/'),
    host.artifact.descriptor.envelopeURL.replace('/_nextgo/', '/%5fnextgo/'),
  ];
  for (const path of exactAliases) {
    const response = await request.get(host.origin + path, {headers:{Accept:'text/html'}});
    expect(response.status()).toBe(404); expect(response.headers()['content-type']).not.toContain('text/html');
  }
  const encodedBasename = await request.head(host.origin + actualCore.replace(/app\.wasm$/, 'app%2Ewasm'));
  expect(encodedBasename.status()).toBe(200); expect(encodedBasename.headers()['content-type']).toBe('application/wasm');
  for (const allowed of ['/workspace/notes.v2', '/workspace/notes.json/details', '/workspace/report%252Ejson']) {
    const response = await request.get(appURL(host, allowed), {headers:{Accept:'text/html'}});
    expect(response.status()).toBe(200); expect(response.headers()['content-type']).toContain('text/html');
  }
  const nonDocument = await request.get(appURL(host, '/workspace/non-document'), {headers:{Accept:'application/json'}});
  expect(nonDocument.status()).toBe(404);
  const htmlExcluded = await request.get(appURL(host, '/workspace/html-quality-zero'), {headers:{Accept:'text/html;q=0, application/json'}});
  expect(htmlExcluded.status()).toBe(404);
  const destination = await request.get(appURL(host, '/workspace/resource-destination'), {headers:{Accept:'text/html','Sec-Fetch-Dest':'script'}});
  expect(destination.status()).toBe(404);
  const mutation = await request.post(appURL(host, '/workspace/notes/a'), {data:'not a document'});
  expect(mutation.status()).toBe(405);
  const head = await request.head(appURL(host, '/workspace/notes/head'), {headers:{Accept:'text/html'}});
  expect(head.status()).toBe(200); expect((await head.body()).length).toBe(0);
  const envelope = await request.get(host.origin + host.artifact.descriptor.envelopeURL);
  expect(envelope.status()).toBe(200); expect(envelope.headers()['content-type']).toBe('application/json');
  expect((await envelope.body()).length).toBe(host.artifact.descriptor.bytes);
  await page.locator('[data-shell-link="board"]').click(); await mounted(page, host, 'boards/team');
  await expect(count(page)).toHaveText('0');
});
