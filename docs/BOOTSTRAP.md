# Initial HTML and Go runtime startup

Buffered documents and the first progressive HTML flush place the completed
`#nextgo-app` shell and hidden `#nextgo-initial-ready` marker before the runtime
scripts. The compiler-matched `wasm_exec.js` runs first, followed by external
`boot.js`, as parser-ordered classic scripts. Neither runtime script relies on
head `async` execution. Early dependency declarations remain in the head;
configured CSP nonces, runtime build URLs and CDN prefixes are preserved.

The shell is useful before WASM starts. Once the parser reaches the bootstrap,
its asynchronous WASM work allows parsing of subsequent stream content to
continue. A defensive one-shot MutationObserver waits for the initial-ready
marker if needed, then disconnects; startup does not poll animation frames.
This avoids depending on frame delivery in a paused/hidden document. Island
hydration still attaches to existing server nodes.

This is separate from the [deferred chunk completion marker](STREAMING.md#initial-document-completion).
The initial-ready marker means the app shell is complete; each later inert
template still needs its matching outside completion marker before Go adopts
and hydrates its content. Neither marker promises that all deferred regions
have arrived or that application data remains authorized indefinitely.

## Reproduction and test boundaries

Native WebKit reproduced head-async bootstrap failing to execute while a real
HTTP stream remained open in three runs without Playwright route interception.
The old deterministic post-marker regression also failed. The corrected
partial-region scenario splits the document before initial-ready, suppresses
animation-frame callbacks, then proves original SSR identity, hydration and
counter interaction after the marker/scripts arrive.

The stream fixture forwards the first flush through the bootstrap's closing
script tag, rather than stopping at the marker and withholding the runtime.
Its delayed WASM path uses a real paused upstream response with bounded
eight-entry holding state. Shell-prefetch checks observe actual wire data and
cache hint/reference use, including real byte-level cancellation and lifetime
behavior. This is browser/server behavior evidence, not a test that passes
because HTML or WASM was replaced with synthetic success responses.

## Qualified source

The final frozen 637-input manifest is retained under
`.nextgo-test-results/qualification/bootstrap-ready/final/`, SHA-256
`c8e17ce95c8eab897ddfebec5fc2bec063c771ec83f4303b88365040f2e3613f`.
The tested native server SHA-256 is
`ec883424a0b0b248b2e7d89aa66a66dcbad371b7a40e99183a3fbd4e82947513`.
Both Go 1.27.3/macOS and Go 1.24.13/Linux full native race/vet, matching
core/module WASM tests/vet, generation checks and guarded production builds
pass. Complete host and nonroot read-only Linux HTTP/buffered-and-streaming
mock Lambda executable suites also pass.

The final browser subset passes **232 checks**, counted once:

- Host: 120 selected core/navigation/location/skew/script-CSP/restore/actual
  BFCache regressions, eight partial-region, ten viewport and ten public-shell
  checks: 148 total.
- Linux Firefox/WebKit/touch: 24 partial-region, 30 viewport and 30 public-shell
  checks: 84 total.

Five fresh WebKit diagnostic runs also pass but are excluded from 232. The
preceding 129-check viewport artifact and initial partial/harness failures stay
separate historical evidence. These selected checks are not the complete
capability or release-browser inventory.

Remote checkpoint `4e78454` remains distinct: its streaming/shell job observed
39 passes and one response-observer hang reporting `ERR_ABORTED`; the abort's
caller is still unresolved. Native SDK/container jobs and the broad browser job
passed; the latter records 1,484 passes and three skips on that historical
checkpoint. Those results are separate from the current 232 checks. The
streaming job failed, so this startup correction does not establish a remote
abort fix or an all-green remote result. All 79 capability rows and 11
release gates remain tracked in [the inventory](PARITY.md).
