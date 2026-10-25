//go:build !js

package resourcepanel

import "github.com/Suhaibinator/Next.go/ui"

func View(props Props) ui.Node {
	return ui.Island("resourcepanel", props, ui.El("section", ui.Attrs{"class": "counter", "aria-label": "Scoped public resource"},
		ui.El("h2", nil, ui.Text("One explicit public source, two scoped views")),
		ui.El("p", nil, ui.Text("Primary: "), ui.El("output", ui.Attrs{"data-resource-primary": ""}, ui.Text(props.Initial.Message+" · "+props.Initial.Generated))),
		ui.El("p", nil, ui.Text("Secondary: "), ui.El("output", ui.Attrs{"data-resource-secondary": ""}, ui.Text(props.Initial.Message+" · "+props.Initial.Generated))),
		ui.El("p", ui.Attrs{"data-resource-status": "", "role": "status", "aria-live": "polite"}, ui.Text("Server-rendered public data. Hydration adopts its explicit seed without a request.")),
		ui.El("button", ui.Attrs{"type": "button", "data-resource-reuse": "", "disabled": ""}, ui.Text("Load with bounded reuse")),
		ui.El("button", ui.Attrs{"type": "button", "data-resource-refresh": "", "disabled": ""}, ui.Text("Invalidate and fetch")),
		ui.El("button", ui.Attrs{"type": "button", "data-resource-fail": "", "disabled": ""}, ui.Text("Request one server failure")),
		ui.El("button", ui.Attrs{"type": "button", "data-resource-retry": "", "disabled": ""}, ui.Text("Retry explicitly")),
		ui.El("a", ui.Attrs{"href": props.Endpoint, "data-nextgo-reload": ""}, ui.Text("Read the public JSON endpoint"))))
}
