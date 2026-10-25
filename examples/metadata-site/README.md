# Public metadata files example

This small Go/WASM application demonstrates managed application-root metadata:
original `icon.png`, `apple-icon.png`, and `favicon.ico` files, plus typed Go
`Robots`, `Sitemap`, and `Manifest` startup factories. The compiler discovers
these declarations; there are no manual metadata handlers or static/Go conflicts.

The overview and about pages inherit icon/manifest defaults. The explicit page
replaces both groups, and the disabled page deliberately clears them. A Go
counter in the retained root layout demonstrates that soft navigation updates
the head while preserving the existing interactive layout.

From the repository root:

```sh
go run ./cmd/nextgo dev ./examples/metadata-site
go run ./cmd/nextgo build -out /tmp/nextgo-metadata-build ./examples/metadata-site
/tmp/nextgo-metadata-build/server -addr 127.0.0.1:4205
```

Set `NEXTGO_METADATA_BASE_PATH=/metadata-site` and optionally
`NEXTGO_METADATA_ASSET_PREFIX=https://your-cdn.example/static` for a mounted
deployment. Metadata remains at the global application mount, independent of
the asset CDN. The factories receive the configured global base path through
`nextgo.URL`; they never copy incoming Host/cookie/locale values. The manifest
uses ordinary local relative references so its scope and icon resolve within
the same mount. Replace the illustrative `https://example.test` public origin
in `app/config.go` before publishing crawler URLs. The tiny manifest is browser
metadata, not a service worker, offline/install workflow or PWA qualification.

The committed raster assets are original procedural artwork distributed under
the repository's MIT license. Reproduce them with:

```sh
cd examples/metadata-site/app
go generate
```

Native example checks:

```sh
go test -race ./examples/metadata-site/...
```

The prepared four-engine browser fixture requires a production binary built
from this example; it never rebuilds or edits sources itself:

```sh
NEXTGO_METADATA_SERVER_BINARY=/tmp/nextgo-metadata-build/server \
  npx playwright test --config tests/metadata-files.config.mjs
```

It runs the mounted server on 4205 and an asset-only forwarding CDN on 4206,
checking native resource responses, SSR/head defaults, real cross-origin Go WASM,
head replacement/clearing, history and retained counter identity. These are test
tools, not application JavaScript. An optional separately served static export
can be checked by setting `NEXTGO_METADATA_STATIC_URL` to its mounted root URL.
Static hosts own MIME/cache header policy; byte publication does not configure
a CDN. No production/crawler/installability claim follows from these fixtures.
