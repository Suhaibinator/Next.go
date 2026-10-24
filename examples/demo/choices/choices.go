package choices

import "slices"

// Preferences are public preview data. This example does not persist them.
type Preferences struct {
	Channel string   `json:"channel"`
	Topics  []string `json:"topics"`
}

func Default() Preferences            { return Preferences{Channel: "email", Topics: []string{"ssr"}} }
func Clone(p Preferences) Preferences { p.Topics = slices.Clone(p.Topics); return p }
func Validate(p Preferences) map[string]string {
	fields := map[string]string{}
	if p.Channel != "email" && p.Channel != "sms" {
		fields["channel"] = "Choose email or SMS."
	}
	if len(p.Topics) == 0 || len(p.Topics) > 3 {
		fields["topics"] = "Choose at least one topic."
	}
	seen := map[string]bool{}
	for _, v := range p.Topics {
		if (v != "routing" && v != "ssr" && v != "wasm") || seen[v] {
			fields["topics"] = "Choose distinct listed topics."
		}
		seen[v] = true
	}
	return fields
}
func Merge(current, submitted, confirmed Preferences) Preferences {
	if current.Channel != submitted.Channel {
		confirmed.Channel = current.Channel
	}
	if !slices.Equal(current.Topics, submitted.Topics) {
		confirmed.Topics = slices.Clone(current.Topics)
	}
	return confirmed
}
