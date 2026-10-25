//go:build !js

package details

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Form lifecycle — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("The forms have left the page.")), ui.El("p", nil, ui.Text("Their scopes release listeners and cancel field work. A late result must not update a detached form. Returning creates fresh public previews.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return to form previews")))}, nil
}
