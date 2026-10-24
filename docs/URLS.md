# Deployment URLs

`Config.BasePath` mounts application pages and endpoints at a literal prefix.
`Config.AssetPrefix` places public files and the Go/WASM runtime at another path
or HTTP(S) origin. Defaults preserve root deployment:

```go
func Configure(c *nextgo.Config) {
    c.BasePath = "/docs"
    c.AssetPrefix = "https://cdn.example.com/release"
    // Optional: c.TrailingSlash = true
}
```

Prefixes use ASCII letters, digits, `-._~`, complete nonempty segments and no
trailing slash (at most 8 KiB per prefix). Empty `BasePath` means root. Empty `AssetPrefix` follows
`BasePath`; `AssetPrefix: "/"` explicitly places assets at root. Queries,
fragments, credentials, dot segments and encoded prefixes are rejected. Prefer
HTTPS for a production asset origin; mixed-content rules still apply.

## Go URL helpers

Routes, configured handler keys, redirect/rewrite rules and invalidation labels
remain application-relative. Build explicit URLs for links and form actions:

```go
ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts?tag=go#first")}, ui.Text("Posts"))
ui.El("img", ui.Attrs{"src": nextgo.MustAssetURL(c, "/logo.svg"), "alt": "Our logo"})
```

Here `c` is a page/layout `*nextgo.Context`. `URL(ctx, path)` and
`AssetURL(ctx, path)` return errors; their `Must` forms are for validated literals.
Escape individual dynamic segment values with `url.PathEscape` before assembling
a path. Inputs start at the app root and are **not already mounted URLs**.
The example above produces `/docs/posts?tag=go#first` and
`https://cdn.example.com/release/assets/logo.svg`.

Browser Go has matching `client.URL`, `client.AssetURL` and `Must` helpers.
`client.Navigate`, `Replace` and `Prefetch` accept browser URLs:

```go
if err := client.Navigate(client.MustURL("/posts")); err != nil {
    status.Text(err.Error())
}
```

Plain HTML retains browser semantics. The framework does not rewrite arbitrary
`ui.El` attributes, embedded CSS, raw HTTP handlers or JavaScript URL strings.
A raw `href="/other-app"` can deliberately leave the mount and follows ordinary
browser navigation. The demo and generated starter use the helpers.

`Context.Redirect("/posts", 303)`, configured redirects and action-result
redirects already accept app paths and apply the base; do not pre-apply a helper
there. Standard `http.Redirect` calls in application handlers need an explicitly
resolved URL. Cookie paths stay under application control through `http.Cookie`.

## Request, rendering and asset ownership

Middleware, loaders and file-routed HTTP handlers receive an application-relative
`Request.URL.Path`, like an `http.StripPrefix` mount. In slash-appending mode,
ordinary route paths are normalized to their logical spelling (`/posts`, not
`/posts/`) before middleware runs. Configured HTTP subrouters receive the original
app-relative slash spelling so directory redirects keep working. `Request.RequestURI` retains
the original escaped public target. Rewrites can change the app-relative URL;
navigation envelopes preserve the original public URL. Parameters and queries
keep their ordinary contracts. Requests outside the base receive a plain 404
without running application middleware or loaders.

Buffered and streamed documents include the configured bootstrap/runtime URLs.
The minimal loader reads those URLs from document attributes. Browser navigation
and loading requests stay inside the mount; endpoints and outside-app links are
excluded. Public page-cache keys include the complete compatibility identity, preventing
HTML/head reuse between differently mounted applications or layout/endpoint
ownership rules sharing a store.

Typed `Page.Resources` and local metadata links resolve through the base. Links
under `/assets/` resolve through `AssetPrefix`; head validation permits stylesheet
and font requests only from the mount or configured public asset subtree.
Absolute canonical/social URLs remain application data: construct them from a
trusted public origin plus the URL helper rather than an untrusted Host header.
The demo does this with its illustrative `media.SiteURL`.

The dynamic image optimizer remains at the application origin. Set
`images.ImageOptions.Endpoint` to the mounted `/_nextgo/image` URL when using it.
Static image attributes, including HTTP(S) width-based srcsets, can use the asset helper. Existing font/CSS generators
and CSS `url(...)` contents are not automatically rewritten: use deployment-aware
sources or relative public CSS URLs. Automated CSS/font URL adaptation remains
unfinished.

Embedded runtime/public assets are also available at the configured asset path.
For a CDN, proxy only versioned runtime paths (`/_nextgo/<build>/...`) and public `/assets/...` paths at that prefix to the executable; do not proxy action/loading/image service paths. A custom server can
use `app.AssetHandler()` behind `http.StripPrefix`; it serves only embedded files,
has anonymous CORS and omits application handlers/middleware. These files are
public and must never contain secrets. Runtime files have immutable URLs; missing
versions do not receive an immutable-cache promise. Runtime IDs include the app
identity, source fingerprint, WASM, bootstrap and compiler-matched runtime support.

Development keeps its local `/_nextgo/events` control endpoint at root. Versioned
asset generations remain available across mount changes. Remote development
asset origins must actually serve the generated assets; a Configure hook can
leave `AssetPrefix` empty when `c.Dev` is true. The generated executable supports
`-path-config` to emit public deployment-path JSON without starting a listener.

## Static hosting and deployment changes

`nextgo export` reads the built app's path configuration. `-paths` and
`StaticParams` still identify app-relative pages. For the configuration above,
output includes:

```text
docs/index.html
docs/posts/index.html
docs/_nextgo/data/posts.json
release/_nextgo/<build>/app.wasm
release/_nextgo/<build>/wasm_exec.js
release/_nextgo/<build>/boot.js
release/assets/style.css
```

Serve pages and navigation JSON at the app origin. Publish/proxy the asset
subtrees at the configured CDN path with anonymous CORS. A plain static host
cannot infer cross-origin headers from files. Export staging rejects file
collisions and preserves the preceding output on failure.

`Config.TrailingSlash` defaults to false: `/posts/` redirects to `/posts`, and
`/docs` is the mounted root. With true, `/posts` redirects to `/posts/`, and the
mounted root is `/docs/`. Canonical redirects use HTTP 308, preserving methods
and bodies. Queries and escaped segments survive redirects. URL helpers,
framework redirects, navigation/history, loading, crawlers and export envelopes
share this policy. File-like final segments (such as `/sitemap.xml`),
`/.well-known/`, `/assets/` and `/_nextgo/` services/assets omit the optional
terminal slash. Route declarations and StaticParams remain logical app paths.
Configured prefix HTTP handlers own their directory slash policy.

Static export emits directory `index.html` files in either mode and records
`trailingSlash` in its manifest. Generated links and navigation JSON follow the
configured policy; configure equivalent canonical redirects on the static host
if required. Static files cannot implement HTTP redirects by themselves.

Runtime changes to mounts/origins or slash policy require coordinated routing
and asset availability. Documents, page envelopes and loading responses carry a
public configuration identity covering build/protocol, deployment paths, endpoint
selection, layout retention, navigation view-transition policy, opaque request-rule/recursive parallel-slot ownership (including parent scopes) and effective public-page cache policies. Cache policy
values/labels contribute an opaque digest; they are not copied into the browser
manifest. A changed or missing identity falls back to full
document navigation before the browser commits incompatible page HTML, head,
state or history. Prefetch does not reload the page and cannot cache an
incompatible response. Loading UI from a different configuration is ignored.
A same-origin redirect outside the preceding mount also uses a full document.
The identity is a compatibility marker, not an authentication signature.
Configuration is established when `nextgo.New` creates an application; mutating
its supplied registration maps/slices after startup is unsupported. Create a new
application instance when changing ownership rules.

Live responses use `X-Nextgo-Configuration`; static JSON embeds the same identity.
Export rejects mismatched HTML/JSON or changes between pages, preserving the
preceding published output. Its manifest records the protocol and identity.
Already cached compatible pages can remain usable until their TTL/Refresh;
there is no deployment notification or affinity service. A removed mount needs
proxy/server redirects so a full-document request can find its replacement.

Local proxy tests switch and roll back real instances of one binary, including
base moves, asset changes and slash policy. Production old-asset retention,
actual cloud/CDN qualification and multi-zone rolling guarantees remain release
work; the fixtures do not prove those properties.
