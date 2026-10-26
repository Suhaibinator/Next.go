# Changelog

This repository is an experimental source distribution. No versioned release
or support window is established here. Historical entries identify committed
checkpoints, not published packages or a production certification. The separate
unreleased section records current source work and its scoped verification. See
[compatibility and upgrade policy](docs/COMPATIBILITY.md),
[security reporting](SECURITY.md) and the full
[capability inventory/release gates](docs/PARITY.md).

## Unreleased — explicit client-only component boundaries

- Add `ui.ClientIsland` with a useful server fallback and one synchronous
  `Scope.ClientView` call during Mount. The outer island remains stable; the
  returned child scope owns the bounded browser-created view and its behavior.
  Ordinary islands retain their existing SSR hydration contract. Mount failure
  disposes owned work and restores the original fallback; arbitrary application
  side effects are not rolled back. [Contract](docs/CLIENT_ONLY.md).
- Both SDKs pass 13 scoped native/WASM/vet/format commands with all 11 distinct
  new source test groups passing at their required targets. Production checks
  pass five steps on Go 1.24 and six on Go 1.27, including a Darwin cross-build.
  Native HTTP verifies eight runtime assets, including all five lazy modules.
- Actual browser fixtures pass 20 cases per SDK across four profiles: 40 new
  cases, without failures, skips, retries or flaky results, with both outer
  controllers and cleanup checks passing. They cover eager/lazy boundaries,
  retained ordinary-island identity, independent state, error/panic rollback,
  focus, disposal/navigation return and superseded downloads. Goexit has focused
  WASM unit coverage, not a browser scenario; the unit suite's predeclared
  existing development-module skip is documented separately.
- Correct V17's description of pre-existing exported navigation data: declared
  static routes already publish HTML and JSON envelopes. Full client-only page
  routing, unexported-route/catch-all fallback, new-boundary static-host,
  retained-layout/streamed-insertion/development-refresh integration and physical
  device qualification remain open. No ABI migration, prior-campaign rerun,
  performance, cloud or release-gate closure is claimed.

## Unreleased — explicit inherited metadata clearing

- Add `seo.Metadata.Clear` / `seo.ScalarClears` for inherited description,
  canonical and robots strings. Empty values with clear flags suppress
  inheritance through defensive snapshots; nonempty child values win and reset
  their flags. Existing explicit-page/legacy precedence and enforced access-
  denial `noindex` remain unchanged. Titles, social metadata, document settings,
  resources and JSON-LD keep their existing rules.
- Add `/metadata-clear` and its prebuilt-only browser fixture. Late clearing
  preserves the Go controls, focus, description element and frozen head
  companions. [Contract and test command](docs/METADATA.md#explicit-scalar-clearing).
- Fresh reconstructed-source qualification passes seven focused native/WASM/
  vet/format commands per SDK, five Go 1.24 production checks and six Go 1.27
  checks including a Darwin cross-build. Browser fixtures pass eight cases per
  SDK across four profiles: 16 new cases, without skips, retries or flaky
  results, with both outer controllers passing. Three initial fixture
  expectation failures remain recorded separately; their correction did not
  change production code. This does not rerun the prior 344-case campaign or
  add LOCAL/Lambda/AWS proof. All 79 capability rows and 11 open release gates
  remain; no capability or gate closes.

## Unreleased — bounded deferred typed metadata

- Add `Page.DeferredMetadata` using the existing typed `MetadataFunc`, an owned
  fallback and request Memo. Read-only late work shares render/callback admission
  and preserves document settings, resources and JSON-LD; it cannot change
  response policy or register `After` work.
- Keep initial HTML blocking by default. Explicit production browser opt-in can
  deliver fallback then a Go-only head update; negotiated production navigation
  works independently of that flag. Development buffers, build captures resolve,
  and partial public shells reject the callback. See [contract and
  sample](docs/METADATA.md).
- Both SDKs pass 13 focused native/WASM/vet/format commands and five production
  checks each. Actual browser application checks pass 44 per SDK across four
  profiles; the Go 1.27 outer post-removal observation failure remains recorded
  separately, with independently verified cleanup and no rerun. LOCAL checks
  pass 15 cases plus two syntax checks per SDK across native HTTP and local
  buffered/streaming mock Lambda; they do not contact AWS. Per-case cleanup is
  fixture-asserted, with independent final Node/namespace cleanup only.
- Unchanged affected core/location/lazy/navigation-stream suites pass 128 cases
  per SDK: 256 existing cases, for 344 browser application cases including the
  88 new metadata cases. No skips, retries or flaky results occur in these
  completed suites. Native/browser exits are observed; the legacy proxy identity
  is later kernel-absent, with its exit code and request/socket joins unobserved.
  These selected suites are not the complete framework matrix. Full crawler/
  proxy/CDN, all framework combinations and production qualification remain
  open; no capability or release gate closes.

## Unreleased — lazy-host registration boundary

- Move the module-host installer reference from `Run` to successful
  `RegisterLazy` registrations. Installation stays at the same startup point;
  public APIs, ABI 5 and invalid/late-registration behavior remain unchanged.
- Paired Linux/arm64 measurements reduce core bytes in a controlled no-lazy
  diagnostic. The full four-module demo retains its host and grows slightly;
  both size budgets and G05 still fail. See the [measurement and
  controls](docs/PERFORMANCE.md#lazy-host-registration-observation-october-26-2026-utc).
- Focused WASM/vet and production controls pass on both SDKs. Candidate browser
  checks pass 40 cases per SDK across four profiles. This adds no startup,
  CPU/RSS, runtime-retirement or production-maturity claim.

## Unreleased — scoped link feedback and explicit asset dependencies

- Add `Scope.ObserveLinkStatus` for one owned anchor, including immediate
  synchronization, commit/supersession cleanup and a shared 512-observer bound.
  Eager and lazy modules use paired ABI 5; resource invalidation epochs remain
  mandatory. Retained-layout examples keep duplicate-destination links distinct.
- Add asset-manifest v2 dependency aliases and private intermediate outputs.
  Sequential topological execution uses verified per-consumer copies with a
  shared bounded copy ledger. The Go token/CSS example now has two actual stages
  and publishes only `style.css`; version 1 retains independent-tool behavior.
- Both SDKs pass 15 targeted native/WASM/vet commands and separate production
  artifact checks. The final per-link browser fixtures pass 60 cases per SDK
  across four engines; initial fixture failures remain recorded in
  [link feedback](docs/LINK_STATUS.md). Selected existing core/location/lazy/stream
  regressions pass 128 cases per SDK, and static hosts pass 12 per SDK: 400
  browser executions on the pre-readiness-change producers. Four Node transport
  cases per SDK check native/local buffered/local streaming/static delivery;
  they do not instantiate WASM or contact AWS. Thirty-three CI-helper checks
  pass separately; remote workflow execution is not implied.
- Align lazy-host readiness with committed `CurrentLocation`, permitting idle
  link observers in valid saved/fallback native documents without enabling SPA
  routing. All four WASM personalities and vets pass on both SDKs; this later
  change has no fresh full browser, HMR or offline campaign. Development-watch
  and complete deployment/release qualification remain open. These bounded
  additions do not complete the capability inventory or close release gates.

## Unreleased — experimental managed offline public editions

- Add a production compiler path for native `app/offline.go` policies, a separate
  Go/WASM worker, immutable eager/lazy/public dependencies and the matching SDK
  shim. Generated native and local Lambda entrypoints prepare the complete
  anonymous public bundle before accepting traffic.
- Add explicit eager Go enable/status/update/disable APIs. Saved pages hydrate
  their existing DOM and use native document navigation. Queries, APIs, private
  state and mutations remain network-only; nothing queues or replays writes.
- Bind staged storage to complete decoded hashes, freshness and reservations for
  active, previous and candidate generations. Updates wait for closed clients;
  disable can retain charged public data until a later explicit reconciliation.
- Preserve request fields and physical body cancellation through network-first
  document delivery. Document clones avoid Chromium preload reuse before the
  fresh-network cache override. Sample buttons prevent restored enabled state
  from bypassing their server-rendered hydration gate.
- Add the [offline sample](examples/offline/README.md), actual two-generation
  browser fixtures and native/local Lambda transport checks. The earlier
  `dc425dd` checkpoint qualified native preparation only. Current scoped proof
  and retained failed-run history are recorded in [OFFLINE.md](docs/OFFLINE.md).
- Keep this production-only subset experimental. Development, static export,
  prerender/locales/zones and other documented integrations are refused. Complete
  PWA support, timer/runtime retirement and all 11 production release gates stay
  open; this is not full Next.js compatibility or production certification.

## Unreleased — JSON validation dependency boundary

- Move the duplicate-key/lexical validator into `internal/wirejson/scan`, whose
  runtime dependency graph excludes the standard JSON decoder. Keep
  `wirejson.Unique` as a direct forwarding function and retain `Strict` without
  changing its behavior. The scanner and decoder-oracle tests move unchanged
  apart from the package name.
- Native race, matching WASM, vet and dependency checks pass on Go 1.24.13 and
  1.27.3. A bounded 30-second differential fuzz run also passes. Existing callers
  still use the parent decoder package; this refactor claims no application
  bundle reduction or performance-budget completion.

## Unreleased — mutation conflicts and generated server cleanup

- Add native `actions.ConflictError` for an application-declared rejected write,
  using the existing 409 error envelope and escaped progressive HTML. Public
  messages and fields exclude wrapped private details; authorization errors
  retain precedence. Handler cancellation now asks callers to reconcile an
  unconfirmed outcome before retrying.
- Close successfully constructed applications on generated startup, inspection,
  export and Lambda errors. HTTP startup failures cancel and join their shutdown
  worker. All four generated HTTP server types separate the five-second request
  drain from application cleanup and force-close remaining connections after a
  failed drain.
- Integrated native race/vet passes on Go 1.24.13 and 1.27.3: 69 tested packages,
  3,069 passing test/subtest records and one expected helper skip per SDK.
  Four WASM modes and four vets pass per SDK. Actual deadline probes cover
  ordinary, aggregate and release entrypoints plus an instrumented ingress
  constructor with an unchanged HTTP lifecycle. Fresh host production builds,
  four local transport checks and 12 Chromium checks pass. New immutable CI and
  complete release qualification remain pending.

## Unreleased — independent zone release endpoints and ingress

Frozen V2 production and V3 development increments have scoped compiler,
browser and transport evidence. A retained V1/V2 mixed-SDK matrix also passes
without rebuilding the release endpoints. These results do not qualify later
source changes or establish production/Next.js multi-zone parity; see the
[exact checkpoint record](docs/PARITY.md).

- Added `NewZoneRelease` with a fixed trusted public zone/origin, explicit exact
  backend authorities, frozen release-scoped public/runtime assets and bounded
  compatibility metadata. Expectation headers identify deployment compatibility;
  they do not authenticate ingress or a tenant. Applications still authorize
  visitors and mutations.
- Added `NewZoneIngress` for an immutable complete active/retained release
  snapshot, exact host/longest literal mount selection and bounded physical
  HTTP exchanges. It preserves supported framework protocols while rejecting
  unsupported upgrades/identities and does not add response caching, failover,
  redirect following or mutation retry. Old asset paths never fall back to the
  active release.
- Added `zones release`, `zones ingress` and `zones ingress-build`. A selected
  endpoint publishes its binary and `zone-release.json` atomically to a new
  directory; the ingress compiler publishes a standalone binary plus its
  routing manifest. Selected-module peer-change/fingerprint independence is
  still being corrected/qualified and is not claimed proven here.
- Added a [two-module source example](examples/independent-zones/README.md) with
  public process-local state, lazy counters, actions, cookies and streamed UI.
  Its intended A1/B1 → A2/B1 → A1/B1 flow leaves the Blog release unchanged while
  retaining exact Alpha asset generations. It supplies no production identity,
  durable storage, data migration or idempotency guarantee.

The v1 endpoint excludes locales, image optimization, managed metadata,
prerender and static-export purposes. Trusted public-origin and TLS/offload
policy are independent of backend authority; forwarding headers cannot supply
that trust. See [the new contract](docs/INDEPENDENT_ZONES.md). All 79 capability
rows and 11 open release gates remain tracked. This entry does not claim final
compiler/browser/transport or cloud qualification.

## Source checkpoints, newest first

### `76211b8` — experimental opt-in lazy Go state refresh

Implemented a bounded development-only refresh contract with completed scoped
native, WASM, browser and retirement verification. Full Fast Refresh parity,
production hot swapping and release readiness are not claimed.

- Lazy component manifests may opt into version-1 explicit state/DOM schemas.
  A mount names its public instance with `Scope.RefreshInstance` and registers
  bounded plain JSON cells using `client.NewRefreshState`. It does not capture
  arbitrary application/private state or infer semantic schema compatibility.
- Accepted replacement retires the previous managed Go module physically,
  including successful Go main exit and paired development-bridge cleanup.
  Unsupported/busy lifecycle or native/shared/core/eager/props/schema changes
  require the conservative full-document fallback. Arbitrary Go/JavaScript
  work cannot be forcibly reclaimed or discovered by this contract. Components
  must use `Scope.Go` plus managed transports to own the whole worker through
  actual return (64 workers per scope). The matching development shim
  digest/private timer shape gates activation; unknown shims reload normally.
- The isolated [refresh example](examples/refresh/README.md) contains two public
  counters and native-instance diagnostics. Production/static artifacts retain
  ordinary lazy execution and omit development replacement behavior.
- CI scripts declare sample generation, production/static builds, bounded Node
  transport fixtures, sequential development and four-engine production/static
  checks. Their configuration is not proof they passed. Legacy jobs remain.

Current host development passes **33** cases on Chromium/native WebKit/touch.
Linux passes **88 unique cases**: 44 each with matching Go 1.24.13 and Go 1.27.3,
across all four engines, without failures/skips/retries in the completed runs.
The first stable-SDK attempt reached 38 before infrastructure interruption with
exit 255/lost tmpfs; it remains incomplete diagnostic evidence and is excluded
from 88. A fresh stable run passes all 44 in 9.7 minutes on the same frozen
source; no functional fix is inferred from infrastructure recovery.

Eight approved warm JSON archival checks (four per SDK) pass separately and
are **not added to 88**. Their metadata records 16 actual old-Go runtime exits
with exit 0 and an empty timer map before candidate execution. The initial
list-only reporter discarded successful raw attachments. The supplement supplies
raw metadata for those eight warm checks without changing assertions; it does
not reconstruct discarded attachments for the 88. Final proof is retained at
`.nextgo-test-results/qualification/refresh-wave/linux-dev/qualification-final.json`,
SHA-256 `ebd991c42b99297945309dc02090fd38aa8765e51a9391847eb4c69c7db9cdfb`.
All 1,007 runtime inputs remain unchanged. CI now retains bounded success JSON
artifacts; its new configuration is not remote proof before the new source runs.

Host and both Linux SDK full native race/vet (66 tested/173 no-test packages),
matching core/mutation/module WASM tests and vet pass. Linux production passes
**96 browser cases** (48 per SDK across HTTP, buffered/streaming local Lambda
and static transports); zone/CLI/adapter checks pass **26**, and legacy
executables pass **456**. Those are separate groups. Host production passes four
Node transport cases and 12 Chromium cases. The legacy full host suite records
**334 passes/one strict published-BFCache failure**, with no retry/skip/assertion
change; it is not an all-green suite or a causal HMR failure finding.

The [current size-only report](docs/benchmarks/refresh-phase-go1.27.3-macos-arm64.json)
records **8,401,692 raw / 2,157,893 Node gzip-9 core bytes**; both eager budgets
fail. It provides no startup/CPU/RSS or causal before/after claim. Production retains ordinary lazy execution:
development activation is gated, but shared library paths can remain linked,
so no zero-production-payload claim follows. See [the contract](docs/HOT_REFRESH.md)
for bounded ownership, limits and exact evidence. All 79 capability rows and
11 open release gates remain tracked; previous source/CI proofs remain distinct.

Remote CI for `76211b8` also records a failed strict published-BFCache browser
check. Its cause remains unresolved; the earlier `bb62d65` green checkpoint is
a separate source/result. Neither result qualifies the newer release/ingress
source above, and no application fix is inferred from this historical failure.

### `bb62d65` — native zones and trusted deployment context

This bounded in-process composition increment has passing native/WASM,
production, focused browser and final four-engine Linux development checks;
it is not independent application release/deployment parity.

- Added NewZones exact canonical-host/longest-literal-mount routing, trusted
  Config.Zone()/CurrentZone metadata, independent App ownership and explicit
  DocumentPaths document-navigation boundaries. Trusted tenant metadata is not
  authentication. Page/API/action authorization remains application work.
- Added cache/shared-namespace/tracer ownership checks, startup rollback,
  bounded group close and child-aware Lambda telemetry flushing. Native
  process globals and same-origin browser authority are not sandboxed.
- Added strict zones.json v1, zones build/dev and one standalone binary from
  four declarations/three application roots. One module/workspace/toolchain is
  required; alternate asset CDNs, domain/nested-zone locales, aggregate static
  export/prerender and independently released applications remain unsupported.
  [Zone contract](docs/ZONES.md), [synthetic example](examples/zones/README.md).

Both SDK full native race/vet (62 tested/166 no-test packages), matching
core/mutation/module WASM tests/vet, eight generation checks and all three host
production/Linux builds pass. Linux passes 48 zone browser, 228 legacy executable
and 13 zone/CLI/adapter checks. Host passes 36 zone plus 23 separate static cases,
228 legacy executable checks in 117.313 s and the final 13-check zone/CLI/adapter
wrapper in 1.372 s without skips. Its primary browser run initially passes 334/335 with
one masked Chromium BFCache NotRestoredReasons observation, then an unchanged
strict nine-case subset and one full 335/335 repeat pass. The positive host
record is 394, not all attempts green; the initial cause/stability remains open
without an application change. Final host development Chromium passes one case
in 52.7 s; two WebKit passes use an older harness. Final Linux development
passes four Chromium/Firefox/WebKit/touch cases in 4.1 minutes, with no skips,
flaky results or global errors. Each covers eight workflow phases and no-JS SSR;
cleanup confirms CLI exit 0, no build directories and all 30 observed listeners
closed. These are separate development results, not added to the host 394.
The initial host copy filter omitted the production Go scripts package; a
separate Linux overlay masked that source with a directory containing only the
test runner. Helper-filter and mount-only corrections resolve those distinct
setup failures without changing Node/Go application behavior.

The [ordinary-demo size observation](docs/benchmarks/zones-phase-go1.27.3-macos-arm64.json)
is **8,237,069 raw / 2,120,180 Node gzip-9 core bytes**; both size budgets fail.
Four Code-section payloads match the preceding artifact, while Data hashes
differ. No whole-file reproducibility, speed or causal-size claim follows.
The synthetic tenant issuer is openly available and requires fresh checks per
protected endpoint; it is not production account authentication. AWS execution
still awaits approval, with no AWS actions performed. All **79 capability rows
and 11 open release gates** remain tracked.

The separate `bb62d65` remote browser checkpoint records 1,672 passes with three
existing skips. It belongs to this older zones source, not the subsequent
refresh implementation or its qualification.

### `c2ff5ce` — explicit field validation, choice identity and transport factoring

This additive forms checkpoint has passing native/WASM, browser and executable
checks for its bounded contracts. No release or production certification is
introduced.

- Added `RadioChoice`/`BindRadioChoice` for an absent radio versus a selected
  empty value, and `BindMultipleOptions` for exact selected option positions
  when values repeat. `SelectedOptionValues` returns DOM-order native values
  with multiplicity; it cannot recover option positions from native POST
  strings. Legacy value-based bindings keep their existing restrictions.
- Added portable field-validation tickets and scoped `BindFieldValidation` with
  explicit input/blur/manual triggers, debounce, whole-draft provenance,
  separate error sources and submission gating. Binding starts no request.
  Eight document callback leases/128 controllers span eager/lazy runtimes;
  cancellation fences output while running callbacks retain physical capacity.
  Server-side validation, authorization and persistence remain application work.
  See [field validation](docs/FIELD_VALIDATION.md) and the dedicated
  [public preview example](examples/form-validation/README.md).
- Factored type-independent mutation transport/envelope handling while retaining
  typed/custom codec, error-order, partial-output and cancellation semantics.
  The isolated `502` comparison removes 70,517 raw/13,161 gzip-9 bytes, not a
  causal whole-batch or speed/allocation measurement. The exact current core is
  **8,237,069 raw / 2,120,177 Node gzip-9 bytes**; both size budgets still fail.
  [Controlled comparison](docs/benchmarks/mutation-factoring-go1.27.3-macos-arm64.json),
  [refrozen artifact](docs/benchmarks/form-validation-refreeze-go1.27.3-macos-arm64.json).
- Added an opt-in [AWS qualification scaffold](deployment/aws-qualification/README.md)
  with 18 passing offline tests and separate source review. A synthetic ZIP is
  prepared, but cloud approval is pending and no AWS calls have run. The
  scaffold is not cloud qualification, a billing cap or production auth.

Both current/minimum SDK full native race/vet (60 tested/141 no-test packages),
matching core/mutation/expanded-module WASM tests/vet and the current three
native production builds pass. Final Linux checks pass **48** four-engine form
cases, **6** form HTTP/mock-Lambda cases and **228** legacy executable checks.
Host passes **36** focused and **23** separate static cases, **228** legacy
executable checks in 118.784 s and **6** new form transport cases. All five
application generation checks pass. The primary host browser run passes
**335/335** in 4.8 minutes, completing **394 host browser passes** (335 primary,
36 forms, 23 separate static), without failures/skips. Earlier artifact runs are
not counted again; Linux form/executable groups remain separate. A final test-only
clock correction leaves the runtime sources refrozen and the compared six
Linux WASM Code sections identical. Earlier failed clock/redirect/untouched-error
expectations and fixture-cleanup attempts remain retained as provenance.

Module ABI 4, navigation 3 and action/action-view 1 are unchanged. All **79
capability rows and 11 open release gates** remain tracked.

The `c2ff5ce` remote workflow subsequently passes all five jobs: browser
1,620 passes/three existing skips, streaming 280 and container 285 plus
collector/smoke, alongside both SDK jobs. This is evidence for that source only,
not the newer zones source. The older `502cbd9` container exit-23 failure and
its unconfirmed cause remain preserved below.

### `20cb9d2` — CI smoke-pipe hardening

This workflow correction avoids early-closing smoke-test pipes. It does not
establish the historical exit-23 cause or remote success of the forms checkpoint. The preceding `502cbd9` workflow
has four passing jobs but a container exit-23 failure whose cause remains
unconfirmed: browser 1,572 passes/three existing skips, streaming 280 passes,
and both SDK jobs with four Node mock-Lambda cases each are separate successful
groups. New form CI steps accompany the forms checkpoint; no current whole-CI
pass is inferred from their configuration.

### `502cbd9` — native phases, public metadata and bounded content

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
