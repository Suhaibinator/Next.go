//go:build !js

package path

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/routes"
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	params, err := routes.ReadCatalogByPath(c.Params)
	if err != nil {
		return nextgo.Page{}, err
	}
	path := strings.Join(params.Path, "/")
	if path == "" {
		path = "(catalog root)"
	}
	return nextgo.Page{Title: "Catalog route — Next.go", Body: ui.El("section", ui.Attrs{"class": "counter"}, ui.El("h1", nil, ui.Text("Optional catch-all catalog")), ui.El("p", nil, ui.Text("Captured path: "), ui.El("code", ui.Attrs{"data-optional-path": ""}, ui.Text(path))), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features")}, ui.Text("Back to features")))}, nil
}
