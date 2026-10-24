# Go browser view transitions

`Config.ViewTransitions` opts same-document Go navigation into the browser's
native View Transition API. It defaults to false. Hydration still attaches to
server HTML without a transition or DOM replacement. The implementation lives
in the Go/WASM runtime; no additional JavaScript bridge is shipped.

```go
func Configure(config *nextgo.Config) {
    config.ViewTransitions = true
}
```

Run the example with:

```sh
NEXTGO_DEMO_VIEW_TRANSITIONS=true go run ./cmd/nextgo dev ./examples/demo
```

Visit `/transitions` for a server-rendered page with a Go state update and link
controls. Navigation, Replace, Refresh and back/forward share the same commit
path. Initial full documents, streamed shells and static exports carry the
policy. It participates in the runtime configuration identity: a policy change
cannot silently mix incompatible HTML and navigation responses from one binary.

## Navigation controls

Ordinary intercepted page links animate when the policy is enabled. Download,
external, modified, targeted and full-document links keep their existing browser
behavior. Fragment-only links do not animate. A failed fetch or rejected envelope
leaves current content in place without starting a transition.

```go
ui.El("a", ui.Attrs{
    "href": nextgo.MustURL(c, "/posts"),
    "data-nextgo-transition": "none",
}, ui.Text("Field notes without animation"))

ui.El("a", ui.Attrs{
    "href": nextgo.MustURL(c, "/posts/hello"),
    "data-nextgo-transition-type": "details",
}, ui.Text("Read the post"))
```

`none` disables animation while preserving Go navigation. The optional type is
one ASCII identifier starting with a letter, up to 63 bytes; later characters
can include letters, digits, hyphens and underscores. Invalid link types bypass
animation rather than breaking navigation.

Browser Go code can pass typed options:

```go
err := client.NavigateWithOptions(client.MustURL("/posts"), client.NavigationOptions{
    Replace: true,
    Transition: client.TransitionOptions{Disabled: true},
})
```

`Navigate` and `Replace` remain available. Types default to `navigation`, `history`
or `refresh`. Custom `TransitionOptions.Types` accepts at most eight names using
the same grammar; `auto` and `none` are reserved. Invalid programmatic options
return an error before canceling navigation. Types require the browser's newer
`ViewTransition.types` support; browsers without it still use ordinary native
transitions. This is a Go-native API, not a React component compatibility layer.

## Explicit component updates

`client.Transition` runs one synchronous Go DOM/state update. Call it in a
goroutine, because a browser event callback cannot block while awaiting another
browser callback. Return network results before starting the transition. Reuse a
component cancellation context, cancel it in `Scope.Cleanup`, and keep the update
itself short. The [transitioncard example](../examples/demo/components/transitioncard/client_js.go)
shows state subscription, cancellation, error handling and disposal.

```go
ctx, cancel := context.WithCancel(context.Background())
scope.Cleanup(cancel)
scope.On(button, "click", func(client.Event) {
    go func() {
        err := client.Transition(ctx, client.TransitionOptions{Types: []string{"counter"}}, func() error {
            state.Set(state.Get() + 1)
            return nil
        })
        // Handle err while the component is still mounted.
        _ = err
    }()
})
```

The explicit component API opts in independently of `Config.ViewTransitions`;
that setting governs navigation only. `Transition` returns after the update,
without waiting for animation. An error/panic in the update is returned; partial
application mutations are not rolled back.

## CSS and accessibility

The browser supplies a default root transition. Use ordinary stylesheets to
select pseudo-elements or give matching elements a unique `view-transition-name`:

```css
.product-image { view-transition-name: product-image; }
::view-transition-group(*) { animation-duration: 180ms; }
@media (prefers-reduced-motion: reduce) {
  ::view-transition-group(*),
  ::view-transition-old(*),
  ::view-transition-new(*) { animation: none !important; }
}
```

The Go runtime checks reduced motion before each transition. A change to reduced
motion skips an active animation. A failed preference query also bypasses
animation. Missing/throwing browser APIs, duplicate CSS names and animation
rejections degrade to ordinary updates. Keep reduced-motion CSS as well, including
for animation triggered outside this runtime. Focus, announcements and scroll
restoration run during the existing navigation commit; snapshot overlays do not
replace the accessible page. Manual screen-reader and physical-device review
remain release work.

## Ordering, cancellation and limits

Navigation fetches and validates HTML/head/configuration before taking snapshots.
Inside the native update callback it rechecks cancellation, then commits body,
metadata, history, hydration, focus and scroll. Component updates and navigation
share one active native transition. A newer transition skips the preceding
animation; it does not cancel an unrelated component's state change. Superseded
navigation and unmounted component contexts separately prevent their stale
updates. A same-document history/fragment change also skips an active animation.

Snapshot setup waits at most one second before skipping animation and applying
an ordinary update. An eventual native callback remains callable and cannot
repeat the update. Go callbacks for browser promises are retained until settlement
and released on both success and failure. A conforming browser's native lifecycle
must settle; Go cannot force an altered third-party implementation to invoke or
release callbacks. Rendering and updates execute on the browser main thread.
Snapshot textures, named elements and animation resources consume browser-managed
memory; no production memory/load budget has yet been established.

The current API does not manage cross-document transitions, nested independent
transition roots, React enter/exit/share reconciliation, resources/fonts readiness,
route-specific animation choreography or retained whole-route state. Streaming
chunks themselves do not start transitions, and navigation still resolves a
complete HTML envelope before committing. These limits remain in the parity
inventory rather than being treated as full feature conformance.

## Verification

`npm run test:wasm` exercises name bounds, fallback/update errors, canceled late
callbacks and the one-second snapshot limit. Browser fixtures use actual native
transitions for navigation/state, types when supported, Refresh/Replace/history,
focus/layout retention, reduced motion, streaming, API absence/failure, network
failure, duplicate CSS names, unmount cancellation and slow/late callbacks.
Static-host tests also verify exported transition policy and JSON navigation.
Generated executable tests cover HTTP and both mock Lambda transports, including
streamed shell ownership. These are subset checks; all production maturity gates
remain open in [PARITY.md](PARITY.md).

Sources: [Next.js view-transition guidance](https://nextjs.org/docs/app/guides/view-transitions),
[native API](https://developer.mozilla.org/en-US/docs/Web/API/Document/startViewTransition),
[skip behavior](https://developer.mozilla.org/en-US/docs/Web/API/ViewTransition/skipTransition),
[CSS View Transitions specification](https://www.w3.org/TR/css-view-transitions-1/).
