//go:build !js

package about

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Workspace architecture — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("A shared Go dependency")), ui.El("p", nil, ui.Text("The page and Go browser component import the same sibling module. Its server-only repository stays outside the browser dependency graph.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Back to the counter")))}, nil
}
