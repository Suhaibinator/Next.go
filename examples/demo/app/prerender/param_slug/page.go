//go:build !js

package article

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
	"time"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Prerendered article — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text(c.Params["slug"])), ui.El("p", ui.Attrs{"data-generated": ""}, ui.Text(time.Now().UTC().Format(time.RFC3339Nano))), counter.View(counter.Props{Label: "Article counter", Initial: 1}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/prerender")}, ui.Text("Prerender home")))}, nil
}
