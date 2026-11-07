// Evidence only: observer microtasks, allocations and DOM reads can perturb timing.
// Original native objects/promises/receivers survive; no body clones/extra reads.
const flights = new WeakMap();
let nextToken = 0;
const trim = value => { try { return String(value?.message || value || '').slice(0, 160); } catch { return 'unreadable diagnostic error'; } };
async function bounded(s, label, action, reserve = 400) {
  const available = s.remaining - reserve;
  if (available <= 0) { s.operations[label] = 'not attempted: diagnostic budget exhausted'; return; }
  const start = Date.now(); let timer;
  try {
    const value = await Promise.race([Promise.resolve().then(action), new Promise((_, reject) => { timer = setTimeout(() => reject(Error('diagnostic deadline; operation may remain pending')), available); })]);
    s.operations[label] = 'completed'; return value;
  } catch (error) { s.operations[label] = trim(error); }
  finally { clearTimeout(timer); s.remaining -= Date.now() - start; }
}
const incomplete = error => { try { console.warn(`Modal-refresh diagnostic incomplete: ${trim(error)}`); } catch {} };
export async function installModalRefreshDiagnostics(page) {
  const s = { token: `modal-${++nextToken}`, slot: '__nextgoModalRefreshDiagnostic', remaining: 3000, operations: {}, arms: [] };
  flights.set(page, s);
  try {
    s.install = await bounded(s, 'install', () => page.evaluate(({slot, token}) => {
      if (window[slot]) return { installed: false, reason: 'slot already owned' };
      if (![window.fetch, window.AbortController?.prototype.abort, window.ReadableStream?.prototype.getReader, window.ReadableStreamDefaultReader?.prototype.read, window.MutationObserver].every(v => typeof v === 'function')) return { installed: false, reason: 'unsupported native APIs' };
      const state = { token, active: true, installed: false, restored: false, native: [], go: [], nativeDropped: 0, goDropped: 0, tracked: 0, cycles: [], current: null, unsupported: [], unsupportedDropped:0 };
      const signals = new WeakMap(), streams = new WeakMap(), readers = new WeakMap(), listeners = [];
      const warn = message => {if(state.unsupported.length<16)state.unsupported.push(message);else state.unsupportedDropped++;};
      const now = () => ({ t: performance.now(), timeOrigin: performance.timeOrigin });
      const record = (kind, event) => { try { if (!state.active) return; if (state[kind].length < 128) state[kind].push({...now(), ...event}); else state[kind + 'Dropped']++; } catch {} };
      const text = (value, n = 160) => String(value ?? '').slice(0, n);
      const reason = value => { try { return value instanceof Error || value instanceof DOMException ? { type: text(value.name), message: text(value.message) } : { type: typeof value, value: typeof value === 'string' ? text(value) : typeof value === 'number' || typeof value === 'boolean' ? value : null }; } catch { return {type:'unreadable'}; } };
      const ownedURL = input => {
        try { const u = new URL(typeof input === 'string' || input instanceof URL ? input : input.url, location.href); return u.origin === location.origin && u.pathname === '/photos/one' && !u.search ? `${u.origin}/photos/one` : null; } catch { return null; }
      };
      const intent = args => {
        const headers = new Headers(args[1]?.headers || (args[0] instanceof Request ? args[0].headers : undefined));
        const values = {};
        for (const name of ['x-nextgo-navigate', 'x-nextgo-purpose', 'x-nextgo-stream', 'x-nextgo-metadata']) {
          const value = headers.get(name); values[name] = value === null ? null : value === '1' || value === 'prefetch' ? value : 'other';
        }
        return values;
      };
      const app = document.getElementById('nextgo-app'), status = document.getElementById('nextgo-status'), description = document.querySelector('meta[name="description"]');
      const fixed = value => value === null ? null : value === '' || value === 'true' ? value : 'other';
      const consume = records => {
        if(state.go.length>=128){state.goDropped+=records.length;return;}
        if(records.length>128)state.goDropped+=records.length-128;
        const items=records.slice(0,128);
        for (let i = 0; i < items.length; i++) {
          const r = items[i], cycle = state.current;
          const key = r.target === app ? 'app-busy' : r.target === status ? 'status-hidden' : 'description-content';
          // Reconstruct batched transitions: next oldValue is this mutation's
          // resulting value. A final current value alone could miss fast starts.
          const next = items.slice(i + 1).find(n => n.target === r.target && n.attributeName === r.attributeName);
          const after = key === 'description-content' ? null : next ? next.oldValue : r.target.getAttribute(r.attributeName);
          record('go', { kind: 'mutation', cycle: cycle?.index || 0, key, old: key === 'description-content' ? 'not collected' : fixed(r.oldValue), after: key === 'description-content' ? 'write' : fixed(after) });
          if (!cycle) continue;
          if (key === 'app-busy' && after === 'true') cycle.busyStart = true;
          if (key === 'status-hidden' && after === null) cycle.statusStart = true;
          if (cycle.busyStart && cycle.statusStart && !cycle.pending) { cycle.pending = true; record('go', {kind:'pending-pattern', cycle:cycle.index}); }
          if (key === 'description-content' && cycle.pending) { cycle.commitMarkerWrites++; record('go', {kind:'managed-commit-marker', cycle:cycle.index}); }
          if (key === 'app-busy' && after !== 'true' && cycle.pending) cycle.busyClear = true;
          if (key === 'status-hidden' && after !== null && cycle.pending) cycle.statusClear = true;
          if (cycle.pending && cycle.commitMarkerWrites && cycle.busyClear && cycle.statusClear && !cycle.patternComplete) { cycle.patternComplete = true; record('go', {kind:'pending-marker-clear-pattern', cycle:cycle.index}); }
        }
      };
      let observer;
      if (app && status && description) {
        observer = new MutationObserver(consume);
        observer.observe(app, {attributes:true, attributeOldValue:true, attributeFilter:['aria-busy']});
        observer.observe(status, {attributes:true, attributeOldValue:true, attributeFilter:['hidden']});
        observer.observe(description, {attributes:true, attributeFilter:['content']});
      } else warn('missing Go-owned marker nodes');
      state.arm = index => {
        if (index !== 1 && index !== 2 || state.cycles.some(c => c.index === index)) return {armed:false, reason:'unsupported cycle'};
        if (observer) consume(observer.takeRecords());
        const cycle = {index, ...now(), busyStart:false, statusStart:false, pending:false, commitMarkerWrites:0, busyClear:false, statusClear:false, patternComplete:false, fetches:0, bytes:0, eof:0, aborts:0, errors:0};
        state.current = cycle; state.cycles.push(cycle); record('go', {kind:'armed', cycle:index, initialBusy:app?.getAttribute('aria-busy') === 'true', initialStatusHidden:status?.hidden ?? null}); return {armed:true};
      };
      const originalFetch = window.fetch, originalAbort = AbortController.prototype.abort, originalGetReader = ReadableStream.prototype.getReader, originalRead = ReadableStreamDefaultReader.prototype.read, then = Promise.prototype.then;
      const observe = (promise, good, bad) => { Reflect.apply(then, promise, [value => {if(!state.active)return;try {good(value);} catch {warn('native observer failed');}}, error => {if(!state.active)return;try {bad(error);} catch {warn('native observer failed');}}]); };
      function fetchObserver(...args) {
        const promise = Reflect.apply(originalFetch, this, args);
        try {
          if(!state.active)return promise;
          const url = ownedURL(args[0]); if (!url) return promise;
          if (state.tracked >= 64 || !state.active) {state.nativeDropped++; return promise;}
          const id = ++state.tracked, cycle = state.current, info = {id, cycle:cycle?.index || 0, url, intent:intent(args)};
          if (cycle) cycle.fetches++;
          record('native', {kind:'fetch-start', ...info});
          const signal = args[1]?.signal || (args[0] instanceof Request ? args[0].signal : null);
          if (signal) {
            signals.set(signal, {info, cycle});
            const onAbort = () => {if (cycle) cycle.aborts++; record('native', {kind:'signal-abort', ...info, reason:reason(signal.reason)});};
            signal.addEventListener('abort', onAbort, {once:true}); listeners.push(() => signal.removeEventListener('abort', onAbort)); if (signal.aborted) onAbort();
          }
          observe(promise, response => {
            const finalURL = ownedURL(response.url); record('native', {kind:'fetch-response', ...info, status:response.status, outsideTarget:!finalURL});
            if (!finalURL) return;
            if (response.body) streams.set(response.body, {info, cycle}); else {if (cycle) cycle.eof++; record('native', {kind:'read-eof', ...info, emptyBody:true});}
          }, error => {if (cycle) cycle.errors++; record('native', {kind:'fetch-reject', ...info, reason:reason(error)});});
        } catch {warn('fetch observer failed');}
        return promise;
      }
      function abortObserver(...args) {
        try {const entry = signals.get(this.signal); if (entry && state.active) {if (entry.cycle) entry.cycle.aborts++; record('native', {kind:'abort-call', ...entry.info, reason:reason(args[0]), stack:text(new Error().stack,1024)});}} catch {}
        return Reflect.apply(originalAbort, this, args);
      }
      function getReaderObserver(...args) {
        const reader = Reflect.apply(originalGetReader, this, args); try {const entry = streams.get(this); if (entry && state.active) readers.set(reader, entry);} catch {} return reader;
      }
      function readObserver(...args) {
        const promise = Reflect.apply(originalRead, this, args);
        try {const entry = readers.get(this); if (entry && state.active) observe(promise, part => {
          const bytes = part.done ? 0 : part.value?.byteLength ?? 0;
          if (entry.cycle) {entry.cycle.bytes += bytes; if (part.done) entry.cycle.eof++;}
          record('native', {kind:part.done?'read-eof':'read-chunk', ...entry.info, bytes});
        }, error => {if (entry.cycle) entry.cycle.errors++; record('native', {kind:'read-reject', ...entry.info, reason:reason(error)});});} catch {warn('reader observer failed');}
        return promise;
      }
      state.restore = () => {
        state.domDisconnected=!observer;
        if (observer) {try {consume(observer.takeRecords());} catch {warn('pending DOM mutation capture failed');} try {observer.disconnect();state.domDisconnected=true;} catch {warn('DOM observer disconnect failed');}}
        state.active = false; state.restoration = [];
        for (const [owner, name, wrapper, original] of [[window,'fetch',fetchObserver,originalFetch],[AbortController.prototype,'abort',abortObserver,originalAbort],[ReadableStream.prototype,'getReader',getReaderObserver,originalGetReader],[ReadableStreamDefaultReader.prototype,'read',readObserver,originalRead]]) {
          try {if (owner[name] === wrapper) owner[name] = original; state.restoration.push({name, oursRemaining:owner[name] === wrapper});} catch {state.restoration.push({name, oursRemaining:true});}
        }
        state.listenerCleanupComplete=true;
        for (const remove of listeners) {try {remove();} catch {state.listenerCleanupComplete=false;}}
        state.restored = state.domDisconnected && state.listenerCleanupComplete && state.restoration.every(x => !x.oursRemaining);
      };
      window[slot] = state; window.fetch = fetchObserver; AbortController.prototype.abort = abortObserver; ReadableStream.prototype.getReader = getReaderObserver; ReadableStreamDefaultReader.prototype.read = readObserver;
      state.installed = window.fetch === fetchObserver && AbortController.prototype.abort === abortObserver && ReadableStream.prototype.getReader === getReaderObserver && ReadableStreamDefaultReader.prototype.read === readObserver;
      return {installed:state.installed, unsupported:state.unsupported};
    }, {slot:s.slot, token:s.token}));
  } catch (error) {s.operations.install = trim(error);}
}
export async function armModalRefreshDiagnostics(page, index) {
  const s = flights.get(page); if (!s || s.arms.length>=2) return;
  try {s.arms.push(await bounded(s, `arm-${index}`, () => page.evaluate(({slot,token,index}) => {const state=window[slot];return state?.token===token?state.arm(index):{armed:false,reason:'state absent'};}, {slot:s.slot,token:s.token,index})));} catch (error) {s.operations.arm=trim(error);}
}
export async function captureModalRefreshDiagnostics(page, info) {
  const s = flights.get(page); if (!s) return; flights.delete(page);
  try {
    s.snapshot = await bounded(s, 'snapshot-restore', () => page.evaluate(({slot,token}) => {
      const state=window[slot]; if (state?.token!==token) return {installed:false,reason:'state absent after navigation or failed installation'};
      let restoreError=null; try {state.restore();} catch (error) {restoreError=String(error).slice(0,160);}
      const result={installed:state.installed,restored:state.restored,restoration:state.restoration,domDisconnected:state.domDisconnected,listenerCleanupComplete:state.listenerCleanupComplete,restoreError,native:state.native,go:state.go,nativeDropped:state.nativeDropped,goDropped:state.goDropped,tracked:state.tracked,cycles:state.cycles,unsupported:state.unsupported.slice(0,16),unsupportedDropped:state.unsupportedDropped};
      if(window[slot]===state)delete window[slot]; return result;
    }, {slot:s.slot,token:s.token}));
    const evidence={version:1,budgetMS:3000,budgetRemainingMS:Math.max(0,s.remaining),operations:s.operations,install:s.install||null,arms:s.arms,snapshot:s.snapshot||null,
      caveat:'Diagnostic observer microtasks/DOM reads can perturb timing. Native reader observations do not consume or clone bytes. DOM marker pattern is observational, not a completion counter after void Refresh returns. Clock t is callback-delivery performance.now milliseconds with timeOrigin. Replaced-document absence/timed-out operations are incomplete; outer context owns physical closure. No root-cause classification.'};
    s.operations.attachment='not completed when serialized'; let body=Buffer.from(JSON.stringify(evidence));
    while(body.length>256*1024&&(evidence.snapshot?.native.length||evidence.snapshot?.go.length)){evidence.attachmentTruncated=true;if(evidence.snapshot.native.length)evidence.snapshot.native.pop();else evidence.snapshot.go.pop();body=Buffer.from(JSON.stringify(evidence));}
    await bounded(s,'attachment',()=>info.attach('modal-refresh-diagnostics',{body,contentType:'application/json'}),0);
  } catch (error) {incomplete(error);}
}
