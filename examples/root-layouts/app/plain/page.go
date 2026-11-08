//go:build !js

package plain

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Implicit document root", Body: ui.El("main", ui.Attrs{"data-root-layout": "implicit"}, ui.El("h1", nil, ui.Text("No persistent layout")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/"), "data-nextgo-prefetch": "false"}, ui.Text("Marketing home")))}, nil
}
