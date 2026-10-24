# Managed draft/preview mode

Draft mode lets an authorized editor preview unpublished content while ordinary
visitors continue using published caches. Signing and session validation stay on the server. A minimal bootstrap bridge
forwards native history lifecycle events; restoration policy and state remain
in Go, with no browser signing code. The comparison follows the
[Next.js draft-mode contract](https://nextjs.org/docs/app/guides/draft-mode);
the Go API and wire protocol are independent.

## Configure a server-only manager

```go
import "github.com/Suhaibinator/Next.go/draft"

manager, err := draft.New(draft.Options{
    Keys: map[string][]byte{"current": signingKey},
    ActiveKey: "current",
    Scope: "my-site:preview:v1",
    Path: "/", // use the application's global BasePath when mounted
})
if err != nil {
    return err
}
config.Draft = manager
```

Load `signingKey` from server-side secret configuration. Each key must contain
32–64 cryptographically random bytes. `New` copies keys and freezes its options;
the zero-value manager is rejected. Up to four named keys can verify sessions.
Only `ActiveKey` issues new sessions. Removing a key or changing `Scope` revokes
the corresponding sessions. Keep keys and scope consistent across replicas or
Lambda executions that should accept the same preview cookie.

`Scope` is a stable application/preview epoch, with 1–128 printable non-space
ASCII bytes. Cookie names default to `nextgo_draft`; configure distinct names
and scopes for applications sharing an origin. `Path` defaults to `/` and must
be a canonical ASCII path. A single global app path, such as `/shop`, lets one
session work across `/shop/en` and `/shop/fr`. Configured domains receive
separate host-only sessions. The signature binds the canonical request authority,
including its port, so copying a token to another authority does not enable it.
Forwarded headers do not determine the signing authority.

Cookies are host-only, HttpOnly, SameSite=Lax and Secure by default. Set
`AllowInsecure: true` only for an explicit HTTP deployment, such as local
development. Secure and insecure policies have distinct signatures. `__Host-`
cookie names additionally require Secure and `/`; `__Secure-` requires Secure.
The public HTTPS policy is explicit, including behind a TLS-terminating proxy.

TTL defaults to one hour and accepts whole seconds between one minute and
24 hours. Tokens contain a format version, issue/expiry times and a random
nonce, authenticated with HMAC-SHA256. They contain no identity, CMS credential,
signing key or application record. Invalid, oversized, ambiguous, expired,
wrong-scope and tampered cookies fail closed to ordinary published mode.

## Authorize entry and provide an exit

`manager.Cookie(request)` returns a cookie; it does not authorize the caller,
publish a response, choose a redirect or implement CSRF protection. An entry
handler must authenticate and authorize preview access first. Use a bounded
POST, a same-origin/CSRF policy, private responses and an application-owned
destination. Publish the result with `http.SetCookie`, or `Context.SetCookie`
when using staged page response controls. Never copy a caller's redirect URL.

`manager.ClearCookie()` returns an expired cookie with the same name, path and
security policy. Publish it from an explicit exit POST and redirect to a trusted
page. Ordinary links and prefetch GETs must not toggle preview.

The [demo entry handlers](../examples/demo/preview/preview.go) implement this
flow with existing server-only editor authentication, exact-origin checks,
a 4 KiB body limit, 401/403 failures and a fixed localized `/preview` redirect.
The progressive form accepts an editor token in a password field. Integrations
can instead send the explicit Bearer credential. Neither credential enters URLs
or SSR props. Real CMS integrations must supply their own authorization and
content-access policy; the sample does not connect to a CMS.

For local demonstration, generate fresh secrets and start the example:

```sh
export NEXTGO_DEMO_DRAFT_KEY="$(openssl rand -hex 32)"
export NEXTGO_DEMO_ADMIN_TOKEN="$(openssl rand -hex 32)"
export NEXTGO_DEMO_DRAFT_HTTP=true
go run ./cmd/nextgo dev ./examples/demo
```

Open `/preview` and enter the editor token. The root layout shows an exit form
throughout the preview session. An unconfigured demo shows only published
content and does not register preview entry/exit handlers. Production HTTPS
deployments omit `NEXTGO_DEMO_DRAFT_HTTP` and obtain secrets from their deployment
configuration.

## Read preview state and fresh content

```go
func Page(c *nextgo.Context) (nextgo.Page, error) {
    if draft.Enabled(c) {
        // Choose the CMS draft endpoint or render an editor indicator.
    }
    // Load with c or c.Context so request policy is propagated.
    // ...
}
```

`draft.Enabled(ctx)` is available in middleware, pages, layouts, metadata,
API/action handlers, streamed work and completion callbacks. It is a verified
request snapshot; a cookie expiring during a long response does not change that
response's context. `draft.Configured(ctx)` distinguishes disabled preview from
an application without managed preview. Both return false outside the managed
scope. `Manager.BindRequest` is available for custom HTTP integration; App binds
automatically before application middleware and handlers.

Verified preview requests bypass full-page snapshots and all framework
`cache.Store` reads, writes, shared flights and stale refreshes reached through
the supplied context. Preview data never overwrites published entries or joins
an in-flight public load. Request-local `nextgo.Memo` still deduplicates work
within that isolated request. The underlying data loaders must continue to
enforce their own authorization and cancellation.

`cache.WithBypass` is also available as an explicit data-cache policy, and
`cache.Bypassed` reports it. Successful bypassed lookups return
`cache.Info{State: cache.Bypass}` with no reuse age. Configuration validation,
panic handling and configured loader admission still apply. Do not recursively
enter an admission-limited Store, including normally cached keys. Propagate
the supplied context to nested work; background contexts discard request policy.

This controls framework stores, not arbitrary application caches, external
HTTP/CDN caches, database replicas or third-party SDK caches. Those integrations
must observe preview policy explicitly.

## Navigation, privacy and deployment

Framework preview responses are `private, no-store` and carry an `X-Robots-Tag:
noindex` header. A final-write wrapper enforces that policy for configured API
handlers, actions, errors, redirects and streaming too. Requests inside the application mount, including root-origin assets and
asset-shaped configured endpoints, receive the same preview privacy policy.
Separate public asset-only/CDN mounts retain their ordinary asset caching. Preview configuration rejects plain static
publication; build a native HTTP or Lambda server instead.

Configured preview applications suppress Go navigation/prefetch response reuse
in both published and preview modes. Every soft navigation must reach the server
because an HttpOnly cookie can change in another tab. Speculative page requests
are skipped because their responses cannot be reused safely. The document and loading/
navigation responses carry an explicit preview-state marker. A missing or changed
response marker triggers a full document load before retaining layout islands;
this disposes old private state after entry, exit, expiry or key rotation.
Unchanged preview mode continues navigating in Go. The enable/exit form's
ordinary 303 redirect also resets browser state without WASM.

This does not erase an already visible page the instant a cookie expires or an
editor signs out elsewhere; the next framework navigation/refresh revalidates it. The [native restoration contract](LIFECYCLE.md) additionally freezes retained
content until a bounded session/generation probe succeeds, preserves Go state
when compatible and uses a fresh document when required. Browser-native cache
eligibility and complete device/authentication qualification remain separate. Do not
use preview cookies as a general authentication/session system. Clearing a
stateless cookie only ends that browser's session: copies remain valid until
expiry or key/scope rotation. Durable revocation, identity-provider integration,
one-time CMS entry codes, arbitrary CDN/cache adapters and real-cloud rollout
qualification remain future work.

Native tests cover signing, rotation, scope/authority/path/policy binding,
expiry, duplicate cookies, authorization, data/public-flight isolation and
private API/stream/error responses. Browser tests cover no-JS forms, delayed
WASM/DOM identity, interaction, unchanged-mode SPA behavior, changed-mode full
navigation, published-cache isolation and mounted locales. Generated executable
fixtures exercise native HTTP and both buffered/streaming mock Lambda runtimes;
mock execution does not establish actual cloud qualification.
