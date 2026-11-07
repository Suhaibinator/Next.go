// Source-only acceptance fixture: the unchanged retained owner starts exactly
// the supplied hash-pinned production executable, in an owned empty directory.
// Point NEXTGO_RETAINED_SERVER_BINARY/SHA256 at a build of examples/merge.
// There is deliberately no build fallback or synthetic application response.
import {test as base, expect} from '@playwright/test';
import {startRetainedServer} from './retained-routes-fixture.mjs';

export const test = base.extend({native: async ({page}, use, info) => {
  const startup = new AbortController();
  const closed = () => startup.abort(Error('Owned merge page closed during startup'));
  page.once('close', closed);
  let native;
  try { native = await startRetainedServer({signal: startup.signal}); }
  catch (error) {
    if (error.retainedProof) await info.attach('merge-startup-proof', {body: JSON.stringify(error.retainedProof), contentType: 'application/json'});
    throw error;
  } finally { page.off('close', closed); }
  try { await use(native); }
  finally {
    const errors = [];
    try { await page.close(); } catch (error) { errors.push(error); }
    try { await native.close(); } catch (error) { errors.push(error); }
    const proof = native.snapshot();
    await info.attach('merge-native-process-proof', {body: JSON.stringify(proof), contentType: 'application/json'});
    // Preserve the receipt even when cleanup fails. Never infer current signal
    // authority from a numeric historical PID/group or retry a refused signal.
    try {
      expect(proof.closed && proof.joined && proof.shutdownRequested && proof.termSent && proof.cwdRemoved && proof.listenerRefused).toBe(true);
      expect(proof.forced || proof.unexpectedExit || proof.identityRefused || proof.cleanupDeferred).toBe(false);
      expect(proof.code).toBe(0); expect(proof.signal).toBeNull();
      if (process.platform !== 'win32') expect(proof.groupEmpty).toBe(true);
      if (process.platform === 'linux') {
        expect(proof.nativeIdentity?.pid).toBe(proof.pid);
        expect(proof.identityChecks.some(value => value.phase === 'capture' && value.outcome === 'verified')).toBe(true);
        expect(proof.identityChecks.some(value => value.phase === 'readiness' && value.outcome === 'verified')).toBe(true);
        expect(proof.identityChecks.some(value => value.phase === 'before-signal' && value.signal === 'SIGTERM' && value.outcome === 'signal-sent')).toBe(true);
      }
    } catch (error) { errors.push(error); }
    if (errors.length) throw new AggregateError(errors, 'Merge fixture cleanup failed');
  }
}});
export {expect};
