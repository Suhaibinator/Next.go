# Browser file inputs and bounded uploads

`client.BindFiles` binds an existing `<input type="file">` to a Go slice of
opaque `client.File` handles. `File.Bytes(ctx, limit)` reads selected bytes into
an owned Go slice using an abortable native reader. Both APIs are browser-only.
The native server and browser share explicit upload DTOs and domain validation;
file handles do not enter the server import graph or serialized island props.

The implementation uses the browser [File API](https://w3c.github.io/FileAPI/),
including immutable File objects and abortable FileReader operations. Selection
restoration uses the HTML [file-input API](https://html.spec.whatwg.org/multipage/input.html#dom-input-files)
and [DataTransfer](https://html.spec.whatwg.org/multipage/dnd.html#the-datatransfer-interface).
Modern browsers providing those APIs are required for restoration.

## Binding and model ownership

Define a browser draft separately from shared server props:

```go
// In client_js.go (js && wasm).
type Draft struct {
    Label string
    Files []client.File
}
model, err := client.NewScopedForm(scope, Draft{Label: initialLabel},
    client.FormOptions[Draft]{
        Clone: func(value Draft) Draft {
            value.Files = slices.Clone(value.Files)
            return value
        },
        Equal: func(a, b Draft) bool {
            return a.Label == b.Label && client.SameFiles(a.Files, b.Files)
        },
        Validate: validateDraft,
    })
if err != nil { return err }
err = client.BindFiles(scope, input, model, "files",
    func(value Draft) []client.File { return value.Files },
    func(value *Draft, files []client.File) { value.Files = files },
    client.FieldBindingOptions{Error: errorsElement, ValidateOnBlur: true})
```

Render an empty file input, a label, an error region and `aria-describedby` on
the server. A server cannot preselect local files. Hydration retains the input
and adopts a live selection made before WASM loaded. Do not give the model an
initial file selection through SSR props. `File.MarshalJSON` returns an error;
explicitly read bytes into a wire DTO when sending them to a server.

`SameFiles` compares browser file identity in order. It distinguishes a new
selection with the same name/size from the previous object. `File.Info()` returns
owned metadata: `Name`, `MediaType`, `Size` and Unix-millisecond `LastModified`.
Metadata is user input, not content validation or authorization. Immutable handles
can be shallow-copied, but their containing slices need an explicit clone.

Bindings share the [form model](FORMS.md)'s atomic capture, field errors, touched
state, pending edits and cleanup. Model edits can clear a selection or restore
previously selected handles using DataTransfer. Reset restores the confirmed
baseline. The local example retains confirmed handles to demonstrate restoration;
the server example clears submitted handles after confirmation and preserves a
newer selection made while the older submission was pending.

Selection shape (input type/name/multiple) is fixed for the binding lifetime.
Dispose and rebind after a schema change. Framework-owned selection limits are
64 files per input, 4,096 UTF-8 bytes per name and 1,024 bytes per media type.
Application validation should impose its own count, size and name policy.

## Reading bytes and cancellation

Inside a `BindSubmit` handler, the context belongs to the submission:

```go
for _, file := range input.Files {
    data, err := file.Bytes(ctx, 256 << 10)
    if err != nil { return client.FormResult[Draft]{}, err }
    // Populate a shared DTO containing Name and []byte, then call a contract.
    _ = data
}
```

Call `Bytes` from a goroutine, outside a synchronous DOM event callback.
`BindSubmit` already runs its handler in one. The required limit is positive and
at most 8 MiB. Zero-byte files are supported. An oversized file is rejected before
starting a read. At most eight read calls are admitted at once per Go/WASM
runtime, including completed buffers waiting for their caller to return.
`ErrFileTooLarge`, `ErrFileReadBusy` and `ErrFileRead` identify public failures.

Cancellation aborts an active private FileReader. Its loadend listener is removed
before the Go callback is released, including cancellation and setup errors.
Unmounting a scoped form cancels its active ticket, so reads using that ticket's
context stop too. Copies of a File handle and reads using independently owned
contexts remain the caller's responsibility.

Each read buffers the whole selected file in browser and Go memory. The returned
slice belongs to the caller. These limits do not bound caller-retained results,
application concurrency across runtimes, DTO cloning, subsequent encoding or
browser process memory. Streaming/chunked reads are not implemented.

## Examples and transport

`/uploads` renders an ordinary multipart POST form. Before WASM, or with the named
native submitter, the browser sends multipart bytes. After hydration the Go
handler builds [native multipart](MULTIPART.md) from File handles and calls
`mutations.UploadFiles.CallMultipart`; it does not read/base64-encode contents in
Go. `/uploads?transport=json` retains the explicit `File.Bytes` and byte-DTO
recipe, where Go JSON encodes bytes as base64 before sending.

Both paths reach the same domain validation and SHA-256 calculation. The sample
allows one or two files of at most 256 KiB each and an 80-byte label. Its JSON and multipart
callers explicitly raise the default mutation input budget from 64 KiB to 1 MiB;
the action router also caps the whole body at 1 MiB. Multipart framing and base64
encoding count toward their respective transport limits. Foreign origins,
unexpected fields, invalid names/counts and oversized contents are rejected.
Native redirects return fresh SSR receipt metadata with empty file inputs.

Bytes are not stored by the demo application. It retains only public receipt
metadata, capped at 256 process-local records; records can be evicted or lost on
restart and are not shared between serverless instances. This is not durable,
authenticated or multi-instance object storage. Applications need their own
identity, content policy and persistent/shared storage before offering uploads.
Metadata such as MIME type and names does not establish that content is safe.

`/file-preview` uses the same binding and bounded Go reads locally, computes its
receipt in Go, and sends no selected bytes to a server. It supports static export.
Local read/reset buttons are disabled until WASM is ready. Both examples reserve
error-region space so validation feedback does not move a pressed action button.

## Verification and remaining work

Native tests cover domain limits, exact binary/empty contents, metadata ownership,
cancellation and multipart/JSON consistency. WASM tests cover byte ownership,
opaque serialization, identity, limits, read failures, callback cleanup,
cancellation/late events and admission. Focused Chromium preview checks cover SSR
input identity, pre-WASM selection, typed and native binary transfer, no-JS POST,
newer selections, handle restoration, read abort and detached listener cleanup.
The frozen upload subset passes **212 runtime and 92 static-host checks**: 53/23
on Chromium and 159/69 on Linux Firefox/WebKit/touch. Existing controlled-form and
composition regressions are included. Both Go 1.24.13 (Linux) and Go 1.27.3
(macOS) pass full native race/vet, matching core/module WASM tests/vet and
route-generation drift checks. Standalone HTTP and both mock Lambda suites pass
on host and nonroot/read-only Linux. The executable form matrix now has seventeen
steps, including byte DTOs, base64-wrapped binary multipart events, native receipt
redirect/fresh SSR, rejection and local preview. Logs are under
`.nextgo-test-results/qualification/uploads/`.

Those initial 304 focused checks qualify the stated subset, not every browser/form
combination or a production release. Native OS chooser interaction, real devices,
screen readers, large/long-lived upload pressure and actual cloud/storage/identity
qualification remain open. [Current bundle measurements](PERFORMANCE.md) still
exceed the proposed eager size budgets.

The newer [native multipart transport](MULTIPART.md) also passes the expanded
292-runtime/92-static matrix, both SDKs and host/Linux HTTP/both mock Lambda
suites. It shares the JSON caller's protocol and origin policy. The bounded
base64 DTO path remains available with `?transport=json`.

Chunked/streaming/resumable uploads, direct object
storage, progress reporting, drag/drop/directory controllers, persistent receipt
storage, complete choice/file schema conformance and production resource,
identity, device, accessibility and cloud qualification remain open in
[the complete inventory](PARITY.md).
