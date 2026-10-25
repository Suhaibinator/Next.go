//go:build !js

package actionview

import (
	"strconv"

	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node {
	button := func(key, label string) ui.Node {
		return ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-av-" + key: ""}, ui.Text(label))
	}
	return ui.Island("actionview", p, ui.El("section", ui.Attrs{"class": "counter", "aria-label": "Combined action and view"},
		ui.El("p", nil, ui.Text("Confirmed count: "), ui.El("output", ui.Attrs{"data-av-count": ""}, ui.Text(strconv.FormatInt(p.Count, 10)))),
		ui.El("p", nil, ui.Text("Confirmed revision: "), ui.El("output", ui.Attrs{"data-av-revision": ""}, ui.Text(strconv.FormatInt(p.Revision, 10)))),
		ui.El("form", ui.Attrs{"method": "post", "action": p.Action},
			ui.El("label", ui.Attrs{"for": "action-view-amount"}, ui.Text("Action view amount")),
			ui.El("input", ui.Attrs{"id": "action-view-amount", "name": "amount", "type": "number", "min": "1", "max": "5", "required": "", "value": "1", "data-av-amount": "", "aria-describedby": "action-view-amount-error"}),
			ui.El("span", ui.Attrs{"id": "action-view-amount-error", "role": "alert", "data-av-error": ""}),
			button("json", "Save and stream Go view"),
			button("multipart", "Save multipart and stream Go view"),
			button("only", "Save without refreshing view"),
			button("invalid", "Try action view validation failure"),
			button("fail", "Try action view server failure"),
			button("viewfail", "Save then fail deferred view"),
			ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Submit native action view form"))),
		ui.El("p", ui.Attrs{"role": "status", "data-av-status": ""}, ui.Text("Ready.")),
		ui.El("section", ui.Attrs{"aria-label": "Last public action receipt", "data-av-receipt": "", "data-confirmed": "false", "data-applied": "false", "data-view-status": "0", "data-view-error": "false"},
			ui.El("p", nil, ui.Text("Last confirmed count: "), ui.El("output", ui.Attrs{"data-av-confirmed-count": ""}, ui.Text("None"))),
			ui.El("p", nil, ui.Text("Last confirmed revision: "), ui.El("output", ui.Attrs{"data-av-confirmed-revision": ""}, ui.Text("None")))),
		button("refresh", "Refresh action view without repeating mutation")))
}
