# Native global error fallback

`Config.GlobalError` is the terminal fallback for synchronous page execution
errors that a route's `Error` does not handle. File-routed applications can
declare the same function in **app/globalerror.go**:

```go
//go:build !js

package app

import (
    nextgo "github.com/Suhaibinator/Next.go"
    "github.com/Suhaibinator/Next.go/ui"
)

func GlobalError(c *nextgo.Context, _ error) (nextgo.Page, error) {
    return nextgo.Page{
        Title: "Application unavailable",
        Body: ui.El("main", ui.Attrs{"role": "alert"},
            ui.El("h1", nil, ui.Text("Something went wrong")),
            ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")},
                ui.Text("Return home"))),
    }, nil
}
```

The compiler discovers only the application-root convention. It rejects nested
and slot-local `globalerror.go`; use `error.go` for a route boundary. The Go
compiler checks the function signature and its native import graph. Ordinary
`Configure` code can set or override `config.GlobalError` directly.

A successful route error boundary takes precedence. An unhandled page/layout
error, a panic, a failed route error boundary, or a failed application not-found
handler can reach the global fallback. Ordinary not-found and authorization
responses retain their existing boundaries. An authorization interrupt returned
by a route error boundary retains the existing access-boundary behavior.

The global body is rendered without route layouts, merge ownership or named
slot authority. It gets the request, parameters and query, and shares staged
response controls explicitly; the framework does not copy a used Context or its
mutex. Slot lookup from this fallback returns `ErrSlotScope`. Request context
and trusted native application code retain their normal authority: this is not
a sandbox or an authentication boundary.

Native handlers receive the actual error. The framework never automatically
serializes that error into HTML or the navigation envelope. Render a stable
public message instead of displaying the cause. The fallback normally returns
HTTP500. Existing response-control rules still apply, including explicit staged
redirects; an ordinary staged success status cannot replace the error status.

If the global function returns an error or panics, the framework logs it and
uses the generic error page. It does not recursively invoke the function or a
route boundary. Rendering/serialization failures and invalid response controls
retain their existing generic fail-closed paths. Already-committed HTML streams,
deferred metadata failures, browser runtime exceptions and independent streamed
segment recovery have separate contracts; this hook does not replace them.
The framework still owns the document envelope and runtime assets.

The current focused tests cover HTML/navigation privacy, failed layout reuse,
nearest-boundary precedence, terminal fallback failures, not-found failures,
slot ownership and shared staged controls on both SDKs. An independent module
actually compiles the native convention and its Go/WASM component. Both local
Runtime API modes execute page, layout, global-panic and local-boundary cases
under race checks. These are focused native/mock-runtime results, not hosted
cloud, browser, complete layout-failure or release qualification. R09 and all
production gates remain open. The [current native qualification](benchmarks/native-global-error-qualification.json)
also records both full 85-package native race/vet runs and 72 generated-executable
host/container cases across both SDKs and all three transports.

## Reproduce the standalone transport checks

The Linux fixture and probe are committed as text inputs under
`tests/fixtures/global-errors/`. Their separate hashes are recorded in the
qualification report; materialized temporary Go files are not part of the
ordinary repository Go-source count. The fixture declares all functions in one
file and uses empty convention files to select the compiler bindings.

Select Go1.27.3 or compatibility-only Go1.24.13 on PATH, then run from the
framework checkout:

```sh
set -eu
framework_root=$(pwd)
qualification_root=$(mktemp -d)
export GOTOOLCHAIN=local CGO_ENABLED=0 GOMAXPROCS=2 GOFLAGS=-p=2
mkdir -p "$qualification_root/application/app"
cp tests/fixtures/global-errors/application.go.txt "$qualification_root/application/app/page.go"
cp tests/fixtures/global-errors/probe.go.txt "$qualification_root/probe.go"
for convention in layout error notfound globalerror; do
  printf '//go:build !js\n\npackage app\n' > "$qualification_root/application/app/$convention.go"
done
(
  cd "$qualification_root/application"
  go mod init example.test/globalerrors
  go mod edit -go=1.24 -require=github.com/Suhaibinator/Next.go@v0.0.0 \
    -replace="github.com/Suhaibinator/Next.go=$framework_root"
)
go build -trimpath -o "$qualification_root/nextgo" ./cmd/nextgo
"$qualification_root/nextgo" build -out "$qualification_root/build" "$qualification_root/application"
go build -trimpath -o "$qualification_root/probe" "$qualification_root/probe.go"
"$qualification_root/probe" "$qualification_root/build/server"
```

The probe runs six cases over HTTP and both local mock Runtime API transports,
requires the actual native UID, checks public body/status/layout ownership and
the generated global-convention header, then requires native exit0 after TERM.
It records body hashes and join outcomes. This is positive-path conformance:
it does not prove failure cleanup, process-group quiescence, bounded log memory,
soak behavior or hosted AWS operation.

For the same checks in an isolated Linux container, copy only those two built
executables into a fresh Docker context:

```Dockerfile
FROM scratch
COPY --chmod=0555 server /server
COPY --chmod=0555 probe /probe
USER 65532:65532
ENTRYPOINT ["/probe", "/server"]
```

Build without network access and run with `--read-only --network=none
--cap-drop=ALL --security-opt=no-new-privileges --pids-limit=128 --memory=256m`.
The qualification report retains both SDKs' host/container results and immutable
binary, probe, fixture and image identities. Container mock Runtime results are
local emulation, with no hosted early-flush, IAM, scaling or rollout claim.
