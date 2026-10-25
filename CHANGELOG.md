# Changelog

This repository is an experimental source distribution. No versioned release
or support window is established here. Historical entries identify committed
checkpoints, not published packages or a production certification. That history
ends at `467ba2f`; the separate unreleased section records current source work
and its scoped verification. See
[compatibility and upgrade policy](docs/COMPATIBILITY.md),
[security reporting](SECURITY.md) and the full
[capability inventory/release gates](docs/PARITY.md).

## Unreleased — native phases, public metadata and bounded content

These changes have scoped frozen-source verification below. They are not a
published release, complete capability parity or production certification.

- Added typed native `Config.Phase` values for development, production,
  prerender capture and static export, selected before the unchanged
  `Configure(*nextgo.Config)` hook. Export configuration, enumeration and serving
  now use the same explicit phase. Export has a separate opaque cache identity
  and does not import production prerender snapshots or consume production
  shared-seed tickets. Phase is native startup data, not a request capability.
  See [configuration phases](docs/CONFIG_PHASES.md).
- Added managed public `MetadataFiles`: six exact application-root filenames
  (`robots.txt`, `sitemap.xml`, `favicon.ico`, `icon.png`, `apple-icon.png`,
  `manifest.webmanifest`) and three optional typed native startup factories
  (`Robots`, `Sitemap`, `Manifest`). Validated bytes are frozen before serving;
  discovery/copy reject conflicting declarations, invalid files and source drift.
  These resources are explicitly public, mounted at the global base path and
  independent of locale/CDN routing. See [metadata files](metadatafiles/README.md)
  and the [standalone example](examples/metadata-site).
- Added `seo.Metadata.Manifest *string`: nil inherits; an explicit empty string
  disables inheritance. Explicit page manifest/icons override managed head
  defaults. Static export verifies metadata identities/digests and publishes
  exact files before replacing output. Native `ExportConfig` uses version 2
  only when metadata is nonempty; the new CLI accepts 1/2, and an older CLI
  rejects 2 instead of silently omitting resources. Static route-artifact
  formats `nextgo-static-v1`/`v2` remain unchanged with an added metadata inventory.
- Added portable bounded `content.Parse` with a named Markdown subset, explicit
  byte/line/node/depth limits, owned input, early link validation and escaped
  UI nodes. Raw HTML remains text; content cannot execute components or access
  native resources. The embedded-content example composes ordinary Go components
  outside parsed content and supports native/static serving. This is not
  CommonMark/MDX compatibility. See [content contract](docs/CONTENT.md).
- Isolated UI MIME parsing on `js && wasm` behind a private licensed parser port
  from Go 1.27.3; native UI still calls the installed SDK parser. Both SDK/native
  and matching-WASM corpora pass; a short differential fuzz run passes 416,679
  executions. Content's short two-worker fuzz session passes 35,269 executions.
  These are bounded observations, not sustained release fuzz qualification.
- Corrected the final examples' stylesheet publication with explicit
  `examples/content/public/style.css` and `examples/metadata-site/public/style.css`
  files. The earlier snapshot's known stylesheet 404 is retained as failed
  provenance; final checks require real stylesheet bytes and computed styles.
  The metadata sample explicitly enables `PreserveLayouts`; its retained counter
  expectation was corrected from 3 to 2, with an actual negative control.

The final Go 1.27.3 source passes full native race tests across **56 packages**,
vet, matching core/module WASM tests and vet, with a stable **821-file** input
manifest. Go 1.24 passes the same native/WASM checks before the final two
stylesheet-only additions; no Go source changed after that minimum-SDK run.
The current Go-source host matrix passes **373** browser checks. A freshly built
final-asset artifact separately passes **20** essential checks. Metadata passes
**15** host and **20** Linux native/static browser checks; content has **16**
harness checks plus **20** Chromium assertions covering actual styles, WASM,
native and static output. A fresh Linux build/export repeat passes **20** styled
metadata/content browser checks with no skips, failures or flaky outcomes,
including real CSS/WASM responses and no HTTP errors. All **819** Go/module
hashes remain unchanged from the qualified SDK/WASM matrix. These counts are
scoped evidence, not a combined full-release total.

Security scan `9e1f4938-9cb5-43c6-b086-816de9738d2a` completes with **zero
reported findings** for immutable snapshot `272acabad…`, covering **88 changed
source files** (52 inventory entries plus 36 additional files). The scan recorded
no warnings and no fixes were made during review. This is a bounded diff review,
not an independent immutable-release security audit. The current core measures
**8,225,235 bytes raw / 2,117,285 bytes with Node gzip level 9**; both eager size
budgets still fail. See [exact final artifact measurement](docs/benchmarks/metadata-phase-go1.27.3-macos-arm64.json).
The separate [controlled MIME experiment](docs/benchmarks/mime-parser-go1.27.3-macos-arm64.json)
reduces 100,405 raw / 26,714 gzip-9 bytes on its paired inputs; that comparison
does not attribute all differences between whole-application artifacts to MIME.

Core/module ABI **4**, navigation **3** and action/action-view **1** are unchanged
by this batch. Existing browser-through-local-Lambda evidence below uses the
preceding `467ba2f` artifact; new metadata-specific Lambda and actual AWS
qualification remain open. All **79 capability rows and 11 open release gates**
remain tracked, and no release/tag/support commitment is introduced.

## Source checkpoints, newest first

### `467ba2f` — scoped resources, safe media and staged build tooling

- Added scoped byte-oriented `ResourceStore`/`AsyncResource` APIs with explicit
  source ownership, load/retry, bounded public reuse and in-flight sharing,
  cancellation and SSR seed adoption. Public reuse remains an explicit
  application promise; private/session data needs private policy. Disposal and
  cancellation retain physical loader admission until settlement. See
  [client resources](docs/CLIENT_RESOURCES.md).
- Mandatory resource invalidation epochs across eager/lazy runtimes advance the
  browser module ABI from 3 to **4**. Rebuild and republish core plus all modules
  together; mixed ABI bundles fail negotiation. A final correction prevents late
  hydration of old SSR DOM from adopting a pre-mutation seed. A browser
  counterfactual fails on the preceding behavior and passes with the correction.
  Navigation protocol 3 and action/action-view protocol 1 remain unchanged.
  The historical cookie/dispatch checkpoint below used ABI 3.
- Added the server-only `socialimage` package for bounded typed bitmap layouts,
  text and supplied images rendered to PNG, with HTTP GET/HEAD, ETags and bounded
  process-local encoded caching. The public demo validates its query fields;
  applications still authorize private content. This is a limited Go drawing
  API with no HTML/CSS/Satori compatibility, remote fetch or automatic social
  filename discovery. See [social images](docs/SOCIAL_IMAGES.md).
- Added explicit iframe destination policies, restrictive sandbox/permission
  controls and escaped native audio/video/source/track helpers, with a local
  script-free demo. Browser CSP must constrain later iframe navigation; media
  codecs, accessibility and deployment policy retain their documented limits.
  See [frames and media](docs/EMBEDS.md).
- Added optional version-1 asset-tool manifests and typed native Go hooks,
  bounded input/output staging, output collision checks and successful-generation
  publication through dev/build/export. The Go design-token example needs no
  Node; Sass/PostCSS/Tailwind are explicit externally installed adapters.
  Tools run as trusted native code with inherited authority, not in an OS
  sandbox. See [asset pipeline](docs/ASSET_PIPELINE.md).
- Added development-only `nextgo inspect` and a bounded read-only version-1
  graph/status endpoint, with loopback peer/Host/origin controls and last-good
  build semantics. It publishes selected public graph metadata, without source,
  environment values or component props; it is not an MCP server. See
  [development inspection](docs/DEV_INSPECTION.md).
- Added explicit `Context.RequireServer()` response staging and static-export
  refusal for declared runtime HTTP dependencies. This prevents publishing pages
  whose application API behavior is unavailable on a plain static host; it is not
  automatic dependency analysis. See [static dependencies](docs/STATIC_DEPENDENCIES.md).

That checkpoint's frozen Go source passes full native race/vet on Go 1.24 and 1.27,
matching core/module WASM tests and vet. Host browser checks pass **373** cases
(335 primary, 15 resource and 23 separate-export checks); Linux passes **44**
new feature checks across Chromium, Firefox, WebKit and touch (20 resource,
12 media and 12 social), alongside **228** existing HTTP/mock-Lambda executable
regressions. Separate CSS and inspection checks pass **22** and **21** cases.
New-feature Node assertions also pass **8** local mock-Lambda event cases
(4 host, 4 Linux), covering buffered/streaming responses at root and mounted
CDN/trailing-slash configurations: useful SSR, navigation protocol 3, resource
JSON, PNG/ETag, embed CSP, three lazy WASM modules and matching support/media
asset bytes. Separate browser-through-local-Lambda checks pass **18** host and
**24** Linux cases on this `467ba2f` artifact; those results do not qualify newer
metadata behavior or real AWS. Remote CI's five jobs succeed for this source,
including **1,528** browser checks with **3** skips, **280** streaming checks,
**285** container checks, both SDK jobs and four Node cases per SDK. Actual AWS,
ingress/CDN, physical-device, accessibility, load/soak and complete recovery
qualification remain open. These earlier artifact/CI results are distinct from
the newer source checks above.

That Go 1.27.3 host core measured **8,320,548 bytes raw / 2,142,557 bytes with
Node gzip level 9**; both eager size budgets still fail. See the
[measurement artifact](docs/benchmarks/seven-feature-go1.27.3-macos-arm64.json)
and [performance limits](docs/PERFORMANCE.md). The security scan reported no
findings for its original pre-fix source snapshot; it is not an audit of the
final source or a clean immutable-release security review. All 11 release gates
remain open.

Support policy included at that checkpoint:

- Added a compatibility/support/deprecation policy, independent protocol/ABI
  identity inventory, concrete upgrade/rollback procedure and security-reporting
  guidance. Public mixed-generation conformance fixtures accompany the policy.
- No release was tagged. No support period, private response SLA or release
  gate closure follows from adding these documents.

### `be8c96a` — browser validator size and production SDK pin

- Replaced browser-imported regular-expression validation with bounded ASCII
  checks for the affected public protocol fields, reducing the paired reference
  WASM size. Current bundles still exceed both eager size budgets; this is not
  a general performance or startup improvement claim.
- Pinned the production Docker Go SDK to 1.27.3 by digest. The module retains
  Go 1.24 as a minimum language/compatibility target; production SDK policy and
  required matching WASM/runtime support are in [deployment](docs/DEPLOYMENT.md).

### `a57f53e`, `d41ed6c` — native cookies, shared mutation dispatch and build inputs

- Added native successful-result cookies and bounded document-wide typed action
  dispatch across eager/lazy runtimes, including physical-settlement ownership.
  The browser module ABI at that checkpoint was 3. Rebuild and republish the
  entire generation; separately retained older modules are not a compatible
  plugin system.
- Stabilized timestamped generated build-overlay paths and recorded controlled
  same-input SDK checks. This is scoped reproducibility evidence, not a promise
  that clocked prerender generations or arbitrary builds are byte-identical.
- Added focused browser/executable qualification, with caveats retained in
  [cookies](docs/NATIVE_COOKIES.md), [dispatch](docs/MUTATION_DISPATCH.md) and
  [build evidence](docs/PARITY.md).

### `0209ee8` — confirmed mutation and refreshed view

- Added explicit typed `CallAndRefresh`/multipart calls and generated wrappers.
  Action-view protocol 1 carries confirmed data followed by a guarded fresh
  view, with build/configuration/draft preflight and bounded streaming.
- Migration rule: a confirmed result remains confirmed when view refresh fails.
  Reconcile the view separately; never replay the write to repair UI. Ordinary
  typed calls retain explicit refresh behavior. See [action views](docs/ACTION_VIEWS.md).

### `644606f`, `624a19c` — generated typed actions

- Added explicit versioned action-package discovery, shared typed references,
  native registration and Go/WASM adapters, with compiler boundary fixtures and
  qualification. Corrected browser test synchronization/refresh coverage.
- Generated actions share the existing action transport and application-owned
  revision scheme; they do not lift inline server closures or infer auth,
  database transactions or idempotency. See [convention](docs/GENERATED_ACTIONS.md).

### `f4ecda9` — bounded viewport links and ordered startup

- Added bounded viewport prefetch selection and parser-ordered WASM startup.
  Public shell prefetch remains explicit and version-checked; speculation does
  not grant authorization or trigger a full-document mismatch reload.

### `4e78454`, `95b52ee` — partial prerender and shell reuse

- Added explicit build-time public shells with request-time Go regions, shell
  prefetch and exact-generation resume. Partial regions use private prerender
  manifest version 3. Regenerate private artifacts with their server build.
- Automatic partial-shell analysis and complete Cache Components semantics
  remain unfinished. See [partial prerendering](docs/PARTIAL_PRERENDERING.md).

### `f6a13ee`, `8cfb91d` — streaming ownership and admission

- Waited for complete streamed HTML boundaries before adopting islands and
  bounded application-wide renders/deferred callbacks with shared deadlines.
  Context cancellation remains cooperative; unreturned Go callbacks retain
  their physical slots. See [streaming](docs/STREAMING.md).

### `9f94c9a`, `ac654be` — shared prerender lifecycle

- Added shared seed admission/lifecycle qualification, bounded seed control
  memory and owned retained cache strings; repaired clean-checkout CI inputs.
- The current shared-cache wire is protocol 3, with namespace-wide acknowledged
  invalidation and optional bounded local file journal. Unsupported older wire
  versions fail closed; no released mixed-version translation exists.
  See [shared cache](docs/SHARED_CACHE.md) for persistence and outage limits.

### `b206722` — initial framework checkpoint

- Introduced the Go-native routing/rendering framework, Go/WASM components,
  examples, build/dev/export tooling and feature-conformance fixtures represented
  by that commit. Later checkpoints above refine specific subsets. This broad
  initial import is not a released feature-parity baseline.

## Known gaps affecting upgrades and production use

All 79 capability rows remain tracked and all 11 release gates remain open as
described in [PARITY.md](docs/PARITY.md). In particular, current source evidence does not
establish production maturity, actual AWS/IAM/CDN qualification, replicated/HA
cache operation, complete fault/recovery or manual accessibility coverage,
representative production performance, an independent immutable-release security
review, released-version mixed-generation upgrades, or an operational private
security-response process. Most default caches and example writes are
process-local. Automatic route-level splitting, state-preserving HMR, complete
action/form/cookie coordination and the remaining parity capabilities are still
unfinished. Feature documents retain exact scope and historical test provenance.

For future entries, record the exact commit/tag, user-visible change, incompatible
API/wire/schema/toolchain behavior, migration and rollback constraints, tests and
known exclusions. Do not turn a passing local subset into a release claim.
