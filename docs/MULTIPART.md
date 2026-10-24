# Go browser multipart requests

`client.Multipart` stores bounded text fields and opaque browser File handles.
`client.Fetch` turns it into a fresh native FormData body for each request.
`mutation.Contract.CallMultipart` uses the same body with the contract's endpoint,
revision, JSON response decoding and origin/redirect/cancellation policy.

This uses standard [FormData entries](https://xhr.spec.whatwg.org/#interface-formdata)
and [Fetch body encoding](https://fetch.spec.whatwg.org/#bodyinit-unions).
The browser chooses the multipart boundary. Application JavaScript is not needed.

## Explicit fields and files

Inside a `BindSubmit` handler, use the owned request context:

```go
form, err := client.NewMultipart(1 << 20)
if err != nil { return client.FormResult[Draft]{}, err }
defer form.Close()
if err := form.AddField("title", input.Title); err != nil {
    return client.FormResult[Draft]{}, err
}
for _, file := range input.Files {
    if err := form.AddFile("files", file); err != nil {
        return client.FormResult[Draft]{}, err
    }
}
reply, err := mutations.UploadFiles.CallMultipart(ctx, form,
    mutation.CallOptions{MaxInputBytes: 1 << 20})
```

The contract's server binding must provide `BindOptions.Form`. That mapper reads
text values and `Context.Request.MultipartForm.File`, builds the shared Go input
and performs domain validation. The caller explicitly maps field names and file
handles; `CallMultipart` does not reflect an arbitrary input type into a form or
provide generated typed field stubs. Output decoding remains typed as the
contract's `O`. Authorization still belongs to the server binding/handler.

`AddField` permits repeated names and preserves entry order. Names follow the
form API's 128-byte name rules; values must be valid UTF-8. `AddFile` accepts a
`client.File` from a file binding, validates its metadata, and requires a nonempty
name without NUL/CR/LF. File metadata remains untrusted. The browser's native
filename/value encoding applies, including multipart escaping/line-ending rules.
The server mapper must define its normalization policy.

For ordinary browser HTTP requests, use:

```go
response, err := client.Fetch(ctx, "/api/upload", client.FetchOptions{
    Method: "POST", Multipart: form, MaxBytes: 64 << 10,
})
```

Do not set Content-Type: the browser supplies its boundary. A multipart body
cannot be combined with `Body`, GET or HEAD. `FetchStream` accepts the same request
body while streaming a bounded response; this does not add a custom upload-stream
API. Generic Fetch retains normal browser CORS/redirect behavior. Typed
`CallMultipart` enforces the exact same-origin endpoint, same-origin credentials and no-store cache
policy, redirect rejection and revision/protocol checks used by JSON `Call`.

## Ownership and accounting

Each dispatch builds a private native FormData snapshot before awaiting network
work. Later builder edits or `Close` do not alter that body. Files are immutable
browser handles; this path does not call File.Bytes, copy file contents into Go,
or base64-encode them. Browser encoding/network memory remains browser-managed;
there is no zero-copy or bounded-browser-RSS guarantee.

`Close` drops builder-owned references and is idempotent. It does not cancel a
request already in flight; cancel its context. A closed/zero/nil builder cannot
be dispatched or mutated. JSON serialization of a builder returns an error.

A builder has a required accounting limit from 256 bytes to 64 MiB, at most 128
parts and at most 64 file parts. Accounting starts at 256 bytes and reserves
1,024 bytes per part, three times name/filename/media-type byte lengths, twice
text-value byte lengths for encoding/line endings, and file content size. Failed
adds do not change the builder. `BudgetBytes()` reports this allowance, not the
actual browser-generated HTTP length. `CallOptions.MaxInputBytes` checks that
allowance before dispatch; its default remains 64 KiB.

Receivers must enforce actual whole-request limits independently. Framing and
browser encoding count toward those limits. The current sample chooses 1 MiB
for both caller accounting and server request limits, with enough room for its
two 256-KiB files and small fields. Larger application/provider limits require
separate validation. These per-builder limits do not bound caller allocations,
retained builders/results, concurrent requests or multiple Go runtimes.

## Examples and qualification

`/uploads` now sends native multipart from Go after hydration. Its ordinary POST
and named native submitter use the same server form mapper. `/uploads?transport=json`
keeps the explicit bounded byte-read/base64 DTO recipe. `/file-preview` still
reads/hashes locally without network uploads. Binary/empty contents, delayed
responses, newer selections, cancellation, no-JS POST and receipt SSR share the
[file-control contract](FILES.md).

Focused WASM tests check quotas, failed-add atomicity, repeated fields, File
handles, private snapshots, close/serialization behavior, ambiguous Fetch options,
request markers and typed response decoding. A real Chromium check disables
FileReader and verifies a multipart request with exact binary/empty-file hashes.
The frozen multipart/shared-transport matrix passes **292 runtime and 92 static
checks**: 73/23 on Chromium and 219/69 on Linux Firefox/WebKit/touch. Runtime
coverage includes controlled forms, composition, typed mutation protocol,
redirect/origin protection and optimistic updates. Both Go SDKs pass native
race/vet, matching core/module WASM tests/vet and route-generation drift checks.
Standalone HTTP and both mock Lambda suites pass on host and nonroot/read-only
Linux. The eighteen-step executable form matrix includes multipart requests
returning JSON as well as native redirects. Logs are under
`.nextgo-test-results/qualification/multipart/`.

A [local body-size observation](PERFORMANCE.md) records 524,759 multipart versus
699,167 JSON bytes for the same two synthetic 256-KiB files. This does not prove
throughput, memory, real-network, large-file or production performance. All
release gates remain open.

The server mapper still buffers parsed multipart under its action request limit.
Custom streaming request bodies, chunked/resumable upload protocols, upload
progress, direct object-storage integration and production cloud/resource/device
qualification remain open. The complete [parity inventory](PARITY.md) retains
those requirements.
