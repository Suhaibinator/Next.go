package client

import (
	"reflect"
	"strings"
	"testing"
)

func TestMergeStationaryAnchorsKeepUnmovedIndependentSiblings(t *testing.T) {
	cases := []struct {
		name       string
		old, fresh []string
		want       map[string]bool
	}{
		{"server gap insertion", []string{"left", "right"}, []string{"left", "right"}, map[string]bool{"left": true, "right": true}},
		{"removed incompatible left", []string{"left", "right"}, []string{"right"}, map[string]bool{"right": true}},
		{"fresh prefix", []string{"right"}, []string{"left", "right"}, map[string]bool{"right": true}},
		{"changed carrier grouping", []string{"left+middle", "right"}, []string{"left", "middle", "right"}, map[string]bool{"right": true}},
		{"changed root type represented by owning boundary", []string{"old-schema", "right"}, []string{"new-schema", "right"}, map[string]bool{"right": true}},
		{"empty", nil, nil, map[string]bool{}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := mergeStableSubsequence(test.old, test.fresh)
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("stationary frontier %v,%v: %v %v", test.old, test.fresh, got, err)
			}
		})
	}
}
func TestMergeStationaryReorderNeverPretendsBothRootsStayedPut(t *testing.T) {
	got, err := mergeStableSubsequence([]string{"a", "b", "outside"}, []string{"b", "a", "outside"})
	if err != nil || !got["outside"] || len(got) != 2 || got["a"] == got["b"] {
		t.Fatalf("unsafe move admission: %v %v", got, err)
	}
}
func TestMergeStationaryAnchorAdmissionIsBoundedAndUnambiguous(t *testing.T) {
	for _, bad := range [][]string{{""}, {"same", "same"}, {strings.Repeat("x", 8193)}, make([]string, 65)} {
		if _, err := mergeStableSubsequence(bad, nil); err == nil {
			t.Fatal("accepted ambiguous or overbound anchors", len(bad))
		}
	}
}
