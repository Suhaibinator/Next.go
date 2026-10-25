// Real production executable; local Runtime API only, with no Go compilation.
// NEXTGO_TEST_BINARY must name the already-qualified demo server.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { createServer } from 'node:http';
import { resolve } from 'node:path';
import test from 'node:test';
import { gunzipSync } from 'node:zlib';

const binary = process.env.NEXTGO_TEST_BINARY ? resolve(process.env.NEXTGO_TEST_BINARY) : undefined;
const maxTransportBytes = 8 << 20;
const maxDecodedBytes = 32 << 20;
const requestTimeout = 12_000;

async function digestFile(path) {
  const hash = createHash('sha256');
  for await (const chunk of createReadStream(path)) hash.update(chunk);
  return hash.digest('hex');
}

function decodeReply(mode, data) {
  let metadata, body;
  if (mode === 'buffered') {
    metadata = JSON.parse(data.toString('utf8'));
    body = Buffer.from(metadata.body, metadata.isBase64Encoded ? 'base64' : 'utf8');
  } else {
    const end = data.indexOf(Buffer.alloc(8));
    assert.ok(end > 0 && end <= 16_384, 'bounded streaming metadata delimiter');
    metadata = JSON.parse(data.subarray(0, end).toString('utf8'));
    body = data.subarray(end + 8);
  }
  assert.ok(Number.isInteger(metadata.statusCode), 'HTTP integration status');
  const headers = Object.fromEntries(Object.entries(metadata.headers || {}).map(([key, value]) => [key.toLowerCase(), value]));
  if (headers['content-encoding'] === 'gzip' && body.length) body = gunzipSync(body, { maxOutputLength: maxDecodedBytes });
  assert.ok(body.length <= maxDecodedBytes, 'decoded response bound');
  return { status: metadata.statusCode, headers, body, cookies: metadata.cookies || [] };
}

async function lambdaWorker(mode, scope) {
  let next, queued, active, sequence = 0, stopped = false, logs = '';
  const fail = error => {
    for (const pending of [queued, active]) {
      if (pending) { clearTimeout(pending.timer); pending.reject(error); }
    }
    queued = active = undefined;
  };
  function dispatch() {
    if (!next || !queued || stopped) return;
    assert.equal(active, undefined, 'one active invocation per worker');
    active = queued; queued = undefined;
    const response = next; next = undefined;
    response.writeHead(200, {
      'Content-Type': 'application/json',
      'Lambda-Runtime-Aws-Request-Id': active.id,
      'Lambda-Runtime-Deadline-Ms': String(Date.now() + requestTimeout - 1000),
    });
    response.end(JSON.stringify(active.event));
  }
  const runtime = createServer(async (request, response) => {
    try {
      if (request.method === 'GET' && request.url === '/2018-06-01/runtime/invocation/next') {
        assert.equal(next, undefined, 'only one outstanding runtime poll');
        next = response;
        response.once('close', () => { if (next === response) next = undefined; });
        dispatch();
        return;
      }
      assert.equal(request.method, 'POST');
      assert.ok(active, 'response belongs to an active invocation');
      assert.equal(request.url, `/2018-06-01/runtime/invocation/${active.id}/response`);
      assert.equal(request.headers['lambda-runtime-function-response-mode'] || 'buffered', mode);
      const chunks = [];
      let size = 0;
      for await (const chunk of request) {
        size += chunk.length;
        assert.ok(size <= maxTransportBytes, 'bounded runtime upload');
        chunks.push(chunk);
      }
      assert.equal(request.trailers['lambda-runtime-function-error-type'], undefined, 'no late streaming adapter failure');
      const reply = decodeReply(mode, Buffer.concat(chunks, size));
      const pending = active; active = undefined;
      clearTimeout(pending.timer);
      response.writeHead(202); response.end();
      pending.resolve(reply);
    } catch (error) {
      fail(error);
      if (!response.headersSent) response.writeHead(500);
      response.end();
    }
  });
  runtime.maxConnections = 4;
  runtime.maxHeadersCount = 32;
  runtime.headersTimeout = 5000;
  await new Promise((resolveListen, reject) => {
    runtime.once('error', reject);
    runtime.listen(0, '127.0.0.1', resolveListen);
  });
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_') && key !== 'NEXTGO_ENV_DIR'));
  const child = spawn(binary, [], { cwd: '/', env: {
    ...env,
    AWS_LAMBDA_RUNTIME_API: `127.0.0.1:${runtime.address().port}`,
    NEXTGO_LAMBDA_RESPONSE_MODE: mode,
    NEXTGO_DEMO_BASE_PATH: scope.base,
    NEXTGO_DEMO_ASSET_PREFIX: scope.assets,
    NEXTGO_DEMO_TRAILING_SLASH: String(scope.slash),
    NEXTGO_DEMO_CSP: 'true',
  }, stdio: ['ignore', 'pipe', 'pipe'] });
  const appendLog = data => { logs = (logs + data.toString('utf8')).slice(-32_768); };
  child.stdout.on('data', appendLog); child.stderr.on('data', appendLog);
  const exited = new Promise(resolveExit => {
    child.once('error', error => { fail(error); resolveExit({ error }); });
    child.once('exit', (code, signal) => {
      if (!stopped) fail(new Error(`worker exited ${code}/${signal}: ${logs}`));
      resolveExit({ code, signal });
    });
  });
  return {
    request(method, path, headers = {}) {
      assert.ok(!stopped && !queued && !active, 'bounded sequential fixture requests');
      assert.ok(++sequence <= 64, 'bounded invocation count');
      const url = new URL(path, 'https://seven-feature.test');
      assert.equal(url.origin, 'https://seven-feature.test');
      return new Promise((resolveReply, reject) => {
        const timer = setTimeout(() => fail(new Error(`invocation timeout for ${path}: ${logs}`)), requestTimeout);
        queued = { id: String(sequence), resolve: resolveReply, reject, timer, event: {
          version: '2.0', rawPath: url.pathname, rawQueryString: url.search.slice(1),
          headers: { host: url.host, 'accept-encoding': 'gzip', ...headers },
          requestContext: { domainName: url.host, requestId: String(sequence), http: { method, path: url.pathname, protocol: 'HTTP/1.1', sourceIp: '127.0.0.1' } },
        } };
        dispatch();
      });
    },
    async close() {
      stopped = true;
      fail(new Error('fixture closed'));
      child.kill('SIGTERM');
      let timer;
      const result = await Promise.race([exited, new Promise(resolveTimeout => { timer = setTimeout(() => { child.kill('SIGKILL'); resolveTimeout({ timeout: true }); }, 6000); })]);
      clearTimeout(timer);
      runtime.closeAllConnections();
      await new Promise(resolveClose => runtime.close(resolveClose));
      if (result.timeout) await exited;
      assert.equal(result.timeout, undefined, 'bounded graceful worker shutdown');
      assert.equal(result.error, undefined, 'worker spawned');
      assert.equal(result.code, 0, `worker exited cleanly: ${logs}`);
    },
  };
}

function checkPNG(response) {
  assert.equal(response.status, 200);
  assert.match(response.headers['content-type'], /^image\/png/);
  assert.equal(response.headers['x-content-type-options'], 'nosniff');
  assert.equal(response.body.subarray(0, 8).toString('hex'), '89504e470d0a1a0a');
  assert.equal(response.body.subarray(12, 16).toString(), 'IHDR');
  assert.equal(response.body.readUInt32BE(16), 1200);
  assert.equal(response.body.readUInt32BE(20), 630);
  assert.equal(response.headers.etag, `"${createHash('sha256').update(response.body).digest('hex')}"`);
  assert.match(response.headers['cache-control'], /^public,.*must-revalidate/);
}

async function checkFeatures(t, worker, scope) {
  const page = path => scope.base + path + (scope.slash ? '/' : '');
  const api = path => scope.base + path + (scope.slash ? '/' : '');
  let build, configuration;
  for (const [path, title, marker] of [
    ['/resources', 'Scoped client resources — Next.go', 'data-resource-primary'],
    ['/social-image', 'Social images in Go', 'data-social-image-example'],
    ['/embeds', 'Safe embeds — Next.go', 'id="safe-frame"'],
  ]) {
    const response = await worker.request('GET', page(path));
    const html = response.body.toString('utf8');
    assert.equal(response.status, 200);
    assert.match(response.headers['content-type'], /^text\/html/);
    assert.equal(response.headers['x-nextgo-requires-server'], '1');
    assert.ok(html.includes(title) && html.includes(marker), `meaningful initial ${path} SSR`);
    const currentBuild = /data-nextgo-build="([a-f0-9]+)"/.exec(html)?.[1];
    const currentConfiguration = /data-nextgo-configuration="([a-f0-9]{64})"/.exec(html)?.[1];
    assert.ok(currentBuild && currentConfiguration, 'document generation identity');
    build ||= currentBuild; configuration ||= currentConfiguration;
    assert.equal(currentBuild, build); assert.equal(currentConfiguration, configuration);
    assert.equal(response.headers['x-nextgo-configuration'], configuration);
    if (path === '/resources') assert.ok(html.includes('Public Go resource') && html.includes('data-nextgo-island="resourcepanel"'));
    if (path === '/social-image') assert.ok(html.includes('property="og:image"') && html.includes('summary_large_image'));
    if (path === '/embeds') {
      assert.match(html, /<iframe[^>]*sandbox=""/);
      assert.ok(html.includes('referrerpolicy="no-referrer"') && html.includes('camera &#39;none&#39;'));
      assert.ok(html.includes('id="native-video"') && html.includes('id="native-audio"') && html.includes('kind="captions"'));
    }
    const navigation = await worker.request('GET', `${page(path)}?transport=lambda`, { 'x-nextgo-navigate': '1' });
    assert.equal(navigation.status, 200);
    assert.equal(navigation.headers['x-nextgo-requires-server'], '1');
    assert.equal(navigation.headers['x-nextgo-protocol'], '3');
    const envelope = JSON.parse(navigation.body);
    assert.equal(envelope.version, 3); assert.equal(envelope.status, 200);
    assert.equal(envelope.build, build); assert.equal(envelope.configuration, configuration);
    assert.equal(envelope.url, `${page(path)}?transport=lambda`);
    assert.ok(envelope.title.includes(title) && envelope.html.includes(marker));
  }
  const resources = await worker.request('GET', api('/api/resources'));
  assert.equal(resources.status, 200);
  assert.equal(resources.headers['cache-control'], 'public, max-age=30');
  assert.match(resources.headers['content-type'], /^application\/json/);
  const publicData = JSON.parse(resources.body);
  assert.deepEqual(Object.keys(publicData).sort(), ['generated', 'message']);
  assert.equal(publicData.message, 'Public Go resource'); assert.ok(Number.isFinite(Date.parse(publicData.generated)));
  const resourceHead = await worker.request('HEAD', api('/api/resources'));
  assert.equal(resourceHead.status, 200); assert.equal(resourceHead.body.length, 0);
  const failure = await worker.request('GET', `${api('/api/resources')}?fail=1`);
  assert.equal(failure.status, 503); assert.equal(failure.headers['cache-control'], 'no-store');
  assert.deepEqual(JSON.parse(failure.body), { error: 'The explicitly requested demo failure.' });

  const socialPath = `${api('/api/social')}?title=Lambda%20card&description=Public%20Go%20PNG`;
  const social = await worker.request('GET', socialPath); checkPNG(social);
  const socialHead = await worker.request('HEAD', socialPath);
  assert.equal(socialHead.status, 200); assert.equal(socialHead.body.length, 0);
  assert.equal(socialHead.headers.etag, social.headers.etag);
  assert.equal(Number(socialHead.headers['content-length']), social.body.length);
  const conditional = await worker.request('GET', socialPath, { 'if-none-match': social.headers.etag });
  assert.equal(conditional.status, 304); assert.equal(conditional.body.length, 0);
  const badSocial = await worker.request('GET', `${api('/api/social')}?src=https%3A%2F%2Fexample.invalid`);
  assert.equal(badSocial.status, 400); assert.equal(badSocial.headers['cache-control'], 'no-store');

  const message = '<script>alert("lambda")</script>&';
  const embed = await worker.request('GET', `${api('/api/embed')}?message=${encodeURIComponent(message)}`);
  const embedHTML = embed.body.toString('utf8');
  assert.equal(embed.status, 200);
  assert.equal(embed.headers['content-security-policy'], "default-src 'none'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'");
  assert.equal(embed.headers['referrer-policy'], 'no-referrer');
  assert.equal(embed.headers['cache-control'], 'no-store');
  assert.equal(embed.headers['x-content-type-options'], 'nosniff');
  assert.ok(embedHTML.includes('&lt;script&gt;alert(&#34;lambda&#34;)&lt;/script&gt;&amp;'));
  assert.ok(!embedHTML.includes('<script>') && embedHTML.includes('No referrer sent'));
  const embedHead = await worker.request('HEAD', api('/api/embed'));
  assert.equal(embedHead.status, 200); assert.equal(embedHead.body.length, 0);

  const runtime = `${scope.assets || scope.base}/_nextgo/${build}/`;
  const wasm = await worker.request('GET', runtime + 'app.wasm');
  assert.equal(wasm.status, 200); assert.match(wasm.headers['content-type'], /^application\/wasm/);
  assert.match(wasm.headers['cache-control'], /immutable/);
  assert.equal(wasm.body.subarray(0, 8).toString('hex'), '0061736d01000000');
  const modules = [...new Set(wasm.body.toString('latin1').match(/module-[a-f0-9]{64}\.wasm/g))];
  assert.ok(modules.length >= 2, 'both independently compiled demo lazy modules');
  for (const name of modules) {
    const module = await worker.request('GET', runtime + name);
    assert.equal(module.status, 200); assert.match(module.headers['cache-control'], /immutable/);
    assert.equal(module.body.subarray(0, 8).toString('hex'), '0061736d01000000');
    assert.equal(name, `module-${createHash('sha256').update(module.body).digest('hex')}.wasm`);
  }
  for (const [name, marker] of [['wasm_exec.js', 'globalThis.Go'], ['boot.js', 'app.wasm']]) {
    const support = await worker.request('GET', runtime + name);
    assert.equal(support.status, 200); assert.match(support.headers['content-type'], /javascript/);
    assert.match(support.headers['cache-control'], /immutable/); assert.ok(support.body.includes(Buffer.from(marker)));
  }
  for (const [name, magic] of [['embeds-demo.wav', '52494646'], ['embeds-demo.webm', '1a45dfa3']]) {
    const asset = await worker.request('GET', `${scope.assets || scope.base}/assets/${name}`);
    assert.equal(asset.status, 200); assert.equal(asset.body.subarray(0, 4).toString('hex'), magic);
  }
  const captions = await worker.request('GET', `${scope.assets || scope.base}/assets/embeds-demo.vtt`);
  assert.equal(captions.status, 200); assert.ok(captions.body.toString('utf8').startsWith('WEBVTT'));
  t.diagnostic(`runtime=${build} configuration=${configuration} lazyModules=${modules.length}`);
}

for (const mode of ['buffered', 'streaming']) {
  for (const scope of [{ base: '', assets: '', slash: false }, { base: '/seven-features', assets: '/seven-assets', slash: true }]) {
    test(`prebuilt seven-feature Lambda ${mode} ${scope.base || '/'}`, { skip: !binary && 'set NEXTGO_TEST_BINARY to a prebuilt demo server', timeout: 90_000 }, async t => {
      t.diagnostic(`binary=${binary} sha256=${await digestFile(binary)}`);
      const worker = await lambdaWorker(mode, scope);
      try { await checkFeatures(t, worker, scope); }
      finally { await worker.close(); }
    });
  }
}
