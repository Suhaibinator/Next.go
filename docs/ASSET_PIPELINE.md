# Optional CSS and asset build tools

An application may add `nextgo.assets.json` beside its `app/` directory. Normal
Go applications do not need this file, Node, or a JavaScript package manager.
The pipeline runs during `dev`, `build`, and `export`; generated files become
ordinary embedded public assets in the successful build generation.

The [Go design-token example](../examples/css-pipeline) is executable without
Node. It generates a stylesheet from a CSS template and JSON palette. Sass,
PostCSS, and Tailwind can instead run as explicitly configured external tools;
the framework does not install or bundle these tools.

## Manifest

```json
{
  "version": 1,
  "tools": [
    {
      "name": "Go design tokens",
      "command": ["go", "-C", "{project}", "run", "{inputs}/tools/styles/main.go"],
      "inputs": ["styles", "tools/styles"],
      "outputs": ["style.css"],
      "timeoutSeconds": 120
    }
  ]
}
```

Each command is an argv array. The framework never joins it into a shell
command: pipes, redirection, glob expansion, and command substitution do not
happen. A tool must exit after one build. Do not pass its `--watch` option;
Next.go owns development watching and rebuild publication.

| Field | Meaning |
| --- | --- |
| `name` | Unique diagnostic name, up to 128 characters. |
| `command` | Executable followed by literal arguments. Executables without a slash resolve through `PATH`. |
| `inputs` | Exact relative file or directory names to snapshot and watch. Directories include their descendants, additions, and removals. No glob syntax. |
| `outputs` | Exact relative public file names. Every file written below the output directory must be declared; every declared file must exist. |
| `env` | Optional string environment overrides, added to the inherited process environment. `NEXTGO_ASSET_*` names are reserved. |
| `timeoutSeconds` | Per-tool deadline, default 120 seconds and maximum 600 seconds. The parent build context can cancel earlier. |

Commands run in a private working directory. These substitutions are available
in argv and configured environment values:

| Substitution | Environment variable | Directory |
| --- | --- | --- |
| `{inputs}` | `NEXTGO_ASSET_INPUT_DIR` | A snapshot of the tool's declared inputs, preserving relative paths. |
| `{outputs}` | `NEXTGO_ASSET_OUTPUT_DIR` | An initially empty directory for generated files. |
| `{project}` | `NEXTGO_ASSET_PROJECT_DIR` | The original application directory, useful for installed executables or configuration resolution. |

Inputs are copied independently for each tool. Tools cannot consume another
tool's output as an implicit dependency. Implement a multi-step transformation
inside one adapter executable when, for example, Sass output must pass through
PostCSS. Write only final declared files in `{outputs}` and keep intermediates
in your adapter's working directory.

## Typed Go hooks

The build-time `buildassets` package exposes typed `Pipeline`, `Tool`,
`Workspace`, and `Options` values. A Go caller may supply
`Tool.Transform func(context.Context, buildassets.Workspace) error` instead of
`Tool.Command` and call `buildassets.Run`. Transforms must observe their context;
an in-process function cannot be forcibly terminated.

For CLI integration, write a small Go executable and configure `go run` as in
the example. `buildassets.WorkspaceFromEnv` supplies the typed workspace:

```go
w, err := buildassets.WorkspaceFromEnv()
if err != nil {
    return err
}
source, err := os.ReadFile(filepath.Join(w.Inputs, "styles", "main.css"))
if err != nil {
    return err
}
return os.WriteFile(filepath.Join(w.Outputs, "style.css"), source, 0644)
```

Use this code inside a function that returns an error, and propagate failures
to a nonzero executable exit status. The example's `go -C {project}` selects
the application's module even when staging outside its directory tree.
`buildassets.Run` returns a staged public
directory and a digest of its file names and bytes. `buildassets.Merge` merges
into a disposable build public directory after collision preflight. These APIs
do not publish a generation themselves. The package is unavailable under the
`js` build target so build tools stay out of the browser import graph.

## Sass adapter

With an installed Dart Sass executable on `PATH`, an entry can be:

```json
{
  "name": "Sass",
  "command": ["sass", "--no-source-map", "{inputs}/styles/main.scss", "{outputs}/style.css"],
  "inputs": ["styles"],
  "outputs": ["style.css"]
}
```

Keep imported Sass partials under the declared input directory, or add their
directories and explicit load paths. For a project-local installation, replace
`sass` with `{project}/node_modules/.bin/sass`. Dart Sass documents these argv
options in its [CLI guide](https://sass-lang.com/documentation/cli/dart-sass/).
If you enable source maps, declare the map file as another public output and
review the source paths and content that map will publish.

## PostCSS adapter

With `postcss`, `postcss-cli`, and your plugins installed locally:

```json
{
  "name": "PostCSS",
  "command": ["{project}/node_modules/.bin/postcss", "{inputs}/styles/main.css", "--config", "{project}", "--no-map", "-o", "{outputs}/style.css"],
  "inputs": ["styles", "postcss.config.cjs", "package.json", "package-lock.json"],
  "outputs": ["style.css"],
  "env": {"NODE_ENV": "production"}
}
```

This example resolves plugin modules from the original project's configuration
directory. Adapt `inputs` to the actual configuration and lockfile you use;
listed files must exist. Declare every additional file read by plugins. The
official [PostCSS CLI documentation](https://github.com/postcss/postcss-cli)
describes `--config` as a directory and `--no-map` as disabling inline maps.

## Tailwind adapter

For an installed Tailwind CLI, configure its normal one-shot input/output argv:

```json
{
  "name": "Tailwind",
  "command": ["{project}/node_modules/.bin/tailwindcss", "-i", "{project}/styles/tailwind.css", "-o", "{outputs}/style.css"],
  "inputs": ["styles", "app", "package.json", "package-lock.json"],
  "outputs": ["style.css"]
}
```

Tailwind v4's CLI package is `@tailwindcss/cli`. Its
[installation guide](https://tailwindcss.com/docs/installation/tailwind-cli)
also provides a standalone executable option. Change the executable argument
for your chosen installation. Configure source detection in your Tailwind CSS
entry and declare every source directory it scans, including `components` if
used. This example reads the original CSS entry so its package imports resolve
relative to the installed project dependencies. Source changes during a
framework build cause that generation to be rejected and rebuilt.

These external-tool entries are adapter examples based on upstream interfaces.
The repository tests exercise fixture executables and the Go transform, not
installed Sass, PostCSS, or Tailwind distributions. Pin your tool dependencies
and validate your selected versions. Installed dependency trees are not copied
or automatically watched; declare their lockfile and restart/rebuild after
changing installed packages without a lockfile edit.

## Publication and limits

Asset tools run before compilation finishes, with results staged only inside
the new `.nextgo/build-*` generation. Generated bytes participate in its build
ID. After all commands and validations succeed, outputs merge with that
generation's copied public assets. They are then served, embedded, and exported
through the existing asset path handling, including base paths and asset
prefixes. Generated files are never written to the application's `public/`
directory, so normal outputs do not cause an endless development rebuild loop.
On failure the new stage is removed and development retains the last successful
generation. A stale file from a previous generation cannot satisfy a missing
output declaration.

Public asset URLs retain their declared filenames: changing the runtime build
ID does not turn `/assets/style.css` into a content-addressed URL. Applications
must either generate content-hashed filenames and reference those names, or
configure an appropriate cache-revalidation policy for stable filenames. The
example declares `assets.Stylesheet("/assets/style.css")` in its page resources.
Head rendering resolves that logical asset path through the configured base
path or CDN prefix. Do not pre-resolve page resource URLs with `MustAssetURL`:
the resource validator expects a local path. The example sets this existing
header rule:

```go
config.HeaderRules = append(config.HeaderRules, nextgo.HeaderRule{
    Source: "/assets/style.css",
    Headers: []nextgo.HeaderValue{{
        Name: "Cache-Control", Value: "no-cache, must-revalidate",
    }},
})
```

This policy requires browser revalidation before reusing the stable CSS URL.
Native response header rules cause `StaticExportConfig` and `nextgo export`
to refuse static publication: static-host header publication is not implemented.
The exporter selects the typed native `PhaseStaticExport` before calling the
application's unchanged `Configure(*nextgo.Config)` hook. The example uses that
phase to omit its native-only stylesheet header rule:

```sh
go run ./cmd/nextgo export -out /tmp/nextgo-css-export ./examples/css-pipeline
```

This does not add response headers to exported files. Before serving the export, configure
`Cache-Control: no-cache, must-revalidate` for `assets/style.css` at the static
host/CDN, or adapt the tool and resource references to content-hashed filenames.
Ordinary development and production serving keep the stylesheet revalidation
rule. Other applications choose their own phase-specific configuration; the
framework continues to reject unsupported native header policies. See
[configuration phases](CONFIG_PHASES.md) for the startup contract and private
snapshot/cache isolation.

Input and output names must be clean relative slash paths. Symlinks, special
files, traversal, private directories, `.env` variants, credential filenames,
and private-key extensions are rejected. Generated outputs cannot contain
hidden path segments or use the reserved `_nextgo` prefix. Public/generated
collisions, path-prefix conflicts, and case-only name conflicts fail the build
instead of overwriting a public file. Do not declare the entire project root
as an input; declare the source and configuration directories needed by each
tool.

Limits are 64 tools, 256 input declarations and 256 output declarations per
tool, a 1 MiB manifest, 64 MiB per asset file, 256 MiB per input snapshot and
combined generated output, and 10,000 files per snapshot/output tree. Combined
stdout/stderr is capped at 64 KiB per command with a truncation marker.
Commands receive context cancellation and a bounded wait for inherited pipes.

Build commands and Go transforms are trusted executable code, with the current
user's privileges and inherited environment. Staging and path checks protect
what the framework copies and publishes; they are not an operating-system
sandbox for the executable. Keep secret values out of tool diagnostics and
public source maps. Declare all files a tool reads for correct watching and
reproducible builds.
