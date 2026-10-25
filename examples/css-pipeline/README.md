# Optional CSS pipeline

This app builds the same public `style.css` through two explicit version-2
asset-tool stages. It requires Go and no Node installation:

1. `tools/tokens/main.go` validates the three source color tokens and writes
   canonical, lowercase `tokens.json` as a private intermediate. Duplicate,
   unknown or missing fields, invalid colors and inputs over 4 KiB fail before
   output. Whitespace around a color is trimmed.
2. `tools/styles/main.go` reads that file through its declared `tokens`
   dependency alias and renders `styles/main.css`. Only `style.css` is published.

The renderer does not include the original token source in its input snapshot.
Intermediate JSON stays in the disposable build stage; it is absent from the
public result, embedded public assets and static export. `styles/tokens.json`
remains a source file, and neither stage writes generated output into source
`public/`.

From the repository root:

```sh
go run ./cmd/nextgo dev ./examples/css-pipeline
go run ./cmd/nextgo build ./examples/css-pipeline
go run ./cmd/nextgo export -out /tmp/nextgo-css-export ./examples/css-pipeline
```

See [the asset pipeline guide](../../docs/ASSET_PIPELINE.md) for the manifest,
typed Go API, build lifecycle, and optional Sass/PostCSS/Tailwind adapters.
The [dependency-chain contract](../../docs/ASSET_DEPENDENCIES.md) describes
explicit aliases, isolated snapshots, bounded copies and compatibility limits.
Version-1 independent-tool behavior remains covered by the framework fixtures;
this example opts into version 2 and does not claim npm or Next.js source
compatibility.

Change `styles/tokens.json` or `styles/main.css` to update the stylesheet. A bad
token makes the new stage fail and be removed while development retains its
previous successful generation. Repairing the input produces a fresh final CSS
digest. Native example tests cover the actual Go adapters and last-good output;
actual CLI/native/export qualification is separate from those tests.

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
