# Compatibility, support and upgrades

Next.go is an experimental source distribution. There is no stable release,
published support window, maintained release branch, or LTS commitment recorded
here. The local repository has no version tags at the `be8c96a` checkpoint.
Documentation and passing fixtures describe implemented subsets; they do not
close T19, G11, or any of the [79 capabilities and 11 release gates](PARITY.md).
This policy establishes how changes must be described and tested while those
release obligations remain open.

## What an application can depend on

The module path is `github.com/Suhaibinator/Next.go`, with minimum language target
`go 1.24`. Public Go packages, exported identifiers, documented file conventions,
CLI flags and documented HTTP behavior form the application-facing surface.
`internal/` packages, generated implementation details, DOM marker names and
the browser module bridge are implementation details, even when documented for
debugging. Treat changes to their externally observable behavior as migration
work; importing an `internal/` package is not an upgrade strategy.

Next.js, React, JSX, RSC wire formats, JavaScript configuration syntax and npm
package compatibility are not promised. Capability parity remains the target;
an existing Next.js application cannot run unchanged. Server and browser Go
imports also differ deliberately: native handlers and secrets must stay out of
the `js/wasm` import graph. See [generated actions](GENERATED_ACTIONS.md) and
[typed contracts](MUTATIONS.md).

Pin a full source commit in an application and commit `go.mod` and `go.sum`.
Resolve an explicitly selected revision with:

```sh
go get github.com/Suhaibinator/Next.go@<full-commit-sha>
go mod tidy
go list -m -json github.com/Suhaibinator/Next.go
```

Replace the placeholder with an inspected commit. Local `replace` directives
are useful during development but make the checkout part of the build input;
record its commit and dirty state too. Keep the CLI and imported framework at
the same revision. Do not combine an arbitrary installed `nextgo` with a different
module dependency and expect generated code or artifacts to be compatible.

## Version and deprecation rules

There is no released version series to backport fixes to today. The current
source revision is the development target; compatibility with an older commit
must be demonstrated, not inferred from its age. No security response or repair
deadline is promised. See [security reporting](../SECURITY.md).

When versioned releases are introduced, they must use `vMAJOR.MINOR.PATCH` tags
and publish a scoped support table, release notes, exact source/artifact identity
and qualification evidence. Before `v1.0.0`, incompatible public changes require
a minor-version increment and explicit migration notes; patches are reserved
for compatible corrections. From `v1.0.0`, incompatible public changes require
a major version and the corresponding Go module major-version path. New
compatible APIs use minor versions; compatible fixes use patches. An unstable
feature must be explicitly labeled rather than silently excluded from the
contract. These are rules for future releases, not a declaration that one exists.

An exported API slated for removal should receive a Go `Deprecated:` comment
with its replacement, a changelog entry, and a compiling migration example.
Record the first deprecated revision and the proposed removal boundary. There
is no guaranteed deprecation duration before a stable release. A security or
correctness correction may reject previously accepted unsafe input immediately;
describe the changed behavior and how applications can adapt. Do not maintain
unsafe acceptance solely to avoid an upgrade note.

Do not change protocol interpretation under an unchanged version, reuse an
immutable asset URL for different bytes, or rename a cache schema to conceal an
incompatible payload. A breaking change needs its own version/schema boundary,
negative conformance fixtures and a migration/rollback account.

## Compiler, binary and browser boundaries

The `go 1.24` language/compatibility target is distinct from production SDK
policy. The Dockerfile currently pins Go **1.27.3** by image digest; CI declares
`1.24.x` and `stable` native/WASM checks. Go 1.24 is outside upstream support
under the policy recorded in [deployment instructions](DEPLOYMENT.md#build-toolchain-policy)
and is a compatibility-test target, not a production recommendation. Production
builds must use an upstream-supported, patched SDK. Update its image digest and
record the actual SDK version before repeating qualification; a floating CI
`stable` setting is not an immutable release toolchain record.

Compile the eager core, every lazy module, and `wasm_exec.js` with the same
selected SDK. The build resolves runtime support in the application's toolchain
context; a hand-copied bootstrap from another SDK is unsupported. The Go/WASM
bridge uses experimental `syscall/js`. Next.go does not promise a Go runtime ABI
across SDKs or a native binary ABI for linking framework/plugin objects. Rebuild
the complete application when changing framework revision or SDK.

UI MIME parsing uses a private licensed Go 1.27.3 parser source port only under
`js && wasm`; native UI continues to use the installed SDK's parser. The port
is compiled with the selected WASM SDK, not linked to a different Go runtime.
Its source grammar does not automatically change when the compiler changes.
Rerun the native/WASM corpora and differential checks when updating either the
port or SDK; the short current fuzz observation is not a compatibility guarantee
for every MIME input or future SDK revision.

The generated native executable requires its target OS/CPU and any application's
native dependencies. Existing deployment fixtures cover macOS and non-root,
read-only Linux; they do not establish a universal OS/architecture matrix.
Lambda supports the documented custom-runtime payload-v2 transports; real AWS
qualification remains open. The optional cache file backend is limited to
Linux/macOS private local filesystems with its documented durability semantics.

Chromium, Firefox, WebKit and touch-viewport fixtures provide bounded implemented
subset evidence; they are not a permanent browser-version support promise or
physical-device certification. Retain the Playwright version, installed browser
versions, OS, skips and reports for a candidate. Useful SSR and ordinary links
remain the initial-document path before WASM. No-JS mutation behavior requires
the application's progressive form mapping. Optional browser view transitions
retain their documented fallback. See [deployment](DEPLOYMENT.md),
[transitions](TRANSITIONS.md), and the exact [qualification record](PARITY.md).

## Independent identities and negotiation

Module ABI **4** was introduced at `467ba2f` because resource invalidation epochs
became mandatory core/module state. That checkpoint corrected late hydration so
old SSR DOM could not adopt a pre-mutation resource seed; the preceding behavior
fails its browser counterfactual. The subsequent phase/metadata/content/MIME
checkpoint kept ABI 4, navigation 3 and action/action-view 1 unchanged.
Historical cookie/dispatch checkpoints remain ABI 3.

The historical link-status increment introduced module ABI **5** for
[per-anchor pending-navigation subscriptions](LINK_STATUS.md), retaining
mandatory resource epochs. The current module ABI **6** additionally carries
recognized document-authority revocation. Paired modules fence pending and
mounted scopes before authored observer dispatch; a later authorized snapshot
admits fresh mounts without reviving revoked scopes. Rebuild the eager core,
every lazy module and all
development entries together from the same framework revision and selected Go
SDK, including its paired runtime support. Mixed older/newer bridge assets fail
registration; reusing only an older module or development entry is unsupported.
Navigation remains 3, action/action-view remains 1 and the private development
extension remains 1. Historical ABI 3/4/5 SDK and browser evidence in
[the changelog](../CHANGELOG.md) retains its original scope; it does not qualify
ABI 6 or establish released-version migration or production readiness. The
query/Refresh merge and ABI-6 privacy source increment remain unqualified until
matching compiler/runtime, core/module/dev, browser and deployment checks run.

These numbers are separate compatibility boundaries. A number in one row does
not imply support for the same number in another row.

| Boundary | Current source contract | Mismatch and operational rule |
| --- | --- | --- |
| Navigation envelope | `version: 3`; live responses advertise `X-Nextgo-Protocol: 3`. Requests opt in with `X-Nextgo-Navigate: 1`. Required document language, direction and viewport accompany build/configuration. | Browser validates before adoption. An incompatible generation/protocol/configuration requires a full document; invalid document settings in a compatible response preserve current UI with a recoverable error. Preserve protocol/configuration headers through ingress. |
| Runtime build | Generated content-addressed build IDs include runtime assets, support/bootstrap, source/application inputs; private prerender bytes also contribute to the final generation. `Config.BuildID` is available to custom servers. | Publish the generated server and all referenced assets together. A manually reused `BuildID` does not turn changed user code into a compatible build. Digests identify bytes/ownership; they do not authenticate them. |
| Runtime configuration | Opaque 64-character digest in HTML, navigation and `X-Nextgo-Configuration`; includes build/protocol, URL ownership, route/endpoint manifest, public values, cache/locale/rule/script/parallel policies and layout/transition settings. | The same executable can have different configuration identities. Public page snapshots are isolated by effective ownership. A speculative prefetch must not initiate a document reload; already-cached compatible data can last until its own expiry or Refresh. |
| Progressive navigation stream | `X-Nextgo-Stream: 1`, newline-delimited frame `version: 1`. | Unknown versions, malformed ordering/fields and truncation fail decoding; content cannot be adopted without envelope/DOM checks. This is independent of navigation envelope version 3. |
| Public partial shell | `X-Nextgo-Shell-Protocol: 1`, opaque shell generation, bounded stale lifetime. | Shell reuse requires explicit capability plus exact compatible shell/configuration; request-time regions are freshly rendered. It grants no authorization. See [partial prerendering](PARTIAL_PRERENDERING.md). |
| Typed actions | `X-Nextgo-Action-Protocol: 1` plus explicit revision in `/_nextgo/actions/<name>/<revision>` and `X-Nextgo-Action-Revision`. | Unsupported sent protocol/revision returns 409 before handler execution; a removed revision is 404. Native forms may omit negotiation headers and still use their explicitly versioned URL. |
| Combined mutation/view | `X-Nextgo-Action-View: 1`, `application/x-nextgo-action-view+json`, frame `version: 1`, action build/configuration/draft preflight. | Incompatible preflight is rejected before mutation. After a confirmed result, a view error does not undo or disprove the write; reconcile the view separately, never replay the confirmed mutation. |
| Eager/lazy browser bridge | Module ABI **6**, descriptor epoch plus SHA-256 digest/length and content-addressed module name; resource epochs, per-anchor link-status forwarding and document-authority revocation before module observer dispatch are part of the core/module contract. | Cross-generation/ABI registration fails. Rebuild/publish the core, all lazy modules and development entries together with paired SDK runtime support; SSR remains available when a lazy component fails. Old-origin DOM cannot reseed invalidated resources. There is no supported independently upgraded module plugin ABI. |
| Asset-tool manifest | `nextgo.assets.json` versions **1** and **2**; v1 retains independent input snapshots and publication of every verified output. V2 adds explicit dependency snapshots and intermediate outputs excluded from public publication. | Unsupported/invalid manifests fail build preparation. Tool versions and dependencies are application build inputs; changing them requires regeneration and checksum review. Tools execute with trusted native authority; staging is not an OS sandbox, and the framework installs no tools. See [asset pipeline](ASSET_PIPELINE.md). |
| Development inspection | Read-only protocol version **1**, bounded public graph/status metadata, loopback-only access policy. | Inspection consumers must check schema version; node IDs are scoped to a graph rather than persistent cross-build identities. This development endpoint is absent from production servers. See [inspection](DEV_INSPECTION.md). |
| Native startup phase | `Config.Phase` selects development, production, prerender or static export before the unchanged Configure hook. No browser phase field. | Unset programmatic phase follows existing native mode flags; unknown or conflicting modes fail construction. Export has separate cache ownership and does not import production prerender snapshots/tickets. See [phases](CONFIG_PHASES.md). |
| Managed public metadata | Six fixed root filenames, frozen validated public bytes, optional Robots/Sitemap/Manifest startup factories. | Static/factory collisions and invalid source/format/size fail before serving. Paths use global BasePath rather than locale/CDN prefixes. Explicit head manifest/icons override defaults; these files cannot depend on page middleware for privacy. See [metadata files](../metadatafiles/README.md). |
| Native export configuration | Version **2** when the managed metadata inventory is nonempty; otherwise **1**. New CLI accepts 1/2. | Earlier CLI rejects 2, preventing silent resource omission. Pair CLI/framework revisions. Published route-artifact formats remain `nextgo-static-v1`/`v2` with an added metadata inventory; metadata bytes are revalidated before publication. |
| Static runtime declaration | `Context.RequireServer()` stages `X-Nextgo-Requires-Server: 1`; no JSON/protocol bump. | Export refuses marked HTML or navigation before body consumption/publication. This supplements existing dynamic-feature checks and does not infer arbitrary Go/API dependencies. See [static dependencies](STATIC_DEPENDENCIES.md). |
| Shared-cache HTTP | Protocol **3**, strict request/response shape. Application `Namespace`/`Schema`/key identities are separate. | Unknown/older protocol fails closed; there is no rolling translation layer or silent local outage fallback. Pair authority/client revisions or use separate authorities for an incompatible transition. |
| Private prerender artifact | Manifest versions 1/2/3 are currently recognized; shared seeds require their supported shape and partial regions require version 3. Bytes are bound to final build identity. | This is trusted private build input, not an upload/interchange format. Regenerate on upgrade. Startup rejects artifact/build/shape mismatches; do not edit embedded bytes to bypass checks. |
| Cache journal | `NEXTGO-CACHE-WAL-1` and seed-aware `NEXTGO-CACHE-WAL-2` formats have bounded recovery checks. | Unknown/damaged/incomplete data, reduced incompatible limits or a regressed clock fail recovery. No arbitrary cross-release file-format migration/backup compatibility is promised. Never repair by truncating/editing live journals. |

See [streaming](STREAMING.md), [action views](ACTION_VIEWS.md),
[dispatch](MUTATION_DISPATCH.md), [URL ownership](URLS.md),
[shared cache](SHARED_CACHE.md), and [prerendering](PRERENDER.md) for the
authoritative behavior and bounds of each subset. This table is not a public
implementation API for internal decoders.

For a custom server that calls application `Configure` itself, populate
`Config.Phase` before that call. `New` does not invoke Configure automatically.
Changing phase does not select an environment-file mode, bypass authorization or
set up a static host's response headers. Existing header-rule/draft/request-time
feature refusals remain in force during export. Applications own phase-specific
data-cache keys and external writes; separate framework snapshot identity is
not application database isolation.

Managed metadata is an explicit public declaration, including output from native
factories. Factories run once during startup and receive no incoming request;
keep private data outside their output. `seo.Metadata.Manifest` is a pointer:
nil inherits, a pointer to `""` disables inheritance, and a nonempty pointer
supplies an explicit manifest link. Explicit icons similarly override managed
defaults. Use these typed controls rather than depending on DOM marker details.
The [content parser](CONTENT.md) has a separate named grammar and bounds; it does
not import CommonMark/MDX source compatibility into the Go package contract.

## A reversible application upgrade

1. Record the current full framework/application commits, module files, SDK
   and container digests, build/configuration identities, environment/ingress
   configuration, action revisions, cache schemas, and artifact checksums.
   Retain the previous executable/static tree and all old runtime assets.
2. Read [CHANGELOG.md](../CHANGELOG.md) and relevant feature contracts, inspect
   the actual diff, then update the pinned dependency and matching CLI. Regenerate
   references, review that diff and run `nextgo generate --check`. Compile native
   handlers and the matching WASM core/module targets; types alone do not catch
   an unbumped semantic action/schema change.
3. For an incompatible mutation, introduce a new explicit revision rather than
   editing the old contract in place. Keep both handlers only when each remains
   authorized and semantically correct. For example, a `cart/add/v2` that uses a
   new quantity unit needs its own DTO/handler; `v1` must retain its old unit or
   be intentionally removed. Do not alias the old name to new semantics. Old
   documents require the old endpoint until their retention window ends.
4. For incompatible cached JSON, use a new application `Schema`, keeping the
   logical namespace stable only if both versions belong to the same data and
   invalidation scope. Shared invalidation spans every schema in a namespace;
   test that old and new writers/readers cannot republish stale data. This does
   not migrate the application's database. Use an expand/contract database
   transition or another explicitly reversible application migration.
5. Build into a new artifact directory. Run native race/vet, both compiler
   targets and the relevant browser/static/executable transport fixtures from
   the frozen source. Exercise old-document → new-server and new-document →
   old-server behavior, retained/removed action revisions, cache authority outage,
   cookies/draft/auth changes, mount moves, deferred responses and writes whose
   acknowledgement is lost. Verify actual ingress and CDN header/cache rules.
6. For a cache authority upgrade, first stop/drain writers and close the single
   authority cleanly before taking a complete private-directory backup on a
   filesystem with the required semantics. Test a copy with the candidate in an
   isolated directory. Keep the original snapshot plus old executable/options;
   opening data with a new authority may change its format. Prefer a separate
   empty authority/schema migration when cross-version reuse is unqualified.
   Do not swap individual journal files or restore a live authority backwards;
   old acknowledged invalidations must not become stale successes.
7. Publish all assets before routing candidate traffic. Keep old generations
   reachable, retain changed-URL redirects and use deployment affinity or a
   coordinated transition where required. Test canary native HTTP and selected
   Lambda/static transports with current production routing before widening.
   A document reload cannot fix a removed URL/asset or make a replayed write safe.
8. Roll back by restoring the previous executable **with** its matching assets,
   environment and routing. Recheck old/new action and persistent-data contracts;
   binary rollback cannot reverse a committed write, unsafe cookie change,
   database migration or acknowledged cache invalidation. If those contracts
   cannot run backwards, reconcile/roll forward explicitly. Do not claim a
   reversible upgrade until that path has actually been exercised.

Retain old assets/endpoints for at least the application's chosen old-document,
cache and in-flight-request windows; Next.go supplies no global deployment
notification or production asset-retention service. Determine those windows
from the deployed application rather than inventing a framework TTL guarantee.
Unknown mutation outcomes require reconciliation/idempotency policy at the
application boundary. There are no automatic mutation retries.

## Release evidence still required

`compatibility_contract_test.go` demonstrates public HTTP identity changes,
old-document rejection before writes and explicit action revision coexistence.
`internal/project/runtime_test.go` checks asset inputs/toolchain support;
`internal/navigation`, `internal/actionview`, and `cache/shared` tests reject
unsupported wire versions; `tests/skew.spec.mjs` exercises local process rollout
and rollback. These are current-source fixtures, not published-version support.

Current and historical feature verification are separated in
[CHANGELOG.md](../CHANGELOG.md). The current source has Go 1.27.3 full race/vet
across 56 packages and matching core/module WASM/vet; Go 1.24 passes the same Go
checks before two final stylesheet-only changes. The recorded metadata/content,
MIME, CSS, browser and export checks qualify only their stated subsets. A fresh
Linux build/export repeat passes 20 styled metadata/content browser checks, with
819 Go/module hashes unchanged from the qualified SDK/WASM matrix. Prior
`467ba2f` remote CI and local browser-through-Lambda results do not qualify new
metadata-specific Lambda behavior; actual AWS and production upgrade behavior
remain unqualified.

Scan `9e1f4938-9cb5-43c6-b086-816de9738d2a` reports zero findings across 88
changed source files in immutable snapshot `272acabad…`, without fixes during
review. It is a bounded diff review, not an independent release security audit.
The earlier pre-fix scan belongs to its historical source snapshot. Current
measured eager WASM transfer/raw size budgets still fail; the separate controlled
MIME experiment does not establish whole-application performance improvement.

G11 remains open: publish immutable release tags and verified install examples,
an actual support/backport table, migration notes for every incompatible change,
a working private security channel, and retained old/new candidate fixtures.
Qualify compiler upgrades, core/module ABI transitions, real ingress/CDN asset
retention, mixed action/cookie/draft generations, authority journal migration and
rollback on frozen artifacts. Link successful reports and unresolved exclusions
from the candidate release record. CI configuration alone and this policy do not
establish those results or relax any other release gate.
