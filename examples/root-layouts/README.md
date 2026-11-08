# Independent document roots

This native Go example deliberately has no `app/layout.go`. Route groups provide
marketing (`/`, `/guide`, `/masked`) and workspace (`/dashboard`,
`/dashboard/settings`, `/dashboard/stream`) roots. `/plain` has the implicit root.

```sh
go run ../../cmd/nextgo dev .
```

The Go counter retains its value and DOM node inside one root. Ordinary links,
Go Navigate and Go Replace cross root boundaries through native document
navigation and start the destination counter at its own initial value. The
conditional `/masked?other=1` rewrite demonstrates response-root validation.

For plain static hosting, export the supported pages explicitly:

```sh
go run ../../cmd/nextgo export -out ./out -paths /,/guide,/masked,/dashboard,/dashboard/settings,/plain .
```

The static-export phase omits the conditional request rule and its link. The
streamed page remains a server-only demonstration. Root identity and state
semantics are unchanged for the published pages. Configure
`NEXTGO_METADATA_BASE_PATH` and `NEXTGO_METADATA_ASSET_PREFIX` for mounted
qualification; these intentionally match the shared deployment fixture's public
settings. No external runtime service or application JavaScript is required.

See [the root contract](../../docs/ROOT_LAYOUTS.md) and
[the unfinished parity inventory](../../docs/PARITY.md).
