# Go workspaces and local build inputs

Next.go uses Go modules and `go.work`; applications do not need a separate package manager or dependency resolver. The CLI selects the main module that actually owns the application's nearest `go.mod`. A parent workspace module cannot capture an unlisted nested module. Root applications and applications in subdirectories retain the same route/component conventions.

The [multi-module example](../examples/workspace/README.md) has a site module, a shared module and a workspace that also selects the local framework. Its server page and Go/WASM counter share ordinary Go constants; a native-only repository embeds a text file. Build the application once and deploy its executable without the workspace or source tree.

## Resolution and watches

The CLI asks the Go tool for the selected module/environment and JSON representations of module/workspace files. It does not modify those files, run `go work sync`, tidy dependencies or replace Go's resolution rules. Active workspace modules, local replacements from main modules and workspace replacement targets enter the input snapshot. Replacements declared in dependency modules are not followed, matching Go's rule that those directives do not control the main build. Alternate module files selected through `GOFLAGS=-modfile=...` and explicit `GOWORK` paths are respected. `GOWORK=off` disables workspace selection. Relative replacements stay anchored to the owning module root, including when an alternate module file is stored elsewhere. A symlinked workspace file retains its selected workspace-directory base.

Development watches Go source, module/sum files, the active workspace/sum file, public assets and matched `go:embed` inputs in these local roots. Source additions, deletions and changed contents trigger builds. Symlinked Go source files include both their link and target contents; symbolic links in public assets remain rejected. Embed directives are read as Go comment tokens, so directive-looking text inside strings is inert. Patterns support quoted filenames, directory expansion, globbing and `all:` hidden-file behavior. The Go compiler remains responsible for validating declarations, target constraints, invalid patterns and package/module ownership.

Snapshots are conservative across local module source trees, rather than a minimal imported-package graph. Unused Go files or unused local replacement targets can trigger additional rebuilds. The selected native/browser compiler graphs still determine what reaches each executable. Versioned module-cache entries are treated as immutable and are not polled; change a dependency version/checksum or use an explicit local replacement to develop it.

A failed workspace/module parse retains the previous watch roots and manifest paths. The last successful server keeps serving. Fixing an external module or workspace file retriggers the build without requiring an application edit. Invalid generations are not published, and inputs are checked again after discovery/generation and compilation. Changes during a build discard it and require the normal retry cycle. Prepare dependency sums before a production build; if the Go compiler updates a sum file, rerun after reviewing that change. Existing generation retention and browser reload/error feedback apply to workspace builds as well.

## Embedded data and limits

A changed embedded file participates in the build identity even when no `.go` file changed. Build-only data belongs in this snapshot. Runtime environment files remain outside the snapshot; `.env` and `.env.*` files, including case variants, are rejected when an embed pattern would include them. Load configuration through the [environment API](ENVIRONMENT.md), and keep secrets out of source code, public files and compile-time embedded assets. Arbitrary renamed secrets cannot be classified automatically.

Input hashing streams regular files with cancellation checks. The current limits are 256 roots in a resolved plan, 256 workspace/replacement entries per parsed manifest, 100,000 input records, 256 MiB per file, 2 GiB total selected file bytes and 10,000 embed patterns. Metadata command output is capped at 2 MiB and each Go metadata command has a ten-second deadline; build snapshots use the parent cancellation context with a thirty-second deadline. Go source is read separately to scan directives. These are admission limits, not a measured peak-memory or large-repository performance claim.

Generated `.nextgo` output, Git metadata, dependency/output directories and ordinary hidden directories remain excluded from source polling. Explicit embed patterns may include otherwise excluded data, as Go permits; public assets retain their existing hidden-file rules. Workspace and local dependency sources are included in runtime build identities, but native server packages and loaders do not become browser imports. Native-only files should use build constraints or a blank `serveronly` import, as before.

## Stable compiler overlays and discovery scope

The compiler now uses a private per-build overlay with stable virtual generated main-package/source
paths, while preserving the owning module, Main and VCS metadata in Go build
information. A source-file target that drops that provenance is not the accepted
contract. Focused same-input provenance/reproducibility fixtures pass on both
SDKs; this is not a general production-binary reproducibility claim.

Caller `GOFLAGS=-overlay=...` mappings must be merged with framework mappings,
rather than silently replaced. Overlay JSON and backing files participate in
input/watch tracking; paths follow the Go command's working-directory rules,
not the directory containing the JSON file. Reserved generated-path collisions
fail closed. Scoped tests and host/Linux deployment cover this integration;
complete release qualification remains open. Do not infer support for every overlay or
workspace combination.

Route/action discovery still scans physical files and directory declarations.
Overlaid bodies of existing files are supported by compilation, but virtual-only
routes/actions and structural reshaping of action contracts are not discovered
through the overlay. Keep structural declarations physical and rerun generation;
compiler overlays do not replace AST discovery with a virtual filesystem.

The scoped fixtures compare fixed VCS/inputs across sequential/concurrent
physical generations, check Main/module/VCS provenance and all runtime assets,
and exercise the original prerender snapshot. They pass on Go 1.27.3 and
Go 1.24.13 with Git present. These are controlled compiler fixtures, not
cross-checkout/OS/toolchain, production-age or immutable-release proof.

The goal addresses timestamped generated-main-path/build-ID churn in WASM.
Prerendered native artifacts also contain intentional generation ages, so
stable compiler paths do not imply byte-identical native binaries or complete
checkout/toolchain-independent release reproducibility. G07 remains open.

## Verification and remaining work

[Native tests](../internal/project/inputs_test.go) cover workspace module selection, nested ownership, local/alternate-file replacements, missing targets, error recovery, symlink inputs, embed patterns/exclusions, cancellation and file limits. A real native/WASM compile fixture proves that sibling server-only embedded data stays outside the browser binary. [Browser tests](../tests/workspace.spec.mjs) compare initial SSR node identity, run the sibling-module Go counter, and navigate/history-traverse without reloading the document. [Development checks](../tests/workspace-dev.spec.mjs) edit a sibling Go file and embedded text, retain the last working browser state during errors and recover from broken manifests. [Executable tests](../tests/deployment/workspace_test.go) run the source-independent workspace sample over native HTTP and both mock Lambda response modes; the Linux fixture runs non-root with a read-only filesystem.

A minimal target/import graph, workspace planning cache, module-proxy/registry qualification, vendoring/Cgo/non-Go tool inputs, symlinked package-directory traversal, portable Windows behavior, reproducible checkout-independent identities and large-monorepo performance/soak qualification remain open. This feature does not establish those release gates or full framework maturity. The [parity inventory](PARITY.md) retains them.

The conventions follow the official [Go module/workspace reference](https://go.dev/ref/mod) and [embed reference](https://pkg.go.dev/embed). No alternate module grammar is introduced.
