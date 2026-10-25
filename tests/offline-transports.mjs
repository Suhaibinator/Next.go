// Prebuilt-only production transport qualification. Node standard library only.
// This executes real native and Lambda Run entrypoints; it never compiles Go,
// starts a browser, retries an invocation, follows redirects, or contacts AWS.
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {readFile, writeFile, mkdir, lstat, realpath, open} from 'node:fs/promises';
import {constants as fsConstants} from 'node:fs';
import {request as httpRequest} from 'node:http';
import {spawn} from 'node:child_process';
import {resolve, dirname, relative, isAbsolute, join} from 'node:path';
import {gunzipSync} from 'node:zlib';
import {setTimeout as delay} from 'node:timers/promises';
import {startLambdaGateway} from './lambda-features-gateway.mjs';

const maxWire = 32 << 20, maxDecoded = 32 << 20;
const nativeAdmission = 'buffered-anonymous-document-v1';
const hex64 = /^[0-9a-f]{64}$/, runtimeID = /^[0-9a-f]{20}$/;
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const activeRequests = new Set();
let interrupted;
for (const signal of ['SIGTERM', 'SIGINT']) process.once(signal, () => {
  interrupted = new Error(`qualification interrupted by ${signal}`);
  for (const request of activeRequests) request.destroy(interrupted);
});

function options(argv) {
  const allowed = new Set(['--index', '--index-sha', '--out', '--base', '--generation', '--ports']);
  const result = {base: '', generation: 'A', ports: '4271,4272,4273'};
  for (let i = 0; i < argv.length; i += 2) {
    assert.ok(allowed.has(argv[i]) && i + 1 < argv.length, `unknown/missing option: ${argv[i]}`);
    const key = argv[i].slice(2);
    assert.equal(Object.hasOwn(result, `${key}Seen`), false, 'duplicate option');
    result[key] = argv[i + 1]; result[`${key}Seen`] = true;
  }
  assert.ok(isAbsolute(result.index || '') && isAbsolute(result.out || ''), 'absolute --index and --out required');
  assert.match(result['index-sha'] || '', hex64, 'explicit --index-sha required');
  assert.ok(result.base === '' || /^\/(?:[A-Za-z0-9_-]+)(?:\/[A-Za-z0-9_-]+)*$/.test(result.base), 'literal base required');
  assert.ok(['A', 'B', 'both'].includes(result.generation), 'generation A|B|both');
  result.ports = result.ports.split(',').map(Number);
  assert.ok(result.ports.length === 3 && new Set(result.ports).size === 3 && result.ports.every(port => Number.isSafeInteger(port) && port >= 1024 && port <= 65535), 'three distinct owned unprivileged ports required');
  return result;
}
function checkInterrupted() {if (interrupted) throw interrupted;}
async function regularFile(path, limit) {
  assert.ok(Number.isSafeInteger(limit) && limit > 0 && limit <= 128 << 20);
  const stat = await lstat(path);
  assert.ok(stat.isFile() && !stat.isSymbolicLink() && stat.size > 0 && stat.size <= limit, `bounded regular file required: ${path}`);
  assert.equal(await realpath(path), resolve(path), 'no symlink path components');
  const file = await open(path, fsConstants.O_RDONLY | fsConstants.O_NOFOLLOW);
  try {
    const opened = await file.stat();
    assert.ok(opened.isFile() && opened.dev === stat.dev && opened.ino === stat.ino && opened.size === stat.size && opened.mtimeMs === stat.mtimeMs && opened.ctimeMs === stat.ctimeMs, 'artifact changed while opening');
    const chunks = []; let used = 0;
    for (;;) {
      // At most limit+1 bytes are ever read, even if the opened file grows.
      const buffer = Buffer.allocUnsafe(Math.min(64 << 10, limit + 1 - used));
      const {bytesRead} = await file.read(buffer, 0, buffer.length, null);
      if (bytesRead === 0) break;
      used += bytesRead;
      assert.ok(used <= limit, 'artifact exceeded bounded read');
      chunks.push(buffer.subarray(0, bytesRead));
    }
    const after = await file.stat();
    assert.ok(after.size === opened.size && used === opened.size && after.mtimeMs === opened.mtimeMs && after.ctimeMs === opened.ctimeMs, 'artifact changed while reading');
    return Buffer.concat(chunks, used);
  } finally {await file.close();}
}
function literalLocalURL(url, origin) {
  const value = new URL(url);
  assert.equal(value.origin, origin, 'all resource requests belong to the declared loopback origin');
  assert.equal(value.username, ''); assert.equal(value.password, ''); assert.equal(value.hash, '');
  assert.equal(value.search, '');
  return value.pathname;
}
function raw(port, host, path, {method = 'GET', headers = {}} = {}) {
  checkInterrupted();
  assert.ok(path.startsWith('/') && !path.startsWith('//') && path.length <= 8192);
  return new Promise((resolveResponse, reject) => {
    const request = httpRequest({hostname: '127.0.0.1', port, path, method, agent: false, headers: {Host: host, 'Accept-Encoding': 'gzip', ...headers}, maxHeaderSize: 32 << 10}, response => {
      const chunks = []; let bytes = 0;
      response.on('data', chunk => {
        bytes += chunk.length;
        if (bytes > maxWire) response.destroy(new Error('bounded transport response exceeded'));
        else chunks.push(chunk);
      });
      response.once('error', reject);
      response.once('aborted', () => reject(new Error('truncated HTTP response')));
      response.once('end', () => {
        try {
          assert.equal(response.complete, true, 'complete HTTP response');
          const wire = Buffer.concat(chunks, bytes);
          const encoding = response.headers['content-encoding'] || '';
          assert.ok(encoding === '' || encoding === 'gzip', `unsupported encoding: ${encoding}`);
          const body = method === 'HEAD' ? wire : encoding === 'gzip' ? gunzipSync(wire, {maxOutputLength: maxDecoded}) : wire;
          assert.ok(body.length <= maxDecoded);
          assert.equal(response.headers['set-cookie'], undefined, 'anonymous transport must not set cookies');
          resolveResponse({status: response.statusCode, headers: response.headers, body, wireBytes: wire.length, encoding, decodedSHA256: hash(body)});
        } catch (error) {reject(error);}
      });
    });
    activeRequests.add(request);
    request.once('close', () => activeRequests.delete(request));
    request.once('error', reject);
    request.setTimeout(20_000, () => request.destroy(new Error('owned loopback request timeout')));
    request.end();
  });
}
function mime(response, type) {assert.equal(response.headers['content-type'], type);}
function publicNoCookies(response) {
  assert.equal(response.headers['set-cookie'], undefined);
  assert.equal(response.headers.location, undefined);
  assert.equal(response.headers['x-content-type-options'], 'nosniff');
}
function vary(response, required = false) {
  const values = String(response.headers.vary || '').split(',').map(value => value.trim().toLowerCase()).filter(Boolean);
  assert.ok(values.every(value => value === 'accept-encoding'), 'only delivery Accept-Encoding variation');
  if (required) assert.ok(values.includes('accept-encoding'), 'compression negotiates Accept-Encoding');
}
function etag(response, digest) {
  assert.equal(response.headers.etag, `${response.encoding === 'gzip' ? 'W/' : ''}"${digest}"`, 'ETag binds decoded bytes; gzip is weak');
}
function modeHeader(response, mode) {
  assert.equal(response.headers['x-nextgo-fixture-lambda-mode'], mode === 'native' ? undefined : mode, 'actual local Lambda transport proof');
}
function record(summary, label, response) {
  summary.responses.push({label, status: response.status, contentType: response.headers['content-type'] || null, encoding: response.encoding || 'identity', wireBytes: response.wireBytes, decodedBytes: response.body.length, decodedSHA256: response.decodedSHA256, etag: response.headers.etag || null, vary: response.headers.vary || null});
}
function unescapeAttribute(value) {
  return value.replace(/&(?:quot|#34|#x22|apos|#39|#x27|lt|gt|amp);/gi, entity => {
    const lower = entity.toLowerCase();
    if (['&quot;', '&#34;', '&#x22;'].includes(lower)) return '"';
    if (['&apos;', '&#39;', '&#x27;'].includes(lower)) return "'";
    return {'&lt;': '<', '&gt;': '>', '&amp;': '&'}[lower];
  });
}
function bodyAttribute(html, name) {
  const body = /<body\b[^>]*>/i.exec(html);
  assert.ok(body, 'SSR body exists');
  const matches = [...body[0].matchAll(new RegExp(`\\b${name}=(?:"([^"]*)"|'([^']*)')`, 'g'))];
  assert.equal(matches.length, 1, `one ${name} attribute`);
  return unescapeAttribute(matches[0][1] ?? matches[0][2]);
}
function descriptorFromHTML(html) {return JSON.parse(bodyAttribute(html, 'data-nextgo-offline'));}
function validateClient(client, origin, base, catalog) {
  assert.deepEqual(Object.keys(client).sort(), ['version', 'scope', 'workerURL', 'catalogURL', 'ownerPolicyID', 'runtimeID', 'configurationID'].sort());
  assert.equal(client.version, 1);
  assert.equal(client.scope, origin + (base || '/'));
  assert.equal(client.workerURL, origin + base + '/_nextgo/offline/sw.js');
  assert.equal(client.catalogURL, origin + base + '/_nextgo/offline/catalog');
  assert.equal(client.ownerPolicyID, 'nextgo-offline-demo');
  assert.match(client.runtimeID, runtimeID); assert.match(client.configurationID, hex64);
  if (catalog) {
    assert.equal(client.runtimeID, catalog.RuntimeID); assert.equal(client.configurationID, catalog.Configuration);
    assert.equal(client.ownerPolicyID, catalog.Config.ID);
  }
}
function extractBootstrap(script) {
  const marker = 'const D=', start = script.indexOf(marker);
  assert.ok(start >= 0 && script.indexOf(marker, start + marker.length) < 0, 'one frozen worker bootstrap');
  let cursor = start + marker.length, depth = 0, quoted = false, escaped = false;
  const jsonStart = cursor;
  assert.equal(script[cursor], '{');
  for (; cursor < script.length; cursor++) {
    const char = script[cursor];
    if (quoted) {if (escaped) escaped = false; else if (char === '\\') escaped = true; else if (char === '"') quoted = false; continue;}
    if (char === '"') quoted = true;
    else if (char === '{' || char === '[') depth++;
    else if (char === '}' || char === ']') {depth--; if (depth === 0) {cursor++; break;}}
  }
  assert.equal(depth, 0); assert.equal(quoted, false);
  assert.ok(script.slice(cursor).startsWith(', C=JSON.parse(D.catalogJSON)'), 'literal bootstrap boundary');
  return {descriptor: JSON.parse(script.slice(jsonStart, cursor)), templateSHA256: hash(script.slice(0, jsonStart) + '__BOOTSTRAP__' + script.slice(cursor))};
}
function docPath(base, path) {return base + (path === '/' && base ? '' : path);}
function safeEnvironment(origin, base, runtimeRoot) {
  // Intentionally no process.env read: no cloud/credential/env-file inheritance.
  return {PATH: '/usr/local/bin:/usr/bin:/bin', HOME: join(runtimeRoot, 'home'), TMPDIR: join(runtimeRoot, 'tmp'), LANG: 'C', TZ: 'UTC', GOMAXPROCS: '1', GOMEMLIMIT: '384MiB', NEXTGO_OFFLINE_ORIGIN: origin, NEXTGO_OFFLINE_BASE_PATH: base};
}
async function nativeServer(binary, port, environment, runtimeRoot) {
  let logs = '', exitResult, stopping = false, joined = false;
  const child = spawn(binary, ['-addr', `127.0.0.1:${port}`, '-env-dir', join(runtimeRoot, 'empty-env')], {cwd: runtimeRoot, env: environment, shell: false, detached: true, stdio: ['ignore', 'pipe', 'pipe']});
  const append = chunk => {logs = (logs + chunk.toString('utf8')).slice(-32_768);};
  child.stdout.on('data', append); child.stderr.on('data', append);
  const exited = new Promise(resolveExit => {
    child.once('error', error => {exitResult = {error: error.message}; resolveExit(exitResult);});
    child.once('exit', (code, signal) => {exitResult = {code, signal}; resolveExit(exitResult);});
  });
  return {
    snapshot: () => ({childPID: child.pid, stopping, joined, exit: exitResult ? {...exitResult} : null, logTail: logs}),
    close: async () => {
      if (stopping) return; stopping = true;
      if (!exitResult) child.kill('SIGTERM');
      let timer;
      const result = await Promise.race([exited, new Promise(resolveExit => {timer = setTimeout(() => {child.kill('SIGKILL'); resolveExit({timeout: true});}, 6000);})]);
      clearTimeout(timer); if (result.timeout) await exited;
      joined = true;
      assert.equal(result.timeout, undefined, 'native shutdown joins without forced kill');
      assert.equal(result.error, undefined, 'native startup');
      assert.equal(result.code, 0, `native exit: ${logs}`);
    },
  };
}
async function waitHealth(port, host, base, owner) {
  const deadline = Date.now() + 20_000;
  let lastError;
  while (Date.now() < deadline) {
    checkInterrupted();
    const state = owner.snapshot();
    assert.equal(state.exit, null, `child exited before ready: ${JSON.stringify(state)}`);
    try {
      const response = await raw(port, host, base + '/api/health');
      assert.equal(response.status, 200); assert.equal(response.body.toString(), '{"ok":true,"source":"live-network-only"}\n');
      assert.equal(response.headers['cache-control'], 'no-store');
      mime(response, 'application/json; charset=utf-8');
      return response;
    } catch (error) {lastError = error; await delay(100);}
  }
  throw new Error(`real application health readiness failed: ${lastError?.message}`);
}
async function assertClosed(port, host) {
  await assert.rejects(raw(port, host, '/'), error => error.code === 'ECONNREFUSED', 'owned listening socket closed');
}

async function inspectMode({binary, generation, mode, port, origin, host, base, out}) {
  const runtimeRoot = join(out, `${generation}-${mode}-runtime`);
  for (const directory of [runtimeRoot, join(runtimeRoot, 'home'), join(runtimeRoot, 'tmp'), join(runtimeRoot, 'empty-env')]) await mkdir(directory, {recursive: true, mode: 0o700});
  const env = safeEnvironment(origin, base, runtimeRoot);
  const summary = {generation, mode, port, runtimeRoot, startedAt: new Date().toISOString(), responses: [], passed: false};
  let owner, result, failure;
  try {
    owner = mode === 'native' ? await nativeServer(binary, port, env, runtimeRoot) : await startLambdaGateway({binary, mode, port, allowedHosts: [host], workerArgs: ['-env-dir', join(runtimeRoot, 'empty-env')], trustedEnvironment: env});
    const health = await waitHealth(port, host, base, owner); modeHeader(health, mode); record(summary, 'live-health', health);
    const get = async (path, label, requestOptions) => {const response = await raw(port, host, path, requestOptions); modeHeader(response, mode); record(summary, label, response); return response;};
    const home = await get(docPath(base, '/'), 'ordinary-home');
    assert.equal(home.status, 200); mime(home, 'text/html; charset=utf-8');
    const homeHTML = home.body.toString('utf8');
    assert.match(homeHTML, /A small public reading shelf\./);
    assert.match(homeHTML, /data-offline-count[^>]*>3<\/output>/);
    assert.equal(homeHTML.includes('data-nextgo-offline-saved='), false, 'normal SSR is not saved-document mode');
    const client = descriptorFromHTML(homeHTML); validateClient(client, origin, base);
    assert.equal(bodyAttribute(homeHTML, 'data-nextgo-build'), client.runtimeID);
    assert.equal(bodyAttribute(homeHTML, 'data-nextgo-configuration'), client.configurationID);

    const catalogResponse = await get(base + '/_nextgo/offline/catalog', 'catalog');
    assert.equal(catalogResponse.status, 200); mime(catalogResponse, 'application/json; charset=utf-8'); publicNoCookies(catalogResponse); vary(catalogResponse, mode !== 'native');
    assert.equal(catalogResponse.headers['cache-control'], 'public, max-age=0, must-revalidate');
    etag(catalogResponse, catalogResponse.decodedSHA256);
    const catalogJSON = catalogResponse.body.toString('utf8'), catalog = JSON.parse(catalogJSON);
    assert.equal(catalog.Version, 1); assert.equal(catalog.Admission, nativeAdmission);
    assert.deepEqual(Object.keys(catalog).sort(), ['Version', 'Config', 'Admission', 'RuntimeID', 'Configuration', 'SavedAtUnix', 'Assets', 'Documents'].sort());
    assert.equal(catalog.Config.Origin, origin); assert.equal(catalog.Config.BasePath, base);
    validateClient(client, origin, base, catalog);
    assert.ok(Number.isSafeInteger(catalog.SavedAtUnix) && catalog.SavedAtUnix >= Math.floor(Date.parse(summary.startedAt) / 1000) - 5 && catalog.SavedAtUnix <= Math.floor(Date.now() / 1000) + 5, 'startup saved-at timestamp');
    assert.deepEqual(catalog.Documents.map(document => document.Path).sort(), ['/', '/guide', '/offline']);
    assert.ok(Array.isArray(catalog.Assets) && catalog.Assets.length >= 6 && catalog.Assets.length <= 256);
    assert.equal(new Set(catalog.Assets.map(asset => asset.URL)).size, catalog.Assets.length);
    for (const kind of ['core-wasm', 'worker-wasm', 'runtime-shim', 'boot-script']) assert.equal(catalog.Assets.filter(asset => asset.Kind === kind).length, 1, `one required ${kind}`);
    assert.ok(catalog.Assets.some(asset => asset.Kind === 'lazy-wasm'), 'actual sample lazy bundle');
    assert.ok(catalog.Assets.some(asset => asset.Kind === 'style'));

    const scriptResponse = await get(base + '/_nextgo/offline/sw.js', 'worker-script');
    assert.equal(scriptResponse.status, 200); mime(scriptResponse, 'text/javascript; charset=utf-8'); publicNoCookies(scriptResponse); vary(scriptResponse, mode !== 'native');
    assert.equal(scriptResponse.headers['service-worker-allowed'], base || '/');
    assert.equal(scriptResponse.headers['cache-control'], 'public, max-age=0, must-revalidate');
    etag(scriptResponse, scriptResponse.decodedSHA256);
    const script = scriptResponse.body.toString('utf8');
    assert.equal(script.includes('__NEXTGO_OFFLINE_DESCRIPTOR__'), false); assert.equal(script.includes('__NEXTGO_OFFLINE_SHIM__'), false);
    const bootstrap = extractBootstrap(script), worker = bootstrap.descriptor;
    assert.deepEqual(Object.keys(worker).sort(), ['version', 'client', 'generation', 'catalogJSON', 'catalogSHA256', 'namespace'].sort());
    assert.equal(worker.version, 1); assert.deepEqual(worker.client, client); assert.match(worker.generation, hex64);
    assert.equal(worker.catalogJSON, catalogJSON); assert.equal(worker.catalogSHA256, catalogResponse.decodedSHA256);
    assert.match(worker.namespace, /^nextgo-offline-v1-[0-9a-f]{64}$/);
    summary.runtimeID = client.runtimeID; summary.configurationID = client.configurationID;
    summary.catalogSHA256 = catalogResponse.decodedSHA256; summary.catalogGeneration = worker.generation;
    summary.scriptSHA256 = scriptResponse.decodedSHA256; summary.workerTemplateSHA256 = bootstrap.templateSHA256;
    for (const [path, label, type, size, digest] of [
      [base + '/_nextgo/offline/catalog', 'catalog-head', 'application/json; charset=utf-8', catalogResponse.body.length, catalogResponse.decodedSHA256],
      [base + '/_nextgo/offline/sw.js', 'worker-script-head', 'text/javascript; charset=utf-8', scriptResponse.body.length, scriptResponse.decodedSHA256],
    ]) {
      const head = await get(path, label, {method: 'HEAD'});
      assert.equal(head.status, 200); assert.equal(head.body.length, 0); assert.equal(head.encoding, '');
      mime(head, type); etag(head, digest); assert.equal(Number(head.headers['content-length']), size);
      assert.equal(head.headers['cache-control'], 'public, max-age=0, must-revalidate');
      if (label === 'worker-script-head') assert.equal(head.headers['service-worker-allowed'], base || '/');
    }

    for (const asset of catalog.Assets) {
      assert.match(asset.SHA256, hex64);
      assert.ok(Number.isSafeInteger(asset.DecodedBytes) && asset.DecodedBytes > 0 && asset.DecodedBytes <= maxDecoded);
      const path = literalLocalURL(asset.URL, origin);
      assert.ok(path.startsWith(base + '/_nextgo/' + client.runtimeID + '/'), 'immutable runtime asset ownership');
      const response = await get(path, `asset:${asset.Kind}:${path}`);
      assert.equal(response.status, 200); mime(response, asset.ContentType); publicNoCookies(response); vary(response, true);
      assert.equal(response.headers['cache-control'], 'public, max-age=31536000, immutable');
      assert.equal(response.body.length, asset.DecodedBytes); assert.equal(response.decodedSHA256, asset.SHA256); etag(response, asset.SHA256);
      if (asset.Kind.endsWith('-wasm')) {
        assert.deepEqual(response.body.subarray(0, 8), Buffer.from([0, 97, 115, 109, 1, 0, 0, 0]));
        assert.equal(response.encoding, 'gzip', 'real production WASM transport is compressed');
        assert.ok(response.wireBytes < 4 << 20, 'actual compressed WASM fits buffered adapter wire budget');
      }
      if (asset.Kind === 'runtime-shim') assert.ok(script.includes(response.body.toString('utf8')), 'worker contains compiler-matching shim bytes');
      const head = await get(path, `asset-head:${asset.Kind}`, {method: 'HEAD'});
      assert.equal(head.status, 200); assert.equal(head.body.length, 0); mime(head, asset.ContentType);
      assert.equal(head.encoding, ''); assert.equal(Number(head.headers['content-length']), asset.DecodedBytes); etag(head, asset.SHA256); vary(head, true);
    }
    for (const document of catalog.Documents) {
      assert.match(document.SHA256, hex64); assert.ok(Number.isSafeInteger(document.DecodedBytes) && document.DecodedBytes > 0 && document.DecodedBytes <= 512 << 10);
      const source = base + '/_nextgo/offline/documents/' + worker.generation + '/' + document.SHA256;
      const response = await get(source, `prepared-document:${document.Path}`);
      assert.equal(response.status, 200); mime(response, 'text/html; charset=utf-8'); publicNoCookies(response); vary(response, mode !== 'native');
      assert.equal(response.headers['cache-control'], 'no-store'); assert.equal(response.headers['x-nextgo-offline-admission'], nativeAdmission);
      assert.equal(response.headers['x-nextgo-offline-document'], document.SHA256); assert.equal(response.headers['x-nextgo-offline-generation'], worker.generation);
      assert.equal(response.headers['x-nextgo-build'], client.runtimeID); assert.equal(response.headers['x-nextgo-configuration'], client.configurationID);
      assert.equal(response.decodedSHA256, document.SHA256); assert.equal(response.body.length, document.DecodedBytes); etag(response, document.SHA256);
      const html = response.body.toString('utf8');
      if (document.Path === catalog.Config.Fallback) {
        assert.match(html, /Offline reading fallback/);
        assert.doesNotMatch(html, /<(?:script|style|link|img|iframe|object)\b|data-nextgo-/i, 'saved fallback is deliberately inert');
      } else {
        assert.equal(bodyAttribute(html, 'data-nextgo-offline-saved'), '1');
        assert.deepEqual(descriptorFromHTML(html), client);
        if (document.Path === '/guide') assert.match(html, /Read the (?:updated )?saved edition deliberately\./);
      }
      const head = await get(source, `prepared-head:${document.Path}`, {method: 'HEAD'});
      assert.equal(head.status, 200); assert.equal(head.body.length, 0); assert.equal(head.headers['x-nextgo-offline-admission'], nativeAdmission);
      assert.equal(Number(head.headers['content-length']), document.DecodedBytes); assert.equal(head.headers['cache-control'], 'no-store');
      if (document.Path === '/') {
        const ordinary = await get(docPath(base, '/'), 'ordinary-home-selector-does-not-vary', {headers: {'X-Nextgo-Offline-Prepare': '1', 'X-Nextgo-Offline-Generation': worker.generation}});
        assert.equal(ordinary.status, 200); assert.equal(ordinary.headers['x-nextgo-offline-admission'], undefined);
        assert.equal(ordinary.body.includes(Buffer.from('data-nextgo-offline-saved=')), false);
        const negativeCases = [
          [source, {Cookie: 'example=1'}, 400, 'cookie'],
          [source, {Authorization: 'Bearer fixture'}, 400, 'authorization'],
          [source, {Range: 'bytes=0-9'}, 400, 'range'],
          [source, {'X-Nextgo-Navigate': '1'}, 400, 'navigation'],
          [source + '?variant=1', {}, 400, 'query'],
          [source.replace(worker.generation, '0'.repeat(64)), {}, 404, 'unknown-generation'],
          [source.replace(document.SHA256, '0'.repeat(64)), {}, 404, 'unknown-document'],
          [source.replace('/_nextgo/offline/', '/%5fnextgo/offline/'), {}, 404, 'encoded-reserved-namespace'],
        ];
        for (const [path, headers, status, name] of negativeCases) {
          const negative = await get(path, `source-refused:${name}`, {headers});
          assert.equal(negative.status, status); assert.equal(negative.headers['x-nextgo-offline-admission'], undefined);
          assert.equal(negative.headers['cache-control'], 'no-store');
        }
      }
    }
    result = {client, ordinaryHomeSHA256: home.decodedSHA256, liveHealthSHA256: health.decodedSHA256, catalog: {...catalog, SavedAtUnix: '__per_startup_time__'}, workerTemplateSHA256: bootstrap.templateSHA256, namespace: worker.namespace};
    summary.passed = true;
  } catch (error) {failure = error; summary.error = {name: error.name, message: error.message, stack: error.stack};}
  finally {
    if (owner) {
      try {await owner.close(); await assertClosed(port, host);} catch (error) {summary.closeError = {message: error.message, stack: error.stack}; failure ||= error; summary.passed = false;}
      summary.lifecycle = owner.snapshot();
      if (mode !== 'native') {
        try {
          assert.ok(summary.lifecycle.joined && summary.lifecycle.exit?.code === 0);
          assert.equal(summary.lifecycle.failures.length, 0, 'no local integration failures');
          assert.equal(summary.lifecycle.droppedReports, 0, 'all Runtime API response observations retained');
          assert.equal(summary.lifecycle.droppedFailures, 0, 'all local failure observations retained');
          assert.ok(summary.lifecycle.invocations < 128, 'fixed transport suite stays inside observation capacity');
          assert.equal(summary.lifecycle.queued, 0); assert.equal(summary.lifecycle.active, null);
          assert.ok(summary.lifecycle.reports.length >= summary.responses.length, 'every observed application response originates in Runtime API report');
          if (mode === 'buffered') for (const report of summary.lifecycle.reports) {
            assert.ok(report.runtimeBytes <= 6 << 20, 'real buffered response JSON envelope stays under Lambda payload budget');
            assert.ok(report.httpBytes <= 4 << 20, 'real buffered response wire body stays under adapter budget');
          }
        } catch (error) {summary.lifecycleError = {message: error.message, stack: error.stack}; failure ||= error; summary.passed = false;}
      }
    }
    summary.finishedAt = new Date().toISOString();
    await writeFile(join(out, `${generation}-${mode}.json`), JSON.stringify(summary, null, 2) + '\n', {mode: 0o600});
  }
  if (failure) throw failure;
  return result;
}

async function main() {
  const opts = options(process.argv.slice(2));
  assert.equal(process.platform, 'linux', 'prebuilt artifacts require Linux'); assert.equal(process.arch, 'arm64', 'prebuilt artifacts require arm64');
  assert.ok(Number(process.versions.node.split('.')[0]) >= 22, 'Node 22+ required');
  // Preserve prior evidence and ensure the explicit env directory is new.
  await mkdir(opts.out, {recursive: false, mode: 0o700});
  const summary = {version: 1, scope: 'actual prebuilt native HTTP and LOCAL/mock Lambda Run buffered/streaming transport; no AWS/browser/compiler', index: opts.index, indexSHA256: opts['index-sha'], basePath: opts.base, origin: null, ports: opts.ports, generations: [], startedAt: new Date().toISOString(), passed: false};
  try {
    summary.harness = {mainSHA256: hash(await readFile(new URL(import.meta.url))), gatewaySHA256: hash(await readFile(new URL('./lambda-features-gateway.mjs', import.meta.url)))};
    const indexBytes = await regularFile(opts.index, 64 << 10); assert.equal(hash(indexBytes), opts['index-sha']);
    const index = JSON.parse(indexBytes.toString('utf8')); assert.equal(index.version, 1);
    const origin = new URL(index.origin);
    assert.equal(origin.protocol, 'http:'); assert.equal(origin.hostname, '127.0.0.1'); assert.equal(origin.href, index.origin + '/');
    assert.ok(origin.port && !opts.ports.includes(Number(origin.port)), 'logical origin is distinct from owned transport sockets');
    summary.origin = index.origin;
    const indexRoot = dirname(opts.index), generations = opts.generation === 'both' ? ['A', 'B'] : [opts.generation];
    for (const generation of generations) {
      const entry = index[generation]; assert.ok(entry && typeof entry.binary === 'string' && !isAbsolute(entry.binary)); assert.match(entry.binarySHA256, hex64);
      const binary = resolve(indexRoot, entry.binary), relation = relative(indexRoot, binary);
      assert.ok(relation && !relation.startsWith('..') && !isAbsolute(relation), 'index-local binary only');
      const bytes = await regularFile(binary, 128 << 20);
      assert.equal(hash(bytes), entry.binarySHA256); assert.deepEqual(bytes.subarray(0, 4), Buffer.from([0x7f, 0x45, 0x4c, 0x46]));
      assert.equal(bytes[4], 2); assert.equal(bytes[5], 1); assert.equal(bytes.readUInt16LE(18), 183, 'Linux arm64 ELF executable');
      const evidence = {generation, binary, binarySHA256: entry.binarySHA256, modes: []}; summary.generations.push(evidence);
      let reference;
      for (const [index, mode] of ['native', 'buffered', 'streaming'].entries()) {
        const result = await inspectMode({binary, generation, mode, port: opts.ports[index], origin: summary.origin, host: origin.host, base: opts.base, out: opts.out});
        if (reference) assert.deepEqual(result, reference, 'decoded public catalog/doc/asset/descriptor identities equal across transports; only startup SavedAt/generation excluded');
        else reference = result;
        evidence.modes.push({mode, passed: true});
      }
      assert.equal(hash(await regularFile(binary, 128 << 20)), entry.binarySHA256, 'prebuilt binary unchanged after execution');
      evidence.runtimeID = reference.client.runtimeID; evidence.configurationID = reference.client.configurationID;
      evidence.documentIdentities = reference.catalog.Documents; evidence.assetIdentities = reference.catalog.Assets;
    }
    assert.equal(hash(await regularFile(opts.index, 64 << 10)), opts['index-sha'], 'index unchanged after execution');
    summary.passed = true;
  } catch (error) {summary.error = {name: error.name, message: error.message, stack: error.stack}; process.exitCode = 1;}
  summary.finishedAt = new Date().toISOString();
  await writeFile(join(opts.out, 'summary.json'), JSON.stringify(summary, null, 2) + '\n', {mode: 0o600});
  console.log(JSON.stringify({passed: summary.passed, summary: join(opts.out, 'summary.json'), generations: summary.generations.map(entry => ({generation: entry.generation, modes: entry.modes, runtimeID: entry.runtimeID}))}));
}
await main();
