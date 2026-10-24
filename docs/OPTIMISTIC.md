# Optimistic Go state

`client.Optimistic[T, A]` keeps tentative operations separate from a confirmed
state prefix. A pure reducer applies an action `A` to a value `T`. Begin an
intent before asynchronous work, then confirm its canonical operation or reject
it when the request fails. The model derives a new view without replacing DOM
nodes or performing virtual DOM reconciliation.

The demo uses [typed mutation transport](MUTATIONS.md) for its shared Go DTOs
and versioned endpoint. The [demo page](../examples/demo/app/optimistic/page.go) renders a useful server
count before WASM loads. Its [Go island](../examples/demo/components/optimistic/client_js.go)
shows pending state, canonical corrections, rejection, cancellation and explicit
refresh. A progressive HTML form also submits and receives fresh SSR without
WASM; its explicit action URL makes the page reject plain-static publication.
The public demonstration uses independent process-local data; production
mutations still require authentication, authorization, durable storage and an
application transaction policy.

## Model and island ownership

```go
model, err := client.NewScopedOptimistic(scope, initial,
    func(value, delta int64) int64 { return value + delta },
    client.OptimisticOptions[int64, int64]{Limit: 16},
)
if err != nil {
    return err
}

stop, err := model.Subscribe(func(snapshot client.OptimisticSnapshot[int64]) {
    count.Text(strconv.FormatInt(snapshot.Value, 10))
    add.Property("disabled", snapshot.Available == 0)
    // Update an accessible status element from snapshot.Pending as well.
})
if err != nil {
    return err
}
scope.Cleanup(stop)
```

`NewScopedOptimistic` is a `js && wasm` API and closes its model when the island
unmounts or hydration fails. `NewOptimistic` creates the same portable model
without an island owner; call `Close` yourself. Both constructors accept a
single optional `OptimisticOptions` value and return an error for invalid input.

`Snapshot` returns the current value, unresolved `Pending` count, remaining
`Available` ledger slots and `Closed` flag. The default ledger limit is 64 and
the supported range is 1–1024. Buffered confirmations occupy slots until earlier
intents settle. Rejections release their slots immediately. There can be at most
256 observers per model. Begin fails with `ErrOptimisticLimit` when capacity is
exhausted, so a caller can apply backpressure before sending a request.

## Intent lifecycle

```go
intent, err := model.Begin(int64(5)) // update derived state immediately
if err != nil {
    showError(err)
    return
}

// Run transport work in a goroutine, outside the browser event callback.
go func() {
    acceptedDelta, err := submit(ctx)
    if ctx.Err() != nil {
        // The island owner will close its model on disposal. For a user
        // cancellation while still mounted, reject the local intent too.
        _ = intent.Reject()
        return
    }
    if err != nil {
        if intent.Reject() == nil {
            showError(err)
        }
        return
    }
    if err := intent.Resolve(acceptedDelta); err != nil {
        // A reset/cancellation may have invalidated this late result.
        if !errors.Is(err, client.ErrOptimisticSettled) &&
            !errors.Is(err, client.ErrOptimisticClosed) {
            showError(err)
        }
    }
}()
```

`Commit()` confirms the original action. `Resolve(accepted)` replaces it with a
canonical accepted action. `Reject()` removes it. A settled or reset intent
cannot apply twice; further settlement returns `ErrOptimisticSettled`. Intent
IDs are opaque and unique within one model lifetime. They are not globally
unique server idempotency keys.

An example starting at 10:

| Operation | View | Unresolved intents |
| --- | --- | --- |
| Begin A: add 1 | 11 | 1 |
| Begin B: add 5 | 16 | 2 |
| Resolve B with canonical add 2 | 13 | 1 |
| Reject A | 12 | 0 |

The ledger retains B's accepted operation while A is unresolved. When A settles,
the confirmed prefix folds in start order. The same rule handles noncommutative
local reducers: an older response cannot overwrite a later accepted local
operation simply by arriving last.

The reducer must apply operations that remain meaningful after preceding
rejections. Use canonical deltas, version-aware patches, or records carrying
stable local/server identifiers. A full server snapshot may already contain
other outstanding operations; applying it as a delta can double-count them.
For noncommuting server mutations, serialize them in intent order or supply a
version-aware application reconciliation policy. The model does not infer
server transaction order from network delivery order.

## Cancellation, refresh and reset

Use a cancellable context for transport and register its cancellation with
`scope.Cleanup`. Reject an intent to remove its local projection immediately;
also cancel its request when appropriate. Rejecting or closing a model does not
cancel transport automatically.

An aborted request can follow a successful server commit. Local rollback cannot
prove that the mutation was undone. The demo preserves this distinction and
offers **Refresh server value** to reconcile with fresh SSR. Its browser test
deliberately commits on the server, holds the response, cancels the local intent,
then verifies the refreshed count.

`Reset(freshValue)` clears the ledger and installs a fresh authoritative value.
It invalidates every previous handle; a late response cannot modify the new
baseline. Cancel/reconcile associated transports separately. Resetting while
the server is still processing mutations may require another read or a
version/idempotency protocol. Automatic retries are not provided: applications
must handle ambiguous commit outcomes safely.

`Close` is idempotent. It drops values, entries and subscriptions without calling
observers, so island disposal does not write to detached DOM. Future mutations
return `ErrOptimisticClosed`; its closed snapshot has zero data. A callback
already dispatched can finish. The demo also checks the island's cancellation
context before updating status after a response.

## Ownership and callbacks

Treat values and actions as immutable. Supply `CloneValue` and `CloneAction`
when they contain maps, slices or other mutable references, copying every part
that reducers or callers can mutate. Snapshot and reducer inputs use those clone
functions. For example, a `map[string]int` can use `maps.Clone`; nested mutable
values need a deeper application clone. Primitive-value models need no clones.

Reducers and clones run under the model lock. They must be pure, work for all
reachable states, return promptly and never call back into the model. A
recoverable panic during mutation calculation returns an error and leaves that
mutation unapplied. `Snapshot` calls `CloneValue` directly, so a faulty clone can
also panic at that call site.

Model methods are safe for concurrent callers. Observers execute serially
outside the lock; reentrant or concurrent writes may coalesce to the latest
snapshot. Subscribe synchronizes before returning unless notification is already
in progress, when it synchronizes after the active callback. The stop function
is idempotent. Observer panics are isolated and reported through
`OnObserverError`: portable models log by default, and scoped browser models
use the framework status/console. An error-handler panic is logged without
stranding later notifications. Observer callbacks must return promptly.

## Verification and remaining work

Native and matching-toolchain WASM tests cover canonical resolution, rollback,
all 384 settlement-order/rejection combinations for four intents, concurrent
settlement under the race detector, cloning, bounds, reset, reentrant cleanup,
callback failures and scope disposal. Browser tests cover SSR identity, pending
status/backpressure, corrections, response reordering, rejection, network failure,
cancellation before delivery and after a server commit, unmount cleanup, and
mounted/slash/prefix-locale/domain-locale deployments. Executable tests check
initial SSR, canonical action results, validation without mutation and fresh SSR
through HTTP and both mock Lambda transports, including progressive form
redirects. Plain-static export rejects the server-dependent demo while
preserving a previous publication; the generic state primitive itself can be
used with client-only work on a static host.

This is a Go state primitive with explicit DOM updates and transport integration.
Automatic server-snapshot reconciliation, idempotency/transaction adapters,
controlled-form/optimistic-list helpers, independent streamed UI settlement,
animation scheduling and full production resource/accessibility qualification
remain open in [the parity inventory](PARITY.md). Next.js documents optimistic
form updates through [its forms guide](https://nextjs.org/docs/app/guides/forms)
and React's [useOptimistic API](https://react.dev/reference/react/useOptimistic).
