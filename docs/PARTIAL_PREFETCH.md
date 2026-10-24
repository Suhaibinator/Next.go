# Public-shell prefetch and request-time resume

An explicitly partial route can prefetch its **public fallback shell** and use
that HTML in a later server-validated streaming navigation. Private regions are
never prefetched or placed in the browser route cache. This concrete-path
optimization is implemented. Native/WASM and standalone executable checks pass;
the final browser matrix passes 40 focused checks. It does not implement a session-scoped App Shell or
the full Next.js partial-prefetch/Cache Components model.

Declare the route with `PrerenderPolicy.Partial` and `ui.RequestRegion` as in
[partial prerendering](PARTIAL_PRERENDERING.md). Existing pointer/focus intent
and `client.Prefetch(target)` negotiate the optimization automatically. No
additional endpoint, application JavaScript or client region implementation is
needed. Development and existing private/cache-bypass conditions skip this
speculation.

## Prefetch contains only public output

An eligible request sends these headers:

```http
X-Nextgo-Navigate: 1
X-Nextgo-Purpose: prefetch
X-Nextgo-Shell-Protocol: 1
```

The response is a versioned navigation JSON envelope with the frozen public
fallback HTML, static title/head/document metadata and `shell`, a 64-character
lowercase hexadecimal generation digest. It does not contain private region
output or run a region callback. Prefetch can therefore work while a region's
provider is failing. Ordinary request middleware and access rules still run;
the optimization grants no right to fetch a protected route.

The response remains HTTP `private, no-store`. A separate Go-owned memory-cache
opt-in requires `X-Nextgo-Prefetch: shell`, `X-Nextgo-Shell-Protocol: 1` and a
single valid `X-Nextgo-Shell-Stale` duration in milliseconds. Positive reuse is
at most **30,000 ms**, clipped to the remaining shell freshness/hard deadline,
with lookup/marshalling and client request time deducted. Zero means no browser
retention. This is not HTTP/CDN caching or a guarantee that server content
remains unchanged during that interval.

Only this negotiated public shell enters the existing **32-entry / 8-MiB
accounted LRU**. It shares capacity with ordinary eligible route entries; it
does not add another unbounded cache. Shell retention rejects `Set-Cookie`,
`Vary: *`, `Age`, public cache directives, missing private/no-store policy,
duplicate/malformed capability or lifetime headers, invalid digests and
out-of-range lifetimes. Generic full-page privacy rules are unchanged. Existing
refresh/mutation cache-epoch invalidation applies; there is no remote
invalidation subscription or session-change detection.

A speculative envelope over **2 MiB of serialized JSON** is skipped with 204,
without running request regions. A client without explicit shell-protocol
capability also keeps the preceding 204-prefetch behavior. The shell feature
does not make an otherwise ineligible private response cacheable.

## Every resume still contacts the server

A click or managed history navigation never commits a cached partial shell
without a fresh server request. The client advertises capability again and may
propose its cached generation:

```http
X-Nextgo-Navigate: 1
X-Nextgo-Stream: 1
X-Nextgo-Shell-Protocol: 1
X-Nextgo-Shell: <64-character cached digest>
```

Middleware, access checks, current shell selection, stream admission and route
ownership validation run before committing a view. The digest binds the public
body, static metadata/head/document, region descriptors/props, canonical path,
route, build/configuration and generation timestamp. It is reference metadata,
not an authorization/session token, integrity signature or private-data grant.

If the proposed generation still matches, the ordinary stream's `shell` frame
can carry `shellRef: true`, the matching `shell` digest and `html: ""`. It still
carries current URL and static metadata. The client pins its cached HTML plus
cache epoch, requires an exact digest/build/configuration/URL/metadata match,
then restores the HTML and performs the normal detached DOM/head/ownership
validation before commit. An epoch change before commit rejects the reference.
Fresh private region chunks follow through the existing stream protocol.

If the generation changed or the hint is absent/invalid, the server sends a full
current shell frame instead. There is no new stream-frame version: references
are permitted only under explicit shell-protocol negotiation on both requests.
Legacy buffered navigation resolves the live regions and contains no shell
reference markers. Direct URLs still receive useful server HTML and live
regions through the ordinary initial-document transport.

Malformed, forged, missing-cache, metadata-mismatched or invalidated references
fail before adoption, preserve the current view and discard the local hint;
there is no hidden retry that silently trusts or replaces the reference.
Normal user retry can fetch again. Build/configuration skew still performs
document recovery, access denials retain their existing behavior and validated
busy 503 responses preserve the view through the usual recoverable path.
Transport errors after a valid shell commit use the existing streamed-content
retry behavior.

This revalidation occurs per request. The framework cannot recall private bytes
already emitted or infer a session change after a response was authorized.
Applications remain responsible for middleware and backing-data authorization,
and for protecting a region's data before reading or returning it.

## Inspect the example

Build and run the demo, open `/partial`, then focus or hover a link to a known
partial path. In browser network tools, the prefetch has the shell capability,
fallback HTML and no `data-partial-request-id`; the subsequent active navigation
still makes a server request and receives fresh request-time output.

The protocol can also be inspected without WASM:

```sh
curl -i \
  -H 'X-Nextgo-Navigate: 1' \
  -H 'X-Nextgo-Purpose: prefetch' \
  -H 'X-Nextgo-Shell-Protocol: 1' \
  'http://127.0.0.1:3060/partial/alpha?message=Current-request-value'
```

The query can affect the later request region, not the captured public shell.
`NEXTGO_DEMO_PARTIAL_REGION_FAIL=true` at runtime demonstrates that shell-only
prefetch does not call that provider; active region rendering then deliberately
fails. Do not treat a successful shell prefetch as region/service readiness.

## Verification and remaining work

Both Go 1.27.3/macOS and Go 1.24.13/Linux native race/vet runs pass, with
matching core/module WASM tests and vet. Complete host and nonroot read-only
Linux standalone HTTP/buffered-and-streaming mock Lambda suites pass, including
actual-process shell prefetch/resume. Native server/header/lifetime audits and
browser cache/reference checks cover the negotiated boundaries.

The final observer fixtures pass **40 focused browser checks**: 10 on host
Chromium and 30 on Linux Firefox/WebKit/touch WebKit. The initial Linux run also
passed 30 checks; it is excluded from this final count.
Prior launch-path/CDP-observer failures remain in the logs under
`.nextgo-test-results/qualification/partial-shell/`. Preceding 377-check partial
rendering results describe earlier source and are not a current broad regression
count. This focused matrix does not establish a production release claim.

The historical remote `95b52ee` browser job retained 334 passes and one hanging
cold-skew case in its 335-case scope; the other four jobs passed. Fetch cleanup
now distinguishes completed EOF from cancellation so consuming an HTTP response
does not abort it afterward. Matching WASM tests cover consumed/error/canceled
responses, and local skew diagnostics pass 20 checks. These focused results do
not make the old remote run pass, and the new checkpoint has not yet run in CI.

This supports public shells for **concrete route paths**. Unknown parameters
still use existing per-path shell generation/fallback policy. Session-scoped
App Shell reuse, parameter-independent shells, automatic request-data analysis,
automatic cached components/nested lifetimes, viewport prefetch, speculative
private phases and coordinated invalidation across tabs/replicas/CDNs remain
future work. All 79 capability rows and 11 release gates remain tracked in
[the parity inventory](PARITY.md).
