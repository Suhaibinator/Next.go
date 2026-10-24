# Public shells with request-time regions

`PrerenderPolicy.Partial` captures a public page/layout shell and static metadata
while named server regions render from the current request. This explicit Go
subset is implemented with native/compiler, browser and standalone executable
verification for the scope below. It is not full Next.js Partial
Prerendering or Cache Components compatibility.

## Declare the boundary

Put `prerender.go`, `page.go` and `regions.go` in the same ordinary route
package. Keep them native-only with `//go:build !js`. The generator registers
the region functions in the server entrypoint; routes and region implementations
remain absent from the browser import graph.

```go
// prerender.go
func Prerender() *nextgo.PrerenderPolicy {
    return &nextgo.PrerenderPolicy{
        Partial: true, HostIndependent: true,
        Revalidate: time.Hour, Expire: 24 * time.Hour,
        Tags: []string{"catalog"}, Fallback: nextgo.FallbackBlocking,
    }
}
```

```go
// page.go
type VisitorProps struct { Slug string `json:"slug"` }

func Page(c *nextgo.Context) (nextgo.Page, error) {
    return nextgo.Page{
        Title: "Catalog",
        Body: ui.El("main", nil,
            ui.El("h1", nil, ui.Text("Public catalog")),
            ui.RequestRegion("visitor-panel", "visitor",
                VisitorProps{Slug: c.Params["slug"]},
                ui.El("p", ui.Attrs{"role": "status"},
                    ui.Text("Loading current request…"))),
        ),
    }, nil
}
```

```go
// regions.go
func Regions() map[string]nextgo.RegionFunc {
    return map[string]nextgo.RegionFunc{"visitor": visitor}
}

func visitor(c *nextgo.Context, raw json.RawMessage) (ui.Node, error) {
    var props VisitorProps
    if err := json.Unmarshal(raw, &props); err != nil {
        return nil, err
    }
    // Validate props and authorize any backing-data read here.
    return ui.El("p", nil,
        ui.Text(props.Slug+": "+c.Query.Get("message"))), nil
}
```

`nextgo.RegionFunc` has signature
`func(*nextgo.Context, json.RawMessage) (ui.Node, error)`. Its `Context` carries
the current request, route parameters, query, cookies/authentication headers and
middleware context values. It is not the anonymous shell-generation context.
Callbacks receive owned props bytes; validate their application schema and
honor context cancellation for data reads. They can return Go islands or nested
deferred nodes through the existing server component APIs.

The first argument to `ui.RequestRegion` is a unique boundary ID; the second is
a registered name. Both match `[A-Za-z][A-Za-z0-9_-]*` and are at most 128 bytes.
Only the ID, name, public static props and fallback framing enter the captured
shell. Callbacks/closures are never serialized or run during build capture.

## What is cached

Page/layout/provider work and head/document metadata run once per public shell
generation. Capture materializes their HTML and bounded region byte-offset
descriptors; a retained shell is reused by canonical path, locale, final build
and effective configuration. Serving it does not rerun the static providers.
Each region is rebound to the current request after shell selection; personalized
region output is never put into the static shell cache.

`HostIndependent: true` is an application promise covering the complete static
page, layouts, metadata, fallbacks and props. Build and runtime shell generation
use a synthetic public GET with canonical route parameters and immutable public
framework/locale configuration. It has no cookies/authentication or other
headers, query, body, TLS state, Go path values, arbitrary middleware values or
hidden request transport state. Shell regeneration may retain cancellation and
deadline, not private request values. Go can still read globals, files, services
or environment variables: there is no purity analysis or automatic secret
classification. Keep all request-sensitive output inside registered regions.

Props and fallbacks are public artifact/cache data. Never put credentials or
private values in them. Captured HTML is trusted compiler/authenticated-store
output, not an upload format or HTML sanitizer. Retained HTML/metadata/props
own their backing storage under the existing snapshot ownership contract.

Every final representation is `private, no-store`; the framework clears reusable
navigation-age/prefetch state. Cookie/query requests can reuse a public shell
while their regions remain fresh. Draft/nonce/cache-bypass/sensitive-rule/body
conditions keep the existing live-render bypass. Clients negotiating the
[public-shell prefetch protocol](PARTIAL_PREFETCH.md) can retain only fallback
HTML/head for a later server-validated resume; region callbacks never run during
prefetch. Clients without that capability retain 204-prefetch behavior.
Do not add a separate `CachePolicy`: ordinary full-page caching of a route
with regions is rejected.

## Rendering, errors and access

Initial HTML contains useful static content and each region's immediate
fallback. Regions use the existing inert-template transport and outside
completion marker; Go/WASM adopts completed nodes and hydrates their islands.
Deferred region content requires WASM to become visible; without it, fallbacks
remain. Negotiated navigation commits the validated static shell and metadata,
then receives request-time regions. Existing cancellation and recoverable
current-route retry rules apply; a retry makes fresh region requests and does
not replay a mutation automatically.

Title, head, document controls and initial HTTP status belong to the static
shell. Region responses are sealed: callbacks cannot set headers/cookies,
redirect, change the committed status or stream metadata. Authenticate and
authorize the request in middleware/API/data boundaries before flushing any
protected shell. A late region denial/error becomes generic deferred error
content; it is not an authorization/status-interrupt mechanism. Validate access
again inside private data loaders as appropriate.

All progressive regions share [application stream admission](STREAMING.md#application-wide-admission-and-deadlines)
and its byte/count/deadline/callback limits. Legacy navigation JSON and HEAD use
buffered live region resolution; a callback ignoring cancellation can retain
its physical callback/render slots until it returns. Limits do not forcibly
terminate Go code or bound arbitrary application allocations, RSS or concurrency
in other app processes.

Capture allows at most 128 regions, 2 MiB of shell HTML, 64 KiB of JSON props per
region and 1 MiB aggregate region props. Props require valid UTF-8 and unambiguous
JSON. Ordered nonoverlapping byte offsets and complete fallback framing are
validated before shell adoption. Fallbacks must be immediate: nested request
holes and ordinary `ui.Defer` callbacks inside static capture are rejected.
Named registrations must exist, and a partial route must capture at least one
region. Existing manifest, metadata, shared-codec and actual-JSON admission
budgets still apply independently.

## Regeneration and deployment

Local shells use the existing prerender lifetime/tag/path invalidation behavior,
including original generation ages, background stale refresh and blocking hard
expiry. `FallbackBlocking` can generate a public shell for an unlisted valid
path; `FallbackNotFound` keeps the enumerated closed-list behavior. See
[dynamic parameter composition](STATIC_PARAMS.md). Development renders live;
it is not proof that production reused an embedded shell.

`Partial: true, Shared: true` uses `Config.SharedCache` and the existing online
build-ticket protocol for **shells only**. Original authority ages, one-use
consumption, invalidation/revocation and optional durable-file recovery do not
persist private region results. Build/startup require the online authority;
unknown tickets skip seeds safely. Use acknowledged `ExpireCache`/
`MarkCacheStale` for shared invalidation; legacy `Revalidate*` remains local.
See [the authority contract](SHARED_CACHE.md#shared-prerender-admission).

Partial artifacts use private manifest **version 3**, and shared snapshots with
region descriptors use DTO **format 3**. Current readers retain support for
older full-page formats 1/2; older readers reject the new shell format instead
of treating its fallbacks as a complete page. Actual serialized shared JSON
must fit 1 MiB. A valid shell rejected by the narrower shared admission bounds
is rendered once without retention; a valid decoded path over the shared
512-byte label limit also uses an uncached public shell. Its regions still run
fresh and the final response remains private/no-store.

Only ordinary routes support this explicit cached-shell composition. Parallel
owners/defaults and interception composition are rejected or use their existing
live cache-bypass path; they do not gain independent cached-region ownership.
Static export rejects request-region responses before reading their body or publishing and
preserves previously published output. A runtime server is required.

## Example and verification scope

Run the ordinary demo commands, then visit `/partial`, `/partial/alpha` or
`/partial/beta`. The dynamic sample enumerates alpha/beta and rejects unknown
slugs. Shell generation/title/meta markers remain stable while request-region
IDs, query text and cookie-derived visitor text are fresh. Two Go counters in
the shell/region hydrate independently. For example:

```sh
go run ./cmd/nextgo build --out ./dist/demo ./examples/demo
./dist/demo/server -addr 127.0.0.1:3060
curl --no-buffer -H 'Cookie: nextgo_partial_name=Alice' \
  'http://127.0.0.1:3060/partial/alpha?message=Hello&delay=500ms'
```

`NEXTGO_DEMO_PARTIAL_REGION_FAIL=true` is a build guard: a build still succeeds
because region callbacks are not run. At runtime that flag deliberately fails
the region, so unset it for successful interactive output. Conversely, setting
`NEXTGO_DEMO_PARTIAL_SHELL_FAIL=true` only after a successful production build
demonstrates that an eligible retained shell does not rerun its providers;
uncached paths, live bypass or regeneration can still fail. Neither flag is a
request query switch or a production health mechanism.

### Qualification (October 24, 2026 UTC)

The following results describe the preceding explicit partial-rendering source.
The later public-shell prefetch/resume increment has separate focused qualification; see
[its scope](PARTIAL_PREFETCH.md#verification-and-remaining-work).

The corrected frozen source passes full native race/vet on Go 1.27.3/macOS and
Go 1.24.13/Linux, matching core/module WASM tests and client vet on both SDKs,
host module vet, and demo/workspace generation checks. A fresh guarded demo
build with `NEXTGO_DEMO_PARTIAL_REGION_FAIL=true` succeeds, proving build capture
does not invoke region callbacks; fresh workspace/cache builds also pass.
Complete host and nonroot read-only Linux executable suites pass, including
partial rendering, preceding shared-cache/seed/HTTP behavior and both buffered
and streaming mock Lambda transports. UI/root/audit/codec/ownership and actual
native/WASM import-graph checks cover the explicit boundary and forged inputs.

The final browser subset passes **377 checks / three existing Chromium-only CDP
skips on other engines**: 95 host Chromium checks (61 production, 13 navigation
streaming, seven partial, nine shared-prerender and five deployment-skew), and
282 Linux Firefox/WebKit/touch checks (180 production, 39 navigation streaming,
21 partial, 27 shared-prerender and 15 deployment-skew). Each final suite is
counted once. Earlier diagnostic/precheck runs, including ten macOS Chromium/
WebKit skew checks on another artifact and initial seven-check attempts, are
not added to this total.

Logs and the source manifest are retained under
`.nextgo-test-results/qualification/partial/`. The initial private-header
freeze failure was fixed before repeating qualification. A conflicting results
bind directory and a WebKit fixture's readiness fragmentation were harness
failures, corrected without weakening application behavior; initial logs,
traces and counterfactual evidence remain retained. The final 21 Linux partial
checks and a concurrent seven-check host run pass.

Remote checkpoint `8cfb91d` is historical: its broad browser job finished with
**953 passes, three skips and one WebKit deployment-skew failure**, while its
other four jobs passed. The failing test waited only for HTTP 200; a cold
prefetch carrying an old slot selection can legitimately return 400 under new
configuration, while a warm cached prefetch can return 200. Current corrected
skew checks cover both and prove mismatch discard/full-document recovery
(5 host + 15 Linux, already included above). That fixes the test assumption;
it is not a claim that the historical remote run passed or that current remote
CI is entirely green.

These results verify the implemented subset, not full V12/Cache Components
parity or production maturity. No release gate is closed.

Automatic Go request-data analysis, automatic cached components and nested
lifetime propagation, complete session-scoped partial prefetch, unknown-parameter shell reuse, static
application shells, metadata streaming and parallel/independent route-region
composition remain future work. Actual-cloud, accessibility, load/soak and
recovery qualification remain open. All 79 capability rows and 11 release gates
remain tracked in [the parity inventory](PARITY.md).
