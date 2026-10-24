# Intercepting routes

Interceptions render a registered destination inside a named slot during Go
navigation while keeping a revalidated background page. A direct request or
browser reload renders the destination's ordinary page. The destination URL
remains shareable. This is a Go routing API, without Next.js source compatibility.

## Files and ownership

```text
app/gallery/page.go                                  /gallery background
app/gallery/layout.go                                composes c.Slot("modal")
app/gallery/slot_modal/default.go                     empty closed slot
app/gallery/slot_modal/intercept_root/photos/param_id/page.go
app/photos/param_id/page.go                           /photos/:id standalone
```

`intercept_root` is allowed directly inside a named slot. Its descendants use
absolute logical application paths: `photos/param_id` targets `/photos/:id`.
There is no extra URL segment for `intercept_root` or the slot name. A normal page
must cover each destination; API/static-enumeration callbacks are rejected in the
interception tree. Each interception slot needs a default. Ambiguous targets at
the same owner fail discovery/registration.

Coverage uses route grammar: a normal page pattern must cover every path admitted
by its interception pattern. A single parameter page cannot cover a catch-all;
an optional catch-all standalone page can cover an optional catch-all interception
even when their parameter names differ. Coverage by a union of separate standalone
patterns is not inferred.

The generator supplies an implicit primary owner for the gallery layout. Set
`Config.PreserveLayouts = true` to retain matching background DOM and Go islands.
Its layout combines its body with `c.Slot("modal")` just like other parallel
routes. Interception pages inherit layouts, metadata and boundaries declared
inside `intercept_root`; they do not inherit the slot directory's normal/default
wrapper chain. Share wrapper functions explicitly when both branches need them.

Programmatic applications declare the same relationship through `Slot.Intercepts`:

```go
nextgo.Route{
    Pattern: "/gallery", Page: gallery,
    Layouts: []nextgo.LayoutFunc{galleryLayout},
    LayoutIDs: []string{"gallery"}, LayoutBases: []string{"/gallery"},
    Primary: &nextgo.PrimarySlot{ID: "gallery.children", Base: "/gallery", Layout: 0},
    Slots: []nextgo.Slot{{
        ID: "gallery.modal", Name: "modal", Base: "/gallery", Layout: 0,
        Default: emptyModal,
        Intercepts: []nextgo.Route{{Pattern: "/photos/:id", Page: photoModal}},
    }},
}
// Also register a normal /photos/:id page.
```

The primary declaration must belong to the same layout/base as the slot. All
branches are frozen during application construction and included in runtime
configuration identity. Existing slot graph/count/depth/registration bounds apply;
normal and intercepted routes together allow at most 128 branches per slot.

## Requests, state and authorization

Only framework navigation can intercept. The browser sends the active slot URL
selections already used for parallel routes. The server requires a registered
background route and its parent-bound primary scope, then selects the deepest
eligible owner. Only the selected named slot may intercept. Matching direct
destinations beneath the same layout still render their ordinary page.

At the deepest eligible owner, ordinary route specificity applies across slots:
static targets outrank parameters and catch-alls. If multiple distinct backgrounds
remain equally eligible, the carrier is rejected before any page data loads.
Map iteration and slot registration order do not select a background. A uniquely
deeper owner can resolve otherwise ambiguous shallower origins.

Saved state is public, untrusted view selection. It never supplies rendered HTML,
trusted role/tenant decisions or permission to reuse private data. Background and
modal loaders execute with current request headers, cookies and cancellation.
Shared parameter names in the owner's base and target bind to equal decoded
values; `/gallery/:tenant` cannot intercept `/photos/:tenant/:id` for another
tenant. Data access must still authorize the actual resource independently.

Keep authorization in shared server data functions used by both standalone and
interception pages. The standalone page's own layouts/providers do not run as a
second authorization pass when an interception page renders. The example's
`internal/photos.Load` is shared by both views. `X-Demo-Gallery-Deny` and
`X-Demo-Photo-Deny` are test probes, not an authentication system.

Stateful background responses bypass shared caching. Destination navigation
responses also bypass public and URL-only browser prefetch reuse, including when
no background is active. The standalone ordinary document may use an explicitly
safe public page policy. A 401/403 from either branch drops the protected composed
view and disposes its islands. A failed network request preserves the current
view. Interception errors use the branch's ordinary error/not-found machinery.

## Dialogs and history

The example uses a server-rendered native `<dialog>` and Go `Scope.Modal`. That
helper upgrades the existing node, uses the browser's modal/inert semantics,
cycles keyboard focus at the first/last visible control, and invokes a supplied
callback for Escape. Its cleanup removes scoped listeners, closes the dialog and
restores its connected opener after retained DOM reconnects. Changing slot effects
are disposed before any retained sibling moves, so native modal inertness is cleared
while the background is connected; retained nested islands stay mounted. Keep an accessible
dialog name and a visible close control in server markup.

`client.Back()` and `client.Forward()` traverse the browser history asynchronously.
They cancel superseded Go navigation first and may leave the application if the
browser entry belongs elsewhere. Ordinary pushed photo navigation closes on back,
and forward reopens the modal.
Reloading either photo URL produces the standalone page. Modal counter state
resets when its content changes; matching background state survives while its
revalidated output remains unchanged. Changed background output replaces and
disposes that background according to the normal slot content-hash contract.

`client.Refresh()` revalidates an open modal without adding history. The example's
Refresh button preserves the native dialog and both counters for unchanged output;
`X-Demo-Photo-Version: updated` demonstrates changed modal data and cleanup while
the gallery stays mounted. Fresh background/photo denials during refresh discard
the protected composition. These behaviors are checked across Chromium, Firefox,
WebKit and touch, with server refresh/denial checks in HTTP and both Lambda mocks.

### Returning after push or replace

An interception page can read `from, ok := c.InterceptedFrom()`. It returns its
selected background's logical URL and query; normal pages receive false. This is
public view data, not an authorization result. Pass `nextgo.MustURL(c, from)` in
island props to adapt the logical URL to the application's base/locale mount.

The example's close button and Escape call `client.BackOrReplace(returnTo)`.
When the immediately preceding runtime-tracked destination matches `returnTo`,
the helper traverses back. If opening the modal replaced the gallery entry, it
replaces the photo entry with the gallery instead of visiting an unrelated page.
Unknown, evicted or other-document history also uses replacement. Failed network
returns preserve the current modal and controls for retry.

The runtime stores at most 128 opaque history records, with URLs bounded to 4096
bytes each. It verifies each record against the active entry's URL. These records
describe navigation and grant no resource access. A return target without a
fragment accepts the tracked source fragment so back can restore it; queries,
origins and explicitly requested fragments must match. A longer/unknown history
falls back to replacing the current entry. The helper does not search arbitrary
older entries or unwind an entire modal stack.

Add `data-nextgo-replace="true"` to an ordinary local page link for declarative
replacement, including same-page fragment changes. Modified clicks, downloads,
targets and external links keep their ordinary behavior. Before WASM starts, the
link remains a normal browser link. The Gallery example includes both open and
replace-open links.

## Current limits and qualification

Interception owners currently belong to the application's primary layout tree;
owners inside named slots are rejected. Absolute target directories replace
Next.js relative matcher syntax. Primary-owner metadata remains the document
head; independent slot head composition is still open. Stacked contextual overlays,
arbitrary history-stack unwinding and all nested-owner/context
combinations need further qualification. Native dialogs require modern browser
support. Keyboard tests do not establish screen-reader/manual accessibility or
production resource/load qualification.

Static exports cannot publish the stateful gallery page. Unrelated and standalone
pages can still be exported when they satisfy the ordinary static contract.
Real cloud/CDN/TLS and operational qualification remain release gates; local
Lambda fixtures use the Runtime API mock rather than an AWS account.

Source: [Next.js intercepting routes](https://nextjs.org/docs/app/api-reference/file-conventions/intercepting-routes).
The complete objective remains tracked in [PARITY.md](PARITY.md).
