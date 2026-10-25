# Server actions

`Router.ServeWithView` is the native integration hook for an explicitly
negotiated mutation/current-view response. It shares the ordinary action
execution path and passes `ViewSuccess` to a renderer only after successful
encoding and the existing `OnRevalidate` callback. The renderer receives owned
encoded bytes and copies of the effect selectors, and is responsible for any
additional fallible shared-cache expiry before loading the view. It must not
rerun the action. The hook does not negotiate view URLs, authorize destinations,
or implement the browser commit protocol on its own.

View-mode errors always return ordinary typed JSON, including multipart
submissions with an exact view-protocol `Accept` header. The hook clones request
headers for this negotiation; it does not alter the caller's request. Ordinary
`ServeHTTP` forms still receive their usual HTML errors.

The view hook limits the encoded success response to 64 KiB. A result exceeding
that bound may follow a committed mutation; ordinary invalidation still runs,
and the response reports a generic unknown outcome requiring reconciliation
before retry. Failures never imply rollback. Renderer panics before output
receive a generic 500; after a write or flush they are logged without appending
ordinary JSON to a partial view stream. Normal `ServeHTTP` response behavior
and progressive forms retain their existing contract.

`actions` provides explicit Go mutation handlers for JSON requests and ordinary
HTML forms. Authentication, authorization, persistence, and domain validation
belong in the handler. Register actions before serving requests.

```go
router := actions.New(actions.Options{
    OnRevalidate: func(tags, paths []string) {
        // Invalidate application caches after a successful action.
    },
})
router.Handle("profile", func(c *actions.Context) (actions.Result, error) {
    // Authenticate and authorize this request before modifying any data.
    values, err := c.ParseForm()
    if err != nil {
        return actions.Result{}, err
    }
    name := strings.TrimSpace(values.Get("name"))
    if name == "" {
        return actions.Result{}, &actions.ValidationError{
            Message: "Please enter your name.",
            Fields: map[string]string{"name": "Name is required."},
        }
    }
    if err := saveProfile(c.Context, name); err != nil {
        return actions.Result{}, err
    }
    return actions.Result{
        Data: map[string]string{"name": name},
        Redirect: "/profile?saved=1",
        RevalidateTags: []string{"profile"},
        RevalidatePaths: []string{"/profile"},
    }, nil
})
mux.Handle("/_nextgo/actions/", http.StripPrefix("/_nextgo/actions/", router))
```

```html
<form method="post" action="/_nextgo/actions/profile">
  <label>Name <input name="name" required></label>
  <button type="submit">Save</button>
</form>
```

HTML form submissions receive a `303 See Other` for successful local redirects.
Without a redirect, successful forms receive a confirmation document. Validation
errors return escaped HTML with status `422` and field messages. These documents
are deliberately basic; an application can validate and redirect to its own
form pages when it needs a custom presentation.

For JSON handlers use `c.DecodeJSON(&input)` and return `Result.Data`. An
`Accept: application/json` request (or JSON request body) receives:

```json
{"data":{"name":"Ada"},"redirect":"/profile?saved=1"}
```

Errors have the shape
`{"error":{"code":"validation_error","message":"Please enter your name.","fields":{"name":"Name is required."}}}`.
JSON redirects are data, allowing the caller to use client navigation after it
receives the response. They do not trigger a fetch redirect automatically.

## Request and security contract

- Only `POST` executes handlers. Action names are case-sensitive identifier
  segments separated by slashes; unknown names return `404`.
- All bodies are read and bounded before executing a handler. The default limit
  is 1 MiB; override it with `MaxBodyBytes`. Oversized inputs return `413`.
- `DecodeJSON` accepts one JSON value and rejects unknown struct fields.
  `ParseForm` accepts URL-encoded and multipart bodies, returns body fields only,
  and never merges query values. Multipart files are cleaned up when the action
  returns, so copy anything that must outlive the request.
- Requests require an origin matching the request's scheme and Host, an exact
  `AllowedOrigins` entry, a matching Referer when Origin is absent, or
  `Sec-Fetch-Site: same-origin` when both are absent. Cross-site Fetch Metadata
  is always rejected. Command-line clients should supply an Origin header.
- Forwarded headers are ignored. Behind a TLS-terminating proxy, configure the
  exact public origin in `AllowedOrigins` and have the proxy preserve Host. Do
  not derive trusted origins from arbitrary incoming forwarding headers.
- Origin checks prevent browser cross-origin mutations; they do not identify a
  user. Always authorize sensitive operations. Data and validation messages in
  responses are visible to the requester.
- Redirects must be local absolute paths starting with a single `/`; external
  origins, backslashes, control characters and encoded authority markers are
  rejected. Handlers should select redirect destinations themselves.
- Returned errors and panics receive generic `500` responses and call
  `LogError`. A `ValidationError` is explicitly public. Canceled/deadline-expired
  handlers return `408`. Every response is `Cache-Control: no-store`.
- Successful results trigger `OnRevalidate` once when tags or paths are present,
  after response serialization and redirect validation. Validation and execution
  failures do not trigger it. Hooks must be concurrency-safe. Actions execute
  once per received request; retry deduplication and transactions belong in the
  application when necessary.

Commit the backing write before returning revalidation labels. Wire
`OnRevalidate` to `App.RevalidateTag`/`App.RevalidatePath` or the shared store's
`ExpireTag`/`ExpirePathPrefix` methods for server-local read-after-write behavior.
Strict expiration cancels obsolete loads and retries callers still awaiting
their result, including completed results not yet selected for delivery.
Loaders must be read-only and honor cancellation; previously delivered results,
other server instances, browser caches and CDN caches need separate coordination.
Ordinary `Invalidate*` methods intentionally let existing waiters finish with
old data. [Cache semantics](../cache/README.md) describe these guarantees and limits.


## Authorization failures

Return `actions.ErrUnauthorized` or `actions.ErrForbidden` before a protected
mutation. The page framework recognizes the same error identities. JSON and
progressive forms receive HTTP 401/403 with generic messages, private/no-store
policy and `X-Robots-Tag: noindex`; wrapped details and returned data/redirects
are suppressed. Failed actions never invoke invalidation callbacks. They cannot
undo a mutation already performed by a handler.

Set `Options.UnauthorizedChallenge` to your scheme's `WWW-Authenticate` value
when appropriate (printable ASCII, at most 8 KiB; invalid configuration panics
at router creation). The protected sample uses `Bearer realm="nextgo-demo"`.
Progressive return links use the application's base/slash configuration.
[Access contracts and the sample](../docs/ACCESS.md) show authentication at
shared data, API and action boundaries. Managed identity/session infrastructure
remains application integration work.

Go `client.Fetch` POSTs to this application's `/_nextgo/actions/` path clear its
navigation cache on dispatch and settlement, including HTTP/transport failures.
That also cancels pending prefetches and prevents intervening responses from
repopulating the old cache epoch. It does not refresh the current view: call
`client.Refresh` or navigate after a successful mutation as appropriate. Other
origins, mounts and ordinary API URLs are outside this automatic invalidation.
Server revalidation labels/hooks are still required, and other tabs/instances
need separate coordination. A failed/canceled transport can leave an unknown
commit outcome; use application transaction/idempotency and recovery policies.

For shared Go input/output contracts, versioned endpoints and a WASM caller, see
[typed mutation contracts](../docs/MUTATIONS.md). Existing untyped handlers remain
available; typed binding still uses this router’s origin, body and effect rules.
