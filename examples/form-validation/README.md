# Public form validation example

This dedicated Go/WASM example combines two independent server-rendered forms,
explicit radio presence, repeated select values, and per-field asynchronous
validation. It previews public synthetic input; it does not save a record or
provide authentication or private data handling.

Run from the repository root:

```sh
go run ./cmd/nextgo dev ./examples/form-validation
```

`NEXTGO_FORM_VALIDATION_BASE_PATH` and
`NEXTGO_FORM_VALIDATION_ASSET_PREFIX` configure mounted routes and public assets.
Ordinary links and native POST work before WebAssembly starts. The root page
declares `RequireServer`, so a plain static export rejects its API dependency.

Each name/code field opts into input/blur checking after a short debounce, with
an explicit check and retry button. `reserved` produces a public field error;
`offline` simulates an unavailable check; `slow-reserved` produces a delayed
rejection. Checks use request cancellation. New edits, reset, submission and
component disposal fence older results. Leaving the page releases the form's
scope; returning starts a new preview. No asynchronous request runs at binding.
The field status regions and errors are connected to their controls and remain
visible independently for the primary and secondary forms.

The optional radio has two separate states: no selection and a selected option
whose value is empty. The Go model uses `client.RadioChoice`; native POST reads
the field's presence, rather than collapsing both states through `Form.Get`.

The first two select options deliberately share `value="notes"`. The Go model
uses selected option indices and the JSON preview returns those exact indices.
Native POST submits repeated **values**. Its receipt preserves that multiplicity
and does not infer which equal-valued option was selected. Reset restores the
confirmed model's exact option identity. The application uses
`client.SelectedOptionValues` to show the native value sequence in DOM order.

`/api/validate` is a bounded no-store public GET; `/api/preview` accepts bounded
JSON or URL-encoded POST and validates again on the server. Named native submit
buttons deliberately bypass Go enhancement. Their HTML receipt contains escaped
public input and repeated values. The JSON preview preserves typed option
identity. A successful response confirms a preview, not persistence.

Native checks cover actual handlers, cancellation, payload/domain bounds,
selected-empty presence, repeated-value receipts, exact JSON option identity,
escaped text and useful mounted SSR:

```sh
go test -race ./examples/form-validation/...
```

All application behavior is Go. Browser fixture JavaScript is development-only.
