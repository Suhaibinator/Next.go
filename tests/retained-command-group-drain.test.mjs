// Data-only tests: no real process probe, signal, clock sleep or timer is used.
import test from 'node:test';
import assert from 'node:assert/strict';
import {drainClosedProcessGroup, attachGroupDrain, groupDrainLimits, validGroupDrainObservation} from './retained-command-group-drain.mjs';

const joined = (withSignal = false) => ({joined: true, outstanding: 0, timersStarted: 1, timersCleared: 1, abortListenersAdded: withSignal ? 1 : 0, abortListenersRemoved: withSignal ? 1 : 0});
function fake({outcomes = ['PRESENT', 'ESRCH'], start = 100, gain, probe, wait, now} = {}) {
  const state = {time: start, calls: [], waits: [], returned: 0};
  const io = {
    now: () => now ? now(state) : state.time,
    probe: group => { state.calls.push(group); return probe ? probe(state) : outcomes[Math.min(state.calls.length - 1, outcomes.length - 1)]; },
    wait: async (milliseconds, signal) => {
      state.waits.push(milliseconds);
      if (wait) return wait(state, milliseconds, signal);
      state.time += gain ?? milliseconds; state.returned++; return joined(!!signal);
    },
  };
  return {state, io};
}
const options = extras => ({closed: true, deadlineMS: 1000, ...extras});
function noCredit(value) {
  assert.equal(value.empty, false); assert.equal(value.status, 'INCOMPLETE'); assert.equal(value.outstanding, value.joined ? 0 : null);
  assert.equal(value.signalAuthority, false); assert.equal(value.censusConsulted, false); assert.equal(value.processTreeAbsence, false);
}

test('Post-close PRESENT becomes EMPTY only after an actual injected ESRCH observation and a joined bounded wait', async () => {
  const {state, io} = fake(); const value = await drainClosedProcessGroup(321, options(), io);
  assert.equal(value.empty, true); assert.equal(value.status, 'EMPTY'); assert.equal(value.reason, 'EMPTY'); assert.equal(value.lastOutcome, 'ESRCH');
  assert.equal(value.group, 321); assert.equal(value.closed, true); assert.equal(value.attempts, 2); assert.equal(value.waits, 1); assert.equal(value.joined, true); assert.equal(value.outstanding, 0);
  assert.equal(value.startedAtMS, 100); assert.equal(value.deadlineMS, 600); assert.equal(value.finishedAtMS, 120); assert.equal(value.elapsedMS, 20); assert.deepEqual(value.samples, [{atMS: 0, outcome: 'PRESENT'}, {atMS: 20, outcome: 'ESRCH'}]);
  assert.deepEqual(state.calls, [321, 321]); assert.deepEqual(state.waits, [20]); assert.equal(state.returned, 1);
  assert.equal(value.timersStarted, value.timersCleared); assert.equal(value.abortListenersAdded, value.abortListenersRemoved);
  assert.equal(value.signalAuthority, false); assert.equal(value.censusConsulted, false); assert.equal(value.processTreeAbsence, false);
  assert.equal(Object.isFrozen(value), true); assert.equal(Object.isFrozen(value.samples), true); assert.ok(Buffer.byteLength(JSON.stringify(value)) <= 4096);
});

test('Original command deadline and the independent500ms observer cap never renew while a group stays present', async () => {
  const short = fake({outcomes: ['PRESENT']});
  const shortValue = await drainClosedProcessGroup(321, options({deadlineMS: 135}), short.io);
  noCredit(shortValue); assert.equal(shortValue.deadlineMS, 135); assert.equal(shortValue.finishedAtMS, 135); assert.equal(shortValue.reason, 'DEADLINE_EXPIRED'); assert.equal(shortValue.elapsedMS, 35); assert.deepEqual(short.state.waits, [20, 15]); assert.equal(shortValue.attempts, 2); assert.equal(shortValue.waits, 2);
  const capped = fake({outcomes: ['PRESENT']});
  const cappedValue = await drainClosedProcessGroup(321, options({deadlineMS: 10_000}), capped.io);
  noCredit(cappedValue); assert.equal(cappedValue.reason, 'DEADLINE_EXPIRED'); assert.equal(cappedValue.elapsedMS, 500); assert.equal(cappedValue.attempts, 25); assert.equal(cappedValue.waits, 25); assert.equal(capped.state.returned, 25); assert.ok(capped.state.waits.every(value => value <= 20));
  const expired = fake({outcomes: ['ESRCH']});
  const expiredValue = await drainClosedProcessGroup(321, options({deadlineMS: 100}), expired.io);
  noCredit(expiredValue); assert.equal(expiredValue.reason, 'DEADLINE_EXPIRED'); assert.equal(expiredValue.attempts, 0); assert.deepEqual(expired.state.calls, []);
});

test('Finite32-call admission, strict clock failures, late results and unknown synchronous probe outcomes fail closed', async () => {
  const capped = fake({outcomes: ['PRESENT'], gain: 1});
  const value = await drainClosedProcessGroup(321, options(), capped.io);
  noCredit(value); assert.equal(value.reason, 'ATTEMPT_LIMIT'); assert.equal(value.attempts, 32); assert.equal(value.waits, 31); assert.equal(capped.state.returned, 31); assert.equal(value.samples.length, 32); assert.ok(Buffer.byteLength(JSON.stringify(value)) <= groupDrainLimits.maxBytes);
  const regression = fake({probe: state => { state.time--; return 'ESRCH'; }});
  const regressed = await drainClosedProcessGroup(321, options(), regression.io); noCredit(regressed); assert.equal(regressed.reason, 'CLOCK_REGRESSION');
  const unavailable = fake({now: () => NaN});
  const invalid = await drainClosedProcessGroup(321, options(), unavailable.io); noCredit(invalid); assert.equal(invalid.reason, 'INVALID_CLOCK'); assert.equal(invalid.attempts, 0);
  const late = fake({probe: state => { state.time += 501; return 'ESRCH'; }});
  const lateValue = await drainClosedProcessGroup(321, options(), late.io); noCredit(lateValue); assert.equal(lateValue.reason, 'DEADLINE_EXPIRED'); assert.equal(lateValue.lastOutcome, 'ESRCH');
  const stoppedClock = fake({gain: 0, outcomes: ['PRESENT']});
  const stopped = await drainClosedProcessGroup(321, options(), stoppedClock.io); noCredit(stopped); assert.equal(stopped.reason, 'INVALID_CLOCK'); assert.equal(stopped.joined, true);
  for (const outcome of ['PERMISSION_DENIED', 'UNAVAILABLE', true, {then() { throw Error('Asynchronous probe must never be awaited'); }}]) {
    const sample = fake({probe: () => outcome}); const result = await drainClosedProcessGroup(321, options(), sample.io);
    noCredit(result); assert.equal(result.reason, outcome === 'PERMISSION_DENIED' ? 'PERMISSION_DENIED' : 'PROBE_UNAVAILABLE'); assert.equal(result.attempts, 1); assert.equal(result.waits, 0);
  }
});

test('Only valid closed spawned-PID records admit a probe; invalid inputs never become absence evidence', async () => {
  for (const group of [0, 1, -1, 1.5, NaN, Infinity, '321', 0x80000000]) {
    const sample = fake(); const value = await drainClosedProcessGroup(group, options(), sample.io);
    noCredit(value); assert.equal(value.reason, 'INVALID_REQUEST'); assert.deepEqual(sample.state.calls, []);
  }
  const open = fake(); const value = await drainClosedProcessGroup(321, options({closed: false}), open.io);
  noCredit(value); assert.equal(value.reason, 'COMMAND_NOT_CLOSED'); assert.deepEqual(open.state.calls, []);
  for (const deadlineMS of [NaN, Infinity, -1, undefined]) {
    const sample = fake(); const result = await drainClosedProcessGroup(321, options({deadlineMS}), sample.io);
    noCredit(result); assert.equal(result.reason, 'INVALID_REQUEST'); assert.deepEqual(sample.state.calls, []);
  }
});

test('Aborting during a fake owned pending wait settles all timer/listener receipts before returning without another probe', async () => {
  const controller = new AbortController();
  const sample = fake({outcomes: ['PRESENT'], wait: (state, milliseconds, signal) => {
    assert.equal(signal, controller.signal); state.time += 7; controller.abort(); state.returned++; return joined(true);
  }});
  const value = await drainClosedProcessGroup(321, options({signal: controller.signal}), sample.io);
  noCredit(value); assert.equal(value.reason, 'ABORTED'); assert.equal(value.attempts, 1); assert.equal(value.waits, 1); assert.equal(value.joined, true); assert.equal(value.elapsedMS, 7); assert.equal(sample.state.returned, 1);
  assert.equal(value.timersStarted, 1); assert.equal(value.timersCleared, 1); assert.equal(value.abortListenersAdded, 1); assert.equal(value.abortListenersRemoved, 1);
  const preAborted = fake({outcomes: ['ESRCH']});
  const before = await drainClosedProcessGroup(321, options({signal: controller.signal}), preAborted.io);
  noCredit(before); assert.equal(before.reason, 'ABORTED'); assert.equal(before.attempts, 0); assert.equal(before.waits, 0);
  const duringProbe = new AbortController(), raced = fake({probe: () => { duringProbe.abort(); return 'ESRCH'; }});
  const observed = await drainClosedProcessGroup(321, options({signal: duringProbe.signal}), raced.io);
  noCredit(observed); assert.equal(observed.reason, 'ABORTED'); assert.equal(observed.lastOutcome, 'ESRCH');
});

test('Failed or unjoined wait receipts cannot earn physical emptiness and no rejected adapter is retried', async () => {
  const failed = fake({wait: () => { throw Error('Owned fake wait rejected after settlement'); }});
  const failure = await drainClosedProcessGroup(321, options(), failed.io);
  noCredit(failure); assert.equal(failure.reason, 'WAIT_FAILED'); assert.equal(failure.joined, false); assert.equal(failure.outstanding, null); assert.equal(validGroupDrainObservation(failure), true); assert.equal(failure.waits, 1); assert.equal(failed.state.calls.length, 1);
  for (const wait of [() => undefined, () => ({...joined(), joined: false}), () => ({...joined(), outstanding: 1}), () => ({...joined(), timersCleared: 0}), () => ({...joined(true), abortListenersRemoved: 0})]) {
    const sample = fake({wait}); const value = await drainClosedProcessGroup(321, options(), sample.io);
    noCredit(value); assert.equal(value.reason, 'WAIT_UNJOINED'); assert.equal(value.joined, false); assert.equal(value.outstanding, null); assert.equal(validGroupDrainObservation(value), true); assert.equal(sample.state.calls.length, 1);
  }
});

test('Bounded attachment reserves4KiB, preserves old command proof and rejects duplicate, contradictory or oversized receipts before mutation', async () => {
  const sample = fake({outcomes: ['ESRCH']}), value = await drainClosedProcessGroup(321, options(), sample.io);
  assert.equal(validGroupDrainObservation(value), true);
  const proof = {pid: 321, closed: true, groupEmpty: false, original: {identity: 'preserve'}};
  assert.equal(attachGroupDrain(proof, value), true); assert.deepEqual(proof.original, {identity: 'preserve'}); assert.equal(proof.groupEmpty, false); assert.equal(proof.groupDrain.empty, true); assert.notEqual(proof.groupDrain, value);
  const saved = JSON.stringify(proof); assert.throws(() => attachGroupDrain(proof, value)); assert.equal(JSON.stringify(proof), saved);
  for (const change of [record => { record.closed = false; }, record => { record.empty = false; }, record => { record.lastOutcome = 'PRESENT'; }, record => { record.samples[0].outcome = 'PRESENT'; }, record => { record.signalAuthority = true; }, record => { record.finishedAtMS = record.deadlineMS; }, record => { record.deadlineMS = record.startedAtMS + 501; }, record => { record.extra = 'not bounded schema'; }, record => { record.attempts = 33; }, record => { record.waits = 32; }, record => { record.timersStarted = 1; }, record => { record.joined = false; }, record => { record.outstanding = null; }]) {
    const altered = JSON.parse(JSON.stringify(value)); change(altered); const target = {pid: 321, closed: true, groupEmpty: false, keep: 'old'}; const before = JSON.stringify(target);
    assert.equal(validGroupDrainObservation(altered), false); assert.throws(() => attachGroupDrain(target, altered)); assert.equal(JSON.stringify(target), before); assert.equal(Object.hasOwn(target, 'groupDrain'), false);
  }
  const foreignGroup = {...JSON.parse(JSON.stringify(value)), group: 322};
  assert.equal(validGroupDrainObservation(foreignGroup), true);
  const mismatched = {pid: 321, closed: true, groupEmpty: false, keep: 'old binding'}; const mismatchBefore = JSON.stringify(mismatched);
  assert.throws(() => attachGroupDrain(mismatched, foreignGroup)); assert.equal(JSON.stringify(mismatched), mismatchBefore);
  const oversized = {pid: 321, closed: true, groupEmpty: false, keep: 'x'.repeat(60 << 10)}; const prior = JSON.stringify(oversized);
  assert.throws(() => attachGroupDrain(oversized, value)); assert.equal(JSON.stringify(oversized), prior); assert.equal(Object.hasOwn(oversized, 'groupDrain'), false);
  const presentSample = fake({outcomes: ['PRESENT']}); const present = await drainClosedProcessGroup(321, options({deadlineMS: 120}), presentSample.io);
  const denied = {pid: 321, closed: true, groupEmpty: false}; assert.equal(attachGroupDrain(denied, present), true); assert.equal(denied.groupDrain.empty, false); assert.equal(denied.groupDrain.reason, 'DEADLINE_EXPIRED'); assert.equal(denied.groupEmpty, false);
});
