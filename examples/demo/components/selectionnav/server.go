//go:build !js

package selectionnav

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	rows := []ui.Node{}
	for i, item := range p.Items {
		rows = append(rows, ui.El("p", nil, ui.Text(item.Label+": "), ui.El("output", ui.Attrs{"data-selection-index": strconv.Itoa(i), "aria-label": p.Label + " " + item.Label}, ui.Text(Text(item.Selection, true)))))
	}
	return ui.Island("selectionnav", p, ui.El("section", ui.Attrs{"aria-label": p.Label, "class": "small"}, ui.Fragment(rows...), ui.El("span", ui.Attrs{"data-selection-updates": "", "hidden": ""}, ui.Text("0"))))
}
