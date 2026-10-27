//go:build !js

package products

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, child ui.Node) (ui.Node, error) {
	return ui.El("article", ui.Attrs{"data-merge-layout": "inner", "data-layout-query": c.Query.Get("q")}, ui.El("p", ui.Attrs{"data-layout-fresh": "inner"}, ui.Text("Inner layout query "+c.Query.Get("q"))), child), nil
}
