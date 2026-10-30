//go:build !js

package layouts

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, child ui.Node) (ui.Node, error) {
	return ui.El("section", ui.Attrs{"data-merge-layout": "outer", "data-layout-query": c.Query.Get("q")}, ui.El("p", ui.Attrs{"data-layout-fresh": "outer"}, ui.Text("Outer layout query "+c.Query.Get("q"))), child), nil
}
