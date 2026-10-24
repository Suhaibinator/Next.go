//go:build !js

package optimistic

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func View(p Props) ui.Node {
	button := func(key, label string) ui.Node {
		return ui.El("button", ui.Attrs{"type": "button", "class": "reset", key: "", "disabled": ""}, ui.Text(label))
	}
	return ui.Island("optimistic", p, ui.El("section", ui.Attrs{"class": "counter", "aria-label": "Optimistic mutations"},
		ui.El("h2", nil, ui.Text("Confirm, reject, or cancel a change")),
		ui.El("p", nil, ui.Text("Each change appears immediately. The server accepts at most two units per request, so adding five demonstrates a canonical correction.")),
		ui.El("output", ui.Attrs{"data-optimistic-value": "", "aria-label": "Optimistic count", "aria-live": "polite", "aria-busy": "false"}, ui.Text(strconv.FormatInt(p.Initial, 10))),
		ui.El("p", ui.Attrs{"data-optimistic-pending": "", "role": "status"}, ui.Text("No pending changes.")),
		ui.El("div", ui.Attrs{"class": "counter-actions"}, button("data-optimistic-add", "Add one"), button("data-optimistic-canonical", "Add five"), button("data-optimistic-reject", "Try a rejected change"), button("data-optimistic-cancel", "Cancel pending changes"), button("data-optimistic-refresh", "Refresh server value")),
		ui.El("p", ui.Attrs{"data-optimistic-error": "", "role": "alert", "hidden": ""}, ui.Text("")),
		ui.El("p", ui.Attrs{"data-optimistic-notice": "", "class": "small"}, ui.Text("This public demonstration uses process-local data. Real mutations need authentication and durable storage."))))
}
