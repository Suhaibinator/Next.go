# One explicit public static client shell

**Experimental.** Dedicated Linux AMD64 functional CI passes on both SDKs for
the checkpoint recorded below, including actual exports, browser navigation and
fixture closure. Earlier static navigation, scalar metadata and ClientIsland
evidence retain their own checkpoint scope. This bounded result does not
establish full SPA parity or close a capability or release gate.

## Declare a generic public subtree

Ordinary static export already publishes concrete HTML and navigation JSON for
declared paths. This opt-in lets one explicitly declared optional-catchall
subtree reuse a single public build-rendered document at unenumerated deep URLs:

```go
func Configure(c *nextgo.Config) {
    c.StaticShell = &nextgo.StaticShell{
        Pattern: "/workspace/*rest?",
        Component: "workspace",
    }
}
```

`Pattern` must select an existing page exactly and have a nonempty literal
prefix followed by one terminal optional catchall. A root-wide `/*rest?`, dynamic
prefix, multiple shell declaration or overlapping route/endpoint/handler/document
ownership is refused. The zero-tail seed is derived from the pattern:
`/workspace`, placed through the ordinary BasePath/trailing-slash policy. The
exporter explicitly includes that seed even without a `StaticParams` enumerator.
There is no independently configurable seed override.

`Component` names exactly one discovered eager or lazy browser component. Its
native seed page must render exactly one matching `ui.ClientIsland` with useful
fallback and public constant props, using the existing [ClientView contract](CLIENT_ONLY.md).
It must not contain another ordinary island, layout/selection/slot/template/
stream/region marker or nested framework declaration. Native HTTP routing is
unchanged: this is an export declaration, not a request-time rewrite.

The declaration is explicit permission to publish the generic build bytes.
Native Go factories, middleware and custom nodes are trusted application code;
this is not a sandbox or automatic secret/request-dependency analysis. Do not
capture account data, authentication, request cookies, query-specific records or
per-deep-path native props in the seed. Set-Cookie/authentication challenges are
refused during capture, but absence of those headers does not prove public data.
A framework-default no-store header is not a privacy detector either.

## Read the actual committed browser route

The new portable helper `Location.RouteParams(pattern, basePath)` returns an
owned decoded parameter map for the visible escaped pathname. The browser
convenience `client.CurrentRouteParams(pattern)` uses the committed location and
application base; it returns false before location is ready. Both eager and lazy
components use the existing core-supplied location/path state, without a new
module host method or ABI change.

Inside the component's synchronous Mount, before `scope.ClientView`:

```go
location, ready := client.CurrentLocation()
params, matched := client.CurrentRouteParams("/workspace/*rest?")
if !ready || !matched {
    return fmt.Errorf("workspace requires a committed workspace URL")
}
rest := params["rest"] // decoded once; empty at /workspace
query := location.Query() // independently owned, repeated values available
```

A pattern match is not authorization or proof of which page is active. Query
parsing retains the existing Go net/url behavior; raw `Search` and `Hash` remain
available separately. Do not read deep params from native seed `Context.Params`,
public props or a seed selection snapshot. The runtime publishes the committed
visible URL before Mount, including the initial deep URL.

Cross-page/query navigation uses normal Go commits and remounts the view's local
state. Hash-only changes use the existing same-page location observer flow;
`Replace`, back/forward and same-target requests keep their existing policies.
There is no new retained-layout state, client PageLoader/data lifecycle or
implicit request to rerun the native seed for query values.

## Publication and compatibility

The opt-in uses native `ExportConfig` version 3 and `.nextgo-export.json` format
`nextgo-static-v3`, carrying static-shell descriptor version 1. These are separate
contracts from navigation protocol 3 and module ABI 5, which remain unchanged.
Without `StaticShell`, native export configurations 1/2 and ordinary published
static formats 1/2 keep their existing meanings and parser extension behavior;
version 3 adds strict shape/duplicate checks, not retroactive strictness for 1/2.
Older CLIs must refuse unsupported version 3 rather than silently omit the shell.

Use a fresh paired CLI/framework/core/lazy build. Descriptor 1 accepts only the
actual CLI-generated 20-lowercase-hex build ID; this restriction does not change
manual `Config.BuildID` behavior in native HTTP or ordinary exports. The descriptor
binds the build/configuration, pattern/component, seed URL, exact immutable
navigation-envelope URL, byte count and SHA-256. It is embedded in every exported
HTML document, so an ordinary page in that artifact can enter the declared
subtree. The immutable envelope is published at:

```text
<base>/_nextgo/<build>/static-shell/<sha256>.json
```

The descriptor is bounded to 16 KiB and the envelope to 8 MiB. That URL stays
application-mounted and same-origin even with a separate AssetPrefix. The
stored envelope still names the seed; only a validated local navigation copy
is rebound to the actual target. Exact bytes are hashed before decoding and
normal protocol/status/build/configuration/document checks still apply. Hashes
and configuration IDs detect skew; they are not signatures or access control.
All output is staged before replacement. Validation or write failure preserves
the preceding complete export under the existing publication rules.

The seed head must remain generic. Canonical/alternate/hreflang links, `og:url`,
JSON-LD, nonce-dependent output and deferred metadata are refused. Valid constant
title/description/robots, social text/images, document settings, icons/manifest
and ordinary stylesheet/font-preload resources retain their existing validators.
No per-record metadata or native status is invented for a reused deep URL.

## Static-host contract

Export emits routing information; it does not configure a CDN or install a
universal try-files rule. A host adapter must follow the actual declaration:

1. Reject methods other than GET/HEAD before any artifact lookup, including
   exact files and ordinary documents. Then validate raw escaped paths before
   provider normalization. Require literal complete BasePath and shell-prefix
   spellings; encoded mount/prefix aliases,
   invalid UTF-8, dot/empty segments and encoded separators are not aliases.
2. Serve exact retained public/runtime/data/metadata files and declared ordinary
   documents first, with their proper MIME and status.
3. Only after an exact miss, permit GET/HEAD document requests in the exclusive
   declared subtree to use the seed HTML while preserving the visible URL.
   Require a positive `Accept: text/html` quality and reject a conflicting
   `Sec-Fetch-Dest` when present. Mutation methods and reserved API/runtime/asset
   namespaces cannot select the HTML shell.
4. Apply the virtual-document suffix rule below, keep unknown outside paths as
   actual static 404s and preserve the original query/fragment behavior.

For virtual requests only, the final decoded segment must not end in one of
`.json`, `.wasm`, `.css`, `.js`, `.mjs`, `.cjs` or `.map`, case-insensitively,
after removing at most one framework terminal slash. Thus `report%2EJSON` and
`report.json/` are refused; `notes.v2` and `notes.json/details` remain eligible
routes. A seed ending in a denied suffix is also invalid. This is exactly seven
suffixes, not a general file-extension classifier. Exact retained files and
ordinary exported documents win first; native HTTP and general RouteParams
matching are unchanged by this static-shell-only predicate.

The browser independently requires a positive descriptor match and the first
matching manifest page route. It fetches the immutable envelope directly; an
arbitrary ordinary `/_nextgo/data/...json` 404 never activates a shell fallback.
An owned deep document is generic HTTP 200. A client missing-screen cannot turn
it into HTTP 404 or supply native per-record SEO. Outside-prefix unknowns remain
real static errors. The reviewed reference host is a qualification fixture, not
a provider-independent deployment adapter; operators must implement and qualify
these rules on their chosen host.

## Deliberate exclusions and failure behavior

Version 1 refuses a selected route with layouts, layout IDs/bases, slots/primary
selection, request regions, page Cache policy or Prerender policy. Application
configurations with retained layouts, parallel routing, locales/domains, managed
offline/draft, request redirects/rewrites/header rules, server actions or optimized
images are also refused. Streamed/deferred/partial seed output, runtime-server
dependencies and request nonces remain rejected. Ordinary pages outside the
subtree retain their existing export behavior; this is not a general loosening
of those guards.

The browser needs the paired Go/WASM runtime and its normal modern browser
fetch/URL/DOM capabilities. Without WASM, the useful generic fallback remains.
A malformed descriptor keeps that fallback and reports an error without an
automatic reload loop. A missing, corrupt, wrong-MIME or mismatched immutable
envelope fails the navigation and preserves the current view through existing
cancellation/history handling; it does not silently borrow an old page or retry
another JSON endpoint. Missing core/lazy assets still require operator repair.
There is no automatic deployment-generation retention or universal cloud recovery.

## Sample and qualification boundary

The [isolated sample](../examples/static-shell/README.md) has one ordinary
home page plus `/workspace/*rest?`, public fallback/props and an eager or lazy
Go view. Operator settings select `NEXTGO_STATIC_SHELL_MODE=eager|lazy` and an
optional `NEXTGO_STATIC_SHELL_BASE=/mounted`. The view reads actual deep params,
query and fragment, exposes local Go state and renders an honest client missing-
screen. It has no private records, layouts, server APIs or application JavaScript.
An export example is:

```sh
NEXTGO_STATIC_SHELL_MODE=eager go run ./cmd/nextgo export \
  -out /tmp/nextgo-workspace-static ./examples/static-shell
```

For the earlier candidate source `6138a69f`, focused 14-command native/WASM/
CLI-export/vet checks pass on Go 1.24.13 and 1.27.3. The Go 1.27.3 Linux ARM64
producer then completes seven commands: a fresh CLI, real generate/check, and
four actual eager/lazy root/mounted exports. Their HTML, navigation envelopes,
descriptor and runtime inventories are recorded. The emitted route helper and
three action-reference helpers come from that real generator, not handwritten
substitutes. In that earlier local campaign, Go 1.24 sample exports never
launched because resource admission failed, and no static-shell browser cases
ran. Earlier admission and generator-allowlist failures remain separate failed
attempts, not retroactive passes.

On October 27, 2026, the [dedicated public run 40454404997](https://github.com/Suhaibinator/Next.go/actions/runs/40454404997)
succeeded for head `7926f225653f45cb44267c2e3beff9043fdddd68`, actual PR checkout
merge `a5e768c1eb0df182d50c1bdcf668c30f2be35d92`, source SHA-256
`641f13abfbdb510607577aeefdb732b29d0caa2790e1fbc46cdfc277b167fb33`.
The [Go 1.24.13 job](https://github.com/Suhaibinator/Next.go/actions/runs/40454404997/job/122024256317)
and [Go 1.27.3 job](https://github.com/Suhaibinator/Next.go/actions/runs/40454404997/job/122024256653)
each completed seven producer commands: a fresh CLI, real generate/check and
four eager/lazy root/mounted exports. Check-mode generation wrote nothing, and
the inventory retained exactly four actual generated helpers. Each SDK
actually discovered and passed all 48 browser cases, with zero skips or
retries, across Chromium, Firefox, WebKit and touch WebKit profiles. These are
96 observations across the two SDKs over 48 case identities per SDK.

All 96 case records verify joined fixture server/socket/handler and other
tracked cleanup work, zero remaining tracked counts and a post-close
`ECONNREFUSED` listener probe. Both browser child processes closed with exit 0
and empty process groups, without TERM or KILL. These are the bounded physical
closure checks in the [CI proof contract](../tests/STATIC_SHELL_CI.md), not
cgroup, memory/load/soak or arbitrary application-cleanup guarantees.

The [preceding public static-shell run](https://github.com/Suhaibinator/Next.go/actions/runs/40449613378)
at `cb8cabbc` remains failed: each SDK stopped at its first fixture-closure
proof failure, with zero passing cases, one failure and 47 not run. The later
success does not relabel that attempt. Only run 40454404997 is counted here;
other pending/duplicate runs, core jobs and earlier broader browser totals are
separate evidence. This Linux AMD64 functional result uses the reference static
host and does not qualify the held ARM64 resource profile or a provider/cloud
deployment.

The feature remains experimental. General client-page loaders, auth/private
data, multiple/root-wide/locale shells,
retained route ownership, per-record status/SEO, real provider/cloud/device/soak
qualification and production maturity remain open. All 79 capability IDs and
11 open gates remain.
