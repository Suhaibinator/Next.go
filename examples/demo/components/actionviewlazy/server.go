//go:build !js

package actionviewlazy

import (
	"strconv"

	"github.com/Suhaibinator/Next.go/ui"
)

func View(props Props) ui.Node {
	return ui.Island("actionviewlazy", props, ui.El("section", ui.Attrs{"class": "counter", "aria-label": "Lazy Go action view"},
		ui.El("h2", nil, ui.Text("A separate Go/WASM caller")),
		ui.El("p", nil, ui.Text("Server count: "), ui.El("output", ui.Attrs{"data-av-lazy-count": ""}, ui.Text(strconv.FormatInt(props.Count, 10))), ui.Text("; revision: "), ui.El("output", ui.Attrs{"data-av-lazy-revision": ""}, ui.Text(strconv.FormatInt(props.Revision, 10)))),
		ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-av-lazy-save": ""}, ui.Text("Save from lazy Go and stream view")),
		ui.El("p", ui.Attrs{"role": "status", "data-av-lazy-status": ""}, ui.Text("Waiting for the lazy Go runtime."))))
}
