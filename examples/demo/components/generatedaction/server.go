//go:build !js

package generatedaction

import (
	"strconv"

	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node {
	return ui.Island("generatedaction", p, ui.El("section", ui.Attrs{"class": "counter", "aria-label": "Generated action counter"},
		ui.El("p", nil, ui.Text("Confirmed count: "), ui.El("output", ui.Attrs{"data-generated-count": ""}, ui.Text(strconv.FormatInt(p.Count, 10)))),
		ui.El("p", nil, ui.Text("Confirmed revision: "), ui.El("output", ui.Attrs{"data-generated-revision": ""}, ui.Text(strconv.FormatInt(p.Revision, 10)))),
		ui.El("form", ui.Attrs{"method": "post", "action": p.Action, "data-generated-form": ""},
			ui.El("label", ui.Attrs{"for": "generated-amount"}, ui.Text("Generated amount")),
			ui.El("input", ui.Attrs{"id": "generated-amount", "name": "amount", "type": "number", "min": "1", "max": "5", "required": "", "value": "1", "data-generated-amount": "", "aria-describedby": "generated-amount-error"}),
			ui.El("span", ui.Attrs{"id": "generated-amount-error", "role": "alert", "data-generated-error": ""}),
			ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-generated-json": ""}, ui.Text("Save with generated Go caller")),
			ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-generated-multipart": ""}, ui.Text("Save with generated multipart caller")),
			ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-generated-fail": ""}, ui.Text("Try a private server failure")),
			ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Save with native generated form"))),
		ui.El("p", ui.Attrs{"role": "status", "data-generated-status": ""}, ui.Text("Ready.")),
		ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-generated-refresh": ""}, ui.Text("Refresh generated server snapshot"))))
}
