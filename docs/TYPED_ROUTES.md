# Generated Go routes and parameters

`nextgo dev`, `build`, and `export` generate `routes/nextgo_gen.go` in the
application directory before compiling either target. The generated `routes`
package contains URL constructors and decoded parameter structs. It imports
only the shared `routeinfo` package when needed, so using it in a Go/WASM island
does not import server pages, loaders, layouts, or configuration.

Generate without compiling or starting a server:

```sh
go run ./cmd/nextgo generate ./examples/demo
go run ./cmd/nextgo generate --check ./examples/demo
```

For an independent application, use the installed `nextgo generate .` command
or `go run github.com/Suhaibinator/Next.go/cmd/nextgo generate .`. Check mode
reports missing or stale output and writes nothing. Commit the generated file
when you want ordinary Go tooling and `go test ./...` to work immediately after
checkout; run generation before those tools when creating routes from scratch.
The repository CI uses `generate --check` to detect drift on both Go versions.

## Static and dynamic paths

For the demo's `app/posts/page.go` and `app/posts/param_slug/page.go`:

```go
routes.Posts() // "/posts"

path, err := routes.PostsBySlug(routes.PostsBySlugParams{Slug: "Go WASM"})
// path == "/posts/Go%20WASM"
```

Values are decoded data. The builder escapes each segment exactly once; a
literal `%2f` becomes `%252f` and is read back as the literal `%2f`. Empty scalar
parameters, dot segments, slashes/backslashes, controls and invalid UTF-8 are
rejected with an error. Parameter values are omitted from error messages.
String content is validated at runtime: Go types cannot prove that a runtime
string is a valid route segment.

Constructors return logical application paths, without deployment or locale
prefixes. Resolve them at the request/browser boundary:

```go
// Server code, using the current request context:
path, err := routes.PostsBySlug(routes.PostsBySlugParams{Slug: post.Slug})
if err != nil {
    return nextgo.Page{}, err
}
link := ui.El("a", ui.Attrs{"href": nextgo.MustURL(ctx, path)}, ui.Text(post.Title))

// Browser code under //go:build js && wasm:
err = client.Navigate(client.MustURL(routes.Posts()))
```

Query strings and fragments are separate from parameter data. To add them,
parse the returned path with `net/url`, set `RawQuery` from `url.Values.Encode`
and set the decoded `Fragment`, then resolve that URL with `nextgo.URL` or
`client.URL`. Putting `?` or `#` inside a parameter encodes those characters as
part of the path. The helpers do not infer types for arbitrary query strings.

## Typed server parameters and catchalls

Each dynamic route also exports a reader for `Context.Params`:

```go
params, err := routes.ReadPostsBySlug(ctx.Params)
if err != nil {
    return nextgo.Page{}, err
}
post, err := repository.Find(ctx.Context, params.Slug)
```

Readers validate all parameters in that route and return the zero struct on
failure. They allow additional context keys, which can belong to other scopes.
They are conveniences for decoded parameters after routing, rather than an
authentication or route-matching API. Matching and static-over-dynamic
precedence remain the router's responsibility.

Catchall structs expose `[]string` instead of the runtime map's slash-joined
strings:

```go
path, err := routes.DocsByPath(routes.DocsByPathParams{
    Path: []string{"guide", "Go WASM"},
}) // "/docs/guide/Go%20WASM"

params, err := routes.ReadDocsByPath(ctx.Params)
// params.Path is an independently owned decoded segment slice.

path, err = routes.CatalogByPath(routes.CatalogByPathParams{})
// Optional catchall: "/catalog". Required catchalls reject an empty slice.
```

An unknown struct field, a slice in a scalar field, or a string in a catchall
field fails Go compilation. Constructors still return ordinary strings for
interoperation with the existing HTML and navigation APIs. These APIs continue
to accept manually constructed strings; generation does not globally prohibit
them or make endpoint URLs eligible for client page navigation.

## Discovery, naming and output ownership

Helpers follow visible URL segments: `/posts/:slug` becomes `PostsBySlug`;
dynamic routes also get `PostsBySlugParams` and `ReadPostsBySlug`. Route groups
do not appear in URLs or helper names. Named/intercepted branches with the same
pattern share one constructor. Synthetic/default rendering carriers do not add
URLs; concrete named-slot routes do. File HTTP endpoints get constructors too,
with endpoint comments in the generated source.

Names use exported Go identifiers. If normalization or a generated reader/type
causes a package symbol collision, every conflicting route gets a deterministic
pattern-hash suffix. Conflicting parameter field names get positional suffixes.
Generation order does not affect the result. Adding a colliding route can
change those helper names, producing compile errors at existing call sites;
inspect the generated declarations when resolving them. These naming rules are
experimental and have no published stable-version compatibility guarantee yet.

The `routes` directory is reserved for this package. Additional application
files using `package routes` (or external `routes_test` tests) are preserved;
they must avoid declaring the generated symbol names.
Generation refuses a conflicting package, nested module, output symlink,
symlinked output directory, or an existing `nextgo_gen.go` without its generated
ownership header. It formats output, publishes by temporary-file rename, and
does not rewrite unchanged bytes. It does not execute route code to discover
types. Build input snapshots detect source changes during discovery/generation;
generated output participates in build identity and watching. The development
supervisor records the resulting fingerprint to avoid a second reload caused
by its own generation.

## Evidence and remaining work

Native tests exercise escaping/matching round trips, cardinality and unsafe
values; generated-package tests run real Go compiler failures and a WASM build,
verify server imports are absent, and check naming, ownership, drift and
symlinked application roots. Browser tests exercise Unicode/escaped catchalls,
validation without a request, optional empty paths, direct SSR, history,
mounted/CDN and prefix/domain-locale deployments. Development tests add and
remove a route that calls its newly generated constructor and check one reload
per successful change.

This is a Go API for typed path construction and parameter access. It does not
provide TypeScript declarations or React/Next.js source compatibility. Automatic
route-value schema inference, typed query schemas, configurable helper aliases,
build-decision/bundle reports and broader naming/migration qualification remain
future work in [the parity inventory](PARITY.md). For comparison, Next.js
documents [typed links](https://nextjs.org/docs/app/api-reference/config/next-config-js/typedRoutes)
for TypeScript applications.
