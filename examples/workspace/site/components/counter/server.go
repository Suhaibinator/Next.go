//go:build !js

package counter

import (
	"github.com/Suhaibinator/Next.go/ui"
	"workspace.example/shared"
)

func View() ui.Node {
	return ui.Island("counter", Props{Label: shared.Label, Step: shared.Step}, ui.El("section", ui.Attrs{"aria-label": shared.Label}, ui.El("p", ui.Attrs{"data-workspace-label": ""}, ui.Text(shared.Label)), ui.El("output", ui.Attrs{"data-workspace-count": "", "aria-live": "polite"}, ui.Text("0")), ui.El("button", ui.Attrs{"type": "button", "data-workspace-increment": "", "disabled": ""}, ui.Text("Add shared step"))))
}
