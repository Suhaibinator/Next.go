# Generated typed Go action references

The CLI discovers explicitly versioned action packages and generates typed
Go/WASM callers plus native registration. Both adapters delegate to the existing
`mutation.Contract`; there is no second transport or application JavaScript.
This subset is implemented and under qualification. No final SDK/browser/
deployment count is claimed for this increment.

## Action package convention

Place each action beneath the application's `actions/` directory:

```text
examples/demo/
  actions/generated/add/
    contract.go
    action.go
  actionrefs/                 generated
  app/generated-actions/
    page.go
```

`contract.go` is shared Go without build constraints. It declares `Input`,
`Output` and exactly one `Contract` initialized through the named mutation
import with two string literals:

```go
package add

import "github.com/Suhaibinator/Next.go/mutation"

type Input struct { Amount int64 `json:"amount"` }
type Output struct { Count int64 `json:"count"` }

var Contract = mutation.Must[Input, Output]("generated/add", "v1")
```

The contract name must match the path beneath `actions/`; nested directories
become slash-separated names. The revision is explicit: changing a Go type
does not automatically change the wire schema. Keep browser-shared DTOs and
initialization public, and do not mutate `Contract` after initialization.

`action.go` must use the exact `//go:build !js` constraint, blank-import
`github.com/Suhaibinator/Next.go/serveronly`, and export these native functions:

```go
func Handle(c *actions.Context, input Input) (mutation.Result[Output], error)
func Options() mutation.BindOptions[Input]
```

`Options` is a startup factory. Its optional `Authorize` runs before decoding;
the handler must still enforce input/data-level authorization and transaction
rules. Its optional `Form` maps native URL-encoded/multipart fields into `Input`;
without a mapper, the contract accepts JSON only. The
[demo implementation](../examples/demo/actions/generated/add/action.go) includes
validation and progressive form mapping for a public, process-local counter.
Production data storage and authentication belong to the application.

Discovery parses Go syntax without executing application code or package
initializers. The ordinary native/WASM compilers then enforce actual handler,
factory and caller types. Both files must use the same importable package;
symlinks, nested action modules, malformed declarations, literal/path mismatches
and generated symbol collisions are rejected. Discovery is not automatic
closure lifting, arbitrary code analysis or secret classification. Normal Go
initializers, configuration and prerender loaders can still run at build/startup
under their existing contracts.

## Generate and call

```sh
go run ./cmd/nextgo generate ./examples/demo
go run ./cmd/nextgo generate --check ./examples/demo
```

The CLI also regenerates references during its ordinary dev/build workflow.
For `generated/add`, the shared `actionrefs` package exposes:

```go
type GeneratedAddInput = add.Input
type GeneratedAddOutput = add.Output
func GeneratedAddPath() string
```

Browser-only named adapters expose:

```go
func GeneratedAdd(context.Context, GeneratedAddInput, ...mutation.CallOptions) (mutation.Response[GeneratedAddOutput], error)
func GeneratedAddMultipart(context.Context, *client.Multipart, ...mutation.CallOptions) (mutation.Response[GeneratedAddOutput], error)
```

For example, in a `js && wasm` component goroutine:

```go
response, err := actionrefs.GeneratedAdd(ctx,
    actionrefs.GeneratedAddInput{Amount: 2})
if err != nil { return err }
count.Text(strconv.FormatInt(response.Data.Count, 10))
```

Tie `ctx` to component cleanup and keep asynchronous work outside browser event
callbacks, as for other Go Fetch calls. Multipart adapters forward an explicitly
built `client.Multipart`; they do not infer a form schema or file policy.

`GeneratedAddPath()` returns `/_nextgo/actions/generated/add/v1` as an
application-relative path. Resolve it through
`nextgo.MustURL(c, actionrefs.GeneratedAddPath())` for server form actions;
do not hardcode mount/locale prefixes. Generated callers inherit the existing
same-origin, revision, byte-limit, cancellation, redirect and error-privacy
checks. A returned redirect is a suggestion: the caller chooses navigation or
refresh. No automatic retry or refreshed/streamed UI is added.

## App-owned native registration

The native adapter exports `actionrefs.Register(*actions.Router)`. The CLI
appends it to `Config.ActionRegistrars` after application `Configure` and before
constructing the app. Do not append it again from `Configure`; duplicate
registrations fail startup. Custom programmatic servers can explicitly pass it:

```go
config.ActionRegistrars = append(config.ActionRegistrars, actionrefs.Register)
app, err := nextgo.NewContext(startupCtx, config)
```

If `Config.Actions` is nil, the app creates a router with app-local logging and
the established process-local tag/path revalidation hooks. If it is a non-nil
`*actions.Router`, the app clones its registrations/options first. Existing
origin/body/privacy/logging/revalidation behavior is preserved. A custom
router's hooks remain its own; generated registration does not overwrite them.
An opaque `http.Handler` in `Config.Actions` remains supported without
registrars, but cannot be combined with them.

Registrars run on an unpublished clone. Duplicate/invalid registration,
descriptor drift or a registrar/factory panic fails startup and discards the
clone, leaving the original router registration table unchanged. Generated
native registration checks every declared name/revision before binding. There
are at most 256 registrar functions per app. Handlers and hook closures retain
their application state and concurrency responsibilities; cloning registration
does not roll back external factory side effects or isolate mutable globals.

Successful `RevalidateTags`/`RevalidatePaths` use that router's existing hooks.
Default hooks are local; shared caches require explicit fallible
`ExpireCache`/`MarkCacheStale` calls in application logic. Cache invalidation is
not a backing-database transaction or a cross-tab/CDN notification mechanism.

## Generated output and interruption

Generation owns `routes/nextgo_gen.go` plus these three action-reference files:

- `actionrefs/nextgo_gen.go`: shared aliases/path helpers and generation anchor.
- `actionrefs/nextgo_gen_js.go`: named browser calls.
- `actionrefs/nextgo_gen_server.go`: native registrar.

Do not edit generated outputs. Handwritten files may coexist only without
package/symbol/ownership conflicts; unowned files are not overwritten. Input
snapshots skip exactly these four known generated targets to avoid regeneration
loops, while the complete build fingerprint includes them. Other custom files
in `routes`/`actionrefs` remain inputs.

Publication preflights outputs and replaces each file atomically, **not the
whole set as one transaction**. The three action files share a generation hash
anchor; mixed old/new adapters fail compilation rather than silently binding a
different contract. If generation is interrupted, rerun it before building.
`--check` writes nothing and reports stale/missing output. A native declaration
whose runtime contract identity drifts fails with a regenerate diagnostic.

## Example, evidence and remaining work

Run the demo and open `/generated-actions`. It demonstrates generated JSON and
multipart calls, a no-JS progressive form, public validation failures, private
error redaction and an explicit refresh of the server snapshot. Its counter is
ephemeral and resets when the process restarts. Existing manually bound actions
continue to work; adopting discovery does not require converting all of them.

Qualification of this new discovery/registration subset is pending. Existing
mutation transport/browser/deployment results qualify their preceding source;
they are not a final result for these generated adapters.

The capability reference is Next.js's
[`use server` file/function boundary](https://nextjs.org/docs/app/api-reference/directives/use-server);
the [official latest-release API](https://api.github.com/repos/vercel/next.js/releases/latest)
still identifies stable v16.4.3. This Go convention does not interpret that
directive or import React source. Inline closures, automatic state/form mapping,
single-response refreshed/streamed UI, durable idempotency/transactions and
complete production/security/network/cloud qualification remain future work.
All 79 capability rows and 11 release gates remain in [the inventory](PARITY.md).
