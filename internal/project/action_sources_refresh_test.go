package project

import (
	"strings"
	"testing"
)

func TestActionSourcesCombinedViewDelegatesAndReservesSymbols(t *testing.T) {
	p, err := Discover(fixture(t, actionFixtureFiles("profile/save")))
	if err != nil {
		t.Fatal(err)
	}
	sources, err := ActionSources(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"func ProfileSaveAndRefresh(", "mutation.RefreshResponse[ProfileSaveOutput]", "a0.Contract.CallAndRefresh(ctx, input, options...)", "func ProfileSaveMultipartAndRefresh(", "a0.Contract.CallMultipartAndRefresh(ctx, form, options...)"} {
		if !strings.Contains(string(sources["nextgo_gen_js.go"]), want) {
			t.Fatal("missing generated wrapper", want)
		}
	}
	for _, symbol := range []string{"ProfileSaveAndRefresh", "ProfileSaveMultipartAndRefresh"} {
		actions := append([]Action(nil), p.Actions...)
		second := actions[0]
		second.Name = "other/action"
		second.Symbol = symbol
		actions = append(actions, second)
		if validateActions(actions) == nil {
			t.Fatal("generated wrapper collision accepted", symbol)
		}
	}
}
