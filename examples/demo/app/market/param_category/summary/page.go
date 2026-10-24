//go:build !js

package summary

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Category summary — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("Summary: "+c.Params["category"])), ui.El("p", nil, ui.Text("This static child inherits its parent's parameter enumeration.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/market/"+c.Params["category"])}, ui.Text("Back to category")))}, nil
}
