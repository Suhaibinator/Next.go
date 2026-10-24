# Shared data and public-page caching

`cache/shared` is an experimental, native-only read-through cache for multiple
Go application instances. It adds an explicit backend contract, JSON values,
fenced load leases, acknowledged expiration/stale marking, optional scheduled
stale refresh, an authenticated HTTP adapter and a bounded reference memory
service. It uses the Go standard library.

Explicit serialized data uses `cache/shared.Lookup`. Public rendered pages can
separately opt in through `Config.SharedCache` and `CachePolicy.Shared`.
`Config.Cache` and legacy `App.Revalidate*` methods remain process-local.
`PrerenderPolicy.Shared` separately opts build seeds and runtime regeneration
into the authority protocol. An optional file backend persists shared state for
one local authority; replication/HA, browser/CDN coherence and full durability/production recovery
qualification remain unfinished.

Next.js also exposes custom handlers for externally stored cached data; its
[cache handler contract](https://nextjs.org/docs/app/api-reference/config/next-config-js/cacheHandlers)
is the capability reference, not a compatible wire or source API.

## Run two application instances

Build once, then use the **same token** in three terminals. Generate a fresh
private token, for example with `openssl rand -hex 32`; do not commit it. The
service requires 32–256 visible ASCII bytes, and one service endpoint serves one
configured application/tenant namespace.

```sh
# Terminal 1: replace the placeholder with your generated token.
export NEXTGO_CACHE_TOKEN='<generated-token>'
go run ./cmd/nextgo-cache -addr 127.0.0.1:3050 -namespace demo
```

```sh
# Build from the repository, then run the resulting executable anywhere.
go run ./cmd/nextgo build -out ./dist/demo ./examples/demo

# Terminal 2
export NEXTGO_CACHE_TOKEN='<same-generated-token>'
NEXTGO_DEMO_SHARED_ENDPOINT=http://127.0.0.1:3050 \
  ./dist/demo/server -addr 127.0.0.1:3060

# Terminal 3
export NEXTGO_CACHE_TOKEN='<same-generated-token>'
NEXTGO_DEMO_SHARED_ENDPOINT=http://127.0.0.1:3050 \
  ./dist/demo/server -addr 127.0.0.1:3061
```

Visit `/shared-cache` on both app addresses within 15 seconds. The generation ID,
creation time and generating process agree; the second read normally reports
`fresh`. The ordinary form expires the shared tag and redirects to fresh SSR.
The other process then observes that replacement. The root layout still runs its
Go/WASM island and links still navigate in Go. The shared data package and token
are excluded from the browser graph by a `serveronly` import barrier.

Use `/shared-cache?refresh=background` for scheduled stale refresh. The **Mark
shared generation stale** form clips the last-good deadline to at most another
30 seconds and redirects to that view. Its initial response can show the old ID
with state `stale`; a later read on either app observes the refreshed ID. Native
HTTP runs completion work after handling the response. Lambda drains it after
accepted publication, before requesting another event. The sample's `?fail=1`
mode simulates a transient loader failure: fresh hits still work, scheduled
refresh retains valid last-good data, and hard-expired reads fail until a later
successful load. This is an availability demonstration using public generated
data, not an authorization or transaction policy.

The sample intentionally generates **public random IDs**, rather than pretending
its cache is a database. Restarting an app preserves the service's current data;
restarting the default memory service loses it. An outage returns the framework's
generic server error instead of privately reusing an old process-local value.
Without an endpoint, the route renders setup instructions. Its document and
navigation responses use `private, no-store`.

## Explicit data API

Construct the client in server-only application configuration. The endpoint and
token must come from trusted server configuration, not request fields or public
component props. The HTTP constructor permits HTTPS, plus HTTP for loopback
addresses. A supplied `http.Client` is copied; its transport can configure private
CAs. Redirects and cookie jars are disabled even on a supplied client.

```go
backend, err := shared.NewHTTP(endpoint, token, nil)
if err != nil { return err }
data, err := shared.New(backend, "catalog", "post-json-v1", shared.Options{})
if err != nil { return err }

key := url.Values{"slug": {slug}, "locale": {locale}}.Encode()
post, info, err := shared.Lookup(ctx, data, key, shared.Policy{
    FreshFor: 30 * time.Second,
    ExpireAfter: 5 * time.Minute,
    Tags: []string{"posts"},
    Paths: []string{"/posts/" + slug},
}, func(work context.Context) (Post, error) {
    return repository.LoadPost(work, slug, locale)
})
```

Every hit contacts the authoritative backend. There is no process-local L1 cache
and no invalidation bus to miss. The `(Namespace, Schema, Key)` tuple separates
data without ambiguous string concatenation. Namespace/schema allow 1–128 ASCII
letters, digits, dots, dashes and underscores; keys allow 1–4096 UTF-8 bytes
without NUL. Include **every value-affecting input**, such as tenant, permission
version, user and locale, in the tuple. The API cannot infer these or authorize
reads. Different tenants need appropriate namespace/credential isolation.

Use a new schema for incompatible DTO changes; choose whether a schema is stable
across builds or includes a build identity. JSON serialization is the contract:
arbitrary Go pointer identity, functions, interface concrete types, private fields
and custom object graphs are not preserved. Misses and hits both decode owned
results. The default decoder rejects unknown fields and incompatible types;
duplicate fields, excessive nesting and codec panics fail the read. Custom JSON
codecs remain responsible for their own field semantics. A shared ID with
inconsistent normalized policy returns `ErrPolicy` while its entry or lease
exists. Label order/duplicates and path normalization do not create spurious policy conflicts.

At retention boundaries, the reference memory/file backends own the backing
storage of namespace/schema/key and tag/path strings, as well as stored value
bytes. Store construction also owns its namespace/schema strings; pending-ticket
and active-session namespaces and recovered journal identities are owned.
A short substring cannot retain an unrelated large caller buffer through those
metadata fields. This ownership does not measure or bound callers' other
references, concurrent operations, transient buffers or process heap/RSS.

`FreshFor` is 0–365 days. A finite `ExpireAfter` must be positive, at least
`FreshFor`, and at most 365 days; `shared.Never` disables hard expiry. Equal
positive fresh/expiry windows model an exact TTL with no stale interval. Both
ages start at accepted publication, not application
startup or lease acquisition. Without a scheduler, a stale read **blocks for
fresh data**; a loader failure returns an error rather than stale fallback. With
a scheduler, an unexpired last-good generation can be returned as `stale` while
one fenced refresh is reserved. `cache.Info` reports `miss`, `fresh`, `stale` or
explicit `bypass` with conservative generation ages. Hard expiry always prevents
stale delivery. `cache.WithBypass(ctx)` runs the loader without touching shared
storage, reserving refresh work or converting its result to JSON.

Default bounds are a 5-second lease, a 30-second whole-lookup timeout and 25-ms
polling while another instance loads. Configure `Options` for leases of
100 ms–1 minute, a positive total timeout up to 5 minutes, and polling of
1 ms–1 second. Earlier caller deadlines win. There are no lease renewals or
detached background workers. Cancellation/error cleanup can spend up to an
additional 500 ms on best-effort lease release; a failed release is bounded by
the lease deadline. Timeouts govern cooperative work, not preemptive Go return
guarantees. Loader cancellation is cooperative. Lease expiry,
invalidation or an owner cancel can start replacement work, so loaders must be
read-only/idempotent and honor their supplied context.

## Scheduled stale refresh

Pass at most one final `shared.LoadOptions` value to opt into a bounded
scheduler. For a framework page or handler, use the same completion scheduler as
the local cache:

```go
post, info, err := shared.Lookup(ctx, data, key, policy,
    func(work context.Context) (Post, error) {
        return repository.LoadPost(work, slug, locale)
    }, shared.LoadOptions{
        ScheduleRefresh: nextgo.CacheRefresh(ctx),
        KeepStaleOnError: func(err error) bool {
            return !errors.Is(err, ErrPermanentlyUnavailable)
        },
    })
```

The scheduler is separate from serialized `Policy`: callers can choose blocking
or scheduled reads of the same key without changing its storage identity. Copy
loader inputs before registration; do not retain a response writer, request
body or mutable page/request data. The worker receives the scheduler's context,
not the canceled lookup context. `nextgo.CacheRefresh` supplies a fresh request
Memo scope and uses the application's existing bounded completion queue. Native
HTTP starts work after handling; Lambda waits for accepted response publication
and drains work before `/next`. This does not prove delivery through a client or
proxy, and the queue is neither persistent nor a required-write mechanism.

The first stale reader acquires one remote refresh lease. Other scheduled
readers can receive the same unexpired last-good value while that lease is busy;
blocking readers continue to wait for fresh data. A rejected reservation or
registration panic releases the lease and falls back to a blocking lookup.
Accepted work must run with a deadline. Discarded work releases its reservation;
a job whose lease expires in the queue skips its loader, allowing a later reader
to try again. There are no hidden goroutines or lease renewals. A delayed or
saturated queue therefore does not guarantee that every accepted refresh runs.

A custom `cache.RefreshScheduler` owns its run/finish lifecycle: on successful
registration it must finish once after run returns or after discarding the job,
and must never run after finish. Errors must leave no scheduled work behind.
The refresh wrapper also makes cleanup idempotent on executed work. A missing
worker deadline fails the job without invoking the loader. Caller cancellation
does not cancel accepted lifecycle work, but lease, worker and shutdown
deadlines still apply; Go loaders must honor them cooperatively.

By default, refresh errors retain the last-good generation until its existing
hard deadline. `KeepStaleOnError` may return false to discard it; a predicate
panic also selects discard. Discard is fenced to the failing lease, so a late
error cannot delete a generation another replica has already published. A failed
discard RPC has an unknown remote outcome, and its error is reported with the
refresh failure. The error policy also applies to blocking loads; it does not
turn their errors into stale successes. No error extends a last-good deadline,
and backend read outages still fail closed rather than serving a remembered
local value. These policies do not make stale data appropriate for permission changes: use acknowledged
strict expiration after a committed authorization-sensitive write.

## Invalidation and fencing

After a backing write commits, wait for shared invalidation:

```go
_, err := data.Expire(ctx, shared.Selector{Kind: "tag", Value: "posts"})
if err != nil { return err } // the backing write has already happened
```

Selectors are `key`, `tag`, exact `path`, path `prefix`, or `all` with an empty
value. They apply to **every schema in the store's namespace**, including pending
leases which have not published a value. Prefix matching respects path segments:
`/posts` matches `/posts/a`, not `/postscript`. Supply decoded URL path labels;
normalization removes queries/fragments and cleans slashes. At most 64 tags and
64 paths, each 1–512 UTF-8 bytes without NUL, may be supplied per policy.

For a stale-while-refresh policy, mark the last-good generation instead of
strictly expiring it:

```go
_, err := data.MarkStale(ctx, shared.Selector{Kind: "tag", Value: "posts"},
    30*time.Second)
if err != nil { return err }
```

`MarkStale` preserves generation age, makes matching data immediately stale and
retires pre-mark leases across all schemas. Its finite window must be 0–365
days and caps the old deadline at `min(existing, now+window)`. Repeated marking
cannot extend it. `shared.Never` imposes no extra cap and preserves any existing
finite deadline; zero is strict expiration. A finite mark can give a previously
non-expiring generation a hard deadline. Missing/expired values and unpublished
leases are removed. A successful new publication starts its own normal policy
ages. A stale mark schedules no work itself: a later lookup chooses blocking or
scheduled refresh. Profile-driven callers can explicitly use a resolved
`cache.Profile.Expire` as the window; the API does not implicitly inherit or
propagate profiles through nested loaders.

The backend atomically selects a fresh value, grants a unique bounded lease, or
reports another owner. Only the current unexpired token can publish. Release
and conditional discard match the token and can retire it even after its lease
expires, but they cannot touch a replacement owner or committed generation.
Expiration removes both the stored value and its lease. A blocking loader
finishing with a retired token retries acquisition and cannot deliver its
obsolete result; retired scheduled work cannot publish a result either. Old
tokens remain unusable after eviction and reference-service restart.

Ordinary blocking `Begin` polling omits stale value bytes. `BeginStale`, used
when a scheduler is supplied, includes a valid stale generation while another
lease is busy, so those readers can return it without additional work.
Application clocks need not agree: the backend supplies relative ages/deadlines,
and the client deducts elapsed transport/decoding time. Generations which expire
in transit or during synchronous scheduling are not returned past hard expiry.

Invalidation does not recall an operation already selected by the backend or
cancel arbitrary application code immediately. The old owner's cooperative work
can continue until it returns or reaches its lease/caller deadline; its token
still cannot republish. A failed/canceled HTTP operation may have committed
remotely. Transport calls are not silently retried; repeating `Expire` is safe.
Writes plus invalidation are not one transaction, and this package does not
supply write idempotency, outbox delivery or permission-revocation transactions.
Browser documents, browser navigation caches and CDN copies need their own
invalidation/revalidation policy.

## Shared public-page snapshots

Public pages opt in independently of explicit shared data loaders. Configure a
trusted store once, then set `Shared: true` on selected active page policies:

```go
backend, err := shared.NewHTTP(endpoint, token, nil)
if err != nil { return err }
pages, err := shared.New(backend, "catalog", "page-snapshots-v1", shared.Options{})
if err != nil { return err }
config.SharedCache = pages
if config.CacheProfiles == nil {
    config.CacheProfiles = map[string]cache.Profile{}
}
config.CacheProfiles["public-pages"] = cache.Profile{
    Stale: 0, Revalidate: 30 * time.Second, Expire: 5 * time.Minute,
}
for i := range config.Routes {
    if config.Routes[i].Pattern == "/posts/:slug" {
        config.Routes[i].Cache = &nextgo.CachePolicy{
            Shared: true, Profile: "public-pages", Tags: []string{"posts"},
        }
    }
}
```

Routes without `Shared: true` retain their process-local policy. Shared routes
require `Config.SharedCache` and a positive TTL, valid Lifetime or resolved named
profile. API endpoints and disabled cache policies cannot be combined with shared
page caching. Prerender routes instead declare `PrerenderPolicy.Shared`; do not
combine `Cache` and `Prerender`. Production programmatic applications must
supply a code/release `BuildID` rather than the `development` default, and change
it when the application code changes. The CLI supplies its build identity.
Development still renders pages directly instead of using page-cache entries.

A TTL maps to equal fresh and hard-expiry ages and blocks when expired. Lifetime
and named-profile routes use the bounded completion scheduler for stale refresh;
their hard expiry always requires fresh data. Existing profile browser-reuse
windows remain separate. The sample page profile uses `Stale: 0`, so the Go
browser cache does not conceal a server cache transition.

### Public-output and storage boundaries

A shared page policy is an explicit assertion that its body, layouts, metadata
and serialized island props are public and request-independent except for its
keyed inputs. Middleware still runs for every request. Cookie/Authorization
requests, requests with bodies, draft/nonce/cache-bypass contexts and sensitive
rule conditions retain the existing public-cache bypass behavior. Parallel or
primary-owner routes and streamed output do not enter this snapshot store.
Public snapshot rendering rejects response mutations and nested completion
registration. Application-specific private context/header variation cannot be
automatically inferred. For private middleware data, wrap the request context
with `cache.WithBypass` before page-cache selection.

The snapshot freezes the rendered body, title, description, legacy metadata,
document controls and structured head together. It stores no component/render
closures. Head HTML is regenerated from validated meta/link/inert JSON-LD
descriptors; JSON-LD text is freshly escaped before SSR. Format/binding checks,
UTF-8 and document validation, descriptor allowlists and metadata/head/body
budgets reject malformed stored snapshots. `GeneratedAt` is diagnostic;
authoritative backend ages govern reuse, rather than comparing application
wall clocks.

Framework snapshot retention owns the body/title/description, metadata names
and values, head descriptors/markup and document-control string backing, together
with their maps/slices/containers. Local prerender import applies the same
materialized-page ownership boundary. This prevents a small retained metadata
substring from pinning its entire original request/CMS buffer. It is narrower
than a general deep-copy promise for arbitrary application values; the generic
local cache still shares those values by reference.

**The body is trusted rendered HTML.** The configured authenticated namespace
and all its writers are part of the application's HTML trust boundary, like a
private compiler snapshot artifact. This format has no content signature and
is not an upload format or sanitizer. Keep cache credentials away from
untrusted clients and unrelated/generic data writers. A separate namespace is
required for a separate writer trust domain; a schema name is not authorization.

The actual serialized DTO must fit the shared store's **1-MiB JSON limit**. Its
codec also bounds the body at 4 MiB, individual title/description at 64 KiB,
legacy metadata at 256 entries/256 KiB, and head descriptors at 512 elements and
1 MiB, with existing 16-node/512-KiB aggregate JSON-LD limits. These are admission
bounds, not total renderer/heap limits. A valid fresh render that exceeds a
shared-codec/admission limit is rendered once and served uncached with HTTP 200
and `private, no-store`; it is not rendered again just to bypass caching. An
oversized background replacement uses token-fenced discard of the old cached
generation; a failed remote acknowledgement retains its unknown-outcome
semantics. Invalid data read from the remote store fails closed, as do backend outages; there is no local fallback page cache.

### Identity, host variation and the example

The page key/binding hashes an unambiguous tuple of snapshot format, build,
runtime configuration, route pattern, request host and request URI. Query
strings therefore vary snapshots. Invalid UTF-8 inputs or decoded paths longer
than the shared path-label limit of 512 bytes render uncached instead of failing
a valid URL. Key hashing avoids delimiter collisions and oversized raw URI keys.
Runtime configuration identity includes resolved cache policies, shared scope
identity and layout IDs/bases, keeping incompatible snapshots separate across
mounts, locales, layouts and builds.

`Store.ScopeID()` is an opaque digest of **namespace and schema only**; it does
not encode the endpoint or token. Relocating the same logical store or rotating
credentials does not change that identity. Use a new namespace/schema for an
independent data realm, including a different source behind the same endpoint.

With the two production app processes above, open `/shared-pages` to reach
`/shared-pages/alpha` and `/shared-pages/beta`. The demo uses schema
`demo-pages-v1`, tag `shared-pages`, a fifteen-second fresh window, one-minute
hard expiry and no Go browser reuse. Body, layout, title, social/structured head,
document controls and Go island markup belong to the same generation. Each
browser hydrates its own existing counter nodes and keeps independent client
state. The forms demonstrate tag expiration, path-prefix expiration and a stale
mark using the effective profile's expiry window. Without a shared endpoint,
these routes render normally and explain how to enable caching.

For **page** reuse, replicas must receive the same public request Host. Different
direct ports are separate host variants by design, even if the binary and store
match. Compare direct instances with a common trusted Host:

```sh
curl --fail -H 'Host: public.test' http://127.0.0.1:3060/shared-pages/alpha
curl --fail -H 'Host: public.test' http://127.0.0.1:3061/shared-pages/alpha
```

For browser navigation, place both behind one public-origin reverse proxy that
preserves that Host; do not rewrite arbitrary user headers into trusted cache
variation. The explicit `/shared-cache` **data** example uses its own key and
can agree across different direct ports. The page example's source-failure
switch is server configuration, `NEXTGO_DEMO_SHARED_PAGE_FAIL=true`, rather than
a query parameter. A fresh cache hit can survive that source failure; a failed
refresh can retain the last-good public snapshot until hard expiry. This does
not make the memory backend durable or prove real production recovery.

### Acknowledged application invalidation

After a backing commit, use the context helpers from pages, API handlers,
actions or completion work, or their methods on a constructed `App`:

```go
if err := nextgo.ExpireCache(ctx,
    shared.Selector{Kind: "tag", Value: "posts"}); err != nil {
    return err // backing commit already happened; invalidation may be partial
}

if err := nextgo.MarkCacheStale(ctx,
    shared.Selector{Kind: "prefix", Value: "/posts"}, 30*time.Second); err != nil {
    return err
}
```

`App.ExpireCache` and `App.MarkCacheStale` perform the same operations without
context-based app lookup. Selectors/windows are validated before either store
changes. The current application's local store is changed first, then the
configured shared namespace is acknowledged; local changes remain even when the
remote operation fails. There is no cross-store or backing-database transaction.
Check and handle the returned error. Repeating expiration is safe; repeated
stale marking never extends an existing deadline.

Context helpers require an application scope and reject use from read-only
public snapshot loaders with `ErrPublicCacheResponse`. Literal `key` selectors
refer to actual store keys, not logical URLs; page callers normally use tags,
paths or prefixes. Shared invalidation spans every schema in its configured
namespace, while the local operation affects this application's process-local
store. Independently configured data stores, other namespaces, local caches in
other replicas, browser documents, Go browser reuse and CDN copies still need
their own invalidation policy. Legacy `App.RevalidateTag`, `App.RevalidatePath`
and their profile variants deliberately remain process-local; they do not hide
network I/O or remote failures behind their existing signatures.

Local prerender policies keep their process-local import path. Shared policies
use the online admission protocol below. A memory service does not retain state
across restart; neither backend provides replicated authorities or an
invalidation bus for arbitrary process-local caches.

## Shared prerender admission

This bounded subset is implemented with native, browser and real-process
verification. The [verification scope](#verification-scope) distinguishes current
checks from preceding increments and the remaining production release gates.

An ordinary route can opt into `PrerenderPolicy.Shared: true` with
`Config.SharedCache`. Build must reach the authenticated authority **before any
static-parameter enumeration or page rendering** to reserve a one-use ticket.
The private compiler artifact carries that opaque reservation; startup consumes
it to admit build snapshots, and runtime regeneration uses the same shared store.
Both build and runtime require the endpoint and private credential. There is no
offline seed-adoption mode.

Generation age begins at the backend's reservation time. Startup does not trust
builder timestamps or renew the seed's lifetime. Existing values and live load
leases take precedence, and expired/oversized/capacity-rejected snapshots may
be omitted. An ordinary valid build render that exceeds the narrower shared
codec or 1-MiB actual-JSON admission limits is skipped, and its ticket is still
consumed even when no seed is admitted. Normal runtime rendering can serve that
valid page uncached. Malformed private manifests, build bindings and ticket
syntax still fail startup validation.
Unknown, expired, consumed or revoked tickets skip import and allow
ordinary runtime loading; authority transport/protocol errors fail startup.
This is optional cold-start seeding, not durable application data or a queue.

Tickets expire after 30 days; the bounded reference authority keeps 128 pending
tickets and evicts the oldest at capacity. Consumption opens one of at most 16
five-minute import sessions. Batches allow 128 values/1 MiB; sessions allow
10,000 distinct values/32 MiB, with existing per-value, cache-capacity and actual
JSON admission limits. Sessions are ephemeral and never resume after restart.
Published seed values and acknowledged ticket reservation/consumption/revocation
are retained by the optional file backend. A memory authority loses them at
restart and denies unknown tickets; a new deployment reserves a new ticket.

The ticket/session quotas apply to the entire backend, across namespaces.
Their metadata has a separate global `MaxControlBytes` ledger, defaulting to
8 MiB; it does not consume ordinary entry `Capacity`/`MaxBytes` accounting.
See [seed-control accounting](#seed-control-accounting) for pressure behavior
and diagnostics. The typed helper validates the complete set's keys, policies and
aggregate metadata/encoding budgets before consuming a ticket, then sends
bounded chunks. This is not an all-or-nothing import: earlier chunks may be
retained if a later chunk or cleanup fails. Each offered key is remembered once
within a session, even if expiry or capacity rejects it; retries never refill
it. An unknown/expired session accepts zero values. Consumption also occurs for
empty or oversized-only sets, preventing a later cold start from refilling holes.

Expiration and stale marking revoke every pending ticket and active import
session in the namespace, across schemas, **even if the selector matches zero
published entries**. This broad fence prevents a later process from restoring
a build predating a backing-data invalidation. Use acknowledged
`ExpireCache`/`MarkCacheStale`; legacy process-local revalidation does not contact
the authority. A successful build ticket is a trusted-writer capability; keep
the embedded private artifact and cache service out of public static handlers.

Build reservation failures and runtime import/cleanup failures are returned to
the caller and abort build/startup. They may follow a committed operation with
an unknown acknowledgement or partially published chunks. Restore service or
credentials and retry startup; a consumed ticket then skips import safely.
At the 16-session capacity, finish other imports or wait for their five-minute
expiry. A new online build can reserve a new seed opportunity. Deleting the
authority directory discards the cache and pending permissions, rather than
repairing a failed rollout.

Custom servers can bound startup enumeration and bootstrap network work with
`nextgo.NewContext(ctx, config)`. Its context controls construction only; cancel
it after success and close the application with a fresh bounded `App.Close`
context after HTTP serving ends. `New(config)` keeps default bounded startup,
and generated binaries create a signal context before construction. Cancellation
can have a partially committed/consumed-ticket outcome; retry normal startup.
Session cleanup is best effort with an independent 500-ms deadline and does not
restore the consumed ticket.

Shared prerender keys bind final build/configuration, route pattern and canonical
encoded path independently of the runtime host, as required by
`HostIndependent`. Runtime-only shared pages retain their host-sensitive keys.
The shared build artifact is version 2 and seed snapshot DTO format 2 carries
build provenance; runtime-only snapshot DTO format 1 remains valid. Head
descriptors are still validated and freshly rendered, while body HTML retains
the authenticated-writer trust boundary.

See [the prerender example and complete constraints](PRERENDER.md#shared-build-seeds-and-regeneration).
The demo `/shared-prerender/alpha` and `/shared-prerender/beta` remain local by
default. Set `NEXTGO_DEMO_SHARED_PRERENDER=true`,
`NEXTGO_DEMO_SHARED_ENDPOINT` and `NEXTGO_CACHE_TOKEN` for both build and runtime
to enable shared admission, then use the native tag/path expiration forms.
Replicated authorities, actual-cloud deployment and operational rollout/recovery
qualification remain open; this adds no browser/CDN coherence or arbitrary local
cache invalidation bus.

## Reference service and adapters

`shared.Backend` exposes `Begin`, `BeginStale`, `Commit`, `Release`, `Discard`,
`Expire` and `MarkStale`. Custom storage adapters must preserve the atomic
selection/token/expiry contract, input bounds, owned byte buffers, context
behavior and namespace isolation. Stale marking must fence pre-mark leases;
conditional discard must not delete a replacement generation. An eventually
consistent independent read followed by an unconditional write cannot implement
that contract. Durable databases need atomic conditional publication and durable
invalidation/lease semantics appropriate to their recovery model.

Build a source-independent service executable with
`go build -trimpath -o ./dist/nextgo-cache ./cmd/nextgo-cache`. For a Linux target,
use `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` (or your target architecture) on that
build command. Copy the binary and mount the TLS files separately; Go and Node
are not runtime dependencies. The service's memory is not a persistent volume.

`shared.NewMemory(entries, bytes)` is the bounded reference implementation.
`shared.NewHandler` adds protocol-v3 HTTP with a bearer token, one allowed
namespace and bounded active operations. `cmd/nextgo-cache` runs it with default
1,024 keys (leases included), 64 MiB accounted retained data, 8 MiB seed-control
accounting and 32 active HTTP
operations. Limits are flags; `-help` lists them. Live leases are protected from
admission eviction, and failed admission does not evict unrelated values. Stored
values are at most **1 MiB** before base64 HTTP encoding, envelopes at most
**2 MiB**. Errors contain generic codes, not keys, payloads or credentials.

The command accepts plaintext on loopback. Other bind addresses require
`-tls-cert` and `-tls-key`; its TLS minimum is 1.3. It bounds header/body, idle
and write times and drains on SIGTERM, forcing close after five seconds. HTTP requests
require authentication, POST and JSON, reject browser Origin headers and content
encodings, and receive `private, no-store`. The client rejects redirects,
unexpected status/shape/version/content encoding, oversized bodies and ambiguous
or case-aliased protocol fields. Protocol v3 adds build-ticket admission to the
stale-read, conditional-discard and stale-marking operations introduced in v2.
Protocol-v1/v2 clients and services are incompatible and fail closed. There is
no version negotiation, rolling protocol migration or
released support promise yet. Upgrade app clients and the cache service as a
coordinated change; mixed-version rollout qualification remains open. Secret
rotation and service lifecycle are deployment responsibilities.

Accounting covers key/label/value bytes and conservative containers, **not**
transient encoding buffers, arbitrary loader allocations, Go heap/RSS, sockets or
all incoming application callers. JSON encoding can allocate before its output
limit is checked. Busy blocking readers poll; scheduled stale readers can
return the valid last-good generation instead. No representative load/soak claim
follows. The memory service is one authority, not a durable or highly available cluster.
It can be used by Linux or Lambda workers that can reach its trusted endpoint;
real cloud networking, TLS termination, scaling and recovery remain to qualify.

### Seed-control accounting

The reference memory and file backends bound seed tickets, active import
sessions and once-per-key evidence separately from retained page/data values.
Use the optional memory constructor to choose both ledgers:

```go
memory, err := shared.NewMemoryWithOptions(shared.MemoryOptions{
    Capacity: 1024,
    MaxBytes: 64 << 20,
    MaxControlBytes: 8 << 20,
})
if err != nil { return err }
usage := memory.Usage()
// usage.DataBytes / usage.DataLimit and usage.ControlBytes / usage.ControlLimit
// are separate accounting ledgers, not process memory measurements.
```

`MaxControlBytes: 0` selects `shared.DefaultControlBytes` (8 MiB); explicit
values range from 1 byte to 1 GiB. `NewMemory(entries, bytes)` preserves its
existing signature and uses the default control budget. `FileOptions` exposes
the same `MaxControlBytes` field. The CLI's positive `-control-bytes` flag
applies to both memory and `-data-dir` modes; `-bytes` remains the data ledger.

Control charges are **512 + namespace byte length** per pending ticket,
**768 + namespace byte length** per session, and **96 bytes** per distinct
offered seed key. Sessions retain a domain-separated SHA-256 digest of the
scoped namespace/schema/key identity rather than a copy of its potentially
4-KiB key string. The global ledger is shared by all namespaces; existing
ticket/session/count, batch and session-value limits still apply independently.

Reservation may evict the oldest pending tickets to fit the count/control
budget, but never active sessions. If fitting is impossible it returns
`ErrCapacity`. Starting a session returns `ErrCapacity` without consuming its
ticket when session count or control space is exhausted. Installation checks
the entire batch's additional seen-key charge before changing seen markers,
values or admission evictions; control pressure rejects that batch with
`ErrCapacity`. Previously committed chunks remain, as for other partial imports.
Finish, namespace revocation and expiry pruning release the corresponding
control charges; no hidden expiry worker is started.

Local `(*Memory).Usage()` and `(*File).Usage()` return `Entries`, `DataBytes`,
`DataLimit`, `SeedTickets`, `SeedSessions`, `SeedSeenKeys`, `ControlBytes` and
`ControlLimit`. They report the retained ledger snapshot; expired control
records can remain counted until a backend operation prunes them. Existing
`Stats()` continues to return only retained data keys/leases and accounted data
bytes. These diagnostics add no HTTP operation or protocol change: wire v3 is
unchanged.

Both ledgers are conservative accounting, **not a Go heap, peak-memory or RSS
guarantee**. Concurrent request decoding, input values, serialization buffers,
file-backend candidate clones, copy-on-write seen maps, loader allocations and
GC retention remain separate resources. Size transport concurrency and process
limits accordingly. The control-budget and ownership additions pass the
[repeated qualification matrix](#control-budget-qualification-october-24-2026-utc);
preceding shared-prerender results below remain historical evidence.
Deterministic 4-MiB substring-backing audits cover retained shared memory/file
identities, local keys/labels and framework snapshots. Those pointer checks
verify ownership boundaries; they are not heap/RSS or benchmark evidence.

## Durable local file authority

`shared.OpenFile` is an optional synchronous file-backed implementation of the
same Backend contract. The memory backend remains the default. File storage is
for **one authority on a trusted private Linux/macOS local filesystem**, reached
by app instances through the existing authenticated HTTP adapter. It is not an
active-active database, replicated service, HA failover scheme or NFS/network
filesystem adapter. Real power-loss, filesystem and cloud qualification remain
open release work.

```go
disk, err := shared.OpenFile("/absolute/private/cache-dir", shared.FileOptions{
    Capacity: 1024,
    MaxBytes: 64 << 20,
    MaxControlBytes: 8 << 20,
    MaxDiskBytes: 256 << 20,
})
if err != nil { return err }
defer disk.Close() // after serving requests and draining completion work
handler, err := shared.NewHandler(shared.HandlerOptions{
    Backend: disk, Namespace: "catalog", Token: token,
})
if err != nil { return err }
// Serve handler through your private HTTP/TLS server.
```

The command exposes this mode without application changes:

```sh
# NEXTGO_CACHE_TOKEN must already contain the private service credential.
go run ./cmd/nextgo-cache -namespace demo -data-dir /absolute/private/cache-dir
```

`-data-dir` selects file storage; `-disk-bytes` optionally sets its active-journal
budget. `-entries` and `-bytes` continue to bound resident keys and accounted
data memory; `-control-bytes` separately bounds seed-control accounting. Leaving
`-data-dir` empty selects memory. Keep the backend open until all
its callers and completion work are drained, then call `Close`; the CLI owns
that lifecycle. Serverless app workers should remain HTTP clients of the
persistent authority. A separate `/tmp` directory in each Lambda worker does
not provide shared persistence across cold starts.

### Ownership, locking and retained state

The directory must be owned by the service user, mode **0700**, and not a
symlink. WAL/staging files must be owned single-link regular **0600** files.
The stable `cache.lock` file is an owned, empty, single-link regular **0600**
file. A nonblocking exclusive kernel `flock` is held until `Close` or process
exit; another authority cannot open the same store concurrently. The lock
inode is separate from the replaceable WAL, is close-on-exec, and is never
unlinked by the backend. Do not delete or rename it while an authority is
running: a new lock inode would no longer coordinate with the old holder.
Unsupported operating systems return a clear unsupported-backend error.

Published values, generation identities, original freshness/expiry deadlines,
stale marks, evictions and acknowledged expiration are journaled. Entries keep
their original age across reopening; downtime does not grant a new fresh
window. **Load/refresh leases and queued callbacks are not restored.** New
leases use new tokens, so a pre-restart owner cannot publish into recovered
state. Restored values still belong to their namespace/schema/build/configuration
keys. Shared prerender admission persists pending tickets and their consumption;
only published seed values persist, not active import sessions. WAL format 2
is written on the first seed mutation; an existing format-1 journal can be
upgraded, while older executables cannot read the new seed-bearing format.
The migration compacts atomically under the same private lock and disk bounds.
It does not downgrade when tickets are consumed or expire. To roll back to an
older executable, keep the newer authority serving compatible clients or
explicitly discard/recompute the whole cache under an operator-approved
recovery procedure; do not replace the WAL with an older backup. Wire v3 also
requires a coordinated client/service upgrade; old clients fail closed.

Mutations are journaled and synchronized before successful acknowledgement and
before adopting their candidate in-memory state. Frames have bounds/checksums;
transactions have sequence, count and digest checks. The backend requires
storage that honors durable file/directory sync and atomic same-directory
rename, plus a nondecreasing wall clock. Detected backward time or an uncertain
write/sync/rename outcome makes the open instance unavailable rather than
silently serving an old in-memory fallback. Close it and reopen only through
normal recovery. Context cancellation can leave an unknown remote outcome;
repeat acknowledged expiration when required. Kernel storage calls are not
preemptible Go work and can outlive context or HTTP-drain budgets.

### Bounds, compaction and failure recovery

Zero `FileOptions` fields select 1,024 resident keys, 64 MiB accounted data,
8 MiB seed-control accounting and 256 MiB **active WAL**. Allowed explicit limits
are 1–1,000,000 keys, 1 byte–1 TiB accounted data, 1 byte–1 GiB seed control,
and 4 KiB–1 TiB active WAL. These are not heap/RSS,
transient serialization or whole-filesystem quotas. Reopening with reduced
limits can reject an existing journal; it does not silently discard history
until it fits. File writes and resident-state work are synchronous; no
throughput or load/soak claim follows.

Pending tickets are durable and replay against the configured control budget;
active sessions and their seen-key maps remain ephemeral. Reopening with a
smaller control limit can fail closed during journal replay rather than evicting
durable ticket history to fit. Restore a sufficient configured budget and retry;
do not truncate or replace the journal to force admission. The ledger adds no
WAL format change to the existing seed-bearing v2 journal.

Atomic compaction can temporarily retain both the old journal and its replacement:
peak journal space can reach **twice `MaxDiskBytes`**, plus fixed lock/directory
metadata. It uses one fixed staging path, `cache.next`, then adopts the new
`cache.wal` after synchronization and rename. An orphan staging file is removed
only under the exclusive lock after valid primary-WAL recovery. It is never an
automatic fallback source.

**Incomplete/corrupt WAL startup fails closed.** Recovery does not truncate an
unfinished append, skip a damaged frame, fall back to a prior generation or
restore an old backup automatically. An interrupted append can therefore make
the service unavailable even when earlier acknowledged transactions exist.
An existing initialization lock without `cache.wal` is also rejected. Operators
may need to stop every authority, retain the failed directory for diagnosis,
explicitly discard the **whole** cache directory and recompute from the backing
source. Do not delete individual marker/journal files to bypass recovery. This
is an availability limitation, not automatic repair or durable-queue behavior.

Checksums and transaction validation detect malformed data, **not rollback to a
whole valid WAL prefix or an older whole store**. There is no independent
anti-rollback witness or content signature. Trusted operators/storage must not
restore acknowledged history to an older state. Acknowledged persistence relies
on the filesystem honoring its synchronization contract; local process-exit
checks cannot prove arbitrary media, power-loss, restore or adversarial-storage
behavior.

### Container packaging

The Dockerfile provides independent `cache-build`, `cache-artifact` and
`cache-runtime` targets. The cache build does not require an application/WASM
build. `cache-artifact` exports `nextgo-cache`; `cache-runtime` runs it as
UID/GID **65532** with a private **0700** `/data` volume and file storage enabled.
Use a named/persistent volume at `/data` to retain the authority's state across
container replacement, with ownership/mode matching that user. An anonymous
volume or container-local filesystem is not a persistence policy.

```sh
docker build --target cache-runtime -t nextgo-cache:local .
docker volume create nextgo-cache-data
```

Mount that volume at `/data`, pass the private `NEXTGO_CACHE_TOKEN` through the
runtime environment, and keep one live authority. The runtime's default listener
is loopback inside the container. Publishing it to another host/container
requires a non-loopback bind plus mounted TLS certificate/key flags, as enforced
by the command; the service UID must be able to read those secrets. This local
storage server does not need an outbound CA bundle. Named volumes, TLS,
permissions, backups, resource limits and recovery remain deployment
responsibilities; this image does not add replication or HA.

## Verification scope

### Control-budget qualification (October 24, 2026 UTC)

The final frozen control-budget/retained-ownership source passes full native
race tests and vet on Go 1.27.3/macOS arm64 and Go 1.24.13/Linux arm64, matching
core/lazy-module WASM builds/vet and generation checks. Normal demo, workspace
and standalone cache production builds pass. The complete host and nonroot
read-only Linux HTTP, both buffered/streaming **mock** Lambda, durable authority
and shared-seed executable suites pass.

Seven independent control-budget tests cover ledger pressure and recovery.
Retained-ownership checks cover shared memory/file identities, local cache
keys/labels and framework snapshots; CLI flag/reopen tests and core copy-on-write/
scoped-hash checks also pass. Deterministic substring-backing checks verify that
short retained fields do not alias their 4-MiB source buffers; they do not
establish a heap or RSS limit.

The repeated browser matrix passes **341 checks / 3 existing Chromium-only
protocol skips on other engines**: 241 production, 36 shared pages, 28 shared
data and 36 online-build shared-prerender checks. These are the completed local
host/Linux runs for the current frozen source, counted once rather than added
to the identical preceding matrix. Logs are retained under
`.nextgo-test-results/qualification/control-budget/`. Files named
`*ledger-initial*` precede the retained-backing fixes and are excluded from final
evidence.

The final [lease-bookkeeping observation](PERFORMANCE.md#seed-ledger-lease-bookkeeping-observation-october-24-2026-utc)
records 2,997 B/op and 27 allocations/op in all nine samples across 0/1,000/
10,000 retained seed hashes. No journal writes occur in its timed loop; it is
not storage/HTTP throughput, production load, heap/RSS or speed-comparison
evidence. Both reference WASM size budgets still fail.

This verifies the implemented subset. It does not certify the entire remote
CI workflow or an immutable release. All 79 capabilities remain tracked and
all 11 [release gates](PARITY.md) remain open, including actual AWS, power-loss/
media recovery, replicated/HA operation, operational rollout, representative
load/soak, security and performance qualification.

### Preceding shared-prerender qualification (October 24, 2026 UTC)

The frozen Go source passes full native race tests and vet on Go 1.27.3/macOS
arm64 and Go 1.24.13/Linux arm64, matching core/lazy-module WASM builds and vet,
generation checks and both standalone production app/cache builds. Full host
and nonroot, read-only Linux HTTP executable suites pass, together with buffered
and streaming **mock** Lambda Runtime API suites. Real file-authority tests
exercise seed admission/recovery and cancellation; a live-work latch proves
the mock Lambda worker does not poll `/next` while confirmed completion work
remains blocked.

Root integration coverage includes 17 tests and 24 invalidation variants;
backend seed audit coverage includes six tests, plus transport and adversarial
batch tests. These cover online reservation before generation, authority-owned
age, ticket consumption/revocation, repeated deployments, rejected/partial
admission, deadline/cancellation behavior and protocol failures. The online-build
browser fixture passes **36 checks: 9 host Chromium and 27 Linux Firefox,
WebKit and touch WebKit**. It builds with a live authority before starting the
app and verifies meaningful build HTML, DOM-preserving Go hydration, independent
counters, navigation, native invalidation and reload behavior.

The same frozen source also passes the existing **241 production + 36 shared-page
+ 28 shared-data checks**. The combined result is **341 passes / 3 existing
Chromium-only protocol skips on other engines**, counting each final suite once.
Playwright WebKit/touch coverage is not certification of every Safari/device.

Logs are retained under
`.nextgo-test-results/qualification/shared-prerender/`. Earlier attempts using
unmigrated protocol-v2 fixtures remain in `*unmigrated-fixture.log`; the corrected
v3 fixture and observers were rerun successfully. Browser container wrappers
now use image-owned source/dependencies and the independent cache artifact,
avoiding a host bind mount's invalid VCS metadata. The final results above are
the completed runs after those fixture corrections.

These are working-tree checks of the bounded implemented subset. They do not
establish actual AWS/IAM/cloud cold starts, physical machine power-loss or media
recovery, replication/HA, network partitions, immutable release rollout,
representative load/soak, performance or independent security qualification.
All 11 [release gates](PARITY.md) remain open. The latest
[warm-local performance observation](PERFORMANCE.md#shared-prerender-observation-october-24-2026-utc)
also leaves both WASM size budgets exceeded.

### Preceding durable-file qualification (October 24, 2026 UTC)

The durable-file increment passes full native race/vet on Go 1.27.3 macOS and
Go 1.24.13 Linux, matching core/module WASM checks and generation checks, both
standalone app builds, and complete host/Linux HTTP plus buffered/streaming mock
Lambda executable suites. Real cache-process tests kill and reopen the service
after acknowledgments, reject old leases/concurrent writers, retain capped
original deadlines, refuse corrupt/missing journals, and preserve a complete
rendered page/head until acknowledged invalidation survives another restart.
The nonroot Linux fixture keeps app/root files read-only and exposes only a
private bounded cache tmpfs. The independent cache runtime also opens its data
volume under UID65532 and exits cleanly on SIGTERM.

The same frozen Go source passes **305 browser checks**, with three existing
Chromium-only protocol skips on other engines: 241 production regressions,
36 enabled shared-page and 28 enabled shared-data checks across Chromium,
Firefox, WebKit and touch. The browser cache fixtures retain their reference
memory service; durable service composition is verified by the real-process
HTTP tests. These counts do not establish power-loss/media/HA/network-partition,
actual AWS, load/soak or immutable-release qualification. An initial unfrozen
Go 1.24 native run observed changing source inputs and was repeated after the
freeze; final logs are retained under
`.nextgo-test-results/qualification/durable-cache/`.


The focused package tests cover cross-client load coalescing, typed JSON
ownership, schema and tenant isolation, matching pending/published invalidation,
late fenced publication/release, age/hard-expiry/never behavior, cancellation,
loader/codec failure, admission/eviction, outage/recovery, binary/body bounds,
protocol corruption and redirect credential protection. Compiler checks reject
importing this package into WASM. The service tests include configuration,
graceful shutdown, a trusted TLS 1.3 handshake and rejection of TLS 1.2 and untrusted certificates.

Deployment tests run the generated app from `/`, use two native HTTP processes
against an external-to-app service, check restart/outage/invalidation behavior,
and run buffered and streaming **mock** Lambda workers against the same HTTP
protocol. Qualification results for this working tree are recorded in
[PARITY.md](PARITY.md); they are not immutable release, real-cloud, durability,
multi-region consistency, performance or independent security evidence.

Reproduce native and enabled-browser checks:

```sh
go test -race ./cache/shared ./cmd/nextgo-cache
npm run test:shared-cache -- --project=shared-chromium
sh scripts/test-shared-cache-container.sh
```

The separate Playwright fixture owns a cache-service process and two app
processes on loopback ports 4146–4148. It checks delayed-WASM initial HTML and
node identity, shared generations, native form invalidation, document-preserving
Go navigation/history, server-only network access and no-JS progressive forms.
The container command builds actual Linux binaries with Go 1.24 and runs
Firefox, WebKit and touch WebKit. The CI workflow includes both checks. This
fixture's token is an explicitly public test credential, not a deployment secret.

### Preceding shared-data qualification (October 24, 2026 UTC)

Before the scheduled-refresh/protocol-v2 extension, that source state passed
full `go test -race ./...` and `go vet ./...` on
Go 1.27.3/macOS arm64 and Go 1.24.13/Linux arm64; matching core/module WASM tests
and browser vet passed on both. Generation checks passed on both formatters. The
TLS command tests passed on both native SDKs. Both app production builds succeeded.

The production browser regression suite passed **61 Chromium + 180 Linux
Firefox/WebKit/touch checks**, with three existing Chromium-only protocol checks
skipped in those Linux engine projects. The enabled shared-cache fixture passed
**3 Chromium + 9 Linux engine checks**, giving **253 passes / 3 skips** for that
preceding increment. Complete host and non-root/read-only/network-isolated Linux
executable suites passed for standalone HTTP and buffered/streaming mock Lambda. The shared
backend in those executable tests lives outside the app worker, in the harness;
the enabled browser fixture also runs the actual standalone cache-service binary.
Service outage/recovery evidence injects HTTP 503; it is not a full network
partition, durable-service crash or real-cloud recovery qualification.

Final logs are retained locally under
`.nextgo-test-results/qualification/shared-cache/`: `native-final.log`,
`go124-native-final.log`, `wasm-final.log`, `go124-wasm-final.log`,
`build-final.log`, `host-browser-final.log`, `host-shared-browser-final.log`,
`linux-browser-final.log`, `linux-shared-browser-final.log`,
`deployment-final.log` and `linux-deployment-final.log`. Earlier completed runs
are separate files; counts above do not add repeated passes together. The final
TLS test was added before the repeated full native/browser/deployment matrix;
no Go source or tests changed during that final matrix. That matrix predates
scheduled refresh and protocol v2; it is retained as historical working-tree
evidence, not current extension or immutable release qualification. Refreshed
results belong in the parity inventory after its final source freeze and checks.
All 79 capability rows and 11 open release gates remain in that inventory.


### Preceding scheduled-refresh qualification (October 24, 2026 UTC)

Before shared public-page snapshots, the v2 scheduled-refresh source passed
full native race/vet on Go 1.27.3/macOS arm64 and
Go 1.24.13/Linux arm64, matching core/module WASM tests and vet, both generated
route checks and both production builds. An independent agent pass added eight
focused regressions and identified two corrected issues: a known-canceled work
context could still enter a loader, and near-MaxInt64 reported ages could wrap
negative. Saturation keeps browser reuse budgets conservative. Lost commit
acknowledgements cannot discard a remotely committed replacement; discard
transport failures preserve joined error evidence and remain lease-bounded.

Enabled browser checks pass **7 Chromium + 21 Linux Firefox/WebKit/touch cases**.
They cover delayed SSR identity, native expiration, authoritative observation of
scheduled publication, failure retention, hard-expired failure/recovery, Go
navigation/history and no-JS stale-mark forms. A blocking app read cannot mask a
broken refresh worker in the publication assertions. The broader production
regression suite passes **61 Chromium + 180 Linux cases**, with the same three
existing Chromium-only protocol skips: **269 passes / 3 skips** for this increment.

Complete standalone HTTP and both mock Lambda executable suites pass on the
host and in non-root, read-only, network-isolated Linux. The new ten-event Lambda
flow guards the backend against publication before `/response` is accepted,
checks the old busy generation before that acknowledgement, and requires the
new authoritative generation before issuing `/next`. It also verifies strict
expiration, transient failure retention/release, blocking regeneration, hard
expiry and recovery. This demonstrates the local adapter lifecycle; it does not
qualify actual AWS freeze, VPC/TLS networking or durable-service failure recovery.

Logs live in `.nextgo-test-results/qualification/shared-refresh/`: `native.log`,
`go124-native.log`, `wasm.log`, `go124-wasm.log`, `build.log`,
`deployment-focused.log`, `deployment.log`, `linux-deployment.log`,
`host-shared-browser.log`, `linux-shared-browser.log`, `host-browser.log` and
`linux-browser.log`. Source and Go tests were frozen before the coordinated
matrix; repeated/preceding phases are not added to these pass counts. Simulated
source failures intentionally log callback/render errors while their recovery
assertions pass. These working-tree results do not close any production release
gate or establish full Next.js capability parity.


### Preceding shared-page qualification (October 24, 2026 UTC)

The frozen shared-page source passes full native race/vet on
Go 1.27.3/macOS arm64 and Go 1.24.13/Linux arm64, matching core/module WASM tests
and browser vet, generated route checks, and demo/workspace production builds.
Twenty-three native shared-page integration functions and the codec tests cover
cross-App reuse, metadata equivalence, scope/config/build/host/URI isolation,
privacy bypass, exact TTL/profile refresh, local/shared invalidation and partial
failure, late refresh fencing, malformed stored bindings/head, admission limits
and HEAD-first representation handling. Cached loaders evaluate GET while the
outer HEAD response still suppresses the body; the original request is unchanged.

Browser qualification passes **9 Chromium + 27 Linux Firefox/WebKit/touch** new
shared-page cases. A stable-origin gateway selects separate normal/faulting app
processes while preserving the same public Host and removing its test-only
selector header. Counter state remains independent between documents; initial
nodes survive hydration, navigation resets/disposes page islands, and metadata
matches the selected body generation. Recorded opaque cache IDs let publication
probes observe the backend without a blocking app read repairing a broken worker.
Private Cookie/Authorization/body requests never touch shared storage. Native
forms, path-vs-group invalidation, stale/failure/hard-expiry recovery and no-JS
behavior pass. The existing shared-data suite adds **7 + 21** passes; production
regressions add **61 + 180**, with three existing Chromium-only protocol skips:
**305 passes / 3 skips** for this increment, without adding repeated runs.

Complete generated executable suites pass on the host and in non-root,
read-only, network-isolated Linux. The new HTTP suite compares stored DTOs with
full-document and navigation HTML/head/document output, tests same-Host replicas,
private bypass, acknowledged invalidation/failure, outage/recovery, app restart
and configuration variation. Both mock Lambda modes run healthy and warmed
faulting-worker flows, with backend guards against refresh commits before stale
response acceptance and authoritative checks before the next event. These are
local/mock tests, not actual AWS or durable-store recovery results.

Logs are under `.nextgo-test-results/qualification/shared-pages/`: `native.log`,
`go124-native.log`, `wasm.log`, `go124-wasm.log`, `build.log`,
`deployment-focused.log`, `deployment.log`, `linux-deployment.log`,
`host-pages-browser.log`, `linux-pages-browser.log`, `host-shared-browser.log`,
`linux-shared-browser.log`, `host-browser.log` and `linux-browser.log`.
All Go source/tests were frozen for that coordinated matrix. Deliberate faulting
replica diagnostics are expected and accompanied by passing retention/recovery
assertions. The core bundle remains over its reference size budgets. All
79 capability rows and 11 open release gates remain in the parity inventory.
