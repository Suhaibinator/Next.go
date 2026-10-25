// Actual prebuilt artifacts, HTTP and LOCAL RuntimeAPI only. No compilation.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {join} from 'node:path';
import test from 'node:test';
import {assets, base, modes, note, request, startRefreshProduction} from './refresh-production-fixture.mjs';

const digest = bytes => createHash('sha256').update(bytes).digest('hex');
// Optional qualification evidence from the artifact's actual compiler. Ordinary
// production remains supported when its matching SDK shim is unknown to HMR.
const expectedShim = process.env.NEXTGO_REFRESH_EXPECTED_SHIM_SHA256;
if (expectedShim !== undefined && !/^[a-f0-9]{64}$/.test(expectedShim)) throw new Error('Invalid fixture expected SDK shim digest');
function attr(html, name) {const value = new RegExp(`\\b${name}="([^"]*)"`).exec(html)?.[1]; assert.notEqual(value, undefined, name); return value.replace(/&#34;|&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');}
function data(reply) {assert.equal(reply.status, 200, reply.text.slice(0, 300)); assert.match(reply.headers['content-type'], /^application\/json/); assert.match(reply.headers['cache-control'] || '', /no-store/); return JSON.parse(reply.text);}
for (const mode of modes) {
  const available = mode === 'static' ? process.env.NEXTGO_REFRESH_STATIC_ROOT : process.env.NEXTGO_REFRESH_SERVER_BINARY || process.env.NEXTGO_E2E_SERVER_BINARY;
  test(`prebuilt refresh ${mode} ordinary runtime with HMR disabled`, {skip: !available && 'provide the frozen refresh binary and mounted static export', timeout: 60_000}, async t => {
    const worker = await startRefreshProduction(mode); t.diagnostic(JSON.stringify(worker.proof));
    try {
      const get = (path, options) => request(worker.origin, path, options);
      const home = await get(base); assert.equal(home.status, 200); assert.match(home.headers['content-type'], /^text\/html/);
      assert.equal((home.text.match(/data-refresh-instance=/g) || []).length, 2);
      for (const text of ['First counter', 'Second counter', '>3</output>', '>10</output>', 'Waiting for lazy Go', 'Shared page chrome from the server', 'Keep a public draft while Go changes.']) assert.ok(home.text.includes(text), text);
      assert.ok(home.text.includes(note)); assert.ok(home.text.includes(`href="${base}/details"`)); assert.ok(home.text.includes(`${assets}/assets/style.css`));
      const build = attr(home.text, 'data-nextgo-build'), configuration = attr(home.text, 'data-nextgo-configuration'), runtime = attr(home.text, 'data-nextgo-runtime');
      assert.match(build, /^[a-f0-9]{20,64}$/); assert.match(configuration, /^[a-f0-9]{64}$/); assert.equal(runtime, `${assets}/_nextgo/${build}/`);
      assert.ok(!JSON.parse(attr(home.text, 'data-nextgo-navigation')).development);
      const bootstrap = await get(runtime + 'boot.js'); assert.equal(bootstrap.status, 200); assert.ok(!bootstrap.text.includes('__nextgoDevRuntime')); assert.ok(!bootstrap.text.includes('__nextgoDevExitCode'));
      const shim = await get(runtime + 'wasm_exec.js'); assert.equal(shim.status, 200); assert.ok(shim.body.length > 1000);
      const shimDigest = digest(shim.body); t.diagnostic(`ordinary SDK support sha256=${shimDigest}; expected digest ${expectedShim ? 'explicit fixture compiler evidence' : 'not supplied'}`);
      if (expectedShim) assert.equal(shimDigest, expectedShim, 'explicit compiler-paired fixture support bytes');
      const core = await get(runtime + 'app.wasm'); assert.equal(core.status, 200); assert.match(core.headers['content-type'], /^application\/wasm/); assert.ok(core.body.subarray(0, 8).equals(Buffer.from([0, 97, 115, 109, 1, 0, 0, 0])));
      const modules = [...new Set(core.body.toString('latin1').match(/module-[a-f0-9]{64}\.wasm/g) || [])]; assert.equal(modules.length, 1, 'one ordinary lazy implementation shared by both instances');
      for (const file of modules) {const reply = await get(runtime + file); assert.equal(reply.status, 200); assert.equal(file, `module-${digest(reply.body)}.wasm`); assert.ok(reply.body.subarray(0, 4).equals(Buffer.from([0, 97, 115, 109])));}
      const style = await get(`${assets}/assets/style.css`); assert.equal(style.status, 200); assert.match(style.headers['content-type'], /^text\/css/); assert.ok(style.body.length > 100);
      const head = await get(`${assets}/assets/style.css`, {method: 'HEAD'}); assert.equal(head.status, 200); assert.equal(head.body.length, 0);
      for (const path of ['/_nextgo/refresh', '/_nextgo/events', `${base}/_nextgo/refresh`, `${base}/_nextgo/events`]) {
        const denied = await get(path); assert.equal(denied.status, 404, path); assert.ok(!String(denied.headers['content-type']).includes('text/event-stream'));
      }
      const details = await get(`${base}/details`); assert.equal(details.status, 200); assert.ok(details.text.includes('data-refresh-details')); assert.ok(!details.text.includes('data-refresh-instance='));
      if (mode === 'static') {
        assert.ok(!home.text.includes('data-refresh-work')); assert.ok(!home.text.includes('data-refresh-server-instance'));
        // Inert endpoint declarations remain in the navigation manifest so the
        // client knows they are endpoints. No visible links or worker props may
        // advertise them in a file-only export.
        assert.ok(!/\b(?:href|action)="[^"]*\/api\/(?:status|work)/.test(home.text));
        const props = [...home.text.matchAll(/\bdata-nextgo-props="([^"]*)"/g)].map(match => JSON.parse(attr(match[0], 'data-nextgo-props'))).filter(value => value.instance === 'primary' || value.instance === 'secondary');
        assert.equal(props.length, 2); for (const value of props) assert.equal(value.workURL, '');
        assert.equal((await get(`${base}/api/status`)).status, 404); assert.equal((await get(`${base}/api/work?delay=0`)).status, 404);
        const manifest = JSON.parse(await readFile(join(worker.proof.root, '.nextgo-export.json'), 'utf8')); assert.ok(['nextgo-static-v1', 'nextgo-static-v2'].includes(manifest.format)); assert.equal(manifest.basePath, base); assert.equal(manifest.assetPrefix, assets);
      } else {
        assert.equal((home.text.match(/data-refresh-work=/g) || []).length, 2);
        const first = data(await get(`${base}/api/status`)); assert.match(first.instance, /^[a-f0-9]{32}$/); assert.equal(first.source, 'native-v1'); assert.ok(Number.isSafeInteger(first.requests));
        assert.ok(home.text.includes(first.instance));
        const statusHead = await get(`${base}/api/status`, {method: 'HEAD'}); assert.equal(statusHead.status, 200); assert.equal(statusHead.body.length, 0);
        const second = data(await get(`${base}/api/status`)); assert.equal(second.instance, first.instance); assert.equal(second.requests, first.requests + 1);
        assert.deepEqual(data(await get(`${base}/api/work?delay=0`)), {complete: true});
        const workHead = await get(`${base}/api/work?delay=0`, {method: 'HEAD'}); assert.equal(workHead.status, 200); assert.equal(workHead.body.length, 0);
        for (const query of ['delay=-1', 'delay=15001', 'delay=0&delay=1', 'delay=0&extra=1', 'delay=no']) assert.equal((await get(`${base}/api/work?${query}`)).status, 400);
        const wrong = await get(`${base}/api/status`, {method: 'POST'}); assert.equal(wrong.status, 405); assert.equal(wrong.headers.allow, 'GET, HEAD');
        const nav = data(await get(`${base}/details?production=1`, {headers: {'X-Nextgo-Navigate': '1'}})); assert.equal(nav.version, 3); assert.equal(nav.status, 200); assert.equal(nav.build, build); assert.equal(nav.configuration, configuration); assert.ok(nav.html.includes('data-refresh-details'));
      }
    } finally {await worker.close();}
  });
}
