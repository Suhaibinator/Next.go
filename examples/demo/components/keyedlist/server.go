//go:build !js

package keyedlist

import "github.com/Suhaibinator/Next.go/ui"

func View(p Props) ui.Node {
	rows := []ui.Node{}
	for i, item := range p.Items {
		if p.Mismatch && i == 1 {
			item.ID = "unexpected"
		}
		rows = append(rows, row(item, p.Table))
	}
	host := ui.El("ul", ui.Attrs{"data-rows": ""}, rows...)
	if p.Table {
		host = ui.El("table", ui.Attrs{"aria-label": p.Name}, ui.El("tbody", ui.Attrs{"data-rows": ""}, rows...))
	}
	button := func(attr, label string) ui.Node {
		return ui.El("button", ui.Attrs{"type": "button", attr: "", "disabled": ""}, ui.Text(label))
	}
	return ui.Island("keyedlist", p, ui.El("section", ui.Attrs{"aria-label": p.Name, "data-list-name": p.Name},
		ui.El("h2", nil, ui.Text(p.Name)), ui.El("p", nil, ui.Text("Rows are server rendered. Stable keys preserve existing controls and Go state.")),
		ui.El("label", nil, ui.Text("New row label"), ui.El("input", ui.Attrs{"data-new-label": "", "aria-label": "New row label"})),
		button("data-add", "Add row"), button("data-update", "Update first row"), button("data-reverse", "Reverse rows"), button("data-rotate", "Rotate rows"), button("data-invalid", "Reject duplicate keys"), button("data-fail", "Reject invalid batch"), button("data-limit", "Reject oversized list"),
		host, ui.El("p", ui.Attrs{"role": "status", "data-list-status": ""}, ui.Text("Waiting for Go")),
		ui.El("a", ui.Attrs{"href": p.Target}, ui.Text("Next list page"))))
}
