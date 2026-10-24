//go:build !js

package about

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Architecture — Next.go", Description: "How server rendering and Go WebAssembly islands fit together.", Body: ui.El("article", ui.Attrs{"class": "prose"},
		ui.El("p", ui.Attrs{"class": "eyebrow accent"}, ui.Text("UNDER THE HOOD")), ui.El("h1", nil, ui.Text("A little framework.\nA clear boundary.")),
		ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("The server owns pages and data. The browser owns interactions. HTML connects the two.")),
		ui.El("h2", nil, ui.Text("1. Render on the server")), ui.El("p", nil, ui.Text("Generated route registration connects ordinary Go packages to HTTP paths. Pages load data, compose components, and nest inside shared layouts.")),
		ui.El("h2", nil, ui.Text("2. Attach in the browser")), ui.El("p", nil, ui.Text("Go compiles browser components to a single WebAssembly bundle. Each island receives public JSON props and attaches listeners to its existing server-rendered nodes.")),
		ui.El("h2", nil, ui.Text("3. Navigate with Go")), ui.El("p", nil, ui.Text("Internal page links request a rendered fragment. The Go runtime cleans up old islands, updates the page and metadata, and mounts the next set. Page islands reset between pages; shared layout islands preserve their nodes and state.")),
		ui.El("h2", nil, ui.Text("Deliberately small")), ui.El("p", nil, ui.Text("The framework now demonstrates streaming, persistent layouts, server actions, caching, advanced routes and media metadata. Full Next.js feature parity and production maturity remain active work, tracked in docs/PARITY.md. HMR, automatic code splitting and other release gates are still open.")),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts")}, ui.Text("Read the field notes →")))}, nil
}
