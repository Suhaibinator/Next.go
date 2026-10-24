//go:build !js

package gallery

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	modal, err := c.Slot("modal")
	return ui.Fragment(body, modal), err
}
