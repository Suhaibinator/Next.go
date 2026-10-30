// Failure-only DOM observation, after original assertions and before teardown.
// One synchronous browser evaluation; no worker API reads, wrappers or repairs.
export async function observeOfflineUpdateFailure(context, info) {
  const report = {version: 2, observation: 'unavailable', values: null,
    capturePhase: 'late-failed-case-before-fixture-close',
    lastErrorCapture: {present: null, complete: null, truncated: null},
    meaning: 'One late DOM snapshot, not operation-stage or native-cause evidence. Only exact allowlisted LastError values are complete and never truncated; unrecognized or unavailable values are not full captures.'};
  try {
    if (!context) return;
    const pages = context.pages();
    if (pages.length !== 1 || pages[0].isClosed()) return;
    const observation = await pages[0].evaluate(() => {
      const root = document.querySelector('[data-offline-controls]');
      if (!root || document.querySelectorAll('[data-offline-controls]').length !== 1) return null;
      const conflict = 'nextgo: a conflicting service worker owns an overlapping scope';
      const reasons = [
        'registration-list', 'registration-scope-type', 'registration-scope',
        'registration-active-worker', 'registration-waiting-worker', 'registration-installing-worker',
        'registration-duplicate', 'registration-empty', 'registration-empty-selection',
        'controller-worker', 'registration-proof-unavailable', 'registration-proof-missing',
        'registration-proof-empty-lookup', 'registration-proof-changed',
        'registration-proof-empty-final-inventory', 'registration-proof-empty-update',
        'registration-proof-empty-message', 'registration-proof-empty-unregister',
        'active-worker-changed', 'worker-state-target', 'message-worker',
        'controller-changed', 'waiting-worker-changed',
        'registration-proof-empty-installing',
        'parent-controller-changed', 'child-controller-changed', 'child-controller-invalid',
      ];
      const allowedErrors = ['', conflict, ...reasons.map(reason => `${conflict} [${reason}]`)];
      const rawError = root.getAttribute('data-offline-last-error');
      const rawOperation = root.getAttribute('data-offline-operation-error');
      const rawState = root.querySelector('[data-offline-state]')?.getAttribute('data-offline-state');
      const rawCompleted = root.getAttribute('data-offline-completed-operations');
      const boolean = name => {
        const value = root.getAttribute(name);
        return value === 'true' ? true : value === 'false' ? false : null;
      };
      const waiting = root.getAttribute('data-offline-waiting-generation');
      const completeError = allowedErrors.includes(rawError);
      const values = {
        lastError: allowedErrors.includes(rawError) ? rawError : rawError === null ? 'unavailable' : 'unrecognized',
        operationError: ['', 'busy', 'unavailable', 'unsupported', 'conflict', 'protocol', 'unknown', 'canceled', 'deadline', 'operation-failed'].includes(rawOperation) ? rawOperation : 'unrecognized',
        renderedState: ['disabled', 'checking', 'enabling', 'updating', 'disabling', 'working', 'waiting', 'error', 'busy', 'ready', 'staged', 'failed', 'unavailable', 'unsupported', 'unconfigured'].includes(rawState) ? rawState : 'unrecognized',
        completedOperations: typeof rawCompleted === 'string' && /^(?:0|[1-9][0-9]{0,9})$/.test(rawCompleted) && Number(rawCompleted) <= 0xffffffff ? Number(rawCompleted) : null,
        controlling: boolean('data-offline-controlling'),
        enabled: boolean('data-offline-enabled'),
        waitingGenerationPresent: waiting === '' ? false : typeof waiting === 'string' && /^[a-f0-9]{64}$/.test(waiting) ? true : null,
      };
      return {values, lastErrorCapture: {present: rawError !== null, complete: completeError, truncated: completeError ? false : null}};
    });
    if (observation !== null) {
      report.observation = 'observed'; report.values = observation.values;
      report.lastErrorCapture = observation.lastErrorCapture;
    }
  } catch { report.observation = 'unavailable'; }
  finally {
    // No raw exceptions, URLs, generation digests, profiles or DOM text escape.
    // A missing/closed page is unobserved; existing fixture owns physical close.
    try {
      const body = Buffer.from(JSON.stringify(report));
      if (body.length <= 4096) await info.attach('offline-update-failure-observation', {body, contentType: 'application/json'});
    } catch {} // Attachment failure cannot mask the original test assertion.
  }
}

// The retirement callback is the existing test fault cleanup. This helper adds
// no browser/SW reads: it emits only the fixed scalar result already returned.
// A returned call is not an independent physical-retirement proof.
function offlineRetirementProof(proof) {
  if (proof === null || typeof proof !== 'object') return {values: null, complete: false};
  let complete = true;
  const flag = value => {
    if (typeof value === 'boolean') return value;
    complete = false; return null;
  };
  const count = value => {
    if (Number.isInteger(value) && value >= 0 && value <= 0xffffffff) return value;
    complete = false; return null;
  };
  const callCount = proof.nativeUpdateCalls === null ? null : count(proof.nativeUpdateCalls);
  const witness = ['disabled', 'no-attempt', 'installed', 'called', 'unavailable'].includes(proof.nativeUpdateWitness) ? proof.nativeUpdateWitness : (complete = false, null);
  const values = {
    forcedParentEmptyOnly: flag(proof.forcedParentEmptyOnly),
    lists: count(proof.lists), lookups: count(proof.lookups),
    framesAdded: count(proof.framesAdded), framesRemoved: count(proof.framesRemoved),
    framesConnected: count(proof.framesConnected), maximumLiveFrames: count(proof.maximumLiveFrames),
    nativeUpdateCalls: callCount, nativeUpdateWitness: witness,
    nativeWitnessUnavailable: flag(proof.nativeWitnessUnavailable),
    pendingFrameListeners: count(proof.pendingFrameListeners),
  };
  return {values, complete};
}

export async function retireOfflineParentFault(page, info, retire, primaryFailed) {
  let proof, failure, returned = false;
  try {proof = await retire(page); returned = true;} catch (error) {failure = error;}
  // Retirement is attempted BEFORE any diagnostic projection/attachment. Even
  // throwing getters or rejected attachments cannot prevent that cleanup call.
  try {
    const projected = offlineRetirementProof(proof);
    const report = {version: 1, retirementCall: returned ? 'returned' : 'threw',
      capturePhase: 'after-parent-fault-retirement-attempt', proof: projected.values,
      proofComplete: projected.complete, naturalCause: 'UNKNOWN',
      meaning: 'Existing scalar fault-retirement result only; not a native operation-stage or physical client-ID proof. A missing attachment is unavailable evidence.'};
    const body = Buffer.from(JSON.stringify(report));
    if (body.length <= 4096) await info.attach('forced-parent-empty-retirement-proof', {body, contentType: 'application/json'});
  } catch {} // Diagnostic failure must not mask an assertion or cleanup error.
  // Preserve the original case assertion when both failed. If its body passed,
  // a cleanup throw must still fail the case, including a non-Error rejection.
  if (!returned && primaryFailed !== true) throw failure;
  return proof;
}
