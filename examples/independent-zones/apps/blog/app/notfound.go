//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func NotFound(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "blog route not found", Body: ui.El("article", ui.Attrs{"data-independent-not-found": "blog"}, ui.El("h1", nil, ui.Text("blog: no matching page")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return home")))}, nil
}
