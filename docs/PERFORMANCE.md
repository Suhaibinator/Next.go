# Performance measurements and budgets

The default Go browser runtime uses `client.Fetch`, a small `syscall/js` bridge
to the browser Fetch API. Navigation, prefetch and the example's server-action
component avoid importing `net/http`. Applications may still use Go's
`net/http`, but doing so links its dependencies into their WASM bundle.

These are measurements of this framework before and after one change. They are
not comparisons against Next.js, production field results, or evidence of full
framework performance parity.

The latest recorded browser sample is the
[viewport-link production observation](#viewport-link-production-observation-october-25-2026-utc).
The separate [seed-ledger observation](#seed-ledger-lease-bookkeeping-observation-october-24-2026-utc)
measures native allocation/bookkeeping work.
Earlier reports remain historical measurements of their specific artifacts.

## Viewport-link production observation (October 25, 2026 UTC)

The current [production-loopback report](benchmarks/viewport-go1.27.3-macos-arm64.json)
records the `/prefetch` example with Go 1.27.3/macOS arm64, Chromium
156.0.8078.4 and ten fresh contexts in one browser process. Eager WASM measures
8,260,663 raw bytes and 2,143,540 gzip-9 bytes. Its SHA-256 is
`31c2915750d9a69180d0ff194d27ea7120a5aa65de3503ad3b9171ba2783ff54`. Both the 6-MiB raw and
1.6-MiB gzip-9 reference budgets **fail**.

Readiness ranged from 99 to 111 ms (median 107 ms) on warm loopback without
CPU/network throttling. This includes parser-ordered runtime startup, automatic
visible-link work and compiled
code reuse; it is not a production or competing-framework timing comparison.
The observed 6,815,744-byte WASM linear-memory capacity excludes JavaScript
buffers and does not measure live heap, peak memory or RSS. See the
[feature qualification and limits](VIEWPORT_PREFETCH.md#bundle-and-local-startup-observation).

## Measured WASM and startup improvement

Measured on 2026-10-23 UTC with Go 1.27.3 (`darwin/arm64`), Chrome
156.0.8078.48, Node 25.7.0, macOS Darwin 27.0.0 and Apple M4 Max. Both builds
used `GOOS=js GOARCH=wasm go build -trimpath -ldflags='-s -w'`, registered the
same three example components (counter, layoutstate and toolbox), and ran the
same one-counter SSR fixture. The baseline used `net/http` for browser
navigation and actions; the candidate used the bounded Fetch bridge.

| Metric | Earlier `net/http` build | Fetch build |
| --- | ---: | ---: |
| Raw WASM bytes | 11,949,267 | 5,041,174 |
| Gzip bytes, Node zlib level 9 | 3,164,850 | 1,371,244 |
| Gzip bytes, Go default level used by HTTP middleware | 3,298,495 | 1,426,364 |
| Median navigation start to hydrated counter | 2,833.1 ms | 1,352.4 ms |
| Median WASM download and decompression | 2,631.4 ms | 1,170.1 ms |
| Median instantiation | 13.55 ms | 7.85 ms |
| Median initial `go.run` call | 69.00 ms | 56.65 ms |

The controlled comparison reduced raw bytes by 57.8%, gzip transfer bytes by
56.7%, and median hydration time by 52.3%. Each variant ran six times, alternating
order, in a fresh headless Chrome process with HTTP cache disabled, 10 Mbit/s
download/upload throughput, 40 ms CDP latency and 4× CPU throttling. WASM and
HTML were served from an ephemeral localhost HTTP server; WASM was gzip encoded.
The matching Go `wasm_exec.js` was served without compression for both variants.
Ready time includes document/runtime startup, not just the WASM download.
The startup harness uses its level-9 Node gzip artifacts; the separate Go gzip
row shows the middleware's default compression level applied to the same bytes.
The [raw report](benchmarks/fetch-browser-2026-10-23.json) retains every sample
and the exact measurement settings.

Artifact SHA-256:

```text
baseline  98319568b86cb5a530016fabdf6ff8440b6ff6e38d72689ca903d43b899e0fa6
candidate c9cbec598e0c5cf706871140d4ad3fb0dae03499eb20d957f481955ef6b8b671
```

Changing just framework navigation while leaving the toolbox's `net/http`
action import produced no size improvement: the large HTTP dependency graph
remained linked. Exact bundle sizes depend on the Go toolchain, application
imports, generated registrations and compression implementation. The table
describes those measured artifacts; future builds need fresh measurements.

## Reproduce a browser comparison

Keep before/after WASM artifacts built with the same Go toolchain and a
counter component registered as `counter`. Install the repository's Node
development dependencies and Chromium, then run:

```sh
npm ci
npx playwright install chromium
node scripts/benchmark-browser.mjs \
  --baseline /absolute/path/before.wasm \
  --candidate /absolute/path/after.wasm \
  --out /tmp/nextgo-startup.json
```

Optional flags: `--chromium /path/to/chrome`, `--trials 6`, `--cpu 4`,
`--latency 40`, `--mbps 10`, and `--runtime /path/to/wasm_exec.js`. The runtime
must match the compiler used for both artifacts. The script uses an ephemeral
port and records browser/toolchain/hardware versions, artifact hashes and sizes,
settings, every sample and medians. Browser runtimes do not capture actual
low-end device behavior merely by applying CPU throttling.

## Browser HTTP API and correctness checks

Call Fetch in a goroutine because JavaScript event callbacks must return before
browser Promises can settle. Attach context cancellation to component cleanup:

```go
ctx, cancel := context.WithCancel(context.Background())
scope.Cleanup(cancel)
scope.On(button, "click", func(client.Event) {
    go func() {
        response, err := client.Fetch(ctx, "/api/health", client.FetchOptions{
            MaxBytes: 64 << 10,
            Cache: "no-store",
        })
        if err != nil || ctx.Err() != nil { return }
        // Inspect response.StatusCode and decode response.Body in Go.
        _ = response
    }()
})
```

Fetch preserves binary bodies, returns HTTP error statuses normally, defaults to
same-origin credentials, and obeys browser CORS. The response body is read as a
bounded stream: default 8 MiB, configurable with positive `MaxBytes`. Cancellation
aborts the request and cancels its reader. Promise callbacks remain live until
settlement even when the caller has already returned. Context deadlines use the
same mechanism. `Cache` accepts browser Fetch cache modes; an empty value uses
the browser default. Navigation and loading requests use `no-store`, so Go's
explicit navigation cache owns refresh/invalidation rather than an independent
browser HTTP cache.

Run native cache/lifecycle tests and browser-compatible WASM Fetch tests:

```sh
go test -race ./client
GOOS=js GOARCH=wasm go test \
  -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./client
```

The latter uses Node's Fetch/Response/ReadableStream built-ins and controlled
transport mocks. Tests cover byte limits with and without Content-Length,
decoded-versus-compressed size, binary requests/responses, headers, HTTP and
network errors, cache modes, deadlines, cancellation during body reading, and
late Promise settlement. Real Chromium navigation tests additionally exercise
oversized responses, actual network cancellation, independent component
lifecycles, and HTTP-cache/Refresh freshness without request interception.

## Initial regression budgets and remaining qualification

The native reference benchmark is available as:

```sh
go test -run '^$' -bench '^BenchmarkHTTP$' -benchmem -benchtime=300ms -count=3 .
```

An initial run on the Go/hardware environment above produced these medians:

| Handler scenario | Time/op | Bytes allocated/op | Allocs/op |
| --- | ---: | ---: | ---: |
| Tiny SSR | 5.61 µs | 7,436 | 129 |
| 100-item SSR | 95.12 µs | 195,074 | 2,616 |
| 100-item JSON navigation envelope | 101.54 µs | 180,057 | 2,563 |
| Warm cached navigation envelope | 12.81 µs | 16,267 | 46 |
| Gzip navigation envelope | 209.02 µs | 1,273,968 | 2,602 |

These sequential handler-only measurements exclude sockets, TLS, browser work,
database/loader latency and concurrent requests. They include application node
construction, request handling, headers and rendering/compression; the warm-cache
case reuses explicitly public output. Response bytes are counted instead of
buffered by a test recorder. Compression allocation is an identified optimization
target in this baseline, not a production memory bound.

Use these as proposed release gates for the small reference application, not
universal application guarantees. They need stable CI hardware and repeated
baselines before enforcing timing thresholds:

- Raw reference-app WASM ≤ 6 MiB and level-9 gzip ≤ 1.6 MiB on the pinned Go
  toolchain; review additions to the browser dependency graph when either grows.
- Cold counter hydration ≤ 2 seconds median under the benchmark settings above;
  investigate a >20% median regression across at least ten fresh-process runs.
- Warm local navigation commits within 100 ms after its response finishes;
  include persistent-layout state and event-disposal assertions during profiling.
- Record native SSR/envelope throughput, allocations, warm-cache hit latency and
  gzip overhead with `go test -bench ... -benchmem` on pinned hardware. Qualify
  realistic concurrent loaders, large pages, cancellation and memory bounds
  separately; a tiny static-page benchmark cannot establish these properties.

Production qualification still needs representative applications, real-device
and field data, load/soak testing, CDN/cold-start behavior on each deployment
target, and a controlled Next.js comparison using equivalent content and
interaction. The eager bundle remains application-wide. Opt-in [lazy component modules](LAZY_COMPONENTS.md) now defer individual component implementations, using independent Go runtimes. Automatic route splitting, shared dependency grouping and full multi-runtime memory/performance qualification remain open.


## Opt-in lazy module observation (October 23, 2026)

The [recorded Go 1.27.3/macOS arm64 sample](benchmarks/lazy-go1.27.3-macos-arm64.json) uses ten new Chromium contexts in one browser process, a warm local development server and no CPU/network throttling. The core WASM was 6,488,360 bytes (1,738,851 bytes at gzip level 9); the lazy counter module was 6,145,861 bytes (1,660,672 gzip). The unique lazy application proof string was absent from core bytes, and a page without lazy islands made no module request. The full counter page requested both bundles. This counter is deliberately small: copied Go/framework dependencies dominate its additional download. This is evidence of the tradeoff, not a download reduction for that page.

At hydration readiness each context exposed two WASM linear memories: 20,447,232 and 5,767,168 bytes. Those are memory-buffer capacities, not Go live-heap or browser-process RSS/peak measurements. They exclude JIT code, JavaScript objects, fetched bytes, compilation buffers, other browser memory and future growth. Two counters shared the second runtime. The eight-module admission limit is not a total-memory guarantee. Measurement instrumentation retains memory references until that context closes.

Observed navigation-to-ready times were 140–150 ms, median 143 ms. The shared browser process can reuse compiled code, so these are warm local observations, not ten independent cold-process measurements or real-network performance. Concurrent qualification workloads were running. They cannot qualify the cold-hydration gate above. This core exceeds the proposed 6 MiB raw/1.6 MiB gzip reference budget on this SDK, so the earlier smaller reference is historical evidence; the size gate remains open. Reproduce with a running demo and `node scripts/measure-lazy.mjs`; set `NEXTGO_BENCH_ORIGIN` and, if needed, `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`. Preserve the exact SDK, browser and machine context for comparisons.

Route grouping, further reduction of duplicated framework code in module executables, dependency sharing and multi-runtime peak-memory/soak/device qualification remain optimization work. See [lazy components](LAZY_COMPONENTS.md) for supported semantics.


### Compiler-target follow-up (same local environment)

The [target-separated observation](benchmarks/lazy-target-go1.27.3-macos-arm64.json) repeats the ten-context local harness after compiling the module's document APIs as bridge-only branches. The earlier sample remains above as historical evidence. Both use Go 1.27.3 on macOS arm64; they are artifact comparisons, not a Next.js benchmark.

| Artifact | Previous raw bytes | Target-separated raw bytes | Previous gzip-9 bytes | Target-separated gzip-9 bytes |
| --- | ---: | ---: | ---: | ---: |
| Eager core | 6,488,360 | 6,481,733 | 1,738,851 | 1,736,824 |
| Lazy counter | 6,145,861 | 5,424,260 | 1,660,672 | 1,484,643 |

The lazy executable shrank 11.74% raw and 10.60% at gzip level 9. This establishes a byte reduction for these measured artifacts. It still includes its own Go runtime and copied dependencies; no shared-heap or shared-dependency linking is implemented.

At readiness the two linear-memory capacities were 20,447,232 and 5,242,880 bytes, compared with 20,447,232 and 5,767,168 previously. These capacities retain all the limits described above; they are not peak/process memory evidence. Local navigation-to-ready was 141–157 ms, median 147.5 ms, versus the previous 143 ms median. The harness and concurrent machine load do not support a startup-speed improvement claim. The eager bundle still exceeds the proposed raw/gzip size budget. Representative cold-process/network/device, multi-module, peak-memory/soak and cross-framework qualification remain open.


### Managed-script integration observation

The [managed-script build observation](benchmarks/scripts-go1.27.3-macos-arm64.json) uses the same Go 1.27.3/macOS arm64 ten-context local harness on `/lazy`. Optional vendor scripts are not declared on that page; this measures the framework/adapter code cost, not third-party library download or execution.

The eager core is 6,605,072 bytes raw / 1,766,108 gzip-9; the lazy component is 5,533,787 raw / 1,510,364 gzip-9. Compared with the compiler-target follow-up above, those add 123,339 raw / 29,284 gzip bytes to core and 109,527 raw / 25,721 gzip bytes to the module. The proposed eager-size gate remains exceeded. Dependencies declared on other pages add their own browser download and execution costs.

### Keyed collection integration observation (October 24, 2026 UTC)

The [keyed-page report](benchmarks/keyed-go1.27.3-macos-arm64.json) and
[current lazy-page report](benchmarks/keyed-lazy-go1.27.3-macos-arm64.json)
record Go 1.27.3, Chromium 156.0.8078.4, Node 25.7.0 and Apple M4 Max/macOS
Darwin 27.0.0. Both use ten new contexts in one browser process, a warm local
development server and no throttling. Reports include artifact hashes, every
sample and environment information. Reproduce with the commands below after
starting the demo; the browser executable override is optional when Playwright's
default browser is installed.

```sh
NEXTGO_BENCH_PATH=/keyed node scripts/measure-lazy.mjs
node scripts/measure-lazy.mjs
```

| Artifact | Raw bytes | Gzip level 9 bytes |
| --- | ---: | ---: |
| Eager sample | 6,982,485 | 1,863,420 |
| Lazy counter module | 5,610,389 | 1,535,244 |

Compared with the historical managed-script sample above, core grew by 377,413
raw / 97,312 gzip bytes, and the lazy module by 76,602 raw / 24,880 gzip bytes.
Other framework/sample additions occurred between these observations, so this
delta does not isolate the keyed API's marginal cost. Keyed browser views import
shared `ui` rendering; `ui` uses a small structural `Flush()` interface so that
its server streaming helper does not import `net/http` into every browser graph.
The native/browser graph regression test guards that separation. Both current
eager sizes exceed the proposed reference budget; optimization and release
performance qualification remain open.

The `/keyed` page requested one WASM artifact and reached hydration readiness in
61–118 ms, median 61 ms, with a 6,291,456-byte linear-memory capacity. `/lazy`
requested both artifacts and reached readiness in 144–150 ms, median 147.5 ms,
with capacities of 20,709,376 and 5,767,168 bytes. These are warm local observations,
with concurrent validation workloads. Browser-process compilation reuse, local
network and the tiny sample make them unsuitable for cold startup or production
latency claims. Memory capacities exclude Go live-heap analysis, process RSS,
JIT/JS/network buffers and peak/long-lived behavior. No Next.js comparison or
full performance gate is established by these measurements.

### Component lifetime integration observation (October 24, 2026 UTC)

The [composition-page report](benchmarks/composition-go1.27.3-macos-arm64.json)
and [lazy-page follow-up](benchmarks/composition-lazy-go1.27.3-macos-arm64.json)
use the same Go 1.27.3/Chromium 156.0.8078.4/Apple M4 Max environment and
ten-context warm-local harness described above. They include artifact hashes
and environment metadata. Set `NEXTGO_BENCH_PATH=/composition` to repeat the
first observation; omit that setting for `/lazy`.

| Artifact | Raw bytes | Gzip level 9 bytes | Change from keyed sample (raw / gzip) |
| --- | ---: | ---: | ---: |
| Eager sample | 7,067,083 | 1,884,827 | +84,598 / +21,407 |
| Lazy counter module | 5,617,449 | 1,537,524 | +7,060 / +2,280 |

The eager change includes new component demonstration code and lifetime/context
support. It does not isolate a framework-only marginal cost. The lazy counter
does not use these APIs directly. Both eager size budgets remain exceeded.
These measurements cannot establish acceptable production startup or memory.

Composition hydration readiness was 60–70 ms, median 63 ms, with a
6,291,456-byte linear-memory capacity. The current `/lazy` page observed
150–166 ms, median 154.5 ms, with two capacities of 20,709,376 and 5,767,168
bytes. Concurrent test load and compiled-code reuse limit comparisons. As with
the preceding reports, these are local observations without throttling, cold
process isolation, representative network/device conditions, live-heap/peak
RSS analysis, field data or a Next.js comparison. All release qualification and
reference-size gates remain open.

### Typed form integration observation (October 24, 2026 UTC)

The [form-page report](benchmarks/forms-go1.27.3-macos-arm64.json) and
[lazy-page report](benchmarks/forms-lazy-go1.27.3-macos-arm64.json) use Go
1.27.3, Chromium 156.0.8078.4, Node 25.7.0 and Apple M4 Max/macOS Darwin
27.0.0. Each has ten new contexts in one browser process against a warm local
development server on port 3010, without CPU or network throttling.

| Artifact | Raw bytes | Gzip level 9 bytes |
| --- | ---: | ---: |
| Eager sample | 7,471,441 | 1,974,272 |
| Lazy counter module | 5,617,438 | 1,537,173 |

The eager sample grew 404,358 raw / 89,445 gzip bytes relative to the component
lifetime observation. This includes the typed model, DOM bindings, shared
validation and demonstration components, rather than isolating a library-only
cost. The lazy counter artifact is essentially unchanged; its small byte delta
is not evidence of a performance improvement. The eager reference-size budget
remains exceeded.

Form readiness was 101–168 ms, median 104 ms, with a 6,291,456-byte WASM
linear-memory capacity. `/lazy` readiness was 199–225 ms, median 208 ms, with
capacities of 20,971,520 and 5,767,168 bytes. These buffer capacities are not
live Go heap, browser RSS or peak-memory measurements. Warm compilation reuse
and machine load limit timing comparisons; no cold-start, real-device, field
or Next.js performance claim follows.

An earlier lazy-page measurement failed when Playwright could not retrieve a
debugger response body after readiness. The harness now falls back to a direct
artifact request outside the measured readiness window, recording `bodySource`
for every asset. The completed reports above used browser response bodies and
include hashes for all samples. A generation URL must remain available while
its bytes are recorded; changing sources during measurement is unsupported.

```sh
NEXTGO_BENCH_ORIGIN=http://127.0.0.1:3010 NEXTGO_BENCH_PATH=/forms node scripts/measure-lazy.mjs
NEXTGO_BENCH_ORIGIN=http://127.0.0.1:3010 node scripts/measure-lazy.mjs
```


### Explicit/blur form validation observation (October 24, 2026 UTC)

The [validation form report](benchmarks/blur-forms-go1.27.3-macos-arm64.json)
and [lazy follow-up](benchmarks/blur-forms-lazy-go1.27.3-macos-arm64.json)
use the same ten-context Go 1.27.3/Chromium 156.0.8078.4/Apple M4 Max harness
on port 3010. The form path is `/forms?validate=blur`. Qualification containers
were also running, so timing comparisons include uncontrolled machine load.
All samples used browser response bodies and include artifact hashes.

| Artifact | Raw bytes | Gzip level 9 bytes |
| --- | ---: | ---: |
| Eager sample | 7,482,915 | 1,976,618 |
| Lazy counter module | 5,617,438 | 1,537,171 |

The eager sample grew 11,474 raw / 2,346 gzip bytes from the initial typed-form
observation. Its SHA-256 is
`bf418f173c874faa2eb5f3b9fe5152f284d20684f91b4c436d8d2c6364d6200e`.
The raw lazy-module size is unchanged; a two-byte gzip difference is not a
performance improvement. The eager size budgets remain exceeded.

Validation-form readiness was 91–219 ms (median 110.5 ms), with a 6,291,456-byte
linear-memory capacity. `/lazy` readiness was 186–216 ms (median 198 ms).
Its core capacity varied from 17,825,792 to 22,020,096 bytes and its module
capacity was 5,767,168 bytes. These are capacity observations, not live heap,
peak RSS, cold startup, representative network/device behavior or a Next.js
comparison. No production performance gate is satisfied by these reports.

### Choice controls and atomic capture observation (October 24, 2026 UTC)

The [choice-page report](benchmarks/choices-go1.27.3-macos-arm64.json) and
[lazy follow-up](benchmarks/choices-lazy-go1.27.3-macos-arm64.json) measure the
corrected radio/multiple-select and atomic-capture implementation. They use
Go 1.27.3, Chromium 156.0.8078.4, Node 25.7.0 and Apple M4 Max/Darwin 27.0.0,
ten new contexts in one browser process against the warm development server on
port 3010. Concurrent qualification work and compiled-code reuse limit timing
comparisons. All asset bodies came from browser responses.

| Artifact | Raw bytes | Gzip level 9 bytes |
| --- | ---: | ---: |
| Eager sample | 7,796,910 | 2,044,551 |
| Lazy counter module | 5,617,451 | 1,537,175 |

The eager sample grew 313,995 raw / 67,933 gzip bytes from the explicit/blur
validation observation. This includes the new demonstration, typed input
instantiations, radio/multiple helpers and shared atomic DOM capture; it is not
a library-only marginal measurement. Its SHA-256 is
`e785bc4e0514d4dc8c16756abeeb74ceeb046009c885264556ae118571a47a80`.
The lazy module's tiny byte difference is not a performance improvement.
Both eager reference-size budgets remain exceeded.

Choice-page readiness was 62–66 ms, median 62.5 ms, with a 6,291,456-byte
linear-memory capacity. `/lazy` readiness was 144–171 ms, median 153 ms; its core
capacity was 21,233,664 or 24,641,536 bytes, with a 5,767,168-byte module capacity.
These observations do not measure live heap, peak RSS, cold startup or physical
devices. Different load/cache conditions prevent a speed-improvement claim.
Representative network/device, production-load and Next.js comparisons remain
required by the open performance gate.

### File controls and bounded upload observation (October 24, 2026 UTC)

The [upload-page report](benchmarks/uploads-go1.27.3-macos-arm64.json) and
[lazy follow-up](benchmarks/uploads-lazy-go1.27.3-macos-arm64.json) use Go 1.27.3,
Chromium 156.0.8078.4, Node 25.7.0 and Apple M4 Max/Darwin 27.0.0. Ten contexts
share one browser process against the warm development server on port 3010.
Qualification work was concurrent. All asset bodies came from browser responses.

| Artifact | Raw bytes | Gzip level 9 bytes |
| --- | ---: | ---: |
| Eager sample | 8,135,163 | 2,114,363 |
| Lazy counter module | 5,617,476 | 1,537,112 |

The eager sample grew 338,253 raw / 69,812 gzip bytes from choice/capture.
The measurement includes the file demo, typed model/DTO instantiations, file
binding, reader and shared validation/hash code; it does not isolate a library
marginal cost. Its SHA-256 is
`05aec2df827a7e3b6a329d0b380daf1b6502b52ae254da66fbdf11f1f2e394df`.
Tiny lazy-module byte differences do not establish a performance improvement.
Both eager reference-size budgets remain exceeded.

Upload-page hydration readiness was 58–62 ms, median 60 ms, with a 6,815,744-byte
linear-memory capacity. `/lazy` readiness was 115–142 ms, median 140.5 ms, with
core capacities of 21,233,664 to 25,165,824 bytes and a 5,767,168-byte module
capacity. These are warm-local readiness/capacity observations, not cold startup,
live heap, peak RSS, representative device/network or Next.js comparisons.
They do not measure file-read/upload throughput, encoding memory, contention or
object-store performance. Large-file and long-lived upload qualification remains
required; no production performance gate is satisfied by these reports.

### Native multipart integration observation (October 24, 2026 UTC)

The [upload-page report](benchmarks/multipart-go1.27.3-macos-arm64.json) and
[lazy follow-up](benchmarks/multipart-lazy-go1.27.3-macos-arm64.json) use the same
Go 1.27.3/Chromium 156.0.8078.4/Node 25.7.0/Apple M4 Max environment and ten-context
warm-local harness on port 3010. Qualification work was concurrent. All body
captures used browser responses.

| Artifact | Raw bytes | Gzip level 9 bytes |
| --- | ---: | ---: |
| Eager sample | 8,162,036 | 2,119,573 |
| Lazy counter module | 5,617,477 | 1,537,221 |

The eager change from the file-control observation is +26,873 raw / +5,210 gzip
bytes, including builder, shared transport and demonstration changes. Its SHA-256
is `a7c039135566e5310b40dcc1234854d2d494985f1404613df5d099d5fda33ad8`.
Both eager size budgets still fail. The tiny lazy difference is not a speed
improvement.

Readiness was 64–73 ms (median 67 ms) on `/uploads` and 152–171 ms (median 158 ms)
on `/lazy`. Capacity observations were 6,815,744 bytes on the upload page and
21,233,664 plus 5,767,168 bytes on the lazy page. These are warm-local readiness
and capacity measurements, not cold startup, live heap, RSS, file throughput or
production/Next.js comparisons.

The [transport body-size report](benchmarks/multipart-transfer-go1.27.3-macos-arm64.json)
uses one synthetic pair of two 256-KiB files through a local streaming byte-count
proxy. Multipart sent **524,759 body bytes**; base64 JSON sent **699,167**, a
174,408-byte (about 24.9%) reduction for that payload. Counts include multipart
framing but exclude HTTP headers, TLS and transport framing; declared Content-Length
matched each observed count. No file contents are logged or retained by the
measurement proxy. This does not establish transfer speed, memory bounds,
large-file behavior or a general performance improvement.

Repeat with the demo running on port 3010:

```sh
NEXTGO_BENCH_ORIGIN=http://127.0.0.1:3010 node scripts/measure-upload-transport.mjs
```

The first observation attempt was rejected by the dev server's trusted Host
check. The harness translates its own local proxy Host/Origin to the configured
local target and rejects foreign origins; framework Host/Origin checks were not
relaxed. The completed report follows that correction.


## Shared-cache integration observation (October 24, 2026 UTC)

The native-only shared-data API adds no new application JavaScript. The current
[core bundle observation](benchmarks/shared-cache-go1.27.3-macos-arm64.json)
records **8,162,036 raw bytes / 2,119,573 gzip-9 bytes**, unchanged byte counts
from the preceding multipart observation. Its SHA-256 is
`243fcc855fa520a54f7b2af3d0a2c1ca8824bd29f432862bc50a79a215f8e1dd`.
Build identity changes alter bytes; matching size is not an identical artifact.
Both the 6-MiB raw and 1.6-MiB gzip-9 reference budgets still fail.

Ten fresh Chromium contexts against the warm local Go 1.27.3 development preview
on port 3011 observed `/uploads` readiness of 61–109 ms (median 64 ms) and a
6,815,744-byte linear-memory capacity. Other qualification work was active;
there was no workload isolation or CPU/network throttling. These values do not
measure shared-cache throughput/latency, heap/RSS, cold starts, mobile devices,
production or Next.js performance. No performance improvement is claimed.


## Scheduled shared-refresh observation (October 24, 2026 UTC)

The [updated core observation](benchmarks/shared-refresh-go1.27.3-macos-arm64.json)
records **8,162,036 raw bytes / 2,119,572 gzip-9 bytes**, SHA-256
`525cf07dfdef826b92ac43546f9bfc6dc85a301fe967fbb3959f701c87cb4b45`.
The new refresh scheduler and wire adapter remain native-only. Raw size is
unchanged; the one-byte gzip difference is not an improvement. The 6-MiB raw and
1.6-MiB gzip-9 reference budgets remain exceeded.

Ten fresh Chromium contexts against the warm local Go 1.27.3 development preview
at port 3011 observed `/uploads` readiness of 60–108 ms (median 63 ms), with a
6,815,744-byte linear-memory capacity. Other qualification was active. There was
no CPU/network throttling or workload isolation; these are not production,
shared-cache performance, heap/RSS, cold/device or Next.js comparison results.


## Shared rendered-page observation (October 24, 2026 UTC)

The [then-current core observation](benchmarks/shared-pages-go1.27.3-macos-arm64.json)
records **8,162,036 raw bytes / 2,119,557 gzip-9 bytes**, SHA-256
`c4da65f544272a747791507b413034f79bad979e953cbcaa393e7799ced06b44`.
Shared snapshot serialization, head reconstruction and invalidation remain
native-only. Raw size is unchanged; the small compressed difference reflects
artifact/build identity and is not a performance improvement. Both the 6-MiB raw
and 1.6-MiB gzip-9 reference budgets remain exceeded.

Ten fresh Chromium contexts against the warm Go 1.27.3 development preview at
port 3011 observed `/uploads` readiness of 62–70 ms (median 64 ms), with a
6,815,744-byte linear-memory capacity. Other qualification was active; there was
no workload isolation or CPU/network throttling. This does not measure shared
page-cache latency/throughput, heap/RSS, cold/device behavior or a production/
Next.js comparison.


## Shared-prerender observation (October 24, 2026 UTC)

The [shared-prerender report](benchmarks/shared-prerender-go1.27.3-macos-arm64.json)
records **8,162,036 raw bytes / 2,119,562 gzip-9 bytes**, SHA-256
`06c662c4fc2f77b2810025eb4221ee2b584e3333306f9b11f1174ab7c882d5b0`.
Build-seed admission and regeneration remain native-only; the sample reuses
the existing Go counter islands. These artifact bytes do not isolate the
marginal cost of that feature. Both the **6-MiB raw and 1.6-MiB gzip-9 budgets
still fail**.

The report uses Go 1.27.3 (`darwin/arm64`), Chromium 156.0.8078.4, Node 25.7.0
and Apple M4 Max/Darwin 27.0.0. Ten new contexts in one browser process against
the warm development preview on port 3011 observed `/shared-prerender/alpha`
hydration readiness of **59–80 ms (median 66 ms)** and a **6,815,744-byte** WASM
linear-memory capacity. All recorded asset bodies came from browser responses.

Qualification workloads were running concurrently; there was no CPU/network
throttling or workload isolation. The path also differs from the preceding
`/uploads` observations, and the browser process can reuse compiled code.
These timings support no improvement or regression comparison. Linear-memory
capacity is not live heap, peak memory or process RSS. This observation does
not measure shared-cache throughput/latency, production cold starts, mobile
devices, representative networks, field behavior or Next.js performance, and
does not satisfy a production performance gate.


## Seed-ledger lease bookkeeping observation (October 24, 2026 UTC)

The [final native benchmark output](benchmarks/control-ledger-go1.27.3-macos-arm64.txt)
records Go 1.27.3 (`darwin/arm64`) on Apple M4 Max. Each operation acquires a
missing-value lease through `File.Begin` and releases it through `File.Release`.
The file backend already holds one import session containing 0, 1,000 or
10,000 once-per-key seed hashes; expired seed values are not retained. Setup
and journal work occur outside the measured loop. The loop performs no
publication, journal writes or filesystem synchronization.

| Retained seed hashes | Samples | Bytes allocated/op | Allocations/op |
| ---: | ---: | ---: | ---: |
| 0 | 3 | 2,997 | 27 |
| 1,000 | 3 | 2,997 | 27 |
| 10,000 | 3 | 2,997 | 27 |

All nine samples report the same per-operation allocation counts, with timings
of **1,599–2,021 ns/op**. The measurement includes retained-string ownership
allocations.
Concurrent native qualification was active, so timing is not an isolated
comparison and supports no before/after speed claim. The result concerns this
specific lease bookkeeping path; it does not establish journal/storage or HTTP
throughput, total heap/RSS, cold starts, concurrent production load or a Next.js
comparison. Seed-control accounting remains separate from temporary allocations
and process memory.

Repeat this benchmark with the desired Go SDK:

```sh
go test -run '^$' -bench '^BenchmarkFileLeaseChurnWithSeedLedger$' \
  -benchmem -count=3 ./cache/shared
```

This native observation provides no new browser-bundle measurement. The latest
recorded sample above still exceeds both the **6-MiB raw and 1.6-MiB gzip-9 WASM
budgets**. No production performance gate is satisfied by this benchmark.
