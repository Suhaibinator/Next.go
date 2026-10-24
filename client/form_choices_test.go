package client

import (
	"strings"
	"testing"
)

func TestChoiceDomainsAndSelectionsAreBoundedAndUnambiguous(t *testing.T) {
	for _, values := range [][]string{nil, {"x", "x"}, {""}, {strings.Repeat("x", 257)}, {string([]byte{0xff})}, make([]string, 257)} {
		if _, err := formChoiceDomain(values, false); err == nil {
			t.Fatal("invalid domain", values)
		}
	}
	domain, err := formChoiceDomain([]string{"", "a", "b"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, selection := range [][]string{{"unknown"}, {"a", "a"}, make([]string, 257)} {
		if _, err := formChoiceSet(selection, domain); err == nil {
			t.Fatal("invalid selection", selection)
		}
	}
	selected, err := formChoiceSet([]string{"", "b"}, domain)
	if err != nil || !selected[""] || !selected["b"] {
		t.Fatal(selected, err)
	}
	selected["outside"] = true
	if domain["outside"] {
		t.Fatal("selection aliased domain")
	}
	if !sameFormChoices([]string{"a", "b"}, []string{"b", "a"}) || !sameFormChoices(nil, []string{}) || sameFormChoices([]string{"a", "a"}, []string{"a", "b"}) || sameFormChoices([]string{"a", "b"}, []string{"a", "a"}) {
		t.Fatal("membership comparison")
	}
}
