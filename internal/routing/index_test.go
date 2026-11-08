package routing

import (
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"testing"
)

func validateLinearReference(patterns []string) error {
	for i, pattern := range patterns {
		if _, err := Validate(pattern); err != nil {
			return err
		}
		for _, previous := range patterns[:i] {
			if Conflicts(previous, pattern) {
				return fmt.Errorf("ambiguous routes %q and %q", previous, pattern)
			}
		}
	}
	return nil
}

func TestIndexedValidationPreservesEveryConflictAndDiagnosticOrder(t *testing.T) {
	patterns := []string{"/", "/a", "/b", "/:id", "/:other", "/a/:id", "/a/:other", "/a/*tail", "/a/*tail?", "/:x/*tail?", "/:y", "/:z/*tail", "/*all", "/*all?", "/a/b", "/a/b/*tail?", "/café", "/%literal", "bad", "/bad//path", "/x/:x/:x"}
	for _, a := range patterns {
		for _, b := range patterns {
			for _, c := range patterns {
				set := []string{a, b, c}
				actual, expected := ValidatePatterns(set), validateLinearReference(set)
				if fmt.Sprint(actual) != fmt.Sprint(expected) {
					t.Fatalf("%q: %v != %v", set, actual, expected)
				}
			}
		}
	}
}

func indexedFixture() []string {
	return []string{"/", "/docs/new", "/docs/:id", "/docs/:id/edit", "/docs/*tail", "/files/*tail?", "/:group/fixed", "/:group/:id/detail", "/café/%literal", "/branch/literal/unmatched", "/branch/:name/target"}
}

func compareIndex(t testing.TB, patterns []string, path string) {
	t.Helper()
	index, err := NewIndex(patterns)
	if err != nil {
		t.Fatal(err)
	}
	order := make([]int, len(patterns))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return Less(patterns[order[i]], patterns[order[j]]) })
	want := -1
	var params map[string]string
	for _, i := range order {
		if values, ok := Match(patterns[i], path); ok {
			want, params = i, values
			break
		}
	}
	got, values, ok := index.Match(path)
	lookup, lookupOK := index.Lookup(path)
	if ok != (want >= 0) || lookupOK != ok || ok && (got != want || lookup != got || !maps.Equal(values, params)) {
		t.Fatalf("%q: indexed %d %v %v, lookup %d %v; linear %d %v", path, got, values, ok, lookup, lookupOK, want, params)
	}
}

func TestCompiledIndexMatchesLinearSpecificityDecodingAndBacktracking(t *testing.T) {
	patterns := indexedFixture()
	for _, path := range []string{"/", "/docs/new", "/docs/one", "/docs/a%20b/edit", "/docs/one/two", "/docs/", "/docs/a%2Fb", "/docs/%252F", "/files", "/files/a/b", "/one/fixed", "/one/two/detail", "/café/%25literal", "/caf%C3%A9/%25literal", "/branch/literal/target", "/branch/literal/unmatched", "/missing", "", "//", "/docs//", "/docs/..", "/docs/%2e", "/docs/%FF", "/docs/%5c", "/docs/%00", "/docs/%", "/docs/one?q=1", "/docs/one#hash"} {
		compareIndex(t, patterns, path)
		reverse := append([]string(nil), patterns...)
		for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
			reverse[i], reverse[j] = reverse[j], reverse[i]
		}
		compareIndex(t, reverse, path)
	}
	deep := "/" + strings.Repeat("segment/", 9999) + "end"
	compareIndex(t, []string{deep, "/*tail"}, deep)
	compareIndex(t, []string{deep + "/:id", "/*tail"}, deep+"/value")
	compareIndex(t, []string{"/*tail"}, deep+"/%2f")
	if _, err := NewIndex([]string{"/x/:a", "/x/:b"}); err == nil {
		t.Fatal("ambiguous index accepted")
	}
	var zero Index
	if _, ok := zero.Lookup("/"); ok {
		t.Fatal("zero index matched")
	}
}

func TestCompiledIndexOwnsItsInputsAndConcurrentResults(t *testing.T) {
	patterns := []string{"/items/:id", "/static"}
	index, err := NewIndex(patterns)
	if err != nil {
		t.Fatal(err)
	}
	patterns[0] = "/changed"
	_, params, ok := index.Match("/items/value")
	if !ok || params["id"] != "value" {
		t.Fatal(params)
	}
	params["id"] = "changed"
	var group sync.WaitGroup
	for n := 0; n < 12; n++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := 0; i < 100; i++ {
				_, params, ok := index.Match("/items/value")
				if !ok || params["id"] != "value" {
					t.Error(params)
				}
				if at, ok := index.Lookup("/static"); !ok || at != 1 {
					t.Error(at, ok)
				}
			}
		}()
	}
	group.Wait()
}

func FuzzCompiledIndexAgainstLinear(f *testing.F) {
	for _, path := range []string{"/", "/docs/new", "/docs/a%20b/edit", "/files/a/b", "/branch/literal/target", "/café/%25literal", "/docs/%252F", "/docs/%2F"} {
		f.Add(path)
	}
	f.Fuzz(func(t *testing.T, path string) {
		if len(path) > 4096 {
			t.Skip()
		}
		compareIndex(t, indexedFixture(), path)
	})
}

func FuzzIndexedValidationAgainstLinear(f *testing.F) {
	for _, patterns := range []string{"/a\n/a/:id\n/b/*tail?", "/files\n/files/*tail?", "/:id\n/:other", "/bad//path"} {
		f.Add(patterns)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 4096 {
			t.Skip()
		}
		patterns := strings.Split(raw, "\n")
		if len(patterns) > 32 {
			t.Skip()
		}
		if got, want := fmt.Sprint(ValidatePatterns(patterns)), fmt.Sprint(validateLinearReference(patterns)); got != want {
			t.Fatal(patterns, got, want)
		}
	})
}
