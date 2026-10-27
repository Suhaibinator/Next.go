// Authored but NOT EXECUTED. In-process synthetic worker capabilities exercise
// gateway protocol/ownership branches only; they are not Go artifact evidence.
import nodeTest from 'node:test';
import assert from 'node:assert/strict';
import { request } from 'node:http';
import { readFile } from 'node:fs/promises';
import { EventEmitter } from 'node:events';
import { startLambdaGateway } from './lambda-features-gateway.mjs';
import { retainedStrictLimits as L } from './retained-strict-gateway.mjs';

const deferred = () => {let resolve; const promise = new Promise(done => {resolve = done;}); return {promise, resolve};};
const test = (name, body) => nodeTest(name, {timeout: 12_000}, body);
const tick = () => new Promise(resolve => setImmediate(resolve));
async function until(predicate) {const deadline = Date.now() + 2000; while (Date.now() < deadline) {if (predicate()) return; await tick();} assert.fail('observation did not arrive');}
function http(url, {method = 'GET', headers = {}, body, chunks, trailers, path} = {}) {
  return new Promise((resolve, reject) => {
    const req = request(url, {method, headers, agent: false, ...(path === undefined ? {} : {path})}, res => {
      const parts = []; res.on('data', part => parts.push(part)); res.once('error', reject);
      res.once('end', () => resolve({status: res.statusCode, headers: res.headers, body: Buffer.concat(parts)}));
    });
    req.once('error', reject);
    if (chunks) for (const chunk of chunks) req.write(chunk);
    if (trailers) req.addTrailers(trailers);
    req.end(body);
  });
}
async function harness(t, {mode = 'buffered', autoClose = true, closeOnSignal = 'SIGTERM', signalAccepted = true, hangingSignal = false, trustedEnvironment = {NEXTGO_DEMO_MARKER: 'owned'}} = {}) {
  const physical = deferred(), calls = [], child = Object.assign(new EventEmitter(), {pid: 900001, kill() {assert.fail('gateway must never call child.kill');}}), state = {capture: null};
  const gateway = await startLambdaGateway({policy: 'retained-strict-v1', binary: '/synthetic/test-only-worker', mode, port: 0,
    trustedEnvironment, workerFactory: async capture => {
      state.capture = capture;
      return {child,
        async requestSignal(signal) {calls.push(signal); if (hangingSignal) return new Promise(() => {}); if (autoClose && signal === closeOnSignal) physical.resolve({code: 0, signal: null}); return signalAccepted;},
        closed: physical.promise, snapshot: () => ({synthetic: true, signals: [...calls]})};
    }});
  t.after(async () => {physical.resolve({code: 0, signal: null}); try {await gateway.close();} catch {/* Expected failed protocol/identity tests inspect rejection explicitly. */}});
  return {gateway, calls, physical, state, child, runtime: `http://${state.capture.env.AWS_LAMBDA_RUNTIME_API}`};
}
async function invocation(h, path = '/a') {
  const publicResult = http(`${h.gateway.origin}${path}`);
  const next = await http(`${h.runtime}/2018-06-01/runtime/invocation/next`);
  assert.equal(next.status, 200);
  return {publicResult, next, id: next.headers['lambda-runtime-aws-request-id']};
}
function post(h, id, kind, body, headers = {}) {
  return http(`${h.runtime}/2018-06-01/runtime/invocation/${id}/${kind}`, {method: 'POST', body,
    headers: {'content-type': 'application/json', ...headers}});
}
const envelope = body => JSON.stringify({statusCode: 200, headers: {'content-type': 'text/plain'}, body, isBase64Encoded: false});

test('strict policy requires a complete environment and factory before any listener startup', async () => {
  await assert.rejects(startLambdaGateway({policy: 'retained-strict-v1', binary: '/synthetic', mode: 'buffered', port: 0}), /workerFactory/);
  await assert.rejects(startLambdaGateway({policy: 'retained-strict-v1', binary: '/synthetic', mode: 'buffered', port: 0, workerFactory() {assert.fail('factory must not run');}}), /trustedEnvironment/);
  await assert.rejects(startLambdaGateway({policy: 'other', binary: '/synthetic'}), /unknown gateway policy/);
});

test('strict factory receives only copied trusted env plus the two owned loopback runtime values', async t => {
  const env = {NEXTGO_DEMO_MARKER: 'kept', NEXTGO_DEMO_BASE_PATH: '/actual-retained'};
  const h = await harness(t, {trustedEnvironment: env}); env.NEXTGO_DEMO_MARKER = 'mutated';
  assert.deepEqual(h.state.capture.args, []);
  assert.deepEqual(Object.keys(h.state.capture.env).sort(), ['AWS_LAMBDA_RUNTIME_API', 'NEXTGO_DEMO_BASE_PATH', 'NEXTGO_DEMO_MARKER', 'NEXTGO_LAMBDA_RESPONSE_MODE']);
  assert.equal(h.state.capture.env.NEXTGO_DEMO_MARKER, 'kept'); assert.equal(h.state.capture.env.NEXTGO_DEMO_BASE_PATH, '/actual-retained');
  assert.match(h.state.capture.env.AWS_LAMBDA_RUNTIME_API, /^127\.0\.0\.1:[1-9][0-9]*$/);
});

test('serial response drains and ACKs before next dispatch; actual IDs, queue and deadlines are observed', async t => {
  const h = await harness(t), first = await invocation(h, '/api/task');
  const secondHTTP = http(`${h.gateway.origin}/b`);
  await until(() => h.gateway.snapshot().observations.some(o => o.kind === 'queued' && o.path === '/b'));
  const before = h.gateway.snapshot(); assert.equal(before.active, first.id); assert.equal(before.queued, 1);
  assert.equal(before.reports.length, 0);
  const firstDispatch = before.observations.find(o => o.kind === 'dispatch');
  assert.equal(firstDispatch.deadline - firstDispatch.at, L.deadlineMS);
  assert.equal(Number(first.next.headers['lambda-runtime-deadline-ms']), firstDispatch.deadline);
  const event = JSON.parse(first.next.body); assert.notEqual(event.requestContext.requestId, first.id);
  assert.equal((await post(h, first.id, 'response', envelope('a'))).status, 202);
  assert.equal((await first.publicResult).body.toString(), 'a');
  const second = await http(`${h.runtime}/2018-06-01/runtime/invocation/next`), secondID = second.headers['lambda-runtime-aws-request-id'];
  assert.notEqual(secondID, first.id); assert.equal(JSON.parse(second.body).rawPath, '/b');
  assert.equal((await post(h, secondID, 'response', envelope('b'))).status, 202);
  assert.equal((await secondHTTP).body.toString(), 'b');
  const after = h.gateway.snapshot(), a = after.reports[0], b = after.reports[1];
  assert.equal(a.kind, 'response'); assert.equal(a.ack, 202); assert.equal(a.httpBytes, 1);
  assert.ok(a.ackOrder < b.dispatchOrder); assert.equal(after.droppedReports, 0); assert.equal(after.droppedFailures, 0);
});

test('genuine-shaped invocation error is accepted only before response and is drained with 202 without replay', async t => {
  const h = await harness(t), item = await invocation(h, '/api/task');
  const body = JSON.stringify({errorMessage: 'context deadline exceeded', errorType: 'NextGoInvocationError'});
  assert.equal((await post(h, item.id, 'error', body, {'Lambda-Runtime-Function-Error-Type': 'Unhandled'})).status, 202);
  assert.equal((await item.publicResult).status, 502);
  const report = h.gateway.snapshot().reports[0];
  assert.equal(report.kind, 'error'); assert.equal(report.error.type, 'NextGoInvocationError');
  assert.equal(report.error.message, 'context deadline exceeded'); assert.equal(report.rawJSONBytes, Buffer.byteLength(body));
  assert.equal(report.ack, 202); assert.equal(report.httpBytes, 0); assert.equal(h.gateway.snapshot().invocations, 1);
  assert.equal(h.gateway.snapshot().failures.length, 0);
});

test('event preserves literal raw path/query/cookies/body and trusted socket scheme', async t => {
  const h = await harness(t);
  const result = http(h.gateway.origin, {path: '/a/../b%2Fz?x=1&x=2+3', method: 'POST', body: 'actual body',
    headers: {'Cookie': 'one=1; two=2', 'X-Forwarded-Proto': 'https'}});
  const next = await http(`${h.runtime}/2018-06-01/runtime/invocation/next`), event = JSON.parse(next.body);
  assert.equal(event.rawPath, '/a/../b%2Fz'); assert.equal(event.rawQueryString, 'x=1&x=2+3');
  assert.deepEqual(event.cookies, ['one=1; two=2']); assert.equal(Buffer.from(event.body, 'base64').toString(), 'actual body');
  assert.equal(event.headers['x-forwarded-proto'], 'http'); assert.equal(event.requestContext.http.path, event.rawPath);
  assert.equal((await post(h, next.headers['lambda-runtime-aws-request-id'], 'response', envelope('ok'))).status, 202);
  assert.equal((await result).body.toString(), 'ok');
});

test('wrong active ID cannot commit an HTTP response and poisons further dispatch', async t => {
  const h = await harness(t), item = await invocation(h);
  assert.equal((await post(h, `${item.id}-wrong`, 'response', envelope('must not escape'))).status, 500);
  assert.equal((await item.publicResult).status, 502);
  assert.equal(h.gateway.snapshot().reports.length, 0); assert.equal(h.gateway.snapshot().poisoned, true);
  await assert.rejects(h.gateway.close(), /gateway failures/);
});

test('a next GET while active is a protocol error and never dispatches a second invocation', async t => {
  const h = await harness(t), item = await invocation(h);
  assert.equal((await http(`${h.runtime}/2018-06-01/runtime/invocation/next`)).status, 500);
  assert.equal((await item.publicResult).status, 502);
  assert.equal(h.gateway.snapshot().observations.filter(o => o.kind === 'dispatch').length, 1);
  await assert.rejects(h.gateway.close(), /gateway failures/);
});

for (const [name, makeBody] of [
  ['envelope over 6 MiB', () => envelope('x'.repeat(L.envelope))],
  ['decoded base64 over 4 MiB', () => JSON.stringify({statusCode: 200, body: Buffer.alloc(L.bufferedBody + 1).toString('base64'), isBase64Encoded: true})],
  ['UTF8 body over 4 MiB', () => envelope('x'.repeat(L.bufferedBody + 1))],
  ['noncanonical base64', () => JSON.stringify({statusCode: 200, body: 'YR==', isBase64Encoded: true})],
  ['malformed base64', () => JSON.stringify({statusCode: 200, body: 'a===', isBase64Encoded: true})],
]) test(`reject ${name} before buffered response commit`, async t => {
  const h = await harness(t), item = await invocation(h), result = post(h, item.id, 'response', makeBody());
  await result.catch(() => null); assert.equal((await item.publicResult).status, 502);
  assert.equal(h.gateway.snapshot().reports.length, 0); assert.ok(h.gateway.snapshot().failures.length);
});

test('base64 at 4 MiB is accepted with exact on-wire body and raw envelope accounting', async t => {
  const h = await harness(t), item = await invocation(h), bytes = Buffer.alloc(L.bufferedBody, 97);
  const body = JSON.stringify({statusCode: 200, body: bytes.toString('base64'), isBase64Encoded: true});
  assert.equal((await post(h, item.id, 'response', body)).status, 202);
  assert.deepEqual((await item.publicResult).body, bytes);
  const report = h.gateway.snapshot().reports[0]; assert.equal(report.rawJSONBytes, Buffer.byteLength(body));
  assert.equal(report.httpBytes, L.bufferedBody); assert.equal(report.base64flag, true);
});

test('error JSON over 64 KiB is rejected before any application response', async t => {
  const h = await harness(t), item = await invocation(h);
  await post(h, item.id, 'error', JSON.stringify({errorType: 'x', errorMessage: 'x'.repeat(L.error)})).catch(() => null);
  assert.equal((await item.publicResult).status, 502); assert.equal(h.gateway.snapshot().reports.length, 0);
});

const streamHeaders = {'Content-Type': 'application/vnd.awslambda.http-integration-response', 'Lambda-Runtime-Function-Response-Mode': 'streaming', 'Transfer-Encoding': 'chunked'};
function stream(h, id, chunks, {headers = {}, trailers} = {}) {
  return http(`${h.runtime}/2018-06-01/runtime/invocation/${id}/response`, {method: 'POST', headers: {...streamHeaders, ...headers}, chunks, trailers});
}
test('stream delimiter split across chunks preserves cookies/body and exact metadata accounting', async t => {
  const h = await harness(t, {mode: 'streaming'}), item = await invocation(h);
  const metadata = Buffer.from(JSON.stringify({statusCode: 201, cookies: ['one=1', 'two=2']}));
  assert.equal((await stream(h, item.id, [metadata, Buffer.alloc(3), Buffer.alloc(5), Buffer.from('actual')])).status, 202);
  const result = await item.publicResult; assert.equal(result.status, 201); assert.deepEqual(result.headers['set-cookie'], ['one=1', 'two=2']);
  assert.equal(result.body.toString(), 'actual');
  const report = h.gateway.snapshot().reports[0]; assert.equal(report.metadataBytes, metadata.length); assert.equal(report.httpBytes, 6);
  assert.equal(report.runtimeBytes, metadata.length + 8 + 6); assert.deepEqual(report.trailers, []);
});

for (const [name, chunks, headers, trailers] of [
  ['missing delimiter', [Buffer.from('{"statusCode":200}')], {}, undefined],
  ['metadata cap', [Buffer.from('{"statusCode":200,"padding":"' + 'x'.repeat(L.metadata) + '"}'), Buffer.alloc(8)], {}, undefined],
  ['wrong mode header', [Buffer.from('{"statusCode":200}'), Buffer.alloc(8)], {'Lambda-Runtime-Function-Response-Mode': 'buffered'}, undefined],
  ['unexpected trailer', [Buffer.from('{"statusCode":200}'), Buffer.alloc(8), Buffer.from('body')], {'Trailer': 'Lambda-Runtime-Function-Error-Type'}, {'Lambda-Runtime-Function-Error-Type': 'NextGo.ResponseTooLarge'}],
  ['body cap', [Buffer.from('{"statusCode":200}'), Buffer.alloc(8), Buffer.alloc(L.streamBody + 1)], {}, undefined],
]) test(`stream rejects ${name} and cannot report successful publication`, async t => {
  const h = await harness(t, {mode: 'streaming'}), item = await invocation(h);
  const publicOutcome = item.publicResult.catch(error => ({aborted: error.message}));
  await stream(h, item.id, chunks, {headers, trailers}).catch(() => null);
  await publicOutcome;
  assert.equal(h.gateway.snapshot().reports.length, 0); assert.ok(h.gateway.snapshot().failures.length);
});

test('concurrent /error cannot replace an already claimed streaming response', async t => {
  const h = await harness(t, {mode: 'streaming'}), item = await invocation(h), publicOutcome = item.publicResult.catch(() => null);
  const streamDone = deferred();
  const req = request(`${h.runtime}/2018-06-01/runtime/invocation/${item.id}/response`, {method: 'POST', headers: streamHeaders, agent: false}, res => {res.resume(); res.on('end', streamDone.resolve);});
  req.on('error', streamDone.resolve); req.write(Buffer.concat([Buffer.from('{"statusCode":200}'), Buffer.alloc(8), Buffer.from('committed')]));
  await until(() => h.gateway.snapshot().responseCommitted);
  assert.equal((await post(h, item.id, 'error', JSON.stringify({errorType: 'x', errorMessage: 'late'}))).status, 500);
  req.end(); await streamDone.promise; await publicOutcome;
  assert.ok(h.gateway.snapshot().failures.some(message => message.includes('one publication')));
});

test('close waits for physical child close rather than synthetic exit and proves both socket sets closed', async t => {
  const h = await harness(t, {autoClose: false}); let completed = false;
  const closing = h.gateway.close().then(() => {completed = true;});
  await until(() => h.calls.includes('SIGTERM')); await tick();
  h.child.emit('exit', 0, null); await tick();
  assert.equal(completed, false); assert.equal(h.gateway.snapshot().joined, false);
  h.physical.resolve({code: 0, signal: null}); await closing;
  const proof = h.gateway.snapshot(); assert.equal(proof.joined, true);
  assert.deepEqual(proof.listenerClosed, {runtime: true, public: true}); assert.deepEqual(proof.sockets, {runtime: 0, public: 0});
  assert.deepEqual(h.calls, ['SIGTERM']); assert.equal(proof.cleanup.forced, false);
  await assert.rejects(http(h.gateway.origin)); await assert.rejects(http(h.runtime));
});

test('public request cap never dispatches an oversized client body', async t => {
  const h = await harness(t);
  const result = await http(`${h.gateway.origin}/oversized`, {method: 'POST', body: Buffer.alloc(L.request + 1)}).catch(() => null);
  if (result) assert.equal(result.status, 413);
  assert.equal(h.gateway.snapshot().invocations, 0); assert.equal(h.gateway.snapshot().queued, 0);
});

test('queue bound rejects the thirty-third waiting request without dispatch or replay', async t => {
  const h = await harness(t), pending = [];
  for (let i = 0; i < L.queue; i++) pending.push(http(`${h.gateway.origin}/queued-${i}`).catch(() => null));
  await until(() => h.gateway.snapshot().queued === L.queue);
  assert.equal((await http(`${h.gateway.origin}/overflow`)).status, 503);
  assert.equal(h.gateway.snapshot().invocations, L.queue);
  assert.equal(h.gateway.snapshot().observations.filter(o => o.kind === 'dispatch').length, 0);
  await h.gateway.close(); await Promise.all(pending);
});

test('bounded report and observation history explicitly counts dropped entries', async t => {
  const h = await harness(t);
  for (let i = 0; i < L.reports + 2; i++) {
    const item = await invocation(h, `/serial-${i}`);
    assert.equal((await post(h, item.id, 'response', envelope('ok'))).status, 202);
    await item.publicResult;
  }
  const proof = h.gateway.snapshot(); assert.equal(proof.reports.length, L.reports); assert.equal(proof.droppedReports, 2);
  assert.equal(proof.observations.length, L.observations); assert.ok(proof.droppedObservations > 0);
});

test('bounded protocol failures cannot silently drop history', async t => {
  const h = await harness(t);
  for (let i = 0; i < L.failures + 2; i++) await http(`${h.runtime}/invalid-runtime-path`, {method: 'POST'});
  const proof = h.gateway.snapshot(); assert.equal(proof.failures.length, L.failures); assert.equal(proof.droppedFailures, 2);
});

test('identity refusal fails cleanup even when a physical close later arrives', async t => {
  const h = await harness(t, {signalAccepted: false});
  await assert.rejects(h.gateway.close(), /identity-authorized/);
  const proof = h.gateway.snapshot(); assert.equal(proof.cleanup.deferredOwnership, true); assert.equal(proof.joined, true);
  assert.deepEqual(h.calls, ['SIGTERM']);
});

for (const hangingSignal of [false, true]) test(`missing physical close is independently bounded when signal ${hangingSignal ? 'never settles' : 'refuses identity'}`, async t => {
  const h = await harness(t, {autoClose: false, signalAccepted: false, hangingSignal});
  const started = Date.now();
  await assert.rejects(h.gateway.close(), /physical close required/);
  const elapsed = Date.now() - started, proof = h.gateway.snapshot();
  assert.ok(elapsed >= L.cleanupMS, 'TERM grace was observed');
  assert.ok(elapsed < L.cleanupMS + L.finalJoinMS + 1500, 'independent bounded cleanup completed');
  assert.equal(proof.joined, false); assert.equal(proof.cleanup.deferredOwnership, true);
  assert.equal(proof.cleanup.joinTimedOut, true); assert.equal(proof.cleanup.signalTimedOut, hangingSignal);
  assert.deepEqual(h.calls, ['SIGTERM', 'SIGKILL']);
  assert.deepEqual(proof.listenerClosed, {runtime: true, public: true});
  assert.deepEqual(proof.sockets, {runtime: 0, public: 0});
  await assert.rejects(http(h.gateway.origin)); await assert.rejects(http(h.runtime));
  // A later factory close remains outer containment work; it cannot retroactively
  // turn the already failed gateway join into a qualification PASS.
  h.physical.resolve({code: 0, signal: null}); await tick();
  assert.equal(h.gateway.snapshot().joined, false);
});

test('a physical close following requested KILL still fails graceful qualification', async t => {
  const h = await harness(t, {closeOnSignal: 'SIGKILL'});
  await assert.rejects(h.gateway.close(), /identity-authorized/);
  const proof = h.gateway.snapshot(); assert.equal(proof.joined, true); assert.equal(proof.cleanup.forced, true);
  assert.equal(proof.cleanup.joinTimedOut, false); assert.deepEqual(h.calls, ['SIGTERM', 'SIGKILL']);
});

test('factory rejection closes owned listener and records unknown-child deferred ownership without signaling', async () => {
  let endpoint;
  const failure = await startLambdaGateway({policy: 'retained-strict-v1', binary: '/synthetic', mode: 'buffered', port: 0, trustedEnvironment: {},
    workerFactory: async ({env}) => {endpoint = `http://${env.AWS_LAMBDA_RUNTIME_API}`; throw Error('factory failed before return');}}).then(() => assert.fail('startup must fail'), error => error);
  assert.equal(failure.gatewaySnapshot.joined, false); assert.equal(failure.gatewaySnapshot.cleanup.deferredOwnership, true);
  assert.deepEqual(failure.gatewaySnapshot.listenerClosed, {runtime: true, public: true});
  assert.deepEqual(failure.gatewaySnapshot.sockets, {runtime: 0, public: 0}); await assert.rejects(http(endpoint));
});

test('legacy body is kept verbatim behind the optional strict dispatch boundary', async () => {
  const derivative = await readFile(new URL('./lambda-features-gateway.mjs', import.meta.url), 'utf8');
  const baseline = await readFile(new URL('./lambda-features-gateway.legacy-baseline.mjs.txt', import.meta.url), 'utf8');
  const marker = '  // Test operators may opt into a small exact authority set.';
  assert.equal(derivative.slice(derivative.indexOf(marker)), baseline.slice(baseline.indexOf(marker)));
  assert.ok(derivative.includes("if (policy !== undefined)"));
  assert.ok(baseline.includes("child.kill('SIGTERM')"));
  const strict = await readFile(new URL('./retained-strict-gateway.mjs', import.meta.url), 'utf8');
  assert.ok(!strict.includes('child.kill(')); assert.ok(!strict.includes('process.env'));
});
