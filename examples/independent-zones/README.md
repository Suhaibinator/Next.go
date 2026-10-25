# Independently built Go zone releases

This topology uses two different Go modules:

| Application | Module | Public mount | Initial source proofs |
| --- | --- | --- | --- |
| Alpha | `independent.example/alpha` | `/` | native `alpha-v1`, lazy Go `A1` |
| Blog | `independent.example/blog` | `/blog` | native `blog-v1`, lazy Go `B1` |

Both declare Go 1.24 as their minimum and replace the framework with the
repository root through `../../../..`. They import their own source and the
framework; neither imports its peer. The root framework module does not depend
on these sample modules. Their `go.mod` boundaries keep root `go test ./...`
from treating them as framework packages. Tests and generation run per module.
Using different supported SDKs for the independent artifacts is a separate
qualification step, not a claim made by these source declarations.

Each application owns its SSR layout, home/about pages, native process state,
typed preview action, two lazy Go counter instances and original public CSS.
Blog also owns a dynamic local-data loader at `/posts/:slug`. Missing records
remain blog 404s. The applications use framework URL/asset helpers, and their
page-specific `card.css` is a logical `Page.Resources` asset. Configure hooks do
not override the release wrapper's public origin, base path, runtime or asset
prefix. Images, managed metadata, locales and prerender policies are absent.

The root `zones.json` is only the source topology. Its public host defaults to
`127.0.0.1:4260`; backend origins and release hashes do not belong in checked-in
source. Fixture/operator code generates a complete ingress manifest from the
actual release descriptors and configured backend origins.

An example operator flow from the framework repository root is:

```sh
go run ./cmd/nextgo zones release -zone alpha \
  -public-origin http://127.0.0.1:4260 -release A1 \
  -out /tmp/independent-alpha-A1 ./examples/independent-zones/zones.json

go run ./cmd/nextgo zones release -zone blog \
  -public-origin http://127.0.0.1:4260 -release B1 \
  -out /tmp/independent-blog-B1 ./examples/independent-zones/zones.json
```

The operator starts each artifact on its configured private transport origin,
then builds/starts ingress using a validated manifest that includes those actual
origins and generated release descriptors. `zones ingress-build -app APPROOT`
uses one module to resolve the framework; it does not turn the peer applications
into one shared SSR executable. Exact CLI startup flags and native HTTP/serverless
transport behavior are qualified by the deployment fixtures.

To stage Alpha A2, change only a disposable Alpha source copy:

- `internal/state/state.go`: `NativeVersion = "alpha-v1"` → `"alpha-v2"`.
- `components/counter/client_js.go`: `clientProof = "A1"` → `"A2"` and
  `incrementStep = 1` → `2`.
- `public/style.css`: its `alpha original stylesheet A1` marker and accent can
  change; `public/release.txt` has an independent `alpha original asset A1` marker.

Build the selected Alpha module as A2, retain A1's exact asset release, and
publish a new ingress routing snapshot with A2 active. B1's executable, source
and native process can remain unchanged. Rollback selects A1 again. Retention
must keep old document module URLs bound to the exact old release; an active
release is not an asset fallback. No automatic durable rollback of application
data is supplied by this public fixture.

`/api/state` exposes public opaque native-instance, request and revision values.
The versioned `preview/v1` contract accepts `{ "amount": 1 }` (1–5), with a native
URL-encoded form equivalent. It adds that amount to a process-local revision and
returns typed public proof. A transport error produces an unconfirmed UI message
and no automatic retry; the fixture checks real revision changes to distinguish
zero commits from a lost response after one commit. This is not idempotency or
durable storage. `/api/cookie` sets an explicitly non-authentication marker with
the declared zone path and private/no-store policy. `/stream` renders a bounded
cancellable public note for actual HTML/navigation streaming checks.

These sources do not implement authentication, real credentials, private user
data, a service mesh, arbitrary reverse proxying, or multi-region coordination.
Deployment descriptors and identity headers are compatibility metadata, not
authorization. Real operator network/origin policy and application data security
remain separate responsibilities.
