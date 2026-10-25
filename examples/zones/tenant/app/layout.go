//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/tenant/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	info := nextgo.CurrentZone(c)
	return ui.Fragment(ui.El("header", ui.Attrs{"data-zone-layout": info.ID}, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("NEXT.GO / TENANT "+info.Tenant)), ui.El("nav", ui.Attrs{"aria-label": "Tenant navigation"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Identity fixture")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/dashboard")}, ui.Text("Member dashboard"))), counter.View(counter.Props{Label: info.ID, Initial: 1})), ui.El("main", ui.Attrs{"id": "main", "tabindex": "-1"}, body)), nil
}
