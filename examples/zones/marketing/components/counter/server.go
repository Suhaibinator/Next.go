//go:build !js

package counter

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	return ui.Island("counter", p, ui.El("section", ui.Attrs{"data-zone-counter": p.Label, "aria-label": p.Label + " layout counter"}, ui.El("button", ui.Attrs{"type": "button", "data-zone-increment": "", "disabled": ""}, ui.Text("Increase "+p.Label+" count")), ui.Text(" "), ui.El("output", ui.Attrs{"data-zone-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial))), ui.Text(" / Go client: "), ui.El("output", ui.Attrs{"data-zone-client": ""}, ui.Text("Waiting for Go"))))
}
