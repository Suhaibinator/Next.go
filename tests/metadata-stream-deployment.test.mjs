// Prospective LOCAL executable qualification: 5 families x 3 adapters = 15.
// This overlay has not been executed. It never builds or synthesizes Go output.
import assert from 'node:assert/strict';
import test from 'node:test';
import {bounded, browserUA, fallbackTitle, finalValue, observe, startMetadataDeployment} from './metadata-stream-deployment-fixture.mjs';

const selected = process.env.NEXTGO_METADATA_DEPLOY_BINARY;
const navigation = {'User-Agent': browserUA, 'X-Nextgo-Navigate': '1', 'X-Nextgo-Stream': '1', 'X-Nextgo-Metadata': '1'};
const nextTurn = () => new Promise(done => setImmediate(done)); // Scheduling barrier, not a timed publication gate.
function privateResponse(reply) {assert.match(reply.headers['cache-control'] || '', /private/); assert.match(reply.headers['cache-control'] || '', /no-store/); assert.equal(reply.headers['set-cookie'], undefined); assert.equal(reply.headers['x-metadata-late'], undefined);}
function anonymous(record) {assert.equal(record.headers.cookie, undefined); assert.equal(record.headers.authorization, undefined);}
function decodedFrames(reply) {
  assert.equal(reply.error, null); assert.equal(reply.status, 200); assert.equal(reply.ended, true); assert.match(reply.headers['content-type'], /^application\/x-nextgo-stream\+json/); assert.ok(reply.text.endsWith('\n'));
  const frames = reply.text.trimEnd().split('\n').map(line => JSON.parse(line)); assert.ok(frames.length <= 131); for (const frame of frames) assert.equal(frame.version, 1); return frames;
}
function pendingShell(frame) {
  assert.equal(frame.type, 'shell'); assert.equal(frame.metadataPending, true); assert.match(frame.metadataOwner, /^[a-f0-9]{32}$/); assert.ok(Array.isArray(frame.metadataIndices));
  assert.equal(frame.page.title, fallbackTitle); assert.equal(frame.page.version, 3); assert.ok(frame.page.html.includes('data-metadata-stream'));
  let previous = -1;
  for (const index of frame.metadataIndices) {assert.ok(Number.isInteger(index) && index > previous && index < frame.page.head.length); previous = index; const element = frame.page.head[index]; assert.notEqual(element.tag, 'script'); assert.notEqual(element.attrs?.rel, 'stylesheet');}
}
function terminal(frames, failed = false) {
  assert.deepEqual(frames.map(frame => frame.type), ['shell', 'metadata', 'done']); pendingShell(frames[0]);
  const end = frames[1]; assert.equal(end.failed === true, failed); assert.equal(end.metadata.version, 1); assert.equal(end.metadata.owner, frames[0].metadataOwner);
  if (failed) assert.deepEqual(Object.keys(end.metadata).sort(), ['owner', 'version']);
  else {assert.deepEqual(Object.keys(end.metadata).sort(), ['description', 'elements', 'owner', 'title', 'version']); assert.equal(end.metadata.title, finalValue.title); assert.equal(end.metadata.description, finalValue.description); assert.ok(end.metadata.elements.some(e => e.tag === 'link' && e.attrs?.rel === 'canonical' && e.attrs.href === finalValue.canonical)); assert.ok(end.metadata.elements.every(e => e.tag !== 'script' && e.attrs?.rel !== 'stylesheet'));}
}
function htmlTerminal(text) {
  const match = /<template data-nextgo-metadata="([a-f0-9]{32})">([\s\S]*?)<\/template>/.exec(text); assert.ok(match);
  const decoded = match[2].replace(/&#34;|&quot;/g, '"').replace(/&#39;|&#x27;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
  const frame = JSON.parse(decoded); assert.equal(frame.type, 'metadata'); assert.equal(frame.version, 1); assert.equal(frame.metadata.owner, match[1]); assert.equal(frame.metadata.title, finalValue.title); return frame;
}
async function withFixture(transport, options, run) {
  const fixture = await startMetadataDeployment(transport, options);
  let primary, cleanup;
  try {await run(fixture);} catch (error) {primary = error;}
  try {await fixture.close();} catch (error) {cleanup = error;}
  if (primary && cleanup) throw new AggregateError([primary, cleanup], 'Primary metadata assertion and owned cleanup both failed');
  if (primary) throw primary;
  if (cleanup) throw cleanup;
}
const options = {skip: !selected && 'supply an absolute pinned PREBUILT demo binary; no compiler fallback', timeout: 45_000};
for (const transport of ['http', 'buffered', 'streaming']) {
  test(`${transport}: unknown UA, HEAD and legacy metadata negotiation block on final`, options, () => withFixture(transport, {}, async fixture => {
    for (const variant of [
      {method: 'GET', headers: {'User-Agent': 'Unrecognized local metadata probe'}},
      {method: 'HEAD', headers: {'User-Agent': browserUA}},
      {method: 'GET', headers: {...navigation, 'X-Nextgo-Metadata': '0'}},
    ]) {
      const gate = fixture.arm(), flight = observe(fixture.origin, fixture.path, {...variant, headers: {...variant.headers, Cookie: 'private-input=not-forwarded', Authorization: 'Bearer not-forwarded'}});
      anonymous(await bounded(gate.entered.promise)); await nextTurn(); assert.equal(flight.state.headersReceived, false, 'held genuine source must prevent a blocking commit'); gate.release();
      const reply = await bounded(flight.done); assert.equal(reply.status, 200); assert.equal(reply.error, null); privateResponse(reply); assert.equal(reply.headers['x-nextgo-metadata'], undefined);
      if (variant.method === 'HEAD') assert.equal(reply.text, '');
      else if (variant.headers['X-Nextgo-Navigate']) {const frames = decodedFrames(reply); assert.deepEqual(frames.map(frame => frame.type), ['shell', 'done']); assert.equal(frames[0].page.title, finalValue.title); assert.equal(frames[0].metadataPending, undefined);}
      else {assert.ok(reply.text.includes(`<title>${finalValue.title}</title>`)); assert.ok(!reply.text.includes(`<title>${fallbackTitle}</title>`)); assert.ok(!reply.text.includes('data-nextgo-metadata='));}
    }
  }));
  test(`${transport}: opted-in browser HTML carries genuine fallback then metadata terminal`, options, () => withFixture(transport, {}, async fixture => {
    const gate = fixture.arm(), flight = observe(fixture.origin, fixture.path, {headers: {'User-Agent': browserUA}}); anonymous(await bounded(gate.entered.promise));
    if (transport === 'buffered') {await nextTurn(); assert.equal(flight.state.headersReceived, false, 'buffered Runtime API intentionally withholds the outer document');}
    else {await flight.waitFor(reply => reply.text.includes('data-metadata-counter')); assert.ok(flight.state.text.includes(`<title>${fallbackTitle}</title>`)); assert.ok(!flight.state.text.includes('data-nextgo-metadata=')); assert.equal(flight.state.ended, false);}
    gate.release(); const reply = await bounded(flight.done); assert.equal(reply.status, 200); assert.equal(reply.error, null); privateResponse(reply); assert.match(reply.headers.vary || '', /User-Agent/i);
    assert.ok(reply.text.includes(`<title>${fallbackTitle}</title>`)); const frame = htmlTerminal(reply.text); assert.equal(frame.metadata.description, finalValue.description);
    assert.equal((reply.text.match(/metadata-stream-jsonld-v1/g) || []).length, 1); assert.equal((reply.text.match(/resource-call=1/g) || []).length, 1); assert.ok(reply.text.includes('data-metadata-counter')); assert.ok(reply.text.includes('nextgo-initial-ready'));
  }));
  test(`${transport}: exact v1 navigation acknowledgment has shell metadata done ownership`, options, () => withFixture(transport, {}, async fixture => {
    const gate = fixture.arm(), flight = observe(fixture.origin, fixture.path, {headers: navigation}); anonymous(await bounded(gate.entered.promise));
    if (transport !== 'buffered') {await flight.waitFor(reply => reply.text.includes('\n')); const shell = JSON.parse(flight.state.text.split('\n')[0]); pendingShell(shell); assert.equal(flight.state.headers['x-nextgo-metadata'], '1'); assert.equal(flight.state.ended, false);}
    gate.release(); const reply = await bounded(flight.done); privateResponse(reply); assert.equal(reply.headers['x-nextgo-metadata'], '1'); assert.equal(reply.headers['x-nextgo-protocol'], '3'); terminal(decodedFrames(reply));
    assert.equal(fixture.sourceRecords.length, 1, 'one Memo-owned genuine source load');
  }));
  test(`${transport}: late native failure retains fallback and redacts private diagnostics`, options, () => withFixture(transport, {mode: 'fail'}, async fixture => {
    const gate = fixture.arm(), flight = observe(fixture.origin, fixture.path, {headers: navigation}); await bounded(gate.entered.promise);
    if (transport !== 'buffered') await flight.waitFor(reply => reply.text.includes('\n'));
    gate.release(); const reply = await bounded(flight.done); privateResponse(reply); terminal(decodedFrames(reply), true); assert.ok(!reply.text.includes('private deferred metadata fixture diagnostic')); assert.ok(!reply.text.includes('javascript:metadata-fixture-invalid'));
    // Before-commit failure is still a real generic error, without fallback or
    // error text leaking through a fabricated successful metadata payload.
    const blockingGate = fixture.arm(), blocking = observe(fixture.origin, fixture.path, {headers: {'User-Agent': 'Unknown local agent'}}); await bounded(blockingGate.entered.promise); blockingGate.release(); const failure = await bounded(blocking.done); assert.equal(failure.status, 500); assert.equal(failure.error, null); assert.ok(!failure.text.includes('private deferred metadata fixture diagnostic')); assert.ok(!failure.text.includes('data-nextgo-metadata='));
  }));
  test(`${transport}: client cancellation physically closes source and next real Go invocation runs`, options, () => withFixture(transport, {cancelCase: true}, async fixture => {
    const gate = fixture.arm(), flight = observe(fixture.origin, fixture.path, {headers: navigation}); await bounded(gate.entered.promise);
    if (transport !== 'buffered') await flight.waitFor(reply => reply.text.includes('\n'));
    flight.cancel(); await bounded(flight.done);
    // Native HTTP propagates socket cancellation. Local Lambda intentionally
    // retains its active invocation: its configured 2s render deadline instead
    // cancels the source before the gateway can dispatch a next Runtime event.
    const canceled = await bounded(gate.closed.promise, 10_000); assert.equal(canceled.canceled, true); assert.equal(canceled.released, false);
    await fixture.health(); // /api/health is served by actual Go, never /_fixture/*.
    assert.equal(fixture.sourceRecords.length, 1);
    // Source socket closure + next invocation + graceful process join are the
    // observable guarantees here; exact Go goroutine/token counts remain native
    // limiter-unit evidence, rather than an inferred public-API assertion.
  }));
}
