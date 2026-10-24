package navigation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSlotStateBoundsAndLocalURLs(t *testing.T) {
	state := SlotState{Configuration: strings.Repeat("a", 64), Selections: map[string]string{strings.Repeat("b", 64): "/boards/chart?q=a%26b"}}
	data, _ := json.Marshal(state)
	if _, err := ParseSlotState(string(data)); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"https://foreign.test/boards", "//foreign.test/boards", "/../escape", "/boards%2Fchart", "/boards#fragment", strings.Repeat("x", 4097)} {
		state.Selections[strings.Repeat("b", 64)] = value
		if state.Validate() == nil {
			t.Fatal("unsafe URL accepted", value)
		}
	}
	if _, err := ParseSlotState(strings.Repeat(" ", MaxSlotStateBytes+1)); err == nil {
		t.Fatal("oversized state accepted")
	}
}
