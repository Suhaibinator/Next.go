//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	// Resolve the declared peer mount with the same public path helper.
	peer, err := (nextgo.PathConfig{BasePath: "/blog"}).URL("/")
	if err != nil {
		return nil, err
	}
	return ui.Fragment(ui.El("header", ui.Attrs{"data-independent-layout": "alpha"}, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("NEXT.GO / INDEPENDENT alpha")), ui.El("nav", ui.Attrs{"aria-label": "alpha navigation"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("alpha home")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about")}, ui.Text("alpha about")), ui.El("a", ui.Attrs{"href": peer, "data-independent-cross": ""}, ui.Text("Visit blog app")))), ui.El("main", ui.Attrs{"id": "main", "tabindex": "-1"}, body), ui.El("footer", nil, ui.Text("Public process-local preview. No authentication or durable data."))), nil
}
