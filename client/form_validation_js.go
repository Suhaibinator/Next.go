//go:build js && wasm

package client

import (
	"context"
	"errors"
	"fmt"
	"math"
	"syscall/js"
	"time"
)

// FieldValidationOptions opts into DOM triggers. Without either event flag the
// controller is manual-only. Binding never starts validation. Zero durations
// use 250 ms debounce and a 10 s cooperative deadline (maximums 5 s and 1 min).
type FieldValidationOptions struct {
	OnInput, OnBlur   bool
	Debounce, Timeout time.Duration
	// OnError handles an applicable operational failure, not a known field
	// message. It runs after logical pending state has been retired.
	OnError func(error)
}

// FieldValidator orchestrates one already-bound field on the browser event
// loop. Calls belong to that event loop. A worker receives only owned input and
// context; its callback must return for physical admission to be released.
type FieldValidator[T any] struct {
	scope                                     *Scope
	model                                     *Form[T]
	field                                     *formDOMField[T]
	name                                      string
	validate                                  func(context.Context, T) (string, error)
	options                                   FieldValidationOptions
	sequence, reset                           uint64
	waiting                                   *fieldValidationAttempt[T]
	running                                   *fieldValidationJob[T]
	stop                                      func()
	cleanup                                   *Resource
	previousClose                             func()
	listeners                                 []fieldValidationListener
	binding                                   *fieldValidationLease
	closed                                    bool
	resumeComposition                         bool
	post, promise, then, setTimer, clearTimer js.Value
}

type fieldValidationAttempt[T any] struct {
	ticket             *FormFieldValidation[T]
	ctx                context.Context
	cancel             context.CancelFunc
	sequence           uint64
	debounce, deadline *fieldValidationTimer
	ready, composing   bool
}
type fieldValidationJob[T any] struct {
	owner    *FieldValidator[T]
	attempt  *fieldValidationAttempt[T]
	permit   *fieldValidationLease
	callback js.Func
	message  string
	failure  error
	finished bool
}
type fieldValidationListener struct {
	node     js.Value
	event    string
	callback js.Func
}
type fieldValidationTimer struct {
	handle, clear js.Value
	callback      js.Func
	done          bool
	released      bool
}

// These counters expose no public state; focused WASM tests verify retained
// callable resources return to baseline after actual settlement.
var fieldValidationCallbacks int
var fieldValidationOwners map[any]map[string]any

const fieldValidationLedgerProperty = "__nextgoFieldValidationLedgerV1"

// A fixed document-shared ledger keeps eager/lazy instances and repeated
// mount/unmount from multiplying admission. It is not an authentication boundary.
type fieldValidationLease struct {
	ledger, token     js.Value
	index             int
	binding, released bool
}

func validationObject() js.Value {
	return js.Global().Get("Object").Call("create", js.Null())
}
func validationToken() js.Value { return js.Global().Get("Object").Call("freeze", validationObject()) }
func validationProperty(object js.Value, name string, value any, writable bool) {
	js.Global().Get("Object").Call("defineProperty", object, name, map[string]any{"value": value, "writable": writable, "enumerable": false, "configurable": false})
}
func validationSlots(size int) js.Value {
	array := js.Global().Get("Array").New(size)
	for i := 0; i < size; i++ {
		js.Global().Get("Object").Call("defineProperty", array, fmt.Sprint(i), map[string]any{"value": js.Null(), "writable": true, "enumerable": true, "configurable": false})
	}
	validationProperty(array, "length", size, false)
	return js.Global().Get("Object").Call("preventExtensions", array)
}
func validationDescriptor(object js.Value, name string, writable bool, enumerable ...bool) (js.Value, error) {
	d := js.Global().Get("Object").Call("getOwnPropertyDescriptor", object, name)
	wantEnumerable := len(enumerable) > 0 && enumerable[0]
	if !d.Truthy() || !d.Get("get").IsUndefined() || !d.Get("set").IsUndefined() || d.Get("writable").Type() != js.TypeBoolean || d.Get("writable").Bool() != writable || d.Get("configurable").Type() != js.TypeBoolean || d.Get("configurable").Bool() || d.Get("enumerable").Type() != js.TypeBoolean || d.Get("enumerable").Bool() != wantEnumerable {
		return js.Undefined(), fmt.Errorf("nextgo: corrupt field validation ledger")
	}
	return d.Get("value"), nil
}
func validationCount(value js.Value, max int) (int, error) {
	if value.Type() != js.TypeNumber {
		return 0, fmt.Errorf("nextgo: corrupt field validation counter")
	}
	n := value.Float()
	if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > float64(max) || math.Trunc(n) != n {
		return 0, fmt.Errorf("nextgo: corrupt field validation counter")
	}
	return int(n), nil
}
func validationSlotCount(array js.Value, size int) (int, error) {
	object := js.Global().Get("Object")
	if array.Type() != js.TypeObject || array.IsNull() || !object.Call("getPrototypeOf", array).Equal(js.Global().Get("Array").Get("prototype")) || object.Call("isExtensible", array).Bool() || js.Global().Get("Reflect").Call("ownKeys", array).Length() != size+1 {
		return 0, fmt.Errorf("nextgo: corrupt field validation slots")
	}
	length, err := validationDescriptor(array, "length", false)
	if err != nil || length.Type() != js.TypeNumber || length.Float() != float64(size) {
		return 0, fmt.Errorf("nextgo: corrupt field validation slots")
	}
	count := 0
	for i := 0; i < size; i++ {
		value, err := validationDescriptor(array, fmt.Sprint(i), true, true)
		if err != nil {
			return 0, err
		}
		if value.IsNull() {
			continue
		}
		if value.Type() != js.TypeObject || !object.Call("getPrototypeOf", value).IsNull() || object.Call("isExtensible", value).Bool() || js.Global().Get("Reflect").Call("ownKeys", value).Length() != 0 {
			return 0, fmt.Errorf("nextgo: corrupt field validation ownership token")
		}
		count++
	}
	return count, nil
}
func checkedValidationLedger(ledger js.Value) error {
	object := js.Global().Get("Object")
	if ledger.Type() != js.TypeObject || ledger.IsNull() || !object.Call("getPrototypeOf", ledger).IsNull() || object.Call("isExtensible", ledger).Bool() || js.Global().Get("Reflect").Call("ownKeys", ledger).Length() != 6 {
		return fmt.Errorf("nextgo: corrupt field validation ledger")
	}
	version, err := validationDescriptor(ledger, "version", false)
	if err != nil || version.Type() != js.TypeNumber || version.Float() != 1 {
		return fmt.Errorf("nextgo: corrupt field validation ledger version")
	}
	limit, err := validationDescriptor(ledger, "limit", false)
	if err != nil || limit.Type() != js.TypeNumber || limit.Float() != defaultFormValidationLimit {
		return fmt.Errorf("nextgo: corrupt field validation ledger limit")
	}
	for _, item := range []struct {
		count, slots string
		max          int
	}{{"active", "slots", defaultFormValidationLimit}, {"bindings", "owners", maxFormValidationFields}} {
		value, err := validationDescriptor(ledger, item.count, true)
		if err != nil {
			return err
		}
		count, err := validationCount(value, item.max)
		if err != nil {
			return err
		}
		slots, err := validationDescriptor(ledger, item.slots, false)
		if err != nil {
			return err
		}
		actual, err := validationSlotCount(slots, item.max)
		if err != nil {
			return err
		}
		if actual != count {
			return fmt.Errorf("nextgo: inconsistent field validation admission")
		}
	}
	return nil
}
func documentValidationLedger() (ledger js.Value, err error) {
	defer func() {
		if recover() != nil {
			ledger = js.Undefined()
			err = fmt.Errorf("nextgo: field validation ledger unavailable")
		}
	}()
	if doc.Type() != js.TypeObject || doc.IsNull() {
		return js.Undefined(), fmt.Errorf("nextgo: field validation requires a document")
	}
	descriptor := js.Global().Get("Object").Call("getOwnPropertyDescriptor", doc, fieldValidationLedgerProperty)
	if descriptor.IsUndefined() {
		ledger = validationObject()
		validationProperty(ledger, "version", 1, false)
		validationProperty(ledger, "limit", defaultFormValidationLimit, false)
		validationProperty(ledger, "active", 0, true)
		validationProperty(ledger, "bindings", 0, true)
		validationProperty(ledger, "slots", validationSlots(defaultFormValidationLimit), false)
		validationProperty(ledger, "owners", validationSlots(maxFormValidationFields), false)
		js.Global().Get("Object").Call("preventExtensions", ledger)
		validationProperty(doc, fieldValidationLedgerProperty, ledger, false)
	} else {
		ledger, err = validationDescriptor(doc, fieldValidationLedgerProperty, false)
		if err != nil {
			return js.Undefined(), err
		}
	}
	err = checkedValidationLedger(ledger)
	return
}
func acquireValidationLease(ledger js.Value, binding bool) (lease *fieldValidationLease, err error) {
	defer func() {
		if recover() != nil {
			lease = nil
			err = fmt.Errorf("nextgo: field validation admission failed")
		}
	}()
	if err = checkedValidationLedger(ledger); err != nil {
		return nil, err
	}
	count, slots, max := "active", "slots", defaultFormValidationLimit
	if binding {
		count, slots, max = "bindings", "owners", maxFormValidationFields
	}
	if ledger.Get(count).Int() >= max {
		return nil, ErrFormValidationCapacity
	}
	array := ledger.Get(slots)
	for i := 0; i < max; i++ {
		if array.Index(i).IsNull() {
			token := validationToken()
			array.SetIndex(i, token)
			ledger.Set(count, ledger.Get(count).Int()+1)
			return &fieldValidationLease{ledger: ledger, token: token, index: i, binding: binding}, nil
		}
	}
	return nil, ErrFormValidationCapacity
}
func (l *fieldValidationLease) release() (err error) {
	if l == nil || l.released {
		return nil
	}
	l.released = true
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("nextgo: field validation admission release failed")
		}
	}()
	if err = checkedValidationLedger(l.ledger); err != nil {
		return err
	}
	count, slots := "active", "slots"
	if l.binding {
		count, slots = "bindings", "owners"
	}
	array := l.ledger.Get(slots)
	if !array.Index(l.index).Equal(l.token) {
		return fmt.Errorf("nextgo: field validation permit owner changed")
	}
	array.SetIndex(l.index, js.Null())
	l.ledger.Set(count, l.ledger.Get(count).Int()-1)
	return nil
}

func newValidationTimer(set, clear js.Value, delay time.Duration, fn func()) (timer *fieldValidationTimer, err error) {
	timer = &fieldValidationTimer{clear: clear}
	timer.callback = js.FuncOf(func(js.Value, []js.Value) any {
		if timer.done {
			return nil
		}
		timer.done = true
		defer timer.release()
		_ = lifetimeGuard(fn)
		return nil
	})
	fieldValidationCallbacks++
	defer func() {
		if recover() != nil {
			timer.release()
			timer = nil
			err = fmt.Errorf("nextgo: field validation timer setup failed")
		}
	}()
	millis := math.Ceil(float64(delay) / float64(time.Millisecond))
	if millis < 0 {
		millis = 0
	}
	timer.handle = set.Invoke(timer.callback, millis)
	return
}
func (t *fieldValidationTimer) stop() {
	if t == nil || t.done {
		return
	}
	t.done = true
	_ = lifetimeGuard(func() { t.clear.Invoke(t.handle) })
	t.release()
}
func (t *fieldValidationTimer) release() {
	if t == nil || t.released {
		return
	}
	t.released = true
	t.callback.Release()
	fieldValidationCallbacks--
}
func (a *fieldValidationAttempt[T]) stopTimers() {
	a.debounce.stop()
	a.deadline.stop()
	a.debounce = nil
	a.deadline = nil
}

// BindFieldValidation requires BindText/BindRadioGroup/BindMultiple (or another
// existing field binding) to have registered name first. Network/work belongs
// in validate, which runs outside synchronous DOM callbacks. The document limit
// bounds validator callbacks through their actual return and posted settlement;
// it cannot account for background work independently spawned by application code.
func BindFieldValidation[T any](scope *Scope, model *Form[T], name string, validate func(context.Context, T) (string, error), options ...FieldValidationOptions) (controller *FieldValidator[T], err error) {
	defer func() {
		if failure := recover(); failure != nil {
			if controller != nil {
				controller.Close()
			}
			controller = nil
			err = fmt.Errorf("nextgo: field validation binding panic (%T)", failure)
		}
	}()
	if !scope.active() || !model.open() {
		return nil, ErrFormClosed
	}
	if validate == nil || len(options) > 1 || !validFormName(name) || model.browser == nil {
		return nil, fmt.Errorf("nextgo: field validation requires an existing field binding")
	}
	registry := model.dom()
	field := registry.fields[name]
	if field == nil || field.capture == nil || len(field.controls) == 0 {
		return nil, fmt.Errorf("nextgo: field validation requires an existing field binding")
	}
	for _, node := range field.controls {
		if !ownedFormElement(scope, Element{node}) {
			return nil, fmt.Errorf("nextgo: validation control is outside scope")
		}
	}
	if owners := fieldValidationOwners[model]; owners != nil && owners[name] != nil {
		return nil, fmt.Errorf("nextgo: duplicate field validation binding")
	}
	var opts FieldValidationOptions
	if len(options) == 1 {
		opts = options[0]
	}
	if opts.Debounce == 0 {
		opts.Debounce = 250 * time.Millisecond
	}
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.Debounce < 0 || opts.Debounce > 5*time.Second || opts.Timeout < 0 || opts.Timeout > time.Minute {
		return nil, fmt.Errorf("nextgo: invalid field validation timing")
	}
	ledger, err := documentValidationLedger()
	if err != nil {
		return nil, err
	}
	lease, err := acquireValidationLease(ledger, true)
	if err != nil {
		return nil, err
	}
	controller = &FieldValidator[T]{scope: scope, model: model, field: field, name: name, validate: validate, options: opts, binding: lease, reset: model.resetVersion, setTimer: win.Get("setTimeout"), clearTimer: win.Get("clearTimeout"), post: win.Get("queueMicrotask")}
	v := controller
	if v.setTimer.Type() != js.TypeFunction || v.clearTimer.Type() != js.TypeFunction {
		v.Close()
		return nil, fmt.Errorf("nextgo: field validation timers unavailable")
	}
	v.promise = js.Global().Get("Promise").Call("resolve")
	v.then = v.promise.Get("then")
	if v.then.Type() != js.TypeFunction {
		v.Close()
		return nil, fmt.Errorf("nextgo: field validation scheduler unavailable")
	}
	if fieldValidationOwners == nil {
		fieldValidationOwners = map[any]map[string]any{}
	}
	if fieldValidationOwners[model] == nil {
		fieldValidationOwners[model] = map[string]any{}
	}
	fieldValidationOwners[model][name] = v
	v.previousClose = field.close
	previousClose := field.close
	field.close = func() {
		v.Close()
		if previousClose != nil {
			previousClose()
		}
	}
	life, err := scope.Lifetime()
	if err != nil {
		v.Close()
		return nil, err
	}
	v.cleanup, err = life.Defer(v.Close)
	if err != nil {
		v.Close()
		return nil, err
	}
	listen := func(event string, fn func()) {
		for _, node := range field.controls {
			callback := js.FuncOf(func(js.Value, []js.Value) any {
				if v.active() {
					if failure := lifetimeGuard(fn); failure != nil {
						v.emit(failure)
					}
				}
				return nil
			})
			fieldValidationCallbacks++
			v.listeners = append(v.listeners, fieldValidationListener{node, event, callback})
			node.Call("addEventListener", event, callback)
		}
	}
	if opts.OnInput {
		for _, event := range []string{"input", "change"} {
			listen(event, func() {
				if err := v.Trigger(); err != nil {
					v.emit(err)
				}
			})
		}
	}
	if opts.OnBlur {
		listen("blur", func() {
			if err := model.Touch(name); err != nil {
				v.emit(err)
				return
			}
			if err := v.Trigger(); err != nil {
				v.emit(err)
			}
		})
	}
	listen("compositionend", func() {
		if v.resumeComposition {
			v.resumeComposition = false
			if err := v.Trigger(); err != nil {
				v.emit(err)
			}
		}
	})
	stop, err := model.Observe(func(state FormSnapshot[T]) { v.observe(state) })
	if err != nil {
		if stop != nil {
			stop()
		}
		v.Close()
		return nil, err
	}
	if !v.active() {
		stop()
		v.Close()
		return nil, ErrFormClosed
	}
	v.stop = stop
	return v, nil
}
func (v *FieldValidator[T]) active() bool {
	return v != nil && !v.closed && v.scope.active() && v.model.open() && v.model.browser != nil && v.model.dom().fields[v.name] == v.field && fieldValidationOwners[v.model][v.name] == v
}
func (v *FieldValidator[T]) emit(err error) {
	if err == nil || !v.active() {
		return
	}
	if v.options.OnError != nil {
		_ = lifetimeGuard(func() { v.options.OnError(err) })
	} else {
		v.model.reportFormError(err)
	}
}
func (v *FieldValidator[T]) observe(state FormSnapshot[T]) {
	if !v.active() {
		return
	}
	if state.ResetVersion != v.reset {
		v.reset = state.ResetVersion
		v.sequence++
		v.resumeComposition = false
	}
	if a := v.waiting; a != nil && a.ctx.Err() != nil && !v.ownedDeadline(a) {
		if v.model.revision == a.ticket.revision {
			v.resumeComposition = false
		}
		v.waiting = nil
		a.stopTimers()
		a.cancel()
		_ = a.ticket.Cancel()
	}
	if job := v.running; job != nil && job.attempt.ctx.Err() != nil && !v.ownedDeadline(job.attempt) {
		job.attempt.stopTimers()
		job.attempt.cancel()
	}
}

// Trigger captures ready DOM input and records logical pending state now, then
// debounces admission. At most one latest waiting request is retained per helper.
func (v *FieldValidator[T]) Trigger() error {
	if !v.active() {
		return ErrFormClosed
	}
	v.sequence++
	sequence := v.sequence
	if a := v.waiting; a != nil {
		v.waiting = nil
		a.stopTimers()
		a.cancel()
		_ = a.ticket.Cancel()
	}
	if job := v.running; job != nil {
		job.attempt.stopTimers()
		job.attempt.cancel()
		_ = job.attempt.ticket.Cancel()
	}
	if !v.active() || v.sequence != sequence {
		return nil
	}
	err := v.model.Capture()
	composing := errors.Is(err, ErrFormBusy)
	if err != nil && !composing {
		return err
	}
	if !v.active() || v.sequence != sequence {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), v.options.Timeout)
	ticket, err := v.model.BeginFieldValidation(ctx, v.name)
	if err != nil {
		cancel()
		return err
	}
	if !v.active() || v.sequence != sequence {
		cancel()
		_ = ticket.Cancel()
		return nil
	}
	a := &fieldValidationAttempt[T]{ticket: ticket, ctx: ctx, cancel: cancel, sequence: sequence, composing: composing}
	v.waiting = a
	v.resumeComposition = composing
	if err := v.model.Touch(v.name); err != nil {
		v.waiting = nil
		cancel()
		_ = ticket.Cancel()
		return err
	}
	if !v.active() || v.sequence != sequence || v.waiting != a {
		return nil
	}
	a.deadline, err = newValidationTimer(v.setTimer, v.clearTimer, v.options.Timeout, func() { v.expire(a) })
	if err == nil && v.active() && v.sequence == sequence && v.waiting == a {
		a.debounce, err = newValidationTimer(v.setTimer, v.clearTimer, v.options.Debounce, func() { a.ready = true; v.pump() })
	}
	if err != nil {
		if v.waiting == a {
			v.waiting = nil
		}
		a.stopTimers()
		cancel()
		_ = ticket.Cancel()
		return err
	}
	if !v.active() || v.sequence != sequence || v.waiting != a {
		a.stopTimers()
		cancel()
		_ = ticket.Cancel()
	}
	return nil
}
func (v *FieldValidator[T]) owned(a *fieldValidationAttempt[T]) bool {
	return v.active() && v.sequence == a.sequence && v.model.revision == a.ticket.revision && v.model.resetVersion == a.ticket.reset && v.model.validationFence == a.ticket.fence && v.model.validationPending[v.name] == a.ticket
}
func (v *FieldValidator[T]) ownedDeadline(a *fieldValidationAttempt[T]) bool {
	return v.owned(a) && errors.Is(context.Cause(a.ctx), context.DeadlineExceeded)
}
func (v *FieldValidator[T]) applicable(a *fieldValidationAttempt[T]) bool {
	return v.active() && v.sequence == a.sequence && v.model.revision == a.ticket.revision && v.model.resetVersion == a.ticket.reset && v.model.validationFence == a.ticket.fence && v.model.validationPending[v.name] == nil
}
func (v *FieldValidator[T]) expire(a *fieldValidationAttempt[T]) {
	if !v.active() || v.sequence != a.sequence {
		return
	}
	if v.waiting == a {
		v.waiting = nil
	} else if v.running == nil || v.running.attempt != a {
		return
	}
	a.stopTimers()
	v.resumeComposition = false
	_ = a.ticket.Cancel()
	a.cancel()
	if v.applicable(a) {
		v.emit(context.DeadlineExceeded)
	}
}
func (v *FieldValidator[T]) pump() {
	if !v.active() || v.running != nil || v.waiting == nil || !v.waiting.ready {
		return
	}
	a := v.waiting
	revision, fence, reset := v.model.revision, v.model.validationFence, v.model.resetVersion
	err := v.model.Capture()
	if errors.Is(err, ErrFormBusy) {
		a.composing = true
		v.resumeComposition = true
		return
	}
	if err != nil {
		v.waiting = nil
		a.stopTimers()
		a.cancel()
		_ = a.ticket.Cancel()
		if v.applicable(a) {
			v.emit(err)
		}
		return
	}
	if !v.active() || v.sequence != a.sequence {
		return
	}
	if a.ctx.Err() != nil || v.waiting != a {
		if v.model.revision != revision && v.model.validationFence == fence+1 && v.model.resetVersion == reset {
			if err := v.Trigger(); err != nil {
				v.emit(err)
			}
		}
		return
	}
	permit, err := acquireValidationLease(v.binding.ledger, false)
	if err != nil {
		v.waiting = nil
		a.stopTimers()
		a.cancel()
		_ = a.ticket.Cancel()
		if v.applicable(a) {
			v.emit(err)
		}
		return
	}
	input, err := a.ticket.Start()
	if err != nil {
		_ = permit.release()
		if v.waiting == a {
			v.waiting = nil
		}
		a.stopTimers()
		a.cancel()
		_ = a.ticket.Cancel()
		if !errors.Is(err, ErrFormValidationStale) && !errors.Is(err, ErrFormValidationSettled) && !errors.Is(err, ErrFormClosed) && !errors.Is(err, context.Canceled) {
			if v.applicable(a) {
				v.emit(err)
			}
		}
		return
	}
	if !v.active() || v.sequence != a.sequence {
		_ = a.ticket.Fail()
		_ = permit.release()
		a.stopTimers()
		a.cancel()
		return
	}
	v.waiting = nil
	job := &fieldValidationJob[T]{owner: v, attempt: a, permit: permit}
	v.running = job
	job.callback = js.FuncOf(func(js.Value, []js.Value) any { job.settle(); return nil })
	fieldValidationCallbacks++
	validate, workerContext := v.validate, a.ticket.Context()
	go func() {
		if panicErr := lifetimeGuard(func() { job.message, job.failure = validate(workerContext, input) }); panicErr != nil {
			job.failure = panicErr
		}
		job.post()
	}()
}
func (job *fieldValidationJob[T]) post() {
	v := job.owner
	if v.post.Type() == js.TypeFunction {
		if err := lifetimeGuard(func() { v.post.Invoke(job.callback) }); err == nil {
			return
		}
	}
	if err := lifetimeGuard(func() { v.then.Call("call", v.promise, job.callback) }); err == nil {
		return
	}
	// A broken host scheduler cannot publish a result. Re-enter only the cleanup
	// JS callback so the physical permit/ticket/function are not orphaned.
	job.failure = fmt.Errorf("nextgo: field validation settlement scheduler failed")
	_ = lifetimeGuard(func() { job.callback.Invoke() })
}
func (job *fieldValidationJob[T]) settle() {
	if job.finished {
		return
	}
	job.finished = true
	v, a := job.owner, job.attempt
	defer func() {
		a.stopTimers()
		a.cancel()
		_ = job.permit.release()
		job.callback.Release()
		fieldValidationCallbacks--
		if v.running == job {
			v.running = nil
		}
		job.owner = nil
		if v.active() {
			v.pump()
		}
	}()
	current := v.owned(a)
	if current && errors.Is(context.Cause(a.ctx), context.DeadlineExceeded) {
		_ = a.ticket.Fail()
		if v.applicable(a) {
			v.emit(context.DeadlineExceeded)
		}
		return
	}
	if job.failure != nil {
		_ = a.ticket.Fail()
		if current && v.applicable(a) {
			v.emit(job.failure)
		}
		return
	}
	_, err := a.ticket.Complete(job.message)
	if current && err != nil && v.applicable(a) {
		v.emit(err)
	}
}

// Close removes timers/listeners/observers and retires publication authority.
// A physically running validator retains only its bounded final settlement.
func (v *FieldValidator[T]) Close() {
	if v == nil || v.closed {
		return
	}
	v.closed = true
	v.resumeComposition = false
	v.sequence++
	if a := v.waiting; a != nil {
		v.waiting = nil
		a.stopTimers()
		a.cancel()
		_ = a.ticket.Cancel()
	}
	if job := v.running; job != nil {
		job.attempt.stopTimers()
		job.attempt.cancel()
		_ = job.attempt.ticket.Cancel()
	}
	if v.stop != nil {
		v.stop()
		v.stop = nil
	}
	for _, listener := range v.listeners {
		_ = lifetimeGuard(func() { listener.node.Call("removeEventListener", listener.event, listener.callback) })
		listener.callback.Release()
		fieldValidationCallbacks--
	}
	v.listeners = nil
	if v.model != nil {
		owners := fieldValidationOwners[v.model]
		if owners[v.name] == v {
			delete(owners, v.name)
			if len(owners) == 0 {
				delete(fieldValidationOwners, v.model)
			}
			if v.field != nil {
				v.field.close = v.previousClose
			}
		}
	}
	_ = v.binding.release()
	if v.cleanup != nil {
		cleanup := v.cleanup
		v.cleanup = nil
		cleanup.Close()
	}
	v.validate = nil
	v.options.OnError = nil
	v.model = nil
	v.scope = nil
	v.field = nil
	v.previousClose = nil
}
