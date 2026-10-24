# Build snapshots and regeneration

`nextgo build` renders explicitly opted-in pages and embeds their validated body,
head, and document metadata in the standalone server. The first anonymous request
can use that content without running the page loader. Go islands hydrate the
existing nodes as usual. Navigation uses the same snapshot and envelope protocol.

The default regeneration path is process-local. Explicit shared policies can
use an online authority for build-seed admission, runtime regeneration and
acknowledged cross-instance invalidation. Replication/HA, parallel route
prerendering, and automatic static/dynamic analysis remain unfinished.
See [the complete parity inventory](PARITY.md), especially D06–D08.

## Opt in

Put `prerender.go` beside a `page.go`, in the same Go package:

```go
//go:build !js

package article

import (
    "time"
    nextgo "github.com/Suhaibinator/Next.go"
)

func Prerender() *nextgo.PrerenderPolicy {
    return &nextgo.PrerenderPolicy{
        HostIndependent: true,
        Revalidate: time.Minute,
        Tags: []string{"articles"},
        Fallback: nextgo.FallbackBlocking,
    }
}
```

`HostIndependent: true` is a required declaration: page output, layouts, and
metadata must be public and independent of credentials, headers, runtime host,
and private middleware context. Go permits direct request reads; the framework
cannot prove this contract from arbitrary application code. Never opt in a page
that might serialize private data. Island props and all snapshot content are
public browser data.

`Revalidate` is the fresh duration, measured from generation, not process startup.
Zero makes every retained generation immediately stale. `Expire` defaults to
`cache.Never`: retain the last good generation until eviction or invalidation.
An explicit finite expiration must exceed `Revalidate`. Refresh failures keep
last-good content until the hard deadline; a non-cacheable/private result removes
it. Existing [cache profiles and revalidation](../README.md#configuration-caching-and-server-actions) cover the underlying
process-local semantics.

A route cannot combine `Cache` and `Prerender`. Prerender is currently limited to
ordinary pages; API routes, parallel branches/owners, and interception branches
are rejected. [Parent/composed parameter enumeration](STATIC_PARAMS.md) supports ordinary page ancestry.

## Dynamic paths

Use `staticparams.go` to enumerate build paths. Ancestor generators can supply parent values, and child callbacks read them with `StaticParentParams(ctx)`; static children inherit complete bindings. See [composition rules](STATIC_PARAMS.md). A page-local list still works:

```go
func StaticParams(ctx context.Context) ([]nextgo.Params, error) {
    return []nextgo.Params{{"slug": "first"}}, nil
}
```

`FallbackBlocking` (the default) generates an unlisted path on its first eligible
request and coalesces simultaneous loads. `FallbackNotFound` returns the nearest
custom 404 for paths absent from the enumerated list, including private requests.
The known-path list survives cache eviction and invalidation. A listed path whose
snapshot has been evicted regenerates on request.

The demo includes `/prerender`, `/prerender/first`, `/prerender/new`, and
`/prerender/fixed/unknown`. The last path demonstrates the closed-list 404 policy.

## Build and deployment

```sh
go run ./cmd/nextgo dev ./examples/demo
go run ./cmd/nextgo build --out ./dist/demo ./examples/demo
./dist/demo/server -addr :3000
```

Development renders pages on every request; it enumerates opted-in paths for
closed-list fallback validation. An application without `prerender.go` keeps the
compile-only build path: startup `Configure` is not executed during compilation.
The CLI discovers build opt-in from `prerender.go`; `Configure` may adjust those
policies but should not introduce new prerender routes without that file.
Programmatic servers can use `BuildPrerender`, `PrerenderBuildID` and the private
`Config.PrerenderFS` artifact directly.

Opted-in production builds compile a temporary host
executable, run application `Configure` and opted-in pages through middleware,
then embed the resulting private artifact in the final target executable. This
also works when the final target is cross-compiled for Linux. Build data sources
must be available to the build environment. Builds execute trusted application
code; they may run loaders and contact configured data services.

The probe uses production environment precedence and a synthetic anonymous GET
with no query/body/credentials. It rejects redirects, errors, streaming pages,
private/no-store middleware output, per-request CSP nonces, response mutation,
rewritten route/path mismatches, and lifecycle work registered from the cached
page renderer. It has a bounded deadline; application loaders must honor context
cancellation. Build output must not depend on the provisional `BuildID`.

A private `prerender.json` is embedded separately from public assets. Snapshot
bytes participate in the final build ID. Startup validates the strict manifest
and ID binding before importing it. This is a trusted compiler artifact, not an
untrusted upload format or a cryptographic release signature. Never expose the
private filesystem through an application handler.

Snapshot HTML is frozen after ordinary UI escaping, URL checks, metadata
validation, and layout rendering. Only that generated immutable snapshot uses
an internal HTML replay node; the public UI API still has no implicit raw HTML.
The runtime renders the document shell with its final build and configuration
IDs and matching Go WASM support files.

Changed mount, asset prefix, public settings, locale, or routing policy causes
incompatible seeds to be skipped. Such scopes regenerate on demand and enumerate
paths again for fallback membership. Configured locale scopes get separate
snapshots. Explicit default-locale aliases regenerate for their own mount.

Runtime requests with queries (including bare `?`), cookies, authorization,
bodies, private response policy, draft/nonce/cache bypass, or sensitive rule
conditions bypass shared prerender output. Middleware still runs for every
request. Public on-demand renders use normalized anonymous request headers and
host, retaining the selected locale scope. Applications remain responsible for
using cache bypass when middleware introduces private context data.

## Invalidation and retention

The following process-local import and restart rules apply when `Shared` is
false. Shared routes use the authority protocol described below.

Call `App.RevalidateTag` or `App.RevalidatePath` after a backing write commits,
or use the existing action/cache invalidation APIs. These expire the imported
entry and prevent an obsolete in-flight load from publishing it again. Soft
profile invalidation schedules refresh on a later request. Regeneration uses a
fresh request memo and the existing bounded completion scheduler.

Import occurs once per unused store. An earlier read or invalidation prevents
bootstrap, even if the store is empty. A pre-used custom store skips seed import
with a diagnostic and regenerates on demand. Eviction or invalidation never
reimports the embedded snapshot during that process lifetime.

**Restart limitation:** a new process starts from the original embedded build
snapshot, retaining its original generation age. Runtime updates and invalidation
tombstones are not durable or shared across processes. With default non-expiring
retention, that old snapshot may be served stale while refresh runs after a
restart. Use an appropriate finite `Expire` where that is unacceptable; shared,
durable deployment semantics remain an open release gate.

The artifact is limited to 10,000 pages and 32 MiB of serialized private data;
additional URL/render/metadata budgets apply. Generator quotas span locale scopes; known-path membership is limited to 10,000 paths/16 MiB across all mounts, including default-locale aliases, and is checked before build publication. Runtime re-enumeration shares a 30-second deadline. Cache capacity and byte admission
may retain only a subset. Generated timestamps mean prerendered builds need not
have reproducible IDs, even with identical source code.

Headers `X-Nextgo-Prerender: build|runtime` and `X-Nextgo-Cache: fresh|stale|miss`
describe eligible responses. These are diagnostics; they do not authorize public
CDN caching. Server lifetime responses remain `no-store` unless a separately
configured browser cache profile permits an explicitly bounded reuse window.

Static export publishes captured content without runtime regeneration.

The corresponding capability is described in [Next.js ISR guidance](https://nextjs.org/docs/app/guides/incremental-static-regeneration).
Go APIs and deployment semantics differ; this is not source compatibility. Browser
execution still has the [Go WASM runtime and startup constraints](https://go.dev/wiki/WebAssembly)
documented in the main README.

## Shared build seeds and regeneration

This increment is under implementation and qualification. The following is its
current contract; full shared-prerender end-to-end qualification is pending.

Set `PrerenderPolicy.Shared: true` and configure `Config.SharedCache` to opt an
ordinary public route into the shared authority. Keep the same public-output
declaration and privacy constraints as local prerendering. Do not add a separate
`CachePolicy`: the framework derives the shared runtime policy from `Prerender`.
The CLI supplies the final code/build identity; development still renders on
request. Application routes/loaders remain server-only, and islands hydrate the
stored DOM through the existing Go WASM runtime.

```go
func Prerender() *nextgo.PrerenderPolicy {
    return &nextgo.PrerenderPolicy{
        HostIndependent: true, Shared: true,
        Revalidate: 15 * time.Second, Expire: time.Minute,
        Tags: []string{"articles"}, Fallback: nextgo.FallbackBlocking,
    }
}
```

**The authority must be online during both build and runtime startup.** Before
calling any `StaticParams` generator or rendering a seed, the build reserves an
opaque one-use ticket from the configured authenticated namespace. It embeds
that ticket in the private prerender artifact. Startup consumes it to begin a
bounded import session and offers the snapshots to the authority. Only the
first successful consumer can use a ticket; later app processes use ordinary
shared lookups. New builds reserve new tickets, so a later deployment can seed
the same authority without reopening an old consumed capability.

The backend measures seed age from its **ticket reservation time**, rather
than trusting the builder's wall clock or granting a new freshness window at
startup. Build and deployment time therefore count against `Revalidate` and
`Expire`. An expired seed is not installed. A long deployment may start with
no usable seed and generate on its first request; build snapshots are an
optional cold-start optimization.

Tickets expire after 30 days. The reference backends retain at most 128 pending
tickets (reserving another evicts the oldest), allow 16 active import sessions
and give each session five minutes. An import batch contains at most 128 values
and 1 MiB of serialized value bytes; a session admits at most 10,000 distinct
values and 32 MiB before ordinary capacity/byte admission. Individual snapshots
must also fit the shared codec's 1-MiB actual-JSON limit. Existing retained
generations or active load leases win over a seed; import does not overwrite
them. Limits may leave only part of a build retained. Interrupted sessions do
not survive authority restart, and consumed tickets are never restored as new
permissions.

Acknowledged expiration or stale marking revokes **all outstanding tickets and
sessions in the selected namespace**, even when no current key matches the
selector. This deliberately broad fence prevents an older build process from
publishing an invalidated generation through a future cold start. A zero-match
invalidation is meaningful. An unknown, consumed, expired or revoked ticket
skips seed import and lets runtime loading proceed; transport and protocol
errors fail startup, rather than silently adopting local embedded HTML.

Shared seed keys bind the final build, effective configuration, route pattern
and canonical encoded path. Their host-independent binding differs from the
host-sensitive runtime-only shared-page cache. Body/head/document snapshots
retain the trusted-writer boundary described in
[shared caching](SHARED_CACHE.md#public-output-and-storage-boundaries). Private
artifact format 2 carries shared reservations; local-only artifacts retain
format 1. A seed's snapshot DTO format 2 records build provenance; ordinary
runtime snapshot DTO format 1 remains supported.

Use acknowledged `nextgo.ExpireCache(ctx, selector)` or
`nextgo.MarkCacheStale(ctx, selector, window)` after a backing write commits.
These apply the current application's local invalidation and the configured
shared operation, returning remote errors and allowing a partial outcome.
Legacy `App.RevalidateTag`/`RevalidatePath` and profile variants remain local.
With the optional file authority, published seeds/runtime generations,
reservation and consumption records and invalidation survive acknowledged
local-file recovery. A memory authority loses this state at restart; unknown
tickets deny import safely. Neither backend is an application database, a
durable job queue or a replicated/HA service. Machine power loss, actual cloud
deployment, rollout and operational recovery qualification remain open.

### Run the shared-prerender example

Start the authority with a private generated `NEXTGO_CACHE_TOKEN`, following
[the cache service instructions](SHARED_CACHE.md#run-two-application-instances).
Pass the same endpoint/token and opt-in to **both commands**:

```sh
export NEXTGO_DEMO_SHARED_ENDPOINT=http://127.0.0.1:3050
export NEXTGO_DEMO_SHARED_PRERENDER=true
# NEXTGO_CACHE_TOKEN is already set to the authority's private credential.
go run ./cmd/nextgo build --out ./dist/demo ./examples/demo
./dist/demo/server -addr 127.0.0.1:3060
```

Open `/shared-prerender/alpha` or `/shared-prerender/beta` for enumerated build
paths, and `/shared-prerender/new` for blocking runtime generation. Each page
exposes a public 32-hex generation, renderer process/time, matching title/meta
and two independently hydrated Go counters. Native forms expire its tag or
path through the acknowledged API without additional JavaScript. The default
demo leaves `NEXTGO_DEMO_SHARED_PRERENDER` unset and keeps these routes local,
so an ordinary build needs no cache service. The process-only
`NEXTGO_DEMO_SHARED_PRERENDER_FAIL=true` flag makes that demonstration loader
fail; it is not a request query parameter and must be absent for a successful
build.
