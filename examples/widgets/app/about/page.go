//go:build !js

package about

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Chart details", Body: ui.El("main", nil,
		ui.El("h1", nil, ui.Text("Chart details")), ui.El("p", nil, ui.Text("The sample data remains readable without the chart library.")),
		ui.Link(nextgo.MustURL(c, "/"), ui.LinkOptions{Prefetch: ui.PrefetchNone}, ui.Text("Chart home")))}, nil
}
