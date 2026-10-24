//go:build !js

package profileform

import "github.com/Suhaibinator/Next.go/ui"

func View(p Props) ui.Node {
	prefix := p.IDPrefix
	if prefix == "" {
		prefix = "profile-" + p.Profile.ID
	}
	fieldID := func(name string) string { return prefix + "-" + name }
	errorID := func(name string) string { return prefix + "-error-" + name }
	input := func(name, label, kind, value string) ui.Node {
		attrs := ui.Attrs{"name": name, "type": kind, "value": value, "data-field": name, "id": fieldID(name), "aria-describedby": errorID(name)}
		return ui.El("div", nil, ui.El("label", ui.Attrs{"for": fieldID(name)}, ui.Text(label)), ui.El("input", attrs), ui.El("span", ui.Attrs{"data-error": name, "id": errorID(name), "role": "alert"}))
	}
	name := p.Profile.Name
	if p.Mismatch {
		name = "Unexpected server name"
	}
	newsletter := ui.Attrs{"name": "newsletter", "type": "checkbox", "value": "on", "data-field": "newsletter", "id": fieldID("newsletter")}
	if p.Profile.Newsletter {
		newsletter["checked"] = ""
	}
	options := []ui.Node{}
	for _, role := range []string{"reader", "editor"} {
		attrs := ui.Attrs{"value": role}
		if role == p.Profile.Role {
			attrs["selected"] = ""
		}
		options = append(options, ui.El("option", attrs, ui.Text(role)))
	}

	rootTag := "form"
	rootAttrs := ui.Attrs{"method": "post", "action": p.Action, "data-profile-form": ""}
	save := ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Save profile"))
	reset := ui.El("button", ui.Attrs{"type": "reset", "data-reset-form": ""}, ui.Text("Reset to saved values"))
	var native ui.Node = ui.Fragment()
	description := "This public demo record is process-local. Native POST and Go submission use the same validated server handler."
	if p.Local {
		rootTag = "section"
		rootAttrs = ui.Attrs{"data-profile-form": ""}
		description = "A local Go preview with no server mutation. Apply and reset become available after WASM loads."
		save = ui.El("button", ui.Attrs{"type": "button", "data-local-save": "", "disabled": ""}, ui.Text("Apply local preview"))
		reset = ui.El("button", ui.Attrs{"type": "button", "data-reset-form": "", "disabled": ""}, ui.Text("Reset to saved values"))
	} else {
		native = ui.El("button", ui.Attrs{"type": "submit", "name": "intent", "value": "save"}, ui.Text("Save with native POST"))
	}
	return ui.Island("profileform", p, ui.El("section", ui.Attrs{"aria-label": "Profile editor"}, ui.El("h1", nil, ui.Text("A typed Go form")), ui.El("p", nil, ui.Text(description)),
		ui.El(rootTag, rootAttrs,
			ui.El("input", ui.Attrs{"type": "hidden", "name": "id", "value": p.Profile.ID, "data-field": "id"}), ui.El("input", ui.Attrs{"type": "hidden", "name": "version", "value": p.Profile.Version, "data-field": "version"}),
			input("name", "Display name", "text", name), input("email", "Email", "email", p.Profile.Email),
			ui.El("label", ui.Attrs{"for": fieldID("bio")}, ui.Text("About")),
			ui.El("textarea", ui.Attrs{"id": fieldID("bio"), "name": "bio", "data-field": "bio", "aria-describedby": errorID("bio")}, ui.Text("\n"+p.Profile.Bio)),
			ui.El("span", ui.Attrs{"data-error": "bio", "id": errorID("bio"), "role": "alert"}),
			ui.El("label", ui.Attrs{"for": fieldID("role")}, ui.Text("Role")), ui.El("select", ui.Attrs{"name": "role", "id": fieldID("role"), "data-field": "role", "aria-describedby": errorID("role")}, options...), ui.El("span", ui.Attrs{"data-error": "role", "id": errorID("role"), "role": "alert"}),
			ui.El("label", ui.Attrs{"for": fieldID("newsletter")}, ui.El("input", newsletter), ui.Text("Newsletter")), save, reset,
			ui.El("button", ui.Attrs{"type": "button", "data-cancel-form": "", "disabled": ""}, ui.Text("Cancel submission")), native,
			ui.El("p", ui.Attrs{"role": "status", "data-form-status": ""}, ui.Text("Ready.")), ui.El("output", ui.Attrs{"data-form-dirty": ""}, ui.Text("Saved values"))),
		ui.El("p", nil, ui.El("a", ui.Attrs{"href": p.Target}, ui.Text("Leave editor"))), ui.El("button", ui.Attrs{"type": "button", "data-refresh-form": "", "disabled": ""}, ui.Text("Reload server profile"))))
}
