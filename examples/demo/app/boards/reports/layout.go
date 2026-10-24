//go:build !js

package reports

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/examples/demo/components/selectionnav"
	"github.com/Suhaibinator/Next.go/ui"
)

func Layout(c *nextgo.Context, body ui.Node) (ui.Node, error) {
	detail, err := c.Slot("reportdetail")
	if err != nil {
		return nil, err
	}
	own, err := c.Selection("children")
	if err != nil {
		return nil, err
	}
	side, err := c.Selection("reportdetail")
	if err != nil {
		return nil, err
	}
	badge := selectionnav.View(selectionnav.Props{Label: "Report selections", Items: []selectionnav.Item{{Label: "children", Selection: own}, {Label: "reportdetail", Selection: side}}})
	return ui.El("section", nil, badge, body, detail, counter.View(counter.Props{Initial: 70, Label: "Reports shell"})), nil
}
