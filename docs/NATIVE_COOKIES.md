# Native action cookies and the signed-session example

This phase is **under implementation/qualification**. Native cookie output and
a signed-session example are present; focused checks exist, but no final
application-build, SDK/browser/deployment matrix is claimed for this phase.
Previous action-view results describe the preceding source.

## Return cookies from a native action

Native action results expose `Cookies []*http.Cookie` with `json:"-"`:

```go
func Handle(c *actions.Context, input Input) (mutation.Result[Output], error) {
    path, err := nextgo.URL(c.Context, "/")
    if err != nil { return mutation.Result[Output]{}, err }
    // Authenticate/authorize and commit application data before returning.
    return mutation.Result[Output]{
        Data: Output{Role: "reader"},
        Cookies: []*http.Cookie{{
            Name: "app_session", Value: signedValue,
            Path: path, HttpOnly: true, Secure: true,
            SameSite: http.SameSiteLaxMode,
        }},
    }, nil
}
```

`actions.Result` supports the same field for manually registered handlers.
These cookies are native HTTP controls, never part of typed JSON `Data`.
The typed binding freezes cookie structs and `Unparsed` entries before invoking
custom data marshaling; the router validates/finalizes the whole cookie set
before publication. Application code must not also serialize credentials in
its own data fields.

Use an explicit public mount path derived from `nextgo.URL`. An empty action
cookie `Path` keeps the browser's standard **action-request** default path,
which may not cover the page being authenticated. It is not automatically
rewritten to the source page. Page-origin cookie defaults in combined views
have a separate [normalization contract](ACTION_VIEWS.md#cookies-draft-state-and-private-output).

## Validation and publication

One result allows at most **32 cookies**, **8 KiB per serialized Set-Cookie
value** and **32 KiB total serialized cookie values**. Validation is atomic for
that result. It rejects nil/invalid cookies, control characters, non-rooted or
backslash-containing explicit paths, and duplicate name/domain/path tuples.
Domain identity normalizes case and a leading dot. Standard `http.Cookie.Valid`
checks apply, plus these explicit attribute requirements:

- `__Secure-` names require `Secure`.
- `__Host-` names require `Secure`, `Path: "/"` and no Domain.
- `SameSite=None` requires `Secure`.

The complete frozen header set is staged only after successful output encoding
and `OnRevalidate`, before the view callback. Cookie responses are private/
no-store. Handler errors, invalid cookies, encoding failures or a panicking
invalidation hook suppress this result's new cookies. An invalid/over-64-KiB
combined-view result also suppresses them; existing invalidation-hook behavior
still runs for that compatibility path. This is not rollback of a committed
database write or middleware headers already staged outside this result.
Never automatically retry an unknown/confirmed mutation just to obtain cookies.

The byte limits cover serialized controls, not loader/private database
allocations, browser quotas, whole heap or RSS. Secure attributes do not create
TLS; browser acceptance and deployment policy still matter.

## Combined views and authorization

`CallAndRefresh`/multipart use a request-local jar to pass native action cookie
updates to the fresh page, which reruns middleware and data authorization.
Received Cookie values/order are preserved; known updates overlay only matching
domain/path/secure scopes, including deletions. The framework does not infer
unknown original cookie attributes. Managed draft state is rebound after
updates; a changed draft identity reports confirmed view conflict before the
fresh GET. Ordinary `Call` retains its explicit refresh/navigation semantics.

These controls do not provide identity-provider integration, a database
transaction, durable token revocation or a complete authentication policy.
The application must authenticate every sensitive action/data access and
decide how a confirmed write with a failed view is reconciled.

## Run the signed-session example

`/cookie-session` demonstrates generated login/logout actions and fresh native
page/data authorization. Configure a private 32-byte HMAC key as 64 hexadecimal
characters and the existing private reader/administrator credentials:

```sh
export NEXTGO_DEMO_COOKIE_KEY="$(openssl rand -hex 32)"
# Configure NEXTGO_DEMO_READER_TOKEN and NEXTGO_DEMO_ADMIN_TOKEN privately.
NEXTGO_DEMO_COOKIE_ALLOW_INSECURE=true \
  go run ./cmd/nextgo dev -addr 127.0.0.1:3060 ./examples/demo
```

Visit `http://127.0.0.1:3060/cookie-session` and use a configured credential.
The explicit insecure setting permits **loopback HTTP only**. Otherwise the
example requires `Request.TLS`; untrusted forwarded-protocol headers do not
grant secure transport. There is no built-in fallback signing key. Non-loopback
TLS termination/identity deployment remains separate work.

The example issues an HttpOnly, SameSite=Lax cookie at the exact public mount.
Its HMAC binds host **including port** and mount scope; role comes from native
credential checking, not a role/query/header supplied by the browser. Signed
claims have a 15-minute maximum lifetime and random nonce, with strict duplicate,
signature and expiry checks. Claims are signed, not encrypted secret storage.
The same configured key/scope permits stateless verification across cold
processes; independent direct ports intentionally have distinct bindings.

Logout clears that browser cookie only. A copied valid token remains valid
until expiry: this example has no durable revocation, managed key rotation,
account store or identity-provider integration. It is a demonstration of the
native controls and fresh checks, not production authentication. D16 and all
79 capability rows/11 release gates remain tracked in [the inventory](PARITY.md).

## Current evidence

Focused native cookie/auth/same-response checks cover the new controls. Full
current SDK, browser, deployment and compiler-overlay qualification is pending.
No broad result from preceding action-view source is reused as proof of this
phase. [Typed dispatch](MUTATION_DISPATCH.md) and [combined-view outcomes](ACTION_VIEWS.md)
define its separate client sequencing and confirmation behavior.
