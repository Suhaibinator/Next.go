package client

import (
	"strings"
	"testing"
)

func TestTransitionNamesAreBoundedCSSIdentifiers(t *testing.T) {
	for _, types := range [][]string{nil, {"navigation", "history", "card-2"}, {strings.Repeat("a", 63)}} {
		if err := (TransitionOptions{Types: types}).validate(); err != nil {
			t.Fatal(types, err)
		}
	}
	for _, types := range [][]string{{""}, {"none"}, {"auto"}, {"9name"}, {"a b"}, {"x\n"}, {"n;{}"}, {"é"}, {strings.Repeat("a", 64)}, {"a", "b", "c", "d", "e", "f", "g", "h", "i"}} {
		if err := (TransitionOptions{Types: types}).validate(); err == nil {
			t.Fatal("accepted invalid transition types", types)
		}
	}
}
