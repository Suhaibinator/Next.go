// Package keyed validates stable collection keys and plans identity-preserving
// updates. It contains no browser or application state.
package keyed

import (
	"fmt"
	"sort"
	"unicode/utf8"
)

const MaxItems = 1000
const MaxKeyBytes = 256

func ValidateKey(key string) error {
	if key == "" || len(key) > MaxKeyBytes || !utf8.ValidString(key) {
		return fmt.Errorf("keyed: require a nonempty UTF-8 key of at most 256 bytes")
	}
	for _, r := range key {
		if r < 32 || r == 127 {
			return fmt.Errorf("keyed: key contains a control character")
		}
	}
	return nil
}
func Validate(keys []string, limit int) error {
	if limit < 1 || limit > MaxItems || len(keys) > limit {
		return fmt.Errorf("keyed: item limit exceeded")
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if err := ValidateKey(key); err != nil {
			return err
		}
		if seen[key] {
			return fmt.Errorf("keyed: duplicate key")
		}
		seen[key] = true
	}
	return nil
}

type Plan struct {
	Removed []string
	Added   []string
	// Stable is a longest increasing subsequence of retained old positions.
	// Traverse desired keys right-to-left; only insert/move keys outside Stable.
	Stable map[string]bool
}

func Reconcile(old, next []string, limit int) (Plan, error) {
	result := Plan{Stable: map[string]bool{}}
	if err := Validate(old, limit); err != nil {
		return result, err
	}
	if err := Validate(next, limit); err != nil {
		return result, err
	}
	positions := map[string]int{}
	for i, key := range old {
		positions[key] = i
	}
	wanted := map[string]bool{}
	sequence := []int{}
	keys := []string{}
	for _, key := range next {
		wanted[key] = true
		if position, ok := positions[key]; ok {
			sequence = append(sequence, position)
			keys = append(keys, key)
		} else {
			result.Added = append(result.Added, key)
		}
	}
	for _, key := range old {
		if !wanted[key] {
			result.Removed = append(result.Removed, key)
		}
	}
	tails, previous := []int{}, make([]int, len(sequence))
	for i, value := range sequence {
		slot := sort.Search(len(tails), func(j int) bool { return sequence[tails[j]] >= value })
		previous[i] = -1
		if slot > 0 {
			previous[i] = tails[slot-1]
		}
		if slot == len(tails) {
			tails = append(tails, i)
		} else {
			tails[slot] = i
		}
	}
	if len(tails) > 0 {
		for i := tails[len(tails)-1]; i >= 0; i = previous[i] {
			result.Stable[keys[i]] = true
		}
	}
	return result, nil
}
