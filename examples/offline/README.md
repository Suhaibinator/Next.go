# Saved anonymous public pages

This example explicitly saves a bounded public reading edition: `/`, `/guide`,
and an approved `/offline` fallback. Go renders the HTML and runs the eager
offline controls and a separate lazy counter. There is no account model, private
page storage, background synchronization, or mutation replay.

Build and run from the repository root with Go 1.24 or newer:

```sh
go run ./cmd/nextgo build --out ./dist/offline ./examples/offline
./dist/offline/server -addr 127.0.0.1:3022
```

Open `http://127.0.0.1:3022/` in a browser that supports service workers and
browser storage. This slice requires a production native build. The offline
hook is explicitly unsupported by `dev`, static export, prerendering, and zones;
it is not a one-command development offline mode.

While online, choose **Enable offline** and check **Status of saved
public pages**. Wait for a complete edition before disconnecting. If this tab is
not controlled yet, reload it once. Disconnect and use the reading links or
reload an approved page. Those links deliberately use full-document navigation;
SPA navigation JSON, API responses, and requests with query strings are outside
the saved inventory. `/api/health` reports `live-network-only` and is never saved.

**Status of saved public pages** makes one explicit serialized status operation
for the active and waiting workers, as applicable, using
`client.RefreshOfflineStatus(ctx)` from `Scope.Go`. The synchronous
`client.OfflineStatus()` API only reads the latest snapshot. All controls wait
for an in-progress operation, including the initial status query. A status query
does not register a worker, install a saved edition, check for an update, repair
network state, or automatically retry an unconfirmed operation. After an error,
choose Status again explicitly before deciding whether to try another operation.

**Check for update** checks the actual worker registration.
A waiting edition activates after every tab in this application scope closes.
Reopen the application afterward; there is no forced activation or automatic
tab reload. **Disable offline** stops saved-page serving and unregisters this
application's worker. While a controlled tab remains open, its public cached
payload stays retained and charged; disabled does not mean erased. Close those
tabs, then explicitly choose **Enable offline** or **Disable offline** on a later
online visit to reconcile the retained data. Background cleanup is not
guaranteed. A tab can remain controlled after unregistering until its next native
reload. Other applications' browser storage is left alone.

The configured fallback is intentionally inert. The saved `/offline` response
uses only that route's approved title and description as escaped text. Its app
body, shared layout, CSS and Go runtime are omitted. The normal online route
still shows the explanatory full page.

The trusted default origin is exactly `http://127.0.0.1:3022`. To deploy at a
different canonical HTTPS origin or mount, configure the native server explicitly:

```sh
NEXTGO_OFFLINE_ORIGIN=https://reader.example \
NEXTGO_OFFLINE_BASE_PATH=/reader \
./dist/offline/server -addr 127.0.0.1:3022
```

Then open `https://reader.example/reader` through the configured HTTPS reverse
proxy. Origin includes the exact scheme and authority, with no trailing slash;
base path is empty or a canonical rooted path with no trailing slash. Neither
value is inferred from request headers. A different port, hostname or mount is
a different scope. Loopback HTTP is for local use; remote origins require HTTPS.

`app/offline.go` declares `saved-anonymous-public` semantics, a 24-hour retention
permission for each page, and closed-client update activation. `app/config.go`
gives only those three fixed-content routes an ordinary public cache policy.
The framework still validates their final anonymous representations and complete
local asset closure before publication. No external resources are needed.

Saved editions may be stale, expire, fail installation, or be removed by the
browser. They do not establish current identity, permission, transaction outcome,
or server freshness. Counter state is document-local and resets on navigation.
The UI reports storage capability and lifecycle errors without promising durable
storage. Native sample tests cover explicit policy, useful SSR and the network-only
API; browser qualification is performed separately against built artifacts.
