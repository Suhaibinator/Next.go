# Environment and public runtime configuration

Next.go reads server configuration in ordinary Go. Environment files are a bounded convenience for application startup. Public browser settings require an explicit map; a variable name or prefix never publishes a value automatically.

## Startup and precedence

`go run ./cmd/nextgo dev ./examples/demo` loads the application's environment files in development mode. Existing process variables, including explicitly empty values, win. Files are considered in this order, with the first definition winning across files:

1. `.env.<mode>.local`
2. `.env.local` (skipped in test mode)
3. `.env.<mode>`
4. `.env`

Modes are `development`, `production`, and `test`. Duplicate assignments inside a single file use the last definition. `.env.example` is never loaded. Keep real files out of version control. The example contains only safe placeholders.

Production binaries use their process environment by default. They do not search their working directory for files. Explicitly opt into a mounted configuration directory:

```sh
./dist/demo/server -addr :3000 -env-dir /run/application-config
# Equivalent directory selection:
NEXTGO_ENV_DIR=/run/application-config ./dist/demo/server
```

`-env-mode` defaults to `production`. The directory must exist; missing individual files are ignored. The same flags work when the executable runs through the Lambda runtime adapter. Ordinary platform environment variables need no files or flags. A warm serverless process retains its startup snapshot; change deployment configuration and restart/replace processes to apply updates.

Generated entrypoints call `environment.Apply` after parsing flags, before `app.Configure`. Read file-derived settings in `Configure`, loaders or handlers. Go package initialization has already run. Files cannot retroactively change package initialization or compiler/runtime options read before `main`. The CLI never loads development files into compiler subprocesses.

Applications with custom entrypoints may call `environment.Apply(dir, environment.Production)` before constructing the app. For tooling, `environment.Load(dir, mode, base)` returns an owned, sorted environment snapshot without changing the process. Do not call `Apply` concurrently with request handling or use it as a per-request configuration mechanism.

## Explicit public settings

```go
// app/config.go; native only
//go:build !js

package app

import (
    "os"
    nextgo "github.com/Suhaibinator/Next.go"
)

func Configure(config *nextgo.Config) {
    config.Public = map[string]string{
        "service.label": os.Getenv("SERVICE_PUBLIC_LABEL"),
    }
}
```

`nextgo.New` validates and copies the map. A server page can read the frozen setting with `nextgo.PublicValue(c.Context, "service.label")`; middleware uses the request context. A browser component calls `client.PublicValue("service.label")`. Both return `(string, bool)`, distinguishing a missing key from an empty value.

Public settings are JSON in the escaped document navigation attribute. Values remain runtime data, outside the WASM asset: one binary and browser bundle can serve different deployment settings. Configuration identity includes the public map. Navigation between incompatible replicas uses the existing full-document recovery path; it does not mix their settings. A client component reads its frozen startup snapshot, not later edits to the DOM attribute. Public values are limited to 64 keys, 128 bytes per key, 1 KiB per value and 16 KiB total, with UTF-8 validation and no NUL bytes.

**Every public value is visible to the browser and to anyone who can fetch the page.** Never put signing keys, tokens, passwords or private connection strings in this map or island props. `NEXT_PUBLIC_*` and similar names have no special behavior. The framework cannot prevent application code from explicitly rendering a secret, including through a loader, metadata, error message or `Config.Public`.

The sample `/environment` page verifies matching SSR props, DOM text and the browser runtime value before enabling its Go button. The initial DOM survives hydration. Set `NEXTGO_DEMO_PUBLIC_MESSAGE` to change its message.

## File syntax and limits

Supported syntax includes `NAME=value`, optional `export`, whitespace, `#` comments, CRLF, a leading UTF-8 BOM, single/double quotes and quoted multiline values. Quote values containing `#`. Unquoted values trim surrounding whitespace; quoted values preserve it. Single quotes are literal. Double quotes recognize `\n`, `\r`, `\t`, `\"`, `\\` and `\$`. Unquoted `\$` also produces a literal dollar sign.

Unquoted and double-quoted values expand `$NAME` and `${NAME}` against the selected environment snapshot, including forward references. Undefined names become empty strings. Explicit process values are literal and are never re-expanded. Cycles and more than 16 reference edges are rejected. Shell operators, default expressions such as `${NAME:-default}`, command execution, includes and interpolation functions are outside this syntax. `$(...)` remains text and is never executed.

Files must be regular files (symlinks to regular files are permitted for projected configuration volumes). Each file is limited to 256 KiB, with at most 256 selected file-derived keys (and 256 keys in any one file), 64 KiB per expanded value and 1 MiB of expanded file-derived values in aggregate. Names use ASCII letters, digits and underscores, starting with a letter or underscore, up to 128 bytes. Parser errors identify a filename/line where available without copying assignment text or values. Loading completes before process changes are applied.

## Development and static export

Development watches only files relevant to development mode. Environment-only edits reuse the last compiled binary, restart it with the new settings and trigger one browser reload after successful startup. Source changes still rebuild the native server and WASM. The last successful process continues serving after a parse or startup error. Removing a file restores the next value in precedence. Environment content and its private watch digest never enter the public asset build ID. An independent random development generation token also detects missed reload events when environment-only changes retain the same WASM URL. Development retains the previous successful asset generations as before.

`nextgo export` reads production-mode files once from the application directory and uses that snapshot for configuration, route enumeration and rendering. The CLI process remains unchanged. Static pages contain their explicitly published settings; changing them requires a new export. Neither build nor export embeds application environment files. Files named `.env` or beginning `.env.` are rejected in `public/`, including nested directories and case variants; the embedded asset handler also refuses direct requests for those names. `.well-known` remains supported. These checks cannot classify arbitrary renamed secret files: keep all private files outside `public/`.

The precedence convention follows the [Next.js environment guide](https://nextjs.org/docs/app/guides/environment-variables). Next.go deliberately uses an explicit runtime public map instead of JavaScript build-time substitution. There is no secret manager integration, encrypted configuration store, hot production reload, Go constant substitution or automatic classification of secrets.


## Verification

The native [loader tests](../environment/environment_test.go) cover precedence, expansion, parsing, failure redaction, ownership, startup application and budgets. [Framework tests](../public_test.go) cover public-map freezing, escaping, request access and environment asset denial. [Export integration](../cmd/nextgo/environment_test.go) compiles an independent application, loads its production files and checks every exported file for a synthetic private value. [Browser tests](../tests/environment.spec.mjs) retain initial DOM references, interact in Go, navigate, traverse history and verify that DOM attribute changes do not mutate the frozen snapshot. [Development tests](../tests/dev.spec.mjs) prove unchanged asset IDs, independent generation/reconnect checks and last-good recovery. [Executable tests](../tests/deployment/environment_test.go) cover explicit production directories, process overrides and HTTP plus both mock Lambda modes outside the checkout, including a non-root/read-only Linux image. These tests do not establish real-cloud or complete production maturity; remaining gates are tracked in [the parity inventory](PARITY.md).
