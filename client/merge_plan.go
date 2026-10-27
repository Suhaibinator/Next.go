package client

import "fmt"

// mergeStableSubsequence chooses existing sibling carriers that need no move.
// Inputs are unique ownership-hole signatures, never URLs or State addresses.
func mergeStableSubsequence(a, b []string) (map[string]bool, error) {
	if len(a) > 64 || len(b) > 64 {
		return nil, fmt.Errorf("merge anchor capacity exceeded")
	}
	for _, sequence := range [][]string{a, b} {
		seen := map[string]bool{}
		for _, key := range sequence {
			if key == "" || len(key) > 8192 || seen[key] {
				return nil, fmt.Errorf("invalid merge anchor signature")
			}
			seen[key] = true
		}
	}
	table := make([][]int, len(a)+1)
	for i := range table {
		table[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				table[i][j] = 1 + table[i+1][j+1]
			} else if table[i+1][j] >= table[i][j+1] {
				table[i][j] = table[i+1][j]
			} else {
				table[i][j] = table[i][j+1]
			}
		}
	}
	keep := map[string]bool{}
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] {
			keep[a[i]] = true
			i++
			j++
		} else if table[i+1][j] >= table[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return keep, nil
}
