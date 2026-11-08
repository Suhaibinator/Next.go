# Scoped browser-library adapters

`Scope.Widget` gives a trusted imperative browser library one owned descendant
container. Go owns dependency loading, startup cancellation and resource
cleanup. The application supplies a constructor and registers each destructor
as soon as it acquires the corresponding resource. The framework adds no
JavaScript adapter bundle; a selected JavaScript library remains an explicit
application dependency.

```go
host, err := scope.Require("[data-chart-host]")
if err != nil {
    return err
}
widget, err := scope.Widget(host, client.WidgetOptions{
    Scripts: []scripts.Script{{
        ID: "pinned-library",
        Src: "/vendor/pinned-library.js",
        Asset: true,
        Integrity: "sha384-...", // Use the actual complete file digest.
    }},
    Mount: func(ctx context.Context, owned *client.Scope) error {
        if err := ctx.Err(); err != nil {
            return err
        }
        instance := js.Global().Get("Library").New(owned.Root.Value)
        owned.Cleanup(func() { instance.Call("destroy") })
        return nil
    },
    Ready: func(err error) {
        // Update status outside the opaque host; handle startup failure here.
    },
})
```

This is browser-only Go code, normally in a `client_js.go` component. Server
views supply ordinary useful HTML and a dedicated plain fallback container.
The real [uPlot example](../examples/widgets) contains complete, pinned SRI
values, a readable data table and eager/lazy components. Its upstream MIT
license is retained with the vendored assets.

## Ownership and fallback contract

The host must be connected, inside the calling scope and outside another
subscope/widget. It cannot be the entire island root. Existing child owners,
nested islands and other framework-managed fallback markers are rejected.
Fallback validation reuses the bounded client-fallback rules. Do not attach
other Go behavior inside that fallback or let a library move its host or mutate
outside it. A widget owns an opaque subtree, not arbitrary document code.

Plain fallback content stays visible while dependencies load. Just before
construction, its actual nodes are detached into private storage and the
constructor receives an empty host. The parent scope's `Require` cannot enter
the widget's subtree. The constructor's child scope supports the usual owned
events, effects and `Cleanup` registration.

On close or startup failure, registered resource destructors run and the
original fallback nodes are restored. Input node identity and values are
retained. Keep accessible source data outside the opaque container when the
library's own canvas/DOM lacks an equivalent accessible presentation.

`Widget.Close` is idempotent. `Widget.Ready` becomes false when any owning scope
starts disposal. The mount context is cancelled during disposal, and no ready
callback runs after cancellation. An ordinary setup failure first cleans up,
then invokes `Ready(error)` while the parent remains active. Constructor panic
or `runtime.Goexit` also closes known resources and reports startup failure.
A panic or Goexit in the ready callback closes the widget.

Constructors must observe cancellation, especially after awaiting external
work. If a constructor still completes late, resources it registers with the
closed child are disposed immediately, and fallback restoration runs again.
The host remains reserved until that worker returns: its parent cannot borrow
it through `Require`/`Subscope`, and other `Widget` admissions in that runtime
reject overlapping roots while late work can still touch it. Call
`Close`, then wait for `Settled()` before reusing a host. Successful live widgets
can already be settled; settling means startup returned, not that the widget
was closed. Update external error/status UI in `Ready`; retry after settlement.

Destructors and ready callbacks must return promptly. Do not wait for startup
settlement from a destructor or ready callback: it would wait on its own
cleanup stack. Destructors must not panic/Goexit or launch unowned cleanup work.
Arbitrary Go/JavaScript, external promises and library globals are not sandboxed
or forcibly stopped. Register cleanup immediately after successful allocation;
a library which throws before returning a resource handle must clean its own
partial allocations.

## Dependencies, bounds and deployment

Up to 16 descriptors are validated and copied before startup, then loaded in
declaration order through `LoadScript`. Early scripts belong in
`Config.EarlyScripts`. Successful source/security identities are deduplicated
for the document lifetime, including loading through the lazy-module host.
`RetryFailedScripts` uses `RetryScript`; successful code never reexecutes.
Closing a consumer cancels its wait, not shared code already downloading or
executing. There is no automatic dependency unload.

Each Go/WASM runtime permits at most 128 live widgets and 64 physical startup
workers, in addition to ordinary scope/task and script-loader limits. Cancelled
constructors retain worker capacity until they return. Host reservations remain
until the relevant work has settled; widget-to-widget reservations also apply
across separate scopes in that runtime. Use scopes supplied by component mounts
and `Subscope`. Duplicating scope owners or mutating a borrowed host through raw
DOM operations violates this ownership contract. These bounds are not a
document-wide budget across independent lazy
WASM runtimes; aggregate memory/soak qualification remains open.

`Asset: true` script sources use logical paths inside `public/`; the script
loader applies base/asset prefixes. For a stylesheet in `Page.Resources`, use
`assets.Stylesheet("/assets/vendor/file.css", integrity)`. Those descriptors are
also logical and are scoped during head assembly; do not pre-resolve them with
`MustAssetURL`. Use versioned filenames or content-addressed deployment URLs and
pin the bytes you publish. SRI verifies downloaded content, not library safety.

No server request is required for a constructor after its assets are published.
Ordinary HTTP, both Lambda modes and static export use the same client API.
Libraries still need a valid application CSP, licensed/published assets and
their own DOM/event/font/device compatibility. The uPlot example makes no
strict-CSP or universal third-party-library compatibility claim.

## Qualification status

The bounded implementation passes 72 Chromium cases across both SDKs and
HTTP/buffered Lambda/streaming Lambda/static hosting, plus 216 host/restricted
container executable cases and 108 race-instrumented executable cases. Each SDK
also passes the 80-package native race/vet checkpoint, all four WASM vet targets
and 11 package runs across four WASM test targets. The two later example/test
path corrections are covered by fresh builds, targeted vet and the complete
browser/executable matrix; framework Go code is unchanged after that broad run.

Fresh Go 1.27.3 native/core/module advisory checks report no known findings
against the verified current official database snapshot. The pinned uPlot npm
audit also reports none. Go 1.24.13 retains known standard-library findings and
is compatibility-only. These are advisory checks, not an independent audit.
Fresh bundle analysis exactly matches the qualified native executable hashes:
Go 1.27's core is 6,879,148 raw / 1,848,216 Go gzip-9 bytes and fails both
reference limits. Go 1.24's 5,672,808 / 1,517,838 fits this fixture's limits but
is not a production workaround. Library/CSS/license bytes total 54,016 and sit
outside the analyzer's runtime-only inventory.

The CI definition retains 144 cases across 16 profiles per SDK; only its four
Chromium transport profiles ran locally. Remote CI, full cross-engine/device,
strict-CSP, broader library/ESM/CSS/update adapters, document-wide resource/soak
and hosted-cloud qualification remain open. No byte budget or release gate is
waived. [Pinned qualification](benchmarks/widgets-qualification.json),
[Go 1.27 bundle](benchmarks/widgets-bundle-analysis-go1.27.json),
[Go 1.24 bundle](benchmarks/widgets-bundle-analysis-go1.24.json),
[complete inventory](PARITY.md).

Native ESM export namespaces are also available through
[ImportModule and WidgetOptions.Modules](MODULE_IMPORTS.md). This ABI 6 increment
has a separate frozen-source qualification record; the preceding widget
checkpoint below retains its original ABI 5 artifacts and test counts.
