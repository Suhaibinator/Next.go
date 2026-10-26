# Explicit client-only view boundaries

This bounded implementation has scoped native/WASM, production and browser
checks on Go 1.24.13 and 1.27.3. It adds an explicit component boundary, not a
full client-only application/router mode. The qualification scope and limits
are recorded below; no capability or release gate closes.

## Choose a boundary deliberately

Ordinary `ui.Island(name, props, body)` renders its useful body on the server.
Its Go `Mount` function attaches behavior to those existing nodes; hydration does
not render or replace the subtree. Keep that model when server-rendered content
is the actual view.

`ui.ClientIsland(name, props, fallback)` instead renders a useful
server fallback. Once the component is available, its Go `Mount` calls
`scope.ClientView(node)` to prepare a browser-created view and replace only that
boundary's children. The outer island node stays in place. A lazy component also
waits for its existing module download. This is an explicit component boundary,
not a client-only page router or a promise to turn an application into an SPA.

A native component factory can return:

```go
func View(p Props) ui.Node {
    return ui.ClientIsland("browserpanel", p,
        ui.El("p", nil, ui.Text("The browser panel is not available yet.")))
}
```

Keep `Props` in shared Go code and browser work in `//go:build js && wasm` files.
Register/discover the component using the ordinary eager or lazy component
conventions. Props are serialized public browser data; do not put secrets or
server-only values in them. This additive boundary does not change the paired
module ABI or its existing compiler/runtime ownership rules.

## Mount once and use the returned child scope

`(*client.Scope).ClientView(ui.Node) (*client.Scope, error)` is available only
during that explicit boundary's `Mount`. Call it exactly once and return any
error. An ordinary island, an inactive/detached boundary, a second attempt or a
call outside Mount is rejected. Render the immediate view synchronously; do not
call it later from an event or a background goroutine.

For example, this browser `Mount` uses the same public `Props` type as its native
factory:

```go
//go:build js && wasm

package browserpanel

import (
    "encoding/json"
    "strconv"
    "syscall/js"

    "github.com/Suhaibinator/Next.go/client"
    "github.com/Suhaibinator/Next.go/ui"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
    p, err := client.DecodeProps[Props](raw)
    if err != nil { return err }
    view, err := scope.ClientView(ui.El("section", nil,
        ui.El("h2", nil, ui.Text(p.Label)),
        ui.El("p", nil, ui.Text(js.Global().Get("navigator").Get("language").String())),
        ui.El("output", ui.Attrs{"data-count": "", "aria-live": "polite"}, ui.Text("0")),
        ui.El("button", ui.Attrs{"type": "button", "data-increment": ""}, ui.Text("Increase"))))
    if err != nil { return err }
    count, err := view.Require("[data-count]")
    if err != nil { return err }
    button, err := view.Require("[data-increment]")
    if err != nil { return err }
    value := 0
    view.On(button, "click", func(client.Event) {
        value++
        count.Text(strconv.Itoa(value))
    })
    return nil
}
```

Here `Props` can be `struct { Label string }`. Bind events, cleanup and child
behavior through the returned scope, which owns the new inner tree. Mount must
return promptly. Later changes use explicit Go state/DOM APIs; `ClientView` is
not a rerendering or reconciliation loop. Framework-owned asynchronous work
still follows its existing scoped work and physical-settlement contracts.

## Fallback, bounds and failure ownership

A useful fallback remains when WASM is disabled, missing or delayed. It can have
ordinary native content and controls. Successful mounting replaces its children,
so fallback edits are not automatically transferred into the browser view.

Both fallback and prepared view must be immediate bounded UI. The fallback has
a 64 KiB rendered-byte bound and 2,000 typed-element bound; the browser validates
the actual detached tree, including a 2,000-descendant-element limit. Existing
prepared-tree byte limits also apply. Nested ordinary/client islands, deferred
boundaries, framework markers and script/style/head declarations are forbidden;
this is not an arbitrary HTML or third-party JavaScript injection API. Custom
`ui.Node` implementations remain trusted application code, with rendered browser
markup separately validated before it can be committed.

The framework prepares and validates a detached tree, retains the original
fallback nodes during Mount, and checks that the boundary still owns its root,
parent and committed inner view. A returned Mount error, a recovered panic or
`runtime.Goexit` during Mount takes the failure cleanup path: owned scoped work
is disposed and the original fallback nodes are restored, including captured
focus/selection where applicable. Mounting without calling `ClientView` also
fails. This is not process/runtime-exit recovery, nor rollback of arbitrary
application DOM mutations, network effects or global library side effects.
Use explicit scope cleanup for any browser library resources you create.

After success, ordinary component disposal owns the new scope and listeners.
The discarded fallback is not a hidden retained route. Development refresh
currently treats a used client view as unsupported for in-place state transfer
and takes the existing full-reload path; no new hot-transfer contract is added.

## Routing and static hosting

Static-host navigation data already exists independently of this candidate.
For declared exported paths, the exporter publishes both useful HTML and
`/_nextgo/data/<path>.json` (with `index.json` for the root). The existing Go
navigation runtime selects those envelopes for static output. Existing static
fixtures cover same-document navigation/history and declared group/catch-all
paths, with mounted and locale variants. This work does not newly implement
that data path.

The new boundary still uses the existing page navigation transport: native
servers provide page envelopes, and static hosts need the already exported
page envelopes. It does not synthesize unexported pages, resolve arbitrary
client-only routes or provide a catch-all host rewrite/fallback. Static-host
qualification of the new boundary, broader retained-layout/streamed-insertion/
refresh interactions, complete client-only page routing and physical-device
qualification remain pending.

## Sample and proof boundary

The [`/client-only` page](../examples/demo/app/client-only/page.go)
combines an ordinary hydrated counter with two eager and two lazy browser-view
boundaries. The scoped browser cases check delayed WASM/modules, independent
state and stable outer roots, explicit error/panic fallback restoration with
focus, detached-listener disposal/navigation return and a superseded lazy
module download. Optional public `fail=error` and `fail=panic` modes deliberately
report demonstration failures; the default sample mounts normally.

The frozen source `583752a4` passes 13 native/WASM/vet/format commands per
Linux ARM64 Go 1.24.13 and 1.27.3 SDK. Eleven distinct new source test groups
(five UI and six client groups) pass: the UI groups run natively and in portable
WASM, and the client groups run in all four core/module development/production
personalities. These repeated observations are not additional distinct tests.
Eight existing root scalar-metadata groups pass on each SDK as regressions.
The three sample packages have no native test files; their compile/vet checks
are not native application test cases. One predeclared existing development-
module host-script test skips, with its core counterpart passing; no new
boundary group skips.

Production checks pass five steps on Go 1.24 and six on Go 1.27, including a
Darwin cross-build in the latter. Actual native HTTP asset checks verify the
compiled core, all five lazy modules, boot and paired shim: eight runtime
assets, with source/artifact pins preserved. This is artifact/delivery evidence,
not a startup or performance result.

Five browser journeys across Chromium, Firefox, WebKit and touch WebKit pass
20 cases per SDK: 40 new cases, with no failures, skips, retries or flaky
results. Both outer controllers pass. The runs verify ordinary island node
identity beside explicit eager/lazy fallback replacement, independent controls,
error/panic fallback restoration and focus, navigation disposal/return, and
superseded lazy download ownership. They record 20 native close/join exit-0
proofs with TERM/no KILL and four browser exit-0 observations per SDK; final
source/artifact pins and owned-container cleanup checks pass. `runtime.Goexit`
cleanup has focused WASM unit coverage, not a native-Goexit browser scenario.

These checks do not qualify the new boundary on static hosts, retained layouts,
streamed insertion or development-refresh integration. Physical devices, full
framework combinations, client-only page routing and catch-all host fallback
remain open. No startup/size/CPU/RSS, cloud or full SPA/Next.js compatibility
claim follows. The earlier metadata/scalar campaigns remain their own
checkpoints; they were not rerun as this 40-case suite. All 79 capability IDs
and 11 open gates remain.

### Run the browser fixture

With the repository's Node development-test dependencies and Playwright browsers
installed, build the production demo and provide the real executable pin:

```sh
go run ./cmd/nextgo build -out /tmp/nextgo-client-only ./examples/demo
export NEXTGO_E2E_SERVER_BINARY=/tmp/nextgo-client-only/server
export NEXTGO_METADATA_STREAM_SERVER_SHA256="$(node -p 'require("node:crypto").createHash("sha256").update(require("node:fs").readFileSync(process.env.NEXTGO_E2E_SERVER_BINARY)).digest("hex")')"
npm run test:client-only
```

The shared fixture requires an absolute executable path and its actual lowercase
64-hex SHA-256, verifies those bytes and has no automatic compiler fallback.
It starts its own native process; stop manually started servers on the metadata
fixture ports first. Node/Playwright are development test tooling, not production
application dependencies. The command runs the scoped fixture described above;
it is not the complete framework qualification matrix.
