//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func NotFound(c *nextgo.Context) (nextgo.Page, error) {
	body, err := Layout(c, ui.El("section", ui.Attrs{"class": "prose"}, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("404 / OFF THE MAP")), ui.El("h1", nil, ui.Text("Nothing here. Yet.")), ui.El("p", nil, ui.Text("This page could not be found. The next good place to start is home.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/"), "class": "button primary"}, ui.Text("Back to the overview →"))))
	return nextgo.Page{Title: "Page not found — Next.go", Body: body}, err
}
