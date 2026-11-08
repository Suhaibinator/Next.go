//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/comparison/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Framework comparison", Body: ui.El("main", ui.Attrs{"data-comparison-page": "home"},
		ui.El("h1", nil, ui.Text("Framework comparison")), ui.El("p", nil, ui.Text("Rendered on the server.")),
		counter.View(counter.Props{Initial: 3}),
		ui.Link(nextgo.MustURL(c, "/about"), ui.LinkOptions{Prefetch: ui.PrefetchNone, Attrs: ui.Attrs{"data-about": ""}}, ui.Text("About")))}, nil
}
