# Request rules and external proxy rewrites

`Config.Redirects` and `Config.Rewrites` are ordered server configuration. They
use the same app-relative `:name`, terminal `*tail`, and optional `*tail?` source
patterns as the Go router. Rules compile once during `nextgo.New`; there is no
request-time regular-expression compilation or JavaScript routing layer.

```go
func Configure(config *nextgo.Config) {
    config.Redirects = []nextgo.RedirectRule{{
        Source: "/old/:slug", Destination: "/posts/:slug", Permanent: true,
        Has: []nextgo.RuleCondition{{Type: nextgo.QueryCondition, Key: "migrate", Value: "yes"}},
    }}
    config.Rewrites = []nextgo.RewriteRule{{
        Source: "/selected", Destination: "/posts/:post",
        Has: []nextgo.RuleCondition{{
            Type: nextgo.HeaderCondition, Key: "X-Selected-Post",
            Value: "(?<post>html-first|go-islands)",
        }},
        Missing: []nextgo.RuleCondition{{Type: nextgo.CookieCondition, Key: "disable-selection"}},
    }, {
        Source: "/service/*path", Destination: "https://trusted-backend.example/api/*path",
    }}
}
```

Redirects use 307, or 308 with `Permanent: true`, preserving methods. Local
redirect destinations receive the configured BasePath/locale/slash policy.
External redirects use their configured literal HTTP(S) authority. Rewrites do
not change the public URL. Internal rewrites select file pages/API routes in one
pass, with no recursive rewrite loop. Configured handlers/framework services and
assets retain their existing precedence before internal rewrites. This is not
the full Next.js before-files/after-files/fallback phase model.

Response-header rules use the same sources and conditions, run before application middleware, and bind headers to the original logical source. See [HeaderRules](HEADER_RULES.md) for typed values, template syntax, final-write privacy and static-export restrictions.

## Conditions and captured values

All `Has` conditions must match; every `Missing` condition must fail to match.
Supported types are `HeaderCondition`, `CookieCondition`, `QueryCondition` and
`HostCondition`. Header keys ignore case; cookie/query keys are exact. Host uses
the validated, lower-case `Request.Host` hostname without its port or trailing
DNS dot. It never uses Forwarded/X-Forwarded headers. Host conditions omit `Key`.

Empty `Value` means presence, including an explicitly empty value. Otherwise the
expression is anchored to the complete value and uses Go's RE2 syntax. `^$`
selects an explicitly empty value. Repeated header/cookie/query values match if
any value matches. Named groups in `Has` are available to destination templates;
names cannot collide with a source parameter or another positive capture.
`Missing` never supplies captures. Malformed queries/hosts do not satisfy a
condition, including a negated condition. Regular expressions are not JavaScript
regexes; unsupported RE2 constructs fail startup.

A destination `:name` occupies one complete path segment. A `*name` preserves a
slash-separated tail; each segment is escaped independently. Query values or a
redirect fragment consisting entirely of `:name` can also reference captures.
Unknown parameters fail startup. Captures cannot change an external scheme,
authority or port. Unsafe resolved paths (dot segments, slashes in a single
segment, backslashes, controls, invalid UTF-8) fail with 400 before redirection or
upstream contact. Static and parameterized path segments share these checks.

Incoming query text is preserved when the destination omits a query. An explicit
destination query replaces it; `?` explicitly clears it. Unused captures are not
automatically copied into the query. Mixed literal/parameter interpolation inside
one query value or path segment is not implemented. Redirect fragments are
preserved; rewrite fragments are rejected because they have no HTTP upstream
meaning.

Limits are 256 total rules, 16 conditions per rule, 4 KiB source patterns, 8 KiB
destination text, 256-byte keys, 2 KiB expressions and 16 KiB resolved URLs.
Expression matching accepts input values up to 8 KiB; oversized regex inputs
cannot satisfy either a positive or negative rule. Presence checks do not need
to inspect the value. Normal HTTP transport header/URL limits also apply.
Unconditional exact local redirect cycles and self-targeting unconditional
redirects are rejected. Parameterized, conditional, external and deployment-level
redirect loops still require application tests and network policy.

Conditions select routing, not authorization. Authenticate and authorize in the
page/data/API/mutation handler itself. Do not put credentials in expressions.

## Caching and browser ownership

A page request whose path could select a conditional rule bypasses shared page
snapshots and advertises `private, no-store`, including the unmatched fallback.
It cannot enter Go prefetch reuse even if its page declares a public cache
policy or tries to advertise public Cache-Control. Data caches remain explicit
application APIs; include tenant/user/input variation in their keys. Custom HTTP
handlers still own their HTTP caching/Vary policy for these redirect/rewrite
conditions. Conditional HeaderRules additionally enforce final-write privacy
for handlers, as described in [the header policy contract](HEADER_RULES.md).

An opaque rule digest contributes to document/navigation/cache ownership.
Condition values and upstream authorities are absent from the browser manifest.
External proxy and external redirect source patterns conservatively require full
document navigation, including conditional sources. They are excluded from Go
prefetch and SPA interception. Navigate/Replace/Prefetch reject these URLs like
HTTP endpoints; use an ordinary anchor for full-document navigation. This avoids interpreting an upstream response as
framework navigation data. Local page aliases continue to use Go navigation and
history; a local redirect commits the final canonical URL. Loading fallbacks can
resolve a local conditional rewrite with the same request conditions.

Static hosts cannot execute these server conditions or upstream requests. Static
export rejects conditional/external rules before publication and preserves the
preceding output. Plain local alias/redirect publication and general static-host
rule generation remain outside the current exporter contract; exporting selected
concrete pages does not publish a server router.

## Proxy policy and trust

External rewrite origins are literal trusted application configuration. Private
service addresses are permitted deliberately; this is a reverse proxy rather
than a user-supplied URL fetch service. It does not implement DNS/IP egress
allowlists. Apply network policy to the configured service if required. HTTP(S)
credentials in the URL, dynamic authorities, unsafe paths and non-HTTP protocols
are rejected. Redirect responses are returned to the caller, never followed by
the proxy transport. Upgrade/WebSocket and CONNECT tunneling are unsupported.

Proxied HTML executes at the frontend application's origin. Trust its producer
as application content. This facility does not sanitize or sandbox another
site's HTML and does not automatically configure multi-zone assets or routing.

`Config.Proxy` supplies application-wide options shared across locale mounts:

| Option | Default | Bounds/behavior |
| --- | --- | --- |
| MaxConcurrent | 16 | 1–1,024; full admission returns 503 with Retry-After |
| MaxRequestBytes | 2 MiB | 1 byte–64 MiB; buffered before upstream contact; overflow 413 |
| MaxResponseBytes | 8 MiB | 1 byte–64 MiB; streamed and counted; known overflow 502 |
| Timeout | 30 seconds | 1 ms–5 minutes; includes request ingestion/upstream body |
| ForwardCredentials | false | Explicitly passes Cookie/Authorization and upstream Set-Cookie; also preserves Origin/Referer |
| ForwardClientInfo | false | Creates fresh X-Forwarded-For/Host/Proto from actual request/TLS |
| Transport | owned standard transport | Optional explicit RoundTripper for TLS, proxy, dialing or per-origin policy |

The default transport ignores environment proxies and disables automatic
response decompression, so body limits count transported content-encoded bytes.
It bounds connections/idle pools and header waits. Request bodies are buffered
within the admission/body budget; request upload streaming is not implemented.
Native HTTP read deadlines bound slow clients before upstream contact. Lambda
already reconstructs a bounded in-memory event body. Custom transports, readers
and ResponseWriters must cooperate with cancellation/deadlines.

Inherited forwarding headers, X-Real-IP, browser trace carriers, hop-by-hop and
framework control headers are removed before upstream requests. When tracing is
enabled, the owned tracer injects its current outbound trace context. Other
application headers are forwarded; upstream services must not blindly trust
arbitrary client-provided vendor/identity headers. The default credential policy
removes Cookie, Authorization, Origin and Referer. The standard reverse proxy
removes Proxy-Authorization. Response headers and late trailers cannot spoof
`X-Nextgo-*`; Set-Cookie headers/trailers require credential opt-in. Ordinary
headers, statuses, bodies and permitted trailers are relayed. Credential opt-in
does not implement authentication or CSRF protection; the upstream must enforce
its authorization/origin policy. HEAD retains representation metadata without
applying a body-transfer budget to its empty body.

Dial/TLS/protocol failures return a generic 502; upstream deadlines before
commit return 504. Before-commit failure logs identify the configured source, a bounded failure
category and error type without backend addresses, request values or error text.
After a streamed response commits, an overflow/read/deadline failure aborts the
stream instead of pretending it completed. Native clients see an incomplete
body/connection; buffered Lambda discards a handler panic into its generic 500,
and streaming Lambda reports its terminal failure. Platform limits can be lower
than proxy budgets. No retry, durable delivery or transparent mutation replay is
provided. Upstream writes may have happened before a failure; use application
idempotency for required mutations. `App.Close` closes owned idle connections
following normal server/completion shutdown; custom transport ownership remains
with the caller.

## Example and verification

```sh
NEXTGO_DEMO_RULES=true \
NEXTGO_DEMO_PROXY_ORIGIN=http://127.0.0.1:4130 \
  go run ./cmd/nextgo dev ./examples/demo
```

Visit `/rules`. The upstream example assumes it serves `/document` and `/echo`;
choose your own trusted HTTP service. `NEXTGO_DEMO_PROXY_TIMEOUT` customizes the
demo deadline. The example also provides an explicit external redirect to that
origin's `/document`; it is intended to be reachable by the browser.

Native/race tests cover conditions, capture bounds/escaping, query/host validity,
cache isolation, redirect cycles, manifest privacy, loading, HTTP/TLS/header and
trailer policy, response streaming, admission/body limits and deadlines.
`FuzzResolvedRuleOriginAndMount` exercises destination confinement. Browser tests
use an actual second upstream server for local SPA/redirect behavior, changed
cookies after prefetch intent, proxy methods/data, external document boundaries
and recoverable API timeouts. Generated executable fixtures run HTTP and both
mock Lambda transports outside the source directory, including non-root,
read-only Linux. These qualify the stated subset, not complete Next.js routing,
real cloud/TLS/CDN rollout or production load/recovery.

Sources: [Next.js redirects](https://nextjs.org/docs/app/api-reference/config/next-config-js/redirects),
[rewrites](https://nextjs.org/docs/app/api-reference/config/next-config-js/rewrites),
[Go ReverseProxy](https://pkg.go.dev/net/http/httputil#ReverseProxy).
