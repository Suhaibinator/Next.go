# Local Go/WASM and Next.js production comparison

This is a small matched-workload experiment against stable **Next.js 16.4.5**,
not a framework-wide performance claim or a passed G05 release gate. It
compares generated Go 1.27.3 and compatibility-only Go 1.24.13 executables with
a production standalone Next.js/React application on the same local machine.
Go 1.24 has known advisories and is not a production recommendation.

The raw observations and qualification record below identify the exact source,
dependencies, native entrypoints, packaged Next.js files and fetched runtime
bytes. Previous exploratory and failing runs remain separate evidence.

## Equivalent visible workload

[`examples/comparison`](../examples/comparison/README.md) and the isolated
[`tests/performance/nextjs`](../tests/performance/nextjs/package.json) reference
have the same text/CSS/favicon, two pages rendered for each request, and a
counter initially rendered as 3. Both initially disable its button, preserve
the SSR output node, enable it after mounting, increment to 4, and navigate to
the about page without replacing the document. A no-JavaScript pass checks
meaningful SSR and native link navigation. Framework-generated document markup,
transport, bootstrap, styles and runtime costs remain part of each result.

Automatic prefetch is disabled in both fixtures. Next.js declares
`dynamic = 'force-dynamic'`; the Go fixture declares neither route caching nor
prerendering. There are no databases, external assets/fonts, server actions,
streaming regions, lazy components, cache fills or application middleware.
This is a useful baseline rather than a representative large production app.
The Next.js dependency is confined to its reference fixture; it is not a
dependency of the Go framework or its generated application.

## Reproduce

Use Linux, Node 24, the root Playwright dependency, Chromium, and both named
Go SDKs. Install the reference with its checked-in lockfile:

```sh
npm ci
npx playwright install chromium
npm --prefix tests/performance/nextjs ci --ignore-scripts --no-audit --no-fund
NEXT_TELEMETRY_DISABLED=1 npm --prefix tests/performance/nextjs run build
mkdir -p .nextgo-test-results/framework-comparison/nextjs-standalone
cp -a tests/performance/nextjs/.next/standalone/. .nextgo-test-results/framework-comparison/nextjs-standalone/
cp -a tests/performance/nextjs/.next/static .nextgo-test-results/framework-comparison/nextjs-standalone/.next/static
```

Build the Go fixture once per SDK, placing the matching SDK first in PATH for
each invocation. For example, run this with Go 1.27.3, then repeat with Go
1.24.13 and the output suffix `go1.24`:

```sh
GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=2 CGO_ENABLED=0 \
go run ./cmd/nextgo build -out .nextgo-test-results/framework-comparison/go1.27 ./examples/comparison
```

Run the controlled observation harness. An installed Chromium may be selected
with `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH`; otherwise Playwright's browser is
used. Select a fresh output directory to retain each run independently.

```sh
node tests/performance/compare.mjs \
  --go27="$PWD/.nextgo-test-results/framework-comparison/go1.27/server" \
  --go24="$PWD/.nextgo-test-results/framework-comparison/go1.24/server" \
  --next="$PWD/.nextgo-test-results/framework-comparison/nextjs-standalone" \
  --out="$PWD/.nextgo-test-results/framework-comparison/observations" \
  --samples=12 --requests=1000
```

The `comparison` CI job builds these versions, checks generated helpers/native
and WASM vet, runs the harness and retains artifacts. It is configured and
syntax-checked; remote results for this working tree remain unverified.

## Controls and interpretation

Each server binds a separate loopback address and receives a complete synthetic
environment. Go runs from an empty temporary working directory; Next.js runs
from its copied standalone package with copied static assets. No development
server or source checkout is required at runtime. Go uses `GOMAXPROCS=2` and
one server process; Next.js uses one Node server without a cluster. Next's
`experimental.cpus=1` constrains build workers, not runtime CPU ownership.
The shared host has no CPU affinity/cgroup control in this experiment.

Each of 12 rounds launches a fresh browser process per variant, reverses the
variant order on alternating rounds, and uses a 1280×800 desktop viewport with
reduced motion. Network is unthrottled loopback. Server/OS caches are warm after
the startup SSR probe. Browser processes are independent, but OS/compiler
caches and shared-host load still affect timing. The runner fixes Chromium's
temporary root to `/tmp` to avoid Unix socket-name overflow under long build
workspace paths; Playwright cleans up its owned files.

The readiness mark records this counter's application mount. Browser timing
starts at navigation; Node-observed navigation and readiness include automation
overhead and use a different clock. FCP, observed LCP and CLS cover this lab
load before interaction; they are not field Web Vitals or INP. A dozen samples
cannot characterize production tails: nearest-rank p95/p99 both select the
largest sample. Outliers remain in the report.

Initial encoded/decoded body totals come from browser Resource Timing, include
the document and all resources observed before interaction, and exclude HTTP
header accounting. They differ from Go gzip-9 analysis and from actual CDN
behavior. After timed phases, the harness fetches each observed path, retains
bounded decoded bytes and hashes, and requires HTTP 200. Served-file capture
is outside the timed observations. Both page exceptions and console errors
must be absent in the JavaScript behavior checks.

Native SSR load runs 1,000 completed requests at concurrency 1 and then 8, with
ten warmups per phase, identity encoding and response-body consumption. The
reported latency includes the local client; throughput is not a saturation
limit. Server RSS/high-water RSS and CPU tick counts are Linux process
observations, excluding browser memory and other processes. They do not measure
Go/WASM live heap, whole-machine memory, cold starts or provider billing.

All owned servers must exit after SIGTERM and close their listeners without a
forced kill. Next's exit code 143 records its SIGTERM convention. This is
cleanup evidence, not a comprehensive drain/recovery test.

## November 7 local observations

The corrected final run passes **36 fresh-browser behavior samples**, three
no-JavaScript checks and **6,000 native SSR requests**, with no page/console
errors and all owned server/listener cleanup checks passing. Node 24.22.1,
Playwright 1.64.1 and Chromium 156.0.8078.79 ran on a shared AMD EPYC Linux
host. Served files were fetched and hashed after timing. Native/WASM vet and
generated-helper checks pass for the Go fixture on both SDKs.

| Observation | Go 1.27.3 | Go 1.24.13, compatibility only | Next.js 16.4.5 |
| --- | ---: | ---: | ---: |
| Initial encoded body bytes, median | 1,853,243 | 1,466,888 | 141,456 |
| Counter mount mark, median ms | 216.9 | 327.1 | 127.8 |
| Counter mount mark, observed maximum ms | 276.8 | 374.0 | 156.0 |
| FCP / observed LCP, median ms | 44 / 44 | 48 / 48 | 64 / 64 |
| CLS, maximum in samples | 0 | 0 | 0 |
| Automation-observed soft navigation, median ms | 75.5 | 74.5 | 61.5 |
| Native SSR concurrency 1, p50 / p95 / p99 ms | 0.340 / 0.635 / 1.108 | 0.299 / 0.453 / 0.991 | 2.053 / 4.148 / 8.226 |
| Native SSR concurrency 8, p50 / p95 / p99 ms | 1.247 / 1.837 / 4.524 | 1.132 / 2.395 / 4.492 | 12.152 / 18.674 / 23.245 |

For this workload, Go's local native SSR timings were lower while its initial
body transfer and counter readiness were higher. The table does not establish
those relationships for other applications or deployments. The earlier run's
Go 1.27 FCP outlier and all prior failed probes remain archived; the final run
is not used to erase their evidence.

The exact HTTP-served Go cores also pass through `analyze -runtime-dir` after
capture. Go 1.27's core measures **6,584,602 raw / 1,780,652 Go gzip-9 bytes**,
exceeding both 6-MiB/1.6-MiB reference limits even for this one-counter fixture.
Go 1.24's core is **5,344,183 / 1,449,020** and fits those limits; its unsupported
SDK/security status makes this an unsuitable production workaround. Full-demo
bundles remain larger and still fail both budgets on both SDKs. The runtime
analysis compression is distinct from the browser's total encoded bytes above.

[Raw final observations](benchmarks/local-framework-comparison.json),
[qualification/provenance](benchmarks/local-framework-comparison-qualification.json),
[Go 1.27 served-runtime analysis](benchmarks/comparison-served-runtime-go1.27.json),
[Go 1.24 served-runtime analysis](benchmarks/comparison-served-runtime-go1.24.json).

## Remaining qualification

Broader equivalent applications, representative devices and networks, repeated
isolated trials, cold/warm build and startup distributions, CPU/memory profiles,
soak and saturation, full field vitals, Linux container performance and actual
serverless/CDN observations remain open. The experiment neither changes byte
budgets nor establishes feature equivalence. See [the full inventory and open
gates](PARITY.md), [SDK security policy](SECURITY_QUALIFICATION.md) and
[artifact analysis](BUNDLE_ANALYSIS.md).
