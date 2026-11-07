# Private retained-route prototype

This experimental first slice adds bounded, opt-in history retention for small
buffered Go pages. Exact source head `44564f16` (tested merge `407382b1`) passed
its existing native, four WASM-personality, vet and build gates on Go 1.24.13 and
1.27.3 in [source CI 40488533451](https://github.com/Suhaibinator/Next.go/actions/runs/40488533451).
That evidence does not qualify this revision's canonical HTML UTF-8 extension,
which has Go formatting/syntax checks only. The separate `1ed87fb4` hosted
campaign failed its format-check on both SDKs before tests, builds or browsers;
its planned 72 browser cases and 73 native-process closures per SDK remain unrun.
The corrected `37d520e0` source gate also passed both SDKs in
[source CI 40498233862](https://github.com/Suhaibinator/Next.go/actions/runs/40498233862).
Its separate hosted producer stopped before browser qualification: Go 1.24.13
exceeded the five-minute compiler-package setup deadline, and Go 1.27.3 failed
six existing native workspace/provenance assertions under the producer's forced
environment. This revision prepares a narrower compiler bootstrap, a native-only
test environment correction, canonical HTML UTF-8 handling and the selected
offline donor/diagnostic integration; their combined bytes are unqualified.
V10, all 79 capabilities, all 11 release gates and production maturity remain
open. The source accompanies `examples/retained`.

## Opt in

The production application configuration and each eligible response must agree:

```go
nextgo.Config{RetainRoutes: true}

nextgo.Page{
    Body: ui.El("main", nil, content),
    Retention: &nextgo.RouteRetention{Partition: "public-demo-v1"},
}
```

`Partition` is public opaque application metadata, 1–128 visible ASCII bytes.
Never put credentials, session tokens, personal data or secrets in it. The
application must change it when previously retained UI is no longer safe to
reveal, including user/account/tenant/authorization changes. The partition is
not an access grant: every history activation first makes a normal fresh server
request and the server must perform its usual authorization and data loading.
Withholding `Page.Retention` declines retention for that response. Defaults stay
off. The manifest flag contributes to the existing configuration identity.

An unchanged server HTML fingerprint and partition permit an owned Back/Forward
visit to reveal the original connected DOM, its original Go component scopes,
counter values, uncontrolled input values and native details state. Push and
Replace create fresh visits. Cache/prefetch responses do not authorize a retained
activation; the current view stays visible during the real request. A failed
request preserves it. A valid denial, missing descriptor, unsupported response
or changed partition discards previously retained private state before displaying
the fresh response. A changed fingerprint renders fresh and resets page state.

This final behavior is deliberately incomplete: full parity requires fresh
server-output reconciliation that preserves compatible component identity and
unaffected state, including Refresh and query updates. The current `Refresh()`
invalidation removes aliases and makes even the active unchanged view ineligible
for reuse. Refresh therefore takes the cold path in this prototype. A hash-only
page cache is not the final V10 semantics.

## Ownership and eligibility

A supported page has exactly one existing ordinary top-level element inside
`#nextgo-app`, optionally surrounded by whitespace. The runtime marks and retains
that element; it adds no wrapper, reparents no island descendants, and preserves
the original `#nextgo-app > main` relationship and DOM identity. Hidden owners
stay connected with `hidden`, `inert`, and `display:none!important`. On activation
the original hidden/inert attribute values and display value/priority are restored.
Framework-owned app/status/announcer IDs stay outside view ownership. Collection
of islands, hydration errors, focus targets and nested scroll includes the owner
itself, and event/link handling ignores hidden owners.

Only ordinary eager islands with managed reversible ownership are eligible.
Conservative cold-path exclusions include:

- Multiple top-level roots, significant top-level text/comments, author IDs,
  custom elements, shadow roots, nested framework view markers, and oversized UI.
- All forms, controls with external `form` associations, all radios,
  `details[name]`, and `a[name]`. These can share document-wide native ownership;
  the prototype never rewrites names or IDs to make them fit.
- Dialogs, frames, video/audio/canvas/object/embed, script/style/link/template,
  base/meta and map/area elements, framework scripts/chunks and deferred metadata
  markers. Named image maps can resolve across connected owners too.
- Lazy modules, client-only rendering, keyed ownership, selection/location/link
  observers, layouts/outlets/slots/templates, parallel/intercepted routes,
  streaming/deferred content and partial shells.
- Development/refresh-HMR mode, draft/offline/static-shell/static-export modes,
  locale configurations, early-script configurations, preserved layouts,
  cache/prerender/region/slot routes, public-cache response policies and responses
  other than successful production buffered pages.

These bypasses are safety limits of the first slice. Multi-root preservation,
lazy/client-only ownership, forms, slots, streaming and the other interactions
remain required work. Keeping hidden siblings connected also means arbitrary
application-wide CSS selectors, global DOM queries and browser search can observe
them. Do not infer a general CSS/DOM isolation guarantee from the direct-child
test. Application code must scope its DOM access and avoid document-global
behavior when opting in.

## Managed activity and cancellation

Terminal lifetime and visible activity are separate. Hiding a supported scope
preserves State, providers, effect definitions and DOM; it detaches managed event
listeners, runs current managed effect cleanups, and cancels visibility-owned
tasks/resources. Hidden effect invalidations become dirty; activation runs the
latest setup once. Listener `js.Func` values are retained while hidden and released
on permanent disposal. Terminal `Scope.Cleanup`/`Lifetime.Defer` resources remain
owned until eviction or ordinary unmount.

If a managed scope cannot resume, its activity is terminally disposed and its
existing boundary is marked with a hydration error. The whole view loses its
reuse aliases. Its DOM remains visible with the generic persistent message
“A component could not resume. Navigate to another page or reload.” Hydration
cannot turn that failure into a ready state. Global link navigation stays usable;
ordinary Refresh or fresh navigation cold-renders and mounts a new scope. The
runtime does not retry Mount or resume the broken scope implicitly.

Use `Lifetime.Effect` for timers, subscriptions and observers that must stop while
hidden. Register each dependency explicitly. `State.Subscribe` and raw DOM/global
listeners are application escape hatches and do not automatically suspend. An
effect cleanup/setup must tolerate repeated visibility cycles and reentrancy.
Register ordinary `Scope.On` handlers once during Mount. An effect that creates
additional scopes/listeners must return their explicit disposer; repeatedly
registering scope-lifetime handlers on each effect setup would duplicate them.

`Scope.Go` captures its activity context before launching and receives cancellation
on hide. It is never replayed on activation. The application must cooperate with
context cancellation, fence its own late callbacks and explicitly initiate new
work when appropriate. A canceled physical worker keeps its ledger charge until
it actually returns. AsyncResource waiters and actual ResourceStore loaders hold
separate physical charges; cancellation of a waiter does not release a still
running loader. Resource sequence/epoch checks reject late results; hidden
invalidation/closure is delivered on visible reactivation without automatic load,
mutation or retry. Do not put irreversible mutations into replayable effects.

Lazy component modules retain their existing ABI. No optional pause/resume ABI
method claims compatibility: their routes take ordinary fresh navigation until
core/module ownership and version-pairing are implemented and qualified.

## Freshness, history and commit

The server hashes the exact rendered body variable into a versioned SHA-256
descriptor and marks retained responses private, no-store. Initial documents
strictly validate descriptor shape/version/partition/hash format after existing
build/config/document checks. Their fingerprint is a trusted server-issued stamp;
the client does not recompute a hash of browser-normalized initial DOM and no raw
HTML snapshot is duplicated in an attribute. Native server tests bind the stamp
to the exact body variable. Navigation envelopes strictly validate the digest
against the exact `Envelope.HTML` bytes before HTML parsing. A digest is not a
MAC, a DOM-integrity monitor, or protection against same-origin JavaScript.

Restore requires a runtime-owned bounded history alias, exact canonical URL
without fragment, same build/config identity, current partition and fresh body
fingerprint. Unknown, forged or evicted history takes the ordinary fresh path.
The existing history registry remains authoritative. Current document metadata,
history and location commit with the selected UI; history restores window scroll,
saved owner/descendant scroll positions, safe input focus and selection. Hash-only
navigation within a tracked alias of the same current owner keeps the existing
shortcut. Distinct or unknown same-URL history visits require fresh validation
too. State is per visit, not a global component-name registry.

Identity allocation, candidate validation and ledger admission precede outgoing
suspension or hiding. Deterministic preparation failure leaves current ownership,
DOM/state and history untouched. Navigation/cache epochs fence preparation,
effect cleanup, eviction, cold teardown and ordinary unmount before DOM/head/
history replacement. Precommit cleanup that starts a newer navigation resumes
the old visible owner where safe; rollback resume failures become explicit
activity errors. Valid privacy denial teardown still removes unsafe old state,
but can never commit a superseded response's head/history/location. This does
not promise rollback of arbitrary application side effects inside cleanup.
Malformed fetched descriptors report a recoverable error without revealing an
unvalidated snapshot; explicit invalidation makes hidden snapshots unavailable.

For partition changes or capacity retirement, ownership retirement and terminal
cleanup are distinct. After cancellation checks, the runtime detaches the old
ordinary root and removes its records/aliases without application callbacks,
then publishes the visible candidate within the real index budgets. It commits
metadata/history, activates and hydrates the new page, and completes focus/scroll/
history bookkeeping before finally disposing the detached old scopes. A newer
navigation started by that terminal cleanup therefore has an already visible,
hydrated committed page to preserve if its request fails. Disposal uses exact
old-scope identities and cannot remove new scopes. Idempotent final cleanup runs
on early return too.
Caught terminal cleanup errors during a same-generation cold response do not
veto its authorized fresh rendering after old private DOM has retired. Cleanup
continues, the fresh page hydrates, and a generic closing warning is shown after
commit only if no newer loading or current error status has taken precedence.
Generation changes still abort the superseded response before replacement.

Synchronous retained operations save/restore a report token on their root Scope;
children resolve the ancestor and disposal captures that root before unlinking.
Each token retains at most 64 diagnostics of 2,048 bytes. Detailed diagnostics go
to the console; visible activity failures stay generic. Old resume/cleanup errors
cannot overwrite a newer navigation's loading/error status. Nil-token default
and module reports forward unchanged. This is not a global report fence or a
solution for arbitrary asynchronous errors or uncooperative blocked callback
stacks.

## Policy bounds

The active owner counts toward all view bounds. The initial policy permits:

| Ledger | Bound |
| --- | ---: |
| Retained view records | 3 total; oldest hidden owner evicted first |
| DOM nodes, including text and root | 4,000 per view; 8,000 total |
| Eager island roots/scopes | 64 per view |
| Response/descriptor/metadata estimate | 4 MiB total |
| Owned retained-history aliases | 128 |
| Raw retained-response body / descriptor | 1 MiB / 1,024 bytes |
| Saved nested nonzero scroll positions | 128 per view |
| Physically running opt-in core workers | 128 until actual return |

Lifecycle admission additionally bounds each supported ownership traversal to
fewer than 128 scopes and fewer than 256 lifetimes, with at most 512 listeners,
64 resources, 512 effects and 512 deferred resources per relevant traversal.
Existing per-scope/per-lifetime API limits still apply. These are policy ledgers,
not RSS guarantees: arbitrary Go closures/state, DOM implementation memory,
cooperative cancellation and loaded WASM runtime memory cannot be measured or
unloaded by these counts. Capacity rejection uses ordinary fresh rendering where
safe. Eviction terminally disposes owned scopes, releases listeners and aliases,
and removes the hidden owner.
Detached terminal retirement is a separate transient phase containing the
already-bounded outgoing owner; it leaves the real retained registry before the
candidate enters. Its canceled workers keep their physical charges until return.
The registry bounds do not claim that parsed incoming or retiring DOM consumes
no memory, or that arbitrary blocked application callbacks have bounded stacks.
Without duplicating initial HTML, its ledger conservatively charges the full
1 MiB server body ceiling plus descriptor/identity overhead; fetched visits use
the bounded envelope estimate. Neither estimate measures later application state
or text growth.

## Verification status and required continuation

The candidate contains native index/server/wire tests, WASM ownership/failure and
lifecycle/resource tests, and a real prebuilt-server Playwright suite. Browser
source covers useful SSR before WASM, hydration identity, counter/draft/details,
effect pause/resume, real canceled HTTP work, direct-child ancestry/CSS, root
island/error/scroll behavior, history/focus/scroll, fresh loaders, supersession,
partition/denial reset, bounded eviction and unsupported native-group fallbacks.
No synthetic application response substitutes for a real server fixture.

The exact `44564f16` source/compiler checkpoint passed both SDKs' native race
suites, existing core/module/development WASM personalities, vet and real CLI/
retained-example builds in the source CI linked above. Declared skips and
repeated test-group executions across personalities/SDKs remain separately
recorded; summed groups are not unique-test counts. This is source/compiler
qualification, not retained-browser or production deployment qualification.

The exact `37d520e0` source gate also passed its complete source commands on
both SDKs. Freshly observed retained-group PASS events were 21 native, 38 core,
13 module, 22 development-core and 13 development-module per SDK. These are
selected repeated group observations, not unique-test totals; existing skips
remain explicit. They exclude the new canonical HTML UTF-8 tests and offline
integration in this revision.

The separate `1ed87fb4` producer stopped at format-check on both SDKs. The
corrected `37d520e0` producer then stopped at compiler-package setup on Go
1.24.13 and six native workspace/provenance assertions on Go 1.27.3 in
[producer CI 40498233638](https://github.com/Suhaibinator/Next.go/actions/runs/40498233638).
Neither reached its planned 72 browser cases and 73 native-process closures per
SDK. The narrower compiler setup and native-only environment correction are
source-reviewed preparations, not successful runtime results. The UTF-8 files
have Go formatting/syntax evidence only; compilation, tests and all four UI
personalities remain pending for this combined source. Selected offline donor
behavior and its failure-only observer also require fresh integration evidence;
a successful donor run does not exercise that observer or prove browser-race
repair. Standalone/browser, cross-platform, resource and deployment gates remain
open; earlier evidence is not promoted to this revision.

The next required increment is fresh-output reconciliation with stable branch/
component compatibility identity distinct from per-visit history aliases,
compatible props updates and a prepare/publish barrier. Refresh/query/history
must share that merge path. Full changed-output state
preservation, multi-root ownership, lazy/core integration, persistent layouts,
forms/client-only/keyed/slots/streams and complete interaction testing remain
open. No Next.js source compatibility or production maturity is claimed.
