# Named parallel routes

Named slot routes are a working subset of the parallel-route capability. They
are ordinary server Go pages, composed by a shared layout and hydrated with the
same Go/WASM island runtime. File discovery uses `slot_name` folders because Go
import paths cannot use Next.js's `@name` syntax.

```text
app/boards/
  layout.go
  page.go
  default.go
  chart/page.go
  settings/page.go
  slot_analytics/
    default.go
    layout.go
    metadata.go
    error.go
    notfound.go
    chart/page.go
    slot_detail/
      layout.go
      default.go
      drill/page.go
  reports/
    layout.go
    page.go
    default.go
    slot_reportdetail/
      page.go
      summary/page.go
  slot_team/
    default.go
    settings/page.go
    activity/page.go
```

The named directories contribute no URL segment. At `/boards/chart`, analytics
selects its chart page; team uses its default on a direct load. At
`/boards/settings`, team selects settings; analytics uses its default on a direct
load. `/boards/activity` is supplied only by the team slot: there is no primary
`activity/page.go`. A direct load uses `boards/default.go` for the implicit
`children` branch. If that primary default is absent, a direct load returns the
application's not-found response. A soft navigation can retain a prior primary
page after server revalidation. Named slots can nest in other named slots and
inside an implicit primary's inner layouts. This is still a subset of Next.js
parallel-route conformance.

The owner must have `layout.go`. Its usual layout signature remains unchanged:

```go
func Layout(c *nextgo.Context, children ui.Node) (ui.Node, error) {
    analytics, err := c.Slot("analytics")
    if err != nil { return nil, err }
    team, err := c.Slot("team")
    if err != nil { return nil, err }
    return ui.Fragment(children,
        ui.El("aside", ui.Attrs{"aria-label": "Analytics"}, analytics),
        ui.El("aside", ui.Attrs{"aria-label": "Team"}, team)), nil
}
```

`page.go` exports the ordinary `Page(*nextgo.Context) (nextgo.Page, error)`.
A slot-root or primary-owner `default.go` exports `Default` with that signature.
The default can return an empty `ui.Fragment`. A primary default requires a
layout owning named slots in the same directory; it creates no standalone URL.
Named pages contribute their URL patterns to generated application routes. The
generator composes each slot-only URL using its real owner's layout, metadata
and boundary ancestry. `Route.Primary`/`PrimarySlot` identify a single implicit children branch;
`Route.Primaries` identifies an ordered outermost-first chain. Choose one
registration form. A `Fallback` declaration identifies the owner whose `Default`
(or `ErrNotFound`) supplies that composite page when no prior selection applies.
Ancestor primary declarations remain matching branches of that composite. Slots can have their own layouts, templates,
error/not-found boundaries and browser islands. A named default inherits the
slot root's layouts, templates, metadata providers, and error/not-found boundaries,
including when that root has a template/provider/boundary without a layout.
Primary defaults use their owner's full ancestry. Defaults have no URL of their
own and do not conflict with matching page patterns.

The generator emits typed `Route.Slots`/`Slot` registrations. Named defaults use
`Slot.DefaultRoute`, a complete `Route` whose `Pattern` equals the slot's `Base`
and whose `Page` points to `Default`. Defaults can contain their own primary
chain and named slot registrations. A function-only `Slot.Default` remains a
shorthand for applications registering routes directly; choose one field.
`DefaultRoute` and matching branches share startup validation and frozen copies
of wrappers, layout identities, metadata layers and access-boundary declarations.
There is no runtime Go interpreter.

Only the owning layout can request its slot. Render each returned slot node
once. Calls are lazy and sequential, on the current request; the framework does
not yet schedule slot loaders concurrently or stream independent slot chunks.
A layout that does not request a slot does not render its data. Authorize in the
slot's data/page code, independently of any conditional layout visibility.

## Direct and soft navigation

A direct request selects matching slot routes or their defaults. Missing a
required default returns the application's not-found response. Global custom
404 composition suppresses named slots so it does not accidentally render an
active branch or fail when a root layout asks for a slot.

On Go navigation, an unmatched slot can keep its previous logical URL. Its page
and local layouts run again with the current request's headers, authorization,
cancellation and request memoization scope. A bounded `X-Nextgo-Slots` carrier
contains the document configuration and public slot-scope/URL selections. It is
not an authorization token. Unknown scopes are ignored, and a scope is tied to
the layout/slot identity, concrete owner path (including tenant parameters),
and enclosing slot identity. Identically named/registered children beneath
different parents therefore have separate ownership and state. A carrier cannot choose another owner's branch or bypass its loader.
Authorize sensitive primary data in the selected page/loader too; a middleware
check based only on the destination path does not authorize a retained page.

The same selection contract applies to implicit children. A matching primary
page always takes precedence over prior state. At a slot-only URL, the server
can select a previously registered primary page under the same concrete owner.
That page, its inner layouts and metadata receive the selected URL/parameters/
query and the current request headers. Shared outer layouts receive the
composition's current route request. Nested slot matching always uses the actual
navigation destination, even when the enclosing branch renders at an older URL;
unmatched children then select and revalidate their saved URLs independently. Context views share response controls and
request memoization without mutating either request view. The primary page
continues to own document metadata, so its title persists at a slot-only URL.
Changing a server data version changes its hash and replaces its DOM; a 401/403
replaces the complete document content. Reload has no selection carrier and
uses the owner's default or 404. A history entry from an incompatible deployment
also discards its selections and resolves the current build's defaults; it does
not send an obsolete ownership carrier or retain the current entry's branches.

The server marks a branch retainable only after successful rendering and a
compatible saved selection: either an unmatched retained URL or an already
active matching URL. The browser retains DOM and Go state only when the scope,
active URL and server-rendered content hash agree. A different matching URL or
changed own content replaces the branch. Returning through history to a branch
already active at that URL preserves its Go state after server revalidation. Unchanged slot-local layouts may
be retained under `PreserveLayouts`, with its existing request-sensitive markup
caveats. A changed slot set replaces the owner layout as needed. Removed branches
dispose their Go listeners/subscriptions before the new content is mounted.

A branch's own hash includes the identities and positions of its direct child
slots, while each child has a separate content hash. Updating a revalidated child
therefore does not reset an otherwise retained parent page or its islands.
Rendering computes HTML and the owned-content hash in one pass, so deferred
loaders do not run twice to obtain a hash. The browser validates the full tree
and builds a reconciliation plan before live mutations. It moves retained child
roots out of replacement regions before disposing parent effects, and rejects
wrong parents, duplicate markers, excessive nesting and inconsistent outlet
ownership before committing metadata/history.

History entries save slot URL selections. Back/forward restores those selections
through the server, preserving route composition. It does not supply a whole-page
DOM cache: a branch that changes still resets its page-local island state. Direct
reload intentionally uses deterministic defaults instead of prior selections.
`Refresh` revalidates the active selections. Switching language/domain still
uses a new document and clears the previous runtime's state.

A 401/403 from any requested slot becomes a document access denial and disposes
all retained layout/slot DOM. Independent per-slot denial UI is not implemented.
Other slot errors, including default loader errors, use local boundaries where
available and can raise the overall HTTP status while other composed content
remains present. A default's local 404 keeps its slot-root wrappers and the other
branches. Its metadata providers execute on each selection and can deny access;
errors and denials cannot become retainable successful output. The primary page owns
the document title/metadata/resources; slot head merging is not implemented.

## Response, cache and resource contracts

Named slots cannot stage headers, cookies, statuses or redirects. Such calls return
`ErrSlotResponse` and invalidate that render even when ignored. Use primary-page
or middleware response controls. Deferred rendering remains subject to the
ordinary sealed-response contract. The primary page retains ordinary response
controls, including when selected from a prior URL. Slot response-control semantics
are separate from application data mutations: loaders should be read-only and cooperative
with request cancellation.

Parallel pages always bypass shared page snapshots and Go prefetch reuse and
advertise `private, no-store`. Explicit data caches still require correct
user/tenant/input keys. Metadata, primary page and slots can share request Memo.
Registered branch cache policies, HTTP endpoints and static-parameter callbacks
inside named slot trees are currently rejected.

Recursive discovery composes nested slot-only URLs using each owner's real
layout/metadata/boundary ancestry. A nested named branch has its own route pool
and fallback; primary retention cannot select an unrelated top-level route.
Implicit primaries can appear inside named slots, and named slots can appear
inside retained primary layouts. Multiple implicit primary owners in one ancestor
layout chain compose separate outlets, scoped to each enclosing primary. Each
owner records its own selected URL and retention decision. An inner fallback can
retain an old page while its outer owner matches a new composite URL. A later
outer fallback can retain the entire inner composition and still revalidate every
selected page and side branch with current request headers.

The renderer resolves owners from outermost to innermost. A layout receives the
request selected by its enclosing primary; the owner itself receives that
primary's current composition request. The leaf page and its metadata receive
the final selected request. Context views share response controls and cancellation
without changing previously handed-out request views. A primary page/layout/
metadata error disables that composition's primary retention; any denial disposes
the whole document view.
An unchanged successful selected URL can retain its Go state after revalidation.

If ancestor and descendant named branches contribute the same URL, generation
selects the deepest compatible layout ancestry. Static composite routes take
precedence over less-specific parameter routes. Incompatible layout groups or
ambiguous logical route patterns fail discovery instead of silently selecting an
unrelated sibling's default/metadata/boundary ancestry.

Branches are buffered before their own content hashes are generated, including
deferred nodes within those branches. Independent concurrent/streamed slot
loading remains pending.

Limits are sixteen composed/rendered slots per document (including implicit
children), eight levels of slot nesting, 128 matching branch pages and one
default per slot, 8,192 unique registered route nodes per application, a 16 KiB
navigation carrier, 4 KiB selected URLs and 256-byte identity labels. Startup
rejects cyclic graphs and computes maximum composed count/depth across branch
choices. Registration freezing and identity hashing handle shared subgraphs
without expanding every selection combination. State uses only same-application
logical URLs; schemes, credentials, authorities, fragments, dot/encoded-slash
paths and malformed identifiers are rejected. Rendered hashes and selections
are public browser data. These limits do not measure the complete Go heap or
browser DOM memory.

Static export of a selected slot page is rejected before publication; the prior
output remains intact. Unrelated concrete pages in the same application can
still be exported. The framework does not yet publish static data for the full
composite selection space.

## Example and verification

Run the usual example command and visit `/boards/chart`, then select Settings.
The analytics counter keeps its DOM/state while team changes. Increment the
primary chart counter and select Activity: its primary DOM/state and title
survive while team changes. Reload Activity to see both deterministic defaults.
The example uses the existing Go counter island; no new JavaScript is introduced. The analytics shell has its own Go counter starting
at 40; it survives default/matching-branch replacement and history restoration
under `PreserveLayouts`. The analytics default also demonstrates slot-local
error/not-found boundaries. `X-Demo-Default-Failure: error` or `missing` selects
those deterministic examples; `X-Demo-Analytics-Metadata-Deny: yes` exercises a
metadata denial. These headers are test probes; the `/restricted` example
contains the application's role/authorization example.

Select Drill after incrementing the chart and analytics counters. `/boards/drill`
is supplied by the nested detail slot; both primary selections can survive while
the detail counter changes. Navigate to Settings to retain the unmatched nested
branch, then use back/forward. The detail layout has a separate counter starting
at 60. `X-Demo-Detail-Version: updated` changes the nested drill data; its parent
page remains mounted. `X-Demo-Detail-Deny: yes` demonstrates nested denial and
ancestor cleanup. Reports demonstrates a named slot inside the retained implicit
primary; `X-Demo-Report-Deny: yes` exercises its data-page denial.

Select Reports, increment Report page and Reports shell, then select Summary.
`/boards/reports/summary` composes the boards and reports primary owners. Soft
navigation retains Report page while the summary side branch changes. Drill then
retains that entire report composition through the boards owner. Back/forward
restores each owner's URL selection independently. A direct Summary load uses
Report default; it does not use the outer boards default because the nested
reports composition matches. `X-Demo-Report-Summary-Deny: yes` exercises denial in
a retained side branch. Both HTTP and Lambda fixtures run this sequence.

Native/race tests cover direct defaults, tenant scoping, reauthorization,
response-control rejection, global 404 compatibility, registration/discovery and
carrier validation. Browser tests verify useful SSR before WASM, hydration node
identity, independent replacement/cleanup, history selections, denial cleanup and
malformed-response retry, implicit-primary hydration/retention/history,
changed server output, deterministic reload, incompatible-deployment history
recovery and primary authorization denial. Generated HTTP and both mock Lambda executable
fixtures verify the same server selection/authorization contract outside the
source directory. Cross-engine and static-host checks qualify the stated subset.

### Reading active segments

Inside an owning layout, `c.Selection("children")` returns a `routeinfo.Selection`
with an opaque parent-bound `Scope`, a slot `Name`, and the active `Segments` below
that layout. An empty name also selects children. Use a declared slot name to read
its branch. `c.SelectedSegments(name)` and `c.SelectedSegment(name)` provide slice
and first-segment conveniences; an index/default branch has no segment.

```go
active, err := c.Selection("analytics")
if err != nil { return nil, err }
// Pass active in island props to implement an active-tab indicator.
```

Named reads resolve the slot once and share its loader/authorization result with
`c.Slot(name)`. They can therefore return loading/authorization errors. Reading
selection metadata does not grant access to any resource. Request queries are
excluded; parameters are decoded, catch-all values occupy one joined segment,
and transparent `group_` folders are omitted. The last point differs from the
Next.js selected-layout-segments hook, which can include route-group segments.

Generated routes supply layout identities and logical bases. Programmatic ordinary
layouts using this API must supply matching `LayoutIDs` and `LayoutBases`; declared
primary owners already carry their identity/base. Calls outside an owning layout
return `ErrSlotScope`. Selection slices are independent snapshots.

Browser islands use `client.CurrentSelection(scope)` for a committed snapshot or
`scope.ObserveSelection(id, callback)` for an immediate read and subsequent changed
commits. The callback receives `(client.Selection, bool)`; false means the selection
is absent. Subscriptions are removed with island cleanup; the returned stop function
also unsubscribes. Callbacks must return promptly. Retained layouts observe their
active branch even when the address names a different parallel branch. Updates do
not replace SSR nodes. The boards and reports selection badges demonstrate this.
Browser observations describe mounted roots: compose a named slot's returned node
into the layout for that selection to be present in the committed snapshot.

After decoding props and requiring an existing output element in `Mount`:

```go
_, err = scope.ObserveSelection(props.Active.Scope, func(active client.Selection, present bool) {
    if !present { output.Text(""); return }
    label, ok := active.Segment()
    if !ok { label = "index" }
    output.Text(label)
})
return err
```

Selection metadata travels as escaped JSON attributes and is public browser data.
The browser validates bounded counts, bytes, scope identities and duplicate scopes
before committing navigation. Component props must not contain secrets. This is
a Go API, without React hook or Next.js source compatibility.

A snapshot allows at most 128 selections and 64 KiB of encoded attributes; each
selection allows 256 segments totaling 4096 bytes. At most 512 browser subscriptions
may be active. Repeated unsubscribe/resubscribe cycles reuse the island's cleanup
registration. Invalid initial metadata leaves SSR content intact, reports a hydration
error and allows the next valid navigation to replace that invalid view and recover.
Invalid navigation metadata preserves the existing interactive view and history.

Outstanding work includes independent concurrent/streaming loading, head composition,
scoped access recovery, complete [interception combinations](INTERCEPTING_ROUTES.md) and full
production resource/accessibility/deployment qualification. All remain tracked
in [PARITY.md](PARITY.md).

Reference: [Next.js parallel routes](https://nextjs.org/docs/app/api-reference/file-conventions/parallel-routes).
See also [selected layout segments](https://nextjs.org/docs/app/api-reference/functions/use-selected-layout-segments).
