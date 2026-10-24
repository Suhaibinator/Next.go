//go:build !js

package settings

import "github.com/Suhaibinator/Next.go/ui"

func View(p Props) ui.Node {
	return ui.Island("settings", p, ui.El("section", nil,
		ui.El("p", ui.Attrs{"data-setting-message": ""}, ui.Text(p.Message)),
		ui.El("button", ui.Attrs{"type": "button", "data-setting-read": "", "disabled": ""}, ui.Text("Read browser configuration")),
		ui.El("output", ui.Attrs{"data-setting-result": "", "aria-live": "polite"}, ui.Text("Waiting for interaction"))))
}
