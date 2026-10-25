# Dynamic PNG social images

`socialimage` provides a Go-native server-only image response capability. It
composes typed rows, columns, overlays, colored panels, bitmap text and supplied
image bytes into deterministic PNG output. It uses only the standard library.
The runnable `/social-image` demo renders a branded 1200 × 630 title/description
card; `/api/social` serves its PNG. No browser JavaScript is needed to generate
or display it.

This is a limited composition API, not JSX, HTML/CSS, SVG or a browser layout
engine. It does not implement general ImageResponse/Satori font or layout
compatibility, automatic social-image filename discovery, or static publication
of generated images. The dynamic demo declares `Context.RequireServer()` and
requires a server deployment; static export rejects it before publication. For
a plain static host, call `Render` during your own build and publish its bytes
as an ordinary public asset.

## A route handler

Put server-only imports behind `//go:build !js`, as with other route handlers:

```go
//go:build !js

package social

import (
    "context"
    "image/color"
    "net/http"

    "github.com/Suhaibinator/Next.go/socialimage"
)

var cards = func() *socialimage.Handler {
    h, err := socialimage.NewHandler(socialimage.Config{},
        func(ctx context.Context, r *http.Request) (socialimage.Document, error) {
            // Validate any application inputs and authorize private data here.
            return socialimage.Document{
                Width: 1200, Height: 630,
                Background: color.NRGBA{R: 9, G: 32, B: 28, A: 255},
                Root: socialimage.Layout{Padding: 56, Gap: 28,
                    Children: []socialimage.Item{
                        {Size: 60, Node: socialimage.Text{
                            Value: "FIELD NOTES / NEXT.GO", Scale: 5,
                            Color: color.NRGBA{R: 121, G: 234, B: 185, A: 255},
                        }},
                        {Node: socialimage.Text{
                            Value: "Useful ideas. Shareable pixels.", Scale: 9,
                            Color: color.NRGBA{R: 255, G: 255, B: 255, A: 255},
                        }},
                    },
                },
            }, ctx.Err()
        })
    if err != nil { panic(err) } // Fixed configuration; validate at startup.
    return h
}()

func Handle(w http.ResponseWriter, r *http.Request) { cards.ServeHTTP(w, r) }
```

`Render(ctx, document, limits)` returns caller-owned encoded bytes without HTTP.
Errors wrap `ErrInvalid`, `ErrLimit`, or the request's cancellation error; use
`errors.Is`. Neither API reads a URL, filesystem path, environment setting or
browser component prop. The application supplies public content explicitly.

## Composition contract

- `Layout` defaults to a column; `Direction: socialimage.Row` selects a row.
  `Padding` surrounds all children and `Gap` separates them. An `Item.Size`
  reserves pixels along the direction. Zero-sized items divide remaining space
  equally, with extra pixels assigned in declaration order. Children fill the
  cross axis. Negative dimensions, excessive padding and layouts whose fixed
  children/gaps do not fit fail. There is no content-dependent auto sizing.
- `Overlay` draws children back-to-front in the same box. `Panel` fills its box.
  Colors use `color.NRGBA` and alpha compositing; set `A: 255` for opaque colors.
- `Text` uses integer scaling, greedy word wrapping, explicit `\n`, optional
  line gap and start/center/end horizontal alignment. `Scale: 0` selects 4.
  Only complete glyphs and lines fit. Long words split across lines; text beyond
  the available height or `MaxLines` is clipped without ellipsis or shrinking.
- `Picture` accepts encoded PNG, JPEG or GIF bytes; GIF uses the first frame.
  `Contain` centers an aspect-preserving image, `Cover` centers/crops it, and
  `Stretch` fills the box. Scaling uses nearest-neighbor sampling. SVG, remote
  URLs, animation output and other codecs are unsupported.
- `Node` accepts the package's value types. Nil and pointer nodes fail. Input
  documents, child slices, image bytes and fonts must not be mutated concurrently
  with rendering. The validated plan owns copied image bytes and glyph rows.

## Font scope and licensing

The working default `ASCIIFont` is an original handwritten 5 × 7 bitmap face
licensed under the repository's [MIT license](../LICENSE). It supports printable
ASCII characters U+0020–U+007E. Lowercase letters render as uppercase. Newlines
are handled by layout; other control characters and invalid UTF-8 fail.
Unsupported glyphs fail instead of being silently replaced. This is a readable
pixel style, deliberately visible in the demo, with no claim of general
typographic compatibility.

Applications can implement `Font` using fixed-metric raster glyph data. `Metrics`
returns width (1–32), height (1–64), and advance (width–64). `Glyph` returns exactly
height rows of five-/up-to-32-bit masks, where bit `width-1-x` controls column x.
Each text node can select its own font; the plan validates and copies each glyph.
Fonts may supply other Unicode code points, but there is no TTF/OTF loading,
kerning, variable advances, antialiasing, bidirectional ordering, shaping,
ligatures, fallback lookup or emoji composition. Supply the appropriate licensed
raster data and implement additional typography outside this package if needed.
Font callbacks are trusted application code and must be concurrency safe and
cooperate with cancellation. Resource limits cannot constrain arbitrary work or
allocations performed inside application callbacks.

## Resource limits and cancellation

Every document undergoes structural/text/source-byte validation before invoking
any font or image decoder. Defaults and maximum configurable values are:

| Limit | Default | Maximum |
| --- | ---: | ---: |
| Output dimension | 2,048 | 8,192 |
| Output pixels | 4,000,000 | 16,000,000 |
| Nodes / nesting depth | 256 / 16 | 1,024 / 32 |
| Total UTF-8 text bytes | 4,096 | 32 KiB |
| Total encoded input image bytes | 8 MiB | 32 MiB |
| Pixels per input image | 16,000,000 | 32,000,000 |
| Encoded PNG bytes | 20 MiB | 64 MiB |
| Concurrent HTTP operations | 2 | 64 |
| Process cache bytes / entries | 16 MiB / 64 | 256 MiB / 4,096 |
| HTTP path + raw query bytes | 8,192 | 65,536 |

Zero config fields select defaults; invalid limits fail at construction/render.
Image headers are inspected before full decoding. PNG output uses a bounded
writer. The drawing plan, output canvas and decoder scratch memory are separate
from encoded cache bytes, so `MaxCacheBytes` is not a total process-memory cap.
Choose limits/concurrency appropriate to your process memory budget. Decodes are
sequential within a document; multiple handler workers may decode concurrently.
Limits bound dimensions/work units rather than guarantee a wall-clock deadline.

The HTTP admission slot covers the builder, planning, decoding and encoding.
Queued requests can cancel. Running work checks cancellation between nodes,
glyphs and image rows, during decoder reads and PNG writes, and before publishing
or caching output. Already-running standard-library codec operations may take
time to reach a check. No goroutine detaches a callback to release its slot:
a canceled, noncooperative builder/font retains admission until it returns.
`Render` has no global admission mechanism; applications calling it directly
must bound their own concurrency. Disconnected requests publish no success body.

## HTTP caching and errors

Builders run on **every** GET/HEAD, including process-cache hits and conditional
requests. Authorize and validate data in the builder before returning a document.
Wrapping `ErrBadRequest` selects 400; wrapping `ErrNotFound` selects 404. Other
builder/layout/decoder/font/limit failures select a generic 500. Failure messages
do not expose application errors, text, image bytes, or private values and have
`Cache-Control: no-store`. Unsupported methods return 405 with `Allow: GET, HEAD`;
oversized request targets return 414. No partial PNG is committed on failure.

The process-local, synchronized LRU caches encoded bytes by normalized drawing
content, including dimensions, colors, glyph masks and copied source bytes.
Request URLs or application-supplied cache keys do not select cached content.
Input/font changes produce different keys. Cache hits still perform planning,
font lookup, and image-header validation; they skip full image decoding, pixel
composition and PNG encoding. Concurrent misses can duplicate work within the
admission bound. `DisableCache` bypasses the LRU; oversized entries are not stored.
No persistent cache, cross-process sharing, TTL/invalidation tags, image-format
negotiation or last-modified policy is provided.

Responses use `image/png`, `nosniff`, a strong SHA-256 ETag of the PNG, and
`private, max-age=0, must-revalidate`. `If-None-Match` accepts strong/weak tags,
comma-separated candidates and `*`, returning 304 without a body. HEAD produces
the same content headers/ETag as GET and no body. `Config.Public: true` explicitly
switches HTTP caching to `public, max-age=0, must-revalidate`; only use it for
content safe for every viewer and shared caches. The demo opts in because its
inputs are public title/description strings. It does not read authentication,
private data, external images, or request Host into the card.

## Metadata integration and verification

Use the endpoint URL with `seo.OpenGraph.Images` and `seo.Twitter.Images`.
Build deployment-aware local paths with `nextgo.URL`/`MustURL` and resolve the
trusted public origin using `Metadata.BaseURL` or an application constant. Do not
construct crawler-facing origins from untrusted Host/forwarded headers. The demo
uses the illustrative `media.SiteURL` (`https://example.com`); replace it before
publishing. Real crawler/CDN behavior, broad font quality, large-scale/soak memory
and production cache qualification remain open.

Focused native/race tests cover deterministic PNG encoding, row/column bounds,
visible glyphs/wrapping, all default ASCII characters, inert text injection,
unsupported/control/UTF-8 rejection, source/bitmap ownership, picture fit and
byte/pixel bombs, generic failures, GET/HEAD/ETag behavior, cache eviction and
privacy, per-request authorization, concurrency, cancellation and physical slot
retention. Demo endpoint tests decode the actual branded 1200 × 630 PNG; page
tests validate escaped output and social metadata. Run:

```sh
go test -race ./socialimage ./examples/demo/socialcard ./examples/demo/app/api/social ./examples/demo/app/social-image
```
