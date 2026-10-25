# Experimental offline contract foundation

This foundation describes **saved anonymous public** documents, not the current
user's authenticated state. Native `App.PrepareOffline` prepares approved HTML
through the application's handler/middleware chain. It does not register a
service worker, install a browser feature or enable offline navigation. Compiler,
worker, storage and browser integration remain unfinished. T16 remains
**Missing** and all release gates remain open.

## Explicit public permission

The `offline` contract starts with an application declaration:

```go
policy, err := offline.Normalize(offline.Config{
    ID: "public-docs",
    Origin: "https://example.test",
    Semantics: offline.SavedAnonymousPublic,
    Pages: []offline.Page{
        {Path: "/", MaxAge: 24 * time.Hour},
        {Path: "/offline", MaxAge: 24 * time.Hour},
    },
    Fallback: "/offline",
    Updates: offline.WaitForClosedClients,
})
```

Handle `err` before using the policy. The origin is trusted exact configuration;
it is not inferred from request Host or forwarded headers. HTTP is restricted
to localhost/loopback. Page paths are literal, query-free app-relative paths;
the fallback must also be an allowed page. At most 16 pages are declared, with
explicit retention of one second to seven days in whole seconds.

This declaration is necessary, not proof that content is public. Cookie absence,
HTTP cache headers and existing prefetch permission cannot authorize persistent
storage. Native final-response admission must inspect the complete anonymous
request/response after middleware unwinds. Forms, credentials, mutations, unsafe
requests, SPA envelopes and automatic replay are outside this contract.

## Native anonymous document preparation

Call the native method explicitly on a constructed application:

```go
prepared, err := app.PrepareOffline(ctx, policy.Config())
if err != nil {
    return err
}
documents := prepared.Documents()
```

The signature is `PrepareOffline(context.Context, offline.Config)
(*PreparedOffline, error)`. No startup capture or HTTP endpoint is installed.
Use a bounded context. The method normalizes the policy and constructs fresh
query-free GETs from its trusted origin. Caller values/credentials are discarded;
cancellation/deadline remain. Every selected page must have an ordinary public
`Route.Cache` declaration with a positive TTL or effective lifetime. Public
permission is an application assertion, not automatic secret classification.
Rendering uses the read-only public snapshot path rather than a personalized
request snapshot.

V1 requires production mode and an exact `BasePath` match. It refuses development,
managed draft configuration, locales, zones, alternate asset prefixes, trailing-
slash configuration, rewrites/redirects and early scripts. Selected prerender,
streamed/region/slot/intercepted pages and API/handler-shadowed paths are refused.
A private guard checks middleware URL changes before framework dispatch.
Middleware that deliberately discards that request context or performs its own
side effects remains trusted application code; this is not a sandbox.

The complete middleware chain runs into a bounded final recorder. It checks
commit-time and final headers after middleware returns, requiring nonempty 200
UTF-8 HTML without flush, overflow, redirect, cookies, authentication challenge,
private/no-store, application `Vary`, CSP, nonce, encoding or trailers. Only the
framework's own compression/navigation `Vary` is omitted for this private,
nonnegotiating capture. Request-sensitive rules, cache bypass and nonce state
also refuse admission. Trusted native code can still perform application work;
it must honor cancellation, which cannot forcibly stop an ignoring callback.
Refusal returns no partial preparation; use `errors.Is(err,
nextgo.ErrOfflineAdmission)` for admission errors and handle context errors too.

The fallback is deliberately different from its normal route body: only the
approved title/description become escaped text in an inert self-contained
document. It contains no application body, head, runtime or assets. Its final
bytes must match that template after middleware unwinds; even whitespace
transforms are refused.

`Policy()` returns owned normalized configuration; `Documents()` returns
independent records and HTML byte copies with path, URL, MIME, SHA-256, retention
and fallback status. `BuildID()`, `ConfigurationID()`, `Identity()` and
`Admission()` bind the native result to policy/build/configuration. This identity
is separate from catalog generation. The result contains document bytes only:
it neither discovers the hydration asset graph nor creates a complete catalog,
worker, stored generation or installation proof.

## Complete immutable inventory

`NewCatalog(CatalogSpec)` owns normalized policy, runtime/configuration identity,
save time, document hashes and every declared asset's exact URL, decoded size,
MIME and SHA-256. All entries are required. One worker, core WASM, matching SDK
shim and bootstrap plus at least one stylesheet are mandatory; lazy/public
dependencies must also be declared. The contract cannot discover dependencies
hidden in application code or HTML.

Public snapshots use the exact runtime's `/_nextgo/<runtime>/offline-public/`
namespace. Mutable `/assets/` aliases, other origins/runtimes, queries and URL
aliases are refused. Catalog generation derives from the full canonical policy
and inventory. The future compiler must assign runtime identity from relative
snapshot/worker/shim/bridge-template bytes first; the catalog cannot feed back
into that identity. Hashes identify bytes, not public permission or authenticity.

`VerifyInventory` requires observations for every entry with exact size, MIME
and digest. The adapter must derive observations by fully consuming and hashing
decoded bytes. A successful header, commit marker or remembered completion flag
is insufficient after eviction/corruption. Native `MarshalCatalog`/`ParseCatalog`
provide bounded strict metadata encoding; they do not approve network content.

## Reservation and activation

Defaults charge active, previous **and staging** generations together:

| Reservation bound | Default |
| --- | ---: |
| One decoded asset | 16 MiB |
| One document | 512 KiB |
| One generation | 32 MiB |
| Total across three generations | 96 MiB |

Catalog/commit allowances are included, and shared URLs are charged again per
generation. Zero budget fields select defaults; smaller consistent limits are
never enlarged to fit a file. These are policy reservations, not browser-storage
quota, heap/RSS or fetch-concurrency guarantees. The ledger performs no I/O.

1. `Restore` rechecks active/previous inventories and charges an interrupted
   stage. Unknown/corrupt stage metadata requires physical reconciliation;
   forgetting its record does not remove its bytes.
2. `Reserve` charges the candidate's full cost before fetching/writing. Refusal
   preserves the last good generation. Its opaque reservation belongs to one
   ledger and cannot authorize another stage.
3. `Observe` accepts each fully read, hash-checked declared entry once. Extra,
   duplicate, mismatched and post-completion observations are rejected.
4. `Complete` requires the whole inventory. It neither activates nor releases
   the reservation; the adapter must persist its bounded completion marker last.
5. `Activate` requires a complete stage, actual closed-client evidence and no
   retained previous generation. Consent messages, `navigator.onLine` and one
   tab's state do not prove that all clients closed.
6. `ConfirmPreviousRemoved`/`ConfirmStagingRemoved` release reservations only
   after exact owned storage deletion and physical work have settled. Canceling
   a fetch or dropping a reference is not deletion/completion evidence.

`Fresh(path, nowUnix, highestObservedUnix)` also requires the adapter's persisted
highest-observed-time fence. Restore and update that fence across restarts;
clock rollback must not extend retention. This stateless predicate cannot infer
an earlier observation by itself or supply an independent trusted clock.

## What remains

There is no registration command, worker lifecycle/storage adapter, complete
compiler asset capture or browser offline test matrix yet. Copying an admission
label into a catalog cannot establish that native preparation approved its bytes.
Private worker experiments, including failed attempts, are retained separately;
they are not successful browser/installation qualification. Useful SSR and the
ordinary online application remain the supported path. See [the inventory](PARITY.md)
for T16 and the unchanged release requirements.

Check the foundation and native gate with:

```sh
go test -race . ./offline
go vet . ./offline
sh scripts/test-wasm.sh
```

The ordinary WASM invocation includes the portable `offline` package. These
checks do not establish worker installation, browser storage or offline-app
qualification. Native host checks pass; minimum-SDK verification is separate.
