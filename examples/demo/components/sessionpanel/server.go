//go:build !js

package sessionpanel

import "github.com/Suhaibinator/Next.go/ui"

func View(props Props, report ui.Node) ui.Node {
	loginButton := ui.Attrs{"type": "submit", "data-session-login": ""}
	logoutButton := ui.Attrs{"type": "submit", "data-session-logout": ""}
	if !props.Enabled {
		loginButton["disabled"] = ""
	}
	if !props.Enabled || props.Role == "anonymous" {
		logoutButton["disabled"] = ""
	}
	return ui.Island("sessionpanel", props, ui.El("section", ui.Attrs{"class": "counter", "aria-label": "Signed cookie session"},
		ui.El("p", nil, ui.Text("Current panel role: "), ui.El("output", ui.Attrs{"data-session-panel-role": ""}, ui.Text(props.Role))),
		ui.El("form", ui.Attrs{"method": "post", "action": props.LoginAction, "data-session-login-form": "", "autocomplete": "off", "novalidate": ""},
			ui.El("label", ui.Attrs{"for": "cookie-session-credential"}, ui.Text("Demo session credential")),
			ui.El("input", ui.Attrs{"id": "cookie-session-credential", "name": "credential", "type": "password", "minlength": "32", "maxlength": "512", "required": "", "autocomplete": "off", "data-session-credential": "", "aria-describedby": "cookie-session-credential-error"}),
			ui.El("span", ui.Attrs{"id": "cookie-session-credential-error", "role": "alert", "data-session-error": ""}),
			ui.El("button", loginButton, ui.Text("Sign in with cookie and refresh")),
			ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-session-login-only": ""}, ui.Text("Sign in without refreshing"))),
		ui.El("form", ui.Attrs{"method": "post", "action": props.LogoutAction, "data-session-logout-form": ""}, ui.El("button", logoutButton, ui.Text("Sign out and refresh"))),
		report,
		ui.El("p", ui.Attrs{"role": "status", "data-session-status": ""}, ui.Text("The server verifies the signed cookie before reading session data.")),
		ui.El("button", ui.Attrs{"type": "button", "disabled": "", "data-session-refresh": ""}, ui.Text("Refresh cookie session"))))
}
