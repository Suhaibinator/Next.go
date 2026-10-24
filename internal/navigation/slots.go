package navigation

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

const SlotsHeader = "X-Nextgo-Slots"
const MaxSlots = 16
const MaxSlotDepth = 8
const MaxSlotRegistrations = 8192
const MaxSlotStateBytes = 16 << 10

// SlotState contains public logical URLs, never trusted authorization decisions.
// Servers reselect and reauthorize every retained branch with current headers.
type SlotState struct {
	Configuration string            `json:"configuration"`
	Selections    map[string]string `json:"selections"`
}

func (s SlotState) Validate() error {
	if !ValidConfigurationID(s.Configuration) || len(s.Selections) > MaxSlots {
		return fmt.Errorf("invalid slot state")
	}
	for key, value := range s.Selections {
		if !ValidConfigurationID(key) || len(value) > 4096 {
			return fmt.Errorf("invalid slot selection")
		}
		u, err := url.Parse(value)
		if err != nil || u.Fragment != "" || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("invalid slot URL")
		}
		if _, err := (paths.Config{}).URL(value); err != nil {
			return fmt.Errorf("invalid slot URL")
		}
	}
	return nil
}
func ParseSlotState(raw string) (SlotState, error) {
	var state SlotState
	if len(raw) > MaxSlotStateBytes || json.Unmarshal([]byte(raw), &state) != nil {
		return state, fmt.Errorf("invalid slot state encoding")
	}
	return state, state.Validate()
}
