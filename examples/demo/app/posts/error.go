//go:build !js

package posts

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Error(c *nextgo.Context, _ error) (nextgo.Page, error) {
	return nextgo.Page{Title: "Server error", Body: ui.El("section", ui.Attrs{"class": "counter", "data-posts-error": "", "role": "alert"}, ui.El("h1", nil, ui.Text("Unable to load field notes")), ui.El("p", nil, ui.Text("The server could not load this page. Please try again.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts")}, ui.Text("Try again")))}, nil
}
