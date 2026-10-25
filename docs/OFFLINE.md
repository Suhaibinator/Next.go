# Experimental managed offline pages

Managed offline mode saves an explicitly declared **anonymous public reading
edition**. It does not preserve the current user's identity, permissions, private
application state or transactions. Production builds now include a Go/WASM service
worker, immutable dependency inventory, native document preparation and eager Go
browser controls. This is a bounded experimental subset; complete offline/PWA
parity and the project's release gates remain open.

## Run the example

```sh
go run ./cmd/nextgo build --out ./dist/offline ./examples/offline
./dist/offline/server -addr 127.0.0.1:3022
```

Open `http://127.0.0.1:3022/` online, choose **Enable offline**, and inspect
**Status of saved public pages**. Wait for a complete saved edition; reload once
if the page is not yet controlled. The sample declares `/`, `/guide` and
`/offline`, with eager Go controls and a separate lazy Go counter. Its
`/api/health` remains network-only. See the [sample guide](../examples/offline/README.md)
for deployment configuration.

Use the exact configured origin and mount. HTTPS is required except on
localhost/loopback; neither request Host nor forwarded headers select the trusted
policy origin. Different hosts, ports and mounts have distinct ownership scopes.
A trusted proxy must preserve the configured public deployment identity.

## Declare public permission

Add native `app/offline.go` with this signature:

```go
//go:build !js

package app

import (
    "time"
    "github.com/Suhaibinator/Next.go/offline"
)

func Offline() offline.Config {
    return offline.Config{
        ID: "public-docs",
        Origin: "https://example.test",
        BasePath: "/reader",
        Semantics: offline.SavedAnonymousPublic,
        Pages: []offline.Page{
            {Path: "/", MaxAge: 24 * time.Hour},
            {Path: "/offline", MaxAge: 24 * time.Hour},
        },
        Fallback: "/offline",
        Updates: offline.WaitForClosedClients,
    }
}
```

`BasePath` must equal the application's mount. Paths are literal, query-free,
app-relative paths; the fallback is one of the declared pages. At most 16 pages
are allowed, each with explicit whole-second retention from one second to seven
days. Every selected route also needs an ordinary active public `Route.Cache`
policy with positive TTL or effective lifetime. This is an application assertion
that the representation is public, not automatic secret classification. Cookie
absence, prefetch permission or a successful HTTP status cannot grant it.

The compiler discovers this provider without executing it during discovery. A
production build supplies `Config.Offline` and its `Config.OfflineBuild` inventory.
`New` owns and verifies the compiler's actual asset bytes and deployment identity.
The generated server then calls `app.PrepareOfflineBundle(ctx)` before accepting
traffic. It prepares once per application process: complete documents, catalog and
worker script become visible together. Failure or cancellation publishes none;
an explicit later call can retry. This is startup capture, not a scheduled refresh
or a request-time cache of arbitrary responses. A new process can create a new
saved generation even for the same runtime.

Custom servers using a compiler-produced inventory must call
`PrepareOfflineBundle` with a bounded startup context before serving. The lower
level `app.PrepareOffline(ctx, config)` remains available for native document
capture; its `PreparedOffline` result alone does not install a worker or supply the
complete hydration inventory. `Policy()` and `Documents()` return owned copies;
`BuildID()`, `ConfigurationID()`, `Identity()` and `Admission()` bind that result.

## Native admission and immutable delivery

Preparation constructs fresh anonymous GETs from trusted policy URLs, retaining
caller cancellation/deadline but discarding credentials and other caller context.
The full application middleware chain executes into a bounded final recorder.
Both committed and final responses must be admitted; request-sensitive access,
redirects, cookies, private/no-store, application `Vary`, authentication challenges,
CSP/nonces, flush, overflow and other unsupported shapes refuse preparation.
Only framework compression/navigation negotiation is omitted for this dedicated
capture. Native application callbacks remain trusted and must cooperate with
cancellation; the framework cannot forcibly stop an ignoring callback.

V1 is production-only. Development/HMR, static export, prerender, draft
configuration, locales, zones, alternate asset prefixes, trailing-slash mode,
rewrites/redirects, early scripts, image optimization (`Config.Images`) and managed
metadata files are refused. Registered static image files can still be delivered
when admitted by the HTML rules and declared inventory.
Selected streamed, request-region, slot/intercepted, API or handler-shadowed pages
are unsupported. Final HTML also passes a restrictive framework markup check:
known inventory references and framework CSS only, with no arbitrary inline
styles, events, iframes, foreign SVG/MathML or refresh redirects. Unsupported
content fails admission rather than being silently removed. Stylesheets use a
conservative pure-CSS lexer subset: `@import`, `url()`, escaped grammar and
unknown functions are rejected rather than discovering further dependencies.

The configured fallback is an **inert document containing only approved escaped
title and description**. It omits the normal application body, layout, head, CSS
and runtime; it is not a body-faithful offline copy. Middleware must leave its
exact approved bytes unchanged.

Managed endpoints live beneath `BasePath`:

- `/_nextgo/offline/catalog` and `/_nextgo/offline/sw.js` publish the current
  catalog and stable worker script URL with revalidation.
- `/_nextgo/offline/documents/<generation>/<document-sha256>` supplies approved
  anonymous document bytes with `no-store` and exact admission/build/configuration
  bindings. `Catalog.PreparationURL(path)` produces this source URL. The worker
  stores those bytes under the original document URL, not the preparation URL.
- `/_nextgo/<runtime>/offline-public/…` contains frozen public dependencies.
  Core, every lazy module, worker WASM, matching SDK runtime, bootstrap, stylesheet
  and declared public files have exact decoded size, MIME and SHA-256 entries.

These reserved services bypass ordinary page middleware; they serve only the
already admitted immutable bundle. The compiler assigns runtime identity before
catalog generation, avoiding a hash cycle. Unknown dependencies or mutable asset
aliases cannot be added to a completed saved edition.

## Go browser controls and native navigation

The eager core exposes `client.OfflineStatus()`,
`client.RefreshOfflineStatus(ctx)`, `client.SubscribeOffline(scope, callback)`,
`client.EnableOffline(ctx)`,
`client.CheckOfflineUpdate(ctx)` and `client.DisableOffline(ctx)`. Run asynchronous
operations from `scope.Go` or another goroutine, not a JavaScript event callback.
`OfflineStatus()` is a pure getter of the last confirmed snapshot.
`RefreshOfflineStatus(ctx)` explicitly queries the owned active and waiting
workers through the same serialized operation admission; it never registers,
updates, repairs bootstrap state, extends a timeout or retries an unconfirmed
result. The sample status button runs it through `Scope.Go` with busy controls.
Subscriptions clean up with their scope; lazy-module controls are unsupported.
A supported secure browser needs Service Workers, Cache Storage, Web Locks and
MessageChannel. Conflicting overlapping service-worker ownership is refused.

`OfflineState` reports enabled/control/saved-document status, current and waiting
generations, phase, errors and `CleanupPending`. A saved page uses **native
full-document navigation**, not SPA envelopes or partial-shell reuse; component
new documents initialize component state from their SSR props. Browser history
restoration, including BFCache, can preserve an existing document. Nondeclared
pages, query URLs, APIs, mutation
POSTs and framework protocol requests stay network-only. They are neither stored
nor queued for replay. Ordinary declared document navigations try the network
once with an internal `cache: "no-store"` fetch that bypasses the browser HTTP
cache while preserving the original credentials, headers and redirect semantics.
Only a transport failure permits a verified saved-document fallback; a live HTTP
error response is not converted into cached success. Received live documents are
not silently persisted as a new edition.

Enable is explicit consent to save the complete inventory. Check-for-update asks
the browser to check the owned registration. The worker does not call
`skipWaiting` or `clients.claim`: a waiting edition activates after all tabs in its
application scope close. Reopen the application to use the new edition. No tab is
forcibly reloaded, and a failed staged update keeps the last good saved edition.

Disable stops saved-document serving and unregisters the exact owner. An already
controlled tab can retain that controller until native reload. Its public cached
payload remains charged while clients can still need it: **disabled does not mean
erased**. Close scoped tabs, then explicitly Enable or Disable during a later
online visit to reconcile retained data. `CleanupPending` exposes this state;
background deletion is not guaranteed. Other applications' storage is untouched.

## Storage and lifetime bounds

Active, previous and staging generations are all charged; shared URLs are charged
again per generation. Defaults are:

| Reservation | Default |
| --- | ---: |
| One decoded asset | 16 MiB |
| One document | 512 KiB |
| One generation | 32 MiB |
| Total across three generations | 96 MiB |

Zero budget fields select defaults. Smaller consistent limits are not enlarged
to fit a file. These are declared payload/metadata reservation bounds, not total
browser storage, process memory/RSS, transient decoding or network guarantees.
Browser eviction and storage failures can make the saved edition unavailable.

The Go ledger reserves the full candidate before writes. Each observation must
match fully consumed decoded bytes; completion requires the entire inventory and
its persisted marker. Activation requires a complete stage and closed-client
checks. A reservation is released only after owned physical deletion settles;
dropping an index or canceling work is insufficient. Interrupted/disabled
payloads still count until reconciliation. Persisted highest-observed wall time
prevents clock rollback from extending retention, but is not an independent
trusted clock.

Service-worker execution and termination remain browser-controlled. There is no
durable background Go scheduling guarantee. Physical retirement of a Go worker
with a pending Go runtime timer has not been qualified; bridge timer cleanup is
not proof of that runtime path. This feature provides no background sync,
authenticated data storage, mutation replay or exactly-once transaction semantics.

## Verification status

Current Linux ARM64 scoped reports cover worker/client unit slices, native and
WASM Accept-Encoding admission, actual production compiler artifacts, sample
preparation and paired A/B builds. The compiler checks include complete eager,
lazy and worker output, byte inventory verification and startup ordering; build
reports alone do not prove installation, offline cold restart or browser update
behavior. Some focused suites overlap and their counts must not be summed.

The final corrected managed browser matrix passed **32 cases**: eight each in
Chromium, Firefox, WebKit and touch emulation, with no failures, skips, retries or
flaky results. It covered DOM-preserving eager/lazy Go interaction, cold saved-page
loading with the server stopped, native links, network-only requests without
replay, an inert 503 for a missing lazy asset, A-to-B waiting/activation after
scoped browsers close, explicit status recovery and deferred disable cleanup.
The cold/warm transport gate is not a claim that the browser HTTP cache was
primed. Final source identity and cleanup accounting are recorded separately in
the qualification report.

All four earlier failed attempts are retained: the first two each recorded two
passes, one cold-restart failure and 29 cases not run; the third recorded 14
passes, one pre-hydration counter-click failure and 17 not run; the fourth
recorded 14 passes, one stale-status-snapshot failure and 17 not run. The explicit
status query fixes a concrete operator control gap. Later inspection found the
new worker active with its persisted owner intact; the original message-port
failure remains unobserved. The final passing matrix does not establish that
initial internal cause or make every prior attempt successful.

The final tracked transport harness also passed 12 combinations against those
latest browser-qualified binaries: both A/B artifacts at root and `/reader`,
using native HTTP plus local buffered and streaming mock-Lambda RuntimeAPI
adapters. Actual catalog, prepared documents, immutable assets, compression and
URL joins were checked. Four Node prechecks and 22 Node cases (16 collector and
six legacy gateway cases) also passed. This execution was Linux ARM64 only; it is
not AMD64, browser-through-Lambda or actual AWS qualification. Remote CI wiring
for this source still needs its own pushed-head result.
There is no full offline/PWA or production-maturity claim. See the
[capability inventory](PARITY.md) for remaining requirements and open release gates.

Useful focused checks include:

```sh
go test -race . ./offline ./offline/worker
go vet . ./offline ./offline/worker
sh scripts/test-wasm.sh
```

These commands are native/WASM checks, not browser installation or deployment
qualification. Browser testing additionally needs Node and Playwright.

To reproduce the production browser matrix from the repository root, first run
`npm ci` and install the Playwright browsers (`npx playwright install`, with the
required operating-system dependencies). Use the matching supported Go SDK.
All framework inputs must already be Git tracked or staged, including new files;
the fixture deliberately does not copy arbitrary untracked source files. Keep
the fresh output directory outside the Git checkout:

```sh
offline_qualification="$(mktemp -d /tmp/nextgo-offline.XXXXXX)"
go build -o "$offline_qualification/nextgo" ./cmd/nextgo
GOCACHE="$(go env GOCACHE)" \
NEXTGO_TEST_NEXTGO_BINARY="$offline_qualification/nextgo" \
node tests/offline-build-fixture.mjs --out "$offline_qualification/build"
NEXTGO_OFFLINE_ARTIFACTS="$offline_qualification/build/index.json" npm run test:offline
```

The shipped builder makes fresh A/B copies and publishes their pinned index only
after both builds and source checks succeed. It never reuses an old output
folder or builds the CLI implicitly. `tests/offline-transports.mjs` is a separate
prebuilt-artifact transport runner; its current executed scope is Linux ARM64.
