//go:build !js

package detail

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(_ *nextgo.Context, body ui.Node) (ui.Node, error) {
	return ui.El("section", ui.Attrs{"data-detail-shell": ""}, body, counter.View(counter.Props{Initial: 60, Label: "Detail shell"})), nil
}
