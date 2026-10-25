# Optional CSS pipeline

This app builds `style.css` from a CSS template and validated JSON color tokens,
using a small Go executable. It requires Go and no Node installation.

From the repository root:

```sh
go run ./cmd/nextgo dev ./examples/css-pipeline
go run ./cmd/nextgo build ./examples/css-pipeline
go run ./cmd/nextgo export -out /tmp/nextgo-css-export ./examples/css-pipeline
```

See [the asset pipeline guide](../../docs/ASSET_PIPELINE.md) for the manifest,
typed Go API, build lifecycle, and optional Sass/PostCSS/Tailwind adapters.
Change `styles/tokens.json` to update the palette. An invalid color makes the
new build fail while development continues serving its previous successful
generation.

The page links its generated stylesheet through `Page.Resources`. Because its
filename stays `style.css`, `app/config.go` applies `Cache-Control: no-cache,
must-revalidate` to `/assets/style.css`. Configure the same policy on a static
export host; a changed runtime build ID does not hash public stylesheet URLs.
The exporter sets `config.Phase` to `nextgo.PhaseStaticExport` before calling
`Configure`, so this example omits its native header rule during export. Native
serving retains that rule; attempting static publication of the native
configuration refuses because static-host header publication is not implemented.
Before serving the export, set `Cache-Control: no-cache, must-revalidate` for
`assets/style.css` at the host/CDN, or adapt the tool and page to use
content-hashed output filenames. The exported files do not configure host
response headers.

See [configuration phases](../../docs/CONFIG_PHASES.md) for native startup and
snapshot-isolation behavior.
