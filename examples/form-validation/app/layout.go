//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	return ui.Fragment(ui.El("header", nil, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("NEXT.GO / PUBLIC FORM PREVIEW")), ui.El("nav", ui.Attrs{"aria-label": "Form pages"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Form previews")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/details")}, ui.Text("Leave forms")))), ui.El("main", ui.Attrs{"id": "main", "tabindex": "-1"}, body), ui.El("footer", nil, ui.Text("Public synthetic data. Preview responses only; nothing is stored."))), nil
}
