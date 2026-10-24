//go:build !js

package scriptwidget

import (
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node {
	return ui.Fragment(scripts.Node(p.Dependency), ui.Island("scriptwidget", p, ui.El("section", ui.Attrs{"aria-label": p.Label, "class": "counter"}, ui.El("h2", nil, ui.Text(p.Label)), ui.El("output", ui.Attrs{"data-script-result": "", "aria-live": "polite"}, ui.Text("Waiting for trusted library")), ui.El("output", ui.Attrs{"data-script-count": ""}, ui.Text("0")), ui.El("button", ui.Attrs{"type": "button", "data-script-increment": "", "disabled": ""}, ui.Text("Increase Go widget")), ui.El("button", ui.Attrs{"type": "button", "data-script-retry": "", "hidden": ""}, ui.Text("Retry library")))))
}
