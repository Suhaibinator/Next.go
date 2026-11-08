//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/social-variants/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	return ui.Fragment(ui.El("header", nil, ui.El("nav", ui.Attrs{"aria-label": "Image pages"},
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Original formats")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/clear")}, ui.Text("Clear image defaults"))),
		counter.View(counter.Props{Initial: 2})), ui.El("main", nil, body)), nil
}
