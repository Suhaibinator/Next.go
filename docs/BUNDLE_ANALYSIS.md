# Go-native build artifact analysis

`nextgo analyze` compiles an ordinary application in production mode and reports
that successful generation as JSON. It uses the same compiler, eager/lazy
registrations and paired WASM shim as `nextgo build`. It introduces no browser
JavaScript, server endpoint or production dependency.

```sh
go run ./cmd/nextgo analyze ./examples/demo > analysis.json
go run ./cmd/nextgo analyze -out analysis.json ./examples/demo
```

Compiler and application build diagnostics go to stderr. JSON goes to stdout
unless `-out` selects an atomically replaced file in an existing directory. The
temporary compiled generation is removed after success, measurement failure,
report-write failure or a failed size budget. This default mode measures a fresh
build. Use the existing-runtime mode below to measure published asset files.

As with `build`, application asset tools, Configure and declared prerender work
can execute during compilation. The measurement stage itself does not execute
the generated server or rediscover files after the build succeeds. It uses the
discovery metadata belonging to that compiled generation.

## Measure existing published runtime files

Select the exact immutable runtime namespace from a static publication or a
trusted copy of its deployed assets. No application directory is supplied:

```sh
nextgo analyze \
  -runtime-dir dist/static/_nextgo/0123456789abcdef0123 \
  -max-core-bytes 6291456 \
  -max-core-gzip-bytes 1677722 \
  -out published-runtime-analysis.json
```

Use the actual generation directory and configured asset prefix; the example
identifier is a placeholder. This mode requires `app.wasm`, `boot.js` and
`wasm_exec.js`. It reads all files within that namespace, including lazy modules
and worker assets. It neither compiles application code nor executes the files,
and works without a Go SDK, Node or source checkout. The caller owns the input;
analysis never cleans it up. Report output must be outside the measured
namespace, including when the output's parent is a symlink into it.

The separate version-1 runtime report contains `analyzerGo`, `compression`,
`scope`, `assets`, `totals`, `budgets` and `violations`. Asset/section/size and
budget semantics match the fresh-build report. It omits build/source identity,
native facts, routes and component registrations because those cannot be
inferred from these files. Hashes identify measured bytes; they do not
authenticate a deployment or prove the server embeds those assets. Keep the
namespace immutable during the read and compare hashes with your deployment
manifest. An `os.Root` confines reads to the opened namespace; symlink and
nonregular entries remain rejected. Bounds and cancellation still apply.

Failed byte budgets retain the complete report before returning nonzero, just
as fresh-build analysis does. The client-only CI matrix measures the same
static runtime tree used by its browser checks, after pinning that publication.
Remote execution of the new step remains unverified.

## What the report measures

The version-1 JSON report contains:

- Build identity and source fingerprint; native executable bytes, SHA-256,
  embedded Go version, target OS/architecture and CGO setting.
- Every file under the generation's immutable runtime namespace, with raw
  bytes, Go gzip level-9 bytes and SHA-256. Kinds distinguish core, lazy module,
  worker, bootstrap, shim and auxiliary runtime files.
- WASM section payload/framing bytes, including code, data and custom sections.
  Eight header bytes plus all payload/framing bytes equal the WASM file size.
- The compiler-managed eager/lazy registrations from this compilation, mapping each
  component to `app.wasm` or its actual content-addressed lazy file.
- Static page/API route summaries and layout/template/slot counts, from the
  completed build's route discovery metadata.
- Total runtime file bytes and the sum of separately compressed files, plus
  the core/bootstrap/shim subset. These totals are inventories, not a claim
  that all files download on every page.

`analyzerGo` identifies the Go version executing measurement, while `native.go`
identifies the SDK that produced the target executable. They may differ when
an installed CLI uses another Go executable through PATH or toolchain selection.
`compression` identifies `go-compress-gzip-level-9`.

Additional application-managed `client.Register` calls are not inferred by the
discovery metadata. The report contains public route/component names and runtime artifact names.
It omits source/import paths, environment/configuration values, props, action
payloads and private prerender contents. Native settings are restricted to the
listed toolchain facts. Reports are local development/CI files; no HTTP
inspection service is added.

## Enforce selected byte budgets

Each limit is opt-in. Zero disables it, equality passes, and negative values
are rejected before compiling application code.

```sh
go run ./cmd/nextgo analyze \
  -max-core-bytes 6291456 \
  -max-core-gzip-bytes 1677722 \
  -max-lazy-bytes 8388608 \
  -out analysis.json ./examples/demo
```

`-max-lazy-gzip-bytes` also limits each individual lazy file's Go gzip-9 size.
Core limits apply to `app.wasm`, independently of bootstrap/shim bytes. Lazy
limits apply per file, rather than to the sum of all modules. Every violation
records its file, metric, actual bytes and limit. The full JSON report is
written before the command returns an error/nonzero exit status. This lets CI
retain measurements from a rejected artifact.

The numbers above illustrate the existing reference budgets and a per-module
limit; they do not establish user-approved release budgets. A report with zero
limits and no violations establishes measurement success. It does not pass G05
or certify production performance. The CI native-SDK jobs retain an independent
demo analysis report; their measurement build is separate from their deployment
binary build, and its hashes must not be attributed to that other executable.

## Boundaries and remaining work

Raw and gzip-9 file sizes are reproducible properties of the specific artifacts
and compression implementation. Go gzip-9 bytes can differ from Node/zlib
gzip-9 bytes and from actual negotiated server/CDN encoding. They do not measure
cold startup, TTFB, Web Vitals, CPU, RSS, hydration, real-device behavior or
network transfer. No Next.js speed or memory comparison follows.

Eager registrations share the core. Route renderers can conditionally create
components, so static route summaries do not establish per-route component use,
request-time lazy downloads or incremental route transfer costs. The analyzer
does not infer those relationships, import graphs or per-package linked bytes.
The WASM parser summarizes bounded section envelopes; it is not an opcode/ABI
validator. Stripped production Go artifacts need not contain a function-name
section.

Runtime walking is bounded to 1,024 entries, 64 MiB per file and 256 MiB in
total; native executable input is bounded to 256 MiB. Symlink and nonregular
runtime entries are rejected. Cancellation is checked during walking, reads,
compression and before publication. These are resource bounds for a trusted
local build, not an OS sandbox for application build tools or protection against
another process mutating the private generation. Aggregate/independent-zone
analysis, public asset reports, live deployment pinning and native/runtime pairing, richer route/import
attribution, Brotli/CDN evidence and approved release-budget enforcement remain
open inventory work.

## Existing-runtime qualification (November 7, 2026)

Both Go 1.24.13 and 1.27.3 CLIs measure the previously pinned static demo
runtime without a Go executable in PATH, and inside restricted scratch
containers. The container has no source, SDK or network and mounts the runtime
read-only. Host/container JSON agrees byte-for-byte per SDK; full static
publication hashes agree before/after measurement. Both core budgets still fail
and the reports are retained with exit status 1. No budget was relaxed.
The CLI/compiler packages have focused ownership/cancellation/malformed-input
checks and the full two-SDK native race/vet regression matrix.
[Reports and provenance](benchmarks/existing-runtime-analysis-qualification.json)
record the artifacts, source snapshot and limitations. These checks qualify CLI
measurement, not a new runtime or browser build, performance improvements or G05.

## Local qualification (November 6, 2026)

Seven source test groups pass with native race instrumentation on both Linux
AMD64 Go 1.24.13 and 1.27.3. They cover malformed/bounded/canceled input,
inclusive and disabled limits, immutable lazy registrations against actual
compiled eager/two-lazy artifacts, report retention on budget failure,
diagnostic separation, atomic report replacement and generation cleanup.
The current Go 1.27.3 source passes full native race tests in 80 packages and
full vet; Go 1.24.13 passes focused vet for the two changed packages. A
three-second Go 1.27.3 section-envelope fuzz run passes 49,835 executions.

Actual CLI demo reports each contain eight runtime files, 24 registrations and
91 route summaries. The Go 1.27.3 command retains a complete JSON file before
its expected nonzero exit for both reference core limits. Its `-out` invocation
and the Go 1.24.13 measurement leave stdout empty.

| Measurement build | Core raw bytes | Core Go gzip-9 bytes | Code-section payload | Data-section payload |
| --- | ---: | ---: | ---: | ---: |
| [Go 1.24.13/Linux AMD64](benchmarks/bundle-analysis-go1.24.13-linux-amd64.json) | 7,650,369 | 1,912,367 | 5,004,773 | 2,632,202 |
| [Go 1.27.3/Linux AMD64](benchmarks/bundle-analysis-go1.27.3-linux-amd64.json) | 8,670,847 | 2,231,618 | 5,716,151 | 2,940,478 |

These are different SDK/artifact measurements, not a controlled optimization or
speed comparison. Both cores exceed the illustrative 6-MiB raw / 1.6-MiB
compressed reference budgets. No budget has been increased or accepted here.

A fresh ordinary demo built with the current compiler passes the 54-case
HTML/HEAD/navigation executable contract on the host and again in a source-free,
non-root, read-only, network-free Linux container. Each includes buffered and
streaming local Lambda. That separately built deployment executable has its own
pin; it is not the executable described by either measurement report. The user
selected local-machine qualification for now. Actual hosted-serverless and
complete cross-engine qualification remain open.

[Qualification provenance, source hashes, logs and deployment pins](benchmarks/bundle-analysis-qualification.json)
record this working-tree checkpoint. CI definitions are present, but their new
remote results and an immutable release candidate remain unverified. All 79
capability IDs and 11 release gates remain in `PARITY.md`.
