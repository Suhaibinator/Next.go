//go:build !js

package details

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/lazycounter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Lazy navigation — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("Navigation from another Go runtime")), lazycounter.View(lazycounter.Props{Label: "Detail lazy counter", Initial: 1, Target: nextgo.MustURL(c, "/about")}))}, nil
}
