# Document-wide typed mutation dispatch

This implemented FIFO has scoped native/browser verification. It coordinates
typed action transport across eager and lazy Go callers. Both SDK native/WASM and host
deployment checks pass, as do corrected FIFO/optimistic fixtures. The affected
browser record passes 342/342. A native JavaScript CDP reproduction still has
an unresolved underlying cause. Preceding action-view matrices remain
historical evidence.

## One active operation, 64 waiting positions

The document core owns one queue with **one active lease and 64 waiting
admissions**. Ordinary typed JSON/multipart calls and combined action-view calls
share it. A combined call owns its lease through confirmation and the whole
view transport, not just until mutation data arrives. Queuing alone does not
cancel/fence the active view or invalidate its cache state.

FIFO means admission order among waiting calls. Concurrent goroutines and input
encoding do not establish a database/business order from source-code invocation
time. Build/configuration/draft/source/history/slot snapshots are taken at
actual dispatch, rather than becoming stale while waiting. Existing generated
wrappers forward these semantics without another per-module queue.

`client.ErrMutationQueueFull` rejects admission before a POST. A canceled queued
context is removed without sending a POST, preserving a custom cancellation
cause. Cancellation/grant races pass the lease onward rather than dispatching
work already canceled. Applications can decide whether to submit a new request
later after a known admission rejection; there is no automatic write retry.

## Cancellation waits for physical transport settlement

An active typed cancellation aborts its Fetch/read operation, but the lease is
not released until the native Promise and reader-cancel work settle. The next
typed POST cannot overtake that still-owned browser transport. A cancellation
error after dispatch still leaves the backing mutation's outcome unknown;
browser completion does not prove that a server transaction stopped.

This retention may make an active typed call return later than its caller's
cancellation signal. Generic `client.Fetch` remains unqueued with its existing
prompt-cancellation behavior and action fence; arbitrary Fetch requests can
still run in parallel. The queue does not serialize tabs, other documents,
server workers or database transactions, nor does it bound application inputs,
allocations or RSS.

The current typed response validator runs while that operation owns the lease.
It must not synchronously wait for another typed mutation that needs the same
queue. Keep validators bounded and synchronous; submit subsequent application
work after the active call returns.

## Lifecycle and lazy callers

Page/BFCache freezing cancels waiting admissions and rejects new calls with
`client.ErrMutationQueueFrozen`. Reopening permits new admission, while an
already accepted operation retains its physical lease until settlement.
Freezing does not prove active writes rolled back. Component contexts still
control their own queued/active work.

Lazy modules use bridge **ABI 4** to delegate both ordinary and combined typed
operations to the same core queue and receive resource invalidation epochs.
The original FIFO checkpoint used ABI 3; rebuild all modules with the core when
upgrading. See [client resources](CLIENT_RESOURCES.md). Immediately before dispatch, the core
synchronously probes the originating module's context, closing the gap before
its abort watcher is installed. Validation callbacks remain retained until the
physical host operation settles; queued cancellation cannot free a callback
that the host might still invoke. Module/version compatibility remains guarded;
there is no shared Go heap across runtimes.

After a combined view commits, its existing document-owned lifetime and
supersession checks still govern remaining chunks, including source-island
self-unmount. See [action views](ACTION_VIEWS.md). Queue order does not turn a
confirmed view error into a reason to repeat a mutation.

## Optimistic state and remaining work

The optimistic ledger can still project multiple local intents. Typed network
transport is serialized; projection order and canonical reconciliation remain
application concerns. Existing tests that deliberately expected concurrent
typed dispatch must change for this intentional policy, rather than hiding the
queue with extra delays or treating it as a runtime regression.

Server transaction ordering/cancellation, durable idempotency, exactly-once
delivery, transactional cookie/data publication and complete production
concurrency/recovery/load/soak qualification remain open. No client queue can
recall an unknown late server commit. Both SDK and host/isolated-Linux deployment
checks pass; the corrected affected browser record passes 342/342. The separate
native JavaScript CDP reproduction's underlying cause remains open. See
[current phase provenance](NATIVE_COOKIES.md#current-evidence).
No full-release or production-maturity claim is made here. All 79 capability
rows/11 release gates remain in [the inventory](PARITY.md).
