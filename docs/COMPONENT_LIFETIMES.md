# Component lifetimes, effects and typed context

Go components can compose within one island without adding DOM wrappers or
replacing SSR nodes. A browser `Scope` owns DOM listeners; its `Lifetime` owns
explicit effects, provider values and cancellable resources. These are
experimental Go APIs on one event loop, with no implicit dependency tracking or
virtual DOM. Pure lifetime behavior also runs in native tests.

## Compose browser behavior

Render child roots with ordinary `ui.El` on the server. In the containing
island's browser `Mount`, acquire the root before creating its child scope:

```go
root, err := scope.Require("[data-editor]")
if err != nil { return err }
editor, stop, err := scope.Subscope(root)
if err != nil { return err }
// MountEditor binds existing SSR nodes through editor.Require/editor.On.
if err := MountEditor(editor); err != nil { stop(); return err }
```

The parent automatically disposes the child. Calling `stop()` releases behavior
immediately and unregisters ownership, leaving DOM in place; it is idempotent.
Remove the element explicitly if it should disappear. The parent `Require`
excludes active child roots and their descendants. A child can create further
subscopes. Roots must be descendants of the parent, within its island, and
must not overlap existing child ownership. `Require` excludes the root itself;
use `child.Root` when binding its root element.
Sibling DOM subscopes have no relative disposal-order guarantee.

Subscopes are Go composition within one island. Separately generated nested
islands retain their own mount/lifecycle boundary. Their context is not inferred
from DOM ancestry. Direct `Element.Value` access remains a trusted application
escape hatch; it can bypass query ownership.

## Typed providers

Declare a key once in browser/shared Go code. Copying a key retains its identity;
two new keys of the same type remain distinct.

```go
var theme = client.NewContext[*client.State[string]]()

life, err := scope.Lifetime()
if err != nil { return err }
err = client.Provide(life, theme, client.NewState("light"))
if err != nil { return err }

childLife, err := editor.Lifetime()
if err != nil { return err }
model, ok := client.UseContext(childLife, theme)
if !ok { return fmt.Errorf("missing theme provider") }
```

Lookup chooses the nearest provider along the Go lifetime parent chain. A child
provider shadows its own subtree; siblings retain their parent value. A nil
interface value is a real shadowing provider. Zero-value keys are invalid for
`Provide`. Closed owners cannot provide or read values. Lifetime closure drops
provider references.

Provider registration/replacement itself does not trigger effects. Provide a
`*State[T]` for reactive shared values and list that state as an effect
dependency. Values are Go references, without cloning, freezing or serialization.
Context is local to a Go runtime and explicit ownership tree: it does not cross
independent islands, separate lazy WASM runtimes, server requests or browser
documents. Use props or application data APIs for those boundaries. Props sent
to the browser remain public data. Context values in browser memory also provide
no secrecy boundary.
Providing a value does not transfer ownership of resources it references;
register their release explicitly with `Defer` or the owning scope's cleanup.

## Effects and cleanup

```go
effect, err := childLife.Effect(func() (func(), error) {
    label.Text(model.Get())
    ctx, cancel := context.WithCancel(context.Background())
    // Start asynchronous work in a goroutine; ignore late results after ctx ends.
    _ = ctx
    return cancel, nil
}, model)
if err != nil { return err }
_ = effect // optional manual Invalidate(), Close(), Err()
```

An effect runs once immediately after registration. Every dependency `State.Set`
invalidates it, including assignments of the same value. It reads current state
through `Get`; reads do not subscribe automatically. Duplicate dependencies are
deduplicated. Ordinary state subscribers run before effect notification. Effects
on the same state have no relative notification-order guarantee; express
dependent work as one effect instead of depending on incidental map order.
Legacy `State.Subscribe` callbacks retain direct callback semantics: their
panics propagate to the caller and may prevent later notifications. Effect
setup/cleanup errors use the isolation contract below.

Before rerunning, the effect releases its previous cleanup. A synchronous setup
returns its next cleanup and optional error. Registering all dependencies before
the first run prevents setup-time state changes from being missed. Reentrant
invalidations coalesce into another run, with a maximum of 64 runs per drain.
Effects are synchronous and unbatched across separate `Set` calls. They must
return promptly: browser Promises/network waits require a goroutine. Cancellation
does not by itself undo application writes; asynchronous results must check
cancellation/ownership before committing their DOM or state changes.

An initial setup error is returned by `Effect`. Later setup/cleanup failures
close that effect, unsubscribe all dependencies and report to the lifetime's
error handler. Panics become generic callback errors. `Err()` retains the
failure; `Close()` is idempotent and `Invalidate()` after closure returns
`ErrEffectClosed`. A cleanup returned alongside a setup error is still released.
If setup closes its owner, its returned cleanup runs immediately. Other effects
and components continue. Arbitrary writes before failure cannot be rolled back.
To recover, explicitly create a replacement effect or remount the child.

Use `life.Defer(cleanup)` to own an independent resource. Its returned `Resource`
handle closes and unregisters the resource immediately. Unlike appending an
unregister callback forever, repeated create/close cycles release owned entries.
`Lifetime.Close` closes children, then effects, then resources, each in reverse
creation order, and continues through cleanup panics. `Scope.Cleanup` retains its
existing reverse-registration order; its integration closes the lifetime during
scope disposal. A scope's lifetime is allocated once, only when used.
Closing a child unregisters it before calling cleanup, allowing that cleanup to
close its parent safely. A regression test covers this reentrant close path.
An owner is effectively closed as soon as an ancestor starts closing. Events,
location/selection observers and script-ready callbacks stop accepting work;
effect invalidation closes its registration instead of starting another setup.
New child/effect/provider/resource registrations return a closed-owner error.
This applies before each descendant's cleanup has been reached, so cleanup in
one child cannot restart a sibling's effect.

## Retention, bounds and qualification

A preserved layout keeps the same scope, provider values and effects through
compatible client navigation. Replaced page scopes close; history recreates
their page state. Keyed row scopes inherit their containing island's lifetime
when `row.Lifetime()` is requested. Removed rows release that lifetime. No
hidden-route effect suspension, scheduler, global application context, cross-WASM
context bridge or automatic dependency inference is implemented.

Framework-owned bounds per lifetime: 2,048 direct child lifetimes, 32 parent
levels, 128 effects, 16 dependency arguments per effect, 256 independent resources
and 128 provider keys. Each state supports 256 effect subscriptions. Each DOM
scope supports 128 explicit subscopes. These bound owned registrations; they do
not bound arbitrary application allocations, callback runtime or goroutine work.

`/composition` and `/composition/details` demonstrate inherited and shadowed
state, DOM-preserving hydration, explicit child removal, effect errors and a
retained layout provider. Native/WASM tests cover cleanup order, cancellation,
reentrancy, nil/key isolation, failures and repeated unregistration. Browser
tests cover initial node/text identity, sibling/island isolation, disposal,
retained layout/history, setup errors and navigation. Plain static publication
and HTTP/both mock Lambda transport fixtures exercise the same sample. These
are local tests, not full production lifecycle, cloud, performance or security
qualification. See [the inventory](PARITY.md) for remaining release work.

The final affected runtime matrix passes 376 checks across Chromium, Linux
Firefox, WebKit and touch WebKit; the final static-host matrix passes 80.
Both SDK race/vet and matching core/module WASM checks pass. Source-independent
HTTP and buffered/streaming mock Lambda suites pass on the host and under
nonroot/read-only Linux. The preceding expanded broad matrix passed 1,172
checks with three platform skips, before the last cleanup fixes; its provenance
is retained in the inventory rather than treated as an immutable release result.
