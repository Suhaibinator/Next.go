# Generated typed Go action references

The CLI discovers explicitly versioned action packages and generates typed
Go/WASM callers plus native registration. Both adapters delegate to the existing
`mutation.Contract`; there is no second transport or application JavaScript.
This subset is implemented with the scoped native, browser, deployment and
measurement evidence below. It is not full action or production-release parity.

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

### Current qualification

Generated-action checks pass **35 cases**: seven host Chromium, seven host WebKit
and 21 Linux Firefox/WebKit/touch cases. Selected affected regressions pass
**138 cases**, and the separate Linux stream-parser suite passes **20 cases**.
These are **193 distinct local browser checks**, with each final suite counted
once; this is not a complete release-browser or remote-CI matrix.

The first Linux action run had 18 passes and three fixture failures; the
corrected fresh-image run passes all 21. The first selected-regression run had
133 passes and five `ECONNREFUSED` failures because its custom configuration
omitted three required fixture servers. After restoring that configuration,
all 138 cases were repeated successfully. Initial failures remain part of the
qualification provenance rather than being counted as passes.

Host Go 1.27.3 full native race/vet, matching core/module WASM tests and explicit
WASM vet pass. Go 1.24.13 full native race/vet, matching core/mutation/module
WASM tests and core/module vet also pass. Its first WASM launch could not create
an OS thread (`newosproc`) before tests; the retained lower-concurrency rerun
passes. Complete host and nonroot read-only Linux HTTP/both mock Lambda
executable suites pass.

The [current production-loopback observation](PERFORMANCE.md#generated-action-artifact-observation-october-25-2026-utc)
records 8,322,265 raw / 2,156,086 gzip-9 WASM bytes. Both size budgets still fail;
its startup/memory observations are not production timing or RSS guarantees.

The 661-file Go/public input manifest is retained under
`.nextgo-test-results/qualification/generated-actions/input-manifest.json`,
SHA-256 `fee9bd739c50a10aed205523fe508f5c1e4a95c7b2efcf837030e2c3768834b0`.
The fresh host server SHA-256 is
`9d8e7c9fdc91eea525b871c2ee4ee00f6a0a66e8726d54dad98aca945a9d20b8`.
Initial failure logs remain alongside the final suite logs.

Remote `f4ecda9` run 40147748543 is terminal: both Go jobs and container passed,
streaming failed, and the broad browser job was cancelled during cross-browser
work after its host npm test step passed. Its failed parser assertion depended
on a 500-ms loader delay; the trace contains live fallback HTML and later
loaded/hydrated content, while the five-second visibility assertion missed the
transient state. Exact paint/scheduling cause is unproven. A real HTTP gate
passes two host and four Linux checks on the same historical artifact without
Go changes; that is a test-timing correction, not a runtime fix or whole remote
pass.

At the recorded **October 25, 2026, 01:22 UTC** snapshot, remote `624a19c`
run 40149720819 had passing native SDK jobs and a terminal failed streaming
job: the generated-action step had 24 passes and four
manual-coexistence failures, one per engine. Earlier steps passed 20 parser,
16 admission, 32 partial-region, 40 shell and 40 viewport cases in that job.
Container and broad browser jobs were unfinished at that snapshot. Subsequent
workflow results remain separate. The 35 corrected local action checks do not
make that historical remote step pass; no all-green CI result is claimed.
Previous mutation transport/browser/deployment matrices remain historical;
no release gate closes with this subset.

The capability reference is Next.js's
[`use server` file/function boundary](https://nextjs.org/docs/app/api-reference/directives/use-server);
the [official latest-release API](https://api.github.com/repos/vercel/next.js/releases/latest)
still identifies stable v16.4.3. This Go convention does not interpret that
directive or import React source. Inline closures, automatic state/form mapping,
single-response refreshed/streamed UI, durable idempotency/transactions and
complete production/security/network/cloud qualification remain future work.
All 79 capability rows and 11 release gates remain in [the inventory](PARITY.md).
