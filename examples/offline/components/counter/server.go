//go:build !js

package counter

import (
	"strconv"

	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node {
	// Keep the SSR disabled gate on reload until Go installs its listener.
	// Firefox otherwise restores a button's previous dynamic disabled state.
	return ui.Island("counter", p, ui.El("section", ui.Attrs{"class": "panel", "data-offline-counter": "", "aria-label": "Lazy Go counter"},
		ui.El("h2", nil, ui.Text("A lazy Go counter")),
		ui.El("p", nil, ui.Text("Public document-local state: "), ui.El("output", ui.Attrs{"data-offline-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(p.Initial)))),
		ui.El("button", ui.Attrs{"type": "button", "autocomplete": "off", "data-offline-increment": "", "disabled": ""}, ui.Text("Increment in Go")),
		ui.El("p", ui.Attrs{"data-offline-counter-proof": ""}, ui.Text("Waiting for lazy Go")),
	))
}
