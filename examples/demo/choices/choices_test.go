package choices

import "testing"

func TestCloneValidationAndPendingChoiceMerge(t *testing.T) {
	initial := Default()
	copy := Clone(initial)
	copy.Topics[0] = "wasm"
	if initial.Topics[0] != "ssr" {
		t.Fatal("clone aliases choices")
	}
	for _, bad := range []Preferences{{Channel: "other", Topics: []string{"ssr"}}, {Channel: "email"}, {Channel: "email", Topics: []string{"wasm", "wasm"}}, {Channel: "email", Topics: []string{"unknown"}}} {
		if len(Validate(bad)) == 0 {
			t.Fatal("invalid choices accepted", bad)
		}
	}
	confirmed := Preferences{Channel: "sms", Topics: []string{"routing"}}
	current := Clone(initial)
	current.Topics = []string{"wasm"}
	result := Merge(current, initial, confirmed)
	if result.Channel != "sms" || result.Topics[0] != "wasm" {
		t.Fatal(result)
	}
	result.Topics[0] = "ssr"
	if current.Topics[0] != "wasm" {
		t.Fatal("merge aliases retained draft")
	}
}
