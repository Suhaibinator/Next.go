# Explicit data caching

`cache` is a process-local data cache with bounded LRU storage, expiration,
concurrent load deduplication, and explicit key, tag, and path invalidation.
It uses only the Go standard library.

For serialized data shared by separate application processes, see the
experimental [`cache/shared` API](../docs/SHARED_CACHE.md). The local APIs on this
page retain their existing process-local semantics.

For server fresh/stale/hard-expiry windows, use `Lifetime` instead of `TTL`:

```go
value, err := cache.GetOrLoad(c.Context, postsCache, "public-posts:v1", cache.Options{
    Lifetime: &cache.Lifetime{Revalidate: time.Minute, Expire: time.Hour},
    ScheduleRefresh: nextgo.CacheRefresh(c.Context),
    Tags: []string{"posts"}, Paths: []string{"/posts"},
}, func(ctx context.Context) ([]Post, error) { return databasePosts(ctx) })
```

`Lifetime` specifies server ages from successful load completion, with
`0 <= Revalidate < Expire`. Before Revalidate, reads reuse fresh data. Between
Revalidate and Expire, reads can return the last good value while reserving one
refresh. At Expire, reads wait for fresh data or an error; they never use the
hard-expired fallback. Successful refresh resets both ages. Failed refresh does
not extend them. Zero Revalidate means every retained read can trigger refresh.
These checks happen at lookup, not when a remote client finishes receiving data.

`nextgo.CacheRefresh` uses the application's bounded `After` lifecycle: native
HTTP starts after the handler, Lambda waits for accepted publication and drains
before `/next`. Refresh gets a fresh Memo scope, worker/queue/deadline limits,
shutdown cancellation and type-only failure logging. A discarded, timed-out or
rejected job releases its reservation so later reads can retry. A hard-expiry
reader joins an already running refresh; canceling that reader alone does not
cancel scheduler-owned work. Strict expiration also cancels background loads.

Without a scheduler, or when reservation fails, stale reads refresh synchronously
with ordinary load coalescing and caller cancellation. A failed synchronous read
returns its error. A previous good entry can remain until hard expiry for a later
attempt; errors themselves are never cached. `KeepStaleOnError` can reject retaining
that entry after a permanent or authorization failure. Its callback runs outside
cache locks; a panic discards the entry. Do not put side effects in this policy.

A custom `RefreshScheduler` must bound work, supply a deadline, and call its
cleanup once after execution or discard, including queue timeout/shutdown. The
Store creates no background worker pool. Scheduled plans retain key/generation
tokens rather than an evicted old value; work/loader closures still count toward
your scheduler and application resource limits. Shared public-page loaders cannot
reserve nested `After` refreshes; they use synchronous data refresh while the
containing page's lifetime controls its snapshot. With a load-concurrency limit,
do not nest calls that can require a load or synchronous refresh in the same Store.

For public pages, assign `CachePolicy.Lifetime`; the framework supplies the
scheduler and freezes body, head and document settings together. Transient refresh
failures retain that snapshot until hard expiry. Known 404/401/403 and public-cache
response-control violations discard it; authorization/request-specific pages
must continue to bypass public caching. Use strict expiration after permission or
data changes that must become visible immediately. Development bypasses page caching.
The framework freezes request headers/URL and clones parameters/query values
for every snapshot load/retry, so middleware changes after the response cannot
alter scheduled input. Requests carrying bodies bypass both the server snapshot
and public reuse headers. Cookie/authorization/response-cookie bypasses likewise
clear cache reuse headers even when global application headers requested caching.
For application data loaders, copy immutable identity/parameters before scheduling;
do not retain a request body, ResponseWriter or other mutable request state.

Standalone `CachePolicy.Lifetime` responses use `Cache-Control: no-store`, with
no Go prefetch reuse. Named profiles below explicitly add browser reuse. The
`/cache` example demonstrates server-only eventual source updates and separately
published actions with immediate strict expiration.

## Named profiles and browser freshness

```go
config.CacheProfiles = map[string]cache.Profile{
    "catalog": {Stale: 30*time.Second, Revalidate: time.Minute, Expire: time.Hour},
}
// Find the intended generated route in Configure, then explicitly opt in:
config.Routes[i].Cache = &nextgo.CachePolicy{Profile: "catalog", Tags: []string{"catalog"}}
```

Choose one of `TTL`, `Lifetime`, `Profile` or inline `ProfileSpec`. `Profile.Stale` is the maximum Go
navigation-cache age of the selected page generation, bounded to 24 hours;
zero disables browser reuse. `Revalidate`/`Expire` are the server ages
above; `Expire: cache.Never` disables the hard deadline. The advertised window is limited by both generation age and remaining
hard expiry; reusing an old server snapshot does not give it a new browser age.
Network/validation latency is deducted conservatively, and cache admission uses
the fetch's original deadline. Some or all of a short window may therefore be
unavailable by the time its response arrives.

`cache.BuiltinProfiles()` returns owned values for explicit `default`, `seconds`,
`minutes`, `hours`, `days`, `weeks` and `max` profiles. Default uses 15m/Never; the
other revalidate/expire pairs are
1s/1m, 1m/1h, 1h/1d, 1d/1w, 1w/30d and 30d/365d. Browser ages are 30s for
seconds and 5m for the others. All can be overridden by `Config.CacheProfiles`;
custom names use 1–64 ASCII letters/digits/hyphens/underscores, and at most 256
configured entries are accepted. There is no implicitly cached route.
The registry and route policies are copied at `New`; configure them before startup.

`nextgo.LookupCacheProfile(ctx, name)` returns the effective owned value,
including overrides, in application requests, completion work and `StaticPaths`
enumeration. Use its `Lifetime()` with data-cache options. Outside an application
context it returns `ErrCacheProfileScope`; unknown names return an error. Data
keys/policies remain the caller's responsibility: this lookup does not automatically
namespace data keys or propagate inner cache lifetimes to an outer page.

Reusable profile pages send `X-Nextgo-Prefetch: public`, a remaining
`X-Nextgo-Stale-Millis` hint, and `Cache-Control: public, max-age=0, must-revalidate`.
Thus intermediaries must revalidate while Go may reuse within the explicit window.
The Go cache still rejects no-store/private/no-cache, response cookies, wildcard
variation, malformed hints, unsupported envelopes and wrong origins/builds/
configurations. Hints are a trusted server public-data assertion, not a signature.
Cookie/auth/body/development/error/stream bypasses do not advertise reuse.
Legacy TTL responses retain their shorter, at-most-30s HTTP-based policy;
whole-second Age rounds up rather than extending a generation's window.

The browser cache still holds at most 32 entries and 8 MiB accounted response
payload. Refresh clears it. Go `client.Fetch` POSTs to the current app's framework
action path clear it on dispatch and settlement, including transport/HTTP failures,
and cancel pending prefetches so intervening results cannot repopulate it. Other
origins, mounts, methods and ordinary API URLs do not trigger this behavior.
Changing identity through other APIs still requires Refresh. This does not refresh
the displayed page automatically, undo writes, invalidate server stores without
revalidation labels, broadcast across tabs/instances, or resolve unknown late
commits after cancellation. Use idempotency/transaction and recovery policies for
real mutations. The `/profiles` demo separates mutation from page refresh.

Effective route cache policies/labels contribute an opaque digest to the runtime
configuration identity and public snapshot namespace. Changed policies cannot
reuse another instance's stored page generation. Cached browser responses can
still survive until their chosen window or Refresh without a server check;
choose windows compatible with your update/old-asset retention policy.

`cache.Lookup`/`Store.Lookup` return a value with generation-consistent `Info`
(`State`, `Age`, `FreshFor`, `ExpiresIn`). Ages use the Store clock and begin at
successful completion. `Info.ReuseFor` bounds a prospective browser age at lookup;
transport code must deduct elapsed time before using it. These describe a page/
value generation, not necessarily the age of data hidden inside nested caches.

Automatic nested lifetime propagation and implicit cached-function scopes
remain work. Shared scheduled refresh is explicit; authority-admitted build
seeds are a separate increment under qualification.

```go
var postsCache = cache.New(512)

func LoadPosts(ctx context.Context) ([]Post, error) {
    return cache.GetOrLoad(ctx, postsCache, "posts:v1", cache.Options{
        TTL: time.Minute,
        Tags: []string{"posts"},
        Paths: []string{"/posts"},
    }, func(ctx context.Context) ([]Post, error) {
        return databasePosts(ctx)
    })
}

// Call after the database transaction has committed successfully.
postsCache.ExpireTag("posts")
```

Use `Store.GetOrLoad` for untyped values. Do not mix typed and untyped access for
the same key; the typed helper returns an error for an inconsistent cached type.

## Semantics

- Capacity limits retained entry count. `WithMaxBytes` adds an accounted byte
  budget with LRU eviction; oversized values are returned without retention.
  Cache hits refresh LRU recency; they do not extend the expiration deadline. Expired
  entries are removed on access, `Len`, or before eviction would displace live
  entries. There is no background eviction goroutine.
- TTL starts after a successful load. A nonpositive TTL still coalesces active
  requests but stores no result. Loader errors and recovered panics are never
  cached. A panic is returned as a loader error to all current waiters.
- A key identifies both the result and its caching policy. Concurrent callers
  must agree on options and the loader. The first caller's options control a
  shared load; options are copied, so later slice mutation cannot change labels.
- Every caller can cancel independently. A foreground loader receives a context with the
  first caller's context values, detached from its deadline and cancellation.
  When all waiters cancel, the load context is canceled and subsequent requests
  start a fresh load. Loaders should always honor cancellation and impose any
  necessary backend-specific timeout themselves.
  Scheduled refresh keeps its scheduler deadline and remains owned by that job
  when individual waiting readers cancel.
- Invalidation removes matching stored entries and detaches matching active
  loads. Existing waiters may still receive an old in-flight result, but that
  result cannot repopulate the cache or replace a newer load. Invalidation does
  not wait for or forcibly stop existing loaders.
- Strict expiration uses `ExpireKey`, `ExpireTag`, `ExpirePath`,
  `ExpirePathPrefix` or `ExpireAll`. It removes stored values, cancels matching
  loads and makes outstanding readers retry their own loader/options in a new
  generation. This includes completed results not yet consumed, even with a
  nonpositive TTL, and earlier loads detached by ordinary invalidation. The
  replacement coalesces normally; an obsolete success/error cannot replace it.
  Publish the backing write before expiration, and compute reads inside the
  loader rather than capturing an obsolete snapshot outside it. Result delivery
  and expiration are ordered under the store lock. A read whose value has already
  been selected for delivery (including a cache hit) can finish after expiration;
  its result cannot be recalled or removed from application/browser caches.
  Use strict expiration for mutation read-after-write requirements in this process.
- `InvalidatePath` matches a path label exactly. `InvalidatePathPrefix` also
  matches descendants at path-segment boundaries. Labels normalize slashes,
  dot segments, and trailing slashes and discard query strings and fragments.
  Labels are explicit; attaching `/posts` does not automatically tag all data
  used by that page. The corresponding `ExpirePath`/`ExpirePathPrefix` methods use
  the same label rules. Key/label methods return the number of distinct affected keys,
  including outstanding readers; multiple generations of one key count once.
- Values are shared references. Treat them as immutable or clone at application
  boundaries. The cache does not serialize, copy, persist, or distribute data.

Keys must include all result-affecting inputs, including tenant/user identity,
locale, filters, and authorization version where relevant. Never use a shared
public key for a result computed from request-specific secrets. Caching is
explicit; HTTP requests and `fetch` operations are never cached automatically.

`cache.WithClock` injects a concurrency-safe monotonic clock for deterministic
expiration tests. `cache.WithMaxConcurrentLoads(n)` optionally bounds active
loaders across distinct keys. Waiting requests remain cancellable. When this
option is enabled, a loader must not synchronously load another uncached key
from the same store, because nested loads could exhaust its slots. Cache
capacity itself does not limit incoming requests or queued keys; apply HTTP
concurrency/rate limits at the application boundary when needed.

Use read-only/idempotent loaders: expiration may retry them, and a canceled old
loader may overlap its replacement. Cancellation is cooperative. A loader that
ignores it can continue holding a concurrency slot and delay replacement work;
the store does not forcibly terminate Go code. Readers remain cancellable during
retries. Repeated concurrent expiration can delay a read until the caller's
deadline; apply request and backend deadlines. A loader must not expire its own
key on every retry. Pending-delivery bookkeeping lasts only while readers await
their results and is separate from retained-entry byte accounting.

`App.RevalidateTag` and `App.RevalidatePath` use strict expiration (the latter
includes descendants). Action revalidation hooks should expire their shared
store after a successful commit, as the demo does. This strengthens server-local
read-after-write behavior; it does not broadcast to other instances or clear an
already delivered browser/CDN response. Ordinary `Invalidate*` and `Clear`
retain their weaker detach semantics for callers that intentionally allow
existing readers to finish with old data.

This is an in-process primitive, not a distributed invalidation or incremental
static regeneration system. Multi-process deployments need an external cache
or a broadcast mechanism to invalidate each process.

## Partial and inline profiles

```go
revalidate, expire, noBrowser := time.Minute, time.Hour, time.Duration(0)
config.CacheProfileSpecs = map[string]cache.ProfileSpec{
    "default": {Revalidate: &revalidate}, // Other ages inherit built-in default.
    "catalog": {Expire: &expire},        // Inherits effective default ages.
    "alias":   {},                      // Copies the effective default.
}
config.Routes[i].Cache = &nextgo.CachePolicy{Profile: "catalog"}
// Or use an inline partial profile rather than a named one:
config.Routes[j].Cache = &nextgo.CachePolicy{
    ProfileSpec: &cache.ProfileSpec{Stale: &noBrowser},
}
```

Nil pointers inherit; non-nil pointers set literal values, including zero or
`cache.Never`. Explicit zero `Stale` disables browser reuse while retaining server
caching. Zero `Revalidate` makes a generation immediately stale. Zero `Expire`
is invalid; it never means omission. The complete resolved lifetime must validate
(`Expire > Revalidate`, or `Expire == Never`), even when ages were inherited.

Resolution happens once at `New`, independently of map iteration order. A partial
`default` overlays the built-in default. Other named specs and inline specs use
that effective default. A name cannot appear in both `CacheProfiles` and
`CacheProfileSpecs`; both maps count toward the combined 256-definition bound.
Full profiles remain literal and do not acquire inherited zero fields. Built-ins
not mentioned in either map keep their preset values. When a built-in is
explicitly replaced by a spec, omitted ages inherit effective default rather than
the old named preset. Names remain case-sensitive and follow the same length/
character rules. Invalid or ambiguous definitions fail before startup.

Resolved registry/route values are owned; later changes to input maps, specs or
pointed-to durations do not change requests or configuration identity. Equivalent
full, named-partial and inline policies share the same canonical identity and
page snapshot namespace. A changed inherited age separates the namespace and
triggers the normal deployment-configuration checks. No automatic request or
route caching is introduced: nil policies and existing zero-TTL policies remain
uncached. An explicit empty inline spec opts into the effective default.

`cache.ProfileSpec.Resolve(defaults)` resolves a one-off spec to an owned value
for data work; `cache.ResolveProfiles(full, specs)` resolves an owned registry
with the same built-ins/validation as application startup. The `/evergreen` demo
uses a named partial alias of the effective default and resolves that alias in
its publication action. Nested lifetime propagation and implicit cached-function
scopes remain separate work.

## Non-expiring lifetimes and the default profile

```go
life := cache.Lifetime{Revalidate: 15 * time.Minute, Expire: cache.Never}
value, info, err := cache.Lookup(ctx, store, "public-posts:v1", cache.Options{
    Lifetime: &life, Tags: []string{"posts"},
    ScheduleRefresh: nextgo.CacheRefresh(ctx),
}, readPosts)
// info.NoExpiry identifies no hard deadline; info.ExpiresIn is then zero.
```

`cache.Never` is an explicit `time.Duration` sentinel accepted only for
`Lifetime.Expire`, `Profile.Expire` and a `MarkStale*` window. Other negative expiry
or marking values are errors; `Revalidate` must remain nonnegative and browser
`Stale` remains bounded at 24h. Legacy nonpositive `Options.TTL`, including the
sentinel's numeric value, still coalesces without retention. Choose Lifetime to
opt into non-expiring storage. There is no artificial distant expiration date.

The built-in `default` profile has `Stale: 5m`, `Revalidate: 15m`, `Expire: Never`.
Opt in for a route with `CachePolicy{Profile: "default"}`, or resolve its effective
value using `nextgo.LookupCacheProfile(ctx, "default")`. `Config.CacheProfiles`
can override it just like the other named profiles. Full `cache.Profile` values
remain literal; `Config.CacheProfileSpecs` supports partial definitions below. Routes without a cache policy and
zero-TTL policies retain their existing behavior. Nested lifetime propagation
and implicit cached-function scopes remain unfinished.

No hard expiry does not mean no refresh, eviction, cancellation or invalidation.
Reads past the fresh window still reserve a coalesced refresh; entry/byte budgets
still evict or reject values, and strict expiration still forces a new generation.
Transient refresh failures may retain last-good data indefinitely while the entry
survives. Configure a finite expiry when that is unacceptable. A rejecting
`KeepStaleOnError` policy (including the page renderer's permanent/auth failure
policy) drops it. These values are process-local, with no restart durability or
replica coordination. Scheduled work retains its normal worker/queue/deadline
bounds. Browser reuse always uses the original generation age; an old server
snapshot never regains a browser window merely because it has no hard expiry.

A finite stale mark clips a non-expiring entry to `now + window`. Later `Never`
marks preserve that finite deadline; only a successful fresh generation can
start its configured non-expiring lifetime again. A `Never` mark on a finite
entry likewise cannot extend its original deadline. `/evergreen` demonstrates
the default profile, progressive publication and Go Refresh; Increment uses
strict expiration to publish a fresh view immediately.

## On-demand stale revalidation

Commit the backing write before choosing a server-local publication policy:

```go
// Readers must wait for a generation loaded after this expiration.
app.RevalidateTag("posts")

// Readers may receive the last-good snapshot while a later read refreshes it.
if err := app.RevalidateTagProfile("posts", "minutes"); err != nil {
    return err
}
// The path variant includes descendant path labels.
if err := app.RevalidatePathProfile("/posts", "minutes"); err != nil {
    return err
}
```

Only the named profile's `Expire` controls this on-demand stale window, including `cache.Never`. The
existing hard deadline is never extended: repeated marks keep the earliest
deadline. Marking does not run a loader, reset generation age, or grant a new
browser lifetime. The next read reserves one coalesced refresh through its
`ScheduleRefresh` option; without a scheduler (or if admission fails), it blocks
for fresh data. Public page snapshots use the same native/Lambda completion
scheduler as ordinary age-based refresh. Transient failures can retain the
last-good value only until the clipped deadline; hard expiry blocks and returns
an error if a fresh load fails.

For data stores, `MarkStaleKey`, `MarkStaleTag`, `MarkStalePath`,
`MarkStalePathPrefix` and `MarkStaleAll` accept an explicit `time.Duration`
window and return the number of distinct affected keys plus an error. `cache.Never` preserves any existing deadline without adding one. Zero is strict expiration;
other negative windows return `ErrRevalidationWindow` without changing entries or
pending work. Named app methods validate the profile before touching the store.
All marks retire matching pre-write active, detached and undelivered loads,
canceling cooperative work and retrying its readers. A retained last-good entry
can still satisfy those readers by design. Queued jobs from the old generation
cannot publish or clear a newer refresh reservation. Already selected/delivered
responses cannot be recalled.

The `/profiles` demo's progressive form commits its public counter update and
marks the `profile-demo` tag using the effective application's profile. It does
not attach strict revalidation labels to the same write. Choose immediate
expiration for mutations requiring server-local read-after-write behavior.
Neither mode broadcasts to other tabs, CDN caches or replicas; Go action cache
clearing only covers requests made through the current browser's `client.Fetch`.

## Comparison with the parity target

Next.js distinguishes immediate read-after-write expiration through
[`updateTag`](https://nextjs.org/docs/app/api-reference/functions/updateTag),
profile-driven stale refresh through
[`revalidateTag`](https://nextjs.org/docs/app/api-reference/functions/revalidateTag),
and separate client/revalidation/expiry timings in
[`cacheLife`](https://nextjs.org/docs/app/api-reference/functions/cacheLife).
Next.go's current `App.RevalidateTag` uses immediate server-local expiration;
its name does not imply the Next.js tag-profile behavior. The separate
`RevalidateTagProfile`/`RevalidatePathProfile` APIs opt into bounded stale marking.
Named profiles, non-expiring server lifetimes and Go browser windows now exist; nested lifetime propagation, implicit cached-function scopes and complete coordinated/distributed invalidation remain unfinished.
The Go APIs are not source-compatible substitutes for those Next.js functions.

## Accounted byte budgets

```go
var postsCache = cache.New(512, cache.WithMaxBytes(32<<20, func(value any) int64 {
    posts, ok := value.([]Post) // typed GetOrLoad exposes the original value
    if !ok { return -1 }       // unsupported values are not retained
    size := int64(64 * len(posts))
    for _, post := range posts {
        size += int64(len(post.Slug) + len(post.Title) + len(post.Body))
    }
    return size
}))
```

Include all fields and backing storage retained by your own value type. The
cache adds conservative key, label and entry charges. The callback runs outside
cache locks and must be concurrency safe and nonblocking. A negative, oversized
or panicking estimate skips retention without failing a successful load.
`store.Bytes()` reports accounted unexpired retained bytes; invalidation, expiry
and clear release that accounting. Loader/weigher `runtime.Goexit` returns an
error to waiters and releases load slots instead of stranding them.

Pass a nil weigher for `cache.Sizer` implementations (`CacheSize() int64`), strings
and byte slices. Other values then skip retention. The framework's frozen public
page snapshots implement `Sizer`. Its default page store allows 128 entries and
64 MiB accounted payload; the demo's explicit process-local store uses 16 MiB. A custom
`Config.Cache` controls its own limits, so retain byte accounting when replacing
that store.

A byte estimate is not a heap measurement: shared backing storage, application
references, transient loader/rendering memory and queued work are outside this
accounting. An entry-count-only `cache.New(n)` remains available. Bound request
concurrency and measure realistic memory behavior separately.

## Shared data across application instances

The local APIs above retain their existing semantics. For explicit serialized
data shared by multiple application processes, see the experimental
[`cache/shared` contract](../docs/SHARED_CACHE.md): an authoritative backend,
fenced leases, cancellable HTTP operations, acknowledged expiration and stale
marking, plus optional stale refresh through `cache.RefreshScheduler` and
`nextgo.CacheRefresh`. Shared reads block by default; scheduled reads can serve
an unexpired last-good value while one replica owns refresh. Hard expiry and
fenced error-discard rules still apply.

Public rendered pages can explicitly opt in using `Config.SharedCache` and
`CachePolicy.Shared`; the default `Config.Cache` remains local. Build seeds have
a separate `PrerenderPolicy.Shared` opt-in described below.
The shared page codec binds frozen body/head/document values to build,
configuration, public host and URI; its authenticated storage namespace is
trusted with rendered HTML. It regenerates validated head markup rather than
replaying stored head HTML. Valid fresh renders that fail shared admission are
served once, uncached, instead of becoming cache-size errors.

Use `nextgo.ExpireCache`/`MarkCacheStale` context helpers or their `App` methods
to acknowledge local plus configured shared invalidation after backing commits.
Validate and handle errors: local changes remain when the remote call fails, and
these stores are not one transaction. Existing `App.RevalidateTag`/`Path`/profile
methods remain process-local. See the [shared page contract](../docs/SHARED_CACHE.md#shared-public-page-snapshots)
for host/scope identity, privacy bypass, admission limits, invalidation scope and
same-origin multi-instance examples.

The reference memory service is not durable. Protocol v3 adds build-ticket
admission and is incompatible with the preceding experimental v1/v2 services;
mixed versions fail closed. Shared caching does not make process-local caches
in other replicas coherent, propagate nested profiles or invalidate browser/CDN
copies.

### Authority-admitted prerender seeds (qualification in progress)

`PrerenderPolicy.Shared: true` uses `Config.SharedCache` for both optional build
seeds and runtime regeneration. Build must reserve a one-use authenticated
authority ticket before parameter enumeration/rendering. Startup consumes the
ticket; generations retain their backend reservation age. Shared expiration or
stale marking revokes outstanding namespace tickets/sessions even when no
published key matches. Unknown/consumed/expired/revoked tickets skip import and
regenerate normally; transport/protocol errors fail startup.

The bounded ticket/session protocol and optional file persistence do not make
this an application database, durable work queue or replicated/HA authority.
See [shared prerender admission](../docs/SHARED_CACHE.md#shared-prerender-admission)
and the [online build/runtime example](../docs/PRERENDER.md#shared-build-seeds-and-regeneration).
The current shared-prerender increment has not completed its full qualification;
preceding durable-cache results describe the preceding feature set.


## Optional durable shared authority

`cache/shared.OpenFile(dir, FileOptions{Capacity, MaxBytes, MaxDiskBytes})` adds a
synchronous local-file Backend behind the shared HTTP service; memory remains
the default. The directory must be private **0700** and owned by the service user,
with private **0600** single-link files. A stable, never-unlinked kernel lock permits
one Linux/macOS authority. App replicas and serverless workers contact that one
service; independent local files in each worker are not a shared database.

The file backend persists published values, original ages, stale marks and
acknowledged expiration; leases/queued refreshes are not restored. It assumes
trusted local storage with durable sync/atomic rename and nondecreasing wall
time. `MaxDiskBytes` limits the active WAL; atomic compaction can temporarily
use twice that journal space plus fixed metadata. Incomplete/corrupt journals
fail closed rather than being truncated or rolled back automatically, so an
interrupted append may require an explicit whole-cache discard and recompute.
Checksums cannot detect rollback to a whole valid prefix/older store; there is
no independent anti-rollback witness.

Use `nextgo-cache -data-dir <private-directory>` or the Dockerfile's separate
cache-runtime target with persistent `/data`. The [durable authority contract](../docs/SHARED_CACHE.md#durable-local-file-authority)
details ownership, locking, active/peak disk accounting, startup failure and
recovery. Shared prerender ticket reservation/consumption and admitted values
are journaled; import sessions and load leases are not restored. WAL format 2
upgrades an existing format-1 journal on the first seed mutation. This is one
local authority, not replicated/HA/NFS storage or a completed production release
qualification.
