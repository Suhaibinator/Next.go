// Frozen prebuilt zones executable only. This suite never compiles Go or calls
// AWS. Browser tests separately qualify actual full-document zone navigation.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {request as httpRequest} from 'node:http';
import {resolve} from 'node:path';
import test from 'node:test';
import {gunzipSync} from 'node:zlib';
import {startLambdaGateway} from './lambda-features-gateway.mjs';

const binary = process.env.NEXTGO_ZONES_BINARY ? resolve(process.env.NEXTGO_ZONES_BINARY) : undefined;
const port = 4232, publicHost = '127.0.0.1:4230', alpha = 'alpha.example.test', beta = 'beta.example.test', unowned = 'unowned.example.test';
const allowedHosts = [publicHost, alpha, beta, unowned];
const responseLimit = 16 << 20;

function request(host, path, {method = 'GET', headers = {}, body = ''} = {}) {
  return new Promise((done, reject) => {
    const outgoing = httpRequest({hostname: '127.0.0.1', port, path, method, headers: {Host: host, 'Accept-Encoding': 'gzip', ...headers}, timeout: 15_000}, response => {
      const chunks = []; let size = 0;
      response.on('data', chunk => {size += chunk.length; if (size > responseLimit) response.destroy(new Error('bounded zones fixture response')); else chunks.push(chunk);});
      response.once('error', reject);
      response.once('end', () => {
        try {
          let data = Buffer.concat(chunks, size);
          if (response.headers['content-encoding'] === 'gzip' && data.length) data = gunzipSync(data, {maxOutputLength: responseLimit});
          done({status: response.statusCode, headers: response.headers, body: data, text: data.toString('utf8')});
        } catch (error) {reject(error);}
      });
    });
    outgoing.once('error', reject); outgoing.once('timeout', () => outgoing.destroy(new Error('bounded zones request timeout'))); outgoing.end(body);
  });
}

function json(reply, status = 200) {
  assert.equal(reply.status, status, reply.text.slice(0, 300)); assert.match(reply.headers['content-type'], /^application\/json/); return JSON.parse(reply.text);
}
function noStore(reply) {assert.match(reply.headers['cache-control'] || '', /no-store/);}
function attribute(html, name) {
  const value = new RegExp(`\\b${name}="([^"]*)"`).exec(html)?.[1]; assert.notEqual(value, undefined, `missing ${name}`);
  return value.replace(/&#34;|&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
}
function identity(reply) {
  assert.equal(reply.status, 200, reply.text.slice(0, 300)); assert.match(reply.headers['content-type'], /^text\/html/);
  const build = attribute(reply.text, 'data-nextgo-build'), configuration = attribute(reply.text, 'data-nextgo-configuration');
  assert.match(build, /^[a-f0-9]{20,64}$/); assert.match(configuration, /^[a-f0-9]{64}$/); assert.equal(reply.headers['x-nextgo-configuration'], configuration);
  return {build, configuration, manifest: JSON.parse(attribute(reply.text, 'data-nextgo-navigation'))};
}
function action(host, path, values, {cookie, native = false, origin = `http://${host}`} = {}) {
  return request(host, path, {method: 'POST', body: new URLSearchParams(values).toString(), headers: {
    'Content-Type': 'application/x-www-form-urlencoded', Accept: native ? 'text/html' : 'application/json', Origin: origin, ...(cookie ? {Cookie: cookie} : {}),
  }});
}

async function application(mode) {
  if (mode !== 'http') {
    // Only the operator environment can override generated exact host mappings.
    // Keep this original-manifest fixture independent of the caller's setting.
    const previous = process.env.NEXTGO_ZONE_HOSTS; delete process.env.NEXTGO_ZONE_HOSTS;
    try {return await startLambdaGateway({binary, mode, port, allowedHosts});}
    finally {if (previous === undefined) delete process.env.NEXTGO_ZONE_HOSTS; else process.env.NEXTGO_ZONE_HOSTS = previous;}
  }
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_') && key !== 'NEXTGO_ZONE_HOSTS' && key !== 'NEXTGO_ENV_DIR'));
  const child = spawn(binary, ['-addr', `127.0.0.1:${port}`], {cwd: '/', env, stdio: ['ignore', 'ignore', 'pipe']});
  let logs = ''; child.stderr.on('data', data => {logs = (logs + data.toString()).slice(-4096);});
  const exited = new Promise(done => {child.once('error', error => done({error})); child.once('exit', (code, signal) => done({code, signal}));});
  async function close() {
    child.kill('SIGTERM'); let timer;
    const result = await Promise.race([exited, new Promise(done => {timer = setTimeout(() => {child.kill('SIGKILL'); done({timeout: true});}, 6500);})]);
    clearTimeout(timer); if (result.timeout) await exited;
    assert.equal(result.timeout, undefined, logs); assert.equal(result.error, undefined, logs); assert.equal(result.code, 0, logs);
  }
  try {
    for (let attempt = 0; attempt < 100; attempt++) {
      try {if ((await request(publicHost, '/api/status')).status === 200) return {close};} catch {}
      await new Promise(done => setTimeout(done, 50));
    }
    throw new Error(`native zones sample did not become ready: ${logs}`);
  } catch (error) {await close(); throw error;}
}

async function assets(host, base, page, marker) {
  const info = identity(page);
  const style = await request(host, `${base}/assets/style.css`);
  assert.equal(style.status, 200); assert.match(style.headers['content-type'], /^text\/css/); assert.ok(style.text.includes(marker));
  const head = await request(host, `${base}/assets/style.css`, {method: 'HEAD'}); assert.equal(head.status, 200); assert.equal(head.body.length, 0);
  const wasm = await request(host, `${base}/_nextgo/${info.build}/app.wasm`);
  assert.equal(wasm.status, 200); assert.match(wasm.headers['content-type'], /^application\/wasm/); assert.ok(wasm.body.subarray(0, 8).equals(Buffer.from([0, 97, 115, 109, 1, 0, 0, 0])));
  assert.match(wasm.headers['cache-control'] || '', /immutable/);
  return {...info, style: style.text, wasm: wasm.body};
}

async function publicOwnership(mode) {
  const root = await request(publicHost, '/'), blog = await request(publicHost, '/blog');
  assert.ok(root.text.includes('data-zone-page="marketing"')); assert.ok(blog.text.includes('data-zone-page="blog"'));
  assert.ok(root.text.includes('zone=marketing tenant= host=127.0.0.1:4230 base='));
  assert.ok(blog.text.includes('zone=blog tenant= host=127.0.0.1:4230 base=/blog'));
  assert.ok(root.text.includes('href="/blog"')); assert.ok(blog.text.includes('href="/blog/details"')); assert.ok(blog.text.includes('action="/blog/_nextgo/actions/preview"'));
  const marketing = await assets(publicHost, '', root, '#126d59'), journal = await assets(publicHost, '/blog', blog, '#76541e');
  assert.notEqual(marketing.build, journal.build); assert.notEqual(marketing.configuration, journal.configuration); assert.notEqual(marketing.style, journal.style);
  assert.ok(marketing.manifest.documents.includes('/blog/*nextgoDocument?')); assert.ok(!(journal.manifest.documents || []).includes('/blog/*nextgoDocument?'));
  for (const [base, expected] of [['', 'marketing'], ['/blog', 'blog']]) {
    const state = await request(publicHost, `${base}/api/status`); noStore(state);
    assert.deepEqual(json(state), {app: expected, zone: expected, tenant: '', host: publicHost, basePath: base, revision: 0});
    const route = await request(publicHost, `${base}/posts/ownership`); assert.equal(route.status, 200); assert.ok(route.text.includes(`data-zone-post="${expected}"`));
    const missing = await request(publicHost, `${base}/not-owned-here`); assert.equal(missing.status, 404); assert.ok(missing.text.includes(`data-zone-not-found="${expected}"`));
    const nav = json(await request(publicHost, `${base}/details?from=deployment`, {headers: {'X-Nextgo-Navigate': '1'}}));
    assert.equal(nav.version, 3); assert.equal(nav.status, 200); assert.equal(nav.build, base ? journal.build : marketing.build); assert.equal(nav.configuration, base ? journal.configuration : marketing.configuration); assert.ok(nav.html.includes(`data-zone-details="${expected}"`));
  }
  const boundary = await request(publicHost, '/blogger'); assert.equal(boundary.status, 404); assert.ok(boundary.text.includes('data-zone-not-found="marketing"'));
  assert.equal((await request(publicHost, `/_nextgo/${journal.build}/app.wasm`)).status, 404);
  assert.equal((await request(publicHost, `/blog/_nextgo/${marketing.build}/app.wasm`)).status, 404);
  const forged = json(await request(publicHost, '/api/status', {headers: {'X-Forwarded-Host': alpha, Forwarded: `host=${alpha};proto=https`, 'X-Tenant': 'alpha'}})); assert.equal(forged.zone, 'marketing');
  const unknown = await request(unowned, '/api/status', {headers: {'X-Forwarded-Host': publicHost, Forwarded: `host=${publicHost};proto=http`}});
  assert.equal(unknown.status, 421); noStore(unknown); assert.ok(unknown.text.includes('Unconfigured zone host'));
  if (mode !== 'http') assert.equal(unknown.headers['x-nextgo-fixture-lambda-mode'], mode, 'unowned Host reached the actual Go Lambda handler');
  const sitemap = await request(publicHost, '/sitemap.xml', {headers: {'X-Forwarded-Host': 'forged.example.test'}});
  assert.equal(sitemap.status, 200); assert.ok(sitemap.text.includes(`http://${publicHost}/details`)); assert.ok(!sitemap.text.includes('forged.example.test'));
  const cached = async (base, expected) => {const reply = await request(publicHost, `${base}/cached`); assert.equal(reply.status, 200); assert.ok(reply.text.includes(`>${expected}</output>`), reply.text.slice(0, 300));};
  await cached('', 'marketing:0'); await cached('/blog', 'blog:0');
  const preview = json(await action(publicHost, '/_nextgo/actions/preview', {note: 'marketing public check'}));
  assert.deepEqual(preview.data, {app: 'marketing', zone: 'marketing', revision: 1, note: 'marketing public check'}); assert.equal(preview.redirect, '/?preview=1');
  await cached('', 'marketing:1'); await cached('/blog', 'blog:0');
  const native = await action(publicHost, '/blog/_nextgo/actions/preview', {note: 'blog native check'}, {native: true});
  assert.equal(native.status, 303); assert.equal(native.headers.location, '/blog?preview=1');
  await cached('', 'marketing:1'); await cached('/blog', 'blog:1');
  assert.equal((await action(publicHost, '/_nextgo/actions/preview', {note: 'denied'}, {origin: 'http://foreign.example.test'})).status, 403);
  assert.equal((await action(publicHost, '/_nextgo/actions/preview', {note: 'x'.repeat(81)})).status, 422);
  assert.equal(json(await request(publicHost, '/api/status')).revision, 1); assert.equal(json(await request(publicHost, '/blog/api/status')).revision, 1);
}

async function tenantAuthorization() {
  const first = await request(alpha, '/'), second = await request(beta, '/');
  assert.ok(first.text.includes('data-tenant-home="alpha"')); assert.ok(second.text.includes('data-tenant-home="beta"'));
  assert.equal(first.headers['x-nextgo-requires-server'], '1'); assert.equal(second.headers['x-nextgo-requires-server'], '1');
  const left = await assets(alpha, '', first, '#4e4c9b'), right = await assets(beta, '', second, '#4e4c9b');
  assert.notEqual(left.configuration, right.configuration, 'same tenant app has distinct trusted configuration identities');
  assert.ok(!left.wasm.includes(Buffer.from('zones-demo-v1:')), 'server HMAC implementation absent from browser import graph');
  for (const [host, tenant] of [[alpha, 'alpha'], [beta, 'beta']]) {
    const state = json(await request(host, '/api/status')); assert.equal(state.app, 'tenant'); assert.equal(state.zone, tenant); assert.equal(state.tenant, tenant); assert.equal(state.host, host); assert.equal(state.basePath, '');
    const denied = await request(host, '/dashboard'); assert.equal(denied.status, 401); noStore(denied); assert.ok(denied.text.includes('data-tenant-denial="401"'));
    assert.equal((await request(host, '/api/record?subject=alice', {headers: {'X-Subject': 'alice', 'X-Role': 'admin', 'X-Tenant': tenant}})).status, 401);
    assert.equal((await action(host, '/_nextgo/actions/preview', {note: 'unauthenticated'})).status, 401);
  }
  async function assume(host, subject) {
    const reply = await action(host, '/_nextgo/actions/assume', {subject}, {native: true});
    assert.equal(reply.status, 303); assert.equal(reply.headers.location, '/dashboard'); noStore(reply);
    const cookies = reply.headers['set-cookie']; assert.ok(Array.isArray(cookies) && cookies.length === 1);
    assert.match(cookies[0], /^nextgo-zones-demo=/); assert.match(cookies[0], /; HttpOnly/i); assert.match(cookies[0], /; SameSite=Lax/i); assert.match(cookies[0], /; Path=\//); assert.ok(!/; Domain=/i.test(cookies[0]));
    return cookies[0].split(';', 1)[0];
  }
  const alice = await assume(alpha, 'alice'), bob = await assume(beta, 'bob');
  const record = async (host, cookie) => {const reply = await request(host, '/api/record', {headers: {Cookie: cookie}}); noStore(reply); return json(reply);};
  assert.deepEqual(await record(alpha, alice), {tenant: 'alpha', title: 'alpha synthetic field notes', subject: 'alice', revision: 0});
  assert.deepEqual(await record(beta, bob), {tenant: 'beta', title: 'beta synthetic field notes', subject: 'bob', revision: 0});
  for (const [host, cookie] of [[beta, alice], [alpha, bob]]) {
    const denied = await request(host, '/dashboard', {headers: {Cookie: cookie}}); assert.equal(denied.status, 403); noStore(denied); assert.ok(denied.text.includes('data-tenant-denial="403"'));
    assert.equal((await request(host, '/api/record', {headers: {Cookie: cookie}})).status, 403);
    assert.equal((await action(host, '/_nextgo/actions/preview', {note: 'wrong member'}, {cookie})).status, 403);
  }
  const split = alice.lastIndexOf('.') + 1, tampered = alice.slice(0, split) + (alice[split] === 'A' ? 'B' : 'A') + alice.slice(split + 1);
  for (const cookie of [tampered, `${alice}; ${alice}`]) assert.equal((await request(alpha, '/api/record', {headers: {Cookie: cookie}})).status, 401);
  const accepted = json(await action(alpha, '/_nextgo/actions/preview', {note: 'alpha only'}, {cookie: alice}));
  assert.deepEqual(accepted.data, {tenant: 'alpha', subject: 'alice', zone: 'alpha', revision: 1}); assert.equal((await record(beta, bob)).revision, 0);
  const native = await action(beta, '/_nextgo/actions/preview', {note: 'beta only'}, {cookie: bob, native: true}); assert.equal(native.status, 303); assert.equal(native.headers.location, '/dashboard?preview=1');
  assert.equal((await record(alpha, alice)).revision, 1); assert.equal((await record(beta, bob)).revision, 1);
  const nav = json(await request(alpha, '/dashboard', {headers: {Cookie: alice, 'X-Nextgo-Navigate': '1'}}));
  assert.equal(nav.status, 200); assert.equal(nav.configuration, left.configuration); assert.equal(nav.build, left.build); assert.ok(nav.html.includes('data-tenant-dashboard="alpha"'));
  const wrong = json(await request(beta, '/dashboard', {headers: {Cookie: alice, 'X-Nextgo-Navigate': '1'}}), 403); assert.equal(wrong.status, 403); assert.ok(wrong.html.includes('data-tenant-denial="403"'));
  const marketing = await request(publicHost, '/dashboard', {headers: {Cookie: alice, 'X-Tenant': 'alpha'}}); assert.equal(marketing.status, 404); assert.ok(marketing.text.includes('data-zone-not-found="marketing"'));
}

for (const mode of ['http', 'buffered', 'streaming']) for (const [name, qualify] of [['public ownership', () => publicOwnership(mode)], ['tenant authorization', tenantAuthorization]]) {
  test(`prebuilt zones ${mode} ${name}`, {skip: !binary && 'set NEXTGO_ZONES_BINARY to the frozen prebuilt aggregate server', timeout: 90_000}, async t => {
    const hash = createHash('sha256').update(await readFile(binary)).digest('hex');
    t.diagnostic(`binary=${binary} sha256=${hash}; original manifest Hosts, actual loopback transport4232; public synthetic identities; LOCAL RuntimeAPI only`);
    const app = await application(mode);
    try {await qualify();} finally {await app.close();}
  });
}
