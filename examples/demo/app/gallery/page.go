//go:build !js

package gallery

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if c.Request.Header.Get("X-Demo-Gallery-Deny") == "yes" {
		return nextgo.Page{}, nextgo.ErrForbidden
	}
	return nextgo.Page{Title: "Photo gallery", Body: ui.Fragment(ui.El("h1", nil, ui.Text("Photo gallery")),
		ui.El("p", nil, ui.Text("Open a photo to keep this Go counter and gallery in the background. Reload a photo URL for its standalone page.")),
		counter.View(counter.Props{Label: "Gallery counter", Initial: 10}),
		ui.El("nav", ui.Attrs{"aria-label": "Gallery photos", "id": "gallery-photos"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/photos/one")}, ui.Text("Open coastal photo")), ui.Text(" · "), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/photos/two")}, ui.Text("Open mountain photo")), ui.Text(" · "), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/photos/one"), "data-nextgo-replace": "true"}, ui.Text("Replace with coastal photo"))))}, nil
}
