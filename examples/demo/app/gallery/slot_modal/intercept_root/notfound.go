//go:build !js

package intercept_root

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/modal"
	"github.com/Suhaibinator/Next.go/ui"
)

func NotFound(c *nextgo.Context) (nextgo.Page, error) {
	from, ok := c.InterceptedFrom()
	if !ok {
		from = "/gallery"
	}
	return nextgo.Page{Body: modal.View(modal.Props{Title: "Photo unavailable", ReturnTo: nextgo.MustURL(c, from)}, ui.El("p", nil, ui.Text("This photo does not exist.")))}, nil
}
