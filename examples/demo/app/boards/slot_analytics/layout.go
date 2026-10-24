//go:build !js

package analytics

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/ui"
)

// Matching pages and the unmatched default share this Go island. Navigation
// replaces the outlet while preserving the layout's mounted state.
func Layout(c *nextgo.Context, uiBody ui.Node) (ui.Node, error) {
	detail, err := c.Slot("detail")
	if err != nil {
		return nil, err
	}
	return ui.El("section", ui.Attrs{"data-analytics-shell": ""}, uiBody, counter.View(counter.Props{Initial: 40, Label: "Analytics shell"}), ui.El("aside", ui.Attrs{"aria-label": "Analytics detail"}, detail)), nil
}
