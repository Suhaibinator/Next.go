//go:build !js

package clientpanel

import "github.com/Suhaibinator/Next.go/ui"

// Fallback remains useful with JavaScript disabled, a delayed runtime, or a
// failed Mount. It contains ordinary native controls, not another Go island.
func Fallback(p Props) ui.Node {
	return ui.El("section", ui.Attrs{"class": "counter", "aria-label": p.Label + " fallback", "data-client-panel-fallback": ""},
		ui.El("h2", nil, ui.Text(p.Label)),
		ui.El("p", nil, ui.Text("This browser-only panel is waiting for Go WebAssembly. Its browser view will replace this fallback inside the same boundary.")),
		ui.El("label", nil, ui.Text("Local fallback draft"),
			ui.El("input", ui.Attrs{"type": "text", "value": "Fallback draft", "maxlength": "80", "autocomplete": "off", "data-client-panel-draft": ""})))
}

func View(p Props) ui.Node { return ui.ClientIsland("clientpanel", p, Fallback(p)) }
