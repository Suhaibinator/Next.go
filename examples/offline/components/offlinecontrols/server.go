//go:build !js

package offlinecontrols

import "github.com/Suhaibinator/Next.go/ui"

func View() ui.Node {
	// Hydration, rather than restored browser button state, enables controls.
	// Status also performs one asynchronous query and waits for busy work.
	button := func(action, label string) ui.Node {
		if action == "status" {
			action = "status-button"
		}
		return ui.El("button", ui.Attrs{"type": "button", "autocomplete": "off", "data-offline-" + action: "", "disabled": ""}, ui.Text(label))
	}
	return ui.Island("offlinecontrols", Props{}, ui.El("section", ui.Attrs{"class": "panel", "data-offline-controls": "", "aria-label": "Saved anonymous public pages"},
		ui.El("h2", nil, ui.Text("Save anonymous public pages")),
		ui.El("p", nil, ui.Text("Saving is explicit. Only this application's approved public edition is eligible. Your current authenticated data and pending writes are never saved or replayed.")),
		ui.El("div", ui.Attrs{"class": "controls"}, button("enable", "Enable offline"), button("status", "Status of saved public pages"), button("update", "Check for update"), button("disable", "Disable offline")),
		ui.El("p", ui.Attrs{"role": "status", "aria-live": "polite", "data-offline-status": "", "data-offline-status-output": "", "data-offline-state": "waiting"}, ui.Text("Server HTML is ready. Waiting for Go to enable the saved-public-page controls.")),
		ui.El("p", ui.Attrs{"class": "note"}, ui.Text("If an update is waiting, close every tab for this application scope before reopening. Disabling stops saved-page serving. Public cached data can remain until controlled tabs close and a later explicit operation reconciles it; background cleanup is not guaranteed. Other sites' browser storage is not cleared.")),
	))
}
