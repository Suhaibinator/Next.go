# Changelog

This repository is an experimental source distribution. No versioned release
or support window is established here. Historical entries identify committed
checkpoints, not published packages or a production certification. That history
ends at `be8c96a`; the separate unreleased section records current source work
and its scoped verification. See
[compatibility and upgrade policy](docs/COMPATIBILITY.md),
[security reporting](SECURITY.md) and the full
[capability inventory/release gates](docs/PARITY.md).

## Unreleased — verified experimental subsets

These changes have scoped frozen-source verification below. They are not a
published release, complete capability parity or production certification.

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

The final frozen Go source passes full native race/vet on Go 1.24 and 1.27,
matching core/module WASM tests and vet. Host browser checks pass **373** cases
(335 primary, 15 resource and 23 separate-export checks); Linux passes **44**
new feature checks across Chromium, Firefox, WebKit and touch (20 resource,
12 media and 12 social), alongside **228** existing HTTP/mock-Lambda executable
regressions. Separate CSS and inspection checks pass **22** and **21** cases.
New-feature Node assertions also pass **8** local mock-Lambda event cases
(4 host, 4 Linux), covering buffered/streaming responses at root and mounted
CDN/trailing-slash configurations: useful SSR, navigation protocol 3, resource
JSON, PNG/ETag, embed CSP, three lazy WASM modules and matching support/media
asset bytes. These direct Runtime API assertions do not exercise browser
hydration through Lambda. Browser-through-Lambda, actual AWS, ingress/CDN,
physical-device, accessibility, load/soak and complete recovery qualification
remain open. Exact feature scope and exclusions remain in the linked contracts.

The measured Go 1.27.3 host core is **8,320,548 bytes raw / 2,142,557 bytes with
Node gzip level 9**; both eager size budgets still fail. See the
[measurement artifact](docs/benchmarks/seven-feature-go1.27.3-macos-arm64.json)
and [performance limits](docs/PERFORMANCE.md). The security scan reported no
findings for its original pre-fix source snapshot; it is not an audit of the
final source or a clean immutable-release security review. All 11 release gates
remain open.

## Unreleased policy work

- Added a compatibility/support/deprecation policy, independent protocol/ABI
  identity inventory, concrete upgrade/rollback procedure and security-reporting
  guidance. Public mixed-generation conformance fixtures accompany the policy.
- No release was tagged. No support period, private response SLA or release
  gate closure follows from adding these documents.

## Source checkpoints, newest first

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
