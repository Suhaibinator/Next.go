package client

import (
	"reflect"
	"strings"
	"testing"
)

func TestRadioChoicePreservesSelectedEmptyAndRejectsAmbiguousAbsence(t *testing.T) {
	domain, err := formChoiceDomain([]string{"", "yes"}, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range []RadioChoice{{}, {Selected: true}, {Value: "yes", Selected: true}} {
		if !validRadioChoice(choice, domain) {
			t.Fatal("valid choice rejected", choice)
		}
	}
	for _, choice := range []RadioChoice{{Value: "yes"}, {Value: "unknown", Selected: true}} {
		if validRadioChoice(choice, domain) {
			t.Fatal("ambiguous or unknown choice accepted", choice)
		}
	}
	if (RadioChoice{}) == (RadioChoice{Selected: true}) {
		t.Fatal("selected empty collapsed into absence")
	}
	if _, err := formChoiceDomain([]string{"", ""}, true); err == nil {
		t.Fatal("duplicate radio values accepted")
	}
	if _, err := formChoiceDomain([]string{"", "yes"}, false); err == nil {
		t.Fatal("legacy radio contract changed")
	}
}

func TestSelectedOptionValuesPreservesMultiplicityIdentityAndOwnership(t *testing.T) {
	values := []string{"same", "other", "same", "", ""}
	indices := []int{4, 2, 0, 3}
	got, err := SelectedOptionValues(values, indices)
	if err != nil || !reflect.DeepEqual(got, []string{"same", "same", "", ""}) {
		t.Fatal(got, err)
	}
	if !reflect.DeepEqual(indices, []int{4, 2, 0, 3}) {
		t.Fatal("caller indices reordered")
	}
	got[0] = "changed"
	if values[0] != "same" {
		t.Fatal("result aliases option values")
	}
	first, _ := SelectedOptionValues(values, []int{0})
	second, _ := SelectedOptionValues(values, []int{2})
	if !reflect.DeepEqual(first, second) || sameFormOptions([]int{0}, []int{2}) {
		t.Fatal("native values confused with local option identities")
	}
	if !sameFormOptions(nil, []int{}) || !sameFormOptions([]int{4, 2}, []int{2, 4}) || sameFormOptions([]int{0, 0}, []int{0, 1}) || sameFormOptions([]int{0, 1}, []int{0, 0}) {
		t.Fatal("option membership comparison incorrect")
	}
	if got, err := SelectedOptionValues(values, nil); err != nil || len(got) != 0 {
		t.Fatal(got, err)
	}
}

func TestOptionSelectionBoundsAndLegacyUniqueValueContract(t *testing.T) {
	for _, indices := range [][]int{{-1}, {2}, {0, 0}, {0, 1, 2}} {
		if _, err := SelectedOptionValues([]string{"a", "a"}, indices); err == nil {
			t.Fatal("invalid option indices accepted", indices)
		}
	}
	for _, values := range [][]string{nil, make([]string, 257), {strings.Repeat("x", 257)}, {string([]byte{255})}} {
		if _, err := SelectedOptionValues(values, nil); err == nil {
			t.Fatal("invalid option values accepted")
		}
	}
	if _, err := formChoiceDomain([]string{"duplicate", "duplicate"}, true); err == nil {
		t.Fatal("legacy unique-value multiple contract changed")
	}
	values := make([]string, 256)
	indices := make([]int, 256)
	for i := range indices {
		indices[i] = i
	}
	if got, err := SelectedOptionValues(values, indices); err != nil || len(got) != 256 {
		t.Fatal("bounded all-empty multiplicity rejected", len(got), err)
	}
}
