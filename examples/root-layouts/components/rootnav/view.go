//go:build !js

package rootnav

import "github.com/Suhaibinator/Next.go/ui"

func View() ui.Node {
	return ui.Island("rootnav", Props{}, ui.El("section", nil,
		ui.El("button", ui.Attrs{"type": "button", "data-root-push": "", "disabled": ""}, ui.Text("Navigate to workspace")),
		ui.El("button", ui.Attrs{"type": "button", "data-root-replace": "", "disabled": ""}, ui.Text("Replace with workspace")),
		ui.El("output", ui.Attrs{"data-root-error": ""})))
}
