//go:build !js

package counter

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	return ui.Island("counter", p, ui.El("section", ui.Attrs{"data-independent-instance": p.Instance, "aria-label": "alpha " + p.Instance + " counter"}, ui.El("h2", nil, ui.Text(p.Instance+" local counter")), ui.El("output", ui.Attrs{"data-independent-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial))), ui.El("button", ui.Attrs{"type": "button", "data-independent-increment": "", "disabled": ""}, ui.Text("Increment local alpha count")), ui.El("p", nil, ui.Text("Compiled client: "), ui.El("output", ui.Attrs{"data-independent-proof": ""}, ui.Text("Waiting for Go"))), ui.El("button", ui.Attrs{"type": "button", "data-independent-call": "", "disabled": ""}, ui.Text("Call typed alpha preview")), ui.El("p", ui.Attrs{"data-independent-status": "", "role": "status"}, ui.Text("Ready.")), ui.El("output", ui.Attrs{"data-independent-action-result": ""}, ui.Text("Not submitted"))))
}
