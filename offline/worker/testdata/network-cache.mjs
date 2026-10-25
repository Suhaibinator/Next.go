import assert from 'node:assert/strict';
import vm from 'node:vm';
import fs from 'node:fs';
import {createHash, webcrypto} from 'node:crypto';

const artifact = fs.readFileSync(process.argv[2], 'utf8');
const marker = 'const ScriptTemplate = `';
let script = artifact.slice(artifact.indexOf(marker) + marker.length, artifact.lastIndexOf('`'));
const hash = value => createHash('sha256').update(value).digest('hex');
const origin = 'https://example.test', docURL = origin + '/reader';
const assetURL = origin + '/reader/_nextgo/' + 'a'.repeat(20) + '/worker-' + hash('worker') + '.wasm';
const catalog = {
  Config: {Origin: origin, BasePath: '/reader', Budget: {InFlight: 4, Queue: 4}},
  Assets: [{URL: assetURL, Kind: 'worker-wasm', SHA256: hash('worker'), DecodedBytes: 6, ContentType: 'application/wasm'}],
  Documents: [{Path: '/'}],
};
const catalogJSON = JSON.stringify(catalog);
const descriptor = {
  namespace: 'nextgo-offline-v1-' + 'a'.repeat(64), generation: 'b'.repeat(64), catalogJSON, catalogSHA256: hash(catalogJSON),
  client: {scope: docURL, workerURL: docURL + '/_nextgo/offline/sw.js', catalogURL: docURL + '/_nextgo/offline/catalog'},
};
script = script.replace('__NEXTGO_OFFLINE_SHIM__', '').replace('__NEXTGO_OFFLINE_DESCRIPTOR__', JSON.stringify(descriptor));
const handlers = new Map(), forwarded = [], clones = [];
let transport = 'offline', ctorAttempts = 0, cancelStarted = false, releaseCancel;
const self = {
  location: {href: descriptor.client.workerURL}, registration: {active: null},
  clients: {async matchAll() { return []; }},
  addEventListener(name, callback) { handlers.set(name, callback); },
};
const context = vm.createContext({
  self, navigator: {locks: {async request(_name, _options, callback) { return callback(); }}},
  caches: {async keys() { return []; }}, crypto: webcrypto,
  Request, Response, Headers, ReadableStream, TextEncoder, TextDecoder, AbortController, MessageChannel, setTimeout, clearTimeout,
  Go: class { constructor() { ctorAttempts++; throw Error('missing bootstrap must not instantiate'); } },
  WebAssembly,
  async fetch(request) {
    forwarded.push(request);
    if (request.signal.aborted) throw new TypeError('aborted before native network');
    if (transport === 'stream') {
      return new Response(new ReadableStream({
        start(controller) { controller.enqueue(new TextEncoder().encode('live body')); },
        cancel() { cancelStarted = true; return new Promise(resolve => { releaseCancel = resolve; }); },
      }), {headers: {'X-Native': 'live'}});
    }
    // A stale HTTP cache can succeed while the real native listener is offline.
    // The document request must bypass it so Go can choose its verified pack.
    if (request.cache !== 'no-store') return new Response('stale normal SSR', {headers: {Age: '3', 'Cache-Control': 'public,max-age=30'}});
    throw new TypeError('native server offline');
  },
});
vm.runInContext(script, context);
function request(url, cache, signal) {
  const value = new Request(url, {cache, credentials: 'include', redirect: 'manual', headers: {'X-Original': 'preserved', Accept: 'text/html'}, signal});
  // Node cannot author a navigation Request. Use its genuine Request/cache/
  // header/signal implementation with the browser-supplied navigation selector.
  if (url === docURL) Object.defineProperty(value, 'mode', {value: 'navigate'});
  // Observe the genuine native clone operation, not a mock Request object.
  const clone = value.clone.bind(value);
  value.clone = () => { const owned = clone(); assert.notEqual(owned, value); clones.push({original: value, owned}); return owned; };
  return value;
}
function start(value) {
  let response, completion;
  handlers.get('fetch')({request: value, clientId: 'view', respondWith(promise) { response = promise; }, waitUntil(promise) { completion = promise; }});
  assert.ok(response); assert.ok(completion);
  return {response, completion};
}
for (const cache of ['default', 'reload', 'no-cache']) {
  const original = request(docURL, cache), before = clones.length;
  const current = start(original);
  const response = await current.response;
  assert.equal(response.status, 503, 'boot-unavailable document must expose real network failure, never stale HTTP-cache SSR');
  assert.equal(response.headers.get('Age'), null);
  await current.completion;
  const sent = forwarded.at(-1);
  assert.equal(sent.url, docURL); assert.equal(sent.cache, 'no-store');
  assert.equal(sent.method, 'GET'); assert.equal(sent.credentials, 'include'); assert.equal(sent.redirect, 'manual');
  assert.equal(sent.headers.get('X-Original'), 'preserved'); assert.equal(sent.headers.get('Accept'), 'text/html');
  assert.equal(clones.length, before + 1); assert.equal(clones.at(-1).original, original);
  assert.equal(original.cache, cache); assert.equal(original.bodyUsed, false); assert.equal(original.signal.aborted, false);
  assert.equal(original.headers.get('X-Original'), 'preserved'); assert.equal(original.credentials, 'include'); assert.equal(original.redirect, 'manual');
}
for (const cache of ['default', 'reload', 'no-cache']) {
  const before = clones.length;
  const current = start(request(assetURL, cache));
  const response = await current.response;
  assert.equal(await response.text(), 'stale normal SSR'); await current.completion;
  assert.equal(forwarded.at(-1).cache, cache, 'asset forwarding preserves its original cache policy');
  assert.equal(clones.length, before, 'asset forwarding does not clone the original Request');
}
// An explicit no-store navigation bypasses the worker before boot/admission.
let intercepted = false, extended = false;
handlers.get('fetch')({request: request(docURL, 'no-store'), respondWith() { intercepted = true; }, waitUntil() { extended = true; }});
assert.equal(intercepted, false); assert.equal(extended, false); assert.equal(forwarded.length, 6);
// Response headers do not settle the physical body lease. Propagate the actual
// original signal, then await the source's cancellation before event completion.
transport = 'stream';
const controller = new AbortController(), streaming = start(request(docURL, 'default', controller.signal));
const response = await streaming.response, sent = forwarded.at(-1);
assert.equal(sent.cache, 'no-store'); assert.equal(sent.signal.aborted, false);
const reader = response.body.getReader(); assert.equal(new TextDecoder().decode((await reader.read()).value), 'live body');
let settled = false; streaming.completion.then(() => { settled = true; });
controller.abort(); assert.equal(sent.signal.aborted, true);
const cancel = reader.cancel('test cancellation');
for (let i = 0; i < 100 && !cancelStarted; i++) await new Promise(resolve => setTimeout(resolve, 1));
assert.equal(cancelStarted, true);
await new Promise(resolve => setTimeout(resolve, 1));
assert.equal(settled, false, 'event must retain capacity through physical source cancellation');
releaseCancel(); await cancel; await streaming.completion; assert.equal(settled, true);
assert.equal(forwarded.length, 7, 'every admitted request forwarded exactly once'); assert.equal(ctorAttempts, 0);
assert.equal(clones.length, 4, 'only the four admitted document requests clone, never assets or no-store bypass');
console.log('document HTTP cache bypass and original-request/body ownership verified');
