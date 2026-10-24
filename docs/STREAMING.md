# Streamed rendering and navigation

A page can return an immediate shell with explicit deferred Go components. Initial document requests receive HTML and inert template chunks. Go/WASM navigation requests can receive a validated shell followed by independently completed boundary frames. The framework adds no JavaScript rendering logic for this transport.

```go
func Page(c *nextgo.Context) (nextgo.Page, error) {
    return nextgo.Page{
        Title: "Dashboard",
        Stream: true,
        Body: ui.Fragment(
            ui.El("h1", nil, ui.Text("Dashboard")),
            ui.Defer("report", ui.Text("Loading report…"),
                func(ctx context.Context) (ui.Node, error) {
                    report, err := loadReport(ctx)
                    if err != nil { return nil, err }
                    return Report(report), nil
                }),
        ),
    }, nil
}
```

The page function, layouts and metadata must finish before the shell can be sent. Put slow region-specific work inside the deferred loader. Authenticate and authorize the page before returning its shell. Response headers, cookies, redirects and HTTP status are sealed before deferred rendering; a late failure cannot change the committed HTTP status.

## Independent boundaries

IDs must match `[A-Za-z][A-Za-z0-9_-]*`, contain at most 128 bytes and be unique across the complete stream. A loader can return more `ui.Defer` nodes. Its parent chunk is emitted first, containing immediate child fallbacks; those child loaders then run independently alongside other work. A slow nested region does not hold up a sibling region.

Fallbacks must be immediate nodes and cannot themselves contain a deferred boundary. Ordinary non-stream rendering, including legacy navigation JSON, still waits for final content. `ui.RenderEvents(ctx, node, emit)` exposes shell/boundary events for custom server integrations; emit callbacks run on the calling goroutine and apply backpressure. `StreamEvent.Err` is server-only diagnostic data and excluded from JSON; its HTML is a generic accessible error message. `ui.RenderStream` adapts those events to the initial document's template transport.

The framework schedules at most 128 boundaries, 16 concurrent loader callbacks and 16 levels of deferred nesting per stream. Framework-owned HTML buffers allow 2 MiB per shell/chunk and 8 MiB total. A bad initial tree produces no shell. A failed or oversized late boundary normally becomes generic error content; a stream-level limit or destination error ends the stream. Callbacks must observe their context. Cancellation signals cooperative Go work; it cannot forcibly terminate application callbacks or make external side effects transactional.

## Initial document completion

An HTML parser can expose an opening `<template>` and partial inert content
before receiving its closing tag. Go waits for a hidden
`span[data-nextgo-chunk-ready]` outside the template, as its next element sibling
with the same boundary ID. The server emits this marker after `</template>`.
Only then does Go dispose the fallback, move the template's existing nodes into
the boundary, remove the template/marker and hydrate its islands. Partial
content remains inert and the fallback stays visible; no patch script is added.

The body's hydration-ready attribute describes the currently hydrated shell,
not every deferred region. Wait for a specific deferred island to hydrate before
using its controls. This marker is separate from navigation's JSON `done`
record; the navigation transport and validation are unchanged.

## Application-wide admission and deadlines

`Config.Streaming` bounds progressive-page rendering across one application,
including all its locale mounts and domain aliases:

```go
config.Streaming = nextgo.StreamingOptions{
    MaxStreams: 32,
    MaxLoaders: 64,
    Timeout: 30 * time.Second,
}
```

Zero fields select those defaults. Explicit values allow 1–1,024 admitted
renders, 1–4,096 physically running deferred callbacks, and a 1-ms–5-minute
render timeout. One limiter is created before locale applications, so changing
locale or domain does not create another allowance. Per-stream boundary,
concurrency, depth and byte limits still apply independently.

Admission occurs for a successful `Page.Stream` render after page, layout,
metadata and prepared-head work, and after an intent-prefetch request has been
skipped with 204. It covers full documents, negotiated navigation streams,
legacy buffered navigation JSON and HEAD rendering. It does not bound the
earlier page/layout/metadata functions, ordinary pages, API/custom handlers,
application-created goroutines or allocations, or other processes.

Stream admission never queues. When full, a document receives **503**,
`Retry-After: 1`, `Cache-Control: private, no-store` and
`X-Nextgo-Stream-Admission: busy` before any shell is committed. Navigation
validates the complete owned JSON response before treating this marked 503 as
a recoverable busy error, preserving the current view. An ordinary unmarked
503 retains its normal response behavior; incompatible build/configuration
still uses full-document recovery. The marker is not a way to bypass envelope
or head/ownership validation.

Admitted workers wait for deferred-loader capacity within their render context
deadline. A callback holds its physical loader slot until it returns or panics,
even after cancellation; canceling a request cannot release a still-running
callback's capacity. Progressive `RenderEvents` can return at its deadline
while such a callback remains blocked. Legacy JSON and HEAD paths invoke
deferred work synchronously, so a noncooperative callback retains both its
render and loader slots until it returns. Callbacks must honor context and
propagate deadlines to their dependencies. These are concurrency/deadline
controls, not forced Go-code termination or a heap/RSS guarantee.
An earlier request deadline still wins; increasing the server timeout does not
extend navigation's separate thirty-second active-reader deadline.

Custom rendering integrations can share `ui.NewStreamLimiter(maxStreams,
maxLoaders)`, set their own context deadline, call `Begin(ctx)`, pass the returned
context to `ui.RenderEvents`, `ui.RenderStream` or `ui.HTMLContext`, and defer
the returned release function. `Begin` fails immediately with
`ui.ErrStreamCapacity`; release is idempotent and never frees an active
callback's loader slot. Direct render calls without an admitted context retain
their existing per-stream bounds rather than inheriting an application limiter.

A deferred callback must not synchronously render another deferred callback
through the same active limiter: this returns `ui.ErrStreamReentrant` instead
of waiting on capacity it already holds. Returning a node containing nested
`ui.Defer` remains supported; its child callbacks are scheduled after the
parent callback returns.

Use a lightweight handler for readiness and monitor streaming saturation
separately; ordinary handlers remain reachable when stream capacity is full.
A busy page is a transient admission result, not proof that restarting a
healthy application will resolve downstream blocking. Limits apply per app
instance, so replicas still need an explicit process-wide/deployment resource
and fairness policy. Representative concurrency, stuck-dependency recovery and
long-running memory/soak qualification remain open.

## Navigation behavior

The browser keeps the current view while waiting for a valid shell. Before committing, it validates response origin, protocol, build/configuration identity, draft state, URL, document/head metadata and boundary declarations. Body, title, head and history use the same commit path as ordinary navigation, including optional view transitions and compatible layout retention.

Once committed, each boundary has its own busy state. Completed islands hydrate and become usable while other regions are pending. Loaded boundary state survives other boundary completions. Incoming chunks cannot introduce scripts, document metadata or managed route/layout/slot ownership. Their registered boundary IDs are checked before DOM changes, and old fallback islands are disposed before replacement. This supports deferred leaf UI inside the current route tree; independently streamed route/slot ownership requires further protocol work.

Back/forward, refresh, superseding navigation and native preview freezing cancel the active reader. Late bytes cannot update a newer navigation. A canceled unfinished boundary gets generic interrupted content; completed regions remain usable. A transport/protocol failure after shell commit preserves the committed route and offers a Go-powered **Retry loading content** button. Retry refreshes the current route, revalidating server output and resetting replaced page islands under the existing refresh rules. It does not roll back a server write or automatically replay mutations. Before-shell failures retain ordinary navigation recovery; incompatible deployments load a fresh document.

Failures returned by a deferred loader do not expose their details to the browser. Server logs retain diagnostics. Generic boundary errors keep the already committed 200 status; validate access and status-changing decisions before streaming. Full independent access-interrupt/error-boundary semantics remain outside this implementation.

## Transport and compatibility

Active navigation negotiates `X-Nextgo-Stream: 1` alongside `X-Nextgo-Navigate: 1`. Streaming pages return `application/x-nextgo-stream+json`, the stream version header, the ordinary navigation protocol/configuration headers, and private/no-store policy. Each newline-delimited record is a versioned `shell`, `chunk` or `done` object. Shell page data uses the existing navigation envelope. A parent frame precedes frames targeting its newly declared children.

The reader accepts arbitrary byte splits, including splits within UTF-8 text. It rejects duplicate/unknown fields, invalid encodings/IDs, excessive depth, missing/duplicate shells, duplicate or unknown boundary completions, trailing data and incomplete EOF. Every successful stream requires an explicit completion record and no unresolved boundaries. Limits are 2 MiB per encoded frame, 8 MiB total encoded bytes, 130 frames and a thirty-second active-navigation deadline. JSON escaping counts toward encoded frame limits, so a raw HTML buffer that fits its UI limit can still exceed the transport limit. An incompatible header/build/configuration uses full-document recovery before shell adoption.

Clients that do not negotiate streaming receive complete buffered JSON as before. Static exports continue to use their existing complete JSON files. Stream pages bypass reusable navigation/page responses. Intent prefetch requests identify their purpose; a streaming page returns a no-store 204 without running deferred UI loaders, allowing the actual navigation to request its shell promptly. Page functions and metadata can still perform work before that decision.

The native writer flushes complete records, and the existing compression wrapper preserves those flushes. Proxies, CDNs and hosting platforms can still buffer them. Buffered Lambda mode returns the valid sequence after collecting it; optional Lambda response-streaming mode can deliver records incrementally. Local generated-binary tests cover both; actual AWS/CDN behavior remains unverified.

Explicit [partial prerendering](PARTIAL_PRERENDERING.md) can reuse a captured
public shell and stream named request-time regions through this transport.
Those callbacks remain fresh/private; static metadata/status and the same
admission/deadline limitations apply. See its qualified subset and remaining
work in the partial-shell contract.

## General browser streams

`client.FetchStream` shares `Fetch` options, same-origin credentials, redirect policy, byte admission and abort semantics. It calls a header callback before delivering owned byte slices, applies callback backpressure and returns an empty `Response.Body`. A callback error stops the reader. Call it from a Go goroutine, rather than blocking a JavaScript event callback. The default decoded response limit remains 8 MiB. Applications remain responsible for their own data schema, authorization and consistency.

## Evidence and remaining qualification

[UI tests](../ui/stream_test.go) cover parent-before-child ordering, independent siblings, context propagation, generic errors, concurrency/size/count limits and failed-writer cancellation. [Codec tests](../internal/navigation/stream_test.go) feed individual bytes and reject malformed/truncated records. [HTTP tests](../navigation_stream_test.go) prove shell-before-loader completion, disconnect cancellation, prefetch suppression and legacy JSON behavior. [WASM tests](../client/fetch_js_test.go) verify header/body order, callback failure and byte admission. [Browser scenarios](../tests/navigation-stream.spec.mjs) exercise real progressive DOM/hydration, history, detached listener cleanup, corrupted transports, retry, deployment mismatch and mounted/slash/locale ownership. [Lambda executable checks](../tests/deployment/navigation_stream_test.go) verify frame ordering in both transports and early shell delivery in streaming mode.

Remote CI at checkpoint `ac654be` passed both native SDK jobs and the container
job, but its broader browser job finished with **952 passes, three skips and
two initial-stream hydration failures**. A deterministic Chromium fixture
reproduced premature adoption of an incomplete parsed template. The outside
completion marker fixes that race. The
[parser-split fixture](../tests/stream-parser.spec.mjs) holds real server HTML
before the island, inside its markup and after `</template>` but before the
marker, checking fallback retention, final control hydration and shell identity.
Focused verification passes **20 checks: five host Chromium and fifteen Linux
Firefox/WebKit/touch WebKit**. These focused results do not replace the failed
remote run or establish a complete release-browser matrix.

### Admission qualification (October 24, 2026 UTC)

The frozen admission source passes full native race/vet on Go 1.27.3/macOS and
Go 1.24.13/Linux, matching core/lazy-module WASM tests and client vet, host module
vet, demo/workspace generation checks and fresh host demo/workspace/cache builds.
Complete host and nonroot read-only Linux HTTP, both mock Lambda transports and
durable/shared-seed executable suites pass. Focused native checks repeated
three times cover shared locale capacity, every response representation,
fail-fast responses, cancellation and physical callback-slot retention.

The final browser matrix passes **329 checks / three existing Chromium-only
CDP skips on other engines**: 83 host Chromium checks (61 production, 13 navigation
streaming, five parser and four admission), and 246 Linux Firefox/WebKit/touch
checks (180 production, 39 navigation streaming, 15 parser and 12 admission).
Each completed suite is counted once. Logs are under
`.nextgo-test-results/qualification/stream-budget/`, with the frozen 686-input
source manifest retained there. Prior parser/resource matrices remain separate
historical results. This verifies the admission subset, not the complete
capability inventory or a production release; all 11 release gates remain open.

Remote checkpoint `8cfb91d` passed both native jobs, its dedicated stream-parser
job and its container job; its broader browser run finished with 953 passes,
three skips and one WebKit deployment-skew failure. A 200-only prefetch wait
was invalid: new configuration can reject a cold old-slot request with 400,
while warm cached output can still return 200. Current skew cases cover both
and validate discard/recovery; see [the later qualification](PARTIAL_PRERENDERING.md#qualification-october-24-2026-utc).
This does not erase the historical failure or certify whole remote CI/release.

Initial document chunks still need Go/WASM to become visible. Metadata streaming, deferred fallbacks containing deferred work, independent route/slot ownership changes, late authorization/status interrupts, full error-boundary reset, automatic partial-shell analysis, cross-process admission/fairness, full peak-memory/soak/accessibility review and actual proxy/cloud qualification remain open. No complete Next.js streaming or production-maturity claim follows from this milestone. See the complete [parity inventory](PARITY.md).

The capability target follows the official [Next.js navigation/streaming guide](https://nextjs.org/docs/app/getting-started/linking-and-navigating). Cooperative cancellation follows [Go's context guidance](https://go.dev/blog/context).
