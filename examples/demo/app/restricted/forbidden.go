//go:build !js

package restricted

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Forbidden(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Access denied — Next.go", Body: ui.El("section", ui.Attrs{"class": "prose", "data-access-forbidden": ""}, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("403 / PERMISSION REQUIRED")), ui.El("h1", nil, ui.Text("Administrator access required")), ui.El("p", nil, ui.Text("Your credentials are valid, but this report requires administrator access.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/features")}, ui.Text("Return to framework features")))}, nil
}
