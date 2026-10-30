package panel

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

// Every changing live datum is explicit in Props. The fallback and initial
// ClientView use this same pure Go view; local drafts are separate browser state.
func Body(p Props, interactive bool) ui.Node {
	kind := "fallback"
	if interactive {
		kind = "client"
	}
	button := func(name string) ui.Attrs {
		attrs := ui.Attrs{"type": "button", "data-merge-" + name: ""}
		if !interactive {
			attrs["disabled"] = ""
		}
		return attrs
	}
	return ui.El("section", ui.Attrs{"data-merge-panel": p.Name, "data-merge-kind": kind, "aria-label": "Merge panel " + p.Name},
		ui.El("h2", nil, ui.Text("Panel "+p.Name)),
		ui.El("p", nil, ui.Text("Product "), ui.El("output", ui.Attrs{"data-merge-product": ""}, ui.Text(p.Product))),
		ui.El("p", nil, ui.Text("Current price "), ui.El("output", ui.Attrs{"data-merge-price": ""}, ui.Text(p.Price))),
		ui.El("p", nil, ui.Text("Query "), ui.El("output", ui.Attrs{"data-merge-query": ""}, ui.Text(p.Query))),
		ui.El("p", nil, ui.Text("Raw query "), ui.El("output", ui.Attrs{"data-merge-raw-query": ""}, ui.Text(p.RawQuery))),
		ui.El("p", nil, ui.Text("Public server version "), ui.El("output", ui.Attrs{"data-merge-version": ""}, ui.Text(p.Version))),
		ui.El("p", nil, ui.Text("Local counter "), ui.El("output", ui.Attrs{"data-merge-count": ""}, ui.Text(strconv.Itoa(p.Initial)))),
		ui.El("button", button("increment"), ui.Text("Increment "+p.Name)),
		ui.El("label", nil, ui.Text("Local draft "+p.Name+" "), ui.El("input", ui.Attrs{"data-merge-draft": "", "value": ""})),
		ui.El("label", nil, ui.Text("Local file "), ui.El("input", ui.Attrs{"type": "file", "accept": ".txt", "data-merge-file": ""})),
		ui.El("label", nil, ui.Text("Nested scroll "), ui.El("textarea", ui.Attrs{"data-merge-scroll": "", "rows": "3"}, ui.Text("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n"))),
		ui.El("button", button("refresh"), ui.Text("Refresh current route")),
		ui.El("p", nil, ui.Text("Render revision "), ui.El("output", ui.Attrs{"data-merge-revision": ""}, ui.Text("0"))),
		ui.El("p", nil, ui.Text("Mount count "), ui.El("output", ui.Attrs{"data-merge-mount": ""}, ui.Text("0"))),
		ui.El("p", nil, ui.Text("Cross-root snapshot "), ui.El("output", ui.Attrs{"data-merge-coherent": ""}, ui.Text("awaiting Go"))),
	)
}
