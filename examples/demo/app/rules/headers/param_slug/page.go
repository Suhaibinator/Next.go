//go:build !js

package headerpage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Response headers — Next.go", Body: ui.El("article", nil,
		ui.El("h1", nil, ui.Text("Response headers from Go")), ui.El("p", nil, ui.Text("Inspect X-Demo-Stage, X-Demo-Slug and X-Demo-Variant on document or navigation responses. Enable NEXTGO_DEMO_HEADER_RULES=true to apply the policy.")), ui.El("p", ui.Attrs{"data-header-slug": ""}, ui.Text(c.Params["slug"])), counter.View(counter.Props{Label: "Header rules counter", Initial: 2}),
		ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/rules/headers/next?variant=green")}, ui.Text("Next header page")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/rules/headers/article?variant=blue")}, ui.Text("Blue header page")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/api/header-policy"), "data-nextgo-reload": ""}, ui.Text("Header policy API")))}, nil
}
