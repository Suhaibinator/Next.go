//go:build !js

package category

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	products := Products(c.Params["category"])
	if len(products) == 0 {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	nodes := []ui.Node{ui.El("h1", nil, ui.Text("Browse "+c.Params["category"]))}
	for _, name := range products {
		nodes = append(nodes, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/market/"+c.Params["category"]+"/"+name)}, ui.Text("Product "+name)))
	}
	nodes = append(nodes, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/market/"+c.Params["category"]+"/summary")}, ui.Text("Category summary")))
	return nextgo.Page{Title: "Category — Next.go", Body: ui.El("article", nil, nodes...)}, nil
}
