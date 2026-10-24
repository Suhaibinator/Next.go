# Composed build parameters

`staticparams.go` can enumerate an ordinary page's parameters or contribute an
ancestor prefix to descendant pages. The Go export remains:

```go
func StaticParams(context.Context) ([]nextgo.Params, error)
```

Child callbacks read an owned map using `nextgo.StaticParentParams(ctx)`. The
framework merges their results with the parent values and validates the complete
binding at that directory's URL prefix. A child may repeat an identical parent
value; changing it is an error.

This works in production prerendering and in the default path selection for
`nextgo export`. Ordinary request-time loading still receives `Context.Params`.
It does not call the parameter generators on every HTTP request.

## Parent and child example

```text
app/
  market/
    param_category/
      staticparams.go
      layout.go
      page.go
      param_product/
        staticparams.go
        page.go
      summary/
        page.go
```

The parent declares categories, in its `staticparams.go`:

```go
//go:build !js

package category

import (
    "context"
    nextgo "github.com/Suhaibinator/Next.go"
)

func StaticParams(ctx context.Context) ([]nextgo.Params, error) {
    return []nextgo.Params{{"category": "coffee"}, {"category": "tea"}}, nil
}
```

The child's `staticparams.go` supplies product values for each parent:

```go
//go:build !js

package product

import (
    "context"
    nextgo "github.com/Suhaibinator/Next.go"
)

func StaticParams(ctx context.Context) ([]nextgo.Params, error) {
    parent := nextgo.StaticParentParams(ctx)
    if parent["category"] == "tea" {
        return []nextgo.Params{{"product": "green"}}, nil
    }
    return []nextgo.Params{{"product": "espresso"}, {"product": "filter"}}, nil
}
```

The product route generates three concrete URLs. The category page generates
`/market/coffee` and `/market/tea`. The static `summary/page.go` suffix inherits
the parent's complete bindings and needs no callback of its own. Its two URLs
are generated too. A parent generator may live without a page or layout in its
own directory, provided it has descendant ordinary pages that consume it.

A dynamic descendant that still lacks a required parameter and has no child
callback is omitted. An optional catch-all can remain empty. An empty returned
slice selects no children for that parent. Existing page-local callbacks may
continue returning all route parameters without any ancestor declarations.

Callbacks can only bind parameter names present at or above their directory.
Unknown names, unsafe/missing values, descendant names supplied by a parent, and
changes to parent values fail enumeration. A generated URL selecting a more
specific page/API route also fails. Group folders preserve ancestry while
remaining absent from the URL. Catch-all values use the existing decoded,
slash-separated `Params` convention; optional empty values remain supported.

## Scope, ownership and reuse

Callbacks run sequentially, top down, once for each distinct declaration and
parent binding in an `App.StaticPaths` invocation. The parent declaration is
shared across its own page and descendant pages. The cache is local to that
invocation and language/domain scope. Another call enumerates fresh data.

`StaticParentParams` returns a copy on every read. It is nil at the root and
outside enumeration. Returned rows are copied before reuse; treat callback
results as immutable after returning and do not mutate them concurrently.

Request locale, deployment URL helpers and named cache profiles remain available
through the enumeration context. Each callback receives a fresh request memo
scope and a cancellation context. Completion hooks are blocked. Errors, panics,
`runtime.Goexit`, and cancellation fail the build/export; private panic payloads
are not inserted into diagnostics. Loaders must honor cancellation. Budgets
cannot stop arbitrary application code from allocating memory or ignoring its
context.

All generated values and page/island props are public data. Do not enumerate
private tenant/user content into a shared artifact. There is no automatic
request-independence or secret classification for arbitrary Go code.

## Manual registration

Generated entrypoints register ancestors as `Route.StaticParamsLayers`, ordered
outermost first. The stable declaration ID is the Go package import path. A
page-local callback also gets `Route.StaticParamsID`, allowing its parent role
to reuse the same declaration result:

```go
nextgo.Route{
    Pattern: "/market/:category/:product",
    Page: productPage,
    StaticParamsLayers: []nextgo.StaticParamsLayer{
        {ID: "market-categories", Pattern: "/market/:category", Load: categories},
    },
    StaticParams: products,
}
```

Manual IDs must name one declaration with one prefix and callback. Use distinct
IDs for callbacks with different captured state; comparing Go function pointers
cannot detect differing closure captures. An omitted page ID gets a private
route-local identity. New freezes the layer slice and rejects inconsistent IDs,
invalid/out-of-order prefixes, nil callbacks and unsupported owners. Ancestor
IDs/prefixes participate in prerender ownership; changing them prevents reuse of
an incompatible embedded seed.

## Bounds and build behavior

Enumeration permits at most 32 ancestor layers, 10,000 callback output rows,
16 MiB of accounted generator keys/values, 10,000 candidate paths and 16 MiB of
unique URL data. Each concrete path is limited to 8 KiB. Repeated rows consume
budgets even when final URLs deduplicate. Cross-product expansion stops at the
limit. IDs are limited to 1,024 bytes.

Locale enumeration shares quotas across the selected artifact. Prerender builds
share generator quotas across canonical scopes and validate runtime known-path
membership, including default-language aliases, before publishing an artifact.
Runtime membership has a shared 30-second enumeration deadline, 10,000 total
paths and 16 MiB of logical URL data across all mounts. Alias entries count too;
a near-limit build may need fewer paths or `PrefixDefault` to avoid additional
alias mounts. Runtime mount/locale changes exceeding these bounds fail startup.

Use `nextgo export` without `-paths` to exercise generated selection. Explicit
`-paths` still selects concrete URLs, subject to renderer and ownership checks.
Static export stages the whole result before replacing an existing publication;
a failed child binding leaves the old artifact intact. Development watches parent
files, rebuilds both targets, and preserves the last successful server if a
candidate fails enumeration during startup. Repairing it reloads the browser.

## Current scope

Try `/market/coffee/espresso` in the demo, or `/shop/fr/market/caf%C3%A9/espresso`
when the documented `/shop` locale mount is enabled. The summary page demonstrates
inherited bindings without a leaf callback. [Prerendering](PRERENDER.md) describes
explicit opt-in and blocking/closed-list runtime fallback.

This supports ordinary page ancestry. Named slots/interception branches and API
handler parameter generation remain unsupported and are rejected. No automatic
static/dynamic analysis, automatic fetch deduplication, incremental parent-list
updates or shared durable generation store is implied. The inventory remains
partial for the broader capability.

The corresponding parent/child behavior is described in
[Next.js generateStaticParams](https://nextjs.org/docs/app/api-reference/functions/generate-static-params).
The signatures and build conventions here are Go APIs, without Next.js source
compatibility.
