//go:build !js

package counter

import (
	"strconv"

	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node {
	return ui.Island("counter", p, ui.El("section", ui.Attrs{"aria-label": "Counter", "data-comparison-counter": "", "data-ready": "false"},
		ui.El("output", ui.Attrs{"data-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial))),
		ui.El("button", ui.Attrs{"type": "button", "data-increment": "", "disabled": ""}, ui.Text("Increment"))))
}
