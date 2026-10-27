// Failure-only DOM observation, after original assertions and before teardown.
// One synchronous browser evaluation; no worker API reads, wrappers or repairs.
export async function observeOfflineUpdateFailure(context, info) {
  const report = {version: 1, observation: 'unavailable', values: null};
  try {
    if (!context) return;
    const pages = context.pages();
    if (pages.length !== 1 || pages[0].isClosed()) return;
    const values = await pages[0].evaluate(() => {
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
      return {
        lastError: allowedErrors.includes(rawError) ? rawError : rawError === null ? 'unavailable' : 'unrecognized',
        operationError: ['', 'busy', 'unavailable', 'unsupported', 'conflict', 'protocol', 'unknown', 'canceled', 'deadline', 'operation-failed'].includes(rawOperation) ? rawOperation : 'unrecognized',
        renderedState: ['disabled', 'checking', 'enabling', 'updating', 'disabling', 'working', 'waiting', 'error', 'busy', 'ready', 'staged', 'failed', 'unavailable', 'unsupported', 'unconfigured'].includes(rawState) ? rawState : 'unrecognized',
        completedOperations: typeof rawCompleted === 'string' && /^(?:0|[1-9][0-9]{0,9})$/.test(rawCompleted) && Number(rawCompleted) <= 0xffffffff ? Number(rawCompleted) : null,
        controlling: boolean('data-offline-controlling'),
        enabled: boolean('data-offline-enabled'),
        waitingGenerationPresent: waiting === '' ? false : typeof waiting === 'string' && /^[a-f0-9]{64}$/.test(waiting) ? true : null,
      };
    });
    if (values !== null) { report.observation = 'observed'; report.values = values; }
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
