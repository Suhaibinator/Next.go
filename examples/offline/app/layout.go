//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/offline/components/counter"
	"github.com/Suhaibinator/Next.go/examples/offline/components/offlinecontrols"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	link := func(path, label string) ui.Node {
		// Saved-document delivery uses native navigation. Navigation JSON and
		// API traffic remain online-only in this bounded example.
		return ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, path), "data-nextgo-reload": ""}, ui.Text(label))
	}
	return ui.Fragment(
		ui.El("header", nil,
			ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("NEXT.GO / SAVED PUBLIC PAGES")),
			ui.El("nav", ui.Attrs{"aria-label": "Offline example"}, link("/", "Overview"), link("/guide", "Reading guide"), link("/offline", "Offline fallback"))),
		ui.El("main", ui.Attrs{"id": "main", "tabindex": "-1"}, body,
			offlinecontrols.View(), counter.View(counter.Props{Initial: 3})),
		ui.El("footer", nil, ui.Text("Saved anonymous public content can be older than the server. No account data or pending mutations are saved.")),
	), nil
}
