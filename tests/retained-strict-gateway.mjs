// Private, source-only qualification helper. Synthetic tests do not qualify a
// production Go artifact. The worker factory alone owns process authority.
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { TextDecoder } from 'node:util';

export const retainedStrictLimits = Object.freeze({request: 1 << 20, event: 6 << 20,
  envelope: 6 << 20, bufferedBody: 4 << 20, metadata: 16 << 10, streamBody: 32 << 20,
  error: 64 << 10, queue: 32, reports: 128, failures: 32, observations: 128,
  admissionMS: 15_000, deadlineMS: 14_000, cleanupMS: 6_000, finalJoinMS: 2_000});
const policy = 'retained-strict-v1', nextPath = '/2018-06-01/runtime/invocation/next';
const excludedHeaders = new Set(['connection', 'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te', 'trailer', 'transfer-encoding', 'upgrade']);
const nulBoundary = Buffer.alloc(8);
const plain = value => value && Object.getPrototypeOf(value) === Object.prototype;
const parseJSON = bytes => JSON.parse(new TextDecoder('utf-8', {fatal: true}).decode(bytes));
function validateOptions({binary, mode, port, allowedHosts, workerArgs, trustedEnvironment, workerFactory}) {
  assert.ok(typeof binary === 'string' && binary.length > 0 && !binary.includes('\0'), 'strict binary');
  assert.ok(mode === 'buffered' || mode === 'streaming', 'strict response mode');
  assert.ok(Number.isInteger(port) && port >= 0 && port <= 65535, 'strict port');
  assert.equal(typeof workerFactory, 'function', 'strict workerFactory required');
  assert.ok(plain(trustedEnvironment), 'strict trustedEnvironment required');
  const entries = Object.entries(trustedEnvironment);
  assert.ok(entries.length <= 64 && entries.every(([key, value]) => /^[A-Z][A-Z0-9_]*$/.test(key) && typeof value === 'string' && value.length <= 8192 && !value.includes('\0')), 'strict environment strings');
  assert.ok(entries.reduce((n, [, value]) => n + Buffer.byteLength(value), 0) <= 32768, 'strict environment cap');
  assert.ok(entries.every(([key]) => !key.startsWith('AWS_') && !key.startsWith('NEXTGO_LAMBDA_')), 'strict runtime environment owned');
  assert.ok(Array.isArray(workerArgs) && workerArgs.length <= 16 && workerArgs.every(arg => typeof arg === 'string' && !arg.includes('\0') && arg.length <= 4096), 'strict argv');
  assert.ok(workerArgs.reduce((n, arg) => n + Buffer.byteLength(arg), 0) <= 4096, 'strict argv cap');
  assert.ok(allowedHosts === undefined || Array.isArray(allowedHosts), 'strict authorities array');
  const hosts = allowedHosts === undefined ? null : [...allowedHosts];
  if (hosts) {
    const authority = /^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?::[1-9][0-9]{0,4})?$/;
    assert.ok(Array.isArray(allowedHosts) && hosts.length > 0 && hosts.length <= 32 && hosts.every(host => typeof host === 'string' && host.length <= 259 && authority.test(host) && (!host.includes(':') || Number(host.slice(host.lastIndexOf(':') + 1)) <= 65535)), 'strict authorities');
    assert.ok(new Set(hosts).size === hosts.length && hosts.reduce((n, host) => n + host.length, 0) <= 4096, 'strict authorities cap');
  }
  return {args: [...workerArgs], environment: Object.fromEntries(entries), hosts};
}
function strictBody(metadata) {
  assert.ok(plain(metadata) && typeof metadata.body === 'string' && typeof metadata.isBase64Encoded === 'boolean', 'buffered body schema');
  let body;
  if (metadata.isBase64Encoded) {
    assert.ok(metadata.body.length % 4 === 0 && /^[A-Za-z0-9+/]*={0,2}$/.test(metadata.body), 'strict base64 syntax');
    assert.ok(metadata.body.length / 4 * 3 - (metadata.body.endsWith('==') ? 2 : metadata.body.endsWith('=') ? 1 : 0) <= retainedStrictLimits.bufferedBody, 'buffered HTTP body cap');
    body = Buffer.from(metadata.body, 'base64');
    assert.equal(body.toString('base64'), metadata.body, 'strict base64 canonical');
  } else body = Buffer.from(metadata.body, 'utf8');
  assert.ok(body.length <= retainedStrictLimits.bufferedBody, 'buffered HTTP body cap');
  return body;
}
function integrationHeaders(metadata, mode) {
  assert.ok(plain(metadata) && Number.isInteger(metadata.statusCode) && metadata.statusCode >= 200 && metadata.statusCode <= 599, 'integration HTTP status');
  assert.ok(metadata.headers === undefined || plain(metadata.headers), 'integration headers');
  const headers = {};
  for (const [key, value] of Object.entries(metadata.headers || {})) {
    assert.equal(typeof value, 'string', 'integration header value');
    if (!excludedHeaders.has(key.toLowerCase())) headers[key] = value;
  }
  assert.ok(metadata.cookies === undefined || (Array.isArray(metadata.cookies) && metadata.cookies.every(cookie => typeof cookie === 'string')), 'integration cookies');
  if (metadata.cookies?.length) headers['Set-Cookie'] = metadata.cookies;
  headers['X-Nextgo-Fixture-Lambda-Mode'] = mode;
  return headers;
}
async function readBounded(request, cap, label) {
  const chunks = []; let used = 0;
  for await (const chunk of request) {used += chunk.length; assert.ok(used <= cap, label); chunks.push(chunk);}
  return Buffer.concat(chunks, used);
}
function noTrailers(request) {assert.equal(request.rawTrailers.length, 0, 'unexpected runtime trailers');}
async function settleBy(promise, deadline) {
  let timer;
  const result = await Promise.race([
    Promise.resolve(promise).then(value => ({settled: true, value}), error => ({settled: true, error})),
    new Promise(resolve => {timer = setTimeout(() => resolve({settled: false}), Math.max(0, deadline - Date.now()));}),
  ]);
  clearTimeout(timer); return result;
}

export async function startRetainedStrictGateway(options) {
  const {binary, mode, port, workerFactory} = options;
  const {args, environment, hosts} = validateOptions(options);
  const L = retainedStrictLimits, queue = [], reports = [], failures = [], observations = [];
  const socketSets = {runtime: new Set(), public: new Set()}, listenerClosed = {runtime: false, public: false};
  let droppedReports = 0, droppedFailures = 0, droppedObservations = 0, sequence = 0, order = 0;
  let worker, active, poll, stopping = false, poisoned = false, joined = false, exit = null, origin = null, runtimeOrigin = null;
  let forced = false, termAccepted = false, deferredOwnership = false, closePromise, workerClosed;
  let signalTimedOut = false, joinTimedOut = false, listenerJoinTimedOut = false;
  let cleanupStartedAt = null, cleanupCompletedAt = null;
  function bounded(list, value, cap, dropped) {if (list.length === cap) {list.shift(); dropped();} list.push(value);}
  function failure(message) {bounded(failures, String(message).slice(0, 2048), L.failures, () => droppedFailures++);}
  function observe(kind, item, extra = {}) {
    bounded(observations, {kind, id: item?.id ?? null, runtimeID: item?.id ?? null, gatewayRequestID: item?.gatewayRequestID ?? null, path: item?.event.rawPath ?? null, order: ++order, at: Date.now(), ...extra}, L.observations, () => droppedObservations++);
    return order;
  }
  function rejectHTTP(response, status, message) {
    if (response.destroyed || response.writableEnded) return;
    if (response.headersSent) response.destroy();
    else {response.writeHead(status, {'Content-Type': 'text/plain', 'Cache-Control': 'no-store'}); response.end(message);}
  }
  function finish(item) {clearTimeout(item.timer); if (active === item) active = undefined;}
  function protocolFailure(error, item) {
    poisoned = true; failure(error.message);
    if (item) rejectHTTP(item.response, 502, 'Local Lambda integration failed');
    for (const queued of queue.splice(0)) {clearTimeout(queued.timer); rejectHTTP(queued.response, 502, 'Local Lambda integration failed');}
  }
  function dispatch() {
    if (!poll || active || !queue.length || stopping || poisoned) return;
    let item;
    while (queue.length && !item) {const candidate = queue.shift(); if (candidate.response.destroyed || Date.now() >= candidate.admissionDeadline) {clearTimeout(candidate.timer); rejectHTTP(candidate.response, 504, 'Local Lambda invocation timed out');} else item = candidate;}
    if (!item) return;
    active = item; item.dispatchedAt = Date.now(); item.deadline = item.dispatchedAt + L.deadlineMS;
    item.dispatchOrder = observe('dispatch', item, {at: item.dispatchedAt, deadline: item.deadline, admissionDeadline: item.admissionDeadline});
    const response = poll; poll = undefined;
    response.writeHead(200, {'Content-Type': 'application/json', 'Lambda-Runtime-Aws-Request-Id': item.id, 'Lambda-Runtime-Deadline-Ms': String(item.deadline)});
    response.end(item.eventJSON);
  }
  function attach(server, name) {
    server.on('connection', socket => {socketSets[name].add(socket); socket.once('close', () => socketSets[name].delete(socket));});
    server.on('close', () => {listenerClosed[name] = true;});
    server.maxHeadersCount = 64; server.headersTimeout = 5000;
  }
  const runtime = createServer({maxHeaderSize: 16 << 10}, async (request, response) => {
    const item = active;
    try {
      assert.ok(!poisoned, 'runtime unavailable');
      if (request.method === 'GET' && request.url === nextPath) {
        assert.ok(!active && !poll, 'serial next requires acknowledged prior publication');
        assert.equal(request.headers['transfer-encoding'], undefined, 'next has no body');
        assert.ok(request.headers['content-length'] === undefined || request.headers['content-length'] === '0', 'next has no body');
        observe('poll', null); poll = response;
        response.once('close', () => {if (poll === response) poll = undefined;});
        dispatch(); return;
      }
      assert.ok(item, 'runtime publication requires active invocation');
      assert.equal(request.method, 'POST', 'runtime publication method');
      const prefix = `/2018-06-01/runtime/invocation/${item.id}/`;
      const kind = request.url === `${prefix}error` ? 'error' : request.url === `${prefix}response` ? 'response' : null;
      assert.ok(kind, 'runtime publication exact active ID');
      assert.ok(!item.publication, 'one publication per invocation');
      item.publication = kind; // Claim before the first read: concurrent POSTs cannot commit.
      let runtimeBytes = 0, httpBytes = 0, metadataBytes = 0, rawJSONBytes = 0, base64flag = null, statusCode = null, errorJSON = null;
      if (kind === 'error') {
        assert.ok(!item.committed, 'error before response commit');
        assert.equal(request.headers['content-type'], 'application/json', 'runtime error JSON');
        const bytes = await readBounded(request, L.error, 'runtime error cap'); runtimeBytes = rawJSONBytes = bytes.length;
        errorJSON = parseJSON(bytes);
        assert.ok(plain(errorJSON) && typeof errorJSON.errorMessage === 'string' && typeof errorJSON.errorType === 'string', 'runtime error schema');
        noTrailers(request);
        rejectHTTP(item.response, 502, 'Local Lambda invocation error');
      } else if (mode === 'buffered') {
        assert.ok(request.headers['lambda-runtime-function-response-mode'] === undefined || request.headers['lambda-runtime-function-response-mode'] === 'buffered', 'buffered response mode');
        assert.equal(request.headers['content-type'], 'application/json', 'buffered response JSON');
        const bytes = await readBounded(request, L.envelope, 'buffered runtime envelope cap'); runtimeBytes = rawJSONBytes = bytes.length;
        noTrailers(request);
        const metadata = parseJSON(bytes), body = strictBody(metadata);
        const headers = integrationHeaders(metadata, mode);
        statusCode = metadata.statusCode; base64flag = metadata.isBase64Encoded; httpBytes = body.length;
        if (!item.response.destroyed && !item.response.writableEnded) {item.response.writeHead(statusCode, headers); item.committed = true; item.response.end(body);}
      } else {
        assert.equal(request.headers['lambda-runtime-function-response-mode'], 'streaming', 'streaming response mode');
        assert.equal(request.headers['content-type'], 'application/vnd.awslambda.http-integration-response', 'streaming content type');
        assert.equal(request.headers['transfer-encoding'], 'chunked', 'streaming transfer encoding');
        assert.equal(request.headers['content-length'], undefined, 'streaming has no content length');
        let prefixBytes = Buffer.alloc(0), begun = false;
        for await (const chunk of request) {
          runtimeBytes += chunk.length;
          assert.ok(runtimeBytes <= L.metadata + 8 + L.streamBody, 'stream transport cap');
          let body = chunk;
          if (!begun) {
            // Only retain the metadata bound plus its delimiter, even if the
            // first network chunk contains a large body.
            const need = L.metadata + 8 - prefixBytes.length;
            const candidate = Buffer.concat([prefixBytes, chunk.subarray(0, need)]);
            const boundary = candidate.indexOf(nulBoundary);
            if (boundary < 0) {assert.ok(candidate.length < L.metadata + 8, 'stream metadata cap'); prefixBytes = candidate; continue;}
            assert.ok(boundary > 0 && boundary <= L.metadata, 'stream metadata cap');
            metadataBytes = rawJSONBytes = boundary;
            const metadata = parseJSON(candidate.subarray(0, boundary));
            const headers = integrationHeaders(metadata, mode); statusCode = metadata.statusCode;
            const consumed = boundary + 8 - prefixBytes.length;
            body = chunk.subarray(consumed); prefixBytes = Buffer.alloc(0); begun = true;
            if (!item.response.destroyed && !item.response.writableEnded) {item.response.writeHead(statusCode, headers); item.committed = true;}
          }
          httpBytes += body.length; assert.ok(httpBytes <= L.streamBody, 'stream HTTP body cap');
          if (!item.response.destroyed && !item.response.writableEnded) item.response.write(body);
        }
        assert.ok(begun, 'stream delimiter required'); noTrailers(request);
        if (!item.response.destroyed && !item.response.writableEnded) item.response.end();
      }
      assert.ok(!poisoned, 'runtime publication invalidated by protocol failure');
      const report = {id: item.id, runtimeID: item.id, gatewayRequestID: item.gatewayRequestID, kind, mode,
        path: item.event.rawPath, method: item.event.requestContext.http.method, queuedAt: item.queuedAt,
        dispatchedAt: item.dispatchedAt, deadline: item.deadline, admissionDeadline: item.admissionDeadline,
        queuedOrder: item.queuedOrder, dispatchOrder: item.dispatchOrder, runtimeBytes, rawJSONBytes,
        base64flag, httpBytes, metadataBytes, trailers: [...request.rawTrailers], statusCode,
        errorMessage: errorJSON?.errorMessage ?? null, errorType: errorJSON?.errorType ?? null,
        error: errorJSON ? {type: errorJSON.errorType, message: errorJSON.errorMessage} : null,
        deadlineExpired: Date.now() >= item.deadline, ack: 202, acknowledgedAt: null};
      // Incoming request is fully drained before acknowledgement. Active stays
      // claimed through finish, preventing a next GET from overtaking the ACK.
      response.once('finish', () => {
        report.acknowledgedAt = Date.now(); report.ackOrder = observe('report-ack', item, {publicationKind: kind, ack: 202});
        report.order = report.ackOrder;
        bounded(reports, report, L.reports, () => droppedReports++); finish(item);
      });
      response.writeHead(202); response.end();
    } catch (error) {
      protocolFailure(error, item);
      if (!response.headersSent && !response.destroyed) {response.writeHead(500, {'Connection': 'close'}); response.end();}
      else response.destroy();
    }
  });
  runtime.maxConnections = 4; attach(runtime, 'runtime');
  const gateway = createServer({maxHeaderSize: 16 << 10}, async (request, response) => {
    try {
      if (stopping || poisoned || exit) {rejectHTTP(response, 503, 'Fixture unavailable'); return;}
      if (!acceptedHosts.has(request.headers.host) || !request.url.startsWith('/') || request.url.startsWith('//') || request.url.includes('#') || request.url.length > 8192) {rejectHTTP(response, 400, 'Invalid local authority or URL'); return;}
      if (request.url === '/_fixture/lambda/ready') {response.writeHead(200, {'Content-Type': 'text/plain', 'Cache-Control': 'no-store'}); response.end(mode); return;}
      if (queue.length >= L.queue) {rejectHTTP(response, 503, 'Local Lambda queue full'); return;}
      const queuedAt = Date.now(), admissionDeadline = queuedAt + L.admissionMS;
      let body;
      try {body = await readBounded(request, L.request, 'HTTP request cap');}
      catch (error) {rejectHTTP(response, 413, 'Fixture request too large'); return;}
      if (stopping || poisoned || response.destroyed) return;
      if (Date.now() >= admissionDeadline) {rejectHTTP(response, 504, 'Local Lambda invocation timed out'); return;}
      if (queue.length >= L.queue) {rejectHTTP(response, 503, 'Local Lambda queue full'); return;}
      const queryOffset = request.url.indexOf('?'), rawPath = queryOffset < 0 ? request.url : request.url.slice(0, queryOffset);
      const rawQueryString = queryOffset < 0 ? '' : request.url.slice(queryOffset + 1), headers = {};
      for (const [key, value] of Object.entries(request.headers)) if (key !== 'cookie' && !excludedHeaders.has(key)) headers[key] = Array.isArray(value) ? value.join(',') : value;
      headers.host = request.headers.host; headers['x-forwarded-proto'] = request.socket.encrypted ? 'https' : 'http';
      const serial = ++sequence, id = `retained-runtime-${serial}`, gatewayRequestID = `retained-gateway-${serial}`;
      const event = {version: '2.0', rawPath, rawQueryString, headers,
        cookies: request.headers.cookie ? [request.headers.cookie] : [], body: body.toString('base64'), isBase64Encoded: true,
        requestContext: {domainName: request.headers.host, requestId: gatewayRequestID,
          http: {method: request.method, path: rawPath, protocol: 'HTTP/1.1', sourceIp: '127.0.0.1'}}};
      const eventJSON = JSON.stringify(event); assert.ok(Buffer.byteLength(eventJSON) <= L.event, 'event JSON cap');
      const item = {id, gatewayRequestID, response, event, eventJSON, queuedAt, admissionDeadline, committed: false};
      item.queuedOrder = observe('queued', item, {admissionDeadline});
      item.timer = setTimeout(() => {
        const index = queue.indexOf(item); if (index >= 0) queue.splice(index, 1);
        rejectHTTP(response, 504, 'Local Lambda invocation timed out');
        // Active ownership remains until its one real report drains and ACKs.
      }, Math.max(1, admissionDeadline - Date.now()));
      response.once('close', () => {const index = queue.indexOf(item); if (index >= 0) {queue.splice(index, 1); clearTimeout(item.timer);}});
      queue.push(item); dispatch();
    } catch (error) {failure(error.message); rejectHTTP(response, 502, 'Local Lambda gateway failed');}
  });
  let acceptedHosts;
  gateway.maxConnections = 64; gateway.requestTimeout = L.admissionMS; gateway.timeout = L.admissionMS + 2000; attach(gateway, 'public');
  function snapshot() {
    let workerProof = null;
    try {workerProof = worker?.snapshot() ?? null;} catch (error) {workerProof = {snapshotError: String(error.message)};}
    return {policy, mode, origin, runtimeOrigin, childPID: worker?.child?.pid ?? null, stopping, poisoned, joined,
      exit: exit ? {...exit} : null, active: active?.id ?? null, queued: queue.length, invocations: sequence,
      activePublication: active?.publication ?? null, responseCommitted: active?.committed ?? false,
      reports: reports.map(report => ({...report, trailers: [...report.trailers], error: report.error ? {...report.error} : null})), failures: [...failures],
      observations: observations.map(observation => ({...observation})), droppedReports, droppedFailures, droppedObservations,
      listenerClosed: {...listenerClosed}, sockets: {runtime: socketSets.runtime.size, public: socketSets.public.size},
      cleanup: {termAccepted, forced, deferredOwnership, signalTimedOut, joinTimedOut, listenerJoinTimedOut,
        startedAt: cleanupStartedAt, completedAt: cleanupCompletedAt}, worker: workerProof};
  }
  async function closeListeners() {
    for (const socket of [...socketSets.runtime, ...socketSets.public]) socket.destroy();
    await Promise.all([[runtime, 'runtime'], [gateway, 'public']].map(([server, name]) => new Promise((resolve, reject) => {
      if (!server.listening) {listenerClosed[name] = true; resolve(); return;}
      server.close(error => error ? reject(error) : resolve());
    })));
    // Socket close events, rather than destroy() calls, are the physical proof.
    await Promise.all([...socketSets.runtime, ...socketSets.public].map(socket => socket.closed ? Promise.resolve() : new Promise(resolve => socket.once('close', resolve))));
  }
  async function cleanup() {
    stopping = true; cleanupStartedAt = Date.now();
    for (const item of queue.splice(0)) {clearTimeout(item.timer); rejectHTTP(item.response, 503, 'Fixture stopping');}
    if (active) {clearTimeout(active.timer); rejectHTTP(active.response, 503, 'Fixture stopping');}
    if (workerClosed) {
      const graceDeadline = cleanupStartedAt + L.cleanupMS;
      const term = await settleBy(Promise.resolve().then(() => worker.requestSignal('SIGTERM')), graceDeadline);
      termAccepted = term.settled && !term.error && term.value === true;
      if (!termAccepted) {
        deferredOwnership = true; signalTimedOut ||= !term.settled;
        failure(!term.settled ? 'worker SIGTERM authority timed out' : term.error ? `worker SIGTERM authority: ${term.error.message}` : 'worker SIGTERM identity authority refused');
      }
      const timely = await settleBy(workerClosed, graceDeadline);
      // Keep the runtime long-poll socket intact during the TERM grace period.
      // Closing it before Go observes SIGTERM could manufacture a runtime I/O
      // failure and race an otherwise graceful process into an error exit.
      const finalDeadline = Date.now() + L.finalJoinMS;
      const listeners = settleBy(closeListeners(), finalDeadline);
      if (!timely.settled) {
        forced = true;
        const kill = await settleBy(Promise.resolve().then(() => worker.requestSignal('SIGKILL')), finalDeadline);
        if (!kill.settled || kill.error || kill.value !== true) {
          deferredOwnership = true; signalTimedOut ||= !kill.settled;
          failure(!kill.settled ? 'worker SIGKILL authority timed out' : kill.error ? `worker SIGKILL authority: ${kill.error.message}` : 'worker SIGKILL identity authority refused');
        }
      }
      // Factory.closed must represent actual close + drained stdio. A bounded
      // join failure rejects cleanup and leaves ownership explicitly deferred;
      // it cannot manufacture a successful physical join or signal unknown PIDs.
      const physical = await settleBy(workerClosed, finalDeadline);
      joined = physical.settled && Boolean(exit && !exit.closeError);
      if (!physical.settled) {joinTimedOut = true; deferredOwnership = true; failure('worker physical close join timed out');}
      const listenerResult = await listeners;
      if (!listenerResult.settled || listenerResult.error) {
        listenerJoinTimedOut = !listenerResult.settled; deferredOwnership = true;
        failure(listenerResult.error ? `listener physical close: ${listenerResult.error.message}` : 'listener physical close join timed out');
      }
    } else {
      const listenerResult = await settleBy(closeListeners(), Date.now() + L.finalJoinMS);
      if (!listenerResult.settled || listenerResult.error) {
        listenerJoinTimedOut = !listenerResult.settled; deferredOwnership = true;
        failure(listenerResult.error ? `listener physical close: ${listenerResult.error.message}` : 'listener physical close join timed out');
      }
    }
    cleanupCompletedAt = Date.now();
    assert.ok(joined, 'strict worker physical close required');
    assert.ok(termAccepted && !forced && !deferredOwnership, 'strict graceful identity-authorized SIGTERM required');
    assert.equal(exit.code, 0, 'strict worker exit code'); assert.equal(exit.signal, null, 'strict worker exit signal');
    assert.ok(listenerClosed.runtime && listenerClosed.public && !socketSets.runtime.size && !socketSets.public.size, 'strict physical listener/socket close required');
    assert.equal(failures.length, 0, 'strict gateway failures');
  }
  function close() {return closePromise ??= cleanup();}
  async function listen(server, wantedPort) {await new Promise((resolve, reject) => {server.once('error', reject); server.listen(wantedPort, '127.0.0.1', resolve);});}
  try {
    await listen(runtime, 0); runtimeOrigin = `http://127.0.0.1:${runtime.address().port}`;
    const factoryResult = await workerFactory({binary, args: [...args], env: {...environment,
      AWS_LAMBDA_RUNTIME_API: `127.0.0.1:${runtime.address().port}`, NEXTGO_LAMBDA_RESPONSE_MODE: mode}});
    assert.ok(factoryResult && Number.isInteger(factoryResult.child?.pid) && factoryResult.child.pid > 0 && typeof factoryResult.requestSignal === 'function' && typeof factoryResult.snapshot === 'function' && factoryResult.closed && typeof factoryResult.closed.then === 'function', 'strict worker ownership capability');
    worker = factoryResult;
    workerClosed = Promise.resolve(worker.closed).then(result => {
      assert.ok(plain(result) && (result.code === null || Number.isInteger(result.code)) && (result.signal === null || typeof result.signal === 'string'), 'worker close result');
      exit = {...result};
      if (!stopping) {failure('strict worker closed before fixture shutdown'); poisoned = true; if (active) rejectHTTP(active.response, 502, 'Local Lambda worker closed'); for (const queued of queue.splice(0)) {clearTimeout(queued.timer); rejectHTTP(queued.response, 502, 'Local Lambda worker closed');}}
    }).catch(error => {exit = {closeError: String(error.message)}; deferredOwnership = true; failure(`worker physical close: ${error.message}`);});
    await listen(gateway, port); origin = `http://127.0.0.1:${gateway.address().port}`;
    assert.ok(!exit, 'strict worker remained alive during startup');
    acceptedHosts = new Set(hosts ?? [`127.0.0.1:${gateway.address().port}`]);
    return {origin, close, snapshot};
  } catch (error) {
    failure(`strict startup: ${error.message}`);
    if (!workerClosed) {
      deferredOwnership = true; stopping = true; cleanupStartedAt = Date.now();
      const listeners = await settleBy(closeListeners(), cleanupStartedAt + L.finalJoinMS);
      if (!listeners.settled || listeners.error) {listenerJoinTimedOut = !listeners.settled; failure('startup listener physical close incomplete');}
      cleanupCompletedAt = Date.now();
    }
    else {try {await close();} catch (cleanupError) {failure(`startup cleanup: ${cleanupError.message}`);}}
    error.gatewaySnapshot = snapshot(); throw error;
  }
}
