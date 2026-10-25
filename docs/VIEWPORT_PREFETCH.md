# Typed links and viewport prefetch

`ui.Link` renders a normal escaped anchor with explicit Go prefetch controls.
Its default opts into production viewport speculation; plain `ui.El("a", ...)`
anchors retain their existing pointer/focus intent behavior. Both work before
WASM loads and keep the router's URL, navigation, authorization and cache rules.
This implemented subset has the bounded qualification recorded below; it does
not establish full prefetch parity or production maturity.

## Use a typed link

```go
ui.Link(nextgo.MustURL(c, "/about"), ui.LinkOptions{}, ui.Text("About"))

ui.Link(nextgo.MustURL(c, "/partial/alpha"), ui.LinkOptions{
    Prefetch: ui.PrefetchIntent,
    Attrs: ui.Attrs{"class": "nav-link"},
}, ui.Text("Load on intent"))

ui.Link(nextgo.MustURL(c, "/about"), ui.LinkOptions{
    Prefetch: ui.PrefetchNone,
    Replace: true,
}, ui.Text("Replace the current history entry"))
```

`PrefetchAuto` (also the zero value) marks an anchor for viewport observation.
`PrefetchIntent` uses pointer/keyboard-focus intent only. `PrefetchNone` disables
automatic viewport and intent work for that link; it does not disable an
explicit `client.Prefetch(target)` call or ordinary navigation. `Replace: true`
selects the existing replace-history behavior instead of push navigation.

`Attrs` accepts ordinary anchor attributes such as class, target, download and
`data-nextgo-reload`. The `href`, `data-nextgo-prefetch` and
`data-nextgo-replace` attributes belong to the typed arguments: trying to supply
them through `Attrs`, including with different casing, fails rendering. Existing
UI escaping and URL validation apply. A typed link does not turn an external
URL or download into an eligible framework prefetch.

There is no application JavaScript or React wrapper. The capability reference
is [Next.js's production viewport-prefetch guidance](https://nextjs.org/docs/app/guides/prefetching),
which describes automatic observation and bounded scheduling. Go APIs, public
shell transport and cache semantics differ; Next.js source compatibility is
not promised.

## Run the example

Viewport speculation is production-only, so build and run the standalone demo:

```sh
go run ./cmd/nextgo build --out ./dist/demo ./examples/demo
./dist/demo/server -addr 127.0.0.1:3060
```

Visit `http://127.0.0.1:3060/prefetch`. The page compares automatic,
intent-only, disabled and plain-anchor links. Watch the browser network panel
without hovering, then scroll to reveal another automatic link. The public
partial-shell link prefetches only fallback/head data; clicking still makes a
fresh server request for private regions. The companion counter verifies that
ordinary interaction remains available. `nextgo dev` deliberately suppresses
viewport work, while its pointer/focus controls still work.

## Scheduler and lifecycle

The current Go browser scheduler uses two viewport workers, at most 32 queued
candidates and 128 simultaneously observed links. It walks the managed app tree
incrementally in slices of 256 elements, yielding to the browser between slices;
observation cohorts rotate after 100 ms when full, rather than imposing a hard
DOM-prefix cutoff. It does not collect an unbounded list of every anchor.
Observer callbacks queue eligible visible links; workers start network work
after those callbacks return and recheck URL, connection and visibility before
fetching. The scheduler does not guarantee a particular
latency or throughput on a large, changing page.

Production manifest policy enables viewport work. A development manifest
suppresses viewport observation; pointer/focus intent and explicit
`client.Prefetch` retain their existing behavior. Missing IntersectionObserver
or MutationObserver support leaves the ordinary intent path available. These
are optional browser optimizations; navigation and native anchors still work.

Viewport work pauses while the document is hidden, the browser reports offline,
Save-Data is enabled, or reported connection type is `2g`/`slow-2g`. Managed
draft/frozen preview state also suppresses it. When APIs are absent, the runtime
does not infer a hidden network/session state. These gates apply to viewport
speculation, not a blanket restriction on manual or intent requests.

Page hide/visibility and network-policy changes clear observations and queued
work and cancel viewport-owned requests. Mutation/scroll/resize rescanning
discovers new links, changed URLs and removes disconnected/ineligible queued
candidates. Foreground navigation cancels a pending viewport-owned prefetch for
its URL and proceeds with active navigation rather than waiting for background
work. Existing intent-owned requests keep their separate behavior.
Workers remain occupied until their pending request finishes even after cancel;
pause/resume cannot create extra viewport workers.

A document-owned WeakMap records admitted/cache-fulfilled attempts by DOM node,
normalized URL and cache epoch. A separate bounded 128-URL/epoch ledger
coalesces sequential duplicate links, including responses that are private or
fail; each URL is at most 8 KiB. Mere queueing, offscreen skips and paused work
do not consume an attempt. Canceling an exact viewport-owned flight on hide
also clears its evidence so resume can retry.

Repeated visibility does not poll an attempted link after its cache TTL expires.
A changed URL or invalidated cache epoch permits another attempt;
pointer/focus/manual prefetch can still request data. Recent-URL evidence can
age out at its count bound. Removed nodes are not kept alive by the WeakMap.
Observer callbacks belong to the document rather than individual page/island
scopes.

Only eligible same-origin HTTP(S) application links participate. Current-path
fragment-only targets, downloads, non-self targets, explicit reload links,
API/handler/asset destinations and out-of-mount URLs keep existing rules. The
viewport scheduler additionally ignores URLs over 8 KiB. Its queue/observer
bounds do not limit all intent/manual network calls, page allocations or browser
RSS.

## Cache scope and verification

Server response eligibility remains authoritative: a visible link does not
authorize a private read or make an otherwise private response reusable. For explicitly partial
routes, [public-shell prefetch](PARTIAL_PREFETCH.md) keeps only the public
fallback/head and always contacts the server before active navigation/private
region rendering.

The existing 32-entry/8-MiB response cache and negotiated public-shell lifetime
continue to decide reuse. Scheduler attempt evidence is not an authentication
token, data-invalidation subscription or session detector.

## Qualified subset and provenance

The current parser-ordered startup source passes both SDK full native race/vet,
matching core/module WASM tests/vet, generation checks, guarded builds and full
host/nonroot read-only Linux HTTP/both mock Lambda suites. Its final selected
browser matrix passes **232 checks**: host 120 selected regressions + 8 partial
+ 10 viewport + 10 shell; Linux 24 partial + 30 viewport + 30 shell. Five fresh
WebKit diagnostic runs pass separately and are excluded. See
[bootstrap behavior, artifact hashes and provenance](BOOTSTRAP.md).

### Preceding viewport artifact

The frozen 635-input Go/module/public source passes full native race/vet on
Go 1.27.3/macOS and Go 1.24.13/Linux, matching core/module WASM tests/vet on
both SDKs, generation checks and a guarded production build. Complete host and
nonroot read-only Linux HTTP/both mock Lambda executable suites pass.

Final viewport checks pass **40 cases**: ten host Chromium and thirty Linux
Firefox/WebKit/touch cases. Selected host SSR, navigation-stream, location and
deployment-skew regressions pass **89 cases** after the correct fixture servers
were restored. That is **129 feature/regression checks on that preceding artifact**, counting each
final suite once, not a full release-browser matrix. Separate current-source
public-shell and partial-region browser reruns were not part of that count.
The later bootstrap-ready matrix above supplies separate current evidence.

The initial host regression configuration omitted `/demo`, `/slash` and locale
fixture servers, causing three setup failures; the corrected complete 89-case
selection passes. An earlier Linux viewport run had 17 passes out of 30 cases;
WebKit's native offline state in a network-isolated container and Playwright
MutationObserver attribution required fixture corrections before the final
30-case pass. Initial logs and diagnosis are retained under
`.nextgo-test-results/qualification/viewport/`, alongside
`go-public-manifest.json`. Failed/diagnostic runs are not added to the final count.

Remote checkpoint `4e78454` has passing native SDK and container jobs, but its
streaming/shell job observed 39 passes and one `Response.finished` observer hang
with `ERR_ABORTED`; the abort's caller remains unresolved. Its broader browser
job subsequently passed 1,484 checks with three skips. These historical results
are not combined with current 232, and the failed streaming job means no
all-green remote result or runtime-abort fix is established. Historical failures in
[the inventory](PARITY.md) remain recorded.

## Bundle and local startup observation

The [recorded production-loopback sample](benchmarks/viewport-go1.27.3-macos-arm64.json)
uses Go 1.27.3/macOS arm64, Chromium 156.0.8078.4, Node 25.7.0 and Apple M4 Max.
The eager WASM is **8,260,663 raw bytes / 2,143,540 gzip-9 bytes**, SHA-256
`31c2915750d9a69180d0ff194d27ea7120a5aa65de3503ad3b9171ba2783ff54`.
Both the **6-MiB raw and 1.6-MiB gzip-9 reference budgets still fail**.

Ten fresh contexts in one Chromium process observed `/prefetch` readiness of
99–111 ms, median **107 ms**, with a **6,815,744-byte** WASM linear-memory
capacity. The standalone production executable ran on loopback with warm
OS/server state, no CPU/network throttling, parser-ordered startup and automatic
visible-link work.
Compiled-code reuse and local conditions limit interpretation. These are not
production timing, network-load or speed-comparison results. Linear-memory
capacity excludes JavaScript buffers and is not live heap, peak memory or RSS.
No performance or production release gate is closed by the measurement.

Session-scoped App Shells, automatic request/session analysis, speculative
private phases, invalidation subscriptions across tabs/replicas/CDNs and full
Next.js prefetch parity remain separate work. All 79 capability rows and 11
release gates remain tracked in [the inventory](PARITY.md).
