//go:build !js

package composition

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func Panel(name string, initial int) ui.Node {
	return ui.El("section", ui.Attrs{"data-panel": name, "aria-label": name},
		ui.El("h2", nil, ui.Text(name)), ui.El("output", ui.Attrs{"data-value": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(initial))),
		ui.El("button", ui.Attrs{"data-increment": "", "type": "button", "disabled": ""}, ui.Text("Increment "+name)),
		ui.El("p", ui.Attrs{"data-effect": "", "role": "status"}, ui.Text("Waiting for Go")))
}
func View(p Props) ui.Node {
	if p.Name == "" {
		p.Name = "Page composition"
	}
	button := func(attr, label string) ui.Node {
		return ui.El("button", ui.Attrs{attr: "", "type": "button", "disabled": ""}, ui.Text(label))
	}
	return ui.Island("composition", p, ui.El("section", ui.Attrs{"data-composition": p.Name, "aria-label": p.Name},
		ui.El("h1", nil, ui.Text("Composed Go components")), ui.El("p", nil, ui.Text("Two child components inherit shared state; a third shadows it. Their effects release resources before updating.")),
		button("data-parent-increment", "Increment shared provider"), button("data-remove-child", "Dispose inherited child"), button("data-fail-effect", "Fail inherited effect"),
		Panel("Inherited", p.Initial), Panel("Sibling", p.Initial), Panel("Shadowed", 100),
		ui.El("p", ui.Attrs{"data-owner-status": "", "role": "status"}, ui.Text("Waiting for Go")),
		ui.El("a", ui.Attrs{"href": p.Target, "data-nextgo-prefetch": "false"}, ui.Text("Next composition page"))))
}
