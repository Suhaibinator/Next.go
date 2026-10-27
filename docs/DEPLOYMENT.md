# Deploying Next.go

The same generated application can run as a self-hosted HTTP server or an AWS
Lambda custom runtime. Native tests exercise the Lambda protocol against a local
mock Runtime API. **Real AWS deployment, IAM, cold-start latency, and platform
limits have not been validated in a cloud account.** CI configuration is included;
its remote results must be checked before treating a revision as release-ready.

## Build toolchain policy

Use an upstream-supported Go SDK for production. The Dockerfile defaults to the
official `golang:1.27.3-bookworm` image, pinned by its multi-architecture manifest
digest. As checked on October 24, 2026, Go supports the 1.26 and 1.27 release
lines: support ends when two newer major releases exist. Go 1.27.3 includes a
`crypto/x509` security fix. See the [official Go release policy and history](https://go.dev/doc/devel/release)
and [current stable downloads](https://go.dev/dl/?mode=json).

Go 1.24 remains the framework's minimum language and compatibility-test target;
it is outside upstream support and is not the recommended production SDK.
Update the production SDK pin deliberately as supported patches arrive, then
rerun native, matching-toolchain WASM, executable and browser qualification.
The generated WASM bundle and `wasm_exec.js` must come from the same SDK; publish
them with their server as one generation.

The global `GO_IMAGE` build argument applies to all compiler stages. To reproduce
the minimum-SDK compatibility image explicitly:

```sh
docker build --target deployment-test \
  --build-arg GO_IMAGE=golang:1.24.13-bookworm@sha256:1a6d4452c65dea36aac2e2d606b01b4a029ec90cc1ae53890540ce6173ea77ac \
  -t nextgo-go124-compatibility .
```

## Self-hosted executable

Build on the target OS and CPU architecture with a supported Go SDK:

```sh
go run ./cmd/nextgo build -out ./dist/demo ./examples/demo
./dist/demo/server -addr :3000
```

Opted-in [prerendered pages](PRERENDER.md) are captured during the build using a
temporary host executable, then embedded privately in the final target binary.
Build loaders need their public data sources available. Both HTTP servers and
Lambda processes import the same snapshot bytes with their original ages.
Regeneration and invalidation are process-local by default: cold starts/restarts
can serve an old embedded generation stale while it refreshes. Opting into
`PrerenderPolicy.Shared` with `Config.SharedCache` uses an online authority for
bounded build-seed admission, runtime regeneration and acknowledged cross-instance
invalidation. The authority must be reachable during the build and startup.
Its optional file backend retains acknowledged shared state across local
authority recovery. See [shared build seeds and regeneration](PRERENDER.md#shared-build-seeds-and-regeneration)
and [shared-cache limits](SHARED_CACHE.md). Replication/HA, machine-power-loss,
actual-cloud and operational recovery qualification remain open.

The executable embeds the application assets and matching WASM runtime. It can
run outside the source checkout with no Go, Node, or source files installed. Send
SIGTERM for graceful HTTP shutdown. Generated ordinary, aggregate-zone, release,
and ingress servers allow five seconds for active requests, close remaining
connections if draining fails, then give application cleanup a separate
five-second context. Allow more than ten seconds in the external supervisor's
termination grace period for both budgets. Connection closure cancels requests
but does not join arbitrary handlers; cleanup hooks must observe cancellation. Place a TLS reverse
proxy in front of it. Configure that proxy
to preserve the Host header, provide appropriate request/body/time limits, and
restrict any forwarded headers to trusted proxies. Keep application secrets in
your deployment's secret manager, never in public assets or island props.

Run a process supervisor or container orchestrator with automatic restart and a
readiness check appropriate to your app (`/api/health` in the example). The health
endpoint must check any dependencies required for your own readiness policy.
Keep at least the previous executable for rollback. Restart instances gradually;
already-open documents can refer to old runtime assets, so retain those assets
at your reverse proxy/CDN or use deployment affinity during rollout.

## Container

The multistage Dockerfile uses the pinned production SDK above to build a static
Linux executable and copies it and CA certificates into a scratch image. It runs
as UID/GID 65532 and needs no writable filesystem for the demo.

```sh
docker build -t nextgo-demo .
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges \
  -p 127.0.0.1:3000:3000 nextgo-demo
```

Use `--build-arg APP=./path/to/app` for another app in the copied module. Native
code using CGo or runtime OS commands needs an appropriate runtime image instead
of scratch. No database or persistent application state is bundled.

## AWS Lambda: API Gateway HTTP API v2 or Function URL

The generated executable detects `AWS_LAMBDA_RUNTIME_API` and runs the
`serverless` adapter instead of listening on TCP. The adapter accepts the payload
**2.0** format used by HTTP API integrations and Function URLs. REST API payload
1.0, WebSockets, and ALB events are not implemented. Responses default to
buffering; the runtime can opt into native Lambda response streaming below.
Use a root-mounted URL: API Gateway custom-domain path mappings are omitted from
`rawPath` in payload v2, so configure an explicit `BasePath` matching the path actually delivered to the handler; it is not inferred from forwarded headers. See [deployment URL conventions](URLS.md).

Use a Linux executable named `bootstrap` in the root of your deployment ZIP with
the `provided.al2023` runtime. Build with Docker to avoid requiring a Linux host:

```sh
docker build --platform linux/amd64 --target artifact \
  --output type=local,dest=./dist/linux .
mkdir -p ./dist/lambda
cp ./dist/linux/server ./dist/lambda/bootstrap
chmod +x ./dist/lambda/bootstrap
(cd ./dist/lambda && zip ../lambda.zip bootstrap)
```

For ARM64, build `linux/arm64` and select `--architectures arm64` below. Provide
an execution role with a Lambda trust policy and the application's minimum AWS
permissions (including logging if required), then use your selected AWS Region:

```sh
export AWS_REGION=us-west-2
export NEXTGO_ROLE_ARN=arn:aws:iam::YOUR_ACCOUNT:role/YOUR_LAMBDA_EXECUTION_ROLE
aws lambda create-function --function-name nextgo-demo \
  --runtime provided.al2023 --handler bootstrap --architectures x86_64 \
  --role "$NEXTGO_ROLE_ARN" --zip-file fileb://dist/lambda.zip \
  --memory-size 512 --timeout 30
aws lambda wait function-active --function-name nextgo-demo
aws lambda create-function-url-config --function-name nextgo-demo --auth-type AWS_IAM
```

`AWS_IAM` requires signed requests and an authorized caller. Use your approved
authentication/proxy setup for a browser-facing deployment. Changing the URL to
public access requires an intentional resource-policy decision; these commands
do not make an unauthenticated public endpoint. For API Gateway, configure a
Lambda proxy integration with `payloadFormatVersion: "2.0"`, a `$default` route,
and permission for your API's execution ARN to invoke this function.

Smoke-test the handler before exposing traffic:

```sh
cat > ./dist/event.json <<'JSON'
{"version":"2.0","rawPath":"/","rawQueryString":"","headers":{"host":"example.test"},"requestContext":{"domainName":"example.test","http":{"method":"GET","path":"/","protocol":"HTTP/1.1","sourceIp":"127.0.0.1"}},"isBase64Encoded":false}
JSON
aws lambda invoke --function-name nextgo-demo --cli-binary-format raw-in-base64-out \
  --payload file://dist/event.json ./dist/response.json
cat ./dist/response.json
```

Then verify the actual HTTPS integration: initial HTML with JavaScript disabled,
WASM content type and successful hydration, dynamic direct access, navigation,
API headers, repeated query parameters, cookies, errors, and request deadlines.
Use versions and an alias for gradual rollout and immediate rollback rather
than sending production traffic directly to `$LATEST`.

### Streaming Function URLs

Use `NEXTGO_LAMBDA_RESPONSE_MODE=streaming` together with a streaming-capable
Lambda integration. For a Function URL, configure **both** the function
environment and the URL invoke mode:

```sh
aws lambda update-function-configuration --function-name nextgo-demo \
  --environment 'Variables={NEXTGO_LAMBDA_RESPONSE_MODE=streaming}'
aws lambda wait function-updated --function-name nextgo-demo
aws lambda update-function-url-config --function-name nextgo-demo \
  --invoke-mode RESPONSE_STREAM
```

The environment command replaces the complete environment-variable map; include
your existing variables when updating a real application. URL authentication
remains `AWS_IAM` as configured above. Ordinary `aws lambda invoke` is a buffered
invocation; validate streaming through the configured Function URL or a client
using `InvokeWithResponseStream`. Do not assume a buffered API Gateway HTTP API
integration becomes streaming by setting this variable.

In streaming mode the runtime sends HTTP integration metadata followed by eight
NUL bytes, then raw response bytes over a chunked Runtime API POST. `http.Flusher`
is available to the application. Deferred HTML and gzip are flushed while the
handler is still running; the response is never collected into a complete body
or base64 envelope. Binary response bytes remain binary.

The integration metadata, including cookies, must fit within 16 KiB. The default
body cap is 200,000,000 transmitted bytes; lower it with
`NEXTGO_LAMBDA_STREAM_MAX_BYTES` (1 through 200,000,000). This local cap is not a
promise about the limits, billing, throughput, or timeouts of your selected AWS
integration. Verify those separately against current service documentation.
An error before response commitment can use the invocation-error endpoint; an
application panic before commitment instead becomes a generic HTTP 500. After
commitment, status and headers cannot change. A panic or body-cap failure is
reported using the declared Lambda error trailers with generic base64 error
metadata. Deadline or shutdown cancellation aborts the pipe and upload. An
upstream consumer failure cancels the handler context and terminates the runtime.

Mock Runtime API tests prove early delivery before handler completion, including
deferred HTML through gzip, metadata/cookies, binary bytes, HEAD/204 behavior,
panic trailers, payload limits, deadline/shutdown cancellation, and rejected
uploads. Real AWS streaming validation remains a release gate: check early
HTML/chunks with an authorized streaming client, reverse-proxy/CDN buffering,
WASM hydration, deadline failures, and observable client behavior after errors.

### Invocation and gateway correlation context (qualification pending)

`serverless.InvocationID(ctx)` / `serverless.GatewayRequestID(ctx)` expose
distinct bounded context metadata from Run and HTTP API v2
event conversion. It reads no caller-supplied HTTP correlation headers, adds no
automatic headers/logging/tracing and grants no authorization. Values outside
1–256 visible-ASCII bytes are omitted; existing Runtime API validation and
response/error routing are unchanged. A missing gateway ID shadows an older
inherited value. Existing bounded `nextgo.After` contexts retain request values.
Fresh unit and native/local buffered/local streaming executable qualification
remain pending; the [dedicated workflow](../.github/workflows/serverless-context.yml)
is unexecuted. Its generated fixture requires both `deployment` and
`invocation_context` tags; plain deployment-tag tests retain their existing
interface. No actual AWS proof is claimed. See [context semantics, application-
owned examples and fixture instructions](SERVERLESS_CONTEXT.md).

### Runtime behavior and limits

- Each invocation gets a request context with the Lambda deadline. Application
  code must honor cancellation; a handler ignoring its context cannot be
  forcibly stopped by a Go adapter. HTTP client disconnects are not forwarded by
  Lambda and cannot cancel the request before its invocation deadline.
- In default buffered mode responses have a 4 MiB body cap to leave room for base64 and the
  JSON envelope within the synchronous Lambda payload limit. Large WASM or
  other static assets should be hosted separately behind your deployment's
  routing layer; the adapter does not silently truncate oversized responses.
  The runtime negotiates gzip before buffering, so browser requests accepting
  gzip can serve compressed WASM within that cap. Clients refusing compression
  may still exceed the limit; validate the actual generated asset size.
- In buffered mode binary bodies and content-encoded bodies use base64; response cookies remain
  individual `Set-Cookie` values. Other repeated headers are comma joined, as
  required by payload v2. HTTP informational responses and trailers cannot be
  preserved by this buffered integration.
- In buffered mode panics become a generic HTTP 500. Malformed invocation envelopes, exceeded
  deadlines, and oversized responses use the Runtime API invocation-error path.
  Runtime API failures terminate the process for Lambda to recycle it.
- Configure authentication, CORS, rate limits, logging, alarms, and cache policy
  for your actual application and deployment. Authentication state and caches
  held in process memory are per execution environment, not a shared database.

Primary protocol references: [Lambda Runtime API](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-api.html),
[custom runtime packaging](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-custom.html),
and [Function URL request/response format](https://docs.aws.amazon.com/lambda/latest/dg/urls-invocation.html).
Streaming transport follows the [custom runtime streaming protocol](https://docs.aws.amazon.com/lambda/latest/dg/runtimes-custom.html#runtimes-custom-response-streaming)
and [HTTP integration framing](https://aws.amazon.com/blogs/compute/using-response-streaming-with-aws-lambda-web-adapter-to-optimize-performance/).

## Reproduce executable deployment checks

```sh
./scripts/test-deployment-container.sh
```

This builds the demo and a black-box Go test runner with the Dockerfile's pinned
production SDK (or an explicitly supplied `GO_IMAGE` override). They run from
a scratch image as UID/GID 65532 with a read-only filesystem, no external
network, dropped capabilities, and bounded container memory/processes. The
fixture launches the actual generated executable from `/`, first as a native
HTTP server and then with local buffered/streaming Lambda Runtime API fixtures.
It verifies useful SSR, direct dynamic routes, APIs, custom 404, navigation
protocol, gzip/versioned WASM assets, early streamed content, bounded completion
work and SIGTERM exit. Locale fixtures cover prefix and configured-domain routing, translated SSR, request APIs and shared assets through native HTTP and both Lambda transports. No source assets or compiler exist in the test image.
The normal production `runtime` image remains the default Docker target.

For a host-compatible built demo executable, run the same tests without Docker:

```sh
NEXTGO_TEST_BINARY="$PWD/dist/demo/server" go test -tags=deployment ./tests/deployment -v
```

The mock Runtime API proves the generated adapter contract locally. It does not
verify AWS IAM, real API Gateway/Function URL behavior, cold starts, proxy/CDN
buffering or multi-instance rollout. The separate native tests exercise runtime
transport rejection, invocation deadlines, size caps and error trailers.
Actual cloud qualification remains required for a production release.

### Request completion work

`nextgo.After` uses bounded process-local callbacks. Native servers start them
when request handling finishes. Generated HTTP servers give application cleanup
its own five-second context after HTTP draining, so an exhausted request-drain
deadline does not immediately cancel completion or tracing cleanup. Custom
servers must call `app.Close` after `http.Server.Shutdown` with an independent
bounded context. Lambda sends its response before starting callbacks and
awaits them before polling `/next`; callback budgets also obey the invocation
deadline. Failed response publication discards callbacks. Cancellation remains
cooperative, and crashes/deploys can lose work. Required jobs belong in durable
infrastructure. See the [public API and limits](../README.md#work-after-request-handling).


The executable conformance fixture also runs both root and `/demo` mounts with a
separate asset path through HTTP and buffered/streaming Lambda. Browser fixtures
cover a real separate asset origin and mounted static hosting. Configure path
prefixes explicitly and coordinate them with ingress/CDN rules; real cloud and
CDN rollout remain unverified.


### Runtime URL policy and version skew

One built binary can start with different URL, endpoint-routing or layout
preservation settings. Build IDs alone do not identify these public ownership
rules. Next.go protocol 3 supplies a configuration identity in documents,
navigation JSON and `X-Nextgo-Configuration`. It includes the build and protocol;
keep both protocol and configuration response headers through your proxy.
A browser with a different configuration loads a full document before adopting
new navigation content. Speculative prefetch never initiates that reload, and
incompatible loading content is ignored. Public page-cache snapshots are
namespaced by the identity as well as Host and URL.

Preserve redirects from a preceding mount to its new location and publish all
referenced assets before changing traffic. A document fallback cannot repair a
removed URL or missing WASM asset. Previously cached compatible pages can remain
available until expiration or Refresh; there is no deployment notification,
server affinity or production asset-retention service yet. Export rejects mixed
HTML/data identities or changes between pages without replacing published files.
Protocol 3 also requires resolved document language, direction and viewport in
navigation data. Export verifies that these attributes match the HTML response
before publishing; avoid nondeterministic or request-mode-dependent metadata.
Older protocol clients fall back to full-document navigation.

`tests/skew.spec.mjs` switches and rolls back actual processes of one binary
behind a local proxy, including a mount move and changed slash/asset policies.
Executable conformance also checks the identity through HTTP and both Lambda
transports. These are local contracts; qualify actual proxy caching, multi-instance
traffic, cloud transport, old assets and in-flight mutations before rollout.
See [URL conventions](URLS.md) for the complete path and configuration contract.


### Protected sample checks

The executable fixture exercises anonymous, reader and administrator requests
against `/restricted`, `/api/report` and `review-report` through HTTP and both
Lambda transports. It checks status, private/no-store handling, challenges and
absence of credential reflection. It injects synthetic server-only credentials;
default production/demo accounts remain disabled. Browser tests also verify
denied actions do not mutate state and role-change denials dispose preceding
protected UI. These checks qualify the sample contract, not a production identity
provider, session store, tenant model or durable mutation implementation.


### Inherited metadata

Generated metadata providers run before any stream shell or ordinary document
is committed. Executable HTTP/Lambda checks verify the dynamic field-note title,
ancestor site metadata and canonical URL, including base paths and slash policy.
Use a trusted public `seo.Metadata.BaseURL` and the deployment URL helpers;
metadata does not derive a trustworthy origin from arbitrary request headers.
Public route snapshots include the resolved head. See [metadata contracts](METADATA.md)
for cache, cancellation, precedence and remaining title/streaming limitations.

### Explicit shared data

The experimental [`cache/shared` API](SHARED_CACHE.md) lets Linux and serverless
workers contact one authenticated backend for serialized data. Its reference
`nextgo-cache` command holds bounded memory and supplies atomic leases and
acknowledged namespace-wide invalidation. Non-loopback service binds require TLS.
The example at `/shared-cache` uses an explicit native data loader; it does not
share `Config.Cache`, rendered route snapshots, sessions or writes. Application
and mock Lambda process tests cover the HTTP protocol, outage/recovery,
invalidation and application restart. Real cloud networks, durable/replicated
storage, service restart recovery and production load/operations remain open.

### Server snapshot lifetimes

`CachePolicy.Lifetime` configures server fresh/revalidation/hard-expiry
ages, with optional `cache.Never` for no hard expiry. Native HTTP starts its coalesced refresh on bounded completion workers
after the handler; Lambda starts only after accepted response publication and
drains before `/next`. Queue, deadline and shutdown behavior shares `After`
configuration. Rejected or expired work releases reservations; unavailable
scheduling falls back to a blocking read. Uncooperative Go loaders cannot be
forcibly terminated.

Snapshot inputs are frozen before middleware returns, and each retry receives
owned parameters/query maps and a fresh Memo scope. Cookie/auth/body requests
bypass public snapshots. Standalone Lifetime responses stay no-store in HTTP and
Go navigation caches. Named profiles explicitly add generation-bounded Go reuse;
HTTP/CDN responses must revalidate with public/max-age=0/must-revalidate. A transient refresh failure keeps the last good snapshot
only until its original hard expiry; permanent/auth/response-control failures
discard it. See [cache contracts](../cache/README.md) for strict expiration and
application data-loader scheduling.

The generated executable harness now changes an illustrative source, observes
the stale response, and then verifies the refreshed snapshot through native
HTTP and buffered/streaming Lambda, at root, mounted and slash-policy URLs.
It also commits a separate update through an action, marks a still-valid profile
snapshot stale, and checks last-good publication followed by refresh on both
Lambda transports and native HTTP. Marking clips the stale deadline without
extending the generation; strict expiration remains available for read-after-write.
The `/evergreen` fixture also checks the non-expiring default profile and its
on-demand/strict invalidation through the same generated executables.
This does not prove actual AWS freeze behavior, distributed consistency or
production ISR. These page-snapshot caches remain process-local and are lost on replacement/cold start.

Verification note (October 23, 2026): the current generated executable passes
the complete HTTP/buffered-Lambda/streaming-Lambda matrix on macOS and in a
non-root/read-only Linux container. This covers current cache/profile and
background cancellation behavior plus locale prefix/domain routing. Race tests
and vet pass on Go 1.24.13/Linux and 1.27.3/macOS; matching-toolchain WASM tests
pass on both. Docker's read-only storage issue was recovered and the Linux
matrix repeated successfully. Actual AWS qualification remains open.


Fully resolved named/partial/inline profile values and labels contribute an opaque digest to the runtime
configuration identity and page-cache namespace. Changing effective policies
therefore separates generations even when instances share a Store and build ID.
Already cached browser data can survive until its configured window or Refresh
without contacting a new instance; qualify old assets and rollout rules against
that window. Go action POSTs clear browser reuse on dispatch/settlement in the
current runtime, not other tabs or replicas. Nested lifetime propagation and implicit cached-function scopes,
distributed invalidation and actual cloud/CDN behavior remain unqualified.

### Locale deployments

[Managed locale routing](LOCALES.md) uses the same embedded executable for
prefix and configured-domain modes. Configure public HTTPS domains explicitly,
preserve Host through the proxy/integration, and send readiness probes with a
configured Host. Unknown domains return 421; assets remain shared/public.
Negotiation redirects are private/no-store. Do not cache negotiated home
redirects or cookie-bearing responses as public content. Translation dictionaries
and canonical/alternate metadata remain application responsibilities.

The generated executable fixtures exercise both modes through native HTTP,
buffered Lambda and streaming Lambda. These are isolated local protocol tests;
actual TLS/CDN behavior and cloud custom-domain routing remain unqualified.
The static exporter supports deterministic locale subpaths with `Detect: false`
and either no domains or an explicit `-domain` selecting one configured host.
Build each domain into a separate directory and bind that artifact to its public
host at the static server/CDN; its manifest records ownership but cannot enforce
Host on its own. Request-time negotiation is rejected before publication. See [locale export/hosting limits](LOCALES.md), including canonical
paths, per-language data directories and host redirect responsibility.

### Static publication versions and explicit client shells

Ordinary exports continue to publish concrete HTML plus navigation JSON. Native
`ExportConfig` versions 1/2 keep their existing contracts (including their
existing parser extension behavior); published `nextgo-static-v1` ordinary and
`nextgo-static-v2` locale-scope artifacts retain their existing meanings.
These native DTO versions and published format versions are separate axes.

Experimental `Config.StaticShell` opts one exclusive
literal-prefix optional catchall into native export configuration 3 and
`nextgo-static-v3` publication with descriptor 1. No declaration means no version
3 transition. Older CLIs must reject unsupported version 3; use one fresh paired
CLI/framework/core/lazy artifact. Descriptor 1 requires the actual 20-lowercase-
hex CLI build ID; navigation protocol 3 and module ABI 5 remain unchanged.

Only a generic public ClientIsland seed is reusable. The host must reject all
methods except GET/HEAD before any file/document lookup, serve exact retained
files/documents first, then apply a validated HTML-Accept fallback
inside the declared literal subtree, with reserved namespaces and the seven
virtual-resource suffixes excluded. Unknown outside paths remain errors. A
client missing-screen still has the generic document's HTTP 200 status. Export
does not install a provider rule, authenticate users or analyze trusted Go
loaders for captured secrets. Read the [declaration, exclusions, immutable-
envelope validation and complete host contract](STATIC_SHELL.md) before attempting
publication. Earlier candidate proof includes both-SDK focused unit checks and
four actual Go 1.27.3 eager/lazy root/mounted exports. Go 1.24 sample exports
never launched; no local static-shell browser cases have run. The new
[public CI workflow](../.github/workflows/static-shell.yml) is pending, as is
static-host browser acceptance; earlier candidate evidence does not qualify its
exact checkout or new packaging. [CI proof scope](../tests/STATIC_SHELL_CI.md)
is Linux AMD64 functional validation, not the separately held ARM64 resource
profile or provider/cloud qualification.

### Static host browser matrix

```sh
./scripts/test-static-browser-container.sh
```

This development-only image uses the same pinned Go SDK and its matching WASM
runtime inside the Playwright browser image, builds/exports the sample, stops the
application servers, and serves only files to Firefox, WebKit and a touch
viewport. Nine scenarios per browser cover
SSR, hydration identity, navigation/history, mounted assets, mixed-generation
recovery, fail-closed unsupported publication, deterministic language scopes and
separate domain artifacts with cross-origin language switching and explicit host
enforcement.
The test container has no external network access. Its compiler and test tools
are absent from the production runtime image. CI also runs this suite directly
with installed browsers through `npm run test:static:crossbrowser`.

### Tracing in native and Lambda runtimes

`Config.Tracing` or `NEXTGO_DEMO_TRACING=true` in the example enables structured
server trace records. The generated executable fixtures verify traced HTTP,
buffered Lambda and streaming Lambda. Completion spans run only after accepted
publication; request spans end at application handler completion and do not
certify client delivery or Runtime API acceptance.

[Tracing configuration](TRACING.md) documents the opt-in trust boundary for
incoming IDs, omitted sensitive fields and synchronous recorder contract.
Use a prompt, concurrency-safe recorder. The JSON recorder writes stderr;
blocked logging can delay work. The optional [bounded OTLP/HTTP JSON exporter](../telemetry/otlp/README.md)
uses a separate worker with explicit queue, byte, attempt and timeout budgets.
`NEXTGO_DEMO_OTLP_ENDPOINT` selects it in the demo. Native shutdown drains
completion callbacks before exporter shutdown; Lambda flushes after publication
handling and completion callbacks, before requesting another invocation, within
two seconds or the earlier invocation deadline. Failures do not republish a
response. Forward `FlushTelemetry(context.Context) error` from custom outer
handlers to retain this behavior.

`scripts/test-collector-container.sh` verifies the generated binary against
Collector Contrib 0.163.2 pinned by digest, with no external network, non-root
execution and a read-only filesystem plus bounded temporary storage. Both mock
Lambda transports verify export before reuse. See the [local collector example](TRACING.md#local-collector-example).
The collector is a test/development dependency and is absent from the production
image. An in-memory queue cannot promise durable delivery. Full SDK/semantic
conformance, real TLS/mTLS/proxy/cloud behavior, metrics and production overload,
loss-alerting and recovery qualification remain open.


### Optional browser view transitions

`Config.ViewTransitions` is a public navigation policy. The generated standalone
HTTP and both mock Lambda transports verify that ordinary and streamed HTML
carry it; it contributes to the runtime configuration identity. The browser
runtime performs transitions in Go/WASM, so the serverless adapter adds no
platform-specific animation code. Static exports also retain the flag and pass
JSON navigation checks on a plain static host. Cross-origin or cross-locale full
document navigations retain their existing behavior. [Transition conventions](TRANSITIONS.md)
describe reduced motion, link/component controls, snapshot fallback, cleanup and
remaining cross-document/performance/accessibility qualification.


### Conditional rules and external services

[Request-rule conventions](REQUEST_RULES.md) cover Has/Missing conditions,
redirects and bounded HTTP(S) proxy rewrites. Generated HTTP and buffered/streaming
Lambda fixtures verify mounted local rewrites/redirects and proxy method, query,
body, credential/control-header policy and timeout behavior. The same fixtures
pass in the non-root/read-only Linux container. Browser fixtures use an actual
second upstream and ensure external sources cross a full document boundary.

Configure backend origins as trusted server-side application configuration.
Private service addresses are allowed deliberately; apply infrastructure egress
policy where needed. The default transport does not inherit environment proxies.
Use `Config.Proxy.Transport` for explicit TLS/mTLS/proxy/dial policy. The native
TLS fixture uses an explicit trusted test CA, not an insecure verification bypass.
Real proxy/CDN/TLS/cloud rollout, multi-zone asset layout and production
load/recovery remain unqualified. Tune proxy budgets within the platform's
response/invocation limits; after a stream commits, failures cannot change its
status and must terminate the stream. Static export rejects conditional/external
request rules and preserves prior output on rejection.


### Named parallel-route ownership

[Parallel routes](PARALLEL_ROUTES.md) use a bounded public selection carrier.
Generated HTTP and buffered/streaming Lambda fixtures verify matching/default
SSR, current-header reauthorization and denial before retained content is
returned. The same binary runs without source in non-root/read-only Linux.
Each invocation reselects slot routes; there is no process-global user route
state. Selected slot pages require a server and fail static publication, while
unrelated concrete pages remain exportable. Slot-only URL fixtures select and
revalidate both primary and named branches. Direct loads use primary defaults
(or 404 when absent); soft navigation retains a prior primary page only after
server rendering with current request headers. Nested slot-only direct loads and
nested metadata denials are exercised in HTTP and both Lambda fixtures. Scopes
are tied to their enclosing branch and concrete owner path, and every invocation
revalidates selected data. Ordered primary-chain fixtures exercise Reports →
Summary → Drill retention and denial with the same HTTP and Lambda executable.
Independent streamed loading and actual cloud/resource/recovery qualification
remain open.
