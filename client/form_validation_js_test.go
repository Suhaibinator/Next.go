//go:build js && wasm

package client

import (
	"context"
	"errors"
	"strings"
	"sync"
	"syscall/js"
	"testing"
	"time"
)

type validationDraft struct{ Name string }
type validationDOMFixture struct {
	scope     *Scope
	model     *Form[validationDraft]
	node      js.Value
	listeners map[string][]js.Value
	functions []js.Func
	composing bool
}

func validationDocument(t *testing.T) {
	t.Helper()
	previous := doc
	doc = js.Global().Get("Object").New()
	t.Cleanup(func() { doc = previous })
}
func validationDOM(t *testing.T, value string) *validationDOMFixture {
	t.Helper()
	fixture := &validationDOMFixture{node: js.Global().Get("Object").New(), listeners: map[string][]js.Value{}}
	node := fixture.node
	node.Set("nodeType", 1)
	node.Set("name", "name")
	node.Set("value", value)
	method := func(name string, fn func(js.Value, []js.Value) any) {
		f := js.FuncOf(fn)
		fixture.functions = append(fixture.functions, f)
		node.Set(name, f)
	}
	method("contains", func(_ js.Value, args []js.Value) any { return args[0].Equal(node) })
	method("closest", func(js.Value, []js.Value) any { return node })
	method("addEventListener", func(_ js.Value, args []js.Value) any {
		fixture.listeners[args[0].String()] = append(fixture.listeners[args[0].String()], args[1])
		return nil
	})
	method("removeEventListener", func(_ js.Value, args []js.Value) any {
		event := args[0].String()
		items := fixture.listeners[event]
		for i, item := range items {
			if item.Equal(args[1]) {
				fixture.listeners[event] = append(items[:i], items[i+1:]...)
				break
			}
		}
		return nil
	})
	fixture.scope = &Scope{Root: Element{node}}
	model, err := NewScopedForm(fixture.scope, validationDraft{Name: value}, FormOptions[validationDraft]{OnObserverError: func(err error) { t.Error(err) }})
	if err != nil {
		t.Fatal(err)
	}
	fixture.model = model
	model.dom().fields["name"] = &formDOMField[validationDraft]{controls: []js.Value{node}, capture: func() (formFieldPatch[validationDraft], error) {
		if fixture.composing {
			return formFieldPatch[validationDraft]{}, ErrFormBusy
		}
		text := node.Get("value").String()
		return formFieldPatch[validationDraft]{matches: func(v validationDraft) bool { return v.Name == text }, apply: func(v *validationDraft) { v.Name = text }}, nil
	}, close: func() {}}
	t.Cleanup(func() {
		fixture.scope.dispose()
		for _, f := range fixture.functions {
			f.Release()
		}
	})
	return fixture
}
func (f *validationDOMFixture) fire(event string) {
	for _, callback := range append([]js.Value(nil), f.listeners[event]...) {
		callback.Invoke(js.Global().Get("Object").New())
	}
}
func validationWait(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("validation did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}
func validationBind(t *testing.T, f *validationDOMFixture, fn func(context.Context, validationDraft) (string, error), opts FieldValidationOptions) *FieldValidator[validationDraft] {
	t.Helper()
	v, err := BindFieldValidation(f.scope, f.model, "name", fn, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close(); validationWait(t, func() bool { return v.running == nil }) })
	return v
}

func TestFieldValidationDebouncePendingAndOwnedErrors(t *testing.T) {
	validationDocument(t)
	baseline := fieldValidationCallbacks
	fixture := validationDOM(t, "initial")
	calls := 0
	inside := false
	v := validationBind(t, fixture, func(_ context.Context, input validationDraft) (string, error) {
		if inside {
			t.Error("worker ran in synchronous input event")
		}
		calls++
		if input.Name != "latest" {
			t.Error(input)
		}
		return "Name is unavailable", nil
	}, FieldValidationOptions{OnInput: true, Debounce: 5 * time.Millisecond, OnError: func(err error) { t.Error(err) }})
	if calls != 0 {
		t.Fatal("binding issued work")
	}
	for _, value := range []string{"one", "two", "latest"} {
		fixture.node.Set("value", value)
		inside = true
		fixture.fire("input")
		inside = false
	}
	if !validationSnapshot(t, fixture.model).Validating["name"] {
		t.Fatal("debounce was not pending")
	}
	if _, err := fixture.model.Begin(context.Background()); !errors.Is(err, ErrFormValidationPending) {
		t.Fatal("submit bypassed debounce", err)
	}
	validationWait(t, func() bool { return !validationSnapshot(t, fixture.model).Validating["name"] })
	state := validationSnapshot(t, fixture.model)
	if calls != 1 || state.Errors["name"] != "Name is unavailable" || !state.Touched["name"] || state.Unconfirmed {
		t.Fatal(calls, state)
	}
	v.Close()
	if fieldValidationCallbacks != baseline {
		t.Fatal("callables leaked", fieldValidationCallbacks, baseline)
	}
}

func TestFieldValidationCoalescesAndRetainsPhysicalWork(t *testing.T) {
	validationDocument(t)
	baseline := fieldValidationCallbacks
	fixture := validationDOM(t, "A")
	started := make(chan string, 3)
	release := make(chan struct{}, 3)
	v := validationBind(t, fixture, func(_ context.Context, input validationDraft) (string, error) {
		started <- input.Name
		<-release
		return "old result", nil
	}, FieldValidationOptions{OnInput: true, Debounce: time.Millisecond, OnError: func(err error) { t.Error(err) }})
	if err := v.Trigger(); err != nil {
		t.Fatal(err)
	}
	validationWait(t, func() bool { return len(started) == 1 })
	if <-started != "A" {
		t.Fatal("wrong input")
	}
	for _, value := range []string{"B", "C"} {
		fixture.node.Set("value", value)
		fixture.fire("input")
	}
	time.Sleep(5 * time.Millisecond)
	if len(started) != 0 || validationSnapshot(t, fixture.model).ValidationInFlight != 1 {
		t.Fatal("cancellation released physical admission")
	}
	release <- struct{}{}
	validationWait(t, func() bool { return len(started) == 1 })
	if <-started != "C" {
		t.Fatal("latest input not coalesced")
	}
	if validationSnapshot(t, fixture.model).Errors["name"] != "" {
		t.Fatal("stale error published")
	}
	v.Close()
	if state := validationSnapshot(t, fixture.model); state.Validating["name"] || state.ValidationInFlight != 1 {
		t.Fatal("dispose released active worker", state)
	}
	release <- struct{}{}
	validationWait(t, func() bool {
		return fixture.model.validationFlights == nil || len(fixture.model.validationFlights) == 0
	})
	if fieldValidationCallbacks != baseline {
		t.Fatal("settlement callable leaked", fieldValidationCallbacks, baseline)
	}
}

func checkFieldValidationDocumentCapacity(t *testing.T) {
	validationDocument(t)
	baseline := fieldValidationCallbacks
	release := make(chan struct{})
	started := make(chan struct{}, defaultFormValidationLimit+1)
	for i := 0; i < defaultFormValidationLimit; i++ {
		f := validationDOM(t, "held")
		v := validationBind(t, f, func(context.Context, validationDraft) (string, error) {
			started <- struct{}{}
			<-release
			return "", nil
		}, FieldValidationOptions{Debounce: time.Millisecond})
		if err := v.Trigger(); err != nil {
			t.Fatal(err)
		}
		validationWait(t, func() bool { return len(started) == i+1 })
		v.Close()
		f.model.Close()
		f.scope.dispose()
	}
	ledger, err := documentValidationLedger()
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Get("active").Int() != defaultFormValidationLimit || ledger.Get("bindings").Int() != 0 {
		t.Fatal("unmount reset document capacity")
	}
	fresh := validationDOM(t, "new")
	var failure error
	v := validationBind(t, fresh, func(context.Context, validationDraft) (string, error) { started <- struct{}{}; return "", nil }, FieldValidationOptions{Debounce: time.Millisecond, OnError: func(err error) { failure = err }})
	if err := v.Trigger(); err != nil {
		t.Fatal(err)
	}
	validationWait(t, func() bool { return failure != nil })
	if !errors.Is(failure, ErrFormValidationCapacity) || len(started) != defaultFormValidationLimit {
		t.Fatal("global admission exceeded", failure, len(started))
	}
	close(release)
	validationWait(t, func() bool { return ledger.Get("active").Int() == 0 })
	if err := v.Trigger(); err != nil {
		t.Fatal(err)
	}
	validationWait(t, func() bool { return len(started) == defaultFormValidationLimit+1 })
	validationWait(t, func() bool { return ledger.Get("active").Int() == 0 })
	v.Close()
	if fieldValidationCallbacks != baseline {
		t.Fatal("closed jobs retained callbacks", fieldValidationCallbacks, baseline)
	}
}
func TestFieldValidationDocumentCapacity(t *testing.T) { checkFieldValidationDocumentCapacity(t) }
func TestModuleTargetFieldValidationDocumentCapacity(t *testing.T) {
	checkFieldValidationDocumentCapacity(t)
}

func TestFieldValidationContextDeadlineBeforeJSTimer(t *testing.T) {
	validationDocument(t)
	f := validationDOM(t, "deadline")
	errorsSeen := 0
	v := validationBind(t, f, func(ctx context.Context, _ validationDraft) (string, error) { <-ctx.Done(); return "", ctx.Err() }, FieldValidationOptions{Debounce: time.Millisecond, Timeout: 20 * time.Millisecond, OnError: func(err error) {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Error(err)
		}
		errorsSeen++
	}})
	set := v.setTimer
	fake := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[1].Int() == 20 {
			return 987654
		}
		return set.Invoke(args[0], args[1])
	})
	defer fake.Release()
	v.setTimer = fake.Value
	if err := v.Trigger(); err != nil {
		t.Fatal(err)
	}
	validationWait(t, func() bool { return errorsSeen == 1 })
	validationWait(t, func() bool { return validationSnapshot(t, f.model).ValidationInFlight == 0 })
	if state := validationSnapshot(t, f.model); state.Validating["name"] || state.Unconfirmed {
		t.Fatal(state)
	}
	time.Sleep(5 * time.Millisecond)
	if errorsSeen != 1 {
		t.Fatal("deadline emitted twice", errorsSeen)
	}
}

func TestFieldValidationDeadlineObserveKeepsPhysicalWork(t *testing.T) {
	validationDocument(t)
	f := validationDOM(t, "deadline")
	release := make(chan struct{})
	started := make(chan context.Context, 1)
	errorsSeen := 0
	v := validationBind(t, f, func(ctx context.Context, _ validationDraft) (string, error) {
		started <- ctx
		<-release
		return "late message", nil
	}, FieldValidationOptions{Debounce: time.Millisecond, Timeout: 20 * time.Millisecond, OnError: func(err error) {
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Error(err)
		}
		errorsSeen++
	}})
	set := v.setTimer
	var expiry js.Value
	fake := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[1].Int() == 20 {
			expiry = args[0]
			return 987654
		}
		return set.Invoke(args[0], args[1])
	})
	defer fake.Release()
	v.setTimer = fake.Value
	if err := v.Trigger(); err != nil {
		t.Fatal(err)
	}
	validationWait(t, func() bool { return len(started) == 1 })
	ctx := <-started
	validationWait(t, func() bool { return ctx.Err() != nil })
	if err := f.model.Touch("sibling"); err != nil {
		t.Fatal(err)
	}
	if v.running == nil || v.running.attempt.deadline == nil || v.running.attempt.deadline.done {
		t.Fatal("Observe canceled current deadline notifier")
	}
	expiry.Invoke()
	if errorsSeen != 1 || validationSnapshot(t, f.model).ValidationInFlight != 1 || v.binding.ledger.Get("active").Int() != 1 {
		t.Fatal("timeout released physical work", errorsSeen)
	}
	close(release)
	validationWait(t, func() bool { return validationSnapshot(t, f.model).ValidationInFlight == 0 })
	if errorsSeen != 1 || validationSnapshot(t, f.model).Errors["name"] != "" {
		t.Fatal("late timeout result published", errorsSeen)
	}
}

func TestFieldValidationTimedOutCallbacksKeepDocumentCapacity(t *testing.T) {
	validationDocument(t)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseWorkers := func() { releaseOnce.Do(func() { close(release) }) }
	// Run before controller t.Cleanup callbacks, including on assertion failure.
	defer releaseWorkers()
	started := make(chan struct{}, defaultFormValidationLimit)
	deadlines := 0
	var admissions, expiries []js.Value
	// The real Go deadline must not race the setup/admission barrier. This test
	// advances the helper's JS clock explicitly after all eight callbacks start;
	// separate tests exercise real Go deadlines winning the JS timer race.
	controlledTimeout := time.Minute
	set := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[1].Int() == int(controlledTimeout/time.Millisecond) {
			expiries = append(expiries, args[0])
		} else {
			admissions = append(admissions, args[0])
		}
		return 987000 + len(admissions) + len(expiries)
	})
	t.Cleanup(set.Release)
	var fixtures []*validationDOMFixture
	for i := 0; i < defaultFormValidationLimit; i++ {
		f := validationDOM(t, "held")
		fixtures = append(fixtures, f)
		v := validationBind(t, f, func(context.Context, validationDraft) (string, error) {
			started <- struct{}{}
			<-release
			return "", nil
		}, FieldValidationOptions{Debounce: time.Millisecond, Timeout: controlledTimeout, OnError: func(err error) {
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Error(err)
			}
			deadlines++
		}})
		v.setTimer = set.Value
		if err := v.Trigger(); err != nil {
			t.Fatal(err)
		}
	}
	if len(admissions) != defaultFormValidationLimit || len(expiries) != defaultFormValidationLimit {
		t.Fatal("missing controlled timers", len(admissions), len(expiries))
	}
	for _, admit := range admissions {
		admit.Invoke()
	}
	validationWait(t, func() bool { return len(started) == defaultFormValidationLimit })
	for _, expire := range expiries {
		expire.Invoke()
	}
	if deadlines != defaultFormValidationLimit {
		t.Fatal("deadline notifications missing", deadlines)
	}
	for _, fixture := range fixtures {
		state := validationSnapshot(t, fixture.model)
		if state.Validating["name"] || state.ValidationInFlight != 1 {
			t.Fatal("deadline failed to retire authority while holding physical work", state)
		}
	}
	ledger, err := documentValidationLedger()
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Get("active").Int() != defaultFormValidationLimit {
		t.Fatal("deadline freed document permits")
	}
	f := validationDOM(t, "ninth")
	var capacity error
	v := validationBind(t, f, func(context.Context, validationDraft) (string, error) {
		t.Error("ninth callback admitted early")
		return "", nil
	}, FieldValidationOptions{Debounce: time.Millisecond, OnError: func(err error) { capacity = err }})
	if err := v.Trigger(); err != nil {
		t.Fatal(err)
	}
	validationWait(t, func() bool { return capacity != nil })
	if !errors.Is(capacity, ErrFormValidationCapacity) {
		t.Fatal(capacity)
	}
	releaseWorkers()
	validationWait(t, func() bool { return ledger.Get("active").Int() == 0 })
}

func TestFieldValidationCompositionResumesAfterEarlierCapture(t *testing.T) {
	validationDocument(t)
	f := validationDOM(t, "old")
	f.composing = true
	calls := make(chan string, 1)
	// Existing field listeners are registered earlier and commit the composed
	// value before the validation controller's compositionend listener runs.
	earlier := js.FuncOf(func(js.Value, []js.Value) any { f.composing = false; _ = f.model.Capture(); return nil })
	defer earlier.Release()
	f.node.Call("addEventListener", "compositionend", earlier)
	v := validationBind(t, f, func(_ context.Context, input validationDraft) (string, error) { calls <- input.Name; return "", nil }, FieldValidationOptions{Debounce: time.Millisecond, OnError: func(err error) { t.Error(err) }})
	if err := v.Trigger(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if len(calls) != 0 {
		t.Fatal("validated incomplete composition")
	}
	f.node.Set("value", "composed")
	f.fire("compositionend")
	validationWait(t, func() bool { return len(calls) == 1 })
	if <-calls != "composed" {
		t.Fatal("composition result lost")
	}
	validationWait(t, func() bool { return v.running == nil })
}

func TestFieldValidationSetupFaultReleasesCallbacks(t *testing.T) {
	validationDocument(t)
	f := validationDOM(t, "fault")
	baseline := fieldValidationCallbacks
	f.node.Set("addEventListener", js.Global().Get("Function").New("throw new Error('fixture listener add failed')"))
	if _, err := BindFieldValidation(f.scope, f.model, "name", func(context.Context, validationDraft) (string, error) { return "", nil }, FieldValidationOptions{OnInput: true}); err == nil {
		t.Fatal("listener fault accepted")
	}
	ledger, err := documentValidationLedger()
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Get("bindings").Int() != 0 || fieldValidationCallbacks != baseline {
		t.Fatal("setup leaked ownership", fieldValidationCallbacks, baseline)
	}
	set := js.Global().Get("Function").New("callback", "delay", "callback(); throw new Error('fixture timer failed')")
	if _, err := newValidationTimer(set, js.Global().Get("clearTimeout"), time.Millisecond, func() {}); err == nil {
		t.Fatal("timer fault accepted")
	}
	if fieldValidationCallbacks != baseline {
		t.Fatal("synchronous timer fault double released", fieldValidationCallbacks, baseline)
	}
}

func TestFieldValidationLedgerRejectsCorruptCounters(t *testing.T) {
	validationDocument(t)
	ledger, err := documentValidationLedger()
	if err != nil {
		t.Fatal(err)
	}
	ledger.Set("active", 0.5)
	if _, err := acquireValidationLease(ledger, false); err == nil {
		t.Fatal("fractional admission accepted")
	}
}

func TestFieldValidationPanicAndPostFailureReleaseOwnership(t *testing.T) {
	validationDocument(t)
	baseline := fieldValidationCallbacks
	f := validationDOM(t, "panic")
	failures := 0
	v := validationBind(t, f, func(context.Context, validationDraft) (string, error) { panic("private callback detail") }, FieldValidationOptions{Debounce: time.Millisecond, OnError: func(err error) {
		if strings.Contains(err.Error(), "private callback detail") {
			t.Error("panic detail leaked")
		}
		if validationSnapshot(t, f.model).Validating["name"] {
			t.Error("failure reported before pending cleared")
		}
		failures++
	}})
	v.post = js.Global().Get("Function").New("throw new Error('fixture scheduler unavailable')")
	if err := v.Trigger(); err != nil {
		t.Fatal(err)
	}
	validationWait(t, func() bool { return failures == 1 })
	validationWait(t, func() bool { return validationSnapshot(t, f.model).ValidationInFlight == 0 })
	ledger := v.binding.ledger
	if ledger.Get("active").Int() != 0 {
		t.Fatal("posting fault leaked physical permit")
	}
	v.Close()
	if fieldValidationCallbacks != baseline {
		t.Fatal("posting fault leaked callable", fieldValidationCallbacks, baseline)
	}
}

func validationSnapshot(t *testing.T, form *Form[validationDraft]) FormSnapshot[validationDraft] {
	t.Helper()
	snapshot, err := form.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
