//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func NotFound(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "alpha route not found", Body: ui.El("article", ui.Attrs{"data-independent-not-found": "alpha"}, ui.El("h1", nil, ui.Text("alpha: no matching page")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return home")))}, nil
}
