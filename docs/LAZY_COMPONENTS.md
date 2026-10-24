# Lazy Go browser components

Lazy components are an opt-in, bounded form of component code splitting. SSR continues to render the complete island. The browser downloads its Go implementation when that island first appears in the active DOM, including content arriving through a streamed boundary. Pages with no lazy islands do not request those modules. Eager components remain the default.

Put `component.json` beside a component's `client_js.go`:

```json
{"loading":"lazy"}
```

The component keeps the ordinary shared props, server rendering and browser `Mount(*client.Scope, json.RawMessage) error` API. No different rendering API or hand-written JavaScript loader is needed. `{"loading":"eager"}` or deleting the file restores the default. Unknown fields, duplicate fields, invalid modes and files larger than 1 KiB fail discovery. Development watches this file and keeps the preceding successful build when it is invalid.

Try `/lazy`, `/lazy/details` and `/lazy/stream` in the demo. Two independent lazy counters share one downloaded implementation, with an eager counter available while the lazy download is pending. The counters validate their server markup and attach handlers to those nodes. They do not reconstruct the initial tree. Serialized props are public browser data.

## Runtime and build contract

The standard Go compiler produces separate executable WASM programs for these components. This implementation does not dynamically link Go packages into the core runtime. Each opted-in package has its own Go scheduler, heap, globals and copied dependencies. Avoid assuming that a package global in one component runtime is the same Go value in another runtime. Ordinary props, public configuration, server APIs and explicit browser storage are appropriate communication boundaries.

The CLI compiles core and module executables as separate Go targets. It reserves the `nextgo_module` build tag for component executables, where a compile-time constant selects bridge forwarding instead of local router ownership. Do not set that tag in application `GOFLAGS`; the CLI reports an error. Calling `client.Run` from a module or `client.RunModule` from a normal target fails before installing document behavior.

Application tags selected through `GOFLAGS` remain enabled in both browser targets. The CLI reads Go's resolved setting, including persistent Go environment configuration, and merges the private module tag without overwriting application tags or other flags. Dependency validation uses the same target flags as compilation, including dependencies selected only for modules. Legacy quoted/space-separated and modern comma-separated tag forms retain Go's file-selection semantics. Changing the parent environment or persistent Go flags requires restarting development; this does not add a watch on Go's environment configuration file.

The core bundle imports eager components and registers generated descriptors for lazy packages. Each descriptor binds the component name, source epoch, byte size and SHA-256 hash. A module filename is `module-<sha256>.wasm`, published beside `app.wasm` under the same versioned runtime directory. All module identities participate in the build version. The CLI validates each module's browser import graph using the same rules as the core: server routes, loaders and unguarded server-only dependencies cannot enter it. Native and browser builds use their selected Go compiler; runtime support is copied from that compiler. Go requires matching compiler/runtime support versions. [Go WebAssembly guidance](https://go.dev/wiki/WebAssembly).

The Go core fetches a module, checks its final URL, status, exact size and digest, instantiates it through browser WebAssembly APIs and starts another instance of the supplied Go runtime. A versioned handshake checks the expected component and source epoch before accepting exports. The small JavaScript bootstrap remains responsible for starting the core; component loading and lifecycle policy are implemented in Go.

The bridge exchanges DOM references, JSON snapshots and numeric scope handles. Go pointers never cross instances. The core owns document navigation, history, streaming and cache policy. Module router calls delegate to it; public settings, committed location and route selections synchronize from its snapshot. Islands retain their own local state and scoped listeners/subscriptions. Unmount disposes those scopes before DOM removal. A downloaded runtime can mount later islands without another fetch, including after back/forward navigation. Retained layout rules apply to island roots as before.

## Bounds and failure behavior

- At most eight lazy component packages may be registered per application.
- At most two module loads/instantiations run concurrently. A load, including waiting for admission, has a twenty-second deadline.
- Each module is limited to 8 MiB of uncompressed WASM. Fetch checks are followed by an exact SHA-256 verification before execution.
- Loads for the same component are deduplicated. A shared load can finish after its original island unmounts; that detached island is never mounted. The runtime remains available for future islands.
- A module download failure retains SSR, marks hydration as failed and offers a Go-powered retry. One retry control handles failed downloads; navigation cleans its listener. Mount validation errors and recoverable Go mount panics clean partially registered effects and retain the server tree.
- Loaded runtimes stay alive for the document lifetime. Active arbitrary Go goroutines/callbacks cannot safely be unloaded by dropping JavaScript references. Eight runtimes is an admission limit, not a measured memory budget.

A WASM trap, runtime exit, or JavaScript exception that escapes the Go runtime can invalidate that instance. Reload the document to recover; this implementation cannot promise recovery of an arbitrarily damaged Go runtime. Application code must keep browser event callbacks short, start asynchronous work in goroutines and register cancellation/cleanup with its scope. JavaScript interop exceptions are not interchangeable with a recoverable Go panic.

## Deployment and tradeoffs

Production binaries embed all runtime files. The same artifacts work through the native server and the Lambda adapter; module assets use the existing compression/cache headers. Static export copies the runtime tree and lazy descriptors, so a plain static host can serve them too. Separate asset prefixes must supply the existing CORS policy. Deploy the native binary and its generated runtime assets together; do not combine hand-picked files from different generations. Development retains preceding asset generations during successful transitions and preserves the last working server after compilation/configuration failures.

This design can reduce the application code requested on pages that do not use a lazy component. It adds a full copied Go runtime and dependencies when the component is used, so total download and memory can increase. Use it for occasional features after measuring representative workloads. It does not automatically split by route, share a single Go heap, group common dependencies, preload based on a route-to-component graph, or guarantee that eight modules fit on a low-memory device. No blanket performance improvement is claimed. See [performance evidence and open gates](PERFORMANCE.md).

## Verification and remaining work

[Build/discovery tests](../internal/project/lazy_test.go) check opt-in configuration, bounds, fingerprint changes and generated import separation. [Compiler-target tests](../internal/project/browser_tags_test.go) compare original and merged tags against Go-selected files, compile an independent application with required tags, and reject a module-only server dependency. [WASM tests](../client/lazy_js_test.go) cover descriptor validation and partial-mount cleanup for Go errors/panics. Separate target tests verify core/module entrypoint ownership, router API forwarding, replacement options and host error propagation. `npm run test:wasm` runs core tests and the private-target bridge checks. [Browser scenarios](../tests/lazy.spec.mjs) cover delayed useful SSR, DOM identity, independent counters, no unused download, deduplication, router/location/history integration, unmount during load, detached controls, network/digest retry, failed mounts, streamed arrival and mounted/CDN/slash/locale paths. Development, plain static hosting and source-independent HTTP/both mock Lambda suites include lazy-module checks.

Automatic route splitting/grouping, cross-runtime application context/effects, advanced modal/slot/animation/mutation integration, high-module-count memory/resource pressure, cold-start/real CDN/device measurements, long-lived-document recovery and independent production security qualification remain open. The complete target remains recorded in [PARITY.md](PARITY.md).
