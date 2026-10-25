//go:build !js

package validationform

import (
	"encoding/json"
	"slices"
	"strconv"

	"github.com/Suhaibinator/Next.go/examples/form-validation/model"
	"github.com/Suhaibinator/Next.go/ui"
)

func View(p Props) ui.Node {
	id := func(name string) string { return "fv-" + p.Instance + "-" + name }
	text := func(name, label, value string) ui.Node {
		return ui.El("div", ui.Attrs{"class": "field"},
			ui.El("label", ui.Attrs{"for": id(name)}, ui.Text(label)), ui.El("input", ui.Attrs{"id": id(name), "name": name, "value": value, "data-fv-field": name, "maxlength": "40", "aria-describedby": id(name+"-error") + " " + id(name+"-validation")}),
			ui.El("span", ui.Attrs{"data-fv-error": name, "id": id(name + "-error"), "role": "alert", "class": "field-error"}),
			ui.El("span", ui.Attrs{"data-fv-validation": name, "data-state": "idle", "id": id(name + "-validation"), "role": "status"}, ui.Text("Not checked.")),
			ui.El("div", ui.Attrs{"class": "buttons"}, ui.El("button", ui.Attrs{"type": "button", "data-fv-check": name, "disabled": ""}, ui.Text("Check "+name)), ui.El("button", ui.Attrs{"type": "button", "data-fv-retry": name, "disabled": ""}, ui.Text("Retry "+name))))
	}
	radios := []ui.Node{ui.El("legend", nil, ui.Text("Optional contact choice"))}
	for _, choice := range []struct{ value, key, label string }{{"", "empty", "Selected empty: no notifications"}, {"email", "email", "Email notifications"}} {
		a := ui.Attrs{"type": "radio", "name": "channel", "value": choice.value, "id": id("channel-" + choice.key), "data-fv-radio": choice.key, "aria-describedby": id("channel-error")}
		if p.Values.Channel.Selected && p.Values.Channel.Value == choice.value {
			a["checked"] = ""
		}
		radios = append(radios, ui.El("label", ui.Attrs{"for": a["id"]}, ui.El("input", a), ui.Text(choice.label)))
	}
	radios = append(radios, ui.El("button", ui.Attrs{"type": "button", "data-fv-clear-radio": "", "disabled": ""}, ui.Text("Clear radio selection")), ui.El("span", ui.Attrs{"data-fv-error": "channel", "id": id("channel-error"), "role": "alert", "class": "field-error"}))
	options := []ui.Node{}
	labels := []string{"Morning notes (notes)", "Evening notes (notes)", "Live events (events)", "Empty topic value"}
	for index, value := range model.OptionValues() {
		a := ui.Attrs{"value": value, "data-fv-option": strconv.Itoa(index)}
		if slices.Contains(p.Values.Topics, index) {
			a["selected"] = ""
		}
		options = append(options, ui.El("option", a, ui.Text(labels[index])))
	}
	indices, _ := json.Marshal(p.Values.Topics)
	values, _ := model.SelectedValues(p.Values.Topics)
	valuesJSON, _ := json.Marshal(values)
	return ui.Island("validationform", p, ui.El("section", ui.Attrs{"data-fv-instance": p.Instance, "aria-label": p.Instance + " public form"},
		ui.El("h2", nil, ui.Text(p.Instance+" preview")), ui.El("form", ui.Attrs{"method": "post", "action": p.Action, "data-fv-form": ""},
			text("name", "Public name", p.Values.Name), text("code", "Public code", p.Values.Code), ui.El("fieldset", nil, radios...),
			ui.El("label", ui.Attrs{"for": id("topics")}, ui.Text("Topic options (the first two share a value)")), ui.El("select", ui.Attrs{"id": id("topics"), "name": "topics", "multiple": "", "size": "4", "data-fv-topics": "", "aria-describedby": id("topics-error")}, options...), ui.El("span", ui.Attrs{"id": id("topics-error"), "data-fv-error": "topics", "role": "alert", "class": "field-error"}),
			ui.El("div", ui.Attrs{"class": "buttons"}, ui.El("button", ui.Attrs{"type": "submit", "data-fv-submit": ""}, ui.Text("Preview with Go")), ui.El("button", ui.Attrs{"type": "reset", "data-fv-reset": ""}, ui.Text("Reset confirmed values")), ui.El("button", ui.Attrs{"type": "submit", "name": "intent", "value": "native", "data-fv-native": ""}, ui.Text("Preview with native POST"))),
			ui.El("button", ui.Attrs{"type": "button", "data-fv-local-validate": "", "disabled": ""}, ui.Text("Validate local rules")),
			ui.El("p", ui.Attrs{"role": "status", "data-fv-status": ""}, ui.Text("Ready."))),
		ui.El("dl", nil, ui.El("dt", nil, ui.Text("Radio state")), ui.El("dd", ui.Attrs{"data-fv-channel-state": ""}, ui.Text(model.ChannelSummary(p.Values.Channel))), ui.El("dt", nil, ui.Text("Selected option indices")), ui.El("dd", ui.Attrs{"data-fv-option-indices": ""}, ui.Text(string(indices))), ui.El("dt", nil, ui.Text("Native option values")), ui.El("dd", ui.Attrs{"data-fv-option-values": ""}, ui.Text(string(valuesJSON)))),
		ui.El("p", nil, ui.Text("Last Go-confirmed indices: "), ui.El("output", ui.Attrs{"data-fv-confirmed-indices": ""}, ui.Text("Not submitted"))),
		ui.El("p", ui.Attrs{"class": "small"}, ui.Text("Native POST reports the submitted repeated values. Equal values alone do not identify which option was selected; the Go model and JSON preview retain exact indices.")),
	))
}
