//go:build !js

package path

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/routes"
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	params, err := routes.ReadDocsByPath(c.Params)
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Documentation route — Next.go", Body: ui.El("section", ui.Attrs{"class": "counter"}, ui.El("h1", nil, ui.Text("Catch-all documentation route")), ui.El("p", nil, ui.Text("Captured path: "), ui.El("code", ui.Attrs{"data-catchall-path": ""}, ui.Text(strings.Join(params.Path, "/")))), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features")}, ui.Text("Back to features")))}, nil
}
