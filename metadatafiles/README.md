# Managed app-root metadata

This native-only package snapshots six public filenames: `robots.txt`,
`sitemap.xml`, `favicon.ico`, `icon.png`, `apple-icon.png`, and
`manifest.webmanifest`. `Sources.Static` is an `fs.FS` rooted at those files;
unknown names and directories are not published. Named symlinks, special files,
and case variants are rejected. Existing manual endpoint directories remain
ordinary routes rather than automatic metadata declarations.

`Sources.Robots`, `Sitemap` and `Manifest` optionally supply typed Go startup
producers instead of static files. A file and producer for the same name
conflict. Producers run once with startup cancellation, never an HTTP request.
Their context receives only the configured global deployment path policy for
URL helpers; the package does not add an incoming Host, cookie, locale, or asset
origin. Producers must cooperate with context cancellation and return public
data. Go code may perform its own explicit work; this package fetches nothing.

```go
set, err := metadatafiles.New(ctx, metadatafiles.Sources{
    Static: embeddedMetadata,
    Sitemap: func(context.Context) ([]seo.SitemapEntry, error) {
        return []seo.SitemapEntry{{URL: "https://example.com/"}}, nil
    },
}, metadatafiles.Options{BasePath: "/base"})
```

The snapshot owns its bytes. `Read` returns a copy; `Entries` and `Head` return
independent descriptors/maps. `Digest` identifies the complete frozen
name/path/MIME/content set, and is empty when no resources exist. Do not mutate
caller-owned inputs concurrently while construction is reading them.

`ServeHTTP` matches the original escaped public URL exactly, for example
`/base/robots.txt`. It does not strip or add a second prefix, redirect trailing
slashes, negotiate language, or use an asset CDN prefix. GET/HEAD responses use
explicit MIME, nosniff, Content-Length, strong SHA-256 ETags, If-None-Match/304,
and `public, max-age=0, must-revalidate`. Unsupported methods return 405. These
are explicitly public bytes independent of request credentials; the application
dispatcher decides its configured-host and header-rule policy.

`Head` returns final global mounted icon/manifest links, suitable for appending
**after** ordinary page URL resolution. The framework owner decides default
inheritance and overrides. The package itself does not modify HTML, browser
state, locale routing or shared caches. It does not infer an origin-root
robots policy when the application owns only a subpath.

## Supported formats and limits

- Combined frozen bytes: 8 MiB, with fixed six-name inventory.
- Robots: UTF-8 public plain text, 512 KiB, at most 8,192 lines and 8 KiB per
  line. Comments and extension directives such as Crawl-delay/Host are preserved;
  no closed crawler grammar or access-control guarantee is claimed. Binary
  controls and line-leading markup are rejected. Typed Go generation reuses
  `seo.MarshalRobots`, including its directive validation, with input quotas.
- Sitemap: UTF-8 XML, 4 MiB, at most 4,096 entries. Supports default-namespace
  `urlset` and `sitemapindex`, absolute HTTP(S) `loc`, optional RFC3339/date
  `lastmod`, and URL-entry `changefreq`/`priority`. Missing/duplicate/unknown
  fields, wrong namespaces/attributes, deep nesting, directives, custom entities
  and processing instructions other than an initial XML declaration fail.
  Standard escaped XML characters and inert comments remain supported. Go
  generation reuses `seo.MarshalSitemap`; it does not discover or crawl URLs.
- PNG: at most 1 MiB, 2,048 pixels per dimension, 4,194,304 pixels. The header is
  checked before complete raster decoding; malformed/truncated data fails.
- ICO: at most 1 MiB and 32 non-overlapping entries, each at most 256×256. Accepts
  PNG payloads or uncompressed 1/4/8/24/32-bit DIB payloads with bounded headers,
  palettes, pixels and masks. Unsupported compression/variants fail. SVG and
  cursor containers are not supported or sniffed as alternate formats.
- Manifest: strict UTF-8 JSON, 64 KiB, at most 16 icons; duplicate, unknown,
  case-aliased, null and incorrectly typed fields fail. Supported fields are
  name, short_name, description, lang, dir, id, start_url, scope, display,
  orientation, background_color, theme_color and icons. Text fields are bounded.
  Display/orientation/direction and icon-purpose values are enumerated; colors
  accept hexadecimal forms and alphabetic named-color tokens. The browser owns
  interpretation of a particular named color.

Manifest URL fields must be local references within the global application
mount. Relative references resolve from `manifest.webmanifest`; omitted
start/scope behave as `./`. Scope must be a directory without a query/fragment,
and contain start_url. Icon references support local PNG/ICO URLs, finite numeric
sizes and any/maskable/monochrome purposes. Linked page/asset availability and
installability remain application/hosting responsibilities. No remote URL,
SVG, service worker, install workflow or offline behavior is added.

## Integration boundaries

The generator discovers root static files and `robots.go`, `sitemap.go`,
`manifest.go`. Native producers use `//go:build !js` and signatures:

```go
func Robots(context.Context) (seo.RobotsOptions, error)
func Sitemap(context.Context) ([]seo.SitemapEntry, error)
func Manifest(context.Context) (metadatafiles.Manifest, error)
```

Discovery parses source without running it; generated typed assignments let the
Go compiler check signatures. The app-root metadata directory cannot introduce
a nested Go module. Static copying rejects changed bytes and output collisions
inside a disposable generation. The framework's publication transaction, not
this copy helper, protects an existing production artifact.

Native initialization, locale-global dispatch, metadata head/cache identities
and matching static-export publication are separate framework integration
points. Export must select only this declared inventory, verify each resource's
type/size/digest/build, and publish exact bytes at its global mounted path.
Static hosts own MIME, ETag and cache-header behavior; bytes alone do not install
native HTTP response policy at a CDN. Startup producers must be deterministic
for export configuration and served snapshots to agree. This bounded subset
does not establish full metadata parity or close production release gates.
