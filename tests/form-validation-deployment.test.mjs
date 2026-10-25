// Prebuilt public sample only. No Go builds, external service, or saved data.
// The browser suites separately qualify debounce and scoped validation ownership.
import assert from 'node:assert/strict';
import {spawn} from 'node:child_process';
import {readFile} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import {request as httpRequest} from 'node:http';
import {resolve} from 'node:path';
import test from 'node:test';
import {gunzipSync} from 'node:zlib';
import {startLambdaGateway} from './lambda-features-gateway.mjs';

const binary = process.env.NEXTGO_FORM_VALIDATION_BINARY ? resolve(process.env.NEXTGO_FORM_VALIDATION_BINARY) : undefined;
const responseLimit = 16 << 20;
const scopes = [{name: 'root', base: '', assets: ''}, {name: 'mounted-assets', base: '/form-validation', assets: '/form-assets'}];

function request(origin, path, {method = 'GET', body = '', headers = {}} = {}) {
  return new Promise((done, reject) => {
    const outgoing = httpRequest(new URL(path, origin), {method, headers: {'Accept-Encoding': 'gzip', ...headers}, timeout: 15_000}, response => {
      const chunks = []; let size = 0;
      response.on('data', chunk => {size += chunk.length; if (size > responseLimit) response.destroy(new Error('bounded form fixture response')); else chunks.push(chunk);});
      response.once('error', reject);
      response.once('end', () => {
        try {
          let data = Buffer.concat(chunks, size);
          if (response.headers['content-encoding'] === 'gzip' && data.length) data = gunzipSync(data, {maxOutputLength: responseLimit});
          done({status: response.statusCode, headers: response.headers, body: data, text: data.toString('utf8')});
        } catch (error) {reject(error);}
      });
    });
    outgoing.once('error', reject); outgoing.once('timeout', () => outgoing.destroy(new Error('bounded form fixture request timeout')));
    outgoing.end(body);
  });
}

function sampleEnvironment(scope) {
  const environment = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_') && !key.startsWith('NEXTGO_FORM_VALIDATION_') && key !== 'NEXTGO_ENV_DIR'));
  return {...environment, NEXTGO_FORM_VALIDATION_BASE_PATH: scope.base, NEXTGO_FORM_VALIDATION_ASSET_PREFIX: scope.assets};
}

async function application(mode, scope) {
  const port = mode === 'http' ? 4218 : 4219;
  const origin = `http://127.0.0.1:${port}`;
  if (mode !== 'http') {
    // The existing gateway captures its child's environment at startup. Its
    // demo-specific variables are ignored by this separately configured app.
    const names = [...new Set([...Object.keys(process.env).filter(name => name.startsWith('NEXTGO_FORM_VALIDATION_')), 'NEXTGO_FORM_VALIDATION_BASE_PATH', 'NEXTGO_FORM_VALIDATION_ASSET_PREFIX'])];
    const previous = names.map(name => process.env[name]);
    for (const name of names) delete process.env[name];
    process.env.NEXTGO_FORM_VALIDATION_BASE_PATH = scope.base; process.env.NEXTGO_FORM_VALIDATION_ASSET_PREFIX = scope.assets;
    try {
      const worker = await startLambdaGateway({binary, mode, port});
      return {origin, close: () => worker.close()};
    } finally {
      for (let n = 0; n < names.length; n++) if (previous[n] === undefined) delete process.env[names[n]]; else process.env[names[n]] = previous[n];
    }
  }
  const child = spawn(binary, ['-addr', `127.0.0.1:${port}`], {cwd: '/', env: sampleEnvironment(scope), stdio: ['ignore', 'ignore', 'pipe']});
  let logs = '';
  child.stderr.on('data', data => {logs = (logs + data.toString()).slice(-4096);});
  const exited = new Promise(done => {child.once('error', error => done({error})); child.once('exit', (code, signal) => done({code, signal}));});
  async function close() {
    child.kill('SIGTERM'); let timer;
    const result = await Promise.race([exited, new Promise(done => {timer = setTimeout(() => {child.kill('SIGKILL'); done({timeout: true});}, 6500);})]);
    clearTimeout(timer); if (result.timeout) await exited;
    assert.equal(result.timeout, undefined, logs); assert.equal(result.error, undefined, logs); assert.equal(result.code, 0, logs);
  }
  try {
    for (let attempt = 0; attempt < 100; attempt++) {
      try {if ((await request(origin, `${scope.base}/api/health`)).status === 200) return {origin, close};} catch {}
      await new Promise(done => setTimeout(done, 50));
    }
    throw new Error(`native form sample did not become ready: ${logs}`);
  } catch (error) {await close(); throw error;}
}

function noStore(reply) {assert.equal(reply.headers['cache-control'], 'no-store'); assert.equal(reply.headers['x-content-type-options'], 'nosniff');}
function json(reply, status = 200) {assert.equal(reply.status, status, reply.text.slice(0, 300)); noStore(reply); assert.match(reply.headers['content-type'], /^application\/json/); return JSON.parse(reply.text);}

async function qualify(app, scope) {
  const get = path => request(app.origin, path === '/' ? scope.base || '/' : scope.base + path);
  const post = (kind, body) => request(app.origin, `${scope.base}/api/preview`, {method: 'POST', body, headers: {'Content-Type': kind}});
  if (scope.base) {
    const canonical = await request(app.origin, `${scope.base}/`);
    assert.equal(canonical.status, 308); assert.equal(canonical.headers.location, scope.base);
    assert.match(canonical.headers['content-type'], /^text\/html/);
  }
  const initial = await get('/');
  assert.equal(initial.status, 200); assert.equal(initial.headers['x-nextgo-requires-server'], '1');
  assert.match(initial.text, /<html\b[^>]*lang="en"/); assert.ok(initial.text.includes('<h1>Choices with a clear state.</h1>'));
  assert.ok(initial.text.includes(`${scope.assets || scope.base}/assets/style.css`), 'native SSR includes the actual scoped stylesheet');
  assert.equal((initial.text.match(/data-fv-instance=/g) || []).length, 2);
  for (const label of ['Morning notes (notes)', 'Evening notes (notes)', 'Empty topic value', 'Selected empty value', 'No selection']) assert.ok(initial.text.includes(label), label);
  assert.ok(initial.text.includes('value="Ada"')); assert.ok(initial.text.includes('value="Lin"'));
  const style = await request(app.origin, `${scope.assets || scope.base}/assets/style.css`);
  assert.equal(style.status, 200); assert.match(style.headers['content-type'], /^text\/css/); assert.ok(style.body.length > 100);
  const runtime = /(?:https?:\/\/[^" ]+)?[^" ]*\/_nextgo\/([a-f0-9]{20,64})\/boot\.js/.exec(initial.text);
  assert.ok(runtime, 'real runtime bootstrap in SSR');
  const wasm = await request(app.origin, `${scope.assets || scope.base}/_nextgo/${runtime[1]}/app.wasm`);
  assert.equal(wasm.status, 200); assert.match(wasm.headers['content-type'], /^application\/wasm/); assert.ok(wasm.body.subarray(0, 4).equals(Buffer.from([0, 97, 115, 109])));

  assert.deepEqual(json(await get('/api/validate?field=name&value=available')), {message: ''});
  assert.deepEqual(json(await get('/api/validate?field=name&value=reserved')), {message: 'This public name is reserved.'});
  assert.deepEqual(json(await get('/api/validate?field=code&value=reserved')), {message: 'This public code is reserved.'});
  assert.deepEqual(json(await get('/api/validate?field=name&value=slow-reserved')), {message: 'This public name is reserved.'});
  const offline = await get('/api/validate?field=name&value=offline'); assert.equal(offline.status, 503); noStore(offline); assert.ok(!offline.text.includes('database'));
  const head = await request(app.origin, `${scope.base}/api/validate?field=name&value=available`, {method: 'HEAD'}); assert.equal(head.status, 200); assert.equal(head.body.length, 0); noStore(head);
  for (const query of ['field=unknown&value=x', 'field=name&field=code&value=x', 'field=name&value=x&extra=y', `field=name&value=${'x'.repeat(129)}`]) assert.equal((await get(`/api/validate?${query}`)).status, 400);
  const validateMethod = await request(app.origin, `${scope.base}/api/validate?field=name&value=x`, {method: 'POST'}); assert.equal(validateMethod.status, 405); assert.equal(validateMethod.headers.allow, 'GET, HEAD');

  const value = {name: ' Ada ', code: 'go', channel: {Value: '', Selected: true}, topics: [3, 1, 0]};
  const accepted = json(await post('application/json', JSON.stringify(value)));
  assert.deepEqual(accepted.value, {name: 'Ada', code: 'GO', channel: {Value: '', Selected: true}, topics: [0, 1, 3]});
  assert.equal(accepted.message, 'Public preview confirmed. Nothing saved.');
  const absent = json(await post('application/json', JSON.stringify({...value, channel: {Value: '', Selected: false}, topics: [1]})));
  assert.deepEqual(absent.value.channel, {Value: '', Selected: false}); assert.deepEqual(absent.value.topics, [1]);
  for (const topics of [[0, 0], [4]]) assert.ok(json(await post('application/json', JSON.stringify({...value, topics})), 422).fields.topics);
  assert.equal(json(await post('application/json', JSON.stringify({...value, name: 'reserved'})), 422).fields.name, 'This public name is reserved.');
  assert.equal((await post('application/json', JSON.stringify({...value, code: 'offline'}))).status, 503);

  for (const present of [false, true]) {
    const fields = new URLSearchParams({name: '<script>alert(1)</script>', code: 'GO', intent: 'native'});
    if (present) fields.append('channel', '');
    for (const topic of ['notes', 'notes', '']) fields.append('topics', topic);
    const reply = await post('application/x-www-form-urlencoded', fields.toString());
    assert.equal(reply.status, 200); noStore(reply); assert.match(reply.headers['content-type'], /^text\/html/);
    assert.ok(reply.text.includes('<html lang="en">')); assert.ok(reply.text.includes('data-fv-receipt'));
    assert.ok(reply.text.includes(`data-present="${present}"`));
    assert.ok(reply.text.includes(present ? 'Selected empty value' : 'No selection'));
    assert.ok(reply.text.includes('[&#34;notes&#34;,&#34;notes&#34;,&#34;&#34;]'));
    assert.ok(reply.text.includes('&lt;script&gt;')); assert.ok(!reply.text.includes('<script>'));
    assert.ok(!reply.text.includes('data-fv-confirmed-indices'), 'native repeated values must not invent equal-valued option identity');
  }
  for (const body of ['name=Ada&name=Lin&code=GO', 'name=Ada&code=GO&channel=&channel=email', 'name=Ada&code=GO&topics=notes&topics=notes&topics=notes', 'name=Ada&code=GO&unknown=1']) assert.equal((await post('application/x-www-form-urlencoded', body)).status, 400);
  for (const body of ['{"name":"Ada","name":"Lin","code":"GO"}', '{"name":"Ada","code":"GO","channel":{"Selected":false,"Selected":true}}', '{"unknown":true}', 'x'.repeat(4097)]) assert.equal((await post('application/json', body)).status, 400);
  assert.equal((await post('text/plain', 'name=Ada')).status, 415);
  const wrongMethod = await get('/api/preview'); assert.equal(wrongMethod.status, 405); assert.equal(wrongMethod.headers.allow, 'POST');
  const unchanged = await get('/'); assert.equal(unchanged.status, 200); assert.ok(unchanged.text.includes('value="Ada"')); assert.ok(unchanged.text.includes('value="Lin"'));
}

for (const mode of ['http', 'buffered', 'streaming']) for (const scope of scopes) {
  test(`prebuilt public form ${mode} ${scope.name}`, {skip: !binary && 'set NEXTGO_FORM_VALIDATION_BINARY to the frozen prebuilt sample', timeout: 90_000}, async t => {
    const hash = createHash('sha256').update(await readFile(binary)).digest('hex');
    t.diagnostic(`binary=${binary} sha256=${hash}; validationGET+HEAD supported, other methods405; public ephemeral preview only`);
    const app = await application(mode, scope);
    try {await qualify(app, scope);} finally {await app.close();}
  });
}
