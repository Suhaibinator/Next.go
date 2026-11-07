// Test-only owner accessor observation. This self-contained function is passed
// directly to page.evaluate; it has no eval, prototype patch or scroll sample.
// Rows describe JS accessor calls, not native scrolling or its original cause.
export function observeRetainedOwnerScroll(argument = {}) {
  const world = argument.world || globalThis;
  const key = '__nextgoRetainedOwnerScrollObservationV1';
  const command = argument.command || 'install';
  const descriptor = Object.getOwnPropertyDescriptor;
  const define = Object.defineProperty;
  const apply = Reflect.apply;
  const empty = reason => ({version: 1, installed: false, complete: false, reason, total: 0, dropped: 0, rows: [], disposed: false, stateCleared: false});
  const ownState = () => {
    const entry = descriptor(world, key);
    return entry && Object.hasOwn(entry, 'value') && entry.value?.marker === key ? entry.value : null;
  };
  if (command === 'finish') {
    const state = ownState();
    if (!state) return empty('not-installed');
    state.active = false;
    // Copy fixed scalars only; never invoke the native scroll accessor here.
    const report = {version: 1, installed: true, complete: state.complete, reason: '', total: state.total, dropped: state.dropped, rows: state.rows.map(row => ({...row})), disposed: false, stateCleared: false};
    try {
      const current = descriptor(state.owner, 'scrollTop');
      if (current?.get === state.get && current?.set === state.set && current.configurable) {
        report.disposed = delete state.owner.scrollTop;
        report.disposed = report.disposed && !descriptor(state.owner, 'scrollTop');
      }
      if (!report.disposed) {report.complete = false; report.reason = 'accessor-ownership-lost';}
    } catch {report.complete = false; report.reason = 'accessor-disposal-unavailable';}
    try {
      if (descriptor(world, key)?.value === state) report.stateCleared = delete world[key];
    } catch {}
    if (!report.stateCleared) {report.complete = false; report.reason = 'state-disposal-unavailable';}
    state.rows.length = 0;
    state.owner = null;
    return report;
  }
  if (command !== 'install') return empty('unsupported-command');
  if (descriptor(world, key)) return empty('state-already-present');
  let state;
  try {
    const owner = argument.owner || world.document.querySelector('#nextgo-app > [data-nextgo-island]');
    if (!owner || descriptor(owner, 'scrollTop')) return empty('own-accessor-or-owner-unavailable');
    let native, parent = Object.getPrototypeOf(owner);
    for (let depth = 0; parent && depth < 16; depth++, parent = Object.getPrototypeOf(parent)) {
      native = descriptor(parent, 'scrollTop');
      if (native) break;
    }
    if (!native || typeof native.get !== 'function' || typeof native.set !== 'function') return empty('native-accessor-unavailable');
    const nativeGet = native.get, nativeSet = native.set;
    state = {marker: key, owner, active: true, complete: true, total: 0, dropped: 0, rows: [], get: null, set: null};
    const reserve = (get, value) => {
      if (!state.active) return null;
      if (state.total < 0xffffffff) state.total++;
      else state.complete = false;
      if (state.rows.length >= 64) {
        state.complete = false;
        state.dropped = Math.min(0xffffffff, state.dropped + 1);
        return null;
      }
      const present = typeof value === 'number' && Number.isFinite(value);
      const row = {sequence: state.total, get, set: !get, threw: false, requestedPresent: !get && present, requested: !get && present ? value : 0, returnedPresent: false, returned: 0, phaseKnown: false, pathOwner: false, pathB: false, busy: false, connected: false, hidden: false, active: false};
      state.rows.push(row);
      // Attribute/connectivity reads only; no geometry, focus or scroll reads.
      try {
        const app = world.document.getElementById('nextgo-app');
        const path = world.location.pathname;
        const connected = owner.isConnected, hidden = owner.hidden;
        const active = owner.hasAttribute('data-nextgo-view-active');
        const busy = app?.hasAttribute('aria-busy');
        if (typeof path === 'string' && typeof connected === 'boolean' && typeof hidden === 'boolean' && typeof active === 'boolean' && typeof busy === 'boolean') {
          Object.assign(row, {phaseKnown: true, pathOwner: path === '/owner', pathB: path === '/b', busy, connected, hidden, active});
        } else state.complete = false;
      } catch {state.complete = false;}
      return row;
    };
    const forward = (fn, receiver, args, get) => {
      let row;
      try {row = reserve(get, args[0]);} catch {state.complete = false;}
      try {
        // Exactly one native call. In particular, setters never read scrollTop
        // and do not coerce their input before the native setter receives it.
        const value = apply(fn, receiver, args);
        if (row && typeof value === 'number' && Number.isFinite(value)) {row.returnedPresent = true; row.returned = value;}
        return value;
      } catch (error) {
        if (row) row.threw = true;
        throw error; // Preserve exact native exception identity.
      }
    };
    state.get = function () {return forward(nativeGet, this, [], true);};
    state.set = function (value) {return forward(nativeSet, this, [value], false);};
    // The owner must not already have an own property. No shared prototype is
    // modified, and finish only deletes these exact installed functions.
    define(world, key, {value: state, configurable: true});
    define(owner, 'scrollTop', {get: state.get, set: state.set, enumerable: native.enumerable, configurable: true});
    return {version: 1, installed: true, complete: true, reason: '', total: 0, dropped: 0, rows: [], disposed: false, stateCleared: false};
  } catch {
    if (state) {
      state.active = false;
      try {
        const current = descriptor(state.owner, 'scrollTop');
        if (current?.get === state.get && current?.set === state.set && current.configurable) delete state.owner.scrollTop;
      } catch {}
      try {if (descriptor(world, key)?.value === state) delete world[key];} catch {}
      state.owner = null;
    }
    return empty('installation-unavailable');
  }
}
