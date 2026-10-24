//go:build !js

package sharedpages

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Shared rendered pages — Next.go", Body: ui.El("section", ui.Attrs{"class": "prose"}, ui.El("h1", nil, ui.Text("Shared rendered pages")),
		ui.El("p", nil, ui.Text("Public route snapshots can share HTML, metadata and Go islands across app processes with the same build, configuration and public host.")),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-pages/alpha")}, ui.Text("Open shared alpha")), ui.Text(" · "), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-pages/beta")}, ui.Text("Open shared beta"))),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/shared-cache")}, ui.Text("Compare the shared data cache"))))}, nil
}
