//go:build !js

package sharedprerender

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Shared prerendering — Next.go", Body: ui.El("section", ui.Attrs{"class": "prose"},
		ui.El("h1", nil, ui.Text("Shared prerendering")),
		ui.El("p", nil, ui.Text("Public build snapshots can seed the same authority used for runtime regeneration. A reserved, revocable build ticket prevents a later process from resurrecting a generation invalidated after its build.")),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-prerender/alpha")}, ui.Text("Open build path alpha")), ui.Text(" · "),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-prerender/beta")}, ui.Text("Open build path beta")), ui.Text(" · "),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-prerender/new")}, ui.Text("Generate an unlisted path"))),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-pages")}, ui.Text("Compare shared runtime pages"))))}, nil
}
