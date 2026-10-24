# Keyed Go collections

`ui.Keyed` and `client.HydrateKeyed` support explicit lists of interactive rows.
SSR supplies useful elements; hydration validates their keys and binds behavior
to those same nodes. `Set` retains a row and its scoped Go state while its key
remains present, moves it when its position changes, renders additions, and
disposes removed rows. The `/keyed` and `/keyed/table` examples demonstrate list
and natural table markup, independent collections, editable text and counters.

## Shared markup and browser binding

Keep props and row markup in shared Go files. Keep native data access and page
functions in server packages or files excluded from the browser build. Browser
binding belongs in a `//go:build js && wasm` file.

```go
type Item struct { ID, Label string }

func Row(item Item) ui.Node {
    return ui.Keyed("li", item.ID, nil,
        ui.El("span", ui.Attrs{"data-label": ""}, ui.Text(item.Label)))
}

// SSR: initial is also serialized in the containing island's public props.
func Rows(initial []Item) ui.Node {
    nodes := make([]ui.Node, len(initial))
    for i, item := range initial { nodes[i] = Row(item) }
    return ui.El("ul", ui.Attrs{"data-rows": ""}, nodes...)
}
```

Inside the browser component's `Mount`, after decoding its initial props:

```go
host, err := scope.Require("[data-rows]")
if err != nil { return err }
list, err := client.HydrateKeyed(scope, host, initial,
    client.KeyedOptions[Item]{
        Key: func(item Item) string { return item.ID },
        View: Row,
        Bind: func(row *client.Scope, item Item) (func(Item), error) {
            label, err := row.Require("[data-label]")
            if err != nil { return nil, err }
            // Register row.On listeners and row.Cleanup subscriptions here.
            return func(next Item) { label.Text(next.Label) }, nil
        },
    })
if err != nil { return err }
// Call from Go event/state handlers and handle the returned error.
err = list.Set([]Item{{ID: "second", Label: "New order"},
    {ID: "first", Label: "Retained state"}})
```

The host owns its direct element children. `ui.Keyed` puts an escaped
`data-nextgo-key` attribute on the chosen element without a wrapper; use `tr`
inside `tbody` for tables. Keys are unique within one collection; separate
collections may use the same keys. Identity follows a key, so changing a key
removes one row and creates another. `Keys()` returns an owned ordered copy.

`Bind` runs once per row, including existing SSR rows. Its child scope owns
listeners, state subscriptions and cleanup. `View` runs only for additions.
Retained rows receive the returned update callback on each `Set`, unless an
optional `Equal(old, next)` returns true. Updating markup remains explicit:
there is no automatic property or subtree reconciliation. Treat supplied values
as immutable; generic values containing slices, maps or pointers are shallow
copies. A row can adopt edits made before WASM loads, as the example does.
Serialized props are public browser data.

## Identity, movement and lifecycle

Hydration requires the exact SSR row count and ordered keys. A mismatch returns
an error and leaves the initial row structure intact. A failing application
binding may already have changed its own nodes; framework cleanup releases the
scopes created before the failure.

Reordering retains DOM nodes and uses a longest increasing subsequence to avoid
moving rows already in relative order. Native `Element.moveBefore` is used when
available; an `insertBefore` fallback restores retained input focus and selection.
The fallback does not promise preservation of every browser-managed state, such
as custom-element lifecycle or dialogs. See the browser's
[moveBefore semantics](https://developer.mozilla.org/en-US/docs/Web/API/Element/moveBefore).
The application decides where focus goes after removing the focused row.

Removing a row disposes its scope before detaching the element. Navigating away
disposes its parent island and all collections. `Close()` releases bindings and
references while leaving DOM in place; later `Set` returns `ErrKeyedClosed`.
Closing a collection unregisters it from its parent instead of accumulating a
cleanup closure for every replacement collection.

Validate keys and prepare all additions before structural changes. New markup
is rendered through `ui.Node`, parsed in an inert document with the host's element
context, checked, then bound while detached. Preparation failures release those
new scopes and preserve the existing collection. Application callbacks must
return promptly and must not mutate collection structure themselves. Use a
goroutine for asynchronous network work and register cancellation with cleanup.

Callback panics become errors. A failure after structural commit closes the
collection and releases its bindings. Arbitrary application DOM writes cannot
be rolled back, so the operation is not a transaction across callback effects.
Reentrant `Set` returns `ErrKeyedReentrant`; callers should queue a later update
instead. Unrelated collections and router navigation remain available.

## Bounds and supported content

- Default 200 items; set `MaxItems` from 1 through 1,000.
- Keys: nonempty valid UTF-8, at most 256 bytes, without ASCII control characters.
- At most 16 live collections per scope and 16 collection nesting levels.
- Added row rendering: 64 KiB per row and 1 MiB total per update.
- Each row: at most 2,000 descendant elements. Host: at most 3,000 child nodes.
- Whitespace text between rows is allowed. Other direct text/comments are rejected.

Rows reject scripts, styles, frames, embedded objects, templates, document-head
elements, unsafe attributes/URLs and framework markers other than keys.
Nested islands and streaming boundaries inside rows are unsupported. Shared row
views must use browser-safe imports. These checks constrain framework-generated
content; trusted application Go/custom nodes and direct DOM access are not an
untrusted-content sandbox.

Browser tests cover delayed-WASM identity and edits, explicit updates, independent
state, native/fallback focus and caret, additions, escaping, removals, navigation,
duplicate/size failures, detached preparation cleanup, binding/update panics,
table rows and repeated listener disposal. The pure planner checks all 720
permutations of six retained rows against an independent minimum-move bound.
This is a bounded collection API. Row scopes can use [explicit lifetimes,
effects and typed context](COMPONENT_LIFETIMES.md); cross-runtime context,
integrated controlled forms, arbitrary component reconciliation and full
retained/hidden-route lifecycle qualification remain open. Current bundle measurements belong in
[PERFORMANCE.md](PERFORMANCE.md).

The final focused run passes 12 Chromium and 36 Linux Firefox/WebKit/touch
checks. Plain static-host tests include the same SSR/hydration/navigation API;
source-independent HTTP and both mock Lambda response modes verify rendered
keyed markup. This is local qualification, with real cloud/load/soak testing
still open. To repeat the dedicated browser checks after installing the test
dependencies:

```sh
node scripts/test-browser.mjs --project=keyed
sh scripts/test-crossbrowser-container.sh tests/keyed.spec.mjs
```

The second command requires Docker and its configured browser image. Native
planner/render tests run with `go test ./internal/keyed ./ui`; `npm run test:wasm`
also checks browser writer bounds and closed-owner handling with the matching
Go WASM runner.
