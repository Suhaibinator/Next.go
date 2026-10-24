//go:build !js

package posts

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Loading(*nextgo.Context) (ui.Node, error) {
	return ui.El("p", ui.Attrs{"data-posts-loading": "", "role": "status"}, ui.Text("Loading field notes from the server…")), nil
}
