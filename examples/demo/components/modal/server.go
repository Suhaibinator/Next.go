//go:build !js

package modal

import "github.com/Suhaibinator/Next.go/ui"

func View(p Props, body ui.Node) ui.Node {
	return ui.Island("modal", p, ui.El("dialog", ui.Attrs{"open": "", "aria-label": p.Title, "data-modal": "", "class": "photo-modal"},
		ui.El("button", ui.Attrs{"type": "button", "data-close": "", "data-nextgo-focus": "", "disabled": "", "aria-label": "Close photo"}, ui.Text("Close")),
		ui.El("button", ui.Attrs{"type": "button", "data-refresh": "", "disabled": "", "aria-label": "Refresh photo"}, ui.Text("Refresh")),
		ui.El("h2", nil, ui.Text(p.Title)), ui.El("p", ui.Attrs{"data-modal-error": "", "role": "alert", "hidden": ""}), body))
}
