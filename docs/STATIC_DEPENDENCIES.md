# Declaring runtime dependencies before static export

A page can render useful HTML while still depending on an application HTTP
endpoint after load: a Go resource fetching `/api/resources`, a generated social
image handler, or a local media route are examples. Copying that HTML to a plain
static host does not publish those handlers. The exporter cannot infer arbitrary
Go code, API calls or URLs embedded in public props.

Declare that requirement explicitly in the page or enclosing layout:

```go
func Page(c *nextgo.Context) (nextgo.Page, error) {
    if err := c.RequireServer(); err != nil {
        return nextgo.Page{}, err
    }
    return nextgo.Page{Body: renderInterface()}, nil
}
```

`RequireServer() error` stages `X-Nextgo-Requires-Server: 1` using the same
response controls as `SetHeader`. `nextgo.RequiresServerHeader` exports the
canonical field name. Repeated calls replace the value rather than duplicating
it. The marker accompanies ordinary HTML and Go navigation responses; no
navigation JSON or action protocol version changes. It declares publication
requirements and does not enable an API, configure routing, authenticate a
request, or set cache policy.

Call before returning from page/layout/boundary preparation and propagate the
error. Rendering/deferred callbacks have sealed headers and return
`ErrHeadersCommitted`. Public cached loaders cannot mutate response controls and
return `ErrPublicCacheResponse`; ignoring that error still rejects their render.
This method does not introduce a new exception to snapshot/prerender safety or
automatically mark every caller of an API. Applications must declare dependencies
on each affected page or an enclosing layout before response preparation ends.

`nextgo export` checks this marker on both HTML and navigation responses before
reading their bodies. It returns a clear server-dependency error and does not
publish the staged directory; any previous complete export remains intact. Its
existing rejection of actions, optimized image endpoints, request regions,
streamed/parallel content and unsupported configuration still applies. The
marker supplements those checks; absence does not prove a page is static-safe.
Custom HTTP handlers can set the same marker when providing export responses.

For a page that genuinely supports static hosting, use pre-generated files,
public external services with the required browser policies, or explicit static
data instead. Then verify the exported output using only a plain static server
with the application process stopped. Do not remove the marker solely to bypass
an export error while the page still references unpublished runtime behavior.

Focused native and exporter fixtures cover staged HTML/navigation output,
late/cached declarations, early response-body cancellation and preservation of
all prior output files. They do not perform whole-program dependency analysis,
publish API handlers as static files or close the [release gates](PARITY.md).
