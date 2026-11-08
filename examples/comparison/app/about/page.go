//go:build !js

package about

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Framework comparison", Body: ui.El("main", ui.Attrs{"data-comparison-page": "about"},
		ui.El("h1", nil, ui.Text("About comparison")), ui.El("p", nil, ui.Text("This route is rendered for each request.")),
		ui.Link(nextgo.MustURL(c, "/"), ui.LinkOptions{Prefetch: ui.PrefetchNone}, ui.Text("Home")))}, nil
}
