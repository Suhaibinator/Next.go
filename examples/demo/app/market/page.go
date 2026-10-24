//go:build !js

package catalog

import (
	nextgo "github.com/Suhaibinator/Next.go"
	category "github.com/Suhaibinator/Next.go/examples/demo/app/market/param_category"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	nodes := []ui.Node{ui.El("h1", nil, ui.Text("Composed catalog")), ui.El("p", nil, ui.Text("Parent categories feed child product enumeration during builds and static exports."))}
	for _, name := range category.Categories(c) {
		nodes = append(nodes, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/market/"+name)}, ui.Text("Browse "+name)))
	}
	return nextgo.Page{Title: "Composed catalog — Next.go", Body: ui.El("article", nil, nodes...)}, nil
}
