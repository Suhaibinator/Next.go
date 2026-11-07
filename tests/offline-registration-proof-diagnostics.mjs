// Failure-only observations, never ownership authority or a replay of the failed
// operation. Sequential reads and a new realm can change timing; these results
// describe only the later observation, not the earlier failure or its cause.
export async function observeOfflineRegistrationProof(context, info, origin) {
  const deadline = Date.now() + 3000;
  const report = {version: 1, phase: 'post-failure', observation: 'not-started', cleanup: 'not-started'};
  const bounded = async (work, cap = 3000) => {
    const remaining = Math.min(cap, deadline - Date.now());
    if (remaining <= 0) return {state: 'deadline'};
    let timer;
    try {
      return await Promise.race([
        Promise.resolve().then(work).then(value => ({state: 'complete', value}), () => ({state: 'rejected'})),
        new Promise(resolve => {timer = setTimeout(() => resolve({state: 'deadline'}), remaining);}),
      ]);
    } finally {clearTimeout(timer);}
  };
  try {
    if (!context) {report.observation = 'context-unavailable'; return;}
    const pages = context.pages();
    if (pages.length > 8) {report.observation = 'page-bound'; return;}
    const page = pages.find(candidate => {
      try {return new URL(candidate.url()).origin === origin;} catch {return false;}
    });
    if (!page) {report.observation = 'owned-page-unavailable'; return;}
    const result = await bounded(() => page.evaluate(async expectedOrigin => {
      const started = performance.now(), expires = Date.now() + 2200;
      const output = {eligibility: 'unavailable', realms: [], frame: {created: false, load: 'not-started', removed: false, listenerRemoved: false}};
      let pendingNativeReads = 0;
      const elapsed = () => Math.min(3000, Math.max(0, Math.round(performance.now() - started)));
      const wait = async (work, nativeRead = false) => {
        if (Date.now() >= expires) return {state: 'deadline'};
        let timer;
        const pending = Promise.resolve().then(() => {
          if (nativeRead) pendingNativeReads++;
          return work();
        }).then(value => {
          if (nativeRead) pendingNativeReads--;
          return {state: 'complete', value};
        }, () => {
          if (nativeRead) pendingNativeReads--;
          return {state: 'rejected'};
        });
        try {
          return await Promise.race([pending, new Promise(resolve => {timer = setTimeout(() => resolve({state: 'deadline'}), Math.max(0, expires - Date.now()));})]);
        } finally {clearTimeout(timer);}
      };
      let frame, onLoad;
      try {
        if (location.origin !== expectedOrigin) return output;
        const controls = document.querySelector('[data-offline-controls]');
        if (!controls || controls.getAttribute('data-offline-last-error') !== 'nextgo: a conflicting service worker owns an overlapping scope [registration-proof-empty-lookup]' || controls.getAttribute('data-offline-operation-error') !== 'conflict') {
          output.eligibility = 'different-failure'; return output;
        }
        const raw = document.body?.getAttribute('data-nextgo-offline');
        if (typeof raw !== 'string' || raw.length > 16384) return output;
        const descriptor = JSON.parse(raw);
        if (descriptor?.version !== 1 || typeof descriptor.scope !== 'string' || typeof descriptor.workerURL !== 'string' || descriptor.scope.length > 4096 || descriptor.workerURL.length > 4096) return output;
        const scopeURL = new URL(descriptor.scope), workerURL = new URL(descriptor.workerURL);
        if (scopeURL.origin !== expectedOrigin || workerURL.origin !== expectedOrigin || scopeURL.username || scopeURL.password || workerURL.username || workerURL.password) return output;
        if (scopeURL.href !== descriptor.scope || workerURL.href !== descriptor.workerURL || scopeURL.search || scopeURL.hash || workerURL.search || workerURL.hash || !descriptor.workerURL.startsWith(descriptor.scope)) return output;
        output.eligibility = 'exact-lookup-failure';
        const workerProof = worker => {
          if (!worker) return 'empty';
          return typeof worker === 'object' && typeof worker.scriptURL === 'string' && worker.scriptURL === descriptor.workerURL ? 'owned' : 'foreign';
        };
        const recordProof = record => {
          const proof = {object: !!record && typeof record === 'object', scopeString: false, exactScope: false, populatedSlots: 0, ownedSlots: 0, foreignSlots: 0, strict: false};
          if (!proof.object) return proof;
          const scope = record.scope;
          proof.scopeString = typeof scope === 'string' && scope.length <= 4096;
          proof.exactScope = proof.scopeString && scope === descriptor.scope;
          for (const key of ['active', 'waiting', 'installing']) {
            const state = workerProof(record[key]);
            if (state !== 'empty') proof.populatedSlots++;
            if (state === 'owned') proof.ownedSlots++;
            if (state === 'foreign') proof.foreignSlots++;
          }
          proof.strict = proof.exactScope && proof.populatedSlots > 0 && proof.foreignSlots === 0;
          return proof;
        };
        const inventoryProof = list => {
          const summary = {array: Array.isArray(list), count: 0, overBound: false, complete: false, malformed: 0, overlaps: 0, exact: 0, foreignOverlaps: 0, emptyExact: 0, foreignSlots: 0, strict: false};
          const exact = [];
          if (!summary.array) return {summary, exact};
          summary.count = Math.min(65, list.length);
          if (list.length > 64) {summary.overBound = true; return {summary, exact};}
          for (const record of list) {
            if (!record || typeof record !== 'object' || typeof record.scope !== 'string' || record.scope.length > 4096) {summary.malformed++; continue;}
            const scope = record.scope;
            if (!scope.startsWith(descriptor.scope) && !descriptor.scope.startsWith(scope)) continue;
            summary.overlaps++;
            const proof = recordProof(record);
            summary.foreignSlots += proof.foreignSlots;
            if (proof.exactScope) {summary.exact++; exact.push(record); if (!proof.populatedSlots) summary.emptyExact++;}
            else summary.foreignOverlaps++;
          }
          summary.complete = true;
          summary.strict = !summary.malformed && summary.overlaps === 1 && summary.exact === 1 && !summary.foreignOverlaps && !summary.emptyExact && !summary.foreignSlots;
          return {summary, exact};
        };
        const observe = async (realm, label) => {
          const result = {realm: label, startMs: elapsed(), state: 'unavailable', api: {container: false, fullList: false, targeted: false}};
          output.realms.push(result);
          try {
            const container = realm.navigator.serviceWorker;
            result.api = {container: !!container, fullList: typeof container?.getRegistrations === 'function', targeted: typeof container?.getRegistration === 'function'};
            if (!result.api.fullList || !result.api.targeted) return;
            const first = await wait(() => container.getRegistrations(), true);
            result.initialRead = first.state;
            if (first.state !== 'complete') return;
            const initial = inventoryProof(first.value); result.initial = initial.summary;
            if (!initial.summary.complete) {result.state = 'inventory-unavailable'; return;}
            const target = await wait(() => container.getRegistration(descriptor.scope), true);
            result.targetRead = target.state;
            if (target.state !== 'complete') return;
            result.targetBeforeFinal = recordProof(target.value);
            const final = await wait(() => container.getRegistrations(), true);
            result.finalRead = final.state;
            if (final.state !== 'complete') return;
            const latest = inventoryProof(final.value); result.final = latest.summary;
            result.controller = workerProof(container.controller);
            result.targetAfterFinal = recordProof(target.value);
            result.identityLatestExact = latest.exact.length === 1 && target.value === latest.exact[0];
            result.postFailureStrictProofObserved = latest.summary.strict && result.targetAfterFinal.strict && result.identityLatestExact && result.controller !== 'foreign';
            result.state = 'observed';
          } catch {result.state = 'read-or-shape-rejected';}
          finally {result.endMs = elapsed();}
        };
        await observe(window, 'parent');
        if (Date.now() >= expires) {output.frame.load = 'deadline'; return output;}
        frame = document.createElement('iframe');
        frame.hidden = true; frame.inert = true; frame.tabIndex = -1;
        frame.setAttribute('aria-hidden', 'true'); frame.setAttribute('inert', '');
        frame.style.setProperty('display', 'none', 'important'); frame.src = 'about:blank';
        const loaded = new Promise(resolve => {onLoad = () => resolve(true); frame.addEventListener('load', onLoad, {once: true});});
        document.documentElement.appendChild(frame); output.frame.created = true;
        const load = await wait(() => loaded); output.frame.load = load.state;
        if (load.state !== 'complete') return output;
        const realm = frame.contentWindow;
        output.frame.sameOrigin = !!realm && realm.origin === expectedOrigin && realm.document === frame.contentDocument;
        output.frame.emptyAboutBlank = !!realm && realm.location.href === 'about:blank' && realm.document.body?.childNodes.length === 0;
        if (output.frame.sameOrigin && output.frame.emptyAboutBlank) await observe(realm, 'empty-frame');
        return output;
      } catch {output.observationError = 'unavailable'; return output;}
      finally {
        if (frame) {
          if (onLoad) {frame.removeEventListener('load', onLoad); output.frame.listenerRemoved = true;}
          frame.remove(); output.frame.removed = !frame.isConnected;
        }
        output.pendingNativeReadsAtReturn = pendingNativeReads;
        output.endMs = elapsed();
      }
    }, origin), 2700);
    report.observation = result.state;
    report.cleanup = result.state === 'complete' ? 'browser-result' : 'unknown-until-owned-context-close';
    if (result.state === 'complete') report.result = result.value;
  } catch {report.observation = 'collector-unavailable';}
  finally {
    // No raw error messages, URLs, descriptors, storage or profile data escape.
    // A timed-out browser evaluation/native promise may survive until the
    // existing fixture closes its owned context; never claim physical joining.
    const serialized = JSON.stringify(report);
    const body = Buffer.byteLength(serialized) <= 16384 ? serialized : '{"version":1,"phase":"post-failure","observation":"report-bound","cleanup":"unknown-until-owned-context-close"}';
    try {
      const attached = await bounded(() => info.attach('offline-post-failure-registration-proof', {body, contentType: 'application/json'}));
      if (attached.state !== 'complete') console.warn('Offline registration diagnostic attachment incomplete: ' + body);
    } catch {console.warn('Offline registration diagnostic attachment unavailable');}
  }
}
