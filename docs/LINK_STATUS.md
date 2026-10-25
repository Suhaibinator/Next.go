# Link loading feedback

`Scope.ObserveLinkStatus` reports pending navigation for one actual anchor owned
by the component. Register after the client runtime has initialized, using a
connected `<a>` within the open scope. The ordinary server-rendered anchor still
works before WASM.

```go
link, err := s.Require("[data-open-report]")
if err != nil { return err }
hint, err := s.Require("[data-report-hint]")
if err != nil { return err }
_, err = s.ObserveLinkStatus(link, func(status client.LinkStatus) {
    text := ""
    if status.Pending { text = "Loading…" }
    hint.Text(text)
})
return err
```

Render a fixed-size hint container to avoid moving the page when its text changes.
Keep ordinary link labels accessible; global navigation loading feedback remains
available. The example at `/link-status` demonstrates eager and lazy Go components
inside a retained layout, duplicate destinations and deferred page content.

The observer synchronizes immediately, then reports changes. Identity belongs to
the clicked DOM anchor, so a second link to the same destination stays idle. Only
the latest eligible intercepted click can be pending. A complete cached route can
skip pending feedback. An in-flight prefetch or public partial shell still needs
an actual page commit. Pending ends after validated DOM, metadata and history
commit; a streamed shell settles the link while deferred regions continue their
own loading/error behavior.

Modified/native/external/download/API/fragment clicks do not select a link.
Programmatic navigation, refresh, history traversal, failures and supersession
clear the previous selected link. The document retains at most one clicked anchor identity until navigation
settles, independently of observer lifetimes, so late subscriptions receive its
actual current state. It retains no component Scope for this source identity.
Scope disposal automatically unregisters callbacks; the
returned stop function allows earlier idempotent cleanup. Callbacks run on the
Go/WASM event loop and must return promptly. The core document permits 512 live
registrations shared by eager and lazy modules. Lazy hosts unregister before
releasing browser callbacks. A broken bridge retains an inactive callback within
a bounded per-runtime allowance until document teardown rather than calling a
released function; its error requires a reload.

Link-status observers currently require ordinary full-document development
recovery when their component changes, matching location-observer restrictions.
They do not claim managed state transfer during component refresh. No handwritten
JavaScript application logic or server loaders enter the browser import graph.

This is a bounded part of loading feedback. Independent route/slot loading,
segment reset/retry, late access/status interrupts and complete production parity
remain open. The implementation uses paired module ABI 5; rebuild the core and
every module together. Resource invalidation epochs introduced in ABI 4 remain
mandatory.

## Scoped verification

The final pinned fixtures pass 15 cases on each of four engines for each SDK:
**60 on Go 1.27.3 and 60 on Go 1.24.13**, with no final failures, skips or retries.
They cover real cached responses, duplicate anchors, native target/modifier
behavior, deferred shells and disposed observers. The Go 1.27 artifact comes
from candidate A; the browser fixture is E, whose Go/application/asset bytes
match A. The Go 1.24 artifacts and fixture both come from E.

Two initial runs remain diagnostic evidence: 6 passes/1 failure/53 not run from
reinserting a test anchor outside its owned outlet, and 19 passes/1 failure/40 not
run from assuming an entry aged 32 seconds was fresh under a 30-second policy.
Those fixture-only corrections change no runtime code. The read-only warning report (`66348794`) records one console warning before
the current page was created and another after context close. The prior page,
realm identity and completion were unobserved; this establishes neither a
harmless event nor a live-cache cause. Broader G03/G06 investigations remain open.

Both SDKs also pass the targeted native/WASM/vet commands, **128 selected
existing core/location/lazy/stream regressions each**, and **12 static-host checks
each**. Together with the 120 link checks, that is 400 browser executions on the
pre-readiness-change producers, with zero final failures, skips, flaky results
or retries. Go 1.27 uses source A/runtime A0; Go 1.24 uses the E producer with
selected legacy tests from A. The runs do not cover BFCache or every mounted
mode. Native/browser processes joined; the exact legacy proxy process was later
absent, but its exit code and request/socket joins were unobserved.

The Go 1.24 static controller first rejected a stale index before any container
or test launched. Original service/preview snapshots were not observed. Only one
active setup checksum changed for the separate successful run; no source fix or
retroactive pass is inferred from it.

A later readiness fix aligns the lazy host with eager `CurrentLocation`
readiness. Valid saved/fallback native documents can observe an idle link without
enabling SPA interception. Matching unit proof on each SDK passes six core,
six development-core, one module and one development-module test, plus four WASM
vets, with no failures or skips. This combination has unit evidence only: the
400 browser checks used earlier producers, and no new full offline/HMR campaign
is claimed for the readiness change.

Four separate Node refresh-production transport checks per SDK cover native,
local buffered/streaming RuntimeAPI and static HTTP/assets; they do not
instantiate WASM. Thirty-three local CI-helper checks pass without executing the
remote workflow. Development-watch, full integration, current budget and remote
CI qualification remain pending. These results do not close release gates or
establish full loading/error parity.
