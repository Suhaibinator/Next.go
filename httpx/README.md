# HTTP compression

Wrap an application or adapter handler:

```go
server := &http.Server{
    Addr: ":3000",
    Handler: httpx.Compression(app),
}
```

`Compression` negotiates gzip using `Accept-Encoding`, including quality values,
wildcards, explicit gzip refusal, and explicit identity preferences. An absent
header serves identity. If neither gzip nor identity is acceptable, it returns
406 without calling the handler.

It compresses text, JSON, SVG, WebAssembly, and other eligible responses as they
are written. It never collects the complete response. Existing encodings,
range requests/responses, HEAD requests, upgrades, bodyless statuses, and
`Cache-Control: no-transform` bypass compression. Image, audio, video, and common
archive/PDF content types also bypass compression. Headers are held until the
first body write or flush so that an empty response stays unencoded.

Compressed responses remove `Content-Length`, byte-specific digest headers, and
`Accept-Ranges`. Strong ETags become weak because the transferred bytes changed.
The middleware merges `Vary: Accept-Encoding` without duplicating an existing
token or changing `Vary: *`. HTTP error responses can also be compressed.

Compressors reuse their substantial flate workspaces through an idle pool
limited to 16 writers. Each active response owns its compressor exclusively;
every release resets its destination to `io.Discard`, including transport
errors, handler panics, and hijacks. A burst cannot retain more than 16 idle
workspaces, and pooled writers do not retain response writers or output buffers.
The server's request limits determine the number of active streams.

`http.Flusher` and `http.Hijacker` remain available when supported by the
underlying response writer. `Unwrap` supports `http.ResponseController`.
Flushing also flushes the gzip stream, preserving incremental HTML and SSE.
Declared trailers and late `http.TrailerPrefix` values are forwarded after the
gzip footer. Panics propagate without committing an otherwise unwritten 200.

As with `http.ResponseWriter`, the writer is not safe for concurrent handler
writes. Use application request/concurrency limits for large numbers of active
streams.

Run `go test ./httpx -run '^$' -bench . -benchmem` to compare representative
navigation-envelope and parallel request costs. Fresh and pooled compressor
benchmarks retain the allocation baseline; they complement concurrency tests
for cross-request isolation, bounded retention, and failure cleanup.
