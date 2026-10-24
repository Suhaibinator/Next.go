# Server tracing

`Config.Tracing` enables request, loader/cache, render, API/action/handler and
completion-work spans. It is optional and uses only standard-library runtime
dependencies. Incoming trace headers are ignored by default. Tracing does not
change page cache identities, browser manifests or the navigation protocol.

This is an instrumentation foundation, not full OpenTelemetry support. The
basic recorder produces structured JSON. An optional bounded standard-library
[OTLP/HTTP JSON exporter](../telemetry/otlp/README.md) is implemented and verified
against an official collector. OpenTelemetry SDK integration, metrics,
browser/navigation and CLI/build spans, complete propagation conformance, real
cloud and production overhead qualification remain open
in [the parity inventory](PARITY.md).

## Enable it

In server-only `app/config.go`:

```go
func Configure(c *nextgo.Config) {
    tracer, err := telemetry.New(telemetry.Options{
        Recorder: telemetry.JSONRecorder(os.Stderr),
        SampleEvery: 1,
    })
    if err != nil { panic(err) }
    c.Tracing = tracer
}
```

Import `github.com/Suhaibinator/Next.go/telemetry` and `os`. The demo supports:

```sh
NEXTGO_DEMO_TRACING=true go run ./cmd/nextgo dev ./examples/demo
```

The same option works with the embedded production binary and both Lambda
transports. JSON lines go to stderr alongside ordinary server logs. A recorder
receives an owned `telemetry.Record` value; it may retain that value. The callback
must be concurrency safe, return promptly and avoid blocking network work.
`JSONRecorder` serializes writes to its caller-owned writer and never closes it.
It is synchronous: a blocked writer can delay request/completion work.

Recorder errors and panics increment `tracer.Stats().Failures` without changing
the response. Successful writes increment `Recorded`. Ending a span is
idempotent. Instrumentation does not impose a timeout or terminate a callback
that blocks or calls `runtime.Goexit`; use cooperative recorders. Sampling
limits expected volume, not a hard per-request span/byte budget. An asynchronous,
bounded exporter is available separately; load/soak evidence remains a
production requirement.

## Span and context ownership

Each application request has one `nextgo.request` server span. Nested locale
mounts share it. Configured middleware, pages, layouts, metadata/data loaders,
APIs and actions inherit its context. Framework spans include:

| Name | Covers |
| --- | --- |
| `nextgo.load` | Page/data, layout and metadata evaluation; cache state is `disabled`, `bypass`, `miss`, `fresh` or `stale` |
| `nextgo.render` | HTML or navigation-envelope rendering, including streamed response handling |
| `nextgo.api` | A file-discovered HTTP endpoint |
| `nextgo.action` | The registered action transport |
| `nextgo.handler` | A configured HTTP handler |
| `nextgo.after` | An accepted completion callback when it actually runs |
| `nextgo.cache.refresh` | An accepted background cache refresh when it actually runs |
| `nextgo.http` | An outbound request made with the optional transport below |

Route labels come from declared patterns or fixed internal endpoint labels,
never actual parameter values. Method labels use standard methods or `OTHER`.
Managed locale labels use configured language codes. Names, routes and locale
labels are capped at 128, 256 and 63 bytes, with control characters removed.
Application spans must use stable operation names/patterns too.

```go
err := telemetry.Run(c, "repository.read", func(ctx context.Context) error {
    return repository.Load(ctx)
})
```

`Run` preserves returned errors and panic/Goexit behavior while recording a
generic failure category. For finer scope control:

```go
ctx, span := telemetry.Start(c, telemetry.Info{Name: "repository.read"})
err := repository.Load(ctx)
span.End(telemetry.Result{Error: telemetry.Classify(err)})
```

`Start` is a no-op outside a traced context; methods on its nil span are safe.
`telemetry.Context(ctx)` returns trace/span IDs and the sampled bit. Use contexts
for correlation; do not write IDs into shared public page output or island props
unless you deliberately account for that variation.

Completion callbacks retain the registering trace context with independent
cancellation and fresh request Memo state. Their parent may already have ended.
Lambda completion spans begin after Runtime API response acceptance; rejected
publication drops callbacks and does not emit execution spans. The request span
measures application handler completion, not platform acceptance or delivery to
a client. Response byte counts measure accepted writer bytes, not proven wire
delivery. Hijacked connection traffic is excluded. Traced response writers retain
Flusher, Hijacker, Pusher and ReaderFrom when supported, plus ResponseController
unwrapping; legacy CloseNotifier is not exposed.

## Privacy and propagation

Framework-generated records contain no raw URL, query values, parameters,
request/response headers, cookies, credentials, bodies or exception messages.
Errors use bounded categories such as `internal`, `canceled`, `deadline`, `panic`
and `aborted`. This contract applies to these trace records; ordinary application
logs and custom operation labels remain application responsibilities.

`TrustIncoming: true` accepts exactly one valid W3C version-00 `traceparent`.
Enable it only for an ingress whose trace context you intend to trust. Zero IDs,
uppercase/invalid hex, duplicate headers and unsupported versions start a new
trace. The implementation preserves a trusted parent's sampled bit. New local
roots make a deterministic 1/N decision using their generated trace ID;
`SampleEvery: 0` defaults to one, with a maximum of 1,048,576. Child spans preserve
that decision. Tracestate and baggage are not automatically propagated, and full
W3C conformance remains unfinished.

The optional outbound transport clones requests/headers, sets the current
`traceparent` and removes stale `tracestate` when replacing an identity:

```go
client := &http.Client{
    Transport: telemetry.Transport{Base: http.DefaultTransport},
    Timeout: 5 * time.Second,
}
req, err := http.NewRequestWithContext(c, http.MethodGet, backendURL, nil)
if err != nil { return err }
response, err := client.Do(req)
if err != nil { return err }
defer response.Body.Close()
```

Its span ends on response-body EOF/Close or transport failure. Callers must close
bodies. Destination URLs, headers and payloads are omitted from records. Other
caller-supplied headers retain normal HTTP semantics; the transport does not copy
incoming baggage into outbound requests. `telemetry.Inject` is available for
other explicit carriers. It emits version-00 traceparent only.

## Verification and remaining work

Native/race fixtures cover parentage, opt-in trust, sampling, malformed carriers,
recorder errors/panics, cancellation, application panic/Goexit, HTTP interfaces,
outbound cloning/body cleanup, cache states, locale mounts and platform-gated
completion. Executable tests run traced HTTP and both mock Lambda transports
outside the source directory. Browser checks verify DOM-preserving hydration,
Go interaction/navigation and streamed-island behavior with tracing enabled.

`go test -bench BenchmarkHTTPTracing -benchmem ./telemetry` reproduces a small
local wrapper benchmark. [Preliminary raw samples](benchmarks/tracing-go127-darwin-arm64.txt)
were collected on Go 1.27.3/macOS arm64 under concurrent verification load,
including httptest machinery. They do not establish a full application, exporter
or production load budget. Complete OpenTelemetry SDK/semantic-convention integration, metrics,
browser/build instrumentation, advanced propagation, production export budget qualification,
tail sampling, operational dashboards and actual cloud qualification remain open.
The bounded OTLP/HTTP exporter covers trace delivery/flush only; see its
[protocol and lifecycle contract](../telemetry/otlp/README.md).

Capability references: [Next.js instrumentation](https://nextjs.org/docs/app/guides/open-telemetry),
[OpenTelemetry Go](https://opentelemetry.io/docs/languages/go/instrumentation/),
[W3C Trace Context](https://www.w3.org/TR/trace-context/).

## Local collector example

```sh
docker run --rm --init -p 127.0.0.1:4318:4318 \
  -v "$PWD/examples/collector/config.yaml:/etc/otelcol/config.yaml:ro" \
  otel/opentelemetry-collector-contrib:0.163.2@sha256:7f22d2a6748110ffb6a59bc0df448bccbf501cdb667d610ede4c61d3863d5849 \
  --config=file:/etc/otelcol/config.yaml
```

In another terminal:

```sh
NEXTGO_DEMO_OTLP_ENDPOINT=http://127.0.0.1:4318/v1/traces \
  go run ./cmd/nextgo dev ./examples/demo
```

The [development collector config](../examples/collector/config.yaml) prints
received spans. Configure a production collector/backend separately; this
example does not supply persistence, dashboards, production authentication or
cloud deployment. `App.Close` drains callbacks and exporter shutdown. Lambda
flushes after completion handling before invocation reuse, with a bounded budget.
