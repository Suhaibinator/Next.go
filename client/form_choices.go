package client

import (
	"fmt"
	"unicode/utf8"
)

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
