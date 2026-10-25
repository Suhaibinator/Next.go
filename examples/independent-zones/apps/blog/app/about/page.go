//go:build !js

package about

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
	"independent.example/blog/internal/state"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "blog release ownership", Body: ui.El("article", ui.Attrs{"data-independent-about": "blog"}, ui.El("h1", nil, ui.Text("blog is a separate Go module")), ui.El("p", nil, ui.Text("Native implementation: "+state.NativeVersion)), ui.El("p", nil, ui.Text("Same-application navigation retains the shared layout. Another release can become active without restarting the peer's native process. Retained assets are served by their exact declared release.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return to blog home")))}, nil
}
