# Native action cookies and the signed-session example

Native cookie output and the signed-session example are implemented. Current
phase qualification includes passing cookie/FIFO, SDK and deployment checks,
plus the corrected 342-case affected browser matrix. A separate native
JavaScript reproduction leaves a CDP observation's underlying cause open.
These scoped results do not establish production maturity. Previous action-view matrices
describe preceding source.

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

Full native race/vet and matching core/mutation/module WASM tests/vet pass on
Go 1.27.3/macOS and Go 1.24.13/Linux. Full host standalone deployment under race
passes HTTP, both mock Lambda, durable-cache and workspace cases in 119.571 s.
The isolated non-root/read-only Linux suite passes 228 checks without failures
or skips, using server SHA-256
`b461e949a67478934cad1ea09ea9776265107c46c7b9e4a427af4adbe1ee875d`.
The isolated deployment image SHA-256 is
`7c3421b64321c0602f62cceacdbab676c6a0681092c8569125f16ab278b5713f`.

All **55 cookie checks pass**: 11 each on host Chromium and native WebKit,
plus 33 Linux Firefox/WebKit/touch checks. The complete affected browser record
is **342/342 passing**: 168 other host regressions, 11 host optimistic,
22 host cookie, 30 host action-view and 111 Linux cookie/action-view/optimistic
checks. Action-view's 75 pass across host Chromium/native WebKit and Linux
Firefox/WebKit/touch without skips. The cookie 55 and action-view 75 are already
included in that 342 and must not be added again.

The preceding observer run recorded 326/327, retaining one Chromium CDP
response-body failure as diagnostic history. A native JavaScript control using
the exact captured 42-byte Uint8Array request body/options reproduced
`ERR_ABORTED`/CDP `No data` once in 20 runs while all genuine frames, native EOF
and no-signal-abort checks passed. It used no Go action handler or DOM commit.
This separates the observation from Go; it does not prove an array-body cause
or identify the underlying browser/server-fixture cause.

The corrected test passively records bounded genuine upstream frames/EOF on
the same response flight, without rewriting, refetching or mocking it. Strict
Go EOF, successful receipt, UI/head/history/counter and JSON 422/409 assertions
remain. Four observer unit tests pass on host and Linux. An additional 20/20
Chromium repetition passes separately and is not added to the unique matrix.

Retained initial failures include a browser-restricted fixture gateway at 4190
(zero browser HTTP requests despite meaningful direct SSR); moving to 4194
allows the final host runs. WebKit's inspector omits custom redirect headers,
so safe actual-wire booleans verify the native 303/protocol/revision/Set-Cookie
alongside real browser cookies and fresh SSR. Firefox's held-header fixture now
releases its gate before waiting for the complete response. These are fixture
corrections, without fabricated response bytes, arbitrary delays or engine skips.

The current 712-input manifest is under
`.nextgo-test-results/qualification/cookie-dispatch/source-inputs.json`, SHA-256
`8be622a2bf7b764917bc5127e8e02102fc839bbe2317fbfc489a61cacfc9d5d7`.
The clean `d41ed6c` host artifact SHA-256 is
`88d3eeed5541cd077e7ee5445de417bc2f3b0e40d2680a601f69839276aa0e07`.
The [actual startup observation](PERFORMANCE.md#cookie-dispatch-artifact-observation-october-25-2026-utc)
records successful SSR/Go interaction but still fails both WASM size budgets.
No broad result from preceding action-view source is reused as phase evidence. [Typed dispatch](MUTATION_DISPATCH.md) and [combined-view outcomes](ACTION_VIEWS.md)
define its separate client sequencing and confirmation behavior.

The terminal `d41ed6c` remote CI remains red: obsolete parallel typed-request
assumptions affect its streaming, browser and container jobs, and one Chromium
response-body CDP observation has an unproven cause. Corrected FIFO fixtures
pass locally, but neither those corrections nor the restricted-port change
establish a cause or framework fix for the separate observation. Diagnostic
contrasts retaining response references, skipping reader-lock release or
skipping same-URL history updates did not establish causality; a fresh native
JavaScript string-body control passed 20 repetitions. The exact Uint8Array
native reproduction above also rules out attributing this only to Go. These
diagnostic repeats are separate from the 342-case matrix. See
[checkpoint provenance](PARITY.md).
