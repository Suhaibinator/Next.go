//go:build !js

package about

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
	"independent.example/alpha/internal/state"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "alpha release ownership", Body: ui.El("article", ui.Attrs{"data-independent-about": "alpha"}, ui.El("h1", nil, ui.Text("alpha is a separate Go module")), ui.El("p", nil, ui.Text("Native implementation: "+state.NativeVersion)), ui.El("p", nil, ui.Text("Same-application navigation retains the shared layout. Another release can become active without restarting the peer's native process. Retained assets are served by their exact declared release.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return to alpha home")))}, nil
}
