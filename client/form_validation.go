package client

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
)

const defaultFormValidationLimit = 8
const maxFormValidationLimit = 32
const maxFormValidationFields = 128

var (
	ErrFormValidationPending    = errors.New("nextgo: form field validation is pending")
	ErrFormValidationCapacity   = errors.New("nextgo: form field validation capacity exceeded")
	ErrFormValidationSettled    = errors.New("nextgo: form field validation is settled")
	ErrFormValidationStale      = errors.New("nextgo: form field validation is stale")
	ErrFormValidationStarted    = errors.New("nextgo: form field validation already started")
	ErrFormValidationNotStarted = errors.New("nextgo: form field validation has not started")
)

// FormFieldValidation is an explicit event-loop ticket. Begin owns logical
// pending state; Start clones input and reserves physical capacity. A worker
// receives that owned input and Context only. Return Complete/Fail to the same
// event loop once the worker actually returns, including cancellation/panic.
// Cancel revokes authority immediately but never frees a started worker slot.
type FormFieldValidation[T any] struct {
	form                   *Form[T]
	name                   string
	ctx                    context.Context
	cancel                 context.CancelFunc
	revision, reset, fence uint64
	started, settled       bool
}

// BeginFieldValidation issues no asynchronous work. Replacing a ticket cancels
// its authority even for unchanged values. At most 128 field names are tracked
// until Reset; waiting tickets retain no value snapshots. Full-T validation is
// conservative: every actual draft edit retires all checks and async errors.
func (f *Form[T]) BeginFieldValidation(ctx context.Context, name string) (*FormFieldValidation[T], error) {
	if !f.open() {
		return nil, ErrFormClosed
	}
	if f.busy {
		return nil, ErrFormBusy
	}
	if ctx == nil || !validFormName(name) {
		return nil, fmt.Errorf("nextgo: field validation requires context and valid field name")
	}
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	if _, known := f.validationFields[name]; !known && len(f.validationFields) >= maxFormValidationFields {
		return nil, ErrFormValidationCapacity
	}
	if f.validationFields == nil {
		f.validationFields = map[string]struct{}{}
	}
	if f.validationPending == nil {
		f.validationPending = map[string]*FormFieldValidation[T]{}
	}
	if old := f.validationPending[name]; old != nil {
		old.retire()
		if !old.started {
			old.finish()
		}
	}
	name = strings.Clone(name)
	requestCtx, cancel := context.WithCancel(ctx)
	ticket := &FormFieldValidation[T]{form: f, name: name, ctx: requestCtx, cancel: cancel, revision: f.revision, reset: f.resetVersion, fence: f.validationFence}
	f.validationFields[name] = struct{}{}
	f.validationPending[name] = ticket
	f.changed()
	if !f.open() {
		return nil, ErrFormClosed
	}
	if err := ticket.authorityError(); err != nil {
		return nil, err
	}
	return ticket, nil
}
func (t *FormFieldValidation[T]) Context() context.Context {
	if t == nil {
		return context.Background()
	}
	return t.ctx
}
func (t *FormFieldValidation[T]) authorityError() error {
	if t == nil || t.settled || t.form == nil {
		return ErrFormValidationSettled
	}
	f := t.form
	if !f.open() {
		return ErrFormClosed
	}
	if err := t.ctx.Err(); err != nil {
		return context.Cause(t.ctx)
	}
	if f.validationPending[t.name] != t || f.revision != t.revision || f.resetVersion != t.reset || f.validationFence != t.fence {
		return ErrFormValidationStale
	}
	return nil
}

// Start runs on the model event loop, before starting a worker. Capacity errors
// leave a waiting ticket retryable; cancel it if admission will not be retried.
// Other errors retire it. No DTO is retained in the ticket or admission ledger.
func (t *FormFieldValidation[T]) Start() (input T, err error) {
	if t == nil || t.settled || t.form == nil {
		return input, ErrFormValidationSettled
	}
	f := t.form
	// A repeated Start never settles an existing worker, even when that run
	// has since been canceled or disposed. Only its completion owns release.
	if t.started {
		return input, ErrFormValidationStarted
	}
	if f.busy {
		t.finish()
		f.validationChanged()
		return input, ErrFormBusy
	}
	if err = t.authorityError(); err != nil {
		_ = t.Fail()
		return input, err
	}
	if len(f.validationFlights) >= f.options.ValidationLimit {
		return input, ErrFormValidationCapacity
	}
	f.busy = true
	input, err = f.clone(f.values)
	f.busy = false
	if err == nil {
		err = t.authorityError()
	}
	if err != nil {
		var zero T
		input = zero
		_ = t.Fail()
		f.flushValidationNotification()
		return input, err
	}
	if f.validationFlights == nil {
		f.validationFlights = map[*FormFieldValidation[T]]struct{}{}
	}
	t.started = true
	f.validationFlights[t] = struct{}{}
	f.changed()
	// Observers can reset, cancel or dispose before the caller gets the input.
	// No worker exists yet, so rollback this provisional physical admission.
	if err = t.authorityError(); err != nil {
		var zero T
		input = zero
		_ = t.Fail()
		return input, err
	}
	return input, nil
}
func (t *FormFieldValidation[T]) retire() bool {
	if t == nil || t.settled || t.form == nil {
		return false
	}
	changed := t.ctx.Err() == nil
	if t.form.validationPending[t.name] == t {
		delete(t.form.validationPending, t.name)
		changed = true
	}
	t.cancel()
	return changed
}
func (t *FormFieldValidation[T]) finish() {
	if t == nil || t.settled || t.form == nil {
		return
	}
	f := t.form
	t.retire()
	if t.started {
		delete(f.validationFlights, t)
	}
	t.settled = true
	t.form = nil
	t.cancel = nil
}
func (f *Form[T]) validationChanged() {
	if !f.open() {
		return
	}
	if f.busy {
		f.validationNotify = true
		return
	}
	f.changed()
}
func (f *Form[T]) flushValidationNotification() {
	if f.validationNotify && !f.busy && f.open() {
		f.validationNotify = false
		f.changed()
	}
}
func (f *Form[T]) endFormCallback() { f.busy = false; f.flushValidationNotification() }

// Complete settles physical ownership exactly once, even on malformed output or
// stale/disposed/canceled authority. Empty means valid; nonempty is a known field
// validation message, not a submission error or unknown mutation outcome.
func (t *FormFieldValidation[T]) Complete(message string) (applied bool, err error) {
	if t == nil || t.settled || t.form == nil {
		return false, ErrFormValidationSettled
	}
	f := t.form
	started := t.started
	authority := t.authorityError()
	if f.busy && authority == nil {
		authority = ErrFormBusy
	}
	checked, checkErr := checkedFieldErrors(FieldErrors{t.name: message})
	var combined FieldErrors
	var candidate FieldErrors
	if authority == nil && started && checkErr == nil {
		candidate = maps.Clone(f.validationErrors)
		if candidate == nil {
			candidate = FieldErrors{}
		}
		if message == "" {
			delete(candidate, t.name)
		} else {
			candidate[t.name] = strings.Clone(checked[t.name])
		}
		candidate, checkErr = checkedFieldErrors(candidate)
		if checkErr == nil {
			combined, checkErr = mergeFormErrors(f.syncErrors, candidate, f.serverErrors)
		}
	}
	t.finish()
	defer f.validationChanged()
	if authority != nil {
		return false, authority
	}
	if !started {
		return false, ErrFormValidationNotStarted
	}
	if checkErr != nil {
		return false, checkErr
	}
	f.validationErrors = candidate
	f.errors = combined
	return true, nil
}

// Fail releases a waiting/started ticket without publishing any field message.
// Use it for transport failure or after a recovered validator panic. It never
// changes Form.Unconfirmed, submission phase, values, baseline or server errors.
func (t *FormFieldValidation[T]) Fail() error {
	if t == nil || t.settled || t.form == nil {
		return ErrFormValidationSettled
	}
	f := t.form
	t.finish()
	f.validationChanged()
	return nil
}

// Cancel retires a check. A started check must still call Complete or Fail once
// its worker returns; cancellation alone cannot establish physical settlement.
func (t *FormFieldValidation[T]) Cancel() error {
	if t == nil || t.settled || t.form == nil {
		return ErrFormValidationSettled
	}
	f := t.form
	changed := t.retire()
	if !t.started {
		t.finish()
	}
	if changed {
		f.validationChanged()
	}
	return nil
}
func (f *Form[T]) hasFieldValidations() bool {
	for _, ticket := range f.validationPending {
		if ticket.ctx.Err() == nil {
			return true
		}
	}
	return false
}
func (f *Form[T]) retireFieldValidations(clearErrors bool) bool {
	changed := len(f.validationPending) > 0
	f.validationFence++
	tickets := make([]*FormFieldValidation[T], 0, len(f.validationPending))
	for _, ticket := range f.validationPending {
		tickets = append(tickets, ticket)
	}
	for _, ticket := range tickets {
		ticket.retire()
		if !ticket.started {
			ticket.finish()
		}
	}
	if clearErrors && len(f.validationErrors) > 0 {
		changed = true
		f.validationErrors = nil
	}
	return changed
}
func mergeFormErrors(sync, async, server FieldErrors) (FieldErrors, error) {
	fields := maps.Clone(async)
	if fields == nil {
		fields = FieldErrors{}
	}
	for name, message := range sync {
		fields[name] = message
	}
	for name, message := range server {
		fields[name] = message
	}
	return checkedFieldErrors(fields)
}
