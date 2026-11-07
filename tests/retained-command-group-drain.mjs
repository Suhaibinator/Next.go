// Physical observation after ChildProcess close only. This helper never sends
// TERM/KILL, consults /proc census, identifies a process or proves tree absence.
import {performance} from 'node:perf_hooks';

export const groupDrainLimits = Object.freeze({maxBudgetMS: 500, maxCalls: 32, intervalMS: 20, maxBytes: 4096, commandProofBytes: 64 << 10, proofReserveBytes: 4096});
const outcomes = Object.freeze(['UNOBSERVED', 'PRESENT', 'ESRCH', 'PERMISSION_DENIED', 'UNAVAILABLE']);
const reasons = Object.freeze(['EMPTY', 'COMMAND_NOT_CLOSED', 'INVALID_REQUEST', 'INVALID_CLOCK', 'CLOCK_REGRESSION', 'ABORTED', 'DEADLINE_EXPIRED', 'ATTEMPT_LIMIT', 'PERMISSION_DENIED', 'PROBE_UNAVAILABLE', 'WAIT_FAILED', 'WAIT_UNJOINED']);
// PGID1 is excluded: kill(-1,0) has all-permitted-process semantics.
const validPID = value => Number.isSafeInteger(value) && value > 1 && value <= 0x7fffffff;
const validClock = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= Number.MAX_SAFE_INTEGER / 1000;
const bytes = value => Buffer.byteLength(JSON.stringify(value), 'utf8');
function kernelProbe(group) {
  try { process.kill(-group, 0); return 'PRESENT'; }
  catch (error) {
    if (error?.code === 'ESRCH') return 'ESRCH';
    if (error?.code === 'EPERM' || error?.code === 'EACCES') return 'PERMISSION_DENIED';
    return 'UNAVAILABLE';
  }
}
// One owned timer. Every completion path clears its handle, removes its exact
// abort listener and settles before a receipt is returned; no Promise.race or
// in-flight asynchronous kernel probe can outlive drainClosedProcessGroup.
function ownedWait(milliseconds, signal) {
  return new Promise(resolve => {
    let timer, settled = false, timersStarted = 0, timersCleared = 0, abortListenersAdded = 0, abortListenersRemoved = 0;
    const finish = () => {
      if (settled) return; settled = true;
      if (timer !== undefined) { clearTimeout(timer); timer = undefined; timersCleared++; }
      if (abortListenersAdded) { signal.removeEventListener('abort', aborted); abortListenersRemoved++; }
      resolve({joined: true, outstanding: 0, timersStarted, timersCleared, abortListenersAdded, abortListenersRemoved});
    };
    const aborted = () => finish();
    if (signal?.aborted) { finish(); return; }
    if (signal) { signal.addEventListener('abort', aborted, {once: true}); abortListenersAdded++; }
    if (signal?.aborted) { finish(); return; }
    try { timer = setTimeout(finish, milliseconds); timersStarted++; } catch { finish(); }
  });
}
const productionIO = Object.freeze({now: () => performance.now(), probe: kernelProbe, wait: ownedWait});
function receipt(value) {
  return !!value && value.joined === true && value.outstanding === 0 && ['timersStarted', 'timersCleared', 'abortListenersAdded', 'abortListenersRemoved'].every(name => Number.isInteger(value[name]) && value[name] >= 0 && value[name] <= 1) && value.timersStarted === value.timersCleared && value.abortListenersAdded === value.abortListenersRemoved;
}
function fixedResult(group, closed) {
  return {version: 1, group: validPID(group) ? group : 0, closed: closed === true, empty: false, status: 'INCOMPLETE', reason: 'INVALID_REQUEST', attempts: 0, waits: 0, joined: true, outstanding: 0, lastOutcome: 'UNOBSERVED', startedAtMS: 0, deadlineMS: 0, finishedAtMS: 0, elapsedMS: 0, timersStarted: 0, timersCleared: 0, abortListenersAdded: 0, abortListenersRemoved: 0, samples: [], signalAuthority: false, censusConsulted: false, processTreeAbsence: false};
}
// deadlineMS is min(the command's ORIGINAL performance.now() watchdog
// deadline, its captured ChildProcess close time +500ms). The owner captures
// these clocks at the actual boundaries. This helper further caps itself at
// start+500ms; it never renews an already-expired caller budget. io is a trusted
// data-only test seam, never application input.
export async function drainClosedProcessGroup(group, options = {}, io = productionIO) {
  const result = fixedResult(group, options?.closed), finish = reason => {
    result.reason = reason; result.empty = reason === 'EMPTY'; result.status = result.empty ? 'EMPTY' : 'INCOMPLETE';
    for (const sample of result.samples) Object.freeze(sample); Object.freeze(result.samples);
    // Fixed bounded fields, at most32 samples. This is a programmer invariant,
    // not a fallback that replaces existing command proof or invents absence.
    if (bytes(result) > groupDrainLimits.maxBytes) throw Error('Group drain fixed result exceeded4096bytes');
    return Object.freeze(result);
  };
  if (!validPID(group) || !options || typeof options !== 'object' || !validClock(options.deadlineMS) || !io || typeof io.now !== 'function' || typeof io.probe !== 'function' || typeof io.wait !== 'function') return finish('INVALID_REQUEST');
  if (options.closed !== true) return finish('COMMAND_NOT_CLOSED');
  const signal = options.signal;
  if (signal !== undefined && (!signal || typeof signal.aborted !== 'boolean' || typeof signal.addEventListener !== 'function' || typeof signal.removeEventListener !== 'function')) return finish('INVALID_REQUEST');
  let start, last, deadline, clockFailure;
  const now = () => {
    let value; try { value = io.now(); } catch { clockFailure = 'INVALID_CLOCK'; return null; }
    if (!validClock(value)) { clockFailure = 'INVALID_CLOCK'; return null; }
    if (last !== undefined && value < last) { clockFailure = 'CLOCK_REGRESSION'; return null; }
    last = value; if (start !== undefined) { result.finishedAtMS = value; result.elapsedMS = value - start; } return value;
  };
  start = now(); if (start === null) return finish(clockFailure);
  result.startedAtMS = start; result.finishedAtMS = start;
  deadline = Math.min(start + groupDrainLimits.maxBudgetMS, options.deadlineMS); result.deadlineMS = deadline;
  for (;;) {
    if (signal?.aborted) return finish('ABORTED');
    const before = now(); if (before === null) return finish(clockFailure);
    if (before >= deadline) return finish('DEADLINE_EXPIRED');
    if (result.attempts >= groupDrainLimits.maxCalls) return finish('ATTEMPT_LIMIT');
    result.attempts++;
    let outcome; try { outcome = io.probe(group); } catch { outcome = 'UNAVAILABLE'; }
    if (!outcomes.slice(1).includes(outcome)) outcome = 'UNAVAILABLE';
    result.lastOutcome = outcome; result.samples.push({atMS: before - start, outcome});
    const after = now(); if (after === null) return finish(clockFailure);
    if (signal?.aborted) return finish('ABORTED');
    if (after >= deadline) return finish('DEADLINE_EXPIRED');
    if (outcome === 'ESRCH') return finish('EMPTY');
    if (outcome === 'PERMISSION_DENIED') return finish('PERMISSION_DENIED');
    if (outcome === 'UNAVAILABLE') return finish('PROBE_UNAVAILABLE');
    if (result.attempts >= groupDrainLimits.maxCalls) return finish('ATTEMPT_LIMIT');
    const delay = Math.min(groupDrainLimits.intervalMS, deadline - after); result.waits++;
    let waited;
    try { waited = await io.wait(delay, signal); }
    catch { result.joined = false; result.outstanding = null; return finish('WAIT_FAILED'); }
    if (!receipt(waited)) { result.joined = false; result.outstanding = null; return finish('WAIT_UNJOINED'); }
    for (const name of ['timersStarted', 'timersCleared', 'abortListenersAdded', 'abortListenersRemoved']) result[name] += waited[name];
    const settledAt = now();
    if (signal?.aborted) return finish('ABORTED');
    if (settledAt === null) return finish(clockFailure);
    if (settledAt <= after) return finish('INVALID_CLOCK');
  }
}
function validResult(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
  const expected = Object.keys(fixedResult(1, true)).sort();
  if (Object.keys(value).sort().join(',') !== expected.join(',') || value.version !== 1 || !validPID(value.group) || typeof value.closed !== 'boolean' || typeof value.empty !== 'boolean' || !['EMPTY', 'INCOMPLETE'].includes(value.status) || !reasons.includes(value.reason) || !outcomes.includes(value.lastOutcome) || !Number.isInteger(value.attempts) || value.attempts < 0 || value.attempts > groupDrainLimits.maxCalls || !Number.isInteger(value.waits) || value.waits < 0 || value.waits > Math.min(value.attempts, groupDrainLimits.maxCalls - 1) || typeof value.joined !== 'boolean' || !((value.joined === true && value.outstanding === 0) || (value.joined === false && value.outstanding === null)) || !['startedAtMS', 'deadlineMS', 'finishedAtMS', 'elapsedMS'].every(name => validClock(value[name])) || value.finishedAtMS < value.startedAtMS || value.elapsedMS !== value.finishedAtMS - value.startedAtMS || value.deadlineMS > value.startedAtMS + groupDrainLimits.maxBudgetMS || value.signalAuthority !== false || value.censusConsulted !== false || value.processTreeAbsence !== false || !Array.isArray(value.samples) || value.samples.length !== value.attempts) return false;
  if (value.empty !== (value.status === 'EMPTY') || value.empty !== (value.reason === 'EMPTY')) return false;
  if (value.empty && (!value.closed || !value.joined || value.outstanding !== 0 || value.attempts === 0 || value.lastOutcome !== 'ESRCH' || value.finishedAtMS >= value.deadlineMS)) return false;
  for (const name of ['timersStarted', 'timersCleared', 'abortListenersAdded', 'abortListenersRemoved']) if (!Number.isInteger(value[name]) || value[name] < 0 || value[name] > value.waits) return false;
  if (value.timersStarted !== value.timersCleared || value.abortListenersAdded !== value.abortListenersRemoved) return false;
  let previous = -1;
  for (const sample of value.samples) {
    if (!sample || typeof sample !== 'object' || Object.keys(sample).sort().join(',') !== 'atMS,outcome' || !validClock(sample.atMS) || sample.atMS < previous || sample.atMS > value.elapsedMS || !outcomes.slice(1).includes(sample.outcome)) return false;
    previous = sample.atMS;
  }
  if (value.samples.length && value.samples.at(-1).outcome !== value.lastOutcome) return false;
  return bytes(value) <= groupDrainLimits.maxBytes;
}
// A drain receipt never replaces the old proof. Preflight the whole pretty JSON
// under its existing64KiB writer, preserving a further4096byte reserve. Failure
// throws BEFORE mutation, so callers must keep groupEmpty false/fail closed.
export function attachGroupDrain(proof, observation) {
  if (!proof || typeof proof !== 'object' || Array.isArray(proof) || Object.hasOwn(proof, 'groupDrain')) throw Error('Group drain proof target invalid or already attached');
  let copy;
  try { if (!validResult(observation) || proof.pid !== observation.group || proof.closed !== observation.closed) throw Error('invalid'); copy = JSON.parse(JSON.stringify(observation)); }
  catch { throw Error('Group drain observation invalid'); }
  const candidate = {...proof, groupDrain: copy}; let length;
  try { length = Buffer.byteLength(JSON.stringify(candidate, null, 2) + '\n', 'utf8'); }
  catch { throw Error('Group drain command proof cannot serialize'); }
  if (length > groupDrainLimits.commandProofBytes - groupDrainLimits.proofReserveBytes) throw Error('Group drain command proof reserve exceeded');
  proof.groupDrain = copy; return true;
}

// The completion auditor reuses this exact pure bounded schema validation.
export {validResult as validGroupDrainObservation};
