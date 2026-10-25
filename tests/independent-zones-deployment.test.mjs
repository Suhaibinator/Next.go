// Nine actual executable combinations: ingress HTTP/buffered/streaming crossed
// with endpoint HTTP/buffered/streaming. Operator swaps only prebuilt snapshots.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import test from 'node:test';
import {expectation, loopbackOrigin, readIndependentArtifacts, startIndependentRelay, startIndependentWorker, transports, wireRequest} from './independent-zones-deployment-fixture.mjs';

const selected = process.env.NEXTGO_INDEPENDENT_ZONES_ARTIFACTS;
const digest = data => createHash('sha256').update(data).digest('hex');
const publicPort = 4270, publicSocket = `http://127.0.0.1:${publicPort}`;
function json(reply, status = 200) {assert.equal(reply.status, status, reply.text.slice(0, 300)); assert.match(reply.headers['content-type'], /^application\/json/); return JSON.parse(reply.text);}
function privateAbsent(reply) {assert.ok(Object.keys(reply.headers).every(name => !name.toLowerCase().startsWith('x-nextgo-zone-')), 'private compatibility identity must not escape ingress');}
function noStore(reply) {assert.match(reply.headers['cache-control'] || '', /no-store/);}
function attr(html, name) {const raw = new RegExp(`\\b${name}="([^"]*)"`).exec(html)?.[1]; assert.notEqual(raw, undefined, name); return raw.replace(/&#34;|&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');}
function frames(reply, media) {assert.equal(reply.status, 200, reply.text.slice(0, 300)); assert.match(reply.headers['content-type'], media); const rows = reply.text.trim().split('\n').map(line => JSON.parse(line)); assert.ok(rows.length >= 2); for (const row of rows) assert.equal(row.version, 1); return rows;}

async function runCombination(t, ingressMode, originMode) {
  const artifacts = await readIndependentArtifacts(selected), workers = [], relay = await startIndependentRelay(artifacts);
  let ingress;
  const releaseFor = id => artifacts.releases[id];
  const direct = (id, path, options = {}) => wireRequest(artifacts.origins[id], path, {...options, headers: {...expectation(releaseFor(id)), ...options.headers}});
  const through = (path, options = {}) => wireRequest(publicSocket, path, {...options, headers: {Host: artifacts.publicHost, ...options.headers}});
  const statePath = id => releaseFor(id).spec.zone.basePath + '/api/state';
  const directState = async id => json(await direct(id, statePath(id)));
  const publicState = async id => {const reply = await through(statePath(id)); privateAbsent(reply); noStore(reply); return json(reply);};
  async function ready(call) {
    let last;
    for (let attempt = 0; attempt < 100; attempt++) {try {const reply = await call(); if (reply.status === 200) return reply; last = reply.status;} catch (error) {last = error.message;} await new Promise(done => setTimeout(done, 30));}
    throw new Error(`prebuilt independent worker not ready: ${last}`);
  }
  async function switchIngress(name) {
    if (ingress) {await ingress.close(); ingress = undefined;}
    ingress = await startIndependentWorker(artifacts.ingresses[name].binary, ingressMode, publicPort, {allowedHosts: [artifacts.publicHost, 'unowned-public.example.test']});
    if (name !== 'httpsDenial') await ready(() => through('/api/state'));
  }
  function action(id, {native = false, extra = {}} = {}) {
    return through(releaseFor(id).spec.zone.basePath + '/_nextgo/actions/preview/v1', {method: 'POST', body: native ? 'amount=1' : '{"amount":1}', headers: {
      Origin: artifacts.publicOrigin, Accept: native ? 'text/html' : 'application/json', 'Content-Type': native ? 'application/x-www-form-urlencoded' : 'application/json',
      'X-Nextgo-Action-Protocol': '1', 'X-Nextgo-Action-Revision': 'v1', ...extra,
    }});
  }
  async function page(id) {
    const release = releaseFor(id), base = release.spec.zone.basePath, reply = await through(base || '/');
    assert.equal(reply.status, 200); privateAbsent(reply); assert.ok(reply.text.includes(`data-independent-zone="${release.spec.zone.id}"`));
    assert.equal(attr(reply.text, 'data-nextgo-build'), release.spec.runtimeID); assert.equal(attr(reply.text, 'data-nextgo-runtime'), `${release.spec.assetPrefix}/_nextgo/${release.spec.runtimeID}/`);
    assert.equal((reply.text.match(/data-independent-instance=/g) || []).length, 2); assert.ok(reply.text.includes('>3</output>')); assert.ok(reply.text.includes('>10</output>'));
    return {reply, build: release.spec.runtimeID, configuration: attr(reply.text, 'data-nextgo-configuration')};
  }
  async function captureAssets(id) {
    const release = releaseFor(id), root = release.spec.assetPrefix, runtime = `${root}/_nextgo/${release.spec.runtimeID}/`, saved = [];
    const save = async path => {const reply = await through(path); assert.equal(reply.status, 200, path); privateAbsent(reply); assert.equal(reply.headers['set-cookie'], undefined); saved.push({path, sha256: digest(reply.body)}); return reply;};
    await save(`${root}/assets/style.css`); await save(`${root}/assets/card.css`); await save(`${root}/assets/release.txt`);
    const core = await save(runtime + 'app.wasm'); assert.ok(core.body.subarray(0, 8).equals(Buffer.from([0, 97, 115, 109, 1, 0, 0, 0]))); assert.match(core.headers['cache-control'] || '', /immutable/);
    const modules = [...new Set(core.body.toString('latin1').match(/module-[a-f0-9]{64}\.wasm/g) || [])]; assert.equal(modules.length, 1);
    for (const name of modules) {const module = await save(runtime + name); assert.equal(name, `module-${digest(module.body)}.wasm`);}
    await save(runtime + 'wasm_exec.js'); await save(runtime + 'boot.js');
    const head = await through(runtime + 'app.wasm', {method: 'HEAD'}); assert.equal(head.status, 200); assert.equal(head.body.length, 0); assert.equal(head.headers['set-cookie'], undefined);
    for (const suffix of ['/assets/', '/assets/missing.css', '/assets/style.css/', '/assets/.env.production', `/_nextgo/${release.spec.runtimeID}/`, `/_nextgo/${release.spec.runtimeID}/main.go`]) {
      const missing = await through(root + suffix); assert.equal(missing.status, 404, root + suffix); privateAbsent(missing); assert.equal(missing.headers['set-cookie'], undefined); assert.ok(!missing.text.includes('data-independent-zone=')); assert.ok(!missing.text.includes('package main'));
    }
    assert.equal((await through(basePath(release) + '/assets/style.css')).status, 404, 'no active-release alias');
    return saved;
  }
  function basePath(release) {return release.spec.zone.basePath;}
  async function protocols(id) {
    const release = releaseFor(id), zone = release.spec.zone.id, base = basePath(release), owner = await page(id);
    const current = await publicState(id); assert.equal(current.zone, zone); assert.equal(current.host, artifacts.publicHost); assert.equal(current.basePath, base); assert.equal(current.publicOrigin, artifacts.publicOrigin); assert.match(current.instance, /^[a-f0-9]{32}$/);
    const navReply = await through(base + '/about?q=fragments', {headers: {'X-Nextgo-Navigate': '1'}}); const nav = json(navReply); privateAbsent(navReply); assert.equal(navReply.headers['x-nextgo-protocol'], '3'); assert.equal(nav.version, 3); assert.equal(nav.build, owner.build); assert.equal(nav.configuration, owner.configuration); assert.ok(nav.html.includes(`data-independent-about="${zone}"`));
    const cookie = await through(base + '/api/cookie', {headers: {Cookie: 'independent-demo=public-client-marker'}}); assert.equal(cookie.status, 200); privateAbsent(cookie); noStore(cookie); assert.match(cookie.headers['cache-control'], /private/); assert.equal(cookie.headers['set-cookie'].length, 1); assert.ok(cookie.headers['set-cookie'][0].startsWith(`independent-demo=${current.nativeVersion};`)); assert.match(cookie.headers['set-cookie'][0], /HttpOnly/); assert.match(cookie.headers['set-cookie'][0], /SameSite=Lax/);
    const stream = await through(base + '/stream', {headers: {'Accept-Encoding': 'identity'}}); assert.equal(stream.status, 200); privateAbsent(stream); assert.ok(stream.text.includes('data-independent-loading')); assert.ok(stream.text.includes('data-independent-loaded')); assert.ok(stream.text.includes(current.nativeVersion));
    if (ingressMode !== 'buffered' && originMode !== 'buffered') {
      const loadingOffset = stream.body.indexOf('data-independent-loading'), loadedOffset = stream.body.indexOf('data-independent-loaded'); assert.ok(loadingOffset >= 0 && loadedOffset > loadingOffset);
      const first = stream.observations.find(item => item.end > loadingOffset), last = stream.observations.find(item => item.end > loadedOffset); assert.ok(last.atMs - first.atMs >= 75, 'actual progressive publication before deferred Go completion');
    }
    const streamed = await through(base + '/stream', {headers: {'Accept-Encoding': 'identity', 'X-Nextgo-Navigate': '1', 'X-Nextgo-Stream': '1'}}); const parts = frames(streamed, /application\/x-nextgo-stream\+json/); privateAbsent(streamed); assert.equal(parts[0].type, 'shell'); assert.equal(parts[0].page.build, owner.build); assert.ok(parts.some(part => part.type === 'chunk' && part.html.includes('data-independent-loaded'))); assert.equal(parts.at(-1).type, 'done');
    const before = (await publicState(id)).revision, response = await action(id); const result = json(response); privateAbsent(response); noStore(response); assert.equal(response.headers['x-nextgo-action-protocol'], '1'); assert.equal(response.headers['x-nextgo-action-revision'], 'v1'); assert.equal(result.data.zone, zone); assert.equal(result.data.instance, current.instance); assert.equal(result.data.applied, 1); assert.equal(result.data.revision, before + 1);
    const viewed = await action(id, {extra: {Accept: 'application/x-nextgo-action-view+json', 'X-Nextgo-Action-View': '1', 'X-Nextgo-Action-URL': base || '/', 'X-Nextgo-Action-Build': owner.build, 'X-Nextgo-Configuration': owner.configuration}}); const view = frames(viewed, /application\/x-nextgo-action-view\+json/); privateAbsent(viewed); assert.equal(view[0].type, 'result'); assert.equal(view[0].result.data.revision, before + 2); assert.ok(view.some(frame => frame.type === 'shell')); assert.equal(view.at(-1).type, 'done');
    const native = await action(id, {native: true}); assert.equal(native.status, 303); privateAbsent(native); assert.equal(native.headers.location, base ? base + '?preview=1' : '/?preview=1'); assert.equal((await publicState(id)).revision, before + 3);
    const wrongOrigin = await action(id, {extra: {Origin: 'https://unowned.example.test'}}); assert.equal(wrongOrigin.status, 403); assert.equal((await publicState(id)).revision, before + 3);
  }
  try {
    for (const id of ['A1', 'A2', 'B1']) {
      const directAddress = loopbackOrigin(artifacts.origins[id]), relayAddress = loopbackOrigin(artifacts.relayOrigin);
      const worker = await startIndependentWorker(releaseFor(id).binary, originMode, +directAddress.port, {backendHosts: [directAddress.host, relayAddress.host], allowedHosts: [directAddress.host, relayAddress.host, 'unowned-backend.example.test']}); workers.push(worker);
      await ready(() => direct(id, statePath(id)));
    }
    const initial = {A1: await directState('A1'), A2: await directState('A2'), B1: await directState('B1')}; assert.notEqual(initial.A1.instance, initial.A2.instance);
    t.diagnostic(JSON.stringify({indexSHA256: artifacts.indexSHA256, ingressMode, originMode, releaseSHA256: Object.fromEntries(Object.entries(artifacts.releases).map(([id, value]) => [id, value.binarySHA256])), ingressSHA256: Object.fromEntries(Object.entries(artifacts.ingresses).map(([id, value]) => [id, value.binarySHA256]))}));
    await switchIngress('I1'); await protocols('A1'); await protocols('B1'); const oldAssets = await captureAssets('A1');
    const blogBefore = await publicState('B1');
    await switchIngress('I2'); const active = await publicState('A2'); assert.equal(active.instance, initial.A2.instance); assert.equal((await publicState('B1')).instance, blogBefore.instance); assert.equal((await publicState('B1')).revision, blogBefore.revision); await page('A2');
    for (const saved of oldAssets) {const reply = await through(saved.path); assert.equal(reply.status, 200); assert.equal(digest(reply.body), saved.sha256); assert.equal(reply.headers['set-cookie'], undefined);}
    await captureAssets('A2');
    const spoof = await through('/api/state', {headers: {'X-Nextgo-Zone-Release': 'A1', 'X-Forwarded-Host': 'forged.example.test', Forwarded: 'host=forged.example.test;proto=https'}}); assert.equal(json(spoof).instance, initial.A2.instance); privateAbsent(spoof);
    const unknown = await through('/api/state', {headers: {Host: 'unowned-public.example.test', 'X-Forwarded-Host': artifacts.publicHost}}); assert.equal(unknown.status, 421); privateAbsent(unknown);
    for (const [mode, target, status] of [['forward', 'A1', 502], ['wrongHost', 'A2', 421]]) {
      relay.configure(mode, target); const beforeA1 = (await directState('A1')).revision, beforeA2 = (await directState('A2')).revision;
      const denied = await action('A2'); assert.equal(denied.status, status); noStore(denied); assert.equal((await directState('A1')).revision, beforeA1); assert.equal((await directState('A2')).revision, beforeA2);
    }
    for (const failure of ['before', 'after']) {
      relay.configure(failure); const before = (await directState('A2')).revision, start = relay.records.length;
      const outcome = await action('A2'); assert.ok(outcome.status >= 500, 'unknown outcome must not become fabricated successful Go output'); noStore(outcome);
      const attempts = relay.records.slice(start); assert.equal(attempts.length, 1, 'unsafe POST is never replayed'); assert.equal(attempts[0].method, 'POST');
      assert.equal((await directState('A2')).revision, before + (failure === 'after' ? 1 : 0));
      if (failure === 'after') {assert.equal(attempts[0].upstreamStatus, 200); assert.equal(attempts[0].originalComplete, true); assert.equal(attempts[0].originalHeaders['x-nextgo-zone-release'], 'A2'); const original = JSON.parse(attempts[0].originalBody); assert.equal(original.data.instance, initial.A2.instance); assert.equal(original.data.revision, before + 1); assert.equal(original.data.applied, 1); assert.match(attempts[0].originalWireSHA256, /^[a-f0-9]{64}$/);}
    }
    relay.configure('forward'); await switchIngress('rollback'); assert.equal((await publicState('A1')).instance, initial.A1.instance); assert.equal((await publicState('B1')).instance, blogBefore.instance); assert.equal((await publicState('B1')).revision, blogBefore.revision);
    for (const saved of oldAssets) assert.equal(digest((await through(saved.path)).body), saved.sha256);
    const beforeTLS = {A1: (await directState('A1')).revision, A2: (await directState('A2')).revision, B1: (await directState('B1')).revision}, relayAttempts = relay.records.length;
    await switchIngress('httpsDenial'); let denied;
    for (let attempt = 0; attempt < 100; attempt++) {try {denied = await through('/_nextgo/actions/preview/v1', {method: 'POST', body: '{"amount":1}', headers: {'Content-Type': 'application/json', Origin: artifacts.publicOrigin, 'X-Forwarded-Proto': 'https'}}); break;} catch {await new Promise(done => setTimeout(done, 30));}}
    assert.equal(denied.status, 400); assert.equal(relay.records.length, relayAttempts); for (const id of ['A1', 'A2', 'B1']) assert.equal((await directState(id)).revision, beforeTLS[id]);
  } finally {
    // A failed stop must not strand the remaining owned processes/listener.
    const failures = [];
    try {await ingress?.close();} catch (error) {failures.push(error);}
    const closed = await Promise.allSettled([relay.close(), ...workers.reverse().map(worker => worker.close())]);
    for (const item of closed) if (item.status === 'rejected') failures.push(item.reason);
    if (failures.length) throw new AggregateError(failures, 'independent fixture cleanup failed');
  }
}

for (const ingress of transports) for (const origin of transports) test(`independent zones ingress ${ingress} origins ${origin}`, {skip: !selected && 'set NEXTGO_INDEPENDENT_ZONES_ARTIFACTS to a complete prebuilt operator index', timeout: 120_000}, t => runCombination(t, ingress, origin));
