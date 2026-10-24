//go:build !js

package lazy

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/examples/demo/components/lazycounter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	target := nextgo.MustURL(c, "/lazy/details")
	return nextgo.Page{Title: "Lazy Go modules — Next.go", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("Lazy Go browser components")), ui.El("p", nil, ui.Text("These islands are already HTML. Their browser package downloads only when needed.")), counter.View(counter.Props{Label: "Eager companion", Initial: 3}), lazycounter.View(lazycounter.Props{Label: "First lazy counter", Initial: 5, Target: target}), lazycounter.View(lazycounter.Props{Label: "Second lazy counter", Initial: 10, Target: target}), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/lazy/stream")}, ui.Text("Stream a lazy island")))}, nil
}
