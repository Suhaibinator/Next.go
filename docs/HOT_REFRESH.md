# Experimental Go module hot refresh

Development hot refresh replaces explicitly opted-in lazy Go/WASM component
runtimes while retaining the document and their compatible island markup.
The persistent Go core coordinates the operation; native server code and the
document's core runtime remain unchanged during an eligible update. Other edits
use the existing rebuild and full-document reload path.

This is a bounded experimental development feature with the scoped qualification
recorded below. It does not provide full Fast Refresh, React/Next.js source
compatibility, arbitrary Go heap migration, or production hot swapping. The
normal production module entrypoint and navigation-v3/module-ABI4 contracts
remain separate from the version-1 development extension.
Development gating controls activation; shared Go library paths can still be
linked into production WASM. This does not claim zero production payload cost
or that all development implementation code is absent from those binaries.

## Opting in

An eligible component declares lazy loading and explicit state/DOM schemas in
its `component.json`:

```json
{
  "loading": "lazy",
  "refresh": {
    "version": 1,
    "stateSchema": "counter/v1",
    "domSchema": "counter/v1"
  }
}
```

The [refresh example](../examples/refresh) has two instances of a public counter,
editable text and a checkbox, an eager component, and a managed asynchronous-work
control. Run it through the ordinary development command. The example edits
compiled Go behavior; it does not simulate an update by rewriting response bytes
or changing the DOM from the fixture.

Inside `Mount`, declare a stable instance identity before creating state cells:

```go
if err := scope.RefreshInstance(p.Instance); err != nil {
    return err
}
count, err := client.NewRefreshState(scope, "count", p.Initial)
if err != nil {
    return err
}
```

Continue to use normal `State.Get`, `Set`, subscriptions, `Scope.Require`, and
scoped event handlers. A state update explicitly updates the view; registering a
cell does not introduce automatic rendering. Subscribe/clean up through the
owning scope. The instance key must be unique among live instances in that
module and stable across this replacement; do not derive it from mount order,
DOM position, or an automatically incremented runtime handle.

`RefreshInstance` and `NewRefreshState` are browser APIs. In ordinary production
and non-refresh components they do not install a migration registry;
`NewRefreshState` returns ordinary state. Existing `NewState` behavior is
unchanged. Only explicitly registered cells participate in the handoff.

Schemas are declarations by the application author. Change `stateSchema` when
the value representation or its meaning becomes incompatible, and `domSchema`
when the mounting contract or expected server markup changes. Equal schema
strings do not prove Go type identity: standard JSON decoding can accept added
fields with zero values, changed array lengths, or numeric conversions. This is
not an automatic structural migration engine.

## State ownership and bounds

The handoff is an in-memory JSON snapshot. It contains source module identity,
source mount handles, explicit instance keys, SHA-256 of exact raw server props,
and named cells. The persistent core separately retains and checks the exact DOM
root references. A numeric source handle is never treated as a replacement
runtime's new handle or used as a DOM selector.

Restoration requires matching component name, state schema, DOM schema, instance,
raw-props digest, and the core's original root ownership. Every old scope must
bind exactly once and every old cell must be claimed exactly once. Missing,
extra, duplicated, removed, or incompatible claims require document fallback.
Changing props does not silently migrate a previous instance's state.

| Bound | Limit |
| --- | --- |
| Opted-in modules | 8 |
| Live scopes per module | 64 |
| Cells across all scopes of one module | 64 |
| One serialized cell | 16 KiB |
| Complete aggregate snapshot, including metadata | 256 KiB |
| Value/type depth and visited nodes | 16 / 4,096 |
| Exact raw props per scope | 64 KiB |
| Captured DOM nodes across changed islands | 8,192 |
| Observed standalone text/checkbox inputs | 256 |
| One candidate module | 8 MiB |
| Public descriptor manifest | 32 KiB |
| Active transaction / retained pending update | 1 / latest complete table |
| Retirement watchdog | 5 seconds |
| Retired/replaced runtimes in one document | 16 |

Keys and schema/instance tokens are bounded, exact, case-sensitive public
identifiers. The state codec validates types and values before marshaling,
including escaped string and numeric byte bounds. It accepts a deliberately
narrow set of plain finite Go JSON values: strings, booleans, numbers,
string-keyed maps, slices/arrays, and structs with explicit exported,
non-anonymous fields and unique simple JSON names. Byte slices use JSON's base64
encoding. UTF-8 must be valid. Ordinary `omitempty` is allowed; other special tag
behavior is unsupported.

Pointers, interfaces, recursive reference types, functions, channels, complex
numbers, browser/DOM values, private/ignored fields, ambiguous JSON tags, and
custom JSON/text codecs are rejected. The type walk also checks empty
collections and pointer-receiver codecs without invoking application methods.
Values are validated again when captured and when decoded into the candidate.
An application can put a larger value into ordinary state; these limits bound
the refresh handoff, not every allocation or backing store in application RAM.

Snapshot JSON is never sent through HTTP or SSE, written into DOM attributes,
logged, or placed in cookies, browser storage, files, or build artifacts. Ordinary
visible UI values and public server props still have their usual rendering
semantics. Snapshots are transaction-local and discarded after successful exact
claims or document replacement; they are not durable state or a session store.

## Runtime retirement

A component disposal acknowledgment does not establish that its Go runtime has
exited. The stable browser bridge retains the actual `Go.run` promise for each
runtime; a successor can start only after the old runtime's real execution
completes normally and its qualified shim cleanup succeeds.

The Go coordinator verifies candidates before disposal, freezes new framework
work and scoped handlers, validates/captures the complete old state, and asks
each changed module to stop. A module disposes its scopes, removes its owned
motion listener, stops owned timers/listeners, and joins its physical callback
ledger. It releases exports only after the stop call's browser stack has
returned and active export callbacks are zero. Generated development main then
returns. The coordinator waits for the actual runtime promise, not merely a
status flag or a closed component scope, before starting a replacement.

Cancellation is distinct from physical completion. A canceled browser request
can return to its caller while its Promise callbacks remain callable. Managed
Fetch/streaming, file reads, script work/RPCs, asynchronous resources, validation
workers and transitions retain their physical ownership through callback
settlement/removal and actual worker return. Cleanup continuations remain
accounted after admission is frozen. A callback that ignores cancellation does
not acquire an early release because a model, observer, or scope was closed.

Start component asynchronous work with `scope.Go(func(ctx context.Context) { ... })`
and use the managed transports inside it. This records ownership before spawning
and retains it through the whole worker's actual return, including time before
its first request and after its last Promise settles. It permits at most 64
concurrent workers per scope. The scope cancels their shared context on unmount;
each worker must check cancellation before changing the DOM. A raw `go` wrapper
around `Fetch` cannot account for work outside the Fetch call and is outside the
refresh contract.

The initial implementation only attempts replacement from a quiescent supported
lifetime. Active managed work, forms, open dialogs, composition, routing,
transitions, queued mutations, unsupported scopes, or a loaded non-refresh lazy
runtime cause a full-document fallback. Cleanup can still produce physical work
that must drain before retirement. A timeout never abandons an old runtime and
starts a second one in the same document.

Effects/providers, child/keyed scopes, and location/selection subscriptions do
not yet have transfer contracts. Generic forms and file/password/radio/select/
textarea controls are outside the supported preservation slice. The current
coordinator checks retained node identity, root/parent ownership, and unchanged
focus/value/checked/selection state for bounded standalone text/checkbox inputs;
it does not recreate browser control state after arbitrary markup replacement.
Registered scope-owned lifetimes, effects, children and location/selection
observers are rejected by the known ownership checks. Manually constructed
unscoped lifetimes, arbitrary heap references and asynchronous work cannot all be discovered;
components using them must not opt in. This is not automatic detection of every
unsupported lifetime.

The shim adapter uses the private `_scheduledTimeouts` map from the compiler's
matching `wasm_exec.js`. The currently qualified digest is
`0c949f4996f9a89698e4b5c586de32249c3b69b7baadb64d220073cc04acba14`.
Runtime shape and the exact paired digest are checked; unknown shims use the
ordinary rebuild/reload path. This is a private implementation dependency,
requiring renewed qualification when the SDK/shim changes, rather than a stable
Go API. The small JavaScript bridge owns the native runtime promise and this
timer cleanup; refresh classification, state, admission and transaction policy
remain in Go.

Application package initializers must not start asynchronous framework work
before refresh registration. Such callbacks cannot be accounted retroactively.
Arbitrary application goroutines, raw `syscall/js` callbacks/listeners,
independent browser Promises/network work, unmanaged timers, provider stores,
and external side effects are outside this opt-in contract. Use managed scoped
APIs and responsive callbacks. A blocked synchronous WASM/JavaScript main thread
also prevents the browser watchdog from running; it cannot be preempted by this
feature.

## Compiler, supervisor and fallback

Eligibility requires a conservative compiler proof covering the selected native
and core graphs, non-eligible application inputs, configuration/schema/SDK/shim,
and the component inventory. Only a proven opted-in module's isolated Go closure
can change without replacing the native child/core. Native, eager/core, shared
or otherwise unproved input changes require the normal rebuild. Environment
changes and aggregate zones also use ordinary rebuild/reload. Build errors keep
the previous successful application and descriptors available.
This classifies an attempted build and leaves the normal watch contract
unchanged. Unclassified inputs cannot grant module-only eligibility when the
proof is tested; an arbitrary text-file edit alone need not trigger a build
unless it is a declared embed, pipeline input, or another normally watched input.

The supervisor additionally requires a same-child startup assertion, produced
after application construction, bound to the actual serving build/generation
and supported development configuration. The supervisor's authoritative HTTP
204 response explicitly disables refresh and permits ordinary baseline
hydration. An unavailable latest-table request is not evidence that refresh was
disabled. A descriptor manifest is public metadata only; it cannot carry the
state handoff.

An eligible update keeps the native server and serving generation, advances a
contiguous revision, and publishes the complete module table. The core validates
every observed revision parent before retaining only the latest pending table.
Revision gaps or incompatible native/core/schema ownership require document
fallback. Content-addressed candidate bytes are checked for exact size, digest,
and response URL and compiled before old modules are disposed. Merely repointing
an unchanged digest/epoch does not require retiring that runtime.

A fresh document first obtains a valid HTTP 200 latest descriptor table bound
to its actual native build/generation, then mounts islands. It can adopt
revision N directly even though the still-running server's embedded baseline is
older; N being nonzero does not itself cause another reload. Only an authoritative
204 permits the embedded ordinary baseline. Transport failure, malformed or
unbound 200, 409 and 503 preserve useful SSR and withhold all island hydration.
They do not trigger automatic document reload, recursive fetch or fallback to
possibly stale embedded modules.

One document-owned accessible panel offers **Retry loading Go** and a native
**Reload page** link. Manual retries reuse its panel/listener, and only one
physical manifest request is admitted at a time. Logical timeout/cancellation
does not open another flight while the previous transport still owns callbacks;
the native reload link remains available while it is pending. SSE received
during failed startup is only a bounded hint: an explicit retry must obtain a
current authoritative response before hydration. A valid response removes the
panel; a 200 table is bound before the first mount. This cold-start recovery
differs from a failed in-document replacement transaction, which reloads a fresh
document without persisting or replaying the old state snapshot.

The supervisor pins the serving native baseline and latest successful candidate
directories, with bounded older generations for transition requests. This is
development retention, not independent module deployment or production cache
rollout. A full reload loses state that was only retained in the old runtime;
browser-native restoration remains subject to browser behavior.

## Evidence and remaining work

The preceding standalone retirement experiment used actual Go/WASM execution
and exact paired shims from Go 1.27.3 and Go 1.24.13, each in Node and Chromium.
Its 19 scenarios per pairing reproduced post-exit listener, Promise and runtime
wake errors, including a surviving wake after explicit timer stop and physical
Promise drain. The qualified timer adapter plus owned cleanup passed 64 actual
retire/reinstantiate cycles in total. An intentionally nonsettling Promise
prevented runtime exit; a responsive JavaScript watchdog requested one actual
fresh document and no successor started in that document.

That experiment did not import this framework. Its private-shim result does not
prove browser heap reclamation, GC/RSS stabilization, arbitrary callback ownership
or production operation. The historical framework matrix below uses the frozen
1,007-input witness
`84996b196f69f05aba872e46ab3630d9537f672ab4a5779006f039e35489ba92`.

The newer cold-start recovery correction described above has passing focused
checks in four runtime test modes. Its two new actual-browser cases are pending
and are not added to the historical 88 Linux or 33 host development executions.
Those earlier results do not qualify this later source increment or replace its
new source-frozen matrix.

| Qualified scope | Result and boundary |
| --- | --- |
| Native and matching WASM | Host Go 1.27.3 and Linux Go 1.27.3/1.24.13 pass full native race/vet: 66 tested packages and 173 without tests; matching core/mutation/module WASM and core/module vet pass. The subprocess-only lock helper's deliberate parent skip is not a missing package test. |
| Development, host Go 1.27.3 | 33 passes across Chromium, WebKit and touch WebKit, with no skips or retries. Host Firefox is not claimed; two earlier smoke cases are supplemental. |
| Development, Linux Go 1.24.13 | All 44 cases pass across Chromium, Firefox, WebKit and touch, with no failures, skips or retries. |
| Development, Linux Go 1.27.3 | A fresh complete run passes all 44 cases across those four engines in 9.7 minutes, with no failures, skips or retries; frozen-input and cleanup checks pass. |
| Ordinary production, Linux, both SDKs | 96 browser executions: 48 per SDK across four engines and HTTP, buffered local Lambda, streaming local Lambda and static modes. Separately, 26 zone/CLI/adapter cases and 456 legacy deployment cases pass across the two SDKs. |
| Ordinary production and transport, host | Four Node cases and 12 Chromium cases pass across those four modes. Separate regressions pass 228 legacy Go cases, four feature-Lambda cases and 13 zone/CLI/adapter cases. |

These are overlapping platform/SDK executions, not a combined count of distinct
capabilities. The completed Linux development matrix is 88 executions (44 per
SDK), with the 33 host executions recorded separately. An earlier stable-SDK run
reached 38 passes before Docker disk exhaustion interrupted it; that incomplete
run is retained and is not added to the 88. Production checks exercise ordinary SSR/hydration/navigation and
inactive HMR; they do not replace development retirement/state-transfer tests.
The Linux production record reuses its SDK-specific four-case Node evidence
rather than counting another execution of it.

The development checks exercise actual compiled Go changes, state cells,
same-document node/control retention, native/core/schema/shared-input fallbacks,
last-good build recovery, descriptor bootstrap and candidate rejection. They do
not establish all runtime failure paths: host coverage explicitly excludes
browser mutation replay, IME/forms/deferred or unmanaged work, and forced stalled
retirement/handshake failures. Full-run terminal list-reporter results are
supplemented by eight passing warm cases, four per Linux SDK, with raw event
archives. Those archives record 16 actual old `Go.run` retirements with exit zero
and an empty timer map before any replacement starts. The eight supplemental
cases are not added to the completed 88-case two-SDK development matrix and do
not supply raw event archives for every case of that full matrix. The earlier
38-case infrastructure interruption and zero-test prelaunch attempt remain
separate records.

The recorded full 335-case host primary regression result remains **334 passed,
one failed**. Native Chromium reported a `masked` BFCache non-restoration; the
cause remains unresolved. An isolated comparison later observed three actual
restores each for bare, old and new variants. Its initial observation defect,
setup errors, and deviation from the planned five-attempt cap (eight retained
attempt files per variant) remain archived. That comparison does not repair or
replace the failed full-suite qualification, and no all-green 335 result is
claimed for this source.

The recorded refresh-phase G05 core size is 8,401,692 raw bytes and 2,157,893 bytes
with the measured Node gzip-9 procedure; both budgets fail. Private-shim cleanup and bounded swaps
do not prove all old runtime memory is reclaimed.

Local provenance is retained under
`.nextgo-test-results/qualification/refresh-wave/host-sdk-qualification.json`,
`.nextgo-test-results/qualification/refresh-wave/linux-sdk/qualification-final.json`,
`.nextgo-test-results/qualification/refresh/dev-host-full/qualification.json`,
`.nextgo-test-results/qualification/refresh-wave/linux-dev/qualification-final.json`,
`.nextgo-test-results/qualification/refresh-wave/linux-production/qualification-final.json`,
`.nextgo-test-results/qualification/refresh-wave/host-production/qualification-final.json`,
and `.nextgo-test-results/qualification/refresh-wave/host-legacy-deployment/escalated/summary.json`.
The initial host legacy launch failures remain in the parent directory rather
than being presented as the final rerun. Docker recovery is recorded separately
in `refresh-wave/infrastructure-recovery.json`; BFCache diagnostics are under
`refresh/bfcache-controlled-20261025/analysis.json` in the same qualification tree.

Full Fast Refresh remains **Partial**. General state/form/provider/resource
migration, arbitrary runtime unloading, React/Next.js source compatibility,
production hot swapping, long-running resource soak and all 11 release gates
remain open. No actual AWS qualification is claimed. The feature does not alter
the separately recorded 79-capability parity scope.
