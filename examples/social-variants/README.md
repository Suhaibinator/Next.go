# Original JPEG and GIF metadata

This Go/WASM example publishes an original procedural JPEG and two-frame GIF as
Open Graph/Twitter metadata, with bounded alt-text files. Native startup validates
and freezes all frames/bytes; static export verifies and publishes the same
inventory. Files remain at the global application mount independently of asset
prefixes. The inherited metadata base is illustrative `https://example.test`;
replace it before deployment. No external image service or application JavaScript
is required to decode the image previews.

```sh
go run ./cmd/nextgo dev ./examples/social-variants
go run ./cmd/nextgo build -out /tmp/social-variants ./examples/social-variants
NEXTGO_METADATA_BASE_PATH=/lambda-features NEXTGO_METADATA_ASSET_PREFIX=/lambda-assets \
  go run ./cmd/nextgo export -out /tmp/social-variants-static ./examples/social-variants
```

The Go counter adopts the SSR value and remains mounted when the clear page
removes metadata defaults and history restores them. The component wrapper uses
the same tested counter implementation as the metadata-site example. The
compiler discovers the local `components/counter/client_js.go` entry point.

One file per metadata role is required. A `.jpg` plus `.png`, `.jpg` plus `.jpeg`,
or a static variant plus its Go PNG producer is rejected. There is no silent
file preference. Both native and compiler/export declaration admission apply
this rule. PNG producers remain PNG-only; JPEG/GIF variants here are explicit
static assets. WebP/AVIF/SVG, arbitrary GIF/JPEG extensions beyond Go's decoder,
nested metadata conventions and full crawler/CDN qualification remain open.

The MIT-licensed fixtures are original procedural artwork; regenerate with:

```sh
cd examples/social-variants/app
go generate
```

The native fixture checks complete JPEG and both GIF frames, MIME, hashes,
GET/HEAD/POST/ETag behavior through HTTP and both local Lambda modes, on host and
source-free scratch containers. The prepared browser matrix defines 48 cases
across four transports and four profiles, with pinned production/static artifacts.
Local execution and remote results are recorded separately in
[`docs/PARITY.md`](../../docs/PARITY.md). Browser decoding does not qualify the GIF
animation clock, motion accessibility, arbitrary imagery or production load.
See the [admission limits](../../metadatafiles/README.md#generated-social-image-publication).
