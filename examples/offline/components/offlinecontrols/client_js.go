//go:build js && wasm

package offlinecontrols

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/Suhaibinator/Next.go/client"
)

// Offline control belongs to the eager document runtime. A lazy component may
// interact locally, but cannot own the application's service-worker lifecycle.
func Mount(scope *client.Scope, raw json.RawMessage) error {
	if _, err := client.DecodeProps[Props](raw); err != nil {
		return err
	}
	root, err := scope.Require("[data-offline-controls]")
	if err != nil {
		return err
	}
	output, err := scope.Require("[data-offline-status-output]")
	if err != nil {
		return err
	}
	buttons := make(map[string]client.Element)
	for _, action := range []string{"enable", "status", "update", "disable"} {
		selector := "[data-offline-" + action + "]"
		if action == "status" {
			selector = "[data-offline-status-button]"
		}
		button, err := scope.Require(selector)
		if err != nil {
			return err
		}
		buttons[action] = button
	}
	closed, busy, actionFailed := false, false, false
	operation := ""
	scope.Cleanup(func() { closed = true })
	render := func(state client.OfflineState) {
		if closed {
			return
		}
		root.Attribute("data-offline-enabled", strconv.FormatBool(state.Enabled))
		root.Attribute("data-offline-controlling", strconv.FormatBool(state.Controlling))
		root.Attribute("data-offline-saved-document", strconv.FormatBool(state.SavedDocument))
		root.Attribute("data-offline-cleanup-pending", strconv.FormatBool(state.CleanupPending))
		root.Attribute("data-offline-active-generation", state.ActiveGeneration)
		root.Attribute("data-offline-waiting-generation", state.WaitingGeneration)
		controllerBusy := false
		controllerMessage := ""
		switch state.Phase {
		case "checking":
			controllerBusy, controllerMessage = true, "Checking this application's saved public edition…"
		case "enabling":
			controllerBusy, controllerMessage = true, "Saving the approved anonymous public edition…"
		case "updating":
			controllerBusy, controllerMessage = true, "Checking for a public edition update…"
		case "disabling":
			controllerBusy, controllerMessage = true, "Disabling saved-page serving…"
		}
		for _, button := range buttons {
			button.Property("disabled", busy || controllerBusy || !state.Supported || !state.Configured)
		}
		phase := state.Phase
		if phase == "" {
			phase = "disabled"
		}
		message := "Saved anonymous public pages are not enabled."
		switch {
		case !state.Supported:
			phase, message = "unsupported", "This browser cannot save the public edition. Online reading still works."
		case !state.Configured:
			phase, message = "unconfigured", "This document has no approved saved-public-page configuration. Online reading still works."
		case busy:
			phase, message = "working", operation
		case controllerBusy:
			message = controllerMessage
		case actionFailed || state.LastError != "":
			phase, message = "error", "The saved-public-page operation could not be confirmed. Check status before trying again; an existing saved edition may remain."
		case state.CleanupPending:
			message = "Saved-page serving is disabled. Public cached data is retained while controlled tabs remain. Close those tabs, then explicitly use Enable offline or Disable offline on a later online visit to reconcile retained data. Background cleanup is not guaranteed."
		case state.Enabled && state.ActiveGeneration == "":
			message = "Saving is enabled. Waiting for a complete public edition; keep this tab online."
		case state.Enabled:
			message = "A saved anonymous public edition is available. It may be older than the server."
		}
		if state.Controlling {
			message += " This tab is controlled by this application's worker."
		} else if state.Enabled {
			message += " Reload once after installation if this tab is not controlled."
		}
		if state.SavedDocument {
			message += " You are reading a saved public document."
		}
		if state.WaitingGeneration != "" {
			message += " An update is waiting. Close every tab in this application scope before reopening."
		}
		output.Text(message)
		output.Attribute("data-offline-state", phase)
	}
	if err := client.SubscribeOffline(scope, render); err != nil {
		return err
	}
	run := func(message string, operationFn func(context.Context) error) {
		if closed || busy {
			return
		}
		busy, actionFailed, operation = true, false, message
		render(client.OfflineStatus())
		if err := scope.Go(func(ctx context.Context) {
			err := operationFn(ctx)
			if closed || ctx.Err() != nil {
				return
			}
			busy, actionFailed, operation = false, err != nil, ""
			render(client.OfflineStatus())
		}); err != nil {
			busy, actionFailed, operation = false, true, ""
			render(client.OfflineStatus())
		}
	}
	scope.On(buttons["status"], "click", func(client.Event) {
		run("Checking this application's saved public edition…", client.RefreshOfflineStatus)
	})
	scope.On(buttons["enable"], "click", func(client.Event) {
		run("Saving the approved anonymous public edition…", client.EnableOffline)
	})
	scope.On(buttons["update"], "click", func(client.Event) {
		run("Checking for a public edition update…", client.CheckOfflineUpdate)
	})
	scope.On(buttons["disable"], "click", func(client.Event) {
		run("Disabling saved-page serving…", client.DisableOffline)
	})
	root.Attribute("data-offline-controls-ready", "true")
	return nil
}
