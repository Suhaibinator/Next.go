//go:build !js

package prerenderdemo

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
	"time"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Prerendered Go — Next.go", Body: ui.El("article", nil,
		ui.El("h1", nil, ui.Text("Build once, revalidate in Go")),
		ui.El("p", ui.Attrs{"data-generated": ""}, ui.Text(time.Now().UTC().Format(time.RFC3339Nano))),
		ui.El("p", ui.Attrs{"data-query": ""}, ui.Text(c.Query.Get("message"))),
		counter.View(counter.Props{Label: "Prerendered counter", Initial: 4}),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/prerender/first")}, ui.Text("Prerendered article")),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/prerender/new")}, ui.Text("Generate an article on demand")),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/prerender/fixed/unknown")}, ui.Text("Unlisted article")),
	)}, nil
}
