//go:build !js

package lazycounter

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(props Props) ui.Node {
	return ui.Island("lazycounter", props, ui.El("section", ui.Attrs{"class": "counter", "aria-label": props.Label}, ui.El("h2", nil, ui.Text(props.Label)), ui.El("output", ui.Attrs{"data-lazy-count": "", "aria-live": "polite"}, ui.Text(strconv.Itoa(props.Initial))), ui.El("button", ui.Attrs{"type": "button", "data-lazy-increment": "", "disabled": ""}, ui.Text("Add one in lazy Go")), ui.El("button", ui.Attrs{"type": "button", "data-lazy-navigate": "", "disabled": ""}, ui.Text("Navigate from lazy Go")), ui.El("output", ui.Attrs{"data-lazy-location": ""}, ui.Text("Waiting for Go")), ui.El("output", ui.Attrs{"data-lazy-proof": ""}, ui.Text("Server-rendered island"))))
}
