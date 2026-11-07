# Scoped browser async resources

The `client` package has an experimental byte-oriented async resource API for
Go/WASM islands. A resource exposes independent idle, loading, ready, failed and
invalidated states, explicit retry, component cleanup, public in-flight dedup and
bounded public reuse. A native server loader can provide an explicit SSR seed.
This is a scoped capability increment for D13/V07, not Next.js compatibility or
production qualification. There is no React Suspense, implicit reactive fetching,
background revalidation, automatic retry, offline persistence or cross-tab cache.

## An explicitly owned source

Create a store from a live `Scope`, register its source once, then bind a view:

```go
store, err := scope.ResourceStore(client.ResourceStoreOptions{})
if err != nil { return err }
source, err := store.Source(client.ResourceSourceOptions{
    Key: "public:catalog:en:v1", // include every representation dimension
    PublicTTL: 15 * time.Second,
    Load: client.ResourceGET(client.MustURL("/api/catalog"), 64<<10, 15*time.Second),
})
if err != nil { return err }
resource, err := scope.AsyncResource(source, client.AsyncResourceOptions{})
if err != nil { return err }
stop, err := resource.Subscribe(func(snapshot client.ResourceSnapshot) {
    switch snapshot.Phase {
    case client.ResourceLoading:
        status.Text("Loading…")
    case client.ResourceReady:
        // Decode the owned snapshot.Body into your application's DTO.
    case client.ResourceFailed:
        status.Text("Unable to load. Retry explicitly.")
    case client.ResourceInvalidated:
        status.Text("Data changed. Reload explicitly.")
    }
})
if err != nil { return err }
scope.Cleanup(stop)
scope.On(button, "click", func(client.Event) { _ = resource.Load() })
scope.On(retry, "click", func(client.Event) { _ = resource.Retry() })
```

Creation and subscription do not issue requests. `Load` and `Retry` start a
managed goroutine and return promptly, so they are safe in synchronous Go/WASM
event handlers. A newer load cancels the previous consumer; only the current
operation can update that view. Errors remain local to the resource. Subscriber
panics are reported through the framework's component callback guard. Reentrant
notifications coalesce and close the resource after 64 nonconverging turns.

A `ResourceSource` permanently binds one key, loader and public policy. Registering
a duplicate live key in the same store fails even if the caller supplies an equal
function. Reads accept no replacement loader. Explicitly share the same source
pointer between views to share its authority; closing a source removes its
registration, invalidates its consumers and fences its old flights. Different
stores never share values or flights. A source can be consumed only by the scope
that owns its store or one of that scope's descendants, not a sibling component.

`PublicTTL: 0` is the default. It disables stored reuse **and in-flight sharing**,
even for two simultaneous reads of the same registered source. Use that mode for
session-dependent reads. A positive TTL explicitly promises request-independent
public data and permits sharing only within the explicitly owned store. Do not
use it for user/tenant/session-specific values. Keys must cover all relevant
representation dimensions; registration does not infer those dimensions.

The native concurrency-safe `NewResourceStore`, `Source`, `Read`, `AdoptSeed`,
`Invalidate` and `Close` APIs are also available for focused testing and explicit
ownership. Browser stores should be created through `scope.ResourceStore` so
component and document invalidation are connected. Browser `AsyncResource` and
its callbacks belong to the Go/WASM event loop; they are not native concurrent
observable state.

## HTTP and server seeds

`ResourceGET(target, maxBytes, publicTTL)` creates a loader using the existing
browser Fetch bridge. It accepts a same-origin URL without credentials or a
fragment, sends GET with same-origin credentials, disables the browser HTTP
cache, rejects redirects, bounds decoded bytes and accepts only HTTP 2xx.
Cancellation retains the store's physical loader slot until browser fetch/read/
cancel promises settle, using the same physical-settlement bridge as typed
mutation admission. A
private/default source passes zero as `publicTTL`.

Positive public reuse additionally requires a `Cache-Control: public` response
with a valid positive `max-age` covering the requested Go TTL, visible `Age`, and
time spent fetching the complete response. Private, no-store, no-cache,
`Vary: *`, `Vary: Cookie`, `Vary: Authorization` and visible Set-Cookie responses
are rejected. Browser Fetch hides Set-Cookie, so these checks cannot establish
that a response is anonymous: the application's positive public policy remains
an explicit trust boundary. The source TTL and helper TTL must match. Custom
loaders have the same public-data responsibility and must enforce their own
transport/size policy before allocating or returning large application values.
The framework checks the returned value size again before retaining it.

SSR seeds are explicit props, not a document-global data cache. A server-only
loader produces bytes and the normal HTML. Supply the identical key and a
server-computed absolute expiry:

```go
// Native server code; do not put request secrets in component props.
seed := client.ResourceSeed{
    Key: "public:catalog:en:v1",
    Body: encodedPublicDTO,
    ExpiresUnixMilli: time.Now().Add(15*time.Second).UnixMilli(),
}
// Go/WASM mount code; source has the same key and 15-second public TTL.
resource, err := scope.AsyncResource(source,
    client.AsyncResourceOptions{Seed: &props.Seed})
```

Adoption requires a positive public TTL, exact key, bounded bytes, an unexpired
absolute expiry no farther than that TTL into the future, and no prior read or
seed adoption for that source. A store invalidation permanently prevents later
seed adoption in that store. Expired, mismatched or already-consumed seeds are
ignored by `Scope.AsyncResource`; they do not fail hydration or start a request.
Existing server HTML remains useful until an explicit load. The core records a
private origin epoch on each island DOM node when first observed. That metadata
survives delayed lazy mounting and same-node download retries: a store created
later on old-origin DOM cannot adopt a pre-mutation seed and starts invalidated.
A different node delivered by a fresh navigation receives the current epoch, so
its new seed remains adoptable after earlier mutations. The private marker is a decimal epoch recorded once by the document hydrator;
resource code does not refresh it when the same node is retried or rebound. It
contains no payload and disappears with its DOM node. It is implementation
metadata on a node shared by the paired core/module runtime, not a public API, a
server-authentication token or a global retained-node map. `AdoptSeed` itself
returns `ErrResourceSeed` when adoption is refused. Adoption that cannot fit the
store byte budget is refused rather than silently losing the seed. A second
view can adopt the same already-owned public value without issuing a request.

Seed and cache insertion, loader results, consumer returns, `Snapshot`, and each
subscriber delivery copy their byte slices. Mutating one cannot modify another
view or future cached read. Keys own their string backing. This API deliberately
uses bytes rather than caching arbitrary mutable Go object graphs. Returned
application errors are not automatically safe to display; the sample uses generic
failure text. Loader panics become a generic error without panic-value disclosure.

## Invalidation and disposal

Framework action dispatch and settlement invalidate resources along with the
navigation cache, including failed/aborted mutation transports: a transport error
does not prove server rollback. The core forwards an invalidation epoch to loaded
lazy modules. The resource checkpoint raised the module ABI from 3 to 4 for this
mandatory freshness propagation. Those results remain historical. Current ABI 6
retains that freshness rule and [per-anchor link-status forwarding](LINK_STATUS.md),
and adds recognized document revocation before module observer dispatch. This
ABI-6 source increment remains unqualified by the older resource results. Mixed
ABI hosts and modules fail their handshake
before mounting; rebuild and deploy the core, every lazy module and development
entries together from the same framework revision and selected Go SDK. The host
shares the original DOM reference with its module, including the private first-observed
origin marker; JSON snapshots carry the current invalidation epoch. Resource
payloads and Go objects remain in their own WASM runtimes. Unchanged module epochs do not invalidate again. `Refresh`
also applies the document cache fence. `client.InvalidateResources()` explicitly
applies the same fence for application-managed API mutations or authentication
changes; ordinary raw API writes are not automatically detected.

Invalidation clears cached values and each mounted resource snapshot, cancels
active reads, and emits `ResourceInvalidated`. It sends no request. The consuming
view must decide how to replace its previously displayed DOM and when to reload;
clearing snapshot bytes cannot erase application-created DTOs or DOM. The sample
clears both displayed values. A late old loader cannot publish a value after an
invalidation or source-close fence. Retry is always explicit and must be reserved
for reads; this API does not retry mutations.

Scope disposal closes resources before their store, removes subscriptions,
cancels consumers, unregisters the scoped store and suppresses callbacks for
closed ancestor scopes. Store/source close are idempotent. Resource close drops
its snapshot immediately. One consumer's cancellation does not abort another
consumer sharing a public flight. When the final consumer leaves, the loader is
canceled and ceases to be reusable.

Cancellation cannot force a Go callback to return. A canceled/timed-out loader
continues owning a physical in-flight slot until it actually returns. Invalidating
or closing a source cannot free that slot prematurely. Repeated cancellations
therefore reach a bounded admission error instead of spawning unlimited loaders.
A loader that ignores cancellation may retain that capacity indefinitely; recovery
requires its eventual return or replacement of the owner, with the application
still responsible for the uncooperative work. Store timeout releases waiting
consumers even if the loader ignores its context. Neither physical slot admission
nor owned-byte accounting establishes a whole-process heap, browser-memory or RSS
bound.

## Default and configurable limits

| Bound | Default | Configurable maximum |
| --- | ---: | ---: |
| Scoped stores per WASM runtime | 64 | 64 |
| Registered sources per store | 64 | 256 |
| Cached values per store | 32 | 256 |
| Store admission bytes | 1 MiB | 16 MiB |
| Returned value bytes | 256 KiB | 16 MiB |
| Physical loader operations per store | 8 | 32 |
| Waiting consumers per store | 128 | 512 |
| Loader timeout | 10 seconds | 1 minute |
| Public TTL | disabled | 5 minutes |
| Views observing one source | 128 | 128 |
| Subscribers per view | 64 | 64 |

Zero option fields use defaults. Negative or out-of-range options fail
construction. Cache admission accounts for owned key/value lengths plus 128 bytes
per entry as a conservative policy charge; this is not an exact allocator or RSS
measurement. Expired entries are discarded and least-recently-used entries are
evicted to satisfy count/byte limits. A value larger than the cache budget can be
returned uncached if it remains within the returned-value bound. At most 256
lifetime cleanup resources per component provide a further component-level bound.
There is no retry queue, timer per cache entry, retained global payload or
persistent browser storage.

## Example and verification scope

`/resources` renders meaningful HTML from `examples/demo/resourcedata`, adopts
its public SSR seed, and binds two views to one registered source. Controls show
bounded reuse, explicit invalidation followed by one deduplicated HTTP request,
a real HTTP 503 followed by explicit retry, and scope cleanup. `/api/resources`
returns request-independent public JSON; `?fail=1` explicitly requests a no-store
503. The sample's native-only loader observes cancellation and has no session or
authorization input. This is an educational public fixture, not an authenticated
application or a production cache adapter. The page calls `Context.RequireServer()`
and propagates its error because its explicit reload/retry controls depend on the
HTTP API. Native HTML and navigation responses declare
`X-Nextgo-Requires-Server: 1`; plain static export rejects this page instead of
publishing a seeded interface whose later HTTP requests cannot work. The useful
initial SSR HTML does not remove that deployment dependency.

Focused native race tests cover registration authority, private isolation,
copy ownership, seed expiry/fences, shared waiter cancellation, byte/count/waiter
admission, LRU eviction, ignored-cancellation physical slots, timeout and generic
loader failures. Go/WASM tests cover scoped lifecycle/snapshot isolation,
no-implicit-fetch seed adoption, explicit retry, ancestor/store ownership,
invalidation epochs, cancellation and bounded same-origin HTTP policy. Sample
native handler/loader tests cover public GET/HEAD, failure/method responses and
cancellation. These tests do not establish cross-engine browser behavior,
application authorization, whole-process resource stabilization, production
performance, or full D13/V07 parity. Qualification must use the final coordinated
artifact rather than adding unrelated historical suite counts.

The isolated browser fixture is prepared as
`node scripts/test-browser.mjs --config tests/resources.config.mjs`. It runs the
paired production executable under a base path, distinct asset prefix and CSP,
with Chromium/Firefox/WebKit/touch projects. Its bounded HTTP proxy records real
resource requests and can hold an upstream response body for disposal/abort
checks; it does not rewrite or fabricate resource response bytes. Run it only
after the coordinated source freeze and record its actual results separately.
