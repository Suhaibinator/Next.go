# Server metadata inheritance

A directory can define `metadata.go` next to its page, layout, or route group:

```go
//go:build !js

package posts

import (
    nextgo "github.com/Suhaibinator/Next.go"
    "github.com/Suhaibinator/Next.go/seo"
)

func Metadata(c *nextgo.Context, parent seo.Metadata) (seo.Metadata, error) {
    canonical, err := nextgo.URL(c, "/posts")
    if err != nil { return seo.Metadata{}, err }
    return seo.Metadata{
        Canonical: canonical,
        OpenGraph: seo.OpenGraph{SiteName: "Field notes", Type: "website"},
    }, nil
}
```

Root-to-leaf providers run after successful page/layout loading and before any
HTML, stream shell, navigation response or public snapshot is committed. HTTP
endpoints bypass them. Providers receive the final route's parameters, query,
request and cancellation context. They share request `nextgo.Memo` values with
pages and layouts, so the same data loader can supply the body and dynamic head.
These synchronous providers are awaited; slow providers delay the initial
response. The separate deferred callback is described below.

The generator registers `Route.Metadata []nextgo.MetadataLayer`. Each layer has
`Load MetadataFunc` and a safe outer `Layouts` prefix. Hand-written registrations
must use ordered, valid prefixes and non-nil loaders. The Go compiler checks
provider signatures. Application route packages and server-only dependencies
remain absent from the generated browser import graph; the browser receives
rendered metadata rather than provider functions or private loader values.

## Merge and ownership

Providers return their own contribution. The framework merges it over preceding
ancestors using `seo.ResolveContribution`; callers need not merge `parent` manually. `parent`
is a separate snapshot: editing its maps/images cannot accidentally modify
ancestor values. Return those edits explicitly if they are intended.

- Empty string fields inherit. Nonempty child strings override parents.
  `Metadata.Clear` explicitly suppresses inheritance for three empty scalars,
  as described below.
- Open Graph and Twitter fields merge individually.
- Nil image/icon slices inherit; a non-nil empty slice clears that list.
- Language alternates merge by key, with child values taking precedence.
- Maps and slices are copied when contributions are merged/resolved.
- A nil viewport inherits; a non-nil viewport replaces that group completely.

`Page.Metadata` is the final typed contribution. Explicit `Page.Title` and
`Page.Description` retain highest precedence. Legacy `Page.Meta` named keys are
case-normalized, reject case-ambiguous duplicates, and override matching typed
named tags without duplicate output. Its `description` entry is folded into the
single document description as a fallback when the page's explicit or typed
description is absent. Typed URL fields remain validated even when a legacy name
masks their rendered tag. Arbitrary meta names do not become Open Graph property
attributes; prefer typed fields for social metadata.

Empty strings alone do not clear inherited scalars, and alternates cannot yet
delete a parent key. Broader typed metadata fields remain work.

### Explicit scalar clearing

`seo.Metadata.Clear` has three boolean fields in
`seo.ScalarClears`: `Description`, `Canonical` and `Robots`. Return an empty
literal together with its clear flag to suppress an inherited typed value:

```go
return seo.Metadata{
    Clear: seo.ScalarClears{
        Description: true,
        Canonical: true,
        Robots: true,
    },
}, nil
```

Without a flag, an empty child still inherits. A nonempty child literal wins
over its flag and resets that field's clear state; ordinary metadata validation
still applies. Empty clear intent survives defensive `seo.Merge` snapshots and
empty descendants until a later nonempty contribution replaces it. The same
contribution rules apply to directory providers, `Page.Metadata` and a deferred
metadata callback.

This clears typed inherited values, not higher-precedence explicit
`Page.Description` or legacy `Page.Meta` entries. Server-enforced access-denial
`noindex` is unaffected. Normal document rendering keeps its single description
meta element with empty content; typed canonical and robots descriptors are
omitted. It does not clear titles, title templates, social metadata, alternates,
document language/direction/viewport, resources or JSON-LD. Their existing
ownership and validation rules remain unchanged.

The [`/metadata-clear` sample](../examples/demo/app/metadata-clear/page.go)
reuses the native deferred loader with an inherited description, canonical URL
and robots value. Its deferred contribution clears those three scalars while
retaining the body, Go counter, input/focus, description element, stylesheet and
JSON-LD.

Fresh reconstructed-source checks pass seven native/WASM/vet/format commands
on each Linux ARM64 Go 1.24.13 and 1.27.3 SDK. They include 15 named native test
groups for this increment, six portable SEO groups and seven existing core
metadata groups; these are source groups, not totals multiplied across runs.
Production checks pass five steps on Go 1.24 and six on Go 1.27, the latter
including a Darwin cross-build. Initial-HTML and negotiated Go-navigation
fixtures pass eight browser cases per SDK across Chromium, Firefox, WebKit and
touch WebKit: 16 new cases, without skips, retries or flaky results. Both outer
controllers pass. The fixture records each native close/join with exit 0 and
no KILL, and four browser exit-0 observations per SDK; final source/artifact
pins and owned-container cleanup checks pass.

An initial reconstructed run failed three fixture expectations; corrected
expectations left production code unchanged. That failed run and earlier
lost-source SDK evidence remain historical, separate from these fresh v2
results. This checkpoint does not rerun the earlier 344-case metadata/affected
campaign or add LOCAL/Lambda/AWS proof. Complete framework combinations,
physical devices and release qualification remain open. All 79 capability rows
and 11 open release gates remain.

To run the new browser fixture from the repository root, first build the demo,
then pin the actual prebuilt executable:

```sh
go run ./cmd/nextgo build -out /tmp/nextgo-metadata-clear ./examples/demo
export NEXTGO_E2E_SERVER_BINARY=/tmp/nextgo-metadata-clear/server
export NEXTGO_METADATA_STREAM_SERVER_SHA256="$(node -p 'require("node:crypto").createHash("sha256").update(require("node:fs").readFileSync(process.env.NEXTGO_E2E_SERVER_BINARY)).digest("hex")')"
npm run test:metadata-clear
```

The shared fixture requires that absolute executable path and its actual
lowercase 64-hex SHA-256; it verifies the bytes and has no automatic compiler
fallback. It starts its own native process and held loopback source on the
metadata fixture ports. Node/Playwright are development test tooling, not
production application dependencies. Install their existing test dependencies
before running the command, and stop any manually started metadata fixture
server first.

## Deferred metadata

This bounded implementation has scoped native/WASM, production-build, browser
and LOCAL transport checks on Go 1.24.13 and 1.27.3. The qualification boundary
and remaining checks are recorded below; no capability or release gate closes.
Existing synchronous metadata conventions and defaults remain available.

`Page.DeferredMetadata MetadataFunc` uses the existing
`func(*nextgo.Context, seo.Metadata) (seo.Metadata, error)` signature. There is
no new directory filename or replacement for `metadata.go`. Directory providers
remain synchronous. The deferred callback runs after successful synchronous
page/layout/metadata work and returns a typed SEO contribution over its owned
fallback snapshot; it does not render a second body.

Put a complete fallback in `Page.Metadata`. Leave legacy `Page.Title` and
`Page.Description` empty when the deferred contribution should change them:
those legacy fields retain their existing highest precedence. For example,
inside a native page function:

```go
return nextgo.Page{
    Stream: true,
    Metadata: seo.Metadata{
        Title: "Article", Description: "Public article summary.",
    },
    DeferredMetadata: func(c *nextgo.Context, parent seo.Metadata) (seo.Metadata, error) {
        // Application-defined loader: observe c.Context and return public SEO data.
        value, err := loadPublicArticleMetadata(c.Context)
        if err != nil { return seo.Metadata{}, err }
        return seo.Metadata{Title: value.Title, Description: value.Description}, nil
    },
    Body: ui.El("main", nil, ui.Text("Useful article body.")),
}, nil
```

`loadPublicArticleMetadata` is an application function, not a framework API.
The callback may finish before the shell; late delivery is not guaranteed.
Normal inheritance, title templates, URL validation and owned contribution
merging still apply. The callback cannot change effective language, direction
or viewport, including theme/color-scheme settings. Resources and JSON-LD are
evaluated/frozen with the fallback and retain their existing head nodes.

### Blocking and progressive representations

Initial HTML stays blocking by default. A trusted native configuration can
enable its progressive representation:

```go
config.Streaming.Metadata = nextgo.MetadataStreamingOptions{
    HTML: true,
    HTMLLimitedBots: []string{"my-audit-crawler"},
}
```

Progressive delivery requires `Page.Stream=true`, a production GET, and a
recognized browser user agent. Built-in bot tokens, configured literal blockers,
unknown agents and oversized/missing agents retain blocking metadata. Additional
blockers are case-insensitive literal substrings, not regular expressions;
at most 64 values of 1–128 bytes are accepted. This is a compatibility policy,
not crawler identity, authentication or a guarantee for every search engine.

Negotiated production Go navigation can receive a metadata terminal independently
of the initial-HTML `HTML` setting. The framework requests and validates that
representation; applications should not manufacture its protocol headers.
Development, non-streaming pages, HEAD, unnegotiated/buffered clients and build
captures resolve metadata before publication. Uncached speculative requests for a
still-deferred representation return 204 without starting the callback. Already
frozen, approved public snapshots retain their existing prefetch behavior. Partial
public shells explicitly reject deferred metadata. Public snapshot/cache paths
publish only resolved metadata under their existing rules; a streamed response
is not a reusable pending public snapshot.

Progressive initial HTML carries a complete fallback and inert completion data.
The Go client updates only the owned typed SEO head, without replacing the body,
rehydrating islands or moving focus. Superseded-document completions cannot own
the new page's head. Without the Go/WASM client, an opted-in progressive HTML
response retains its fallback; use blocking delivery when final metadata must
be present without that client. Live deferred responses use private/no-store
delivery; this does not make application-private metadata safe for public caches.

### Read-only work, bounds and failures

The callback receives cloned parameters, query and request headers, no live
request body, and a defensive metadata parent. It shares request-scoped `Memo`
with preceding loaders. Response controls and `After` registration are refused;
ignoring those errors still invalidates the contribution. Do not use this path
for status, redirect, header, cookie or cache-policy decisions.
Refused controls use `ErrDeferredMetadataResponse` or `ErrAfterDeferredMetadata`;
document changes use `ErrDeferredMetadataDocument`, and partial shells use
`ErrDeferredMetadataPartial`. HTTP failure output stays generic.

Deferred work shares `Config.Streaming` admission, physical callback slots and
deadlines: defaults are 32 renders, 64 callbacks and 30 seconds. Saturation
before commitment produces the existing busy response. Canceled/timed-out work,
including its owned outstanding Memo work, retains capacity until it actually
finishes. Go cannot stop an uncooperative callback; arbitrary application
goroutines, allocations and process memory are outside this accounting.
Completion data is bounded to 256 KiB and 256 typed SEO elements.

A failure before commitment produces a generic error result. After commitment,
callback errors, invalid contributions or refused response mutations preserve
the complete fallback and send failure rather than a replacement head; no loader
replay or late status change is promised. Late access/status interrupts, arbitrary
head HTML/scripts, broader route/slot ownership and full proxy/CDN/crawler/runtime
qualification remain open.

The [native sample and operator settings](../examples/demo/app/metadata-stream/README.md)
show a cancellable loader, one stylesheet resource evaluation, frozen JSON-LD,
retained Go counter/input identity and an optional trusted loopback fixture.
Its fixture controls are not application browser data.

### Scoped qualification

The frozen private metadata source `ab559a01` passes 13 native/WASM/vet/format
commands and five production build/generation/asset checks per SDK on Linux
ARM64 with Go 1.24.13 and 1.27.3. The actual production-browser fixtures pass
44 cases per SDK across Chromium, Firefox, WebKit and touch WebKit: 88
application cases, without retries or skipped cases. They exercise a held real
HTTP metadata source and metadata-only head completion with retained Go state.

The Go 1.27 outer controller nevertheless failed its post-removal observation
classifier. That failed report remains failed; a read-only derivation records
the 44 application passes and independently verified physical cleanup, without
rerunning them. The Go 1.24 outer run passes completely. These distinctions are
part of the recorded proof, rather than a single all-green campaign claim.

A separate LOCAL suite passes 15 cases per SDK, plus two syntax checks each:
actual native HTTP, local buffered mock Lambda and local streaming mock Lambda.
It checks blocking representations, opted-in browser HTML, negotiated navigation,
generic late failures and cancellation. The fixture asserts each case's cleanup;
independent final evidence establishes Node/process-namespace/listener cleanup,
not 15 independent per-case cleanup receipts. These are local adapters, not AWS
or browser-through-Lambda qualification.

The unchanged affected core/location/lazy/navigation-stream suites also pass
128 cases per SDK across 16 projects: 256 existing cases, with no failures,
skips, retries or flaky results. Together these are 344 browser application cases,
separate from the 30 LOCAL transport cases. Each affected run records native
close/join with exit 0 and no KILL, removed temporary working directories and
16 browser exit-0 observations. The legacy proxy's exact PID/start/argv identity
was observed and later absent from the kernel; its exit code and request/handler/
socket joins remain unobserved. There is no separate final TCP-listener sampler
in that legacy inner driver; final no-new-live-process and complete owned-
container drain are the observed boundary. Metadata-fixture cleanup receipts
are not transplanted onto those legacy cases.

These selected suites are not the complete framework matrix. Export,
development-watch, all framework combinations and full crawler/proxy/CDN/cloud/
resource qualification remain open. All 79 capability rows and 11 open release
gates remain.

### Run the production browser fixture

Build the production demo as shown in the [sample](../examples/demo/app/metadata-stream/README.md),
then run from the repository root with the
Node development-test dependencies and Playwright browsers installed:

```sh
export NEXTGO_E2E_SERVER_BINARY=/tmp/nextgo-metadata-stream/server
export NEXTGO_METADATA_STREAM_SERVER_SHA256="$(node -p 'require("node:crypto").createHash("sha256").update(require("node:fs").readFileSync(process.env.NEXTGO_E2E_SERVER_BINARY)).digest("hex")')"
npm run test:metadata-stream
```

`NEXTGO_E2E_SERVER_BINARY` must be an absolute path to an executable production
binary; `NEXTGO_METADATA_STREAM_SERVER_SHA256` must be its actual lowercase
64-hex SHA-256. The fixture verifies the bytes and has no compiler fallback.
It starts its own native process and held loopback data source; do not start
another copy on its fixture ports. Node is development test tooling, not an
application or production runtime dependency.

## Title templates

A root provider can supply a default title and a template for descendants:

```go
return seo.Metadata{Title: "Field notes", TitleTemplate: "%s — Field notes"}, nil
```

An explicit child `Title: "Posts"` resolves to `Posts — Field notes`. An empty
child title inherits the already resolved title without formatting it again.
The nearest ancestor template wins; a contribution's own template applies to
later contributions, not its own title. `AbsoluteTitle: true` bypasses the
inherited template for that contribution. Templates require exactly one literal
`%s` and are limited to 2 KiB; other percent sequences are literal text.

`Page.Metadata` follows directory providers as a final contribution, including
when it comes from a page in the same directory. These are Go contribution
semantics rather than Next.js source compatibility. Explicit legacy `Page.Title`
bypasses templates. A provider returning a resolved `parent.Title` explicitly
should set `AbsoluteTitle` if it wants to preserve that value: returning it makes
it a new title contribution. `seo.Merge` remains a pure value merge without
formatting; `seo.ResolveContribution` also applies the title-template rule.

## Document language, direction and viewport

Document settings belong to typed metadata:

```go
return seo.Metadata{
    Language: "ar", Direction: "rtl", Title: "مرحبا",
    Viewport: &seo.Viewport{
        Fit: "cover", InteractiveWidget: "resizes-content",
        ColorScheme: "light dark",
        ThemeColors: []seo.ThemeColor{
            {Color: "#126d64"},
            {Color: "#071b18", Media: "(prefers-color-scheme: dark)"},
        },
    },
}, nil
```

Defaults are `lang="en"`, `dir="ltr"` and
`width=device-width,initial-scale=1`. Language tags receive bounded ASCII
subtag validation, not registry lookup or translation selection. Direction accepts
`ltr`, `rtl` or `auto`. Set both explicitly when changing writing direction.

`seo.Viewport` supports width/height, initial/minimum/maximum scale, optional
user scaling, fit, interactive-widget policy, color scheme and up to 16 theme
colors with optional media conditions. Dimensions accept their device keyword
or canonical integers from 1 to 10,000. Zero scales mean unspecified/default;
explicit scales must be positive and at most 10, and a minimum cannot exceed a
specified maximum. Fit, widget policy and color scheme use validated enums.
Colors and media values are bounded printable strings, escaped as attributes;
the browser decides whether their CSS values are supported. A nil
`UserScalable` leaves zoom available. Prefer defaults that preserve zoom.

The server owns one viewport tag. HTML, the initial stream shell, public-cache
snapshots and navigation envelopes carry the same resolved document settings.
Go navigation validates them before changing the current view, then updates
language, direction, viewport, title and other head entries with the page commit.
Theme/color-scheme entries are removed when the destination no longer supplies
them. Legacy `Page.Meta["viewport"]` is rejected; use the typed field. Navigation
data cannot override viewport or description through arbitrary head entries.

The `/locale?lang=en|fr|ar` example demonstrates explicit query selection and
history-aware updates. It does not implement locale negotiation, domain routing
or translation management. Mark shared content with its own language/direction
when it differs from the document. Static export freezes each exported view;
this query-dependent example is not a static translation-routing convention.

## Trusted URL base

A root provider can supply a trusted public origin or directory base:

```go
return seo.Metadata{BaseURL: "https://example.com"}, nil
```

With `BaseURL`, canonical, alternate, Open Graph and Twitter URLs may be
relative references. A base path acts as a directory: base
`https://example.com/docs` plus `article` becomes
`https://example.com/docs/article`; `/article` starts at the origin root.
Absolute HTTP(S) URLs remain independent of the base. Queries/fragments are
permitted on resolved values; the base itself must not contain them.
Credentials, non-web schemes and authority-relative references are rejected.
Without a base, these fields retain their absolute HTTP(S) requirement.

`seo.ResolveURL` exposes the same reference-resolution rule. Resolution owns its
maps/slices and does not replace URLs in caller data. Icon paths keep their
application/public-asset semantics and still use configured asset-prefix
resolution; BaseURL does not move resource stylesheets or font preloads.

Use an application-controlled public origin rather than copying an untrusted
Host/forwarded header. Apply `nextgo.URL(c, ...)` to app-relative references when
using `Config.BasePath` or `TrailingSlash`; BaseURL does not infer deployment
mounts. A CDN URL can be explicit or produced with `nextgo.AssetURL`.

## Cache and failure behavior

Metadata is request-time server data. An opted-in public page cache stores the
final rendered body/head/document snapshot and reuses it without rerunning providers
until expiration or invalidation. Request-specific/private providers must follow
the same cache/auth/variation rules as page loaders. They do not automatically
make a declared public cache safe. Snapshot keys include the framework build,
deployment ownership, host and URI; data changes still need cache lifecycle
policy.

Page/layout failures skip success providers. Provider errors discard all partial
metadata and use the route's not-found/error/access result. An access denial
cannot use a boundary below the provider's safe outer layout prefix. Error and
denial pages supply their own metadata; partially resolved private fields are
never inherited into them. Providers observe cancellation, and contributions
returned after cancellation are discarded. Invalid final head URLs/resources
produce a generic 500 before normal document commitment.

The demo uses root, posts and dynamic-post metadata files. Page and dynamic
metadata call `data.FindForRequest` with the same Memo key. Root BaseURL comes
from the illustrative `media.SiteURL`; replace it for your deployment. Tests
cover inheritance/order, parent isolation, memoization/cache snapshots, failure
privacy, stream-head timing, title templates, viewport/document validation,
SSR/navigation cleanup, export consistency and executable HTTP/Lambda output.
Metadata asset-file discovery, generated social images and real crawler
qualification remain separate work in [the parity inventory](PARITY.md).
