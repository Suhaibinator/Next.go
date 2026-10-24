//go:build !js

package templates

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Template(_ *nextgo.Context, children ui.Node) (ui.Node, error) {
	return ui.El("section", ui.Attrs{"data-template-example": ""},
		ui.El("p", nil, ui.Text("The template counter persists for deeper routes and query changes. Changing its child segment resets it.")),
		counter.View(counter.Props{Label: "Template counter", Initial: 0}), children), nil
}
