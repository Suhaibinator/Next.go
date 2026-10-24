//go:build !js

package templates

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return Document(c), nil }

func Document(c *nextgo.Context) nextgo.Page {
	return nextgo.Page{Title: "Segment templates — Next.go", Body: ui.El("article", ui.Attrs{"class": "prose"}, ui.El("h1", ui.Attrs{"data-nextgo-focus": ""}, ui.Text("Segment templates")),
		ui.El("p", ui.Attrs{"data-template-route": ""}, ui.Text(c.Request.URL.RequestURI())),
		ui.El("nav", ui.Attrs{"aria-label": "Template routes"},
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/templates/a")}, ui.Text("Child A")),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/templates/a/details")}, ui.Text("Deeper in A")),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/templates/a?q=second")}, ui.Text("Query in A")),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/templates/b")}, ui.Text("Child B"))))}
}
