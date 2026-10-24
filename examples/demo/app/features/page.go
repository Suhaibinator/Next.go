//go:build !js

package features

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/toolbox"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Framework features — Next.go", Description: "Go server actions, persistent layouts, streaming, metadata and advanced routing.", Metadata: seo.Metadata{
		Canonical: "https://example.com" + nextgo.MustURL(c, "/features"), Robots: "index,follow",
		OpenGraph: seo.OpenGraph{Title: "Go across the stack", Description: "A Go-native full-stack framework feature showcase.", URL: "https://example.com" + nextgo.MustURL(c, "/features"), Type: "website", SiteName: "Next.go demo"},
		Twitter:   seo.Twitter{Card: "summary", Title: "Next.go framework features"}, Alternates: map[string]string{"en": "https://example.com" + nextgo.MustURL(c, "/features")},
	}, Body: ui.Fragment(
		ui.El("section", ui.Attrs{"class": "hero"}, ui.El("div", nil, ui.El("h1", nil, ui.Text("Framework features")), ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("Try server mutations, persistent layout state and streaming. The layout counter above survives navigation between pages.")))),
		toolbox.View(toolbox.Props{Count: state.Count()}),
		ui.El("section", ui.Attrs{"class": "counter"}, ui.El("h2", nil, ui.Text("Progressive HTML form")), ui.El("p", nil, ui.Text("This server action works without WebAssembly or JavaScript. The demo counter is public and resets when the server restarts.")),
			ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/increment")}, ui.El("label", nil, ui.Text("Amount "), ui.El("input", ui.Attrs{"type": "number", "name": "amount", "value": "1", "min": "1", "max": "10", "required": ""})), ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Submit server form")))),
		ui.El("section", ui.Attrs{"class": "counter"}, ui.El("h2", nil, ui.Text("Explore routing and rendering")), ui.El("ul", nil,
			link(c, "/optimistic", "Optimistic Go mutations"), link(c, "/boards", "Parallel route boards"), link(c, "/rules", "Conditional request rules and external rewrites"), link(c, "/transitions", "Go view transitions"), link(c, "/translations", "Managed language routing"), link(c, "/evergreen", "Non-expiring default profile"), link(c, "/profiles", "Named cache profiles"), link(c, "/cache", "Server cache lifecycle"), link(c, "/locale?lang=fr", "Document language and viewport"), link(c, "/restricted", "Authorization boundaries"), link(c, "/lifecycle", "Bounded request completion work"), link(c, "/downloads/report", "HTTP route: /downloads/report"), link(c, "/templates/a", "Segment-keyed templates"), link(c, "/stream", "Streaming deferred content"), link(c, "/docs/guide/getting-started", "Catch-all route: /docs/guide/getting-started"), link(c, "/catalog", "Optional catch-all: /catalog"), link(c, "/catalog/go/wasm", "Optional catch-all with segments"), link(c, "/welcome", "Route group: /welcome"), link(c, "/media", "Image optimization and SEO"), link(c, "/posts?delay=750ms", "Route loading boundary"), link(c, "/posts?fail=1", "Route error boundary"))))}, nil
}
func link(c *nextgo.Context, href, label string) ui.Node {
	return ui.El("li", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, href)}, ui.Text(label)))
}
