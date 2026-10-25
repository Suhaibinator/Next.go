// Package model defines public, ephemeral form preview values. Nothing is saved.
package model

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/client"
)

type Values struct {
	Name    string             `json:"name"`
	Code    string             `json:"code"`
	Channel client.RadioChoice `json:"channel"`
	Topics  []int              `json:"topics"`
}

type PreviewResult struct {
	Value   Values            `json:"value"`
	Fields  map[string]string `json:"fields,omitempty"`
	Message string            `json:"message"`
}

type ValidationResult struct {
	Message string `json:"message"`
}

func OptionValues() []string { return []string{"notes", "notes", "events", ""} }
func Clone(v Values) Values  { v.Topics = slices.Clone(v.Topics); return v }
func Initial(primary bool) Values {
	if primary {
		return Values{Name: "Ada", Code: "GO", Channel: client.RadioChoice{Selected: true}, Topics: []int{0}}
	}
	return Values{Name: "Lin", Code: "WASM", Topics: []int{1}}
}
func Validate(v Values) map[string]string {
	fields := map[string]string{}
	name := strings.TrimSpace(v.Name)
	if !utf8.ValidString(name) || len(name) < 2 || len(name) > 40 {
		fields["name"] = "Use a public name between 2 and 40 bytes."
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			fields["name"] = "Use a public name without control characters."
		}
	}
	code := strings.TrimSpace(v.Code)
	if len(code) < 2 || len(code) > 16 {
		fields["code"] = "Use a public code between 2 and 16 letters."
	}
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-') {
			fields["code"] = "Use letters or hyphens for the public code."
		}
	}
	if !v.Channel.Selected && v.Channel.Value != "" || v.Channel.Selected && v.Channel.Value != "" && v.Channel.Value != "email" {
		fields["channel"] = "Choose an available channel."
	}
	if _, err := SelectedValues(v.Topics); err != nil {
		fields["topics"] = "Choose only the listed topic options once each."
	}
	return fields
}
func SelectedValues(indices []int) ([]string, error) {
	return client.SelectedOptionValues(OptionValues(), indices)
}
func Normalize(v Values) Values {
	v = Clone(v)
	v.Name = strings.TrimSpace(v.Name)
	v.Code = strings.ToUpper(strings.TrimSpace(v.Code))
	slices.Sort(v.Topics)
	return v
}
func ChannelSummary(v client.RadioChoice) string {
	if !v.Selected {
		return "No selection"
	}
	if v.Value == "" {
		return "Selected empty value"
	}
	if v.Value == "email" {
		return "Selected email"
	}
	return "Unsupported selection"
}
