# Go browser navigation and route subscriptions

Browser APIs live in `client` and run under `//go:build js && wasm`.
`Navigate(target)` pushes a page navigation, `Replace(target)` replaces the
current entry, and `Refresh()` revalidates the current view without adding
history. Their successful return means navigation started; loading, cancellation
and recoverable failures are handled by the runtime. Use `client.URL` or
`client.MustURL` when constructing application-relative targets for mounted or
localized deployments.

## Observing committed addresses

`client.CurrentLocation() (client.Location, bool)` reads a value snapshot of the
last committed address. The boolean is false before `client.Run` initializes
the document location. Each island can subscribe with `scope.ObserveLocation`:

```go
//go:build js && wasm

func Mount(scope *client.Scope, raw json.RawMessage) error {
    output, err := scope.Require("[data-current-route]")
    if err != nil {
        return err
    }
    _, err = scope.ObserveLocation(func(location client.Location) {
        output.Text(location.Pathname + location.Search + location.Hash)
        // location.Query().Get("q") reads the first decoded q value.
    })
    return err
}
```

The observer runs immediately, then after a changed URL commits. The server
should render meaningful initial text into the existing output node. The demo's
[shared layout](../examples/demo/app/layout.go) and
[Go island](../examples/demo/components/layoutstate/client_js.go) show this
pattern. The fragment is unavailable on the server, so client synchronization
can add it after hydration without replacing the output element.

`Location` contains immutable string values:

| Field | Meaning | Example |
| --- | --- | --- |
| `URL` | Absolute browser address | `https://example.com/shop/fr/posts?q=Go#intro` |
| `Pathname` | Escaped path, including base path and locale | `/shop/fr/posts` |
| `Search` | Encoded query with leading `?`, or empty | `?q=Go` |
| `Hash` | Encoded fragment with leading `#`, or empty | `#intro` |

`Query()` returns a fresh `url.Values` map on every call. It uses Go's
`net/url.ParseQuery` rules: repeated values are retained, `Get` reads the first,
and malformed fields may be omitted. `Search` preserves the original browser
encoding. The browser's URL parser supplies all four snapshot strings.

Callbacks run after the DOM, head, and history commit. Islands mounting during
that commit see the new address immediately and receive no duplicate
notification for it. A retained layout's observer survives navigation. Leaving
or replacing its island, a failed mount, or an authorization denial removes its
subscription automatically. The returned stop function permits earlier,
idempotent unsubscription. The runtime permits at most 512 live location
observers. Callbacks must return promptly; start a goroutine for network work.
Callback panics are reported through the framework error status.

Subscribers observe successful page navigation, query changes, replacement,
back/forward, native same-page fragment navigation, and custom 404/error views
that commit a URL. Loading, speculative prefetches, failed requests, and
superseded responses do not publish their targets. Refreshing an unchanged URL
does not notify. During a pending cross-page history traversal, the address bar
may already show the target; the snapshot continues to describe the committed
view. A failed traversal restores that view's address. Build/protocol fallbacks
start a new document and new runtime. If initial document validation disables
SPA routing, islands can still read and observe their initial location while
links fall back to ordinary browser navigation.

The snapshot describes the visible browser address, including intercepted modal
targets. Use the [selected-segment APIs](PARALLEL_ROUTES.md) for a particular
retained layout or parallel branch. Rewrites can make the server's logical
route differ from the visible pathname. Direct third-party `history.pushState`
and `history.replaceState` calls are not instrumented; use the Go navigation
APIs. Native fragment events are supported.

## History and dismissal

`Back()` and `Forward()` cancel pending navigation and request a browser history
traversal. They may leave the application; an absent entry is a browser no-op.
`BackOrReplace(target)` uses a bounded, runtime-owned history record to go back
only when the immediately preceding owned URL matches the target. Otherwise it
replaces the current entry with the target. This supports modal dismissal after
both push-open and replace-open navigation. See
[interception history](INTERCEPTING_ROUTES.md) for ownership and fragment rules.

Links with `data-nextgo-replace="true"` replace an entry, including same-page
fragments. Other eligible page links push. External links, downloads, modified
clicks, explicit targets and API links retain ordinary browser behavior.

## Verification and remaining work

`tests/location.spec.mjs` checks initial SSR/text identity, retained subscribers,
dynamic/query/history/fragment changes, replace/refresh, loading and failure,
superseded responses, denial cleanup, and custom 404 commits. Mounted and locale
browser suites verify prefix handling. Native and WASM unit tests check owned
query values, initialization, notification timing/deduplication, observer
bounds, unsubscription and reentrant disposal.

This API is a Go equivalent for observing addresses; it does not provide React
hooks or Next.js source compatibility. Richer segment navigation protocols and
production lifecycle qualification remain open in [the parity inventory](PARITY.md).
