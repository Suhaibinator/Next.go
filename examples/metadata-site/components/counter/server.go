//go:build !js

package counter

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	return ui.Island("counter", p, ui.El("section", ui.Attrs{"aria-label": "Persistent Go counter"},
		ui.El("button", ui.Attrs{"type": "button", "data-increment": "", "disabled": ""}, ui.Text("Increase retained count")),
		ui.Text(" "), ui.El("output", ui.Attrs{"data-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial)))))
}
