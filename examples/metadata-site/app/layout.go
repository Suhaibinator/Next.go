//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/metadata-site/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	var links []ui.Node
	for _, entry := range []struct{ path, label string }{{"/", "Overview"}, {"/about", "About"}, {"/override", "Explicit metadata"}, {"/disabled", "Disabled metadata"}} {
		links = append(links, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, entry.path)}, ui.Text(entry.label)))
	}
	return ui.Fragment(
		ui.El("header", nil, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("NEXT.GO / PUBLIC METADATA")), ui.El("nav", ui.Attrs{"aria-label": "Metadata pages"}, links...), counter.View(counter.Props{Initial: 2})),
		ui.El("main", ui.Attrs{"id": "main", "tabindex": "-1"}, body),
		ui.El("footer", nil, ui.Text("Original raster icons. Typed Go producers. No external fetches or service worker.")),
	), nil
}
