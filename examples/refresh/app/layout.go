//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/refresh/components/eagerbadge"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	return ui.Fragment(ui.El("header", nil, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("NEXT.GO / DEVELOPMENT REFRESH")), ui.El("nav", ui.Attrs{"aria-label": "Refresh pages"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Stateful counters")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/details")}, ui.Text("Refresh boundaries"))), eagerbadge.View()), ui.El("main", ui.Attrs{"id": "main", "tabindex": "-1"}, body), ui.El("footer", nil, ui.Text("Only explicit public state cells opt into development replacement."))), nil
}
