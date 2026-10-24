//go:build js && wasm

package client

import (
	"context"
	"errors"
	"testing"
)

func TestScopedFormClosesPendingWorkAndUnregistersOwnedModels(t *testing.T) {
	scope := &Scope{}
	owner, err := scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		model, err := NewScopedForm(scope, formProfile{Name: "initial"})
		if err != nil {
			t.Fatal(err)
		}
		ticket, err := model.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		model.Close()
		if ticket.Context().Err() == nil || len(owner.resources) != 0 {
			t.Fatal("model ownership remained", len(owner.resources))
		}
	}
	if len(scope.cleanup) != 1 {
		t.Fatal("cleanup callbacks accumulated", len(scope.cleanup))
	}
	model, err := NewScopedForm(scope, formProfile{Name: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	ticket, _ := model.Begin(context.Background())
	scope.dispose()
	if ticket.Context().Err() == nil {
		t.Fatal("unmount did not cancel")
	}
	if _, err := model.Snapshot(); !errors.Is(err, ErrFormClosed) {
		t.Fatal(err)
	}
}

func TestFormCaptureStagesAllControlsAndPreservesUntouchedServerErrors(t *testing.T) {
	f := testForm(t)
	ticket, _ := f.Begin(context.Background())
	ticket.Reject("Rejected", FieldErrors{"enabled": "Server-only error"})
	f.browser = &formDOM[formProfile]{fields: map[string]*formDOMField[formProfile]{
		"name": {capture: func() (formFieldPatch[formProfile], error) {
			return formFieldPatch[formProfile]{apply: func(p *formProfile) { p.Name = "DOM input" }, matches: func(p formProfile) bool { return p.Name == "DOM input" }}, nil
		}},
		"enabled": {capture: func() (formFieldPatch[formProfile], error) {
			return formFieldPatch[formProfile]{apply: func(p *formProfile) { p.Enabled = false }, matches: func(p formProfile) bool { return !p.Enabled }}, nil
		}},
	}}
	if err := f.Capture(); err != nil {
		t.Fatal(err)
	}
	state := snapForm(t, f)
	if state.Values.Name != "DOM input" || state.Errors["enabled"] != "Server-only error" {
		t.Fatal(state)
	}
	before := state.Version
	if err := f.Capture(); err != nil || snapForm(t, f).Version != before {
		t.Fatal("unchanged capture republished", err)
	}
}
func TestFormCaptureIsAtomicAndHandlesCompositionAndCallbackFailure(t *testing.T) {
	f := testForm(t)
	registry := &formDOM[formProfile]{fields: map[string]*formDOMField[formProfile]{
		"enabled": {capture: func() (formFieldPatch[formProfile], error) {
			return formFieldPatch[formProfile]{apply: func(p *formProfile) { p.Enabled = true }, matches: func(p formProfile) bool { return p.Enabled }}, nil
		}},
		"name": {capture: func() (formFieldPatch[formProfile], error) { return formFieldPatch[formProfile]{}, ErrFormBusy }},
	}}
	f.browser = registry
	if err := f.Capture(); !errors.Is(err, ErrFormBusy) || snapForm(t, f).Values.Enabled {
		t.Fatal("partial capture during composition", err)
	}
	if err := f.captureDOMFields(false); err != nil || !snapForm(t, f).Values.Enabled {
		t.Fatal("ready siblings blocked by composition", err)
	}
	f.Reset()
	registry.fields["name"].capture = func() (formFieldPatch[formProfile], error) { panic("DOM read") }
	if err := f.Capture(); err == nil || snapForm(t, f).Values.Enabled {
		t.Fatal("partial capture after read panic", err)
	}
	registry.fields["name"].capture = func() (formFieldPatch[formProfile], error) {
		return formFieldPatch[formProfile]{matches: func(formProfile) bool {
			if err := f.Edit("name", func(*formProfile) {}); !errors.Is(err, ErrFormBusy) {
				t.Fatal(err)
			}
			panic("getter")
		}}, nil
	}
	if err := f.Capture(); err == nil || snapForm(t, f).Values.Enabled {
		t.Fatal("partial capture after getter panic", err)
	}
	registry.fields["name"].capture = func() (formFieldPatch[formProfile], error) {
		return formFieldPatch[formProfile]{matches: func(formProfile) bool { return false }, apply: func(*formProfile) { panic("setter") }}, nil
	}
	if err := f.Capture(); err == nil || snapForm(t, f).Values.Enabled {
		t.Fatal("partial capture after setter panic", err)
	}
	f.Close()
	if err := f.Capture(); !errors.Is(err, ErrFormClosed) {
		t.Fatal(err)
	}
}
