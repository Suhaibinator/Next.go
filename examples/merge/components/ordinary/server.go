//go:build !js

package ordinary

import "github.com/Suhaibinator/Next.go/ui"

func View(p Props) ui.Node {
	var sourceProbe ui.Node = ui.Fragment()
	if p.InitialSource {
		sourceProbe = ui.Fragment(ui.El("output", ui.Attrs{"data-ordinary-props-revision": ""}, ui.Text("0")), ui.El("output", ui.Attrs{"data-ordinary-props-location": ""}, ui.Text("server")), ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-ordinary-read": ""}, ui.Text("Read current props source")), ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-ordinary-refresh": ""}, ui.Text("Refresh ordinary source")), ui.El("a", ui.Attrs{"href": "?q=b", "data-nextgo-prefetch": "false", "data-ordinary-next": ""}, ui.Text("Next ordinary query")))
	}
	return ui.Island("ordinary", p, ui.El("section", ui.Attrs{"data-merge-ordinary": ""},
		ui.El("h2", nil, ui.Text("Ordinary server-rendered component")), ui.El("p", ui.Attrs{"data-ordinary-caption": ""}, ui.Text(p.Caption)),
		ui.El("output", ui.Attrs{"data-ordinary-count": ""}, ui.Text("2")), ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-ordinary-increment": ""}, ui.Text("Increment ordinary")),
		ui.El("label", nil, ui.Text("Ordinary draft "), ui.El("input", ui.Attrs{"data-ordinary-draft": "", "value": ""})),
		sourceProbe,
	))
}
