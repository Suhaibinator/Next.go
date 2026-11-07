//go:build !js

package lazyordinary

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(props Props) ui.Node {
	return ui.Island("lazyordinary", props, ui.El("section", ui.Attrs{"data-lazy-ordinary": ""},
		ui.El("h2", nil, ui.Text("Lazy ordinary Go source")),
		ui.El("p", ui.Attrs{"data-lazy-caption": ""}, ui.Text(props.Caption)),
		ui.El("output", ui.Attrs{"data-lazy-query": ""}, ui.Text(props.Query)),
		ui.El("output", ui.Attrs{"data-lazy-count": ""}, ui.Text(strconv.Itoa(props.Initial))),
		ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-lazy-increment": ""}, ui.Text("Increment lazy counter")),
		ui.El("label", nil, ui.Text("Lazy local draft "), ui.El("input", ui.Attrs{"data-lazy-draft": "", "value": ""})),
		ui.El("output", ui.Attrs{"data-lazy-revision": ""}, ui.Text("0")),
		ui.El("output", ui.Attrs{"data-lazy-location": ""}, ui.Text("server")),
		ui.El("output", ui.Attrs{"data-lazy-mount": ""}, ui.Text("0")),
		ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-lazy-read": ""}, ui.Text("Read lazy props")),
		ui.El("a", ui.Attrs{"href": "/?q=denied", "data-nextgo-prefetch": "false", "data-lazy-deny": ""}, ui.Text("Request protected root")),
		ui.El("a", ui.Attrs{"href": "/lazy-props?q=b", "data-nextgo-prefetch": "false", "data-lazy-next": ""}, ui.Text("Fresh lazy query")),
	))
}
