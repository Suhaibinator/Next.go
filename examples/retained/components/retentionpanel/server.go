//go:build !js

package retentionpanel

import (
	"strconv"
	"strings"

	"github.com/Suhaibinator/Next.go/ui"
)

func View(props Props) ui.Node { return WithContent(props) }

// WithContent permits the eager island itself to be the sole route owner.
func WithContent(props Props, before ...ui.Node) ui.Node {
	panel := ui.El("section", ui.Attrs{"data-retained-panel": props.Name, "aria-label": "Retained panel " + props.Name},
		ui.El("output", ui.Attrs{"data-retained-count": ""}, ui.Text(strconv.Itoa(props.Initial))),
		ui.El("button", ui.Attrs{"type": "button", "data-retained-increment": "", "disabled": ""}, ui.Text("Increment")),
		ui.El("label", nil, ui.Text("Draft "), ui.El("input", ui.Attrs{"data-retained-draft": "", "value": ""})),
		ui.El("details", ui.Attrs{"data-retained-details": ""}, ui.El("summary", nil, ui.Text("Keep this section open")), ui.El("p", nil, ui.Text("The details element and its open state remain the same nodes."))),
		ui.El("label", nil, ui.Text("Scrollable text "), ui.El("textarea", ui.Attrs{"rows": "4", "data-retained-scroll": ""}, ui.Text(strings.Repeat("A retained scroll line.\n", 40)))),
		ui.El("p", nil, ui.Text("Visible clock: "), ui.El("output", ui.Attrs{"data-retained-ticks": ""}, ui.Text("0"))),
		ui.El("p", nil, ui.Text("Effect setups/cleanups: "), ui.El("output", ui.Attrs{"data-retained-setups": ""}, ui.Text("0")), ui.Text("/"), ui.El("output", ui.Attrs{"data-retained-cleanups": ""}, ui.Text("0"))),
		ui.El("button", ui.Attrs{"type": "button", "data-retained-task": "", "disabled": ""}, ui.Text("Start owned task")),
		ui.El("p", nil, ui.Text("Task completed/canceled: "), ui.El("output", ui.Attrs{"data-retained-task-completed": ""}, ui.Text("0")), ui.Text("/"), ui.El("output", ui.Attrs{"data-retained-task-canceled": ""}, ui.Text("0"))),
	)
	children := append(append([]ui.Node{}, before...), panel)
	return ui.Island("retentionpanel", props, ui.Fragment(children...))
}
