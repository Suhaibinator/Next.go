package client

import (
	"fmt"
	"unicode/utf8"
)

// RadioChoice distinguishes an unselected group from a selected empty value.
// The zero value means no selection; when Selected is false Value must be empty.
// Selected choices must match a fixed, unique native radio option value.
type RadioChoice struct {
	Value    string
	Selected bool
}

func validRadioChoice(choice RadioChoice, domain map[string]bool) bool {
	if !choice.Selected {
		return choice.Value == ""
	}
	return domain[choice.Value]
}

// formOptionSelection identifies selected option nodes by their fixed DOM index,
// so repeated values do not collapse distinct native selected options.
func formOptionSelection(indices []int, count int) (map[int]bool, error) {
	if count < 1 || count > 256 || len(indices) > count {
		return nil, fmt.Errorf("nextgo: invalid selected option count")
	}
	selected := make(map[int]bool, len(indices))
	for _, index := range indices {
		if index < 0 || index >= count || selected[index] {
			return nil, fmt.Errorf("nextgo: unknown or repeated option index")
		}
		selected[index] = true
	}
	return selected, nil
}

func sameFormOptions(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int]bool, len(a))
	for _, index := range a {
		if seen[index] {
			return false
		}
		seen[index] = true
	}
	for _, index := range b {
		if !seen[index] {
			return false
		}
		delete(seen, index)
	}
	return len(seen) == 0
}

// SelectedOptionValues returns native value multiplicity in option DOM order.
// Indices are unique option positions, rather than unique values; nil selects
// none. Values may repeat or be empty. This fixed-shape helper does not emulate
// disabled-control/optgroup successful-control filtering or dynamic options.
func SelectedOptionValues(values []string, indices []int) ([]string, error) {
	if err := formOptionValues(values); err != nil {
		return nil, err
	}
	selected, err := formOptionSelection(indices, len(values))
	if err != nil {
		return nil, err
	}
	result := []string{}
	for i, value := range values {
		if selected[i] {
			result = append(result, value)
		}
	}
	return result, nil
}

func formOptionValues(values []string) error {
	if len(values) < 1 || len(values) > 256 {
		return fmt.Errorf("nextgo: form choices require 1 to 256 options")
	}
	for _, value := range values {
		if len(value) > 256 || !utf8.ValidString(value) {
			return fmt.Errorf("nextgo: invalid form option value")
		}
	}
	return nil
}

// Choice lists are sets. DOM order determines the order of newly adopted values;
// an existing model's order is retained when its membership already matches.
func formChoiceSet(values []string, domain map[string]bool) (map[string]bool, error) {
	if len(values) > 256 {
		return nil, fmt.Errorf("nextgo: too many selected choices")
	}
	selected := make(map[string]bool, len(values))
	for _, value := range values {
		if !domain[value] || selected[value] {
			return nil, fmt.Errorf("nextgo: unknown or repeated form choice")
		}
		selected[value] = true
	}
	return selected, nil
}
func sameFormChoices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, v := range a {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
		delete(seen, v)
	}
	return len(seen) == 0
}
func formChoiceDomain(values []string, allowEmpty bool) (map[string]bool, error) {
	if len(values) == 0 || len(values) > 256 {
		return nil, fmt.Errorf("nextgo: form choices require 1 to 256 options")
	}
	domain := make(map[string]bool, len(values))
	for _, value := range values {
		if (!allowEmpty && value == "") || len(value) > 256 || !utf8.ValidString(value) || domain[value] {
			return nil, fmt.Errorf("nextgo: invalid or repeated form option value")
		}
		domain[value] = true
	}
	return domain, nil
}
