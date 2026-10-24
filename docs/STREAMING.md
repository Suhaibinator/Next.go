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

## General browser streams

`client.FetchStream` shares `Fetch` options, same-origin credentials, redirect policy, byte admission and abort semantics. It calls a header callback before delivering owned byte slices, applies callback backpressure and returns an empty `Response.Body`. A callback error stops the reader. Call it from a Go goroutine, rather than blocking a JavaScript event callback. The default decoded response limit remains 8 MiB. Applications remain responsible for their own data schema, authorization and consistency.

## Evidence and remaining qualification

[UI tests](../ui/stream_test.go) cover parent-before-child ordering, independent siblings, context propagation, generic errors, concurrency/size/count limits and failed-writer cancellation. [Codec tests](../internal/navigation/stream_test.go) feed individual bytes and reject malformed/truncated records. [HTTP tests](../navigation_stream_test.go) prove shell-before-loader completion, disconnect cancellation, prefetch suppression and legacy JSON behavior. [WASM tests](../client/fetch_js_test.go) verify header/body order, callback failure and byte admission. [Browser scenarios](../tests/navigation-stream.spec.mjs) exercise real progressive DOM/hydration, history, detached listener cleanup, corrupted transports, retry, deployment mismatch and mounted/slash/locale ownership. [Lambda executable checks](../tests/deployment/navigation_stream_test.go) verify frame ordering in both transports and early shell delivery in streaming mode.

Initial document chunks still need Go/WASM to become visible. Metadata streaming, deferred fallbacks containing deferred work, independent route/slot ownership changes, late authorization/status interrupts, full error-boundary reset, partial prerendering, global loader admission, full peak-memory/soak/accessibility review and actual proxy/cloud qualification remain open. No complete Next.js streaming or production-maturity claim follows from this milestone. See the complete [parity inventory](PARITY.md).

The capability target follows the official [Next.js navigation/streaming guide](https://nextjs.org/docs/app/getting-started/linking-and-navigating). Cooperative cancellation follows [Go's context guidance](https://go.dev/blog/context).
