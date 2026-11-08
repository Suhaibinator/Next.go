# Compiled route lookup

The native page/API dispatcher and initialized Go/WASM navigation manifest now
compile an immutable route index. Applications keep the same pattern grammar,
route registration and parameter APIs. Static segments precede parameters and
catch-alls using the existing lexicographic specificity rules; exact static
paths have a direct lookup. Escaped request segments are decoded once, and only
the selected route receives a parameter map. Encoded slashes/backslashes, dot
segments, controls, empty segments, malformed escapes and invalid UTF-8 retain
the existing rejection rules.

The trie uses an explicit traversal stack, so untrusted URL depth does not grow
the Go call stack. Failed literal branches can fall back to a matching parameter
or catch-all branch. Its maps and parsed patterns are private and read-only
following construction; request parameter maps remain independently owned.
Root-layout/endpoint classification uses a frozen browser lookup snapshot.
Compilation changes neither manifest JSON nor its deployment configuration ID.
Configured handlers, document boundaries, rewrites, middleware, authorization,
layout selection and public transport contracts retain their existing order.

Route-set validation also uses canonical-shape, catch-all-peer and optional-parent
indexes instead of reparsing every pair. It retains the earliest conflicting
registration and the existing error text. Filtered prerender path views rebuild
their matching index rather than retaining indices into another route slice.

Focused qualification includes all ordered triples from a mixed 21-pattern
conflict corpus, comparisons against the original linear matcher and validator,
percent/Unicode/invalid-path cases, backtracking, 10,000-segment paths, concurrent
lookup ownership, a 3,000-route real App dispatcher, native race checks, and the
same routing/manifest tests compiled to Go/WASM. Differential fuzzers retain the
linear implementations as independent references.

The [qualification record](benchmarks/compiled-routing-qualification.json) pins
the source, both SDK artifacts and raw results. Go 1.24.13 and 1.27.3 each passed
80 native race-tested packages, native vet, 11 WASM package runs across all four
core/module/refresh targets and their vet checks, and generated-helper checks
for 14 applications. Each SDK passed 65 Chromium cases over HTTP, both Lambda
adapters and static hosting, plus 156 generated-executable cases on the host and
again in a non-root/read-only/network-isolated Linux container. The two
differential fuzzers executed 69,194 cases on Go 1.27.3 and 188,409 on Go 1.24.13.

Reproduce the paired microbenchmarks without other build/load work:

```sh
GOMAXPROCS=1 go test ./internal/routing -run '^$' -bench '^BenchmarkRoute' -benchtime=100ms -count=3
```

The fixture uses 10, 100, 1,000 and 3,000 patterns with distinct static record
prefixes and one dynamic parameter. The lookup target is the last registered
prefix, deliberately stressing the old linear scan. Results measure lookup,
validation and index construction separately. Reported allocated bytes are
cumulative allocations per operation, not resident memory. They do not measure
HTTP rendering, whole application startup, mixed/concurrent traffic, devices,
cloud cold starts, or another framework. CPU sharing and compiler optimizations
can affect timings and allocation counts. The original standalone baseline and
paired same-package references remain separate evidence.

CI runs both SDK selectors, native race/vet, Go/WASM variants, two differential
fuzzers, and raw paired measurements. Its generated deployment/browser fixtures
also exercise the integrated dispatcher. No timing threshold is inferred from
one local microbenchmark, and no production performance gate is waived.

Paired three-run medians for the 3,000-route fixture on the recorded shared
AMD EPYC Linux host were:

| SDK | Validation, linear → indexed | Last-prefix lookup, linear → compiled | Index construction |
| --- | --- | --- | --- |
| Go 1.27.3 | 2.80 s → 1.23 ms | 1.86 ms → 0.414 µs | 2.75 ms |
| Go 1.24.13 | 3.12 s → 1.27 ms | 2.27 ms → 0.447 µs | 2.83 ms |

The reference demo still exceeds both existing WASM size budgets on both SDKs.
Current source auditing, broader browser engines, actual
cloud/CDN/TLS, representative load and equivalent Next.js comparisons remain
open in [PARITY.md](PARITY.md).
