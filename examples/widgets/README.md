# A scoped chart library from Go

This application adapts pinned uPlot 1.6.32 through Go's `client.Widget` API.
Go loads the trusted script with SRI, constructs the chart, owns its destructor,
handles stop/restart and restores the original SSR fallback. A readable data
table remains outside the canvas container. The explicitly selected upstream library is JavaScript. The classic-script
path needs no application JavaScript wrapper. `/esm` and `/lazy-esm` use a tiny
native ESM adapter to demonstrate default/named exports and a shared live mount
counter through [Go module imports](../../docs/MODULE_IMPORTS.md).

The root page mounts eagerly, `/lazy` uses a lazy Go/WASM component, and
`/about` supports navigation/cleanup checks. `/late` and `/lazy-late` deliberately
simulate 350ms of noncancellable external construction. `/failure` deliberately
fails after a real chart is constructed. These are qualification fixtures, not
recommended application error behavior.

```sh
go run ./cmd/nextgo generate --check ./examples/widgets
CGO_ENABLED=0 go run ./cmd/nextgo build -out ./dist/widgets ./examples/widgets
./dist/widgets/server -addr 127.0.0.1:3000
```

Static publication needs no server for chart interactions:

```sh
go run ./cmd/nextgo export -out ./dist/widgets-static ./examples/widgets
```

The deployment fixture supports `NEXTGO_METADATA_BASE_PATH` and
`NEXTGO_METADATA_ASSET_PREFIX`. Scripts use logical public paths through
`Asset: true`; stylesheet descriptors use `/assets/...` and are scoped by the
framework. The eight exported pages retain useful native HTML without WASM.

The unchanged minified and ESM library files, CSS and its original MIT license are vendored
under `public/vendor`, alongside the application-owned `chart-module-v1.js` adapter. Versioned URLs avoid replacing an existing version's
code. [File hashes and package integrity](../../tests/widgets-vendor.json)
identify their bytes; update the filenames, descriptors and qualification
records together when upgrading. The files were extracted from the npm tarball
after verifying its SHA-512 integrity against registry metadata. This is not an
independent source audit or a guarantee of library safety.

[Lifecycle contract and bounds](../../docs/WIDGETS.md),
[full parity inventory](../../docs/PARITY.md).

The sample also qualifies immutable `Script.Data` settings. A tiny owned
`settings-fixture-v1.js` reads parser-time attributes before Go starts; Go still
owns the UI. Classic uPlot loads receive data attributes before their source is
attached, including through the lazy Go module host. These qualification-only
settings exercise public strings and escaping; they are not a place for secrets.
See [managed scripts](../../docs/SCRIPTS.md).
