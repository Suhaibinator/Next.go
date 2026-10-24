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
Providers are awaited; slow metadata currently delays the initial response.

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

Empty strings do not explicitly clear inherited scalar fields, and alternates
cannot yet delete a parent key. Metadata streaming and broader typed metadata
fields remain work.

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
