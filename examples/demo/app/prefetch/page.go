//go:build !js

package prefetch

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	link := func(id, path, label string, options ui.LinkOptions) ui.Node {
		if options.Attrs == nil {
			options.Attrs = ui.Attrs{}
		}
		options.Attrs["id"] = id
		return ui.El("p", nil, ui.Link(nextgo.MustURL(c, path), options, ui.Text(label)))
	}
	return nextgo.Page{Title: "Viewport prefetch — Next.go", Description: "Typed Go links opt into bounded production viewport prefetch while ordinary anchors retain intent behavior.", Body: ui.El("section", ui.Attrs{"class": "prose", "data-prefetch-demo": ""},
		ui.El("h1", nil, ui.Text("Prefetch when a Go link is visible")),
		link("prefetch-visible", "/posts?vp=visible", "Visible automatic link", ui.LinkOptions{}),
		ui.El("p", nil, ui.Text("ui.Link creates an ordinary server-rendered anchor. In production, visible links may prefetch within the browser runtime's concurrency, connection and visibility limits. Navigation still validates the current server response when required.")),
		counter.View(counter.Props{Label: "Prefetch companion", Initial: 3}),
		link("prefetch-intent", "/posts?vp=intent", "Intent-only link", ui.LinkOptions{Prefetch: ui.PrefetchIntent}),
		link("prefetch-none", "/posts?vp=none", "Disabled automatic link", ui.LinkOptions{Prefetch: ui.PrefetchNone}),
		ui.El("p", nil, ui.El("a", ui.Attrs{"id": "prefetch-legacy", "href": nextgo.MustURL(c, "/posts?vp=legacy")}, ui.Text("Legacy intent link"))),
		ui.El("details", nil, ui.El("summary", nil, ui.Text("Links that keep their normal browser behavior")),
			link("prefetch-api", "/api/health", "API endpoint", ui.LinkOptions{}),
			ui.El("p", nil, ui.Link("https://example.invalid/prefetch", ui.LinkOptions{Attrs: ui.Attrs{"id": "prefetch-external"}}, ui.Text("External link"))),
			ui.El("p", nil, ui.Link(nextgo.MustAssetURL(c, "/style.css"), ui.LinkOptions{Attrs: ui.Attrs{"id": "prefetch-download", "download": ""}}, ui.Text("Download style sheet"))),
			link("prefetch-blank", "/posts?vp=blank", "Open another tab", ui.LinkOptions{Attrs: ui.Attrs{"target": "_blank"}}),
			link("prefetch-reload", "/posts?vp=reload", "Explicit document reload", ui.LinkOptions{Attrs: ui.Attrs{"data-nextgo-reload": ""}}),
			ui.El("p", nil, ui.Link("#prefetch-offscreen", ui.LinkOptions{Attrs: ui.Attrs{"id": "prefetch-fragment"}}, ui.Text("Jump to the offscreen example")))),
		link("prefetch-replace", "/about?vp=replace", "Replace navigation", ui.LinkOptions{Prefetch: ui.PrefetchIntent, Replace: true}),
		ui.El("section", ui.Attrs{"class": "prefetch-offscreen-zone", "data-prefetch-offscreen-zone": ""},
			ui.El("h2", nil, ui.Text("Scroll to reveal more automatic links")),
			link("prefetch-offscreen", "/posts?vp=offscreen", "Offscreen automatic link", ui.LinkOptions{}),
			link("prefetch-shell", "/partial/alpha?message=Viewport%20private", "Public shell link", ui.LinkOptions{}),
			ui.El("p", nil, ui.Text("A partial page may prefetch only its public shell. Clicking still contacts the server for the current request's private regions."))))}, nil
}
