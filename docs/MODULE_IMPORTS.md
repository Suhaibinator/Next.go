# Native JavaScript module imports from Go

`client.ImportModule(ctx, scripts.Module{ID: "chart-v1", Src: "/vendor/chart-module-v1.js", Asset: true})`
returns a native browser module namespace as `js.Value`. Go can read default and
named exports, call exported functions and observe live bindings. Call from an
owned goroutine, such as `Scope.Go` or a widget constructor. The context must be
non-nil. This is an explicit dependency on trusted JavaScript, with Go retaining
application and lifecycle policy.

```go
err := scope.Go(func(ctx context.Context) {
    module, err := client.ImportModule(ctx, scripts.Module{
        ID: "chart-v1", Src: "/vendor/chart-module-v1.js", Asset: true,
    })
    if err != nil || ctx.Err() != nil {
        return
    }
    version := module.Get("libraryVersion").String()
    // Use the exports while this component still owns its UI/resources.
    _ = version
})
```

`Asset: true` resolves a logical public asset through the configured asset prefix
and base path. Ordinary same-origin paths and explicit HTTP(S) sources use the
same URL validation as managed scripts; credentials, fragments, data/blob URLs
and protocol-relative sources are rejected. IDs are stable aliases: rebinding
an ID to a different resolved source fails. Query strings remain part of URL
identity. `scripts.ParseModule` rejects unknown fields and malformed/duplicate
JSON properties.

Native `import()` handles module graph fetching, CORS, evaluation, top-level
await and its native namespace semantics. The generated bootstrap supplies one
arrow-function bridge; no eval, generated Function, bundler or application
JavaScript loader is required. Native semantics for an export named `then`
still apply. The lazy-component RPC wraps a returned namespace to avoid a
second Promise assimilation.

The persistent document core shares one namespace and physical import per
resolved root URL across eager and lazy Go components. It retains up to 32 root
URLs, 128 aliases and 256 current consumers; the host RPC also admits at most
256 pending requests. Failed URLs stay reserved and fail again without another
physical import. Recovery requires a new versioned source URL and identity or a
new document. Cancellation ends a consumer's wait; native module evaluation,
top-level await and transitive fetches continue. Their callbacks retain a
development refresh activity lease until settlement. A never-settling import
keeps its bounded slot and prevents a clean refresh drain.

These are root/request bounds, not limits on the browser's transitive module
graph, JavaScript heap or module map. Modules cannot be unloaded, and framework
ownership does not sandbox trusted library code. Avoid mixing execution policies
for a URL: known root URLs already registered with the managed script loader
are rejected by ImportModule, and imported root URLs are rejected by LoadScript.
Transitive dependencies remain browser managed and are not all enumerated by
these policy checks.

`Module` deliberately has only `ID`, `Src` and `Asset`. Native dynamic import
does not offer this API's per-script integrity, crossorigin, referrer or timing
controls. Unsupported properties are rejected rather than silently ignored.
Use an appropriate response CSP and trusted versioned assets. This API does not
claim SRI for the transitive graph. Applications requiring managed element/SRI
controls can use [LoadScript](SCRIPTS.md) for its separately documented behavior.

`WidgetOptions.Modules` imports dependencies after its ordered Scripts list,
before detaching the SSR fallback. The combined dependency count is at most
16. The constructor can call ImportModule again to read the cached namespace.
`RetryFailedScripts` applies only to the Scripts list; failed native module URLs
retain the native import policy above. Resource destruction still belongs in
the widget scope's Cleanup callback. See [widget ownership](WIDGETS.md).

The import checkpoint introduced private module ABI **6**. The combined
imports/document-authority contract is now **7**. Rebuild the CLI, eager core,
every lazy module and every development entry together with the same selected
Go SDK and its paired runtime support. Mixing a new Go client with an older CLI
bootstrap lacks the import bridge and fails the import. Content-addressed
runtime identities include the generated bootstrap. Navigation remains version
3; action/action-view and the development extension remain version 1.

## Qualification scope

The [widget example](../examples/widgets/README.md) uses the original uPlot
1.6.32 ESM file through a tiny application adapter. It demonstrates a transitive
import, the default constructor, named exports and a live mount counter shared
across eager/lazy SPA navigation. Its native responses use a fresh nonce,
`strict-dynamic` and `wasm-unsafe-eval` for scripts; inline element styles are
allowed for uPlot. Static output requires a deployment-supplied response policy
and does not inherit those native CSP headers.

The frozen-source two-SDK campaign passes 80 native race packages and vet,
11 WASM package runs across four targets, four WASM vet targets and 16 generated
example checks per SDK. The native snapshot preceded one JS-only test
correction: its bridge expectation now requires ABI 6 and rejects ABI 5 as well
as ABI 3/4. The corrected full WASM matrix passes both SDKs.

Fresh pinned artifacts pass **104 Chromium cases** (52 per SDK across HTTP,
buffered/streaming local Lambda and static hosting), **312 ordinary executable
cases** (78 host plus 78 restricted-container cases per SDK) and **156 race
executable cases**. All 104 browser fixture proofs check physical owned transport
teardown and unchanged artifacts with zero source-service requests. Two more
Chromium checks perform real compiled development replacements, preserving two
components' states, original nodes, focus, URL and native process, while each old
runtime exits before its successor starts. Both owned development CLIs exit 0,
remove compiler output and close observed listeners.

The Go 1.27 production core measures **6,919,617 raw / 1,854,612 gzip-9 bytes**;
both reference budgets fail. Go 1.24 measures **5,714,912 / 1,526,731** and fits
this fixture, but remains compatibility-only with known standard-library
advisories. Fresh native bundle analysis exactly matches both qualified server
hashes. Automated advisories report no known findings in the tested Go 1.27
native/core/module source scopes against the authenticated official database;
they are separate from the still-open source security audit.

Failure provenance is retained: an old CLI initially lacked the new bridge;
the first broad WASM run exposed the old ABI-5 test expectation; a concurrent
Go 1.27 run timed out in an existing 20ms form-validation test. That test passes
five isolated repeats and the subsequent complete WASM run. No runtime fix or
passing broad result is inferred from the earlier failures.

[Machine-readable qualification and pins](benchmarks/module-imports-qualification.json)
retain the exact source/artifact/report scope. The configured remote CI matrix
requires 208 checks across 16 browser/transport projects, plus both-SDK
host/container executable cases. Remote CI, Firefox/WebKit and physical-device
results for this increment have not been observed. This implementation is a
bounded interoperability subset. Browser/physical-device coverage, complete
third-party compatibility, resource soak, source security review and production
release gates remain open in [PARITY.md](PARITY.md).
