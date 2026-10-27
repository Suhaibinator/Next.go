// Test-only evidence, not a transport fix. Extra observer microtasks may change
// timing. Original promises/receivers are preserved; no clones/extra body reads.
const flights = new WeakMap();
let nextFlight = 0;
const cap = 128; // Native + CDP ledgers together retain at most 256 events.
const trim = (value, limit = 160) => String(value ?? '').slice(0, limit);
const failure = error => { try { return trim(error?.message || error); } catch { return 'unreadable diagnostic error'; } };
const incomplete = error => { try { console.warn(`Skew diagnostic incomplete: ${failure(error)}`); } catch {} };

async function bounded(state, label, action, reserve = 400) {
  const available = state.remaining - reserve;
  if (available <= 0) { state.operations[label] = 'not attempted: budget exhausted'; return; }
  const start = Date.now();
  let timer;
  try {
    const result = await Promise.race([
      Promise.resolve().then(action),
      new Promise((_, reject) => { timer = setTimeout(() => reject(Error('diagnostic deadline; operation may remain pending')), available); }),
    ]);
    state.operations[label] = 'completed';
    return result;
  } catch (error) { state.operations[label] = failure(error); }
  finally { clearTimeout(timer); state.remaining -= Date.now() - start; }
}

export async function installSkewDiagnostics(page, testInfo, encodedKey) {
  try { await install(page, testInfo, encodedKey); } catch (error) { incomplete(error); }
}

async function install(page, testInfo, encodedKey) {
  const state = { remaining: 3000, operations: {}, cdp: [], cdpDropped: 0, native: null,
    key: decodeURIComponent(encodedKey), token: `skew-${++nextFlight}`, slot: '__nextgoSkewDiagnostic', browser: testInfo.project.use.browserName || 'unknown' };
  flights.set(page, state);
  try {
    const match = value => {
      try { const u = new URL(value); return /^\/posts\/?$/.test(u.pathname) && u.searchParams.get('skew-prefetch') === state.key; }
      catch { return false; }
    };
    const publicURL = value => {
      try { const u = new URL(value); return trim(`${u.origin}${u.pathname}?skew-prefetch=${encodeURIComponent(state.key)}`, 512); }
      catch { return ''; }
    };
    const add = event => { if (state.cdp.length < cap) state.cdp.push(event); else state.cdpDropped++; };
    if (page.context().browser()?.browserType().name() === 'chromium') {
      state.session = await bounded(state, 'cdp-create', () => page.context().newCDPSession(page));
      if (state.session) {
        const ids = new Set();
        state.cdpHandlers = {
          'Network.requestWillBeSent': e => {
            if (!match(e.request.url)) return;
            if (ids.size >= cap && !ids.has(e.requestId)) { state.cdpDropped++; return; }
            ids.add(e.requestId);
            add({ kind: 'cdp-request', id: trim(e.requestId), t: e.timestamp, wallTime: e.wallTime,
              url: publicURL(e.request.url), resourceType: e.type, redirectStatus: e.redirectResponse?.status ?? null });
          },
          'Network.responseReceived': e => { if (ids.has(e.requestId)) add({ kind: 'cdp-response', id: trim(e.requestId), t: e.timestamp, status: e.response.status, resourceType: e.type, url: publicURL(e.response.url) }); },
          'Network.loadingFinished': e => { if (ids.has(e.requestId)) add({ kind: 'cdp-finished', id: trim(e.requestId), t: e.timestamp, encodedBytes: e.encodedDataLength }); },
          'Network.loadingFailed': e => { if (ids.has(e.requestId)) add({ kind: 'cdp-failed', id: trim(e.requestId), t: e.timestamp, canceled: !!e.canceled, error: trim(e.errorText) }); },
        };
        for (const [name, fn] of Object.entries(state.cdpHandlers)) state.session.on(name, fn);
        await bounded(state, 'cdp-enable', () => state.session.send('Network.enable'));
      }
    } else state.operations.cdp = 'unsupported: Chromium CDP only';
    state.nativeInstallation = await bounded(state, 'native-install', () => page.evaluate(({ key, slot, cap, token }) => {
      if (window[slot]) return { installed: false, reason: 'diagnostic slot already owned' };
      if (![window.fetch, window.AbortController?.prototype.abort, window.ReadableStream?.prototype.getReader, window.ReadableStreamDefaultReader?.prototype.read].every(value => typeof value === 'function')) return { installed: false, reason: 'unsupported native APIs' };
      const events = [], signals = new WeakMap(), streams = new WeakMap(), readers = new WeakMap(), listeners = [];
      const state = { token, events, dropped: 0, fetches: 0, installed: false, restored: false, active: true };
      const text = (value, n = 160) => String(value ?? '').slice(0, n);
      const reason = value => {
        try {
          if (value instanceof Error || value instanceof DOMException) return { type: text(value.name), message: text(value.message) };
          return { type: value === null ? 'null' : typeof value, value: typeof value === 'string' ? text(value) : ['number', 'boolean'].includes(typeof value) || value === null ? value : undefined };
        }
        catch { return { type: 'unreadable' }; }
      };
      const record = event => {
        try { if (!state.active) return; if (events.length < cap) events.push({ t: performance.now(), timeOrigin: performance.timeOrigin, ...event }); else state.dropped++; }
        catch { /* Diagnostics must never affect application control flow. */ }
      };
      const target = input => {
        try {
          const u = new URL(typeof input === 'string' || input instanceof URL ? input : input.url, location.href);
          if (!/^\/posts\/?$/.test(u.pathname) || u.searchParams.get('skew-prefetch') !== key) return null;
          return text(`${u.origin}${u.pathname}?skew-prefetch=${encodeURIComponent(key)}`, 512);
        } catch { return null; }
      };
      const originalFetch = window.fetch, originalAbort = AbortController.prototype.abort;
      const originalGetReader = ReadableStream.prototype.getReader, originalRead = ReadableStreamDefaultReader.prototype.read;
      const nativeThen = Promise.prototype.then;
      function observe(promise, resolved, rejected) {
        // Attach observers, but return the *original* native promise to callers.
        Reflect.apply(nativeThen, promise, [value => { try { resolved(value); } catch {} }, error => { try { rejected(error); } catch {} }]);
      }
      function fetchObserver(...args) {
        const promise = Reflect.apply(originalFetch, this, args);
        try {
          const url = target(args[0]);
          if (!url) return promise;
          const id = ++state.fetches;
          if (id > cap || !state.active) { state.dropped++; return promise; }
          const signal = args[1]?.signal || (args[0] instanceof Request ? args[0].signal : null);
          const info = { id, url };
          record({ kind: 'fetch-start', ...info });
          if (signal) {
            signals.set(signal, info);
            const aborted = () => record({ kind: 'signal-abort', ...info, reason: reason(signal.reason) });
            signal.addEventListener('abort', aborted, { once: true });
            listeners.push(() => signal.removeEventListener('abort', aborted));
            if (signal.aborted) aborted();
          }
          observe(promise, response => {
            record({ kind: 'fetch-response', ...info, status: response.status, url: target(response.url) || url });
            if (response.body) streams.set(response.body, info);
            else record({ kind: 'read-eof', ...info, emptyBody: true });
          }, error => record({ kind: 'fetch-reject', ...info, reason: reason(error) }));
        } catch {}
        return promise;
      }
      function abortObserver(...args) {
        try {
          const info = signals.get(this.signal);
          if (info) record({ kind: 'abort-call', ...info, reason: reason(args[0]), stack: text(new Error().stack, 1024) });
        } catch {}
        return Reflect.apply(originalAbort, this, args);
      }
      function getReaderObserver(...args) {
        const reader = Reflect.apply(originalGetReader, this, args);
        try { const info = streams.get(this); if (info) readers.set(reader, info); } catch {}
        return reader;
      }
      function readObserver(...args) {
        const promise = Reflect.apply(originalRead, this, args);
        try {
          const info = readers.get(this);
          if (info && state.active && events.length < cap) observe(promise, part => record({ kind: part.done ? 'read-eof' : 'read-chunk', ...info, bytes: part.done ? 0 : part.value?.byteLength ?? 0 }),
            error => record({ kind: 'read-reject', ...info, reason: reason(error) }));
        } catch {}
        return promise;
      }
      state.restore = () => {
        state.active = false;
        const restoration = [];
        for (const [owner, key, wrapper, original] of [[window, 'fetch', fetchObserver, originalFetch], [AbortController.prototype, 'abort', abortObserver, originalAbort], [ReadableStream.prototype, 'getReader', getReaderObserver, originalGetReader], [ReadableStreamDefaultReader.prototype, 'read', readObserver, originalRead]]) {
          try {
            if (owner[key] === wrapper) owner[key] = original;
            restoration.push({ key, oursRemaining: owner[key] === wrapper });
          } catch (error) { restoration.push({ key, error: text(error), oursRemaining: true }); }
        }
        for (const remove of listeners) { try { remove(); } catch {} }
        state.restoration = restoration;
        state.restored = restoration.every(item => !item.oursRemaining);
      };
      window[slot] = state;
      window.fetch = fetchObserver;
      AbortController.prototype.abort = abortObserver;
      ReadableStream.prototype.getReader = getReaderObserver;
      ReadableStreamDefaultReader.prototype.read = readObserver;
      state.installed = window.fetch === fetchObserver && AbortController.prototype.abort === abortObserver && ReadableStream.prototype.getReader === getReaderObserver && ReadableStreamDefaultReader.prototype.read === readObserver;
      return { installed: state.installed };
    }, { key: state.key, slot: state.slot, cap, token: state.token }));
  } catch (error) { state.operations.install = failure(error); }
}

export async function captureSkewDiagnostics(page, testInfo) {
  try { await capture(page, testInfo); } catch (error) { incomplete(error); }
}

async function capture(page, testInfo) {
  const state = flights.get(page);
  if (!state) return;
  flights.delete(page);
  try {
    state.native = await bounded(state, 'native-snapshot-restore', () => page.evaluate(({ slot, token }) => {
      const state = window[slot];
      if (!state || state.token !== token) return { installed: false, reason: 'diagnostic state absent (navigation or failed installation)' };
      let restorationError = null;
      try { state.restore(); } catch (error) { restorationError = String(error).slice(0, 160); }
      const result = { events: state.events, dropped: state.dropped, fetches: state.fetches, installed: state.installed, restored: state.restored, restoration: state.restoration, restorationError };
      if (window[slot] === state) delete window[slot];
      return result;
    }, { slot: state.slot, token: state.token }));
  } catch (error) { state.operations.capture = failure(error); }
  finally {
    if (state.session) {
      try { for (const [name, fn] of Object.entries(state.cdpHandlers || {})) state.session.off(name, fn); }
      catch (error) { state.operations['cdp-unsubscribe'] = failure(error); }
      await bounded(state, 'cdp-detach', () => state.session.detach());
    }
  }
  state.operations.attachment = 'not completed when serialized';
  const evidence = { version: 1, budgetMS: 3000, budgetRemainingMS: Math.max(0, state.remaining),
    caveat: 'Observer microtasks can perturb timing; document replacement can discard the native ledger. No causal classification. No extra HTTP requests, body clones or body consumption. Context teardown owns physical page/session closure when restoration/detach is partial.',
    operations: state.operations, cdp: state.cdp, cdpDropped: state.cdpDropped, nativeInstallation: state.nativeInstallation || null, native: state.native || null };
  let body = Buffer.from(JSON.stringify(evidence));
  while (body.length > 256 * 1024 && (evidence.cdp.length || evidence.native?.events?.length)) {
    evidence.attachmentTruncated = true;
    if (evidence.native?.events?.length) evidence.native.events.pop(); else evidence.cdp.pop();
    body = Buffer.from(JSON.stringify(evidence));
  }
  await bounded(state, 'attachment', () => testInfo.attach('skew-diagnostics', { body, contentType: 'application/json' }), 0);
}
