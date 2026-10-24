package keyed

import (
	"reflect"
	"strings"
	"testing"
)

func TestPlanAllSixRowPermutationsRetainIdentityWithMinimalMoves(t *testing.T) {
	original := []string{"a", "b", "c", "d", "e", "f"}
	cases := 0
	var visit func([]string, int)
	visit = func(want []string, offset int) {
		if offset < len(want) {
			for i := offset; i < len(want); i++ {
				want[offset], want[i] = want[i], want[offset]
				visit(want, offset+1)
				want[offset], want[i] = want[i], want[offset]
			}
			return
		}
		cases++
		plan, err := Reconcile(original, want, 10)
		if err != nil {
			t.Fatal(err)
		}
		actual := append([]string(nil), original...)
		moves := 0
		for i := len(want) - 1; i >= 0; i-- {
			key := want[i]
			if plan.Stable[key] {
				continue
			}
			moves++
			index := 0
			for actual[index] != key {
				index++
			}
			actual = append(actual[:index], actual[index+1:]...)
			where := len(actual)
			if i+1 < len(want) {
				for j, v := range actual {
					if v == want[i+1] {
						where = j
						break
					}
				}
			}
			actual = append(actual, "")
			copy(actual[where+1:], actual[where:])
			actual[where] = key
		}
		if !reflect.DeepEqual(actual, want) {
			t.Fatal(want, actual, plan)
		}
		// Brute-force every subsequence as an independent lower bound on moves.
		best := 0
		for mask := 0; mask < 1<<len(want); mask++ {
			last := ""
			length := 0
			valid := true
			for i, key := range want {
				if mask&(1<<i) == 0 {
					continue
				}
				if last != "" && key < last {
					valid = false
					break
				}
				last = key
				length++
			}
			if valid && length > best {
				best = length
			}
		}
		if moves != len(want)-best {
			t.Fatal("nonminimal move plan", want, moves, best)
		}
	}
	visit(append([]string(nil), original...), 0)
	if cases != 720 {
		t.Fatal(cases)
	}
}
func TestPlanInsertRemoveAndValidation(t *testing.T) {
	p, err := Reconcile([]string{"old", "keep", "last"}, []string{"last", "new", "keep"}, 4)
	if err != nil || !reflect.DeepEqual(p.Removed, []string{"old"}) || !reflect.DeepEqual(p.Added, []string{"new"}) || len(p.Stable) != 1 {
		t.Fatal(p, err)
	}
	for _, keys := range [][]string{{"a", "a"}, {""}, {"bad\x00"}, {string([]byte{255})}, {strings.Repeat("a", 257)}} {
		if _, err := Reconcile(nil, keys, 1000); err == nil {
			t.Fatal("invalid keys accepted")
		}
	}
	if err := ValidateKey(`café / "<literal>`); err != nil {
		t.Fatal(err)
	}
}
