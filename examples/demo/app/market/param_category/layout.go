//go:build !js

package category

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, n ui.Node) (ui.Node, error) {
	return ui.El("section", ui.Attrs{"data-catalog-category": c.Params["category"]}, ui.El("p", nil, ui.Text("Catalog category: "+c.Params["category"])), n), nil
}
