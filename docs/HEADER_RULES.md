# Response header rules

`Config.HeaderRules` applies ordered response defaults to canonical logical URLs
before application middleware, redirects, rewrites, pages, APIs and asset serving.
It uses the existing request-rule path and condition syntax, with explicit Go
configuration and no browser JavaScript.

```go
config.HeaderRules = []nextgo.HeaderRule{
    {
        Source: "/docs/:slug",
        Headers: []nextgo.HeaderValue{
            {Name: "X-Document", Value: "{{slug}}"},
            {Name: "Referrer-Policy", Value: "strict-origin"},
        },
    },
    {
        Source: "/docs/:slug",
        Has: []nextgo.RuleCondition{
            {Type: nextgo.QueryCondition, Key: "variant", Value: `(?<variant>blue|green)`},
        },
        Missing: []nextgo.RuleCondition{
            {Type: nextgo.CookieCondition, Key: "skip_variant"},
        },
        Headers: []nextgo.HeaderValue{
            {Name: "X-Variant", Value: "{{variant}}"},
            {Name: "Link", Value: "</help>; rel=help", Append: true},
        },
    },
}
```

## Selection and values

Sources use ordinary static, `:parameter`, `*catchall`, and `*optional?` patterns.
BasePath and locale prefixes are removed before selection. Trailing slashes are
normalized for matching, including responses redirecting to the canonical slash
or language spelling. The original logical source is selected once; a rewrite's
destination does not trigger a second header-rule pass.

`Has` and `Missing` use the same header/cookie/query/host conditions as
[redirects and rewrites](REQUEST_RULES.md). Host means the actual validated Host
without its port; forwarded headers never replace it. Missing conditions never
supply captures. Malformed/oversized condition inputs cannot satisfy either a
positive or negative expression. Conditions are selection rules, not identity
or authorization checks.

`{{name}}` can appear within a value and references a source parameter or positive
named regular-expression capture. The name must exist when the application
starts. Optional absent parameters/groups produce an empty string. These are
literal substitutions, without evaluation, escaping, URL encoding or automatic
BasePath expansion. Do not use arbitrary user input as a security policy or
allowlist. Restrict captures to the values your application intends to publish.
The `{{` opening sequence is reserved for substitutions.

Names are static HTTP field names and ignore case. `Append: false` (the default)
replaces existing values; `Append: true` preserves and adds another. Later rules
and later entries run in declared order. An empty value sends an empty field; it
does not remove the field. Header rules, their values and conditions are frozen
at application construction. An opaque digest contributes to document,
navigation, cache and prerender ownership. Configuration text is not inserted
into the browser manifest, but actual response headers are public data.

## Precedence and privacy

Global `Config.Headers` defaults run first, followed by matching header rules.
Middleware and handlers can replace ordinary fields afterwards. Framework-owned
representation metadata, compression, locale metadata and hashed-asset caching
retain their own final behavior. For example, immutable runtime assets keep the
framework's production cache policy. Proxy responses can add upstream fields;
use trusted upstreams and explicit application middleware for stronger policy
composition.

Any path that could select a conditional header rule is marked sensitive,
including a condition miss. Its public page snapshot/prefetch reuse is bypassed.
A final-write guard enforces `Cache-Control: private, no-store` and clears Go
navigation reuse/age fields, including API handlers that try to advertise public
caching. It preserves supported flush/hijack interfaces and ResponseController
unwrapping. Raw hijacked connections own their protocol/framing and are outside
HTTP header enforcement. Conditional headers alone do not automatically add `noindex`.
Managed draft mode retains its independent `noindex` policy.
The development proxy also adds its own `no-store` field; HTTP clients may expose
multiple Cache-Control values rather than one combined string.

Unconditional header defaults do not define server data-cache lifetimes. Use
explicit page/data cache policies and request bypass for private application
context. Middleware that introduces request-specific data remains responsible
for preserving the framework context and using cache bypass when needed.

Rules cover regular app requests and explicit asset-only mounts. AssetHandler
matches unprefixed `/assets/...` or `/_nextgo/...` paths, retains anonymous CORS,
and does not run app middleware. Selected locale canonical/negotiation redirects
also apply the corresponding logical-source header defaults. Requests outside
owned mounts and rejected/unconfigured locale hosts do not gain a policy from
an unrelated application scope.

## Validation and limits

The complete matching policy is resolved before applying any header operation.
Invalid captured values or an exceeded response budget produce a generic private
400 before middleware/page/API execution, without partial rule values or echoed
input. Text must be valid UTF-8 and cannot contain HTTP control characters except
horizontal tab. HTTP transports may normalize optional field whitespace.

Startup rejects unsafe names, unknown/malformed substitutions and fields whose
ownership belongs to the framework/transport or dedicated response APIs:

- `X-Nextgo-*` protocol/control fields.
- Hop-by-hop fields, connection/upgrade/trailer controls, Host and proxy auth.
- Content-Length, Content-Type and Content-Encoding.
- Set-Cookie/Set-Cookie2 and Location; use response cookie or redirect APIs.

Limits are 256 combined header/redirect/rewrite rules, 32 values per header rule,
128-byte names, 8 KiB source value templates, 128 template parts, 128 KiB accounted
header configuration and 32 KiB accounted resolved header contributions per
request. Each resolved value is limited to 8 KiB. Replaced values still count
against resolution work. Existing source/condition limits also apply. These
bounds do not bound arbitrary application middleware or the complete Go heap.

Static export refuses configured header rules before publication: ordinary HTML
and JSON files cannot preserve their HTTP semantics on an unspecified static
host. Previous output remains intact. Static-host header-file generation and
provider-specific policy adapters remain future work.

## Example and verification

```sh
NEXTGO_DEMO_HEADER_RULES=true go run ./cmd/nextgo dev ./examples/demo
```

Visit `/rules/headers/article?variant=blue` and inspect `X-Demo-Stage`,
`X-Demo-Slug`, `X-Demo-Variant`, and Link. The counter and navigation run in Go.
`/api/header-policy` demonstrates final-write privacy even when its handler sets
a public cache header. `X-Demo-Region: eu-west` selects an API response field.
The existing `NEXTGO_DEMO_RULES=true` sample also enables these examples.

Native/race tests cover ordering, freezing, captures/negation, middleware/rewrite
precedence, APIs/404s, assets, locale/slash redirects, budgets, response splitting,
streaming and final-write privacy. A short bounded fuzz run exercises capture
inputs; it is not a sustained security audit. Browser tests exercise useful SSR,
DOM-preserving hydration, Go navigation/history, privacy, malformed values,
asset-only prefixes and localized URLs. Standalone executable tests use native
HTTP and buffered/streaming mock Lambda transports; actual cloud/CDN/security
qualification remains unfinished.

[Next.js headers](https://nextjs.org/docs/app/api-reference/config/next-config-js/headers)
describes the corresponding upstream capability. Pattern/template syntax,
reserved fields, cache conservatism and public APIs here are Go conventions,
without source compatibility. Header-name interpolation, arbitrary Next.js
source regex syntax, per-rule locale/base-path opt-outs and static-host policy
publication remain unsupported.
