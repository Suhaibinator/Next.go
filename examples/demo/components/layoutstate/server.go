//go:build !js

package layoutstate

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	return ui.Island("layoutstate", p, ui.El("span", ui.Attrs{"class": "small", "aria-label": "Persistent layout counter"},
		ui.El("button", ui.Attrs{"type": "button", "data-layout-increment": "", "disabled": ""}, ui.Text("Layout count "), ui.El("output", ui.Attrs{"data-layout-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial)))),
		ui.El("output", ui.Attrs{"data-route-location": "", "aria-label": "Current browser route"}, ui.Text(p.Location))))
}
