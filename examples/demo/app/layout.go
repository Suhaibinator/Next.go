//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/draft"
	"github.com/Suhaibinator/Next.go/examples/demo/components/layoutstate"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	var banner ui.Node = ui.Fragment()
	if draft.Enabled(c) {
		banner = ui.El("aside", ui.Attrs{"role": "status", "data-preview-banner": ""}, ui.Text("Draft preview is on. "), ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/api/preview/disable")}, ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Exit preview"))))
	}
	link := func(href, label string) ui.Node {
		attrs := ui.Attrs{"href": nextgo.MustURL(c, href)}
		if c.Request.URL.Path == href {
			attrs["aria-current"] = "page"
		}
		return ui.El("a", attrs, ui.Text(label))
	}
	return ui.Fragment(
		banner,
		ui.El("header", ui.Attrs{"class": "site-header", "lang": "en", "dir": "ltr"},
			ui.El("a", ui.Attrs{"class": "brand", "href": nextgo.MustURL(c, "/")}, ui.El("span", ui.Attrs{"class": "brand-icon"}, ui.Text("n")), ui.Text("next.go")),
			ui.El("nav", ui.Attrs{"aria-label": "Main"}, link("/", "Overview"), link("/posts", "Field notes"), link("/gallery", "Gallery"), link("/about", "Architecture"), link("/features", "Features")),
			layoutstate.View(layoutstate.Props{Location: nextgo.MustURL(c, c.Request.URL.RequestURI())}),
			ui.El("span", ui.Attrs{"class": "version"}, ui.Text("EXPERIMENTAL / v0.1"))),
		ui.El("main", ui.Attrs{"id": "main", "tabindex": "-1"}, body),
		ui.El("footer", ui.Attrs{"class": "site-footer", "lang": "en", "dir": "ltr"}, ui.El("span", nil, ui.Text("Go from request to interaction.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/api/health")}, ui.Text("API status ↗")), ui.El("span", ui.Attrs{"class": "muted"}, ui.Text("Go · HTML · WebAssembly")))), nil
}
