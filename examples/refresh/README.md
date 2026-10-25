# Explicit public Go state refresh

This isolated application demonstrates development replacement of an opted lazy
Go/WASM component. Two counter instances use stable explicit IDs (`primary` and
`secondary`) and public scalar cells named `count`, `label`, and `enabled`.
The component declares versioned state/DOM schemas in `component.json`; its
native props, server view and shared code keep clear build-tag boundaries.

From the repository root:

```sh
go run ./cmd/nextgo dev ./examples/refresh
```

Edit the actual compiled constants in
`components/refreshcounter/client_js.go`: `clientProof = "v1"` and
`incrementStep = 1`. An accepted component-only replacement can retain the
existing counter nodes, public state, label input/focus and document URL/history.
Native `/api/status` returns a public opaque instance token and monotonic request
count, so fixtures can verify the native child stayed alive. The token is not a
credential or operating PID. No application JavaScript implements these rules.

Refresh has explicit boundaries. Native code, shared code used by both targets,
props, schemas, eager components and environment changes use normal rebuild and
full reload. A managed `Scope.Go` worker starts a delayed `client.Fetch` only from
the public work button; its physical activity makes module preparation fall back to a
document reload. Arbitrary workers, resource stores, forms, optimistic tickets
and private data are not serialized by this sample. Cleanup cancels work when
the component leaves its owner scope.

`NEXTGO_REFRESH_BASE_PATH` and `NEXTGO_REFRESH_ASSET_PREFIX` configure deployment
paths; `NEXTGO_REFRESH_PUBLIC_NOTE` explicitly publishes a harmless native note.
The shared root layout contains an ordinary eager Go badge, and `/details` has
no lazy counters, making dependency classification and unused-module behavior
observable without changing an existing application.

Production and static artifacts use ordinary lazy components and contain no
development replacement contract. Static export omits the server diagnostics and
delayed-work controls, while both interactive counters remain usable. It does
not advertise nonexistent API endpoints. The public CSS is original local data.

Native checks validate meaningful SSR, distinct instances, deployment paths,
static/runtime control differences, opaque native-instance/request behavior and
bounded cancellable work. Browser/deployment fixtures separately qualify real
module ordering, state preservation, fallback and production transport behavior.
