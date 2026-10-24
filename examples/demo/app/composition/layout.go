//go:build !js

package compositionpage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/composition"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	return ui.Fragment(composition.View(composition.Props{Name: "Retained composition", Initial: 9, Target: nextgo.MustURL(c, "/composition/details")}), body), nil
}
