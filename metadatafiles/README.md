# Managed app-root metadata

This native-only package recognizes sixteen public filenames: `robots.txt`,
`sitemap.xml`, `favicon.ico`, `icon.png`, `apple-icon.png`,
`manifest.webmanifest`, `opengraph-image.png`, `twitter-image.png`,
`opengraph-image.alt.txt` and `twitter-image.alt.txt`, plus `.jpg`, `.jpeg`
and `.gif` variants of each social-image role. Each role permits only one
image variant at a time; a Go PNG producer also occupies that role. `Sources.Static` is an `fs.FS` rooted at those files;
unknown names and directories are not published. Named symlinks, special files,
and case variants are rejected. Existing manual endpoint directories remain
ordinary routes rather than automatic metadata declarations.

`Sources.Robots`, `Sitemap`, `Manifest`, `OpenGraphImage` and `TwitterImage`
optionally supply typed Go startup producers instead of static files. A file and producer for the same name
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

- Combined frozen bytes: 8 MiB, with sixteen recognized names and at most ten simultaneously published
  resources because social-image variants are mutually exclusive.
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
- Social-image alt text: at most 1,024 UTF-8 bytes. It requires a matching image variant
  or Go PNG producer. Trimmed text must be nonempty and contain no Unicode control
  characters (including embedded ASCII line breaks); leading/trailing whitespace is removed for head attributes. The
  exact original text bytes remain the public exported file.
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
`manifest.go`, `opengraph-image.go` and `twitter-image.go`. Native producers use `//go:build !js` and signatures:

```go
func Robots(context.Context) (seo.RobotsOptions, error)
func Sitemap(context.Context) ([]seo.SitemapEntry, error)
func Manifest(context.Context) (metadatafiles.Manifest, error)
func OpenGraphImage(context.Context) ([]byte, error) // Public PNG, <= 1 MiB.
func TwitterImage(context.Context) ([]byte, error)  // Public PNG, <= 1 MiB.
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

## Generated social image publication

Image producers can call `socialimage.Render` with explicitly public Go data.
Use `socialimage.Limits{MaxOutputBytes: metadatafiles.MaxImageBytes}` so generation
obeys the metadata file quota. They run once per application startup, including
separate build-tool configuration/enumeration/export processes. HTTP requests
read the frozen PNG; they never invoke a producer or font/image decoder.
The CLI captures that same declared inventory for static hosting and verifies
build, MIME, length, digest, PNG pixels/dimensions and declaration before atomic
publication. Bad responses leave the previous publication in place. Producers
must be deterministic across the separate tool processes with the same SDK.
Go PNG encoders can produce different bytes/ETags across SDK releases even when
decoded pixels are identical; do not assume cross-SDK byte equivalence. This is not build-only
precomputation baked into every executable; qualify cold-start work separately.

`Set.SocialHead(base)` returns owned Open Graph/Twitter default meta elements,
using only the explicitly trusted metadata base and global application mount.
It includes PNG type/dimensions and optional alt text, plus a default Twitter
`summary_large_image` card. Empty base omits automatic social tags while keeping
the files public. The framework uses `Metadata.BaseURL`; request Host,
forwarded headers, asset/CDN prefixes and locale document prefixes never supply
an origin for these image tags. Metadata callbacks may set a different trusted
base for a particular page/domain themselves.

In framework integration, a non-nil typed image list replaces its corresponding
file defaults; a non-nil empty list clears that image group. An explicit Twitter
card value wins, and legacy named Twitter image/card metadata retains its normal
precedence. Global file URLs do not gain a second base or locale prefix during
head assembly. Deferred metadata, navigation ownership and static publication
use the same descriptors. The public files remain available when a page clears
its head defaults.

App-root PNG/JPEG/GIF and bounded alt-text conventions are supported here.
Static `.jpg` and `.jpeg` files use `image/jpeg`; static `.gif` files use
`image/gif`. Header/canvas bounds precede complete standard-library decoding.
JPEG admission additionally caps 128 scans and 4,096 markers before full
coefficient/raster decoding. GIF admission walks the block envelope before
`DecodeAll`, limiting 32 frames, 8,388,608 aggregate frame pixels and 16,384
blocks; every frame rectangle must fit the bounded logical canvas. This bounds
encoded data, dimensions and work units, not a hard wall-clock deadline.
Malformed/truncated images and conflicting variants fail; an image file and Go
producer for the same role also conflict before any producer runs. File variants
retain their original bytes, dimensions, MIME and exact URL in head defaults
and export. Go producers continue to return PNG only.

The [original JPEG/GIF example](../examples/social-variants/README.md) includes
a two-frame procedural animation, a JPEG and a retained Go counter. Nested
file precedence, modern codecs, multiple generated image IDs, arbitrary
request-dependent producers and full Next.js metadata composition remain open.
There is no default-origin inference, font shaping, HTML/CSS image renderer,
automatic CDN header setup or crawler/cold-start/load/security qualification.
