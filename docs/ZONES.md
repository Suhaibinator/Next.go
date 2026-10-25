# Multiple applications and trusted zone context

`NewZones` composes independently configured Go applications behind one native
HTTP handler. A zone has an exact public host, a literal mount, a unique ID, and
optional trusted tenant metadata. This is an experimental composition API; its
qualification is separate from the framework's open production release gates.

## Constructing applications

```go
zones, err := nextgo.NewZones(ctx, []nextgo.ZoneConfig{
    {
        ID: "marketing", Host: "www.example.test",
        Config: nextgo.Config{Phase: nextgo.PhaseProduction, Routes: marketingRoutes},
        Configure: configureMarketing,
    },
    {
        ID: "blog", Host: "www.example.test",
        Config: nextgo.Config{Phase: nextgo.PhaseProduction, BasePath: "/blog", Routes: blogRoutes},
        Configure: configureBlog,
    },
    {
        ID: "alpha", Host: "alpha.example.test", Tenant: "alpha",
        Config: nextgo.Config{Phase: nextgo.PhaseProduction, Routes: tenantRoutes},
        Configure: configureTenant,
    },
})
if err != nil {
    return err
}
```

The constructor accepts 1–64 declarations. IDs are unique ASCII identifiers of
at most 64 bytes; optional tenant identifiers have the same syntax and a
128-byte limit. An identifier begins with a letter or digit and may then contain
letters, digits, `.`, `_`, and `-`; `.` and `..` are invalid. Hosts are bounded
ASCII DNS names or IP authorities, optionally with an explicit port. Wildcards,
credentials, URL paths, and implicit forwarded-host selection are unsupported.
DNS case and one trailing DNS dot are canonicalized; an explicit port remains
part of the selection. The empty base path mounts at `/`; other mounts use the
framework's literal base-path rules.

`Configure func(*Config) error` runs after `Config.Zone()` and the normalized
`Config.Phase` are available, before application construction and metadata
factories. It may configure ordinary application policy but cannot change the
declared base path or zone descriptor. Hooks are trusted native code and must
honor their own resource ownership; they are not isolated execution contexts.

`Config.Zone()` returns an owned `ZoneInfo{ID, Tenant, Host, BasePath}` snapshot.
`CurrentZone(ctx)` reads the selected application's trusted descriptor in native
request handlers, page loaders, actions, static parameter enumeration, metadata
factories, cache regeneration, request regions, and completion callbacks. A
standalone application returns the zero descriptor, even when its caller passes
a context obtained from a different zone. Neither helper authenticates a user.

## Host and path ownership

The group validates the request's `Host` before invoking application middleware,
assets, metadata, actions, or page loaders. It chooses the longest matching
literal mount on that host, using complete path segments. `/blog` owns `/blog`
and `/blog/posts`, while `/blogger` remains with the root application. Encoded
mount spellings are not aliases for literal mounts. Query strings do not select
a different zone. An absolute request URL must agree with `Host`.

An invalid authority or path receives `400`; an unconfigured valid host receives
`421`; a configured host with no matching mount receives `404`. A selected
application's `404` never falls through to a parent application. Its middleware,
routes, errors, and actions execute within that selection. Application rewrites
remain rules of the selected application; they do not redispatch the group.

The public host is deployment configuration. Keep the declared host, or apply an
explicit trusted operator override before construction. `ParseZoneHosts` accepts
a JSON object mapping known zone IDs to exact authorities; empty input preserves
the declarations. It rejects unknown IDs, duplicate JSON keys, invalid hosts,
and input exceeding 64 KiB. The parser reads neither environment variables nor
requests. Listening on a different TCP address does not change a public host.
Configure ingress to preserve or deliberately set the expected `Host`;
`Forwarded`, `X-Forwarded-Host`, query parameters, and cookies do not select it.

Current ownership restrictions are deliberate:

- Public and runtime assets use their application's mount. An alternate local
  asset prefix or separate CDN origin is rejected by `NewZones`.
- A child mount cannot enter a parent's `api`, `assets`, `_nextgo`, or managed
  metadata namespace, or intersect its configured API routes or handlers.
- Locale domains cannot also select zones. Subpath locales are supported on
  leaf zones; a zone with nested zones cannot also configure locales.
- Metadata resources belong to the selected application at its mount. They are
  explicitly public and bypass application page middleware, as in an ordinary
  application. Keep credentials, identities, and private tenant data out of them.

## Full-document boundaries

Each ancestor application receives the nested zone's logical mount in
`Config.DocumentPaths`. This additive configuration also works independently:

```go
c.DocumentPaths = []string{"/blog", "/support"}
```

Entries are non-root literal logical subtrees, relative to that application's
base path. Construction owns, sorts, and deduplicates them, with at most 64
entries and 64 KiB of input. They join the existing navigation-v3 document
boundary manifest and configuration identity. They do not mount HTTP handlers,
change native route matching, or grant permission to serve a path.

An ordinary anchor crossing a declared boundary uses browser document navigation;
the old application's layout, Go runtime, and counter are replaced by the new
document. Same-zone links can retain that application's layout and state.
`client.Navigate`, `Replace`, and `Prefetch` reject document-boundary targets
without starting a framework fetch. For an explicit programmatic document
transition, use the browser's document navigation API. A destination outside an
application's base path or origin is already outside its SPA scope.

Zones sharing an origin also share browser authority: cookies, local storage,
service workers, and same-origin script access are not partitioned by this API.
Use distinct origins and appropriate browser policies when that separation is
required. Go packages, process globals, native credentials, and closures are also
shared process authority. The constructor does not sandbox application code.

## Manifest and command-line entrypoint

The version-1 JSON manifest describes application directories relative to the
manifest's directory:

```json
{
  "version": 1,
  "zones": [
    {"id": "marketing", "host": "www.example.test", "basePath": "", "app": "./marketing"},
    {"id": "blog", "host": "www.example.test", "basePath": "/blog", "app": "./blog"},
    {"id": "alpha", "host": "alpha.example.test", "basePath": "", "tenant": "alpha", "app": "./tenant"}
  ]
}
```

The manifest must be a regular file of at most 1 MiB. Parsing rejects unknown or
duplicate JSON members, unsupported versions, duplicate host/mount bindings, and
case-colliding IDs. Application paths must remain local to the manifest directory;
absolute paths, escapes through `..` or symlinks, and private/generated application
directories are rejected. A manifest declares trusted build inputs, not an
untrusted request-time application loader.

```sh
go run ./cmd/nextgo zones build -out dist/zones examples/zones/zones.json
./dist/zones/server -addr 127.0.0.1:4230
go run ./cmd/nextgo zones dev -addr 127.0.0.1:4230 examples/zones/zones.json
```

`build` publishes one standalone native server binary. `dev` supervises the
manifest and its applications. Flags precede the manifest. Aggregate
`zones export` is unsupported; a group does not become a valid static deployment
by discarding its host dispatcher or server-dependent behavior.

The initial compiler requires one owning Go module and identical effective
workspace, SDK, target, build flags/experiments, and caller overlay mappings for
all applications. Reusing one application directory in several zones is
supported. The compiler builds each unique application once and embeds an
independent copy of its browser/public/private artifacts for every declaration;
there is no asset deduplication promise. Aggregate copies are bounded to 128 MiB
and 16,384 entries before the final native compile. Source, manifest, and build
context changes during compilation cause the result to be rejected.

The generated binary accepts `-zone-hosts` as the JSON override described above;
otherwise it reads `NEXTGO_ZONE_HOSTS`. An explicitly supplied empty flag keeps
the declared hosts. `-addr` changes the listener only. For example, to use a
different local public authority for the two path-selected applications:

```sh
./dist/zones/server -addr 127.0.0.1:4230 \
  -zone-hosts '{"marketing":"localhost:4230","blog":"localhost:4230"}'
```

One explicit aggregate `-env-dir` (or `NEXTGO_ENV_DIR`) and `-env-mode` select
shared environment loading. Host overrides are resolved after that load. The
generated server does not automatically load a separate dotenv environment for
each application: all zones use one native process environment. The development
supervisor explicitly selects the manifest directory for its child environment
and validates a fresh effective override before each publication. Captured
ambient variables, including an explicitly empty value, take precedence over
dotenv files. The exact validated override snapshot is passed to the child.
Invalid dotenv or host configuration preserves the previous host/proxy state;
a valid environment-only host change restarts and reloads once without compiling
unchanged application sources.

Development retains eight successful publications, including environment-only
child restarts. Retained zone runtime assets remain public at each publication's
exact effective host and declared `BasePath/_nextgo/<runtimeID>/` prefix, including during a
host/mount transition. They do not acquire another zone's host or arbitrary path
aliases. Unchanged compiler directories remain owned until their final retained
publication expires. Retention counts publications, not individual zone bundles.
Development inspection, event streams, and build-status HTML additionally
require a loopback peer and same-origin request policy; they are development
controls, not production application routes.

Discovered prerender declarations are unsupported in this first compiler slice;
the compiler rejects them before build-time capture or seed work, including at
each application's rediscovery. The generated startup wrapper also rejects
prerender policies introduced by `Configure`, before constructing applications.
It also requires `Configure` to preserve the generated `Phase` and `Dev` mode
and to keep `PrerenderBuildMode` disabled.
Aggregate prerender, static enumeration, path-config, and export-purpose flags
are rejected before application hooks. This does not qualify unzoned private
seeds for a group. Ordinary independently built applications retain their
separately documented export/prerender behavior. These are aggregate CLI
restrictions: the manual `NewZones` API retains normal phase validation rather
than imposing a blanket prerender ban; direct App build APIs have separate
trusted-zone context tests.

## Authorization and data/cache separation

`Tenant` is trusted deployment metadata, not a signed-in principal or an access
grant. Authenticate the visitor and check membership independently in every
protected page, API, and action. Scope database queries and mutations to both the
authorized subject and the selected tenant. A client can send a configured host;
being routed there does not prove membership. Do not place private data into
`Config.Public`, public assets, metadata, or public page/shell caches.

The opaque page/configuration identity includes all zone fields. Construction
also rejects reused explicit `*cache.Store` pointers and shared-cache namespace
identities across declarations. Distinct page keys alone cannot isolate
invalidation: local `ExpireAll`/tag/path operations affect their store, and shared
`Expire`/`MarkStale` operations affect every schema in a namespace. Allocate a
local store and a distinct shared namespace for each zone. A different shared
serialization schema is insufficient. Namespace comparison deliberately excludes
backend credentials/endpoints, so even separate backends using the same namespace
are conservatively rejected. `NamespaceIdentity()` is comparison metadata, not
authorization or evidence of backend isolation.

Application-owned caches, repositories, global variables, and external services
remain the application's responsibility. In particular, zone-specific page
caching does not automatically scope a generic cache call with a reused key.
Public shell regeneration strips arbitrary request context values and restores
only explicit framework configuration, including trusted zone metadata. It uses
a synthetic public request, not an authenticated request snapshot.

## Lifetime and platform adapters

Construct with a non-nil bounded startup context. If a later application's
construction fails, the group closes previously constructed applications with
an independent bounded cleanup context and returns the startup/cleanup errors.
This cannot undo arbitrary effects performed by trusted configuration hooks.

Use the group as the HTTP handler. A custom native main must first stop accepting
requests and call `http.Server.Shutdown`, then call `zones.Close` with an
independent bounded context. Close rejects subsequent group requests with `503`,
closes child applications concurrently once, drains accepted completion work,
and shuts down their native resources. Its budget is at most five seconds or the
caller's earlier deadline. Native callbacks must cooperate with cancellation;
Go cannot terminate a callback that ignores it.

The group rejects reused explicit `*telemetry.Tracer` pointers so one child's
shutdown does not close another child's recorder early. Separately wrapped
tracers that share an underlying exporter still require deliberate application
lifecycle ownership. `Zones.FlushTelemetry(ctx)` forwards to each child, so the
generic Lambda adapter retains the normal flush-before-freeze behavior. Use the
same group with buffered or streaming `serverless.Run`, and close it when the
runtime exits. Each child's flush/shutdown hook receives its owning zone's
trusted context, rather than a descriptor inherited from the caller.
Completion work remains process-local, bounded, best effort, and
unsuitable for required durable mutations; see [deployment](DEPLOYMENT.md).

## Demonstration and qualification

[`examples/zones`](../examples/zones) composes a root marketing app, a `/blog`
app, and two host-selected instances of one tenant app. Marketing/blog state is
explicitly public and uses independent local stores with the same logical cache
key/tag. The tenant fixture separately authorizes its protected page, record API,
and preview action. Its public issuer lets anyone assume Alice or Bob using a
fresh process-signed demo cookie; it is not real account authentication and its
records are synthetic. A new process invalidates those demo sessions.

The frozen source passes full native race/vet on Go 1.27.3 and Go 1.24.13
(62 tested packages and 166 without tests), matching core/mutation/module WASM
checks and core/module vet on both SDKs. Eight application generated-reference
checks pass. Fresh standalone artifacts include the host aggregate
`0ec073cf…`, host ordinary application `ed686d51…`, and Linux aggregate
`149db99d…`; production and development examples are qualified separately.

Production zone browser checks pass 36 host executions and 48 Linux executions,
with the Linux matrix covering Chromium, Firefox, WebKit and touch. They exercise
useful native HTML before WASM, actual distinct zone bundles and styles,
same-zone state/layout retention, cross-zone document replacement, host/path
ownership, independent cache/action state, and fresh protected page/API/action
membership checks. Browser host overrides select real loopback authorities;
the executable transport matrix separately checks the original declared DNS
hosts. These are overlapping platform executions, not 84 distinct capabilities.

Each platform passes 13 executable cases: six actual aggregate HTTP/
buffered-local-Lambda/streaming-local-Lambda cases, one aggregate export-refusal
case, and six fixture adapter units (1.372 seconds for the final host run). The
228-case legacy executable suite passes independently on each platform
(117.313 seconds on the host). Local Runtime API mocks do not qualify actual AWS.

The host regression record retains an initial primary result of 334/335: native
Chromium did not restore one published document from BFCache and reported the
reason as `masked`. The cause remains unexplained. With the same binary and no
source/assertion edits, a bounded nine-case diagnostic passed, followed by one
approved full repetition passing all 335 original cases. Separately, 23 static
cases pass. The final positive host browser scope is therefore 394 executions
(36 zone + 335 primary-repeat + 23 static); the initial failure remains evidence,
and the diagnostic/prior successes are not added again or treated as a fix.

Actual development CLI/browser qualification passes the final Chromium workflow
on the host (52.7 seconds), including useful HTML with JavaScript disabled,
real Go recompilation/reload, failed-build recovery, public asset and manifest
changes, retained asset ownership, environment-only host changes and graceful
cleanup. Earlier desktop/touch WebKit workflow passes used a preceding harness
and are separate evidence. The final Linux development workflow passes all four
engines in 4.1 minutes, with no skips, flaky results, or global errors. Each runs
the final eight-phase workflow including HTML with JavaScript disabled. Cleanup
confirms all 30 observed listeners closed, CLI exit zero, and no remaining build
directories.

Two development harness failures remain distinct. The initial host fixture copy
omitted the required `scripts` Go package before any application built; a copy
filter repair preceded the qualified host result. The initial Linux runner
overlay exposed only `test-browser.mjs` at `/src/scripts`, masking the production
Go package in the same immutable source root. Correcting that mount alone,
without Node or Go source changes, preceded the final four-engine result.
Neither incident establishes or required a framework/runtime fix.

Local provenance is retained under
`.nextgo-test-results/qualification/zones/host/qualification.json`,
`.nextgo-test-results/qualification/zones-wave/final/qualification-final.json`,
`.nextgo-test-results/qualification/zones-wave/host-deployment-final.log`,
`.nextgo-test-results/qualification/zones-wave/dev-browser/host.json`, and
`.nextgo-test-results/qualification/zones-wave/development-linux/qualification-final.json`.
The Linux adapter's initial no-execute temporary-script failure was repaired in
the Node harness, retaining the same production artifacts and no-execute mount;
the final 13-case result is the qualified local result.

R15 remains **Partial**. This compiler publishes one aggregate binary with a
shared process and rollout. Independently deployable zone releases, independent
upgrade/rollback and production ingress composition remain goals; separate asset
trees and application configuration do not establish them. Real ingress, TLS,
CDN, AWS, multi-process cache deployment, hostile-tenant isolation, operational
soak, and tenant security certification remain unqualified. All 79 capability
rows and all 11 open release gates retain their separately recorded status.
