# OTLP/HTTP trace exporter

This package sends Next.go trace records as OTLP/HTTP JSON using standard-library
dependencies. It owns one worker, a bounded FIFO record queue, a bounded flush
command channel and at most one active export. It exports traces only; it is not
the OpenTelemetry Go SDK and does not implement OTLP/gRPC, binary Protobuf,
metrics, logs or full OpenTelemetry configuration/environment semantics.

```go
exporter, err := otlp.New(otlp.Options{
    Endpoint:    "http://127.0.0.1:4318/v1/traces",
    ServiceName: "my-app",
})
if err != nil { return err }
tracer, err := telemetry.New(telemetry.Options{
    Recorder: exporter.Record,
    Flush: exporter.Flush,
    Shutdown: exporter.Shutdown,
})
if err != nil {
    _ = exporter.Shutdown(ctx)
    return err
}
config.Tracing = tracer
```

Import `github.com/Suhaibinator/Next.go/telemetry/otlp` and `telemetry`.
`App.Close` drains completion work before invoking the tracer's shutdown hook.
Call it after `http.Server.Shutdown` with an independent bounded context. The
generated server does this. Own a tracer/exporter lifecycle once; sharing one
across independently closed applications requires application coordination.

## Queue and time budgets

| Option | Default | Accepted values |
| --- | --- | --- |
| QueueSize | 1,024 records | 1–65,536 |
| MaxQueueBytes | 4 MiB accounted queued data | 1 KiB–64 MiB |
| BatchSize | min(64, QueueSize) | 1–512, no larger than QueueSize |
| MaxAttempts | 3 per batch | 1–5; one disables retries |
| Interval | one second | 1 ms–one minute |
| Timeout | five seconds for a complete batch | 1 ms–one minute |

Zero values choose defaults. Records are validated and copied before admission,
including owned bounded strings and UTC timestamps. Queue admission never waits
for network capacity. `ErrQueueFull` rejects new records while older admitted
records remain queued. Invalid records return `ErrRecord`; sealed exporters
return `ErrClosed`. Accounted bytes include a conservative per-record allowance;
they are not a measurement of the complete Go heap. One in-flight batch,
encoding buffers, HTTP transport state and up to sixteen pending flush commands
are additional bounded resources. Maximum JSON request size is 4 MiB; responses
are read up to 64 KiB. No data is persisted to disk.

`Flush(ctx)` processes records admitted before the call. A flush-owned export
uses the earlier of its context deadline and batch timeout. An automatic batch
already in progress retains its independent timeout; the caller can stop waiting
at its own deadline. Failed processing observed by a flush returns a generic
error. Earlier losses remain visible in `Stats`, even if a later empty flush
succeeds. At most sixteen flush commands may wait; excess calls return
`ErrFlushBusy`. Cancellation does not make admitted records durable.

`Shutdown(ctx)` seals admission and drains. The first shutdown caller owns the
drain deadline. Its expiration cancels the worker's transport and discards
remaining work. Repeated calls can await the same shutdown; their own earlier
deadlines stop waiting without canceling the first drain. Custom transports
must honor cancellation; Go cannot forcibly stop an uncooperative RoundTripper.

## Protocol and failure handling

Endpoint is a full HTTP(S) trace URL. Empty/root paths become `/v1/traces`;
non-root paths are used literally. Credentials, queries and fragments are
rejected. `Headers` explicitly configures collector authentication/other headers;
names/values are validated, bounded and copied. Reserved transport/trace headers
cannot be overridden. Default transport connections do not use environment
proxies. Supply an explicit `Transport` for proxy, custom TLS or mTLS policy.
The exporter uses a background context without application trace state, avoiding
recursive export spans. Redirects are never followed with collector credentials.

JSON carries hex IDs, integer enums and decimal-string nanosecond timestamps.
Records map to one resource (`service.name`, optional `service.version`) and an
experimental Next.go instrumentation scope. It supplies method, route and HTTP
status attributes, plus bounded custom `nextgo.*` fields. Generic failure
categories set error status without sending exception messages. It does not
claim complete HTTP semantic-convention or OpenTelemetry SDK conformance.

HTTP 429, 502, 503 and 504, and network/read failures use exponential backoff with
jitter within the batch's overall timeout and maximum attempts. Retry-After is
honored when valid; a delay extending past the remaining timeout does not trigger
an early retry. Other HTTP errors, malformed/oversized replies and redirects
are not retried. Partial success counts accepted/rejected spans and is never
retried. Warning-only partial success increments the warning counter.

Errors never contain response bodies, configured headers or credential-bearing
URLs. `Stats` reports admitted/exported/lost records, failed batches, actual retry
attempts, warnings and current queue/byte/in-flight counts. `Dropped` includes
queue overflow and admitted records lost during export/shutdown. The tracer's
`Recorded` count means successful recorder admission; use exporter `Exported`
for collector acceptance. Neither count proves downstream durable storage.

## Native and serverless use

The example accepts `NEXTGO_DEMO_OTLP_ENDPOINT`. It selects OTLP instead of the
JSON stderr recorder. Generic `OTEL_*` environment-variable loading is not
implemented; production code should configure the typed options deliberately.

The generated Lambda runtime forwards `App.FlushTelemetry` through compression.
After publication handling and accepted completion callbacks, it flushes before
requesting another invocation. The budget is two seconds or the earlier
invocation deadline. Flush failures are logged by type, without changing or
duplicating a published application response. Transport failures release pending
completion reservations before the flush. A deadline can still lose telemetry;
actual AWS freeze/shutdown behavior remains unqualified. Custom outer handlers
must forward `FlushTelemetry(context.Context) error` to preserve this lifecycle.

## Verification

Run native/race tests with `go test -race ./telemetry/otlp ./serverless`.
The generated executable fixture checks both buffered and streaming Lambda reuse.
`scripts/test-collector-container.sh` also builds the real app and sends spans to
official Collector Contrib 0.163.2, pinned by manifest digest in the Dockerfile.
The fixture runs non-root/read-only with bounded writable tmpfs and no external
network, verifies collector-decoded spans/service ownership, completion drain
and omitted sensitive request values. The collector is a development test tool;
it is absent from the production runtime image.

Real TLS/mTLS/proxies/cloud collectors, exporter soak/recovery/load budgets,
durable delivery, full semantic/propagation conformance and operational alerting
remain open release work.

Protocol sources: [OTLP specification](https://opentelemetry.io/docs/specs/otlp/),
[trace schema](https://github.com/open-telemetry/opentelemetry-proto/blob/main/opentelemetry/proto/trace/v1/trace.proto),
[trace service responses](https://github.com/open-telemetry/opentelemetry-proto/blob/main/opentelemetry/proto/collector/trace/v1/trace_service.proto),
[export timeout/retry contract](https://opentelemetry.io/docs/specs/otel/protocol/exporter/).
