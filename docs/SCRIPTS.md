# Managed external scripts and request nonces

The `scripts` package declares explicitly trusted external dependencies; Go controls when they load and how widgets respond. The framework does not evaluate JavaScript strings or permit executable `ui.El("script", ...)` markup. Application state, events and library adapters remain Go code. A dependency runs with the application's browser privileges: choose trusted sources, and do not construct `Src` from untrusted user content.

## Declarations and Go callbacks

```go
// Server file: //go:build !js
library := scripts.Script{
    ID: "map-library-v1",
    Src: "/map-library.js",
    Asset: true,
    Strategy: scripts.AfterInteractive,
}
body := ui.Fragment(scripts.Node(library), widget.View(library))
```

`Node` emits an inert, hidden declaration, so SSR remains useful before Go or the dependency loads. It works inside pages, layouts and streamed leaf boundaries. `Asset: true` resolves `Src` under the configured public asset mount, including a separate asset origin; `/map-library.js` means the application's public `map-library.js` file. Otherwise `Src` is an absolute HTTP(S) URL or a browser-root-relative URL. It is not implicitly prefixed with the application base path.

```go
// Browser file: //go:build js && wasm
return scope.Script(props.Library, func(err error) {
    if err != nil {
        status.Text("The map library could not load.")
        return
    }
    // Use syscall/js to call the trusted library, and register its teardown
    // with scope.Cleanup. Keep application state and event handling in Go.
    status.Text("Map library ready")
})
```

The callback runs after loading or receives an error, including when the source was already loaded by another widget. It runs again for a newly mounted scope, allowing a widget to initialize its own DOM after navigation. A scope's disposal cancels its waiter and suppresses its late callback. It does not undo a library's global side effects or automatically destroy library-created widgets; register that cleanup explicitly.

For imperative Go work, call `client.LoadScript(ctx, declaration)` from a goroutine. It waits for readiness or cancellation. `client.RetryScript(ctx, declaration)` retries a failed download, and reuses a successful dependency without executing it again. Code after success must still check the library's expected API: a resource load event cannot prove that arbitrary library initialization succeeded. Syntax/runtime errors inside a third-party library are not a framework transaction that can be rolled back.

The same APIs work in opt-in [lazy Go modules](LAZY_COMPONENTS.md). Their Go calls delegate to the core document's loader rather than installing another dependency registry in each WASM runtime.

## Timing and ordering

- `AfterInteractive` is the default. The Go runtime starts the request when it sees the declaration or a Go caller requests it. Scripts do not delay hydration of unrelated islands.
- `LazyOnload` waits for document `load`, then requests an idle callback with a one-second idle deadline. Browsers without that API use a short timer. A later after-interactive caller promotes an already waiting shared dependency.
- `BeforeInteractive` is available only in `Config.EarlyScripts`. These classic scripts execute in document parser order before `wasm_exec.js` and `boot.js`. This deliberately simple native ordering can delay HTML parsing and hydration while the early script downloads. It is not a claim of Next.js's nonblocking early-script optimization.

```go
config.EarlyScripts = []scripts.Script{
    {ID: "consent-v1", Src: "/consent.js", Asset: true,
        Strategy: scripts.BeforeInteractive},
}
```

Early configuration is validated, frozen and included in navigation configuration identity. It applies to every document, including custom errors and streamed shells. It cannot be introduced from a page or deferred chunk during soft navigation. Early scripts execute before Go can attach a load/error listener: the Go wait/retry APIs do not claim readiness for those declarations. Check the library's API explicitly before using it. Early module scripts and worker offloading are not implemented.

Independent dependencies can load concurrently. To require application-level ordering, await the first `LoadScript` call before requesting the next. There is no dependency graph or script-consent scheduler in this version. A loaded dependency remains in the document across navigation. The demo at `/scripts`, `/scripts/details` and `/scripts/stream` demonstrates two Go widgets, a lazy Go consumer, reuse, retry and streaming. The small `vendor-*.js` assets model an explicitly trusted external library; they are not the application's state or navigation implementation.

## Identity, integrity and bounds

Scripts deduplicate by canonical requested source URL, module type, integrity, crossorigin and referrer policy. IDs are aliases for that identity. Changing an ID's source or changing security attributes for an already registered source fails instead of silently executing another version. Navigation and streamed chunks validate declarations before adopting their HTML; speculative responses validate them before caching. Deduplication does not identify two distinct URLs that redirect to the same final resource. Version libraries with a stable, immutable URL and a new ID when their contents change.

`Type: "module"` selects a browser module script for after-interactive/idle loading. `Integrity` accepts SHA-256/384/512 SRI hashes; a supplied hash defaults `CrossOrigin` to `anonymous`. Explicit crossorigin values are `anonymous` or `use-credentials`. Referrer policies are validated. There are no inline script bodies, executable attributes, credential-bearing/protocol-relative/data/blob/JavaScript URLs, or fragment sources. Browser CSP, mixed-content, CORS, integrity and MIME rules still govern loading. Server-side Go never fetches these URLs.

Current admission limits are 16 early declarations, 64 dependency identities and 128 ID aliases for one document, 64 active declarative markers, 256 simultaneous Go waiters and four concurrent inserted-script loads. A shared request has a twenty-second deadline, including admission and idle waiting. Sources are limited to 4 KiB and serialized declarations to 8 KiB. Loaded code, its script node and identity remain for the document lifetime. These bounds do not establish a process-memory or library-resource budget.

Canceling an individual waiter preserves the shared load. Removing or timing out a script element cannot guarantee revocation of browser work or JavaScript that already executed. Failed downloads can be retried explicitly; successful code is never reevaluated by retry. Framework-owned load/error listeners and idle callbacks are released after their operation settles or is canceled.

## CSP nonces

The application owns its CSP header. Middleware can generate and bind a request nonce before rendering:

```go
nonce, err := scripts.NewNonce() // 24 random bytes, base64 encoded
if err != nil { /* return a generic server error */ }
ctx, err := scripts.WithNonce(r.Context(), nonce)
if err != nil { /* return a generic server error */ }
w.Header().Set("Content-Security-Policy",
    "default-src 'self'; script-src 'nonce-"+nonce+
    "' 'strict-dynamic' 'wasm-unsafe-eval'; style-src 'self' 'nonce-"+nonce+
    "'; object-src 'none'; base-uri 'none'")
next.ServeHTTP(w, r.WithContext(ctx))
```

`WithNonce` checks syntax and marks the context to bypass framework public caches immediately, including middleware data work. It does not prove entropy for caller-provided values; prefer `NewNonce`. Framework page responses use `private, no-store`. The nonce is applied to runtime scripts, early scripts, framework inline CSS and typed head JSON-LD in both buffered and streamed documents. It is not serialized into public runtime settings or navigation envelopes. `X-Nextgo-Nonce: 1` identifies a nonce-bound response for build tools.

After soft navigation, dynamically created dependency and JSON-LD nodes use the original document's nonce, read through the bootstrap script's nonce property. A nonce newly generated for a navigation response does not replace the active document's policy. Static export rejects nonce-bound documents and preserves the previous publication. Static sites can use an application-owned source/hash CSP policy instead; nonce middleware requires request-time rendering.

The optional demo configuration `NEXTGO_DEMO_CSP=true` installs a nonce policy; `NEXTGO_DEMO_EARLY_SCRIPTS=true` adds two ordered early library fixtures. Production headers must be tailored to actual assets, connections and embedding requirements. General Trusted Types integration, arbitrary library compatibility, strict-CSP development SSE, real-CDN rollout and independent policy/security qualification remain open.

## Verification and parity limits

[Native tests](../managed_scripts_test.go) cover frozen configuration/identity, early order, per-request nonce privacy/cache behavior and buffered/streamed shells. [Descriptor tests](../scripts/scripts_test.go) cover URL/attribute/JSON/integrity bounds. Core/module WASM tests check admission, canceled callers and shared host delegation. [Browser scenarios](../tests/scripts.spec.mjs) exercise useful delayed SSR, DOM identity, live Go controls, cross-runtime deduplication, navigation/cleanup, failed downloads and retry, integrity rejection, idle promotion, module scripts, streaming, malformed/conflicting responses, concurrent-load bounds, mounted/CDN/locale paths and real parser-injected CSP blocking. Development, plain static-host and generated HTTP/both mock Lambda suites include script/nonce checks.

Automatic dependency ordering, arbitrary data/custom script attributes, separate first-load notification semantics, nonblocking early orchestration, early load-error callbacks, consent integration, worker execution, comprehensive third-party adapter lifecycle and complete performance/security/device qualification remain unfinished. [The parity inventory](PARITY.md) retains those limits and every production gate.

The capability baseline follows the official [Next.js Script reference](https://nextjs.org/docs/app/api-reference/components/script) and [CSP guide](https://nextjs.org/docs/app/guides/content-security-policy). The worker strategy is documented there as experimental and unavailable in App Router; it remains an explicit qualification rather than an implemented Go capability.

## Bounded vendor data attributes

Construct an immutable set with `scripts.NewDataAttributes(map[string]string{
"data-site": "example.test", "data-label": "Public chart"})`, handle its error,
and assign the result to `Script.Data`. The descriptor remains comparable in Go.
The constructor snapshots the map; `Values()` returns an independent copy.
Empty settings keep the existing descriptor wire shape.

Only lowercase ASCII `data-*` names are accepted, and the framework reserves
`data-nextgo*`. There are at most 16 attributes, names are at most 64 bytes,
individual values are at most 1024 UTF-8 bytes without control characters, and
canonical encoded settings are at most 4096 bytes. The complete encoded script
declaration must fit 8192 bytes, matching the existing wire-parser bound.
Unknown fields, duplicate properties, null/non-string values and invalid direct
conversions are rejected. Settings are public trusted vendor inputs; do not put
secrets in them. Library-specific interpretation belongs to the selected trusted
library.

Parser-time early scripts receive escaped attributes in deterministic order.
Managed after-interactive/idle loads set the attributes before attaching the
source. Eager and lazy Go callers share the same document loader. Settings form
part of dependency identity and the early configuration digest: another caller
cannot silently change a loaded URL's vendor configuration. The optional field
preserved module ABI 6 at its checkpoint. The combined imports/authority
contract now uses ABI 7; publish paired framework generations as described in
[compatibility](COMPATIBILITY.md). Native ESM imports have a separate descriptor
and reject this unsupported per-script control.

The frozen-source qualification passes 80 native race packages and vet,
12 WASM package runs across four targets, four WASM vet targets and 16 generated
examples per SDK. The new portable scripts tests run directly under WASM.
Fresh artifacts pass 104 Chromium cases over HTTP, buffered/streaming local
Lambda and static hosting, 336 ordinary host/restricted-container executable
cases and 168 race-enabled executable cases. All Go inputs remain unchanged
through the campaign. The 104 browser fixture proofs confirm owned physical
transport teardown, unchanged artifacts and zero source-service requests.

Fresh bundle analysis exactly matches both qualified native binaries. The
production Go 1.27 core is 6,937,167 raw / 1,861,229 gzip-9 bytes, failing both
reference budgets. Go 1.24 is 5,729,355 / 1,530,997 for this fixture and remains
compatibility-only with known standard-library advisories. Production-SDK
advisory conversion exits are zero for the tested native/core/module source
scopes against the authenticated official database; the full source audit is
still open.

A focused parser test caught null being accepted into a string map by the
standard JSON decoder. The implementation now checks raw value types before
conversion; that correction preceded the frozen-source campaign.
[Source/artifact pins and qualification](benchmarks/script-data-qualification.json)
retain the complete scope. The remote workflow requires 208 cross-engine
browser checks and 84 host/container executable cases per SDK; remote results
for this increment are unobserved. Full browser/device/library/security/resource
and hosted-cloud qualification remain open. Earlier checkpoint records retain
their original inputs and counts.

## Pending lazy-host request admission

The document script host now reserves at most 256 pending RPC requests before
scheduling parsing/load workers. It rejects malformed argument counts/types and
overlong declarations as Promise failures. Failed parsing/loading and ordinary
completion release the reservation. Development refresh counts each admitted
request synchronously and waits for it to settle; frozen admission rejects new
requests. The existing 256 consumer bound and 64 dependency/128 alias limits
remain separate. This is a pending Go-work bound, not a JavaScript sandbox or
a guarantee that removing a script element aborts browser execution.

Both SDKs pass focused saturation/recovery and refresh-drain tests plus the
full 12 WASM package runs across four targets, four WASM vets and 16 generated
examples. Fresh artifacts pass 104 Chromium cases across HTTP, both local
Lambda modes and static hosting, 336 ordinary host/restricted-container cases
and 168 race executable cases. All browser transport teardown/artifact proofs
pass. Current advisory conversions are zero in the tested production-SDK
scopes; compatibility-SDK known findings remain.

Only three JS/WASM-tagged Go inputs changed after the script-data checkpoint.
The rebuilt native CLIs and normal/race deployment harnesses match that
checkpoint byte-for-byte. Its 80 native race packages/vet per SDK are retained
as the unchanged-native-input baseline; a new broad native run is not claimed.
Browser-containing executables/static output are rebuilt and tested. Fresh
bundle analysis matches both current server hashes: Go 1.27 core is 6,938,726
raw / 1,861,976 gzip-9 bytes, failing both reference budgets; Go 1.24 is
5,730,998 / 1,530,264 for this fixture and remains compatibility-only.
[Source scope and qualification](benchmarks/script-admission-qualification.json)
retain exact pins. Hosted cloud, complete resource/soak behavior, independent
source security review and remote cross-engine/device qualification remain
open.
