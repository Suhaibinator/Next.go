//go:build !js

package refreshcounter

import (
	"github.com/Suhaibinator/Next.go/examples/refresh/shared"
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	var work ui.Node = ui.Fragment()
	if p.WorkURL != "" {
		work = ui.Fragment(ui.El("button", ui.Attrs{"type": "button", "data-refresh-work": "", "disabled": ""}, ui.Text("Start delayed public work")), ui.El("p", ui.Attrs{"data-refresh-work-status": "", "data-state": "idle", "role": "status"}, ui.Text("Idle.")))
	}
	toggle := ui.Attrs{"id": "refresh-" + p.Instance + "-enabled", "type": "checkbox", "data-refresh-enabled": ""}
	if p.Enabled {
		toggle["checked"] = ""
	}
	return ui.Island("refreshcounter", p, ui.El("section", ui.Attrs{"data-refresh-instance": p.Instance, "aria-label": p.Instance + " refresh counter"},
		ui.El("h2", ui.Attrs{"data-refresh-title": ""}, ui.Text(p.Label)), ui.El("output", ui.Attrs{"data-refresh-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial))),
		ui.El("label", ui.Attrs{"for": "refresh-" + p.Instance + "-label"}, ui.Text("Public label")), ui.El("input", ui.Attrs{"id": "refresh-" + p.Instance + "-label", "type": "text", "data-refresh-label": "", "value": p.Label, "maxlength": "80"}),
		ui.El("label", ui.Attrs{"for": toggle["id"]}, ui.El("input", toggle), ui.Text("Enable increment")),
		ui.El("button", ui.Attrs{"type": "button", "data-refresh-increment": "", "disabled": ""}, ui.Text("Increment in Go")),
		ui.El("p", nil, ui.Text("Compiled implementation: "), ui.El("output", ui.Attrs{"data-refresh-proof": ""}, ui.Text("Waiting for lazy Go"))),
		ui.El("p", ui.Attrs{"data-refresh-shared": ""}, ui.Text(shared.Message)),
		work,
	))
}
