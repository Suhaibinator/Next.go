package client

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

type formProfile struct {
	Name    string
	Enabled bool
}

func testForm(t *testing.T) *Form[formProfile] {
	t.Helper()
	f, err := NewForm(formProfile{Name: "Initial"}, FormOptions[formProfile]{Validate: func(p formProfile) (FieldErrors, error) {
		if p.Name == "" {
			return FieldErrors{"name": "Required"}, nil
		}
		return nil, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func editName(t *testing.T, f *Form[formProfile], name string) {
	t.Helper()
	if err := f.Edit("name", func(p *formProfile) { p.Name = name }); err != nil {
		t.Fatal(err)
	}
}
func snapForm(t *testing.T, f *Form[formProfile]) FormSnapshot[formProfile] {
	t.Helper()
	s, err := f.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestFormValidationTouchResetAndCanonicalBaseline(t *testing.T) {
	f := testForm(t)
	editName(t, f, "")
	f.Touch("name")
	if _, err := f.Begin(context.Background()); !errors.Is(err, ErrFormInvalid) {
		t.Fatal(err)
	}
	s := snapForm(t, f)
	if !s.Dirty || !s.Touched["name"] || s.Errors["name"] != "Required" || s.Pending || !s.Submitted {
		t.Fatal(s)
	}
	editName(t, f, " new ")
	ticket, err := f.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if input, err := ticket.Input(); err != nil || input.Name != " new " {
		t.Fatal(input, err)
	}
	if _, err := f.Begin(context.Background()); !errors.Is(err, ErrFormPending) {
		t.Fatal(err)
	}
	if err := ticket.Accept(formProfile{Name: "New"}, "Saved"); err != nil {
		t.Fatal(err)
	}
	s = snapForm(t, f)
	if s.Dirty || s.Values.Name != "New" || s.Baseline.Name != "New" || s.Phase != FormSucceeded || s.Pending {
		t.Fatal(s)
	}
	editName(t, f, "Other")
	if err := f.Reset(); err != nil {
		t.Fatal(err)
	}
	s = snapForm(t, f)
	if s.Dirty || s.Submitted || s.ResetVersion != 1 || s.Values.Name != "New" || len(s.Touched) != 0 {
		t.Fatal(s)
	}
	if !errors.Is(ticket.Accept(formProfile{}, ""), ErrFormSettled) {
		t.Fatal("settled ticket accepted")
	}
}
func TestFormPendingEditsSurviveAndOldFieldErrorsDoNotOverwriteThem(t *testing.T) {
	f := testForm(t)
	editName(t, f, "submitted")
	ticket, _ := f.Begin(context.Background())
	editName(t, f, "newer")
	if err := ticket.Accept(formProfile{Name: "Canonical submitted"}, "Saved"); err != nil {
		t.Fatal(err)
	}
	s := snapForm(t, f)
	if s.Values.Name != "newer" || s.Baseline.Name != "Canonical submitted" || !s.Dirty {
		t.Fatal(s)
	}
	ticket, _ = f.Begin(context.Background())
	editName(t, f, "even newer")
	ticket.Reject("Older input rejected", FieldErrors{"name": "Old error"})
	s = snapForm(t, f)
	if s.Values.Name != "even newer" || len(s.Errors) != 0 || s.Phase != FormFailed {
		t.Fatal(s)
	}
	ticket, _ = f.Begin(context.Background())
	ticket.Reject("Invalid", FieldErrors{"name": "Rejected"})
	if snapForm(t, f).Errors["name"] != "Rejected" {
		t.Fatal("matching field error lost")
	}
	editName(t, f, "fixed")
	if len(snapForm(t, f).Errors) != 0 {
		t.Fatal("edited field error remained")
	}
}
func TestFormCancelResetCloseAndDeadlinesRejectLateResults(t *testing.T) {
	for _, operation := range []string{"cancel", "reset", "close", "deadline"} {
		t.Run(operation, func(t *testing.T) {
			f := testForm(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ticket, _ := f.Begin(ctx)
			switch operation {
			case "cancel":
				f.Cancel()
			case "reset":
				f.Reset()
			case "close":
				f.Close()
			case "deadline":
				cancel()
			}
			if ticket.Context().Err() == nil {
				t.Fatal("request stayed live")
			}
			if err := ticket.Accept(formProfile{Name: "late"}, ""); err == nil {
				t.Fatal("late result committed")
			}
			if operation != "close" {
				s := snapForm(t, f)
				if s.Pending || s.Values.Name != "Initial" {
					t.Fatal(s)
				}
			}
		})
	}
}
func TestFormSnapshotsCloneReferenceValuesAndIsolateObservers(t *testing.T) {
	clone := func(v map[string]string) map[string]string {
		out := map[string]string{}
		for k, x := range v {
			out[k] = x
		}
		return out
	}
	input := map[string]string{"name": "original"}
	f, _ := NewForm(input, FormOptions[map[string]string]{Clone: clone, OnObserverError: func(error) {}})
	input["name"] = "outside"
	stops := []func(){}
	for i := 0; i < 2; i++ {
		stop, err := f.Observe(func(s FormSnapshot[map[string]string]) {
			s.Values["name"] = "observer"
			s.Baseline["name"] = "observer"
			s.Touched["name"] = true
		})
		if err != nil {
			t.Fatal(err)
		}
		stops = append(stops, stop)
	}
	s, _ := f.Snapshot()
	if s.Values["name"] != "original" || s.Dirty || len(s.Touched) != 0 {
		t.Fatal(s)
	}
	if err := f.Edit("name", func(v *map[string]string) { (*v)["name"] = "changed" }); err != nil {
		t.Fatal(err)
	}
	s, _ = f.Snapshot()
	if s.Baseline["name"] != "original" || !s.Dirty {
		t.Fatal(s)
	}
	for _, stop := range stops {
		stop()
		stop()
	}
	if len(f.observers) != 0 {
		t.Fatal("unregistration leaked")
	}
}
func TestFormCallbackFailuresAndReentrantPublication(t *testing.T) {
	f := testForm(t)
	if err := f.Edit("name", func(*formProfile) { panic("patch") }); err == nil {
		t.Fatal("panic accepted")
	}
	if snapForm(t, f).Values.Name != "Initial" {
		t.Fatal("partial edit")
	}
	reports := 0
	f.options.OnObserverError = func(error) { reports++ }
	if _, err := f.Observe(func(FormSnapshot[formProfile]) { panic("observer") }); err == nil || reports != 1 || len(f.observers) != 0 {
		t.Fatal(err, reports)
	}
	updates := 0
	stop, err := f.Observe(func(s FormSnapshot[formProfile]) {
		updates++
		if s.Values.Name == "first" {
			editName(t, f, "second")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	editName(t, f, "first")
	if snapForm(t, f).Values.Name != "second" || updates != 3 {
		t.Fatal(updates)
	}
	stop()
	f.options.Validate = func(formProfile) (FieldErrors, error) {
		if err := f.Edit("name", func(*formProfile) {}); !errors.Is(err, ErrFormBusy) {
			t.Fatal(err)
		}
		panic("validator")
	}
	if _, err := f.Begin(context.Background()); err == nil || snapForm(t, f).Pending {
		t.Fatal(err)
	}
}
func TestFormValidationBoundsAndOwnerDisposal(t *testing.T) {
	for _, fields := range []FieldErrors{{"": "bad"}, {"name": strings.Repeat("x", 2049)}} {
		f := testForm(t)
		f.options.Validate = func(formProfile) (FieldErrors, error) { return fields, nil }
		if _, err := f.Begin(context.Background()); err == nil {
			t.Fatal("bad fields accepted")
		}
	}
	owner := NewLifetime()
	f := testForm(t)
	f.owner = owner
	owner.Defer(f.Close)
	ticket, _ := f.Begin(context.Background())
	owner.Close()
	if ticket.Context().Err() == nil {
		t.Fatal("owner did not cancel")
	}
	if _, err := f.Snapshot(); !errors.Is(err, ErrFormClosed) {
		t.Fatal(err)
	}
	if f.observers != nil || f.options.Clone != nil {
		t.Fatal("closed form retained callbacks")
	}
}

func TestFormEditManyPublishesOnceAndOwnsPatchedReferences(t *testing.T) {
	f := testForm(t)
	calls := 0
	f.Observe(func(FormSnapshot[formProfile]) { calls++ })
	if err := f.EditMany([]string{"name", "enabled"}, func(p *formProfile) { p.Name = "Together"; p.Enabled = true }); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !snapForm(t, f).Values.Enabled {
		t.Fatal(calls)
	}
	if f.EditMany([]string{"name", "name"}, func(*formProfile) {}) == nil {
		t.Fatal("duplicate fields accepted")
	}
	clone := func(p map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range p {
			out[k] = v
		}
		return out
	}
	m, _ := NewForm(map[string]string{"name": "old"}, FormOptions[map[string]string]{Clone: clone})
	outside := map[string]string{"name": "new"}
	m.Edit("name", func(p *map[string]string) { *p = outside })
	outside["name"] = "unowned"
	state, _ := m.Snapshot()
	if state.Values["name"] != "new" {
		t.Fatal("patch reference escaped", state)
	}
}
func TestFormMergePolicyAcceptsCanonicalFieldsWithoutLosingNewerEdits(t *testing.T) {
	type profile struct {
		Name    string
		Version int
	}
	f, _ := NewForm(profile{Name: "first"}, FormOptions[profile]{Merge: func(current, submitted, confirmed profile) profile {
		if current.Name != submitted.Name {
			confirmed.Name = current.Name
		}
		return confirmed
	}})
	ticket, _ := f.Begin(context.Background())
	f.Edit("name", func(p *profile) { p.Name = "newer" })
	if err := ticket.Accept(profile{Name: "canonical", Version: 1}, "Saved"); err != nil {
		t.Fatal(err)
	}
	state, _ := f.Snapshot()
	if state.Values.Name != "newer" || state.Values.Version != 1 || !state.Dirty || state.Baseline.Name != "canonical" {
		t.Fatal(state)
	}
}

func TestFormFailedMergeAndInvalidServerDataPreserveThePendingSnapshot(t *testing.T) {
	f := testForm(t)
	f.options.Merge = func(a, b, c formProfile) formProfile { panic("merge") }
	ticket, _ := f.Begin(context.Background())
	editName(t, f, "newer")
	if err := ticket.Accept(formProfile{Name: "server"}, "Saved"); err == nil {
		t.Fatal("merge panic accepted")
	}
	state := snapForm(t, f)
	if state.Values.Name != "newer" || state.Baseline.Name != "Initial" || !state.Pending {
		t.Fatal(state)
	}
	if err := ticket.Reject(strings.Repeat("x", 4097), nil); err == nil {
		t.Fatal("unbounded message")
	}
	if err := ticket.Reject("Bad", FieldErrors{"name": strings.Repeat("x", 2049)}); err == nil {
		t.Fatal("unbounded field error")
	}
	if err := ticket.Reject("Could not confirm", nil); err != nil {
		t.Fatal(err)
	}
}

func TestFormUnconfirmedOutcomesSurviveResetUntilAConfirmedResult(t *testing.T) {
	f := testForm(t)
	ticket, _ := f.Begin(context.Background())
	ticket.Fail("Response unavailable")
	if !snapForm(t, f).Unconfirmed {
		t.Fatal("ambiguous failure looked confirmed")
	}
	f.Reset()
	if !snapForm(t, f).Unconfirmed {
		t.Fatal("reset pretended to undo server write")
	}
	ticket, _ = f.Begin(context.Background())
	ticket.Reject("Known rejection", FieldErrors{"name": "Rejected"})
	if !snapForm(t, f).Unconfirmed {
		t.Fatal("rejection resolved an earlier unknown outcome")
	}
	ticket, _ = f.Begin(context.Background())
	ticket.Accept(formProfile{Name: "Confirmed"}, "Saved")
	if snapForm(t, f).Unconfirmed {
		t.Fatal("confirmation did not reconcile")
	}
	ticket, _ = f.Begin(context.Background())
	f.Reset()
	if ticket.Context().Err() == nil || !snapForm(t, f).Unconfirmed {
		t.Fatal("pending reset did not mark unknown outcome")
	}
}

func TestFormExplicitValidationDoesNotSubmitOrTouchAndOwnsItsResult(t *testing.T) {
	f := testForm(t)
	editName(t, f, "")
	calls := 0
	f.Observe(func(FormSnapshot[formProfile]) { calls++ })
	fields, err := f.Validate()
	if err != nil || fields["name"] != "Required" {
		t.Fatal(fields, err)
	}
	fields["name"] = "outside"
	state := snapForm(t, f)
	if state.Pending || state.Submitted || state.Touched["name"] || state.Errors["name"] != "Required" || state.Phase != FormIdle {
		t.Fatal(state)
	}
	previous := state.Version
	if _, err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if snapForm(t, f).Version != previous || calls != 2 {
		t.Fatal("unchanged validation republished", calls)
	}
	f.Touch("name")
	editName(t, f, "fixed")
	fields, err = f.Validate()
	if err != nil || len(fields) != 0 {
		t.Fatal(fields, err)
	}
}
func TestFormValidationPreservesServerErrorsUntilTheirFieldChanges(t *testing.T) {
	f := testForm(t)
	ticket, _ := f.Begin(context.Background())
	ticket.Reject("Reserved", FieldErrors{"name": "Server-only rule"})
	fields, err := f.Validate()
	if err != nil || fields["name"] != "Server-only rule" {
		t.Fatal(fields, err)
	}
	f.Edit("enabled", func(p *formProfile) { p.Enabled = true })
	fields, err = f.Validate()
	if err != nil || fields["name"] != "Server-only rule" {
		t.Fatal("unrelated validation erased server error", fields, err)
	}
	editName(t, f, "changed")
	fields, err = f.Validate()
	if err != nil || len(fields) != 0 || len(f.serverErrors) != 0 {
		t.Fatal(fields, err)
	}
}
func TestFormPendingDraftValidationSurvivesLateRejectionAndConfirmation(t *testing.T) {
	for _, outcome := range []string{"reject", "fail", "accept"} {
		t.Run(outcome, func(t *testing.T) {
			f := testForm(t)
			ticket, _ := f.Begin(context.Background())
			editName(t, f, "")
			if _, err := f.Validate(); err != nil {
				t.Fatal(err)
			}
			switch outcome {
			case "reject":
				ticket.Reject("Old result", FieldErrors{"name": "Stale server error"})
			case "fail":
				ticket.Fail("Unavailable")
			case "accept":
				if err := ticket.Accept(formProfile{Name: "Confirmed"}, "Saved"); err != nil {
					t.Fatal(err)
				}
			}
			state := snapForm(t, f)
			if state.Errors["name"] != "Required" || state.Values.Name != "" || state.Pending {
				t.Fatal(state)
			}
		})
	}
}
func TestFormValidationFailureAndOwnerCloseAreAtomic(t *testing.T) {
	f := testForm(t)
	f.errors = FieldErrors{"name": "Previous"}
	version := f.version
	f.options.Validate = func(formProfile) (FieldErrors, error) { panic("validator") }
	if _, err := f.Validate(); err == nil {
		t.Fatal("panic accepted")
	}
	if f.version != version || f.errors["name"] != "Previous" {
		t.Fatal("validation partially committed")
	}
	f.options.Validate = func(formProfile) (FieldErrors, error) { return FieldErrors{"name": strings.Repeat("x", 2049)}, nil }
	if _, err := f.Validate(); err == nil {
		t.Fatal("unbounded validation accepted")
	}
	f.options.Validate = func(formProfile) (FieldErrors, error) {
		if _, err := f.Validate(); !errors.Is(err, ErrFormBusy) {
			t.Fatal(err)
		}
		f.Close()
		return nil, nil
	}
	if _, err := f.Validate(); !errors.Is(err, ErrFormClosed) {
		t.Fatal(err)
	}
	if f.serverErrors != nil {
		t.Fatal("closed model retained server errors")
	}
}

func TestFormValidationBoundsApplyToCombinedClientAndServerErrors(t *testing.T) {
	f := testForm(t)
	ticket, _ := f.Begin(context.Background())
	server := FieldErrors{}
	for i := 0; i < 128; i++ {
		server["server"+strconv.Itoa(i)] = "Reserved"
	}
	if err := ticket.Reject("Rejected", server); err != nil {
		t.Fatal(err)
	}
	f.options.Validate = func(formProfile) (FieldErrors, error) { return FieldErrors{"name": "Required"}, nil }
	version := f.version
	if _, err := f.Validate(); err == nil {
		t.Fatal("combined error limit bypassed")
	}
	state := snapForm(t, f)
	if state.Version != version || len(state.Errors) != 128 || state.Errors["server0"] != "Reserved" {
		t.Fatal(state)
	}
}
