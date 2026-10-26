# Deferred public metadata sample

`/metadata-stream` sets `Page.Stream=true` and puts its complete fallback in
`Page.Metadata`. Legacy `Page.Title` and `Page.Description` stay empty. The
deferred callback returns only a typed SEO contribution: title, description,
canonical, Open Graph and Twitter values. It does not replace body markup,
hydrate islands again, move focus, or mutate response controls.

This bounded sample has scoped qualification on Go 1.24.13 and 1.27.3; see the
[recorded scope and remaining checks](../../../../docs/METADATA.md#scoped-qualification).
It does not establish full framework, export or production release qualification.

Initial HTML metadata streaming is an explicit production native operator setting.
Build the normal production binary, then configure it when starting the server:

```sh
go run ./cmd/nextgo build -out /tmp/nextgo-metadata-stream ./examples/demo
NEXTGO_DEMO_METADATA_HTML=true /tmp/nextgo-metadata-stream/server -addr 127.0.0.1:4161
```

The flag accepts exactly `true`, `false`, or an empty value. The default remains
blocking for initial HTML. Development buffers deferred metadata even with this
flag enabled. Recognized browsers in production can receive the fallback followed
by completion; crawlers, unknown agents and other blocking representations resolve
the final metadata first. Negotiated production Go navigation can stream the
metadata terminal independently of this initial-HTML flag. This is an explicit
compatibility policy, not automatic full Next.js metadata behavior or a crawler/
authentication guarantee. See the [API and limitations](../../../../docs/METADATA.md).

The ordinary sample waits 250 milliseconds with cancellation. Its existing Go
counter and native input demonstrate body identity and local state across a late
head update. The repeat link makes a real same-origin client navigation to the
same route with a harmless query; the exit link leaves for Architecture. Neither
query strings nor request headers can select a metadata backend or fixture mode.

One custom stylesheet resource is evaluated once and has the public URL
`/assets/metadata-stream.css?resource-call=1`. A second callback invocation is an
explicit sample error. One JSON-LD companion has identifier
`metadata-stream-jsonld-v1`; both head nodes remain frozen during metadata-only
completion. Resource validators permit local stylesheets and font preloads,
not arbitrary meta/canonical elements. This sample keeps that boundary intact.

For deterministic qualification only, a trusted native operator can set
`NEXTGO_DEMO_METADATA_SOURCE=http://127.0.0.1:4162/value`. It accepts a fixed,
literal loopback HTTP URL without credentials, queries or fragments. The
loader forwards no incoming cookies/authorization, follows no redirects, uses
request-scoped `Memo`, and reads at most 8 KiB of strict, unique-key UTF-8 JSON:

```json
{"version":1,"title":"Public metadata resolved — Next.go","description":"Final metadata arrived without replacing this page's interactive body.","canonical":"https://example.com/metadata-stream?phase=final","socialTitle":"Resolved public metadata","socialDescription":"One completed Go metadata contribution."}
```

`NEXTGO_DEMO_METADATA_SOURCE_TIMEOUT` defaults to `5s` and accepts `1ms` through
`30s`. The existing `NEXTGO_DEMO_STREAM_TIMEOUT` independently bounds the render.
Qualification can hold a real source response until browser assertions complete;
no application JavaScript or fixture response rewriting implements metadata.

`NEXTGO_DEMO_METADATA_TEST_MODE=fail|invalid|readonly` additionally requires both
`NEXTGO_DEMO_METADATA_FIXTURE=true` and that explicit loopback source. These
native-only modes run after the genuine source completes: return an error,
return an unsafe canonical contribution, or ignore a refused late-header write.
No mode or endpoint is serialized into props, browser configuration or WASM.
Before-commit failures produce a generic error response; after-commit failures
keep the complete fallback and report failure without replaying the loader.

Source tests cover typed fallback ownership, the single resource callback,
memoized anonymous HTTP loading, strict input bounds and actual cancellation.
The two SDKs pass focused native/WASM and production checks, 44 application
browser cases each across four profiles, and 15 LOCAL transport cases each. The
Go 1.27 browser controller has a preserved post-removal observation failure,
separate from its 44 application passes and independently verified cleanup.
The unchanged affected suites also pass 128 cases per SDK; together the two
SDKs record 344 browser application cases and 30 separate LOCAL transport cases.
The legacy proxy identity was observed then absent; its exit code and socket
joins remain unobserved. LOCAL mock Lambda checks do not contact AWS; these
selected suites are not complete release qualification.

### Run the production browser fixture

After building the production demo above, run from the repository root with the
Node development-test dependencies and Playwright browsers installed:

```sh
export NEXTGO_E2E_SERVER_BINARY=/tmp/nextgo-metadata-stream/server
export NEXTGO_METADATA_STREAM_SERVER_SHA256="$(node -p 'require("node:crypto").createHash("sha256").update(require("node:fs").readFileSync(process.env.NEXTGO_E2E_SERVER_BINARY)).digest("hex")')"
npm run test:metadata-stream
```

`NEXTGO_E2E_SERVER_BINARY` must be an absolute path to an executable production
binary; `NEXTGO_METADATA_STREAM_SERVER_SHA256` must be its actual lowercase
64-hex SHA-256. The fixture verifies the bytes and has no compiler fallback.
It starts its own native process and held loopback data source; do not start
another copy on its fixture ports. Node is development test tooling, not an
application or production runtime dependency.
