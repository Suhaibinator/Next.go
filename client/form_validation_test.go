package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func beginFieldCheck(t *testing.T, f *Form[formProfile], name string) *FormFieldValidation[formProfile] {
	t.Helper()
	ticket, err := f.BeginFieldValidation(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return ticket
}
func startFieldCheck(t *testing.T, ticket *FormFieldValidation[formProfile]) formProfile {
	t.Helper()
	input, err := ticket.Start()
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func TestFormFieldValidationKnownErrorsPendingAndIndependentFields(t *testing.T) {
	f := testForm(t)
	name := beginFieldCheck(t, f, "name")
	enabled := beginFieldCheck(t, f, "enabled")
	if snapshot := snapForm(t, f); !snapshot.Validating["name"] || !snapshot.Validating["enabled"] || snapshot.ValidationInFlight != 0 || snapshot.Pending {
		t.Fatal(snapshot)
	}
	if _, err := f.Begin(context.Background()); !errors.Is(err, ErrFormValidationPending) {
		t.Fatal("submit bypassed waiting checks", err)
	}
	if got := startFieldCheck(t, name); got.Name != "Initial" {
		t.Fatal(got)
	}
	startFieldCheck(t, enabled)
	if applied, err := name.Complete("Name reserved"); err != nil || !applied {
		t.Fatal(applied, err)
	}
	if applied, err := enabled.Complete("Choose an option"); err != nil || !applied {
		t.Fatal(applied, err)
	}
	snapshot := snapForm(t, f)
	if snapshot.Errors["name"] != "Name reserved" || snapshot.Errors["enabled"] != "Choose an option" || len(snapshot.Validating) != 0 || snapshot.ValidationInFlight != 0 || snapshot.Unconfirmed {
		t.Fatal(snapshot)
	}
	if _, err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Begin(context.Background()); !errors.Is(err, ErrFormInvalid) {
		t.Fatal("submission discarded known async rejection", err)
	}
	replacement := beginFieldCheck(t, f, "name")
	startFieldCheck(t, replacement)
	if applied, err := replacement.Complete(""); err != nil || !applied {
		t.Fatal(applied, err)
	}
	if snapshot := snapForm(t, f); snapshot.Errors["name"] != "" || snapshot.Errors["enabled"] != "Choose an option" {
		t.Fatal("one result cleared another field", snapshot)
	}
}
func TestFormFieldValidationWholeDraftEditAndSameValueSupersession(t *testing.T) {
	f := testForm(t)
	old := beginFieldCheck(t, f, "name")
	startFieldCheck(t, old)
	newer := beginFieldCheck(t, f, "name")
	startFieldCheck(t, newer)
	if old.Context().Err() == nil {
		t.Fatal("same-value retrigger did not cancel old ticket")
	}
	if applied, err := old.Complete("Old result"); applied || err == nil {
		t.Fatal("superseded result applied", applied, err)
	}
	if applied, err := newer.Complete("Current result"); !applied || err != nil {
		t.Fatal(applied, err)
	}
	unrelated := beginFieldCheck(t, f, "enabled")
	startFieldCheck(t, unrelated)
	if err := f.Edit("name", func(value *formProfile) { value.Name = "Changed" }); err != nil {
		t.Fatal(err)
	}
	if snapshot := snapForm(t, f); len(snapshot.Errors) != 0 || len(snapshot.Validating) != 0 {
		t.Fatal("full-T results survived another draft value", snapshot)
	}
	if applied, err := unrelated.Complete("Old full-T result"); applied || err == nil {
		t.Fatal(applied, err)
	}
	if snapForm(t, f).ValidationInFlight != 0 {
		t.Fatal("stale result leaked physical permit")
	}
	// A->B->A must not restore authority to a ticket for the first A.
	aba := beginFieldCheck(t, f, "name")
	startFieldCheck(t, aba)
	editName(t, f, "Other")
	editName(t, f, "Changed")
	if applied, err := aba.Complete(""); applied || err == nil {
		t.Fatal("A->B->A restored old check", applied, err)
	}
}
func TestFormFieldValidationPhysicalCapacityCancellationResetAndClose(t *testing.T) {
	f, err := NewForm(formProfile{}, FormOptions[formProfile]{ValidationLimit: 1})
	if err != nil {
		t.Fatal(err)
	}
	old := beginFieldCheck(t, f, "name")
	startFieldCheck(t, old)
	old.Cancel()
	waiting := beginFieldCheck(t, f, "name")
	if _, err := waiting.Start(); !errors.Is(err, ErrFormValidationCapacity) {
		t.Fatal("cancel freed a running worker", err)
	}
	if snapshot := snapForm(t, f); !snapshot.Validating["name"] || snapshot.ValidationInFlight != 1 {
		t.Fatal(snapshot)
	}
	if err := old.Fail(); err != nil {
		t.Fatal(err)
	}
	startFieldCheck(t, waiting)
	if err := f.Reset(); err != nil {
		t.Fatal(err)
	}
	if snapshot := snapForm(t, f); len(snapshot.Validating) != 0 || snapshot.ValidationInFlight != 1 || snapshot.Unconfirmed {
		t.Fatal(snapshot)
	}
	if applied, err := waiting.Complete("Late"); applied || err == nil {
		t.Fatal(applied, err)
	}
	current := beginFieldCheck(t, f, "name")
	startFieldCheck(t, current)
	f.Close()
	if current.Context().Err() == nil || len(f.validationFlights) != 1 {
		t.Fatal("dispose released physical work early")
	}
	if applied, err := current.Complete(strings.Repeat("x", 2049)); applied || err == nil {
		t.Fatal(applied, err)
	}
	if len(f.validationFlights) != 0 || current.form != nil {
		t.Fatal("late disposed completion retained ownership")
	}
	if err := current.Fail(); !errors.Is(err, ErrFormValidationSettled) {
		t.Fatal(err)
	}
}
func TestFormFieldValidationSyncAndServerErrorsWinAndFenceOldRuns(t *testing.T) {
	f := testForm(t)
	// Submission precedes this check; authoritative rejection must retire it
	// even when neither the field nor draft revision changed.
	submit, err := f.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	old := beginFieldCheck(t, f, "name")
	startFieldCheck(t, old)
	if err := submit.Reject("Rejected", FieldErrors{"name": "Server owns this error"}); err != nil {
		t.Fatal(err)
	}
	if applied, err := old.Complete(""); applied || err == nil {
		t.Fatal("late valid result cleared server error", applied, err)
	}
	current := beginFieldCheck(t, f, "name")
	startFieldCheck(t, current)
	if applied, err := current.Complete(""); !applied || err != nil {
		t.Fatal(applied, err)
	}
	if snapForm(t, f).Errors["name"] != "Server owns this error" {
		t.Fatal("new field check cleared authoritative error")
	}
	syncOld := beginFieldCheck(t, f, "name")
	startFieldCheck(t, syncOld)
	if _, err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	if applied, err := syncOld.Complete("Async overwrite"); applied || err == nil {
		t.Fatal(applied, err)
	}
	if snapForm(t, f).Errors["name"] != "Server owns this error" {
		t.Fatal("sync fence lost server precedence")
	}
	// Explicit synchronous errors similarly have precedence over async outcomes.
	editName(t, f, "")
	f.Validate()
	async := beginFieldCheck(t, f, "name")
	startFieldCheck(t, async)
	async.Complete("Async error")
	if snapForm(t, f).Errors["name"] != "Required" {
		t.Fatal("async error overwrote synchronous validation")
	}
}
func TestFormFieldValidationMalformedOutputFailureAndCanceledContextRelease(t *testing.T) {
	f := testForm(t)
	for _, message := range []string{strings.Repeat("x", 2049), string([]byte{0xff})} {
		ticket := beginFieldCheck(t, f, "name")
		startFieldCheck(t, ticket)
		if applied, err := ticket.Complete(message); applied || err == nil {
			t.Fatal("malformed output accepted", applied, err)
		}
		if snapshot := snapForm(t, f); len(snapshot.Validating) != 0 || snapshot.ValidationInFlight != 0 || len(snapshot.Errors) != 0 || snapshot.Unconfirmed {
			t.Fatal(snapshot)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	ticket, err := f.BeginFieldValidation(ctx, "name")
	if err != nil {
		t.Fatal(err)
	}
	startFieldCheck(t, ticket)
	cancel()
	if applied, err := ticket.Complete("Late"); applied || !errors.Is(err, context.Canceled) {
		t.Fatal(applied, err)
	}
	if snapForm(t, f).ValidationInFlight != 0 {
		t.Fatal("context cancellation settlement leaked capacity")
	}
	waiting := beginFieldCheck(t, f, "name")
	if applied, err := waiting.Complete("No worker"); applied || !errors.Is(err, ErrFormValidationNotStarted) {
		t.Fatal(applied, err)
	}
	if len(snapForm(t, f).Validating) != 0 {
		t.Fatal("invalid pre-start completion left form blocked")
	}
}
func TestFormFieldValidationStartCloneAndObserverReentrancy(t *testing.T) {
	for _, operation := range []string{"close", "cancel", "clone-error"} {
		t.Run(operation, func(t *testing.T) {
			f := testForm(t)
			ticket := beginFieldCheck(t, f, "name")
			f.options.Clone = func(value formProfile) formProfile {
				switch operation {
				case "close":
					f.Close()
				case "cancel":
					ticket.Cancel()
				case "reset":
					f.options.Clone = func(v formProfile) formProfile { return v }
					f.Reset()
				case "clone-error":
					panic("private clone detail")
				}
				return value
			}
			if _, err := ticket.Start(); err == nil {
				t.Fatal("reentrant invalidated clone started worker")
			}
			if len(f.validationFlights) != 0 || len(f.validationPending) != 0 {
				t.Fatal("failed clone retained admission")
			}
		})
	}
	f := testForm(t)
	ticket := beginFieldCheck(t, f, "name")
	resetOnce := false
	stop, err := f.Observe(func(snapshot FormSnapshot[formProfile]) {
		if snapshot.ValidationInFlight > 0 && !resetOnce {
			resetOnce = true
			f.Reset()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := ticket.Start(); err == nil {
		t.Fatal("observer reset did not roll back pre-worker admission")
	}
	if snapForm(t, f).ValidationInFlight != 0 {
		t.Fatal("provisional start leaked permit")
	}
}
func TestFormFieldValidationSnapshotOwnershipAndAdmissionBounds(t *testing.T) {
	for _, limit := range []int{-1, maxFormValidationLimit + 1} {
		if _, err := NewForm(0, FormOptions[int]{ValidationLimit: limit}); err == nil {
			t.Fatal("invalid limit accepted", limit)
		}
	}
	f := testForm(t)
	ticket := beginFieldCheck(t, f, "name")
	snapshot := snapForm(t, f)
	snapshot.Validating["name"] = false
	delete(snapshot.Validating, "name")
	if !snapForm(t, f).Validating["name"] {
		t.Fatal("snapshot lent validation map")
	}
	ticket.Cancel()
	for i := 0; i < maxFormValidationFields-1; i++ {
		name := fmt.Sprintf("field-%d", i)
		ticket, err := f.BeginFieldValidation(context.Background(), name)
		if err != nil {
			t.Fatal(i, err)
		}
		ticket.Cancel()
	}
	if _, err := f.BeginFieldValidation(context.Background(), "overflow"); !errors.Is(err, ErrFormValidationCapacity) {
		t.Fatal("field metadata bound bypassed", err)
	}
	f.Reset()
	if _, err := f.BeginFieldValidation(context.Background(), "after-reset"); err != nil {
		t.Fatal(err)
	}
}
func TestFormFieldValidationCancelOnlyAndTransportFailurePreserveMutationSemantics(t *testing.T) {
	f := testForm(t)
	ticket := beginFieldCheck(t, f, "name")
	startFieldCheck(t, ticket)
	if !f.Cancel() {
		t.Fatal("form cancel missed field validation")
	}
	if snapshot := snapForm(t, f); snapshot.Unconfirmed || snapshot.Phase != FormIdle || len(snapshot.Validating) != 0 || snapshot.ValidationInFlight != 1 {
		t.Fatal(snapshot)
	}
	ticket.Fail()
	active, err := f.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	active.Fail("Unknown mutation")
	ticket = beginFieldCheck(t, f, "name")
	startFieldCheck(t, ticket)
	ticket.Fail()
	if snapshot := snapForm(t, f); !snapshot.Unconfirmed || snapshot.Phase != FormFailed || snapshot.Message != "Unknown mutation" {
		t.Fatal("validation rewrote mutation outcome", snapshot)
	}
}

func TestFormFieldValidationRepeatedStartNeverSettlesExistingWorker(t *testing.T) {
	for _, operation := range []string{"cancel", "reset", "close", "supersede"} {
		t.Run(operation, func(t *testing.T) {
			f, err := NewForm(formProfile{}, FormOptions[formProfile]{ValidationLimit: 1})
			if err != nil {
				t.Fatal(err)
			}
			ticket := beginFieldCheck(t, f, "name")
			startFieldCheck(t, ticket)
			switch operation {
			case "cancel":
				ticket.Cancel()
			case "reset":
				f.Reset()
			case "close":
				f.Close()
			case "supersede":
				beginFieldCheck(t, f, "name")
			}
			if _, err := ticket.Start(); !errors.Is(err, ErrFormValidationStarted) {
				t.Fatal(err)
			}
			if len(f.validationFlights) != 1 {
				t.Fatal("repeated canceled Start released original worker", operation)
			}
			if err := ticket.Fail(); err != nil {
				t.Fatal(err)
			}
			if len(f.validationFlights) != 0 {
				t.Fatal("physical settlement did not release permit")
			}
		})
	}
}

func TestFormFieldValidationMaskedAsyncBucketStillHasIndependentBudget(t *testing.T) {
	f := testForm(t)
	sync := FieldErrors{}
	for i := 0; i < 32; i++ {
		sync[fmt.Sprintf("masked-%d", i)] = "Required"
	}
	f.options.Validate = func(formProfile) (FieldErrors, error) { return sync, nil }
	if _, err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	accepted, rejected := 0, 0
	for i := 0; i < 32; i++ {
		ticket := beginFieldCheck(t, f, fmt.Sprintf("masked-%d", i))
		startFieldCheck(t, ticket)
		applied, err := ticket.Complete(strings.Repeat("x", 2048))
		if err == nil && applied {
			accepted++
		} else {
			rejected++
		}
		if snapshot := snapForm(t, f); snapshot.ValidationInFlight != 0 || len(snapshot.Validating) != 0 {
			t.Fatal("rejected output leaked admission", snapshot)
		}
	}
	if accepted == 0 || rejected == 0 {
		t.Fatal("test did not exercise masked error admission", accepted, rejected)
	}
	if _, err := checkedFieldErrors(f.validationErrors); err != nil {
		t.Fatal("retained hidden errors exceeded budget", err)
	}
	f.options.Validate = nil
	if _, err := f.Validate(); err != nil {
		t.Fatal("unmasking previously accepted async errors failed", err)
	}
}
func TestFormFieldValidationCanceledDuringCloneNeverReturnsWorkerInput(t *testing.T) {
	f := testForm(t)
	ctx, cancel := context.WithCancel(context.Background())
	ticket, err := f.BeginFieldValidation(ctx, "name")
	if err != nil {
		t.Fatal(err)
	}
	f.options.Clone = func(value formProfile) formProfile { cancel(); return value }
	input, err := ticket.Start()
	if !errors.Is(err, context.Canceled) || input != (formProfile{}) || len(f.validationFlights) != 0 || len(f.validationPending) != 0 {
		t.Fatal(input, err, len(f.validationFlights), len(f.validationPending))
	}
}
func TestFormFieldValidationWorkerInputClonesReferenceDTO(t *testing.T) {
	clone := func(value map[string]string) map[string]string {
		out := map[string]string{}
		for key, item := range value {
			out[key] = item
		}
		return out
	}
	f, err := NewForm(map[string]string{"name": "model"}, FormOptions[map[string]string]{Clone: clone})
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := f.BeginFieldValidation(context.Background(), "name")
	if err != nil {
		t.Fatal(err)
	}
	input, err := ticket.Start()
	if err != nil {
		t.Fatal(err)
	}
	input["name"] = "worker mutation"
	if snapshot, err := f.Snapshot(); err != nil || snapshot.Values["name"] != "model" {
		t.Fatal(snapshot, err)
	}
	ticket.Fail()
}

func TestFormFieldValidationObserverRepeatedCancelIsConvergent(t *testing.T) {
	f := testForm(t)
	ticket := beginFieldCheck(t, f, "name")
	calls := 0
	stop, err := f.Observe(func(snapshot FormSnapshot[formProfile]) {
		calls++
		if snapshot.ValidationInFlight > 0 {
			_ = ticket.Cancel()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err := ticket.Start(); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	snapshot := snapForm(t, f)
	if snapshot.ValidationInFlight != 0 || len(snapshot.Validating) != 0 || calls > 5 {
		t.Fatal("idempotent cancel did not converge", snapshot, calls)
	}
}

func TestFormFieldValidationReentrantWaitingStartRetiresWithoutBlocking(t *testing.T) {
	f := testForm(t)
	ticket := beginFieldCheck(t, f, "name")
	f.options.Clone = func(value formProfile) formProfile {
		f.options.Clone = func(v formProfile) formProfile { return v }
		if _, err := ticket.Start(); !errors.Is(err, ErrFormBusy) {
			t.Errorf("nested Start = %v", err)
		}
		return value
	}
	if input, err := ticket.Start(); err == nil || input != (formProfile{}) {
		t.Fatal(input, err)
	}
	if snapshot := snapForm(t, f); len(snapshot.Validating) != 0 || snapshot.ValidationInFlight != 0 {
		t.Fatal("failed reentrant Start blocked form", snapshot)
	}
}
