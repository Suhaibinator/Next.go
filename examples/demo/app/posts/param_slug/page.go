//go:build !js

package slug

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/examples/demo/data"
	"github.com/Suhaibinator/Next.go/examples/demo/routes"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	params, err := routes.ReadPostsBySlug(c.Params)
	if err != nil {
		return nextgo.Page{}, err
	}
	p, err := data.FindForRequest(c.Context, params.Slug)
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Body: ui.El("article", ui.Attrs{"class": "prose"}, ui.El("p", ui.Attrs{"class": "eyebrow accent"}, ui.Text("ENGINEERING / "+p.Slug)), ui.El("h1", nil, ui.Text(p.Title)), ui.El("p", ui.Attrs{"class": "lead"}, ui.Text(p.Summary)), ui.El("p", nil, ui.Text(p.Body)), counter.View(counter.Props{Label: "Helpful votes", Initial: 0}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts")}, ui.Text("← All field notes")))}, nil
}
