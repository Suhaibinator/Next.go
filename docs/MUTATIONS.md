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

## Known application conflicts and uncertain outcomes

A native handler can return `&actions.ConflictError{Message: ..., Fields: ...}`
when it knows that an application concurrency check rejected the requested
write. The router returns HTTP **409**, code **`conflict`**, and the existing
public message/field-error envelope. An empty message uses a safe default asking
the caller to refresh and reconcile. Wrapped private error context is excluded.
Progressive URL-encoded/multipart requests receive escaped 409 HTML instead of a
success redirect; action-view requests receive ordinary failure JSON and never
invoke the successful-view renderer. Returned success cookies and invalidation
selectors are not published when the handler returns a conflict.

Messages and field errors are public browser data. Do not include secrets,
private repository details or unauthorized current values. Authorization errors
win over a joined conflict and hide its details. A conflict intentionally wins
over a joined validation error. This is an explicit application declaration:
returning this type does not itself roll back a write or prove that none occurred.

For example, an application can compare a supplied revision and update a record
inside the same critical section:

```go
package records

import (
    "context"
    "errors"
    "sync"

    "github.com/Suhaibinator/Next.go/actions"
)

type Record struct {
    Revision uint64 `json:"revision"`
    Value    string `json:"value"`
}

type Repository struct {
    mu    sync.Mutex
    value Record
}

func (r *Repository) Update(ctx context.Context, expected uint64, value string) (Record, error) {
    if err := ctx.Err(); err != nil {
        return Record{}, err
    }
    r.mu.Lock()
    defer r.mu.Unlock()
    if err := ctx.Err(); err != nil {
        return Record{}, err
    }
    if expected != r.value.Revision {
        return Record{}, &actions.ConflictError{
            Fields: map[string]string{"revision": "Load the current record and reconcile your edits."},
        }
    }
    next := r.value.Revision + 1
    if next == 0 {
        return Record{}, errors.New("record revision exhausted")
    }
    r.value = Record{Revision: next, Value: value}
    return r.value, nil
}
```

This process-local example shows the atomic comparison and update only. The
handler must authorize the caller's access to the selected repository/record and
validate the value before writing. A real database adapter must combine its
revision predicate and write in the same database transaction. This example
provides no persistence, cross-process isolation, durable idempotency receipt or
framework transaction guarantee. A revision is not an authorization capability.

In a `BindForm` callback, an application that guarantees this conflict means a
rejected write can explicitly map the typed failure to a known form rejection:

```go
var failure *mutation.Error
if errors.As(err, &failure) && failure.Status == 409 && failure.Code == "conflict" {
    return client.FormResult[Record]{}, &client.FormError{
        Message: failure.Message,
        Fields:  client.FieldErrors(failure.Fields),
    }
}
return client.FormResult[Record]{}, err
```

Use this fragment in the callback's error branch. Mapping to `FormError` retains
editable values and reports a known rejection. Return other errors unchanged so
the form's existing failure path remains **Unconfirmed**. Do not classify an
arbitrary 409, transport failure, cancellation or private server failure as a
known rejected write. Refresh/reconcile explicitly; do not automatically resubmit
with the new revision or overwrite the user's edits.

A handler can commit backing data and subsequently return `context.Canceled` or
`context.DeadlineExceeded`. Its response remains HTTP 408/code `request_canceled`,
but the message says the outcome could not be confirmed and asks for
reconciliation before retrying. Neither that message nor request cancellation
establishes rollback. A confirmed `CallAndRefresh` result with a failed view is
still a confirmed mutation: repair the view separately, without another POST.

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

## Opt-in action confirmation and fresh UI

`CallAndRefresh` and `CallMultipartAndRefresh` negotiate a single POST carrying
confirmation followed by a fresh private view. `RefreshResponse[O]` embeds the
ordinary typed response plus `View {Applied, Status, Err}`. Nil top-level error
confirms data even when the view fails; do not resubmit a confirmed mutation to
repair UI. Refresh/reconcile separately. Existing calls remain unchanged.

This bounded action-view subset passes 75 focused cases and 179 affected host
regressions (254 distinct local checks), both SDK native/WASM checks and
host/Linux executable suites. It is not full D10 or production qualification.
Its protocol, invalidation acknowledgement, cookie/draft handling, document
ownership and limits are described in [ACTION_VIEWS.md](ACTION_VIEWS.md).
Native typed results now expose [validated cookie output](NATIVE_COOKIES.md),
and [document-wide FIFO dispatch](MUTATION_DISPATCH.md) coordinates typed JSON,
multipart and combined calls. Both SDK native/WASM and host/Linux deployment
checks pass for these additions; all 55 cookie cases and the corrected 342-case
affected browser matrix pass. A native JavaScript CDP reproduction leaves its
underlying cause open, separately from the functional checks.
The preceding 254-case action-view source does not qualify this phase.
Generic Fetch/server transactions remain independent, and complete login/logout,
session revocation and automatic ordinary-call refresh semantics remain open.

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

Complete action-view form/state and deployment semantics,
automatic form-state mapping, transaction/idempotency adapters, richer auth
integrations and full production/network/resource/cloud qualification remain
open in [the parity inventory](PARITY.md). Next.js documents its server-function
model in [Mutating Data](https://nextjs.org/docs/app/getting-started/mutating-data).
