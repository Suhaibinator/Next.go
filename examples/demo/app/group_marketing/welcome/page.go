//go:build !js

package welcome

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Welcome — Next.go", Body: ui.El("section", ui.Attrs{"class": "counter"}, ui.El("h1", nil, ui.Text("A route group without a URL segment")), ui.El("p", nil, ui.Text("This page lives under app/group_marketing/welcome but is served at /welcome.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features")}, ui.Text("Back to features")))}, nil
}
