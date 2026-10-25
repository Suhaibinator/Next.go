# Typed mutation confirmation with a fresh view

The base action-view subset has the preceding scoped qualification below.
The new native-cookie/FIFO/ABI-3 phase is under implementation/qualification;
no full application-build matrix is claimed for that phase. It adds
an explicit action call that carries typed mutation confirmation and fresh
server-rendered UI through one POST response. Existing `Call` and
`CallMultipart` behavior is unchanged. No full action/parity or production
qualification claim follows from this contract.

## Keep confirmation separate from view success

Go/WASM contracts expose:

```go
func (c Contract[I, O]) CallAndRefresh(ctx context.Context, input I, options ...CallOptions) (RefreshResponse[O], error)
func (c Contract[I, O]) CallMultipartAndRefresh(ctx context.Context, form *client.Multipart, options ...CallOptions) (RefreshResponse[O], error)

type RefreshResponse[O any] struct {
    Response[O]
    View client.ActionViewOutcome
}

// client.ActionViewOutcome:
// Applied bool
// Status  int
// Err     error
```

A **nil top-level error confirms the typed `Data`**, even when `View.Err` is
non-nil. The view may fail after the mutation committed or after part of the
new page was applied. **Never repeat a confirmed mutation to repair its view.**
Keep the returned data and refresh/reconcile separately. A non-nil top-level
error means this client did not obtain a valid confirmation; transport loss or
an oversized/malformed success can leave the mutation outcome unknown. It is
not proof that the backing write was rolled back.

```go
result, err := actionrefs.ViewSaveAndRefresh(ctx,
    actionrefs.ViewSaveInput{Amount: 2})
if err != nil {
    // Handle validation/denial or reconcile an unknown outcome.
    // Do not blindly retry a write after transport failure.
    return err
}
// result.Data is confirmed, independently of the refreshed page.
if result.View.Err != nil {
    // Retain confirmed data and offer a separate Refresh/reconcile action.
}
```

`View.Applied` means a validated page was committed. It can be true alongside
`View.Err` if later deferred content failed. `View.Status` is the refreshed
page/failure status, not necessarily the outer POST status. A valid custom
404 can be applied with status 404; a terminal view failure can report 503
after an HTTP-200 result stream confirmed the mutation. The function returns
after its view consumption finishes or fails, rather than exposing a separate
application confirmation callback.

Generated action references add `FooAndRefresh` and
`FooMultipartAndRefresh`, forwarding the same contract APIs. The existing
`Foo`/`FooMultipart` wrappers still return ordinary typed results without
automatically applying UI. Call either API from a Go goroutine outside a
synchronous browser callback and associate the caller context with its scope.

## Request preflight and the fresh server request

Combined calls explicitly negotiate action-view protocol 1 with the current
build/configuration/draft identity, source path/query and selected slot state.
The server validates these before invoking the action: source metadata is at
most 8 KiB and selected-slot metadata at most 16 KiB. Invalid, duplicated,
cross-scope, API/handler/proxy or incompatible metadata fails before mutation.
An unknown ordinary page URL can still render its custom not-found page.
The source URL is request metadata, not permission to read a page.

The native action still runs its existing origin, body/revision and
authorization-before-decode checks once. Combined responses require an
`actions.Router`; an opaque custom `Config.Actions` HTTP handler can retain
ordinary calls but does not provide this negotiated view path. No new action
binding, automatic authorization or database transaction is introduced.

After success, existing router `OnRevalidate` hooks run as before. The combined
path then acknowledges configured local/shared `ExpireCache` operations for the
returned tag/path selectors. A failed acknowledgement produces confirmed data
plus a view failure (503), without applying stale UI. Local invalidation may
already have happened and the backing mutation is not undone. Handle remote
invalidation failure explicitly; this is not cross-store/database atomicity.

The server makes a fresh same-scope GET internally through middleware with a
fresh request Memo and cache bypass. Page/layout/head work is rendered again
with current request credentials, rather than reusing a public shell/reference.
It preserves the outer `After`/platform completion collector, so internal view
rendering does not prematurely finish a nested platform invocation. Cookies,
headers and status-changing page controls are prepared before the first result
flush. Result is the first wire frame, but it is not an immediate database-commit
notification independent of that preparation.

A bounded eight-step same-scope redirect chain is supported. A destination
outside the application origin/locale scope requires explicit document
reconciliation: the combined view reports 409 rather than following it
automatically. This does not change ordinary contract redirect suggestions or
native progressive form redirects.

## Cookies, draft state and private output

Native `actions.Result.Cookies` and typed `mutation.Result[O].Cookies` now
stage validated native cookie output without JSON exposure. This new phase is
under qualification. The request-local jar can pass those handler cookies, POST
middleware effects and fresh page updates into same-response authorization.
See [native cookie controls and session limits](NATIVE_COOKIES.md). Result-based
cookie output is distinct from page `nextgo.Context.SetCookie`; full identity,
revocation and automatic ordinary-call refresh semantics remain open.

A request-local jar tracks known `Set-Cookie` updates only. Incoming Cookie
values and their order are preserved; no missing original path attributes are
invented. Updates overlay only matching domain/path/secure scopes, including
deletions. This is not shared session storage or complete browser-cookie/session
emulation.

Page-origin cookies with a missing/invalid default path receive an explicit
default derived from the **public escaped page URL** before stream flush or
redirect forwarding, so they do not inherit the POST action endpoint's path.
If that default cannot be represented as a valid Cookie Path, for example a
literal semicolon in the default directory, the mutation remains confirmed but
the view fails with 500 before invalid cookies/UI are emitted. An explicit valid
cookie path works. This deliberate fail-closed edge remains part of the
incomplete D02 request/session contract.

Managed draft state is rebound after cookie updates; a changed state is
reported as view conflict (409) before the next fresh GET. Applications remain
responsible for their own session and backing-data authorization. Data already
emitted cannot be recalled after a later identity/data change.

Combined output is always private/no-store. It carries no public-shell
references or reusable browser-cache lifetime. The ordinary page response and
head/DOM ownership validators still apply, including denial/error rendering.
Deferred callbacks share the current stream admission, deadline and cooperative
cancellation limitations; response streaming does not force-stop arbitrary Go
callbacks or make external effects transactional.

## Protocol and bounded failure

The independent media type is `application/x-nextgo-action-view+json`, protocol
version 1. Its newline-delimited sequence is:

```text
result → shell → chunk… → done
result → view-error
result → shell → chunk… → view-error
```

The typed result JSON is limited to 64 KiB; encoded frames are limited to
2 MiB, total bytes to 8 MiB, frames to 131 and boundaries to 128. The existing
typed response limit can be stricter but cannot raise these protocol limits.
Duplicate/unknown fields, invalid identity/ownership, unexpected ordering,
truncation, trailing records and unfinished boundaries reject the stream.
There is no replay/resume that re-executes the action.
These are wire/transport bounds, not limits on application loaders, database
allocations, whole Go heap or RSS.

A failure before confirmation is returned through the top-level error.
Once the result is validated, later protocol/render/transport failures belong
to `View.Err`; confirmed data remains available. Late generic errors redact
server diagnostics. A result too large or invalid to confirm can still follow
a committed write, so application reconciliation/idempotency policy matters.

## Document ownership and supersession

The core browser document owns combined transport and UI application. Before
commit, including inside the view-transition callback, it checks action
sequence, navigation identity/source, history entry, slots, build/configuration,
draft and page-freeze state. A newer action dispatch fences older UI; navigation,
history changes or freezing can supersede it. An old settlement may invalidate
browser reuse, but cannot reorder or roll back database writes. Application
state outside the replaced DOM still needs its own concurrency policy.
The new [typed dispatch FIFO](MUTATION_DISPATCH.md) serializes ordinary and
combined typed transport across eager/lazy callers. Enqueueing alone does not
fence the active view; snapshots/fences start at actual dispatch. That new queue
is under qualification. Arbitrary generic Fetch and server/database work are
not serialized by it.

Before commit, caller cancellation stops its view. At the synchronous commit
boundary, transport detaches from caller cleanup so replacing the calling island
does not cancel its own remaining chunks. Remaining document-owned work is
bounded by the original 30-second budget and the same ownership fences, not a
new unlimited lifetime. Existing streamed retry/reconciliation is separate
from submitting the action again.

The new phase uses lazy Go/WASM bridge ABI 3 to delegate transport, typed-result
validation and document commit to the core runtime. Module callbacks remain
retained until the physical host operation settles; cancellation cannot free a
callback that the host may still call. This does not create another router or
shared Go heap across modules.

## Example and current evidence

The `/action-view` sample uses the generated `view/save/v1` contract, JSON and
multipart calls, an eager/lazy Go caller, fresh server metadata and a deferred
counter. Its public counter is process-local and resets on restart. It includes
an intentional mutation failure and a separate deferred-view failure to show
that confirmed writes and UI refresh outcomes differ. No application JavaScript
or durable storage is added.

### Preceding action-view qualification

The preceding action-view source passes **254 distinct local checks**: 75 focused
combined-action cases (15 host Chromium + 15 host WebKit + 15 Linux Firefox +
15 Linux WebKit + 15 Linux touch) with no skips, plus 179 selected host
forms/mutation/optimistic/location/lazy/script-CSP/transition/production
regressions. Each completed suite is counted once. These are subset checks,
not the complete capability/release-browser inventory.

Go 1.27.3/macOS arm64 and Go 1.24.13/Linux arm64 full native race/vet, matching
core/mutation/module WASM tests, both WASM vet/component builds and final
host/nonroot read-only network-isolated Linux deployment suites pass. The
executable suites include both mock Lambda modes and every lazy asset; the
focused new deployment suite also passes. Actual-cloud behavior is not implied.

The final 702-input manifest includes Go/module/public/media/component data and
scaffold/workspace embedded inputs. It is retained at
`.nextgo-test-results/qualification/action-view/final/input-manifest.json`,
SHA-256 `2eab8b840abf97197d2606df95ae122798268379feb5785e1ba545acb990640c`.
The exact host server SHA-256 is
`5a12042d9f96ce965821cca099040609493c42619706c1145f1660b8d9157285`.
Previous typed-mutation/generated-action matrices remain historical and are not
added to 254.

A positive native-transition counterfactual against earlier artifact SHA-256
`75d23d1b43a792dad1e58c487dd2bdbd55e6b2d1a83fab33ba7c81179c4327c3`
reproduces live scroll changing from 650 to 0 at the deferred commit while
confirmed data and old SSR remain correct. The core now captures the live
history carrier at its guarded commit. The simpler pending-POST scroll case
already passed on that old artifact through the global scroll listener; it is
not presented as proof of the transition defect. The fixture currently defines
15 cases per project. All 75 cases pass on the five qualified host/Linux
projects without skips, including actual native-transition cases in Chromium
and WebKit. The fixture still skips that case on browsers without the API;
these results do not certify every browser/device.

Earlier Linux deployment failures assumed the first lazy artifact was the
legacy counter. With multiple modules, fixtures now check every module's hash,
WASM magic and immutable caching, and locate the expected legacy proof in some
module while excluding it from core. Navigation-version and slash-readiness
fixture assumptions were also corrected. Initial logs and the counterfactual
remain under `.nextgo-test-results/qualification/action-view/`. Fresh final
deployment and browser checks pass after those fixture corrections; the earlier
failed attempts remain separate evidence.

The [current artifact startup observation](PERFORMANCE.md#action-view-artifact-observation-october-25-2026-utc)
records 8,537,436 raw / 2,203,698 gzip-9 WASM bytes. Both reference size budgets
fail; the measured startup and memory capacities do not establish action
latency/throughput, production performance or RSS.

At the **October 25, 2026, 02:00 UTC** snapshot, remote `644606f` run
40150431665 had passing Go 1.24/stable, container and streaming jobs, while its
broad browser job was unfinished. That checkpoint predates the current local
action-view source; subsequent remote results remain separate. No whole-CI or
production-release pass is inferred from the local matrix.

Inline server closures, durable idempotency/transaction coordination, complete
single-response form/state integration, production dispatch/cookie/session
conformance, durable revocation/key rotation, authorization/session management and
production/network/cloud/load/soak qualification remain future work. D10 stays
partial and all 79 capability rows/11 release gates remain tracked in
[the inventory](PARITY.md).

The target includes [Next.js's documented action cookie updates and UI refresh](https://nextjs.org/docs/app/getting-started/mutating-data#cookies).
The new native-cookie/session subset does not complete that capability or D16;
remaining semantics and qualification stay in the inventory.
