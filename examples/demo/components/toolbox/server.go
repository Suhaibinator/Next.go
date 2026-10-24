//go:build !js

package toolbox

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	return ui.Island("toolbox", p, ui.El("section", ui.Attrs{"class": "counter", "aria-label": "Server action toolbox"},
		ui.El("h2", nil, ui.Text("Server actions and router controls")),
		ui.El("p", nil, ui.Text("Server mutation count: "), ui.El("output", ui.Attrs{"data-server-count": "", "aria-live": "polite"}, ui.Text(strconv.FormatInt(p.Count, 10)))),
		ui.El("div", ui.Attrs{"class": "counter-actions"},
			ui.El("button", ui.Attrs{"type": "button", "data-action-increment": "", "disabled": ""}, ui.Text("Increment on server")),
			ui.El("button", ui.Attrs{"type": "button", "data-action-only": "", "disabled": ""}, ui.Text("Increment without refreshing page")),
			ui.El("button", ui.Attrs{"type": "button", "data-refresh": "", "disabled": ""}, ui.Text("Refresh server data")),
			ui.El("button", ui.Attrs{"type": "button", "data-navigate": "", "disabled": ""}, ui.Text("Navigate to field notes")),
			ui.El("button", ui.Attrs{"type": "button", "data-replace": "", "data-route-target": "/posts/html-first", "disabled": ""}, ui.Text("Replace with first field note")),
			ui.El("button", ui.Attrs{"type": "button", "data-prefetch": "", "disabled": ""}, ui.Text("Prefetch field notes"))),
		ui.El("div", ui.Attrs{"class": "counter-actions"},
			ui.El("label", nil, ui.Text("Guide path "), ui.El("input", ui.Attrs{"data-guide-path": "", "type": "text", "value": "guide/getting-started", "maxlength": "1000"})),
			ui.El("button", ui.Attrs{"type": "button", "data-guide-navigate": "", "disabled": ""}, ui.Text("Open typed guide")),
			ui.El("button", ui.Attrs{"type": "button", "data-catalog-navigate": "", "disabled": ""}, ui.Text("Open catalog root"))),
		ui.El("p", ui.Attrs{"data-action-status": "", "role": "status"}, ui.Text("Server-rendered data. Controls activate after hydration."))))
}
