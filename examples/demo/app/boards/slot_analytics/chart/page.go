//go:build !js

package chart

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if c.Request.Header.Get("X-Demo-Slot-Deny") == "yes" {
		return nextgo.Page{}, nextgo.ErrForbidden
	}
	return nextgo.Page{Body: ui.Fragment(ui.El("h2", nil, ui.Text("Chart analytics")), counter.View(counter.Props{Initial: 5, Label: "Chart analytics"}))}, nil
}
