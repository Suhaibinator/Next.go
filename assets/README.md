# Local fonts and stylesheet links

The `assets` package generates typed head links and a content-addressed local
font stylesheet. It uses no browser JavaScript and does not fetch remote fonts.
Provide font files you are licensed to distribute; the paths below are examples,
not bundled third-party assets.

```go
font := assets.Font{
    Family: "Application Sans",
    Source: "/assets/application-sans.742aa1.woff2",
    Format: "woff2",
    Weight: "100 900",
    Display: "swap",
}

fontHandler, fontCSS, err := assets.NewFontStyles([]assets.Font{font})
if err != nil {
    return err
}
// Register in nextgo.Config.Handlers before creating the application:
config.Handlers[fontCSS] = fontHandler

// Resources contribute escaped <link> elements to the document head:
resources := []assets.Resource{
    assets.Stylesheet(fontCSS),
    font.Preload(),
    assets.Stylesheet("/assets/application.css"),
}
```

Application CSS can then select `font-family: "Application Sans", sans-serif`.
For framework-independent integration, call each resource's `Elements` method,
handle validation errors, and escape the returned `seo.Element` attributes when
rendering. `Page.Resources` accepts this resource list when using the framework.
Initialize `config.Handlers` before assigning to it if it is nil.

`Weight` accepts an integer from 1 through 1000 or an ascending two-number
variable-font range. `Style` accepts `normal`, `italic`, or `oblique`. `Display`
accepts the CSS font-display keywords and defaults to `swap`. Optional
`SizeAdjust`, `AscentOverride`, `DescentOverride`, and `LineGapOverride` pointers
emit numeric percentage overrides on that font face. They are not measured or
inferred automatically. Size adjustment must be positive; all overrides are
bounded to 1000 percent. An explicit zero is supported for metric overrides.

The generated CSS handler supports GET, HEAD, ETag revalidation, and immutable
caching. Its URL hashes the font declarations, including source URLs. When font
file bytes change, give the source a new versioned or content-addressed URL too.
Only preload critical faces; preloading every weight can increase bandwidth and
delay other resources. The browser performs its own font decoding and loading.

Optional stylesheet integrity accepts complete `sha256`, `sha384`, or `sha512`
base64 digests. The helper sets anonymous CORS mode when integrity is supplied.
URL validation allows local root-relative files, excludes remote origins and
path traversal, and safely encodes font-family and source strings into CSS.

This is local font delivery support. Automatic downloading, license discovery,
subsetting, format conversion, glyph-based splitting, fallback-metric calculation,
and third-party script loading are not implemented by this package.

## Frozen local font files

`NewLocalFonts` snapshots public font files and their declarations together.
Font URLs hash the source bytes; the stylesheet URL hashes its generated CSS.
After construction, requests use owned bytes and never reopen the source FS.
Changing a source requires a new snapshot and gives it a new font/CSS identity.
No browser JavaScript, external downloader or native font decoder is added.

```go
fonts, err := assets.NewLocalFonts(ctx, assets.LocalFontConfig{
    FS: publicFontFS,
    Fonts: []assets.LocalFont{
        {File: "application.woff2", Face: assets.Font{
            Family: "Application Sans", Weight: "100 900", Display: "swap",
        }},
    },
})
if err != nil { return err }

// Declare public bytes for managed serving and static publication.
config.LocalFonts = append(config.LocalFonts, fonts)

manifest := fonts.Manifest()
resources := []assets.Resource{fonts.Stylesheet(), manifest.Faces[0].Preload()}
```

`Face.Source` must be empty; the constructor sets it to the public content URL.
`Face.Format` may be empty or match the WOFF/WOFF2 header. All existing face
validation and explicit metric overrides apply. A source can back different
family/weight/style declarations without being reread. Identical encoded font
bytes share one published file. Duplicate normalized face declarations reject
the entire construction.

`Manifest` version 1 includes owned faces and sorted asset URL/type/hash/size
records, including the stylesheet. It omits the input file paths and byte
buffers. Returned metric pointers, slices and records can be modified without
changing the serving snapshot. `Paths` returns a fresh sorted list of every
exact handler path. The reserved namespace is `/_nextgo/local-fonts/`.

CSS references generated font filenames relative to the stylesheet. Framework
head resource resolution therefore preserves configured base/locale mounts.
Managed files are served before page middleware, draft sessions and rewrites,
like explicitly public metadata. They are separate from the embedded
`AssetHandler` tree and an automatically published CDN asset set. A separate asset origin needs
explicit routing/publication; setting `AssetPrefix` alone does not move them.

GET/HEAD use exact content types, SHA-256 ETags, immutable public caching and
anonymous CORS. Weak `If-None-Match` validators work; unknown paths and unsupported
methods do not advertise immutable caching. Compression wrappers can weaken
ETags for negotiated representations. Identity-encoding requests retain the
raw content digest/length. Only public, licensed font bytes belong here.

Construction defaults to 4 MiB per source file and 32 MiB total source reads;
explicit overrides are limited to 32 MiB per file and 64 MiB total. Input paths
are relative, at most 4,096 bytes and 32 components, and reject traversal,
backslashes, control characters and symlinks. FS directories must support
bounded `ReadDir` calls; each component examines at most 1,024 entries. File
types/lengths are checked before reads, and changing lengths reject construction.
Context cancellation is checked between operations and reads; custom FS
implementations retain responsibility for their own blocking operations.

Container admission checks WOFF/WOFF2 signatures, declared lengths/table counts,
supported TrueType/OpenType flavors and a 64-MiB declared decoded-size ceiling.
This is not complete font validation, glyph parsing, decompression or sanitizing
an untrusted font. The browser still decodes/validates it. Transport/provider
response quotas are separate from source limits, especially when buffered
Lambda uses base64; larger overrides require platform-specific qualification.

`Config.LocalFonts` accepts at most 32 constructed snapshots, 256 distinct
published files and 64 MiB of total encoded font/CSS bytes. It deduplicates
identical declarations, rejects conflicting routes/handlers and freezes an owned
inventory before locale construction. Its exact hashed paths enter navigation
configuration identity. Export configuration version 3 declares this inventory;
older exporters reject the version. The CLI fetches each declared file once per
locale scope from its private build server, verifies identity encoding, build,
MIME, strong ETag, length and SHA-256, rejects cookies/redirects and publishes
exact files using the existing staged atomic export. Collisions or changed
responses preserve the previous successful publication. Fonts stay under the
application/locale mount even with a separate `AssetPrefix`.

Only managed declarations enable static publication. Manually registering
`Paths()` in `Config.Handlers` remains possible; pages depending on those custom
handlers should call `c.RequireServer()`. The exporter also rejects undeclared
local-font stylesheet/preload links, including encoded scoped URLs. Static hosts
control MIME/cache/CORS/ETag behavior; export publishes bytes and inventory, not
provider header configuration. Same-origin fonts require no cross-origin
permission. Configure and qualify a CDN separately. Managed offline bundles
and independent zone release v1 reject these snapshots until their pinned
inventories support fonts; combined native applications remain supported. The original `NewFontStyles` helper remains available for applications
that version and publish their own font URLs; its CSS bytes/URLs are unchanged.

The [`/local-fonts` example](../examples/demo/app/local-fonts/page.go) uses two
original GO-only fonts, not third-party font data. Its fixtures are generated
by [a development script](../tests/fonts/generate.py) using fonttools and Brotli;
ordinary Go builds embed the checked-in bytes and need neither Python package.
This sample proves delivery/decoding, not production typography, glyph coverage
or layout-shift quality. Automatic font acquisition/subsetting, fallback metric
inference, automatic CDN publication, font collections, broader formats,
full cross-engine/device/crawler and performance/security qualification remain
open in `docs/PARITY.md`.

### Initial snapshot qualification

This historical checkpoint predates managed static publication.

On Linux AMD64, Go 1.24.13 and 1.27.3 each pass nine Chromium cases across
HTTP, buffered local Lambda and streaming local Lambda. Each run verifies both
original formats decoding without application JavaScript, original glyph
advance widths, same-document Go navigation/history resource ownership and
identity hashes/HEAD/ETag/method behavior. The 18 browser executions repeat the
same nine cases across SDKs; no cross-engine claim follows.

Each SDK also passes 18 asset-case groups on the host and again in a source-free,
non-root, read-only Linux container with no network or capabilities, a 256-MiB
memory limit and 128-PID limit. Those groups repeat the same CSS/WOFF/WOFF2 checks
across three transports and ordinary/mounted French/trailing-slash scopes. Each
checks GET, HEAD, POST rejection and weak-ETag revalidation. Six document checks
per execution verify CSP, resource mounts and the server dependency header.
The container tests inspect transport output; browser execution stays outside
the container.

Six new font unit groups, legacy stylesheet regressions and exporter
rollback/encoded-path/documentation-text checks pass. Full Go 1.27.3 native race
tests (80 tested packages)/vet and native race/vet for the affected Go 1.24.13
packages pass. A three-second header fuzz target completes 6,805 executions
(four seconds including shutdown). This remains bounded header testing, not
complete font validation or an independent security review.

The original payloads are 964 WOFF bytes, 592 WOFF2 bytes and 385 generated CSS
bytes. These identity-encoding sizes establish neither network timing nor CLS,
CPU, memory, quality or Next.js performance parity. Both SDKs' source/artifact
pins, manifests, browser cleanup proofs, test summaries, earlier failed attempts
and limitations are in [the qualification record](../docs/benchmarks/local-fonts-qualification.json).
That checkpoint used a 36-case, 12-profile CI definition. The current definition
adds static files for 48 cases across 16 profiles per SDK; remote results remain
pending. The user
selected local-machine qualification for now; hosted deployment remains open.

### Managed static-publication qualification

The later managed-publication checkpoint passes 12 Chromium cases per SDK
on Linux AMD64 Go 1.24.13 and 1.27.3, across native HTTP, both local Lambda modes
and plain static files. The same original formats decode with JavaScript
disabled, and Go navigation/history removes/restores their resources. The
static fixture serves only a hash-pinned published tree with no Go process or
Runtime API and retains its ordinary no-cache policy; deployment headers
remain the host's responsibility.

Each SDK repeats 18 font asset cases and 54 client-only regressions on host and
isolated scratch container. Go 1.27.3 also passes 22 client-only Chromium
regressions. Export tests verify locale files and preservation of the previous
publication under altered bytes, wrong build/type/encoding, cookies, redirects,
weak validators, oversized/truncated bodies, undeclared links and collisions.
Managed offline and independent-release rejection paths are tested explicitly.
Full Go 1.27.3 native race tests (80 tested packages)/vet and affected
Go 1.24.13 race/vet pass against the frozen source.
See [the later qualification record](../docs/benchmarks/local-fonts-static-qualification.json)
for source/artifact pins and the limits of these local checks. WASM size budgets
still fail; the new static support does not qualify performance, security,
cross-engine behavior or hosted deployment. The current CI definition requires
48 font cases across 16 profiles per supported SDK, with remote results pending.
