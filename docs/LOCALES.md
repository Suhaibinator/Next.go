# Managed locale routing

`Config.Locales` mounts the same Go application under configured language
prefixes or exact domain names. It selects a locale for request loaders,
metadata, layouts, APIs, actions and completion callbacks. Translation remains
application code: the demo uses small Go dictionaries in
[`app/translations/page.go`](../examples/demo/app/translations/page.go).

This is a partial internationalization capability. It does not implement ICU
message formatting, plural rules, translation extraction, localized route
names, or per-domain locale subsets. Deterministic subpath static export is
implemented below, including separate artifacts for configured domains.
Request-time negotiation is rejected before publishing static output. Native HTTP and both Lambda transport
modes support the same routing configuration; actual cloud and proxy behavior
still require deployment qualification.

## Configure prefixes

In the application's server-only `app/config.go`:

```go
func Configure(c *nextgo.Config) {
    c.Locales = &nextgo.LocaleConfig{
        Codes:         []string{"en", "fr", "ar"},
        Default:       "en",
        PrefixDefault: true,
        Detect:        true,
        Directions:    map[string]string{"ar": "rtl"},
    }
}
```

The ordinary `/posts/:slug` route becomes `/en/posts/:slug`,
`/fr/posts/:slug` and `/ar/posts/:slug`. `BasePath: "/shop"` adds a common
mount, for example `/shop/fr/posts/html-first`. Files and parameter names stay
the same. Language prefixes are reserved; a conflicting literal top-level page
or API route causes startup validation to fail.

With `PrefixDefault: false`, the default language uses unprefixed URLs.
Explicit default-prefix aliases also work, for example `/en/posts`, so a
negotiated redirect can pin a language without repeating negotiation.
Prefix spelling is canonicalized with a 308 redirect. Route trailing-slash
policy remains controlled by `Config.TrailingSlash`.

With `Detect: true`, a valid configured preference cookie takes priority over
`Accept-Language`. Header matching is bounded, honors positive quality weights,
and tries exact tags, less-specific tags, then configured languages sharing a
primary tag. Unsupported or malformed preferences fall back to the configured
default. This is a bounded language-tag subset, without a complete Unicode
locale matcher or exclusion-set semantics. Explicit language URLs bypass
negotiation. For an unprefixed default, only the home page is negotiated;
with prefixed defaults, bare page URLs are negotiated. Detection redirects are
307 and private/no-store, with `Vary: Accept-Language, Cookie`.

Existing unprefixed APIs, configured handlers and internal service endpoints
continue to use the host's default locale without language negotiation. Explicit
locale-prefixed endpoints receive that locale. Shared asset URLs stay outside
the language prefix, including when using a CDN asset prefix.

## Request helpers and metadata

```go
func Page(c *nextgo.Context) (nextgo.Page, error) {
    language := nextgo.CurrentLocale(c.Context)
    french, err := nextgo.LocaleURL(c.Context, "fr", "/posts/html-first?view=full#body")
    if err != nil { return nextgo.Page{}, err }
    return nextgo.Page{
        Title: language,
        Body: ui.El("a", ui.Attrs{"href": french}, ui.Text("Français")),
    }, nil
}
```

`CurrentLocale` returns an empty string outside managed routing.
`AvailableLocales` returns an owned list. `LocaleURL` accepts a logical
application-absolute URL, validates it using the ordinary URL policy, and
preserves query and fragment. `MustLocaleURL` panics on invalid programmer
input. URL helpers requiring a locale return `ErrLocaleScope` outside a
managed request or enumeration context.

The selected locale supplies default document language and direction before
WASM loads. Explicit metadata providers/pages can override them;
`Content-Language` follows the resolved document language. Canonical and
alternate URLs remain explicit typed metadata; use `LocaleURL` to generate
them. Dictionaries and metadata must agree about the actual content language.

Same-locale links use normal Go navigation, history, loading/error handling and
island cleanup. A different locale has a different mount/configuration
identity, so changing language loads a complete document and resets client
state. This applies to retained layout islands too. It avoids mixing cached
layout content or language-sensitive state between locale scopes.

## Configure domains

```go
c.Locales.Domains = []nextgo.LocaleDomain{
    {Host: "example.com", Default: "en"},
    {Host: "example.fr", Default: "fr"},
}
c.Locales.PrefixDefault = false
```

All configured languages are available on each host; each host has its own
unprefixed default. `LocaleURL` prefers a domain whose default matches the
requested language, otherwise it uses a prefix on the current domain. Domain
URLs are absolute and use HTTPS. `HTTP: true` explicitly enables HTTP for a
local fixture. Authorities come only from configuration, never forwarded
headers. A negotiated domain redirect includes an explicit destination language
prefix so conflicting host cookies cannot bounce between default-language
home pages.

Hosts are ASCII DNS names or IP addresses, optionally with ports. The public
scheme’s default port (80 for `HTTP: true`, otherwise 443) is normalized away.
Both explicit/default-port request spellings select the same configured host;
policies with overlapping accepted authorities are rejected at startup.
Non-default ports remain exact. Use
ASCII/punycode configuration for international domain names. Unknown hosts get
421; malformed hosts get 400. Shared assets intentionally bypass this host
check and remain public. Preserve the configured public Host through proxies
and API Gateway mappings; readiness probes must also use a configured host.
Domain routing is not a tenant authorization boundary.

## Explicit language preferences

`LocaleCookie(ctx, code, secure)` constructs a one-year host-only,
HttpOnly, SameSite=Lax preference cookie, scoped to the global `BasePath`.
The default name is `nextgo_locale`; `LocaleConfig.Cookie` can change it.
Publishing a cookie is explicit application behavior. A private handler or
origin-checked mutation can call `http.SetCookie` with the result. Pass
`secure: true` for public HTTPS; no forwarded-header inference is performed.
Secure cookie-name prefixes are validated. A host-only cookie does not
automatically propagate across language domains. Language links do not silently
persist a preference.

## Resources, caching and builds

Configuration is owned/frozen by `nextgo.New`. At most 32 locale codes,
16 domains and 64 total mounts are accepted. Tags use a bounded ASCII subset:
2–8 initial letters followed by optional 1–8 character alphanumeric segments,
at most 63 bytes overall. Comparison is case-insensitive, preserving configured
spelling in URLs. Set direction explicitly for right-to-left languages.

Locale mounts share the same bounded cache and completion worker/queue budget.
Host, locale mount and configuration identities isolate rendered snapshots;
application loaders must still declare other cache-varying inputs and enforce
authorization. Parent path/tag invalidation reaches matching snapshots in all
locales. Changing a language never grants access to data.

`App.StaticPaths` enumerates localized paths with a combined 10,000-input-row
budget. The CLI exports deterministic language subpaths when `Detect: false`
and `Domains` is empty, or when one configured domain is explicitly selected
with `-domain`. `StaticParams` runs separately with each locale's
context, so dictionaries or a content repository can enumerate different public
parameters per language. Explicit `-paths` are logical application paths and
expand across all configured languages.

```sh
NEXTGO_DEMO_LOCALES=true NEXTGO_DEMO_LOCALE_DETECT=false \
  go run ./cmd/nextgo export -out ./dist/languages \
  -paths /,/translations,/about,/posts,/posts/html-first ./examples/demo
```

Serve that output with an HTTP static host, then open `/fr/translations`.
Set `TrailingSlash: true` for directory hosts that redirect pages to `/path/`
(such as Python’s directory server); the demo flag is
`NEXTGO_DEMO_TRAILING_SLASH=true`. Match the host’s canonical URL policy to the
built application rather than rewriting its configuration after export.
Both prefixed and unprefixed defaults are supported, including a global
`BasePath`, a separate asset prefix and trailing-slash policy. The complete
feature demo still contains server-only pages; select exportable pages as above.

The exporter reads a bounded, versioned `App.StaticExportConfig` declaration
from the actual generated binary before enumeration/rendering. Each language
owns a configuration identity and data namespace: `/fr/_nextgo/data/about.json`
contains French-scope data, while `/en/_nextgo/data/about.json` belongs to English.
HTML and navigation JSON must agree with that build declaration. Scope changes,
errors and filesystem collisions abort staged output and preserve the previous
publication. Locale exports use a `nextgo-static-v2` ownership manifest; plain
single-scope exports retain v1. The current CLI recognizes both for replacement.
Older CLIs do not recognize v2 and must not be used to overwrite it.

Only selected/enumerated canonical language pages are emitted. Bare URLs that
the native prefix router redirects to the default language, and explicit default
prefix aliases in unprefixed mode, are not generated automatically. Configure
such redirects at the static host if required. Static output cannot read a
preference cookie/header, serve APIs/actions, or rerun query-dependent loaders;
query strings retain browser history while selecting the same built page data.
Cross-language links continue to load a fresh document.

`App.CheckStaticExport` checks an unselected configuration. For a domain build,
use `App.StaticExportConfig(nextgo.StaticExportOptions{Domain: "example.fr"})`
and `App.StaticPathsForDomain(ctx, "example.fr")`. The generated binary exposes
`-export-config -export-domain example.fr` and
`-static-paths -export-domain example.fr` for native tooling.

### Separate domain artifacts

Set `Detect: false` and configure trusted domains as above, then build each
host into its own output directory:

```sh
go run ./cmd/nextgo export -domain example.com -out ./dist/english \
  -paths /,/translations,/about ./myapp
go run ./cmd/nextgo export -domain example.fr -out ./dist/french \
  -paths /,/translations,/about ./myapp
```

These commands assume `./myapp/app/config.go` declares those exact hosts. Domain
selection is required; missing, malformed or unconfigured hosts fail before
publication. The artifact contains all configured language scopes for the
selected host, using that host's default prefix policy. Enumeration runs with
its locale/domain contexts. Cross-language links prefer the configured default
language domain, just as they do on the native server.

The exporter connects only to its local build server and sets the selected Host
header. It never fetches the configured public domains. Both HTML and JSON must
match the selected host's declared configuration identities. The v2 publication
manifest records canonical `domain` ownership, including normalization of the
configured public scheme's default port. Rejected builds preserve prior output.

Deploy each directory to its matching domain. A static file host must enforce
that binding explicitly; the manifest alone does not reject an incoming Host.
The two-origin browser fixture demonstrates a file server that reads manifest
ownership and rejects unknown hosts. Configure HTTPS, public-host validation,
asset routing and canonical redirects at your actual host/CDN. Domain routing
and manifest identities remain compatibility controls, never tenant auth.

Request-time cookie/header negotiation remains unsupported on plain static
hosting. Keep `Detect: false` for static artifacts, or use the embedded HTTP or
Lambda server. Portable host redirect generation and real TLS/CDN/cloud domain
rollout qualification remain unfinished.

Run the demo in prefix mode:

```sh
NEXTGO_DEMO_LOCALES=true go run ./cmd/nextgo dev -addr 127.0.0.1:3020 ./examples/demo
```

Open `/fr/translations` or `/ar/translations`. Only the translations example
has dictionaries; enabling a locale does not translate the other example pages.
`/fr/api/locale` reports the selected scope. The development browser fixture
also uses `NEXTGO_DEMO_LOCALE_DOMAINS` for local HTTP domains; production apps
should configure trusted HTTPS domains directly.

## Verification and remaining work

Native tests cover validation, owned configuration, prefix/domain dispatch,
cookie/header selection, parameter/query preservation, metadata/error language,
API/action context, cache separation, shared completion capacity and enumeration
bounds. Independent-module CLI tests verify language-specific parameter enumeration
and selected-host scope publication, including missing/unknown-domain rejection
without replacing a prior artifact. Browser fixtures exercise SSR before WASM, DOM-preserving hydration,
same-language navigation/history, full-document language switches, direction,
configured origins and cookie-bounce prevention. Plain-static-host Chromium,
Linux Firefox, WebKit and touch fixtures cover localized hydration, navigation
data namespaces, prefixed/unprefixed defaults, domain-specific artifacts and origin switches,
unknown-host policy and failures
that preserve an earlier publication. Executable deployment tests
cover native HTTP plus buffered and streaming mock Lambda routing.

Negotiated static publication and host redirect generation, per-domain locale subsets, localized route
names, translation tooling/formatting, full language-preference conformance,
and real HTTPS/CDN/cloud/accessibility/load qualification remain open in
[the parity inventory](PARITY.md). The
[Next.js internationalization guide](https://nextjs.org/docs/app/guides/internationalization)
is a capability reference; this Go API does not promise source compatibility.
