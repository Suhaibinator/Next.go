//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func NotFound(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "marketing route not found", Body: ui.El("section", ui.Attrs{"data-zone-not-found": "marketing"}, ui.El("h1", nil, ui.Text("marketing: no matching page")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return home")))}, nil
}
