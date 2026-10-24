//go:build !js

package counter

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	return ui.Island("counter", p, ui.El("section", ui.Attrs{"class": "counter", "aria-label": p.Label},
		ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text(p.Label)),
		ui.El("output", ui.Attrs{"data-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial))),
		ui.El("div", ui.Attrs{"class": "counter-actions"},
			ui.El("button", ui.Attrs{"type": "button", "data-decrement": "", "disabled": "", "aria-label": "Decrease " + p.Label}, ui.Text("−")),
			ui.El("button", ui.Attrs{"type": "button", "data-increment": "", "disabled": "", "aria-label": "Increase " + p.Label}, ui.Text("+")),
			ui.El("button", ui.Attrs{"type": "button", "data-reset": "", "disabled": "", "class": "reset"}, ui.Text("Reset"))),
		ui.El("p", ui.Attrs{"class": "muted small"}, ui.Text("Rendered on the server. Powered by Go in your browser."))))
}
