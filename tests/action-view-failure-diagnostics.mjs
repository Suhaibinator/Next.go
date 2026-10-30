// Failure-only diagnostic candidate. Observe at most the first original action
// POST; bind the test's actual Response identity separately. No body/native DOM
// operations, new requests, acceptance waits or application changes occur here.
export function observeActionViewFailure(page, endpoint) {
  const maxEvents = 24, maxFrames = 8;
  const events = [], attached = [];
  let candidate, binding = 'unbound', disposed = false;
  let dropped = 0, frames = 0, registrationFailures = 0, cleanupFailures = 0;
  const add = (kind, extra = {}) => {
    if (disposed) return;
    if (events.length < maxEvents) events.push({ sequence: events.length, kind, ...extra });
    else dropped = Math.min(dropped + 1, 1_000_000);
  };
  const isAction = request => {
    try { const url = new URL(request.url()); return request.method() === 'POST' && url.origin === 'http://127.0.0.1:4185' && endpoint(url.pathname); }
    catch { return false; }
  };
  const request = value => {
    if (!candidate && isAction(value)) { candidate = value; add('candidate-request', { navigation: value.isNavigationRequest() }); }
  };
  const response = value => {
    if (value.request() !== candidate) return;
    const headers = value.headers();
    add('candidate-response', { status: value.status(), json: /^application\/json(?:;|$)/i.test(headers['content-type'] || ''), protocol: headers['x-nextgo-action-protocol'] === '1', revision: headers['x-nextgo-action-revision'] === 'v1' });
  };
  const finished = value => { if (value === candidate) add('candidate-requestfinished'); };
  const failed = value => {
    if (value !== candidate) return;
    const text = value.failure()?.errorText || '';
    add('candidate-requestfailed', { category: /abort|cancel/i.test(text) ? 'aborted-or-cancelled' : /^net::ERR_/i.test(text) ? 'network-error' : 'other' });
  };
  const navigated = frame => {
    if (frame !== page.mainFrame()) return;
    if (frames >= maxFrames) { dropped = Math.min(dropped + 1, 1_000_000); return; }
    frames++;
    let route = 'unparseable';
    try { const url = new URL(frame.url()); route = url.origin !== 'http://127.0.0.1:4185' ? 'other-origin' : url.pathname === '/action-view' ? 'action-view' : 'other-local-route'; } catch {}
    add('main-frame-navigated', { route });
  };
  for (const [name, listener] of [['request', request], ['response', response], ['requestfinished', finished], ['requestfailed', failed], ['framenavigated', navigated]]) {
    try { page.on(name, listener); attached.push([name, listener]); }
    catch { registrationFailures++; }
  }
  const dispose = () => {
    if (disposed) return;
    disposed = true;
    for (const [name, listener] of attached) { try { page.off(name, listener); } catch { cleanupFailures++; } }
    attached.length = 0;
  };
  return {
    dispose,
    bindResponse(value) {
      // The test calls once, synchronously after post() returns and before its
      // unchanged invalid.json(). A candidate mismatch cannot claim identity.
      if (binding !== 'unbound') return;
      try { binding = candidate && value.request() === candidate ? 'exact' : 'unknown'; }
      catch { binding = 'unknown'; }
      add('asserted-response-binding', { identity: binding });
    },
    async attachFailure(testInfo) {
      add('assertion-failed');
      dispose();
      const body = JSON.stringify({ schema: 2, candidateObserved: !!candidate, assertedResponseIdentity: binding === 'exact' ? 'exact-candidate-match' : 'UNKNOWN', bindingAttempted: binding !== 'unbound', events, dropped, registrationFailures, cleanupFailures, qualification: false, meaning: 'Passive candidate events only. Attribute them to the asserted response only for exact-candidate-match. Existing automatic error-context supplies the DOM snapshot. No body read or cause attribution.' });
      if (Buffer.byteLength(body) <= 16384) await testInfo.attach('action-view-original-flight-diagnostic', { body: Buffer.from(body), contentType: 'application/json' });
    },
  };
}
