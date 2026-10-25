//go:build js && wasm

package client

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestOptionalChoiceCaptureIsAtomicAndResetPreservesExactOptions(t *testing.T) {
	type choices struct {
		Radio RadioChoice
		Multi []int
	}
	model, err := NewForm(choices{Radio: RadioChoice{Selected: true}, Multi: []int{2, 0}})
	if err != nil {
		t.Fatal(err)
	}
	defer model.Close()
	liveRadio, liveMulti := RadioChoice{}, []int{1, 2}
	fail := false
	model.browser = &formDOM[choices]{fields: map[string]*formDOMField[choices]{
		"a-radio": {capture: func() (formFieldPatch[choices], error) {
			value := liveRadio
			return formFieldPatch[choices]{apply: func(input *choices) { input.Radio = value }, matches: func(input choices) bool { return input.Radio == value }}, nil
		}, close: func() {}},
		"z-multi": {capture: func() (formFieldPatch[choices], error) {
			if fail {
				return formFieldPatch[choices]{}, errors.New("option identity changed")
			}
			selected := slices.Clone(liveMulti)
			return formFieldPatch[choices]{apply: func(input *choices) { input.Multi = slices.Clone(selected) }, matches: func(input choices) bool { return sameFormOptions(input.Multi, selected) }}, nil
		}, close: func() {}},
	}}
	fail = true
	if err := model.Capture(); err == nil {
		t.Fatal("invalid shape captured")
	}
	before, _ := model.Snapshot()
	if !before.Values.Radio.Selected || !reflect.DeepEqual(before.Values.Multi, []int{2, 0}) {
		t.Fatal("partial choice capture", before.Values)
	}
	fail = false
	if err := model.Capture(); err != nil {
		t.Fatal(err)
	}
	state, _ := model.Snapshot()
	if state.Values.Radio.Selected || !reflect.DeepEqual(state.Values.Multi, []int{1, 2}) {
		t.Fatal(state.Values)
	}
	liveMulti[0] = 0
	owned, _ := model.Snapshot()
	if owned.Values.Multi[0] != 1 {
		t.Fatal("captured selection aliases live source")
	}
	if err := model.Reset(); err != nil {
		t.Fatal(err)
	}
	reset, _ := model.Snapshot()
	if reset.Values.Radio != (RadioChoice{Selected: true}) || !reflect.DeepEqual(reset.Values.Multi, []int{2, 0}) {
		t.Fatal("reset lost selected-empty or exact duplicate identity", reset.Values)
	}
	liveRadio, liveMulti = RadioChoice{Selected: true}, []int{0, 2}
	version := reset.Version
	if err := model.Capture(); err != nil {
		t.Fatal(err)
	}
	unchanged, _ := model.Snapshot()
	if unchanged.Version != version || !reflect.DeepEqual(unchanged.Values.Multi, []int{2, 0}) {
		t.Fatal("equal membership capture republished or reordered", unchanged)
	}
	model.Close()
	if err := model.Capture(); !errors.Is(err, ErrFormClosed) {
		t.Fatal("closed choice capture accepted", err)
	}
}
