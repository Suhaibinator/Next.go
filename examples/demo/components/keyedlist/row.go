package keyedlist

import "github.com/Suhaibinator/Next.go/ui"

// Row is shared by SSR and Go/WASM creation. Retained rows use explicit updates.
func Row(item Item) ui.Node { return row(item, false) }
func row(item Item, table bool) ui.Node {
	controls := []ui.Node{
		ui.El("span", ui.Attrs{"data-label": ""}, ui.Text(item.Label)),
		ui.El("label", nil, ui.Text("Edit "+item.ID), ui.El("input", ui.Attrs{"data-edit": "", "aria-label": "Edit " + item.ID, "value": item.Label})),
		ui.El("output", ui.Attrs{"data-clicks": "", "aria-live": "polite"}, ui.Text("0")),
		ui.El("button", ui.Attrs{"type": "button", "data-click": "", "disabled": ""}, ui.Text("Count "+item.ID)),
		ui.El("button", ui.Attrs{"type": "button", "data-remove": "", "disabled": ""}, ui.Text("Remove "+item.ID)),
	}
	tag := "li"
	if table {
		tag = "tr"
		for i, control := range controls {
			controls[i] = ui.El("td", nil, control)
		}
	}
	return ui.Keyed(tag, item.ID, ui.Attrs{"class": "counter", "data-row": ""}, controls...)
}
