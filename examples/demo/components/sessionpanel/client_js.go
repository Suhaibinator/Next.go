//go:build js && wasm

package sessionpanel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/examples/demo/actionrefs"
	"github.com/Suhaibinator/Next.go/mutation"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	if props.Role != "anonymous" && props.Role != "reader" && props.Role != "administrator" {
		return fmt.Errorf("invalid public session role")
	}
	nodes := map[string]client.Element{}
	for _, key := range []string{"panel-role", "credential", "error", "status", "login-form", "logout-form", "login", "login-only", "logout", "refresh"} {
		node, err := scope.Require("[data-session-" + key + "]")
		if err != nil {
			return err
		}
		nodes[key] = node
	}
	if nodes["panel-role"].Value.Get("textContent").String() != props.Role {
		return fmt.Errorf("session panel differs from server role")
	}
	ctx, cancel := context.WithCancel(context.Background())
	closed, busy := false, false
	currentRole := props.Role
	scope.Cleanup(cancel)
	scope.Cleanup(func() {
		closed = true
		// Never retain an entered credential on a disposed DOM node, including
		// the source node replaced by this call's own fresh page.
		nodes["credential"].Property("value", "")
		scope.Root.Attribute("data-session-closed", "true")
	})
	setBusy := func(value bool) {
		busy = value
		nodes["login"].Property("disabled", value || !props.Enabled)
		nodes["login-only"].Property("disabled", value || !props.Enabled)
		nodes["logout"].Property("disabled", value || !props.Enabled || currentRole == "anonymous")
		nodes["refresh"].Property("disabled", value)
		scope.Root.Attribute("aria-busy", fmt.Sprint(value))
	}
	showFailure := func(callErr error) {
		message := "The session change outcome is unknown. Refresh to reconcile before submitting again."
		var public *mutation.Error
		if errors.As(callErr, &public) {
			message = public.Message
			nodes["error"].Text(public.Fields["credential"])
		}
		nodes["status"].Text(message)
	}
	login := func(refresh bool) {
		if busy || !props.Enabled {
			return
		}
		credential := nodes["credential"].Value.Get("value").String()
		if len(credential) < 32 || len(credential) > 512 || strings.ContainsAny(credential, " \t\r\n") {
			nodes["error"].Text("Use a configured credential between 32 and 512 characters.")
			return
		}
		nodes["error"].Text("")
		nodes["status"].Text("Checking the credential on the server…")
		setBusy(true)
		go func() {
			input := actionrefs.SessionLoginInput{Credential: credential}
			var role string
			var view client.ActionViewOutcome
			var callErr error
			if refresh {
				var result mutation.RefreshResponse[actionrefs.SessionLoginOutput]
				result, callErr = actionrefs.SessionLoginAndRefresh(ctx, input)
				role, view = result.Data.Role, result.View
			} else {
				var result mutation.Response[actionrefs.SessionLoginOutput]
				result, callErr = actionrefs.SessionLogin(ctx, input)
				role = result.Data.Role
			}
			// These local strings never enter persistent component state/props.
			input.Credential, credential = "", ""
			if closed {
				return
			}
			if callErr != nil {
				setBusy(false)
				showFailure(callErr)
				return
			}
			nodes["credential"].Property("value", "")
			currentRole = role
			nodes["panel-role"].Text(role)
			setBusy(false)
			if !refresh {
				nodes["status"].Text("Signed in without refreshing. Refresh to read the server-authorized page.")
			} else if view.Err != nil || !view.Applied {
				nodes["status"].Text("Sign-in succeeded; the view could not finish. Refresh without submitting again.")
			} else {
				nodes["status"].Text("Signed in and refreshed.")
			}
		}()
	}
	scope.On(nodes["login-form"], "submit", func(event client.Event) { event.PreventDefault(); login(true) })
	scope.On(nodes["login-only"], "click", func(client.Event) { login(false) })
	scope.On(nodes["logout-form"], "submit", func(event client.Event) {
		event.PreventDefault()
		if busy || !props.Enabled {
			return
		}
		nodes["error"].Text("")
		nodes["status"].Text("Clearing this browser's cookie…")
		setBusy(true)
		go func() {
			result, callErr := actionrefs.SessionLogoutAndRefresh(ctx, actionrefs.SessionLogoutInput{})
			if closed {
				return
			}
			if callErr != nil {
				setBusy(false)
				showFailure(callErr)
				return
			}
			nodes["credential"].Property("value", "")
			currentRole = result.Data.Role
			nodes["panel-role"].Text(currentRole)
			// On a confirmed logout with a failed fresh view, stop displaying
			// already-delivered private report text while the user reconciles.
			if report, err := scope.Require("[data-session-admin-report]"); err == nil {
				report.Value.Call("remove")
			}
			setBusy(false)
			if result.View.Err != nil || !result.View.Applied {
				nodes["status"].Text("Signed out; the view could not finish. Refresh without repeating sign-out.")
			} else {
				nodes["status"].Text("Signed out and refreshed.")
			}
		}()
	})
	scope.On(nodes["refresh"], "click", func(client.Event) { client.Refresh() })
	if !props.Enabled {
		nodes["status"].Text("Cookie demo disabled: configure its native signing key and secure transport.")
	} else if props.Role == "anonymous" {
		nodes["status"].Text("Not signed in.")
	} else {
		nodes["status"].Text("Signed in as " + props.Role + ".")
	}
	setBusy(false)
	return nil
}
