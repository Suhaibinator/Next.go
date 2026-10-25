// Development-only local HTTP -> actual prebuilt Lambda executable bridge.
// It preserves application bytes and never retries an invocation.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';

const requestLimit = 1 << 20, transportLimit = 8 << 20, preludeLimit = 16 << 10;
const queueLimit = 32, invocationTimeout = 15_000;
const excludedHeaders = new Set(['connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te', 'trailer', 'transfer-encoding', 'upgrade']);

export async function startLambdaGateway({binary, mode, port, allowedHosts, workerArgs = []}) {
  // Test operators may opt into a small exact authority set. This does not
  // resolve those names or trust forwarded headers; all sockets stay loopback.
  // Capture a copy before any async startup so callers cannot widen it later.
  const requestedHosts = allowedHosts === undefined ? [`127.0.0.1:${port}`] : allowedHosts;
  assert.ok(Array.isArray(requestedHosts) && requestedHosts.length > 0 && requestedHosts.length <= 32, 'invalid fixture allowedHosts');
  const hosts = Array.from({length: requestedHosts.length}, (_, index) => requestedHosts[index]);
  const authority = /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?::[1-9][0-9]{0,4})?$/;
  assert.ok(hosts.every(host => typeof host === 'string' && host.length <= 259 && authority.test(host) && (!host.includes(':') || Number(host.slice(host.lastIndexOf(':') + 1)) <= 65535)), 'invalid fixture allowedHosts');
  assert.ok(hosts.reduce((bytes, host) => bytes + host.length, 0) <= 4096 && new Set(hosts).size === hosts.length, 'invalid fixture allowedHosts');
  const acceptedHosts = new Set(hosts);
  // Optional trusted literal argv permits a Node echo worker to read a script
  // from noexec temporary storage. Production executables keep empty argv.
  assert.ok(Array.isArray(workerArgs) && workerArgs.length <= 16, 'invalid fixture workerArgs');
  const args = Array.from({length: workerArgs.length}, (_, index) => workerArgs[index]);
  assert.ok(args.every(arg => typeof arg === 'string' && arg.length <= 4096 && !arg.includes('\0')) && args.reduce((bytes, arg) => bytes + Buffer.byteLength(arg), 0) <= 4096, 'invalid fixture workerArgs');
  let poll, active, sequence = 0, stopping = false, logs = '';
  const queue = [], sockets = new Set();
  const environment = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith('AWS_LAMBDA_') && !key.startsWith('NEXTGO_LAMBDA_') && !key.startsWith('NEXTGO_DEMO_') && key !== 'NEXTGO_ENV_DIR'));
  function rejectHTTP(response, status, message) {
    if (response.destroyed || response.writableEnded) return;
    if (response.headersSent) response.destroy();
    else { response.writeHead(status, {'Content-Type': 'text/plain', 'Cache-Control': 'no-store'}); response.end(message); }
  }
  function complete(item) { clearTimeout(item.timer); if (active === item) active = undefined; }
  function dispatch() {
    if (!poll || active || !queue.length || stopping) return;
    active = queue.shift();
    if (active.response.destroyed) { complete(active); dispatch(); return; }
    const response = poll; poll = undefined;
    response.writeHead(200, {'Content-Type': 'application/json', 'Lambda-Runtime-Aws-Request-Id': active.id, 'Lambda-Runtime-Deadline-Ms': String(Date.now() + invocationTimeout - 1000)});
    response.end(JSON.stringify(active.event));
  }
  function beginResponse(item, metadata) {
    assert.ok(Number.isInteger(metadata.statusCode) && metadata.statusCode >= 200 && metadata.statusCode <= 599, 'integration HTTP status');
    const headers = {};
    for (const [key, value] of Object.entries(metadata.headers || {})) {
      assert.equal(typeof value, 'string');
      if (!excludedHeaders.has(key.toLowerCase())) headers[key] = value;
    }
    if (metadata.cookies?.length) headers['Set-Cookie'] = metadata.cookies;
    headers['X-Nextgo-Fixture-Lambda-Mode'] = mode;
    if (!item.response.destroyed && !item.response.writableEnded) item.response.writeHead(metadata.statusCode, headers);
  }
  const runtime = createServer(async (request, response) => {
    const item = active;
    try {
      if (request.method === 'GET' && request.url === '/2018-06-01/runtime/invocation/next') {
        assert.equal(poll, undefined, 'one runtime poll'); poll = response;
        response.once('close', () => {if (poll === response) poll = undefined;});
        dispatch(); return;
      }
      assert.ok(item, 'runtime response owns an invocation');
      assert.equal(request.method, 'POST');
      assert.equal(request.url, `/2018-06-01/runtime/invocation/${item.id}/response`);
      assert.equal(request.headers['lambda-runtime-function-response-mode'] || 'buffered', mode);
      let used = 0, prefix = Buffer.alloc(0), begun = false;
      const chunks = [];
      for await (const chunk of request) {
        used += chunk.length;
        assert.ok(used <= transportLimit, 'runtime response cap');
        if (mode === 'buffered') {chunks.push(chunk); continue;}
        if (!begun) {
          prefix = Buffer.concat([prefix, chunk]);
          const boundary = prefix.indexOf(Buffer.alloc(8));
          if (boundary < 0) {assert.ok(prefix.length <= preludeLimit + 7, 'stream prelude cap'); continue;}
          assert.ok(boundary > 0 && boundary <= preludeLimit, 'stream prelude cap');
          beginResponse(item, JSON.parse(prefix.subarray(0, boundary).toString('utf8')));
          begun = true;
          if (!item.response.destroyed && !item.response.writableEnded) item.response.write(prefix.subarray(boundary + 8));
          prefix = Buffer.alloc(0);
        } else if (!item.response.destroyed && !item.response.writableEnded) item.response.write(chunk);
      }
      assert.equal(request.trailers['lambda-runtime-function-error-type'], undefined, 'no late Lambda stream error');
      if (mode === 'buffered') {
        const metadata = JSON.parse(Buffer.concat(chunks, used).toString('utf8'));
        const body = Buffer.from(metadata.body, metadata.isBase64Encoded ? 'base64' : 'utf8');
        assert.ok(body.length <= transportLimit, 'HTTP body cap');
        beginResponse(item, metadata);
        if (!item.response.destroyed && !item.response.writableEnded) item.response.end(body);
      } else {
        assert.ok(begun, 'stream prelude present');
        if (!item.response.destroyed && !item.response.writableEnded) item.response.end();
      }
      complete(item);
      response.writeHead(202); response.end();
    } catch (error) {
      if (item) {rejectHTTP(item.response, 502, 'Local Lambda integration failed'); complete(item);}
      console.error(`${mode}: ${error.message}`);
      if (!response.headersSent) response.writeHead(500);
      response.end();
    }
  });
  runtime.maxConnections = 4; runtime.maxHeadersCount = 64; runtime.headersTimeout = 5000;
  await new Promise((resolve, reject) => {runtime.once('error', reject); runtime.listen(0, '127.0.0.1', resolve);});
  // Playwright terminates the fixture's entire process group. Give each worker
  // its own group so close() sends exactly one signal and awaits physical exit.
  const child = spawn(binary, args, {cwd: '/', shell: false, detached: true, stdio: ['ignore', 'pipe', 'pipe'], env: {
    ...environment, AWS_LAMBDA_RUNTIME_API: `127.0.0.1:${runtime.address().port}`, NEXTGO_LAMBDA_RESPONSE_MODE: mode,
    NEXTGO_DEMO_BASE_PATH: '/lambda-features', NEXTGO_DEMO_ASSET_PREFIX: '/lambda-assets', NEXTGO_DEMO_CSP: 'true',
    NEXTGO_DEMO_COOKIE_ALLOW_INSECURE: 'true',
  }});
  const append = data => {logs = (logs + data.toString('utf8')).slice(-32_768);};
  child.stdout.on('data', append); child.stderr.on('data', append);
  const exited = new Promise(resolve => {
    child.once('error', error => {logs += error.message; resolve({error});});
    child.once('exit', (code, signal) => {
      if (!stopping) {
        console.error(`${mode} worker exited ${code}/${signal}: ${logs}`);
        if (active) rejectHTTP(active.response, 502, 'Local Lambda worker exited');
        for (const queued of queue) rejectHTTP(queued.response, 502, 'Local Lambda worker exited');
      }
      resolve({code, signal});
    });
  });
  const gateway = createServer({maxHeaderSize: 16 << 10}, async (request, response) => {
    try {
      if (stopping) {rejectHTTP(response, 503, 'Fixture stopping'); return;}
      if (!acceptedHosts.has(request.headers.host) || !request.url.startsWith('/') || request.url.startsWith('//') || request.url.length > 8192) {rejectHTTP(response, 400, 'Invalid local authority or URL'); return;}
      if (request.url === '/_fixture/lambda/ready') {response.writeHead(200, {'Content-Type': 'text/plain', 'Cache-Control': 'no-store'}); response.end(mode); return;}
      if (queue.length >= queueLimit) {rejectHTTP(response, 503, 'Local Lambda queue full'); return;}
      const chunks = []; let size = 0;
      for await (const chunk of request) {size += chunk.length; if (size > requestLimit) {rejectHTTP(response, 413, 'Fixture request too large'); return;} chunks.push(chunk);}
      if (response.destroyed || stopping) return;
      if (queue.length >= queueLimit) {rejectHTTP(response, 503, 'Local Lambda queue full'); return;}
      const url = new URL(request.url, `http://${request.headers.host}`), headers = {};
      for (const [key, value] of Object.entries(request.headers)) {
        if (key !== 'cookie' && !excludedHeaders.has(key)) headers[key] = Array.isArray(value) ? value.join(',') : value;
      }
      // Trusted fixture transport metadata derives from the real socket, not
      // any browser-supplied forwarding header or an invented HTTPS origin.
      headers.host = request.headers.host;
      headers['x-forwarded-proto'] = request.socket.encrypted ? 'https' : 'http';
      const body = Buffer.concat(chunks, size);
      const item = {id: String(++sequence), response, event: {
        version: '2.0', rawPath: url.pathname, rawQueryString: url.search.slice(1), headers,
        cookies: request.headers.cookie ? [request.headers.cookie] : [], body: body.toString('base64'), isBase64Encoded: true,
        requestContext: {domainName: request.headers.host, requestId: String(sequence), http: {method: request.method, path: url.pathname, protocol: 'HTTP/1.1', sourceIp: '127.0.0.1'}},
      }};
      item.timer = setTimeout(() => {
        const index = queue.indexOf(item); if (index >= 0) queue.splice(index, 1);
        rejectHTTP(response, 504, 'Local Lambda invocation timed out');
        // Active work keeps its physical worker until its report finishes.
      }, invocationTimeout);
      response.once('close', () => {const index = queue.indexOf(item); if (index >= 0) {queue.splice(index, 1); clearTimeout(item.timer);}});
      queue.push(item); dispatch();
    } catch (error) {rejectHTTP(response, 502, 'Local Lambda gateway failed'); console.error(error.message);}
  });
  gateway.maxConnections = 64; gateway.maxHeadersCount = 64; gateway.headersTimeout = 5000;
  gateway.requestTimeout = invocationTimeout; gateway.timeout = invocationTimeout + 2000;
  gateway.on('connection', socket => {sockets.add(socket); socket.once('close', () => sockets.delete(socket));});
  async function close() {
    if (stopping) return; stopping = true;
    for (const item of queue.splice(0)) {clearTimeout(item.timer); rejectHTTP(item.response, 503, 'Fixture stopping');}
    if (active) {clearTimeout(active.timer); rejectHTTP(active.response, 503, 'Fixture stopping');}
    child.kill('SIGTERM'); let timer;
    const result = await Promise.race([exited, new Promise(resolve => {timer = setTimeout(() => {child.kill('SIGKILL'); resolve({timeout: true});}, 6000);})]);
    clearTimeout(timer); if (result.timeout) await exited;
    for (const socket of sockets) socket.destroy();
    runtime.closeAllConnections();
    await Promise.all([new Promise(resolve => runtime.close(resolve)), new Promise(resolve => gateway.close(resolve))]);
    assert.equal(result.timeout, undefined, `${mode} worker graceful shutdown`);
    assert.equal(result.error, undefined, `${mode} worker startup`);
    assert.equal(result.code, 0, `${mode} worker exit: ${logs}`);
  }
  try {
    await new Promise((resolve, reject) => {gateway.once('error', reject); gateway.listen(port, '127.0.0.1', resolve);});
  } catch (error) {await close(); throw error;}
  return {close};
}
