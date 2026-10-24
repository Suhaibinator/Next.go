//go:build !js

package transitioncard

import "github.com/Suhaibinator/Next.go/ui"

func View() ui.Node {
	return ui.Island("transitioncard", Props{}, ui.El("section", ui.Attrs{"class": "counter", "aria-label": "Go view transition"},
		ui.El("h2", nil, ui.Text("Animate an explicit Go state update")),
		ui.El("output", ui.Attrs{"data-transition-count": "", "aria-live": "polite", "class": "transition-card"}, ui.Text("0")),
		ui.El("button", ui.Attrs{"type": "button", "data-transition-increment": "", "disabled": ""}, ui.Text("Animate count")),
		ui.El("button", ui.Attrs{"type": "button", "data-transition-navigate": "", "disabled": ""}, ui.Text("Go navigation without animation")),
		ui.El("p", ui.Attrs{"data-transition-error": "", "role": "status"})))
}
