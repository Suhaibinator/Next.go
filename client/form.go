package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"reflect"
	"strings"
	"unicode/utf8"
)

var (
	ErrFormClosed  = errors.New("nextgo: form is closed")
	ErrFormPending = errors.New("nextgo: a form submission is pending")
	ErrFormSettled = errors.New("nextgo: form submission is settled")
	ErrFormBusy    = errors.New("nextgo: form callback is already running")
	ErrFormInvalid = errors.New("nextgo: form validation failed")
)

type FormPhase string

const (
	FormIdle       FormPhase = "idle"
	FormSubmitting FormPhase = "submitting"
	FormSucceeded  FormPhase = "succeeded"
	FormFailed     FormPhase = "failed"
	FormCanceled   FormPhase = "canceled"
)

type FieldErrors map[string]string

// FormOptions callbacks must be pure, synchronous and return promptly. Clone is
// required for mutable reference-containing values; the default is a value copy.
type FormOptions[T any] struct {
	Clone           func(T) T
	Equal           func(T, T) bool
	Validate        func(T) (FieldErrors, error)
	Merge           func(current, submitted, confirmed T) T
	OnObserverError func(error)
}
type FormSnapshot[T any] struct {
	Values, Baseline          T
	Errors                    FieldErrors
	Touched                   map[string]bool
	Phase                     FormPhase
	Message                   string
	Pending, Dirty, Submitted bool
	Unconfirmed               bool
	Version, ResetVersion     uint64
}
type formObserver[T any] struct {
	fn      func(FormSnapshot[T])
	version uint64
	err     error
}

// Form is a typed client model for one event loop. It is not a concurrent store.
// Edits remain available while submitting; late success never replaces newer edits.
type Form[T any] struct {
	initial, values, baseline                     T
	options                                       FormOptions[T]
	errors                                        FieldErrors
	serverErrors                                  FieldErrors
	touched                                       map[string]bool
	phase                                         FormPhase
	message                                       string
	submitted, closed, busy, publishing           bool
	unconfirmed                                   bool
	revision, version, resetVersion, nextObserver uint64
	observers                                     map[uint64]*formObserver[T]
	active                                        *FormSubmission[T]
	owner                                         *Lifetime
	ownerStop                                     func()
	browser                                       any
	browserStop                                   func()
}
type FormSubmission[T any] struct {
	form     *Form[T]
	input    T
	revision uint64
	ctx      context.Context
	cancel   context.CancelFunc
	settled  bool
}

// FormError carries public application validation messages for browser handlers.
type FormError struct {
	Message string
	Fields  FieldErrors
}

func (e *FormError) Error() string { return e.Message }

type FormResult[T any] struct {
	Value   T
	Message string
}

func NewForm[T any](initial T, options ...FormOptions[T]) (*Form[T], error) {
	if len(options) > 1 {
		return nil, fmt.Errorf("nextgo: form accepts at most one options value")
	}
	var opts FormOptions[T]
	if len(options) == 1 {
		opts = options[0]
	}
	if opts.Clone == nil {
		opts.Clone = func(v T) T { return v }
	}
	if opts.OnObserverError == nil {
		opts.OnObserverError = func(err error) { log.Print(err) }
	}
	if opts.Equal == nil {
		opts.Equal = func(a, b T) bool { return reflect.DeepEqual(a, b) }
	}
	f := &Form[T]{options: opts, phase: FormIdle, version: 1, errors: FieldErrors{}, touched: map[string]bool{}, observers: map[uint64]*formObserver[T]{}}
	if err := lifetimeGuard(func() {
		f.initial = opts.Clone(initial)
		f.values = opts.Clone(initial)
		f.baseline = opts.Clone(initial)
	}); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *Form[T]) open() bool { return f != nil && !f.closed && (f.owner == nil || f.owner.active()) }
func validFormName(name string) bool {
	if name == "" || len(name) > 128 || !utf8.ValidString(name) || strings.TrimSpace(name) != name {
		return false
	}
	for _, r := range name {
		if r < 32 || r == 127 {
			return false
		}
	}
	return true
}
func checkedFieldErrors(fields FieldErrors) (FieldErrors, error) {
	if len(fields) > 128 {
		return nil, fmt.Errorf("nextgo: too many form field errors")
	}
	total := 0
	out := FieldErrors{}
	for name, message := range fields {
		total += len(name) + len(message)
		if !validFormName(name) || len(message) > 2048 || !utf8.ValidString(message) || total > 32<<10 {
			return nil, fmt.Errorf("nextgo: invalid form field errors")
		}
		if message != "" {
			out[name] = message
		}
	}
	return out, nil
}
func validFormMessage(message string) bool { return len(message) <= 4096 && utf8.ValidString(message) }
func (f *Form[T]) clone(value T) (out T, err error) {
	err = lifetimeGuard(func() { out = f.options.Clone(value) })
	return
}
func (f *Form[T]) Initial() (T, error) {
	var zero T
	if !f.open() {
		return zero, ErrFormClosed
	}
	return f.clone(f.initial)
}
func (f *Form[T]) Snapshot() (out FormSnapshot[T], err error) {
	if !f.open() {
		return out, ErrFormClosed
	}
	err = lifetimeGuard(func() {
		out.Values = f.options.Clone(f.values)
		out.Baseline = f.options.Clone(f.baseline)
		out.Dirty = !f.options.Equal(f.values, f.baseline)
	})
	if err != nil {
		return FormSnapshot[T]{}, err
	}
	if !f.open() {
		return FormSnapshot[T]{}, ErrFormClosed
	}
	out.Errors = FieldErrors{}
	for name, message := range f.errors {
		out.Errors[name] = message
	}
	out.Touched = map[string]bool{}
	for name, value := range f.touched {
		out.Touched[name] = value
	}
	out.Phase = f.phase
	out.Message = f.message
	out.Pending = f.active != nil
	out.Submitted = f.submitted
	out.Unconfirmed = f.unconfirmed
	out.Version = f.version
	out.ResetVersion = f.resetVersion
	return out, nil
}
func (f *Form[T]) Edit(name string, patch func(*T)) error { return f.EditMany([]string{name}, patch) }
func (f *Form[T]) EditMany(names []string, patch func(*T)) error {
	if !f.open() {
		return ErrFormClosed
	}
	if f.busy {
		return ErrFormBusy
	}
	if len(names) < 1 || len(names) > 128 || patch == nil {
		return fmt.Errorf("nextgo: invalid form edit")
	}
	seen := map[string]bool{}
	for _, name := range names {
		if !validFormName(name) || seen[name] {
			return fmt.Errorf("nextgo: invalid form edit")
		}
		seen[name] = true
	}
	f.busy = true
	defer func() { f.busy = false }()
	next, err := f.clone(f.values)
	if err != nil {
		return err
	}
	if err := lifetimeGuard(func() { patch(&next) }); err != nil {
		return err
	}
	if !f.open() {
		return ErrFormClosed
	}
	next, err = f.clone(next)
	if err != nil {
		return err
	}
	changed := false
	if err := lifetimeGuard(func() { changed = !f.options.Equal(next, f.values) }); err != nil {
		return err
	}
	if !f.open() {
		return ErrFormClosed
	}
	if !changed {
		return nil
	}
	f.values = next
	f.revision++
	for _, name := range names {
		delete(f.errors, name)
		delete(f.serverErrors, name)
	}
	if f.active == nil {
		f.phase = FormIdle
		f.message = ""
	}
	f.busy = false
	f.changed()
	return nil
}
func (f *Form[T]) Touch(name string) error {
	if !f.open() {
		return ErrFormClosed
	}
	if f.busy {
		return ErrFormBusy
	}
	if !validFormName(name) || (!f.touched[name] && len(f.touched) >= 128) {
		return fmt.Errorf("nextgo: invalid field or touched-field limit exceeded")
	}
	if !f.touched[name] {
		f.touched[name] = true
		f.changed()
	}
	return nil
}
func (f *Form[T]) Reset() error {
	if !f.open() {
		return ErrFormClosed
	}
	if f.busy {
		return ErrFormBusy
	}
	f.busy = true
	defer func() { f.busy = false }()
	next, err := f.clone(f.baseline)
	if err != nil {
		return err
	}
	if !f.open() {
		return ErrFormClosed
	}
	if f.active != nil {
		f.unconfirmed = true
	}
	f.cancelActive()
	f.values = next
	f.revision++
	f.resetVersion++
	f.errors = FieldErrors{}
	f.serverErrors = nil
	f.touched = map[string]bool{}
	f.phase = FormIdle
	f.message = ""
	f.submitted = false
	f.busy = false
	f.changed()
	return nil
}
func (f *Form[T]) validateValues(input T) (FieldErrors, error) {
	fields := FieldErrors{}
	if f.options.Validate != nil {
		var validationErr error
		if err := lifetimeGuard(func() { fields, validationErr = f.options.Validate(input) }); err != nil {
			return nil, err
		}
		if validationErr != nil {
			return nil, validationErr
		}
	}
	return checkedFieldErrors(fields)
}

// Validate evaluates the current draft without creating or settling a request.
// Known server errors remain until their field changes or a new attempt starts.
func (f *Form[T]) Validate() (FieldErrors, error) {
	if !f.open() {
		return nil, ErrFormClosed
	}
	if f.busy {
		return nil, ErrFormBusy
	}
	f.busy = true
	defer func() { f.busy = false }()
	input, err := f.clone(f.values)
	if err != nil {
		return nil, err
	}
	fields, err := f.validateValues(input)
	if err != nil {
		return nil, err
	}
	if !f.open() {
		return nil, ErrFormClosed
	}
	for name, message := range f.serverErrors {
		fields[name] = message
	}
	fields, err = checkedFieldErrors(fields)
	if err != nil {
		return nil, err
	}
	changed := !maps.Equal(fields, f.errors)
	f.errors = fields
	f.busy = false
	if changed {
		f.changed()
	}
	if !f.open() {
		return nil, ErrFormClosed
	}
	return maps.Clone(fields), nil
}

func (f *Form[T]) Begin(ctx context.Context) (*FormSubmission[T], error) {
	if !f.open() {
		return nil, ErrFormClosed
	}
	if f.busy {
		return nil, ErrFormBusy
	}
	if f.active != nil {
		return nil, ErrFormPending
	}
	if ctx == nil {
		return nil, fmt.Errorf("nextgo: form submission requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.busy = true
	defer func() { f.busy = false }()
	input, err := f.clone(f.values)
	if err != nil {
		return nil, err
	}
	fields, err := f.validateValues(input)
	if err != nil {
		return nil, err
	}
	if !f.open() {
		return nil, ErrFormClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.errors = fields
	f.serverErrors = nil
	f.submitted = true
	f.message = ""
	if len(fields) > 0 {
		f.phase = FormFailed
		f.busy = false
		f.changed()
		return nil, ErrFormInvalid
	}
	requestCtx, cancel := context.WithCancel(ctx)
	ticket := &FormSubmission[T]{form: f, input: input, revision: f.revision, ctx: requestCtx, cancel: cancel}
	f.active = ticket
	f.phase = FormSubmitting
	f.busy = false
	f.changed()
	if !f.open() {
		return nil, ErrFormClosed
	}
	if ticket.settled {
		return nil, ErrFormSettled
	}
	return ticket, nil
}
func (t *FormSubmission[T]) Context() context.Context {
	if t == nil {
		return context.Background()
	}
	return t.ctx
}
func (t *FormSubmission[T]) Input() (T, error) {
	var zero T
	if t == nil || t.settled || t.form == nil {
		return zero, ErrFormSettled
	}
	if !t.form.open() {
		return zero, ErrFormClosed
	}
	return t.form.clone(t.input)
}
func (t *FormSubmission[T]) current() (*Form[T], error) {
	if t == nil || t.settled || t.form == nil {
		return nil, ErrFormSettled
	}
	f := t.form
	if !f.open() {
		return nil, ErrFormClosed
	}
	if f.active != t {
		return nil, ErrFormSettled
	}
	if f.busy {
		return nil, ErrFormBusy
	}
	return f, nil
}
func (t *FormSubmission[T]) finish(f *Form[T]) {
	t.settled = true
	t.cancel()
	t.cancel = nil
	t.form = nil
	var zero T
	t.input = zero
	f.active = nil
}
func (t *FormSubmission[T]) Accept(value T, message string) error {
	f, err := t.current()
	if err != nil {
		return err
	}
	if !validFormMessage(message) {
		return fmt.Errorf("nextgo: invalid form message")
	}
	if err := t.ctx.Err(); err != nil {
		t.Cancel()
		return err
	}
	f.busy = true
	defer func() { f.busy = false }()
	baseline, err := f.clone(value)
	if err != nil {
		return err
	}
	var next T
	edited := f.revision != t.revision
	apply := f.revision == t.revision || f.options.Merge != nil
	if f.revision == t.revision {
		next, err = f.clone(value)
	} else if f.options.Merge != nil {
		var current, submitted, confirmed T
		current, err = f.clone(f.values)
		if err == nil {
			submitted, err = f.clone(t.input)
		}
		if err == nil {
			confirmed, err = f.clone(value)
		}
		if err == nil {
			err = lifetimeGuard(func() { next = f.options.Merge(current, submitted, confirmed) })
			if err == nil {
				next, err = f.clone(next)
			}
		}
	}
	if err != nil {
		return err
	}
	fields := FieldErrors{}
	if edited {
		draft := f.values
		if apply {
			draft = next
		}
		validationInput, cloneErr := f.clone(draft)
		if cloneErr != nil {
			return cloneErr
		}
		fields, err = f.validateValues(validationInput)
		if err != nil {
			return err
		}
	}
	if !f.open() {
		return ErrFormClosed
	}
	if err := t.ctx.Err(); err != nil {
		f.busy = false
		t.Cancel()
		return err
	}
	if apply {
		f.values = next
		f.revision++
	}
	f.baseline = baseline
	f.unconfirmed = false
	f.errors = fields
	f.serverErrors = nil
	f.message = message
	f.phase = FormSucceeded
	t.finish(f)
	f.busy = false
	f.changed()
	return nil
}
func (t *FormSubmission[T]) Reject(message string, fields FieldErrors) error {
	f, err := t.current()
	if err != nil {
		return err
	}
	if !validFormMessage(message) {
		return fmt.Errorf("nextgo: invalid form message")
	}
	checked, err := checkedFieldErrors(fields)
	if err != nil {
		return err
	}
	if err := t.ctx.Err(); err != nil {
		t.Cancel()
		return err
	}
	if f.revision == t.revision {
		f.errors = maps.Clone(checked)
		f.serverErrors = checked
	} else {
		f.serverErrors = nil
	}
	f.message = message
	f.phase = FormFailed
	t.finish(f)
	f.changed()
	return nil
}

// Fail records an unconfirmed outcome. Unlike a known application rejection,
// transport/handler failures cannot establish whether a server write committed.
func (t *FormSubmission[T]) Fail(message string) error {
	f, err := t.current()
	if err != nil {
		return err
	}
	if !validFormMessage(message) {
		return fmt.Errorf("nextgo: invalid form message")
	}
	f.unconfirmed = true
	if f.revision == t.revision {
		f.errors = FieldErrors{}
	}
	f.serverErrors = nil
	f.message = message
	f.phase = FormFailed
	t.finish(f)
	f.changed()
	return nil
}
func (t *FormSubmission[T]) Cancel() error {
	f, err := t.current()
	if err != nil {
		return err
	}
	f.unconfirmed = true
	f.cancelActive()
	f.phase = FormCanceled
	f.message = ""
	f.changed()
	return nil
}
func (f *Form[T]) cancelActive() {
	if f.active != nil {
		ticket := f.active
		ticket.finish(f)
	}
}
func (f *Form[T]) Cancel() bool {
	if f == nil || f.active == nil {
		return false
	}
	f.unconfirmed = true
	f.cancelActive()
	if f.open() {
		f.phase = FormCanceled
		f.message = ""
		f.changed()
	}
	return true
}
func (f *Form[T]) Close() {
	if f == nil || f.closed {
		return
	}
	f.closed = true
	f.cancelActive()
	if f.browserStop != nil {
		stop := f.browserStop
		f.browserStop = nil
		stop()
	}
	f.browser = nil
	if f.ownerStop != nil {
		stop := f.ownerStop
		f.ownerStop = nil
		stop()
	}
	f.owner = nil
	f.observers = nil
	f.errors = nil
	f.serverErrors = nil
	f.touched = nil
	var zero T
	f.initial = zero
	f.values = zero
	f.baseline = zero
	f.message = ""
	f.options = FormOptions[T]{}
}
func (f *Form[T]) Observe(fn func(FormSnapshot[T])) (func(), error) {
	if !f.open() {
		return nil, ErrFormClosed
	}
	if fn == nil || len(f.observers) >= 256 {
		return nil, fmt.Errorf("nextgo: invalid observer or form observer limit exceeded")
	}
	f.nextObserver++
	id := f.nextObserver
	observer := &formObserver[T]{fn: fn}
	f.observers[id] = observer
	f.publish()
	if observer.err != nil {
		return nil, observer.err
	}
	if !f.open() {
		return nil, ErrFormClosed
	}
	return func() { delete(f.observers, id) }, nil
}
func (f *Form[T]) changed() { f.version++; f.publish() }
func (f *Form[T]) publish() {
	if f.publishing || !f.open() {
		return
	}
	f.publishing = true
	defer func() { f.publishing = false }()
	deliveries := 0
	for f.open() {
		var id uint64
		var observer *formObserver[T]
		for candidate, item := range f.observers {
			if item.version != f.version {
				id, observer = candidate, item
				break
			}
		}
		if observer == nil {
			return
		}
		if deliveries >= 4096 {
			f.reportFormError(fmt.Errorf("nextgo: form observer notification bound exceeded"))
			f.Close()
			return
		}
		deliveries++
		observer.version = f.version
		snapshot, err := f.Snapshot()
		if err == nil {
			err = lifetimeGuard(func() { observer.fn(snapshot) })
		}
		if err != nil {
			observer.err = err
			delete(f.observers, id)
			f.reportFormError(err)
		}
	}
}
func (f *Form[T]) reportFormError(err error) {
	if f.options.OnObserverError != nil {
		_ = lifetimeGuard(func() { f.options.OnObserverError(err) })
	}
}
