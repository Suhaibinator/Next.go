# MIME parser provenance

`parse.go`, `grammar.go` and the parser corpus in `upstream_test.go` are adapted
from the installed Go 1.27.3 standard library's `src/mime/mediatype.go`, `grammar.go` and
`mediatype_test.go`. The original copyright headers and BSD license are retained
in this directory. The parser and token grammar are copied; MIME formatting,
extension databases, operating-system MIME discovery and encoded-word handling
are omitted.

The implementation retains media-type parameter validation, quoted values,
case folding, equal-versus-conflicting duplicate parameters and RFC 2231 extended
parameters/continuations. It is used by the `js && wasm` UI media helper; native
targets continue using their installed standard-library `mime.ParseMediaType`.
The public `ui.Video`/`ui.Audio` API and rendered attributes are unchanged.

`TestStandardLibraryOracle`, the differential fuzz target and the UI helper
oracle compare against the installed SDK. Run both native and matching-runtime
WASM tests on supported SDKs when updating this port. An SDK parser change must
be reviewed and reconciled explicitly; the directory is not an independent MIME
standard or a promise that future standard-library behavior cannot change.

Upstream files:

- [Media-type parser](https://go.googlesource.com/go/+/refs/tags/go1.27.3/src/mime/mediatype.go)
- [Token grammar](https://go.googlesource.com/go/+/refs/tags/go1.27.3/src/mime/grammar.go)
- [Parser corpus](https://go.googlesource.com/go/+/refs/tags/go1.27.3/src/mime/mediatype_test.go)

Installed SDK source checksums (SHA-256):

| File | SHA-256 |
| --- | --- |
| `src/mime/mediatype.go` | `2179fd195097a382a2ac3cdcbcbc6961f2770dbd61adfabfc60535c1acc5faa0` |
| `src/mime/grammar.go` | `85d1052199201e6ad52e27db50104842295dbf10e87f3914c803c6ce533772a6` |
| `src/mime/mediatype_test.go` | `ba8610fec87a074a67a7ccb9dc817e29eb40f50387a23a35253fb65264bb029a` |
