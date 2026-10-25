# Typed Go mutation contracts

`mutation.Contract[Input, Output]` shares a public Go JSON contract between a
native handler and a Go/WASM caller. It removes repeated JSON transport code
while keeping application handlers out of the browser import graph. It does not
make a public endpoint private or infer authentication, transaction semantics,
idempotency or business validation.

## Shared definition

Place DTOs and contracts outside `app/`, for example in `mutations/contracts.go`:

```go
package mutations

import "github.com/Suhaibinator/Next.go/mutation"

type AddInput struct {
    Amount int64 `json:"amount"`
}
type AddOutput struct {
    Applied int64 `json:"applied"`
    Count   int64 `json:"count"`
}

var Add = mutation.Must[AddInput, AddOutput]("cart/add", "v1")
```

`New` returns a contract and error; `Must` panics on an invalid definition. Names
use slash-separated ASCII identifier segments and have at most 256 bytes.
Revisions are single identifier segments with at most 64 bytes. Definitions are
immutable values with `Name`, `Revision`, `Key` and `Path` accessors. The example
key is `cart/add/v1` and its application-relative path is
`/_nextgo/actions/cart/add/v1`.

Keep runtime dependencies and secrets in native files, using `//go:build !js`
and `serveronly` markers where appropriate. `Bind` exists only in native builds;
`Call` exists only for `js && wasm`. The shared definition is public browser data.
Compiler fixtures build both targets and check that native handlers, `actions`,
`serveronly` and `net/http` stay out of the shared caller's WASM graph.

## Native binding

```go
//go:build !js

mutations.Add.Bind(router,
    func(ctx *actions.Context, input mutations.AddInput) (mutation.Result[mutations.AddOutput], error) {
        // Authenticate/authorize this request and validate domain input before
        // changing persistent data. A contract identifier grants no access.
        result, err := repository.Add(ctx.Context, input.Amount)
        if err != nil {
            return mutation.Result[mutations.AddOutput]{}, err
        }
        return mutation.Result[mutations.AddOutput]{
            Data: mutations.AddOutput{Applied: result.Applied, Count: result.Count},
            Redirect: "/cart",
            RevalidatePaths: []string{"/cart"},
        }, nil
    },
)
```

`Bind` registers through the existing `actions.Router`. Invalid or duplicate
bindings panic, matching `Router.Handle`. Handler input/output mismatches fail
Go compilation. JSON decoding rejects unknown fields and extra values; domain
validation remains in the handler. JSON types must be serializable. A nil
output is encoded as an explicit `data: null`; the caller must declare a nullable
Go output type to accept it. Concrete outputs cannot use a root null result.

The router still enforces POST, trusted-origin policy, bounded request bodies,
generic internal errors and safe local redirects. It encodes the response before
running successful cache effects. A handler may already have committed data
when serialization or response delivery fails: use an application transaction
and a serializable output contract rather than assuming rollback.

`BindOptions[Input].Authorize` can reject a request before input decoding. It
does not replace data-level authorization. The router bounds/reads the request
body before invoking a binding, so this hook is not a substitute for body limits.

## Go/WASM calling

```go
//go:build js && wasm

go func() { // Never block a synchronous browser event callback on transport.
    result, err := mutations.Add.Call(ctx, mutations.AddInput{Amount: 2})
    if err != nil {
        var failure *mutation.Error
        if errors.As(err, &failure) {
            // failure.Status, Code, Message and Fields are public server data.
            showFieldErrors(failure.Fields)
        }
        showError(err)
        return
    }
    showConfirmedValue(result.Data.Count)
    // result.Redirect is an optional suggestion. Call does not navigate.
}()
```

The input argument and `Response[Output].Data` are statically typed. The default
input and response limits are each 64 KiB; a single optional `CallOptions` can
raise or lower them with positive `MaxInputBytes`/`MaxResponseBytes` values.
Input marshaling happens before the encoded byte limit check. These bounds do
not bound arbitrary work performed by application marshal/unmarshal methods.

`Call` resolves its path through the active deployment/locale configuration and
the real window location. An HTML `<base>` element cannot move the request to
another origin. It sends a JSON POST with same-origin credentials, no-store
caching and redirect rejection. A 3xx response cannot forward the request body
to another endpoint. Framework action dispatch and settlement invalidate Go
navigation/prefetch reuse, including failures.

Cancellation aborts transport and preserves `errors.Is` for `context.Canceled`
or `context.DeadlineExceeded`. Other transport errors preserve their cause.
`client.ErrResponseTooLarge` can be recognized through wrapping. A failed or
aborted request does not establish whether the mutation committed; reconcile or
use an idempotency policy before retrying. No automatic retries are performed.

## Protocol and revision checks

Action responses advertise `X-Nextgo-Action-Protocol: 1`. Typed endpoints also
advertise `X-Nextgo-Action-Revision` with their definition's revision. Call sends
both request headers. Unsupported protocol or mismatched revision requests
fail with 409 before invoking the handler. A missing revision endpoint returns
404 before mutation. Progressive forms can omit these headers and use their
explicit versioned action URL.

Successful calls require an exact final endpoint URL, JSON content type,
supported protocol, matching revision, HTTP 200 and a present typed data field.
The decoder rejects unknown response/DTO fields, duplicate JSON keys, extra JSON
values, nesting beyond 64 levels, incompatible data types and unsafe redirect
metadata. Go's JSON field-name/custom-codec behavior otherwise applies. Nullable
outputs use pointers, interfaces, maps or slices. Custom codecs remain an
application responsibility.

Recognized 4xx/5xx failures require a structured public error envelope and never
produce success data. `mutation.Error` exposes status/code/message/field errors;
protocol failures wrap `mutation.ErrProtocol`. Redirect metadata must remain a
safe application-absolute URL. Call returns it as data, allowing the component
to coordinate optimistic settlement and any navigation explicitly.

Revisions are explicit compatibility boundaries. Change the revision when input,
output or mutation semantics become incompatible; keep older registrations only
while their contracts remain supported. Go types do not detect an accidentally
unbumped semantic change or provide deployment-wide exactly-once behavior.
Multi-release asset/action retention and real-cloud rollout qualification remain
open production work.

## Progressive forms and optimistic state

Set `BindOptions[Input].Form` to map URL-encoded/multipart body values to the
typed input. The mapper returns public validation errors when appropriate.
Without it the binding accepts JSON only. Generate form action attributes with
`nextgo.MustURL(ctx, mutations.Add.Path())`; successful local redirects receive
303 and fresh SSR. Native and browser tests cover root, mount, slash and locale
scopes without JavaScript.

The [demo contract](../examples/demo/mutations/contracts.go),
[native binding](../examples/demo/app/config.go) and
[Go island](../examples/demo/components/optimistic/client_js.go) integrate this
transport with [optimistic state](OPTIMISTIC.md). The server returns a canonical
typed delta and count; the island confirms the delta or rejects its local intent.
Its progressive form also makes the sample's server dependency visible to the
static exporter, which rejects publication instead of shipping broken controls.

## Verification and remaining work

Tests cover native binding, authorization before custom decoding, validation
without mutation, version misses/mismatches, serialization failure privacy,
nullable data, progressive redirects, native/WASM compiler errors and import
separation. WASM/browser tests exercise request options, byte limits,
cancellation, protocol/schema poisoning, endpoint origin, redirect rejection,
optimistic cleanup, deployment prefixes and locale/domain scopes. Generated
HTTP and buffered/streaming mock Lambda fixtures check typed metadata and data.

This generic API provides typed calls directly. Optional
[generated action discovery](GENERATED_ACTIONS.md) now emits named browser calls,
shared aliases/path helpers and native registration from explicit action
packages; it delegates to this same transport. The new subset has 193 distinct
local browser checks passing (35 generated actions, 138 affected regressions,
20 Linux parser). Native race/vet and matching core/module WASM tests/vet pass
on both SDKs, alongside host/Linux executable suites. The bundle-size budgets
still fail, and remote CI retains its separately recorded action failures. See
the generated-action qualification for failed-fixture provenance.

Single-response refreshed/streamed UI,
automatic form-state mapping, transaction/idempotency adapters, richer auth
integrations and full production/network/resource/cloud qualification remain
open in [the parity inventory](PARITY.md). Next.js documents its server-function
model in [Mutating Data](https://nextjs.org/docs/app/getting-started/mutating-data).
