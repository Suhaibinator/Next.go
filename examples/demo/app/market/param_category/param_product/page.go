//go:build !js

package product

import (
	nextgo "github.com/Suhaibinator/Next.go"
	category "github.com/Suhaibinator/Next.go/examples/demo/app/market/param_category"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
	"slices"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if !slices.Contains(category.Products(c.Params["category"]), c.Params["product"]) {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	return nextgo.Page{Title: "Catalog product — Next.go", Body: ui.El("article", ui.Attrs{"data-catalog-product": c.Params["product"]}, ui.El("h1", nil, ui.Text(c.Params["category"]+" / "+c.Params["product"])), counter.View(counter.Props{Label: "Catalog counter", Initial: 7}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/market/"+c.Params["category"]+"/summary")}, ui.Text("Category summary")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/market/"+c.Params["category"])}, ui.Text("Back to category")))}, nil
}
