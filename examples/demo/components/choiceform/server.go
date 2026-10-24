//go:build !js

package choiceform

import (
	"github.com/Suhaibinator/Next.go/ui"
	"slices"
)

func View(p Props) ui.Node {
	id := func(name string) string { return p.Prefix + "-" + name }
	radios := []ui.Node{ui.El("legend", nil, ui.Text("Contact channel"))}
	for _, v := range []struct{ value, label string }{{"email", "Email notifications"}, {"sms", "SMS notifications"}} {
		a := ui.Attrs{"type": "radio", "name": "channel", "value": v.value, "id": id(v.value), "data-channel": v.value, "aria-describedby": id("channel-error")}
		if p.Values.Channel == v.value {
			a["checked"] = ""
		}
		radios = append(radios, ui.El("label", ui.Attrs{"for": id(v.value)}, ui.El("input", a), ui.Text(v.label)))
	}
	radios = append(radios, ui.El("span", ui.Attrs{"role": "alert", "id": id("channel-error"), "data-choice-error": "channel"}))
	options := []ui.Node{}
	for _, v := range []string{"routing", "ssr", "wasm"} {
		a := ui.Attrs{"value": v}
		if slices.Contains(p.Values.Topics, v) {
			a["selected"] = ""
		}
		options = append(options, ui.El("option", a, ui.Text(v)))
	}
	method := "post"
	submitText := "Validate on server"
	button := ui.Attrs{"type": "submit", "data-choice-apply": ""}
	reset := ui.Attrs{"type": "reset", "data-choice-reset": ""}
	var native ui.Node = ui.Fragment()
	description := "This public preview validates choices on the server. It does not save a record. Native POST returns the validated choices in the URL."
	if p.Local {
		method = "get"
		submitText = "Apply local choices"
		button["type"] = "button"
		button["disabled"] = ""
		reset["type"] = "button"
		reset["disabled"] = ""
		description = "Local Go choices with no server action. Each radio group has its own form owner."
	} else {
		native = ui.El("button", ui.Attrs{"type": "submit", "name": "intent", "value": "preview"}, ui.Text("Preview with native POST"))
	}
	return ui.Island("choiceform", p, ui.El("section", ui.Attrs{"aria-label": "Choice editor"}, ui.El("h1", nil, ui.Text("Typed Go choices")), ui.El("p", nil, ui.Text(description)),
		ui.El("form", ui.Attrs{"method": method, "action": p.Action, "data-choice-form": ""}, ui.El("fieldset", nil, radios...),
			ui.El("label", ui.Attrs{"for": id("topics")}, ui.Text("Topics")), ui.El("select", ui.Attrs{"multiple": "", "size": "3", "name": "topics", "id": id("topics"), "data-topics": "", "aria-describedby": id("topics-error")}, ui.El("optgroup", ui.Attrs{"label": "Framework topics"}, options...)), ui.El("span", ui.Attrs{"role": "alert", "id": id("topics-error"), "data-choice-error": "topics"}),
			ui.El("button", button, ui.Text(submitText)), ui.El("button", reset, ui.Text("Reset confirmed choices")), native, ui.El("p", ui.Attrs{"role": "status", "data-choice-status": ""}, ui.Text("Ready.")), ui.El("output", ui.Attrs{"data-choice-summary": ""}, ui.Text(p.Values.Channel+": "+joinTopics(p.Values.Topics)))), ui.El("a", ui.Attrs{"href": p.Target}, ui.Text("Leave choices"))))
}
