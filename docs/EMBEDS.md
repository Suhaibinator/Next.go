# Sandboxed frames and native media

`ui.FramePolicy` permits explicitly chosen iframe destinations without relaxing
`ui.El`: general iframe, object, embed, script, srcdoc and event-handler rendering
remain forbidden. `ui.Video` and `ui.Audio` render ordinary native controls and
text tracks. These helpers add no application JavaScript and work in server HTML
before Go/WASM starts, during navigation, and in static documents where the
referenced resources are available.

## Explicit frame destinations

Create an immutable policy from trusted application configuration, then construct
the URL using a decoded root-relative path and `url.Values`:

```go
frames, err := ui.NewFramePolicy(ui.FramePolicyOptions{
    Origins: []string{"https://player.example.com"},
})
if err != nil { return nextgo.Page{}, err }
src, err := frames.URL("https://player.example.com", "/watch", url.Values{
    "video": {publicVideoID},
})
if err != nil { return nextgo.Page{}, err }
frame := frames.Frame(ui.FrameOptions{
    Src: src, Title: "Conference recording", Width: 640, Height: 360,
    Sandbox: ui.FrameSandbox{Scripts: true},
})
```

Origins are literal HTTP(S) scheme/authority pairs. Wildcards, credentials,
protocol-relative URLs, paths, queries, fragments, zone-qualified IP addresses
and unsupported ports are rejected. Matching is exact: a different scheme,
subdomain, explicit port or host spelling needs its own allowlist entry. Do not
build the allowlist from an incoming request Host or untrusted query values.
No server-side fetch or provider URL discovery occurs.

`AllowSelf: true` separately permits root-relative sources. Build mounted paths
with `nextgo.MustURL(c, "/api/embed")`; an empty origin passed to `frames.URL`
then produces a root-relative URL. The policy does not infer a public host or
convert a foreign absolute URL into a local one. An empty policy permits no
frames. Nil policies and invalid options return a render error before writing
markup.

Defaults are a required accessible title, `loading="lazy"`,
`referrerpolicy="no-referrer"`, an empty sandbox, and denied camera, microphone,
location, payment, USB, clipboard, fullscreen, autoplay and picture-in-picture
permissions. `Eager` opts into eager loading. Optional dimensions prevent layout
shifts. All attribute values are escaped; no raw iframe attributes or srcdoc
are accepted.

`FrameSandbox` grants individual scripts, same-origin, forms, popups, downloads,
presentation or pointer-lock capabilities. Scripts and same-origin are always
rejected when combined, even for a remote URL: later redirects can change the
effective origin. Top navigation, popup sandbox escape and storage-access grants
are unavailable. A self frame retains an opaque sandbox origin unless
`SameOrigin` is explicitly selected. Some provider players require capabilities
this foundation deliberately cannot express.

`FramePermissions` can grant fullscreen, autoplay and picture-in-picture only to
the frame's source origin (`'src'`). A grant cannot override a restrictive parent
Permissions-Policy. Supported referrer policies are `no-referrer`, `same-origin`
and `strict-origin`; the latter can disclose the embedding origin.

A persistent fallback follows the iframe. By default it is an escaped ordinary
link named `Open <title>`, with `noreferrer noopener` and full-document navigation.
Set `Fallback` to a custom `ui.Node` when a transcript or other alternative is
more useful. Browsers do not reliably report embedding failure to the parent;
this fallback stays visible rather than waiting for an error callback.

## Application CSP

The application owns its CSP header. `CSPSources()` returns a sorted copied slice
of the policy's literal origins and optional `'self'` token. For example:

```go
sources := frames.CSPSources()
if len(sources) == 0 { sources = []string{"'none'"} }
frameDirective := "frame-src " + strings.Join(sources, " ")
// Add frameDirective to your existing complete application CSP header.
```

The Go policy validates the initial source only. An allowed origin may redirect
or navigate its frame elsewhere. Use browser-enforced `frame-src` to constrain
that behavior and independently audit trusted providers. Sandbox restrictions
continue to apply after navigation. The child's `frame-ancestors` CSP or
X-Frame-Options can still prevent embedding; permission delegation, mixed-content
rules, provider authentication and browser privacy rules also apply. The helpers
do not rewrite global CSP, bypass framing restrictions, proxy third-party HTML,
install provider SDKs, manage consent or provide postMessage communication.

## Native audio and video

```go
player := ui.Video(ui.VideoOptions{
    MediaOptions: ui.MediaOptions{
        Label: "Conference recording", Preload: ui.PreloadMetadata,
        Sources: []ui.MediaSource{
            {Src: nextgo.MustAssetURL(c, "/talk.webm"), Type: "video/webm"},
            {Src: nextgo.MustAssetURL(c, "/talk.mp4"), Type: "video/mp4"},
        },
        Tracks: []ui.TextTrack{{
            Src: nextgo.MustAssetURL(c, "/talk.en.vtt"),
            Language: "en", Label: "English captions", Default: true,
        }},
    },
    Poster: nextgo.MustAssetURL(c, "/talk-poster.jpg"), Width: 640, Height: 360,
})
audio := ui.Audio(ui.MediaOptions{
    Label: "Audio recording",
    Sources: []ui.MediaSource{{Src: "/assets/talk.wav", Type: "audio/wav"}},
})
```

Both players require an accessible label and one to sixteen sources. They render
native controls, default to metadata preload, and do not autoplay. `Video`
also includes `playsinline`; `Muted` must be explicit when selecting `Autoplay`.
Browsers may still decline autoplay. Preload accepts `none`, `metadata` or
`auto` and remains a browser hint. `Loop`, poster, intrinsic dimensions, ID and
class are typed options. CrossOrigin accepts empty, `anonymous` or the explicit
`use-credentials` mode.

Sources, posters and tracks require root-relative or HTTP(S) URLs without
credentials, backslashes or literal whitespace/control characters; data, blob,
protocol-relative and script URLs are rejected. Optional MIME types must match
the element's `audio/` or `video/` family; codec parameters are accepted. Text
tracks support captions (the default kind), subtitles, descriptions, chapters
and metadata. Captions/subtitles require a nonempty language and label, and only
one track may be default. At most thirty-two tracks are accepted. Validation
errors occur before media markup is written.

A nil media fallback supplies a download link to the first source inside the
native element. Unlike frame fallbacks, this content is normally visible only
without audio/video element support. Provide a visible transcript alongside
the player for reliable access. Caption quality, language accuracy, codecs,
transcoding, adaptive streaming, DRM, player customization and browser media
capability detection remain application responsibilities. These helpers do not
check remote resources or convert media. CSP `media-src` must allow source URLs;
posters use `img-src`, and text-track requests may also require `connect-src`
under browser CSP implementations. Cross-origin assets/captions need appropriate
CORS response headers and, where necessary, explicit CrossOrigin.

## Demo and verification

`/embeds` uses a local `/api/embed` document with a script-free
`default-src 'none'` policy and a narrowly scoped `frame-ancestors 'self'`
exception. It displays an original silent teal/orange clip in WebM and MP4,
English VTT captions and a short quiet 440 Hz WAV tone. The committed media
files are ordinary public assets (about 13 KiB combined). Public asset helpers
retain mounted/CDN URL placement. This demo calls `Context.RequireServer()`
because its local frame endpoint and optimized poster require a live server;
static export rejects this route rather than publishing broken local resources.
Its normal page response includes `X-Nextgo-Requires-Server: 1`.
Generic `ui.Frame`, `ui.Video` and `ui.Audio` do not require a live framework
server. Static pages can use public assets and a separately hosted frame URL;
API routes are not exported as documents.

`go test -race ./ui ./examples/demo/app/embeds ./examples/demo/app/api/embed`
checks origin/URL attacks, sandbox defaults, escaping, immutable concurrent
rendering, native media validation, the demo endpoint and the page's server
dependency header with mounted/CDN resource URLs. The focused browser
suite is `node scripts/test-browser.mjs --config tests/embeds.config.mjs`; it
uses a production server mounted at `/safe-embeds` with a separate asset prefix
and nonce CSP. It covers JavaScript-disabled SSR, opaque sandbox isolation,
absent referrer, visible fallback, native media metadata and captions, and
frame disposal/history navigation across Chromium, Firefox, WebKit and touch
WebKit. A test file's existence does not establish a successful qualification;
record actual browser outcomes for the deployment being reviewed.
