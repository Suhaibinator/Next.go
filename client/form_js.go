//go:build js && wasm

package client

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"syscall/js"
	"time"
)

type FieldBindingOptions struct {
	Error Element
	// ValidateOnBlur runs the whole model validator after recording this field
	// as touched. It defaults to false and never starts a submission.
	ValidateOnBlur bool
}
type SubmitOptions struct {
	Status         Element
	Timeout        time.Duration
	FailureMessage string
}
type formFieldPatch[T any] struct {
	apply   func(*T)
	matches func(T) bool
}
type formDOMField[T any] struct {
	controls []js.Value
	capture  func() (formFieldPatch[T], error)
	check    func() error
	close    func()
}
type formDOM[T any] struct {
	model  *Form[T]
	fields map[string]*formDOMField[T]
	submit *Scope
}

func (f *Form[T]) dom() *formDOM[T] {
	if f.browser != nil {
		return f.browser.(*formDOM[T])
	}
	registry := &formDOM[T]{model: f, fields: map[string]*formDOMField[T]{}}
	f.browser = registry
	f.browserStop = func() {
		if registry.submit != nil {
			registry.submit.dispose()
		}
		for _, field := range registry.fields {
			field.close()
		}
		registry.fields = nil
		registry.model = nil
	}
	return registry
}
func NewScopedForm[T any](scope *Scope, initial T, options ...FormOptions[T]) (*Form[T], error) {
	if !scope.active() {
		return nil, ErrFormClosed
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("nextgo: at most one form options value")
	}
	var opts FormOptions[T]
	if len(options) == 1 {
		opts = options[0]
	}
	if opts.OnObserverError == nil {
		opts.OnObserverError = func(err error) { report(err.Error()) }
	}
	owner, err := scope.Lifetime()
	if err != nil {
		return nil, err
	}
	model, err := NewForm(initial, opts)
	if err != nil {
		return nil, err
	}
	resource, err := owner.Defer(model.Close)
	if err != nil {
		model.Close()
		return nil, err
	}
	model.owner = owner
	model.ownerStop = resource.Close
	return model, nil
}
func formBindingGuard(binding **Scope, err *error) {
	if failure := recover(); failure != nil {
		*err = fmt.Errorf("nextgo: form binding panic (%T)", failure)
	}
	if *err != nil && *binding != nil {
		(*binding).dispose()
	}
}
func ownedFormElement(scope *Scope, element Element) bool {
	return scope.active() && element.Value.Type() == js.TypeObject && !element.Value.IsNull() && element.Value.Get("nodeType").Type() == js.TypeNumber && element.Value.Get("nodeType").Int() == 1 && scope.ownsElement(element.Value, true)
}
func newFormScope(parent *Scope, root Element) (*Scope, error) {
	child := &Scope{parent: parent, owner: parent.islandRoot(), Root: root}
	life, err := child.Lifetime()
	if err != nil {
		return nil, err
	}
	if _, err := life.Defer(child.dispose); err != nil {
		child.dispose()
		return nil, err
	}
	return child, nil
}
func validOptionalFormNode(scope *Scope, node Element) bool {
	return node.Value.IsUndefined() || node.Value.IsNull() || ownedFormElement(scope, node)
}
func fieldOptions(scope *Scope, options []FieldBindingOptions) (FieldBindingOptions, error) {
	if len(options) > 1 {
		return FieldBindingOptions{}, fmt.Errorf("nextgo: at most one field binding options value")
	}
	var opts FieldBindingOptions
	if len(options) == 1 {
		opts = options[0]
	}
	if !validOptionalFormNode(scope, opts.Error) {
		return opts, fmt.Errorf("nextgo: field error node is outside scope")
	}
	return opts, nil
}
func showFormFieldError[T any](control Element, node Element, name string, state FormSnapshot[T]) {
	message := ""
	if state.Touched[name] || state.Submitted {
		message = state.Errors[name]
	}
	if message != "" {
		control.Attribute("aria-invalid", "true")
	} else {
		control.Value.Call("removeAttribute", "aria-invalid")
	}
	control.Value.Call("setCustomValidity", message)
	if node.Value.Truthy() {
		node.Text(message)
	}
}
func saveFieldUI(scope *Scope, control, node Element) {
	aria := control.Value.Call("getAttribute", "aria-invalid")
	custom := ""
	if control.Value.Get("validity").Get("customError").Bool() {
		custom = control.Value.Get("validationMessage").String()
	}
	text := ""
	if node.Value.Truthy() {
		text = node.Value.Get("textContent").String()
	}
	scope.Cleanup(func() {
		control.Value.Call("setCustomValidity", custom)
		if aria.IsNull() {
			control.Value.Call("removeAttribute", "aria-invalid")
		} else {
			control.Attribute("aria-invalid", aria.String())
		}
		if node.Value.Truthy() {
			node.Text(text)
		}
	})
}

func formFieldBlur[T any](model *Form[T], name string, validate bool) {
	if err := model.captureDOMFields(false); err != nil {
		if !errors.Is(err, ErrFormClosed) {
			report(err.Error())
		}
		return
	}
	if err := model.Touch(name); err != nil {
		if !errors.Is(err, ErrFormClosed) {
			report(err.Error())
		}
		return
	}
	if validate {
		if _, err := model.Validate(); err != nil && !errors.Is(err, ErrFormClosed) {
			report(err.Error())
		}
	}
}
func registerFormField[T any](scope *Scope, control Element, model *Form[T], name string) (*Scope, *formDOMField[T], error) {
	return registerFormControls(scope, []Element{control}, model, name)
}
func registerFormControls[T any](scope *Scope, controls []Element, model *Form[T], name string) (*Scope, *formDOMField[T], error) {
	if !model.open() {
		return nil, nil, ErrFormClosed
	}
	if !validFormName(name) || len(controls) == 0 || len(controls) > 64 {
		return nil, nil, fmt.Errorf("nextgo: form control name/ownership mismatch")
	}
	for i, control := range controls {
		if !ownedFormElement(scope, control) || control.Value.Get("name").String() != name {
			return nil, nil, fmt.Errorf("nextgo: form control name/ownership mismatch")
		}
		for _, previous := range controls[:i] {
			if previous.Value.Equal(control.Value) {
				return nil, nil, fmt.Errorf("nextgo: duplicate form control")
			}
		}
	}
	registry := model.dom()
	if len(registry.fields) >= 128 || registry.fields[name] != nil {
		return nil, nil, fmt.Errorf("nextgo: duplicate field binding or field limit exceeded")
	}
	count := len(controls)
	for _, field := range registry.fields {
		count += len(field.controls)
	}
	if count > 256 {
		return nil, nil, fmt.Errorf("nextgo: bound form control limit exceeded")
	}
	root := controls[0]
	if len(controls) > 1 {
		root = scope.Root
	}
	child, err := newFormScope(scope, root)
	if err != nil {
		return nil, nil, err
	}
	field := &formDOMField[T]{close: child.dispose}
	for _, control := range controls {
		field.controls = append(field.controls, control.Value)
	}
	registry.fields[name] = field
	child.Cleanup(func() {
		if registry.fields[name] == field {
			delete(registry.fields, name)
		}
	})
	return child, field, nil
}
func selectDefault(control js.Value) string {
	options := control.Get("options")
	value := ""
	selected := false
	for i := 0; i < options.Length(); i++ {
		option := options.Index(i)
		if option.Get("defaultSelected").Bool() {
			value = option.Get("value").String()
			selected = true
		}
	}
	if !selected {
		for i := 0; i < options.Length(); i++ {
			option := options.Index(i)
			parent := option.Get("parentElement")
			disabled := option.Get("disabled").Bool() || (parent.Get("tagName").String() == "OPTGROUP" && parent.Get("disabled").Bool())
			if !disabled {
				return option.Get("value").String()
			}
		}
	}
	return value
}

// BindText controls an input, textarea or single-select without replacing nodes.
// Live edits/autofill are adopted after validating the SSR default against known
// model values. Input setters/getters must be pure and return promptly.
func BindText[T any](scope *Scope, control Element, model *Form[T], name string, get func(T) string, set func(*T, string), options ...FieldBindingOptions) (err error) {
	var binding *Scope
	defer formBindingGuard(&binding, &err)
	if get == nil || set == nil || !ownedFormElement(scope, control) {
		return fmt.Errorf("nextgo: invalid text field binding")
	}
	tag := control.Value.Get("tagName").String()
	isSelect := tag == "SELECT"
	if tag != "INPUT" && tag != "TEXTAREA" && !isSelect {
		return fmt.Errorf("nextgo: text binding requires input, textarea or select")
	}
	if tag == "INPUT" {
		switch control.Value.Get("type").String() {
		case "text", "search", "tel", "url", "email", "password", "number", "date", "datetime-local", "month", "week", "time", "color", "range", "hidden":
		default:
			return fmt.Errorf("nextgo: unsupported text input type")
		}
	}
	if isSelect && (control.Value.Get("multiple").Bool() || control.Value.Get("size").Int() > 1) {
		return fmt.Errorf("nextgo: text binding supports single selects of size one")
	}
	opts, err := fieldOptions(scope, options)
	if err != nil {
		return err
	}
	snapshot, err := model.Snapshot()
	if err != nil {
		return err
	}
	initial, err := model.Initial()
	if err != nil {
		return err
	}
	expected := control.Value.Get("defaultValue").String()
	if isSelect {
		expected = selectDefault(control.Value)
	}
	if expected != get(initial) && expected != get(snapshot.Baseline) && expected != get(snapshot.Values) {
		return fmt.Errorf("nextgo: text field SSR default differs from form values")
	}
	var field *formDOMField[T]
	binding, field, err = registerFormField(scope, control, model, name)
	if err != nil {
		return err
	}
	saveFieldUI(binding, control, opts.Error)
	composing := false
	resetVersion := snapshot.ResetVersion
	sync := func() error {
		if !binding.active() {
			return ErrFormClosed
		}
		if composing {
			return nil
		}
		return model.captureDOMFields(false)
	}
	field.capture = func() (formFieldPatch[T], error) {
		if !binding.active() {
			return formFieldPatch[T]{}, ErrFormClosed
		}
		if composing {
			return formFieldPatch[T]{}, ErrFormBusy
		}
		value := control.Value.Get("value").String()
		return formFieldPatch[T]{apply: func(input *T) { set(input, value) }, matches: func(input T) bool { return get(input) == value }}, nil
	}
	if err := sync(); err != nil {
		return err
	}
	binding.On(control, "compositionstart", func(Event) { composing = true })
	binding.On(control, "compositionend", func(Event) {
		composing = false
		if err := sync(); err != nil && !errors.Is(err, ErrFormClosed) {
			report(err.Error())
		}
	})
	for _, event := range []string{"input", "change"} {
		binding.On(control, event, func(Event) {
			if err := sync(); err != nil && !errors.Is(err, ErrFormClosed) {
				report(err.Error())
			}
		})
	}
	binding.On(control, "blur", func(Event) {
		formFieldBlur(model, name, opts.ValidateOnBlur)
	})
	stop, err := model.Observe(func(state FormSnapshot[T]) {
		if !binding.active() {
			return
		}
		if state.ResetVersion != resetVersion {
			composing = false
			resetVersion = state.ResetVersion
		}
		if !composing && control.Value.Get("value").String() != get(state.Values) {
			control.Property("value", get(state.Values))
		}
		showFormFieldError(control, opts.Error, name, state)
	})
	if err != nil {
		return err
	}
	binding.Cleanup(stop)
	return nil
}
func BindChecked[T any](scope *Scope, control Element, model *Form[T], name string, get func(T) bool, set func(*T, bool), options ...FieldBindingOptions) (err error) {
	var binding *Scope
	defer formBindingGuard(&binding, &err)
	if get == nil || set == nil || !ownedFormElement(scope, control) || control.Value.Get("tagName").String() != "INPUT" || control.Value.Get("type").String() != "checkbox" {
		return fmt.Errorf("nextgo: checked binding requires a checkbox")
	}
	opts, err := fieldOptions(scope, options)
	if err != nil {
		return err
	}
	snapshot, err := model.Snapshot()
	if err != nil {
		return err
	}
	initial, err := model.Initial()
	if err != nil {
		return err
	}
	expected := control.Value.Get("defaultChecked").Bool()
	if expected != get(initial) && expected != get(snapshot.Baseline) && expected != get(snapshot.Values) {
		return fmt.Errorf("nextgo: checkbox SSR default differs from form values")
	}
	var field *formDOMField[T]
	binding, field, err = registerFormField(scope, control, model, name)
	if err != nil {
		return err
	}
	saveFieldUI(binding, control, opts.Error)
	sync := func() error {
		if !binding.active() {
			return ErrFormClosed
		}
		return model.captureDOMFields(false)
	}
	field.capture = func() (formFieldPatch[T], error) {
		if !binding.active() {
			return formFieldPatch[T]{}, ErrFormClosed
		}
		value := control.Value.Get("checked").Bool()
		return formFieldPatch[T]{apply: func(input *T) { set(input, value) }, matches: func(input T) bool { return get(input) == value }}, nil
	}
	if err := sync(); err != nil {
		return err
	}
	binding.On(control, "change", func(Event) {
		if err := sync(); err != nil && !errors.Is(err, ErrFormClosed) {
			report(err.Error())
		}
	})
	binding.On(control, "blur", func(Event) { formFieldBlur(model, name, opts.ValidateOnBlur) })
	stop, err := model.Observe(func(state FormSnapshot[T]) {
		if !binding.active() {
			return
		}
		if control.Value.Get("checked").Bool() != get(state.Values) {
			control.Property("checked", get(state.Values))
		}
		showFormFieldError(control, opts.Error, name, state)
	})
	if err != nil {
		return err
	}
	binding.Cleanup(stop)
	return nil
}
func formStatus[T any](state FormSnapshot[T]) string {
	if state.Pending {
		return "Saving…"
	}
	if len(state.Validating) > 0 {
		return "Checking fields…"
	}
	switch state.Phase {
	case FormSucceeded:
		message := state.Message
		if message == "" {
			message = "Saved submitted values."
		}
		if state.Dirty {
			message += " Newer edits are unsaved."
		}
		return message
	case FormFailed:
		if state.Message != "" {
			return state.Message
		}
		return "Review the highlighted fields."
	case FormCanceled:
		return "Canceled locally. Check the server before retrying."
	}
	if state.Unconfirmed {
		return "Previous submission outcome is unknown. Check the server before retrying."
	}
	if state.Dirty {
		return "Unsaved changes."
	}
	return "Ready."
}

func checkFormControls[T any](root js.Value, registry *formDOM[T]) error {
	controls := root.Get("elements")
	if controls.Length() > 256 {
		return fmt.Errorf("nextgo: form control limit exceeded")
	}
	for i := 0; i < controls.Length(); i++ {
		control := controls.Index(i)
		tag := control.Get("tagName").String()
		kind := control.Get("type").String()
		name := control.Get("name").String()
		if name == "" || (tag != "INPUT" && tag != "TEXTAREA" && tag != "SELECT") || kind == "submit" || kind == "reset" || kind == "button" || kind == "image" {
			continue
		}
		field := registry.fields[name]
		matched := false
		if field != nil {
			for _, bound := range field.controls {
				if bound.Equal(control) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return fmt.Errorf("nextgo: named data control has no matching field binding")
		}
	}
	for _, field := range registry.fields {
		for _, control := range field.controls {
			if !control.Get("form").Equal(root) {
				return fmt.Errorf("nextgo: bound control belongs to another form")
			}
		}
		if field.check != nil {
			if err := field.check(); err != nil {
				return err
			}
		}
	}
	return nil
}

// BindSubmit enhances an ordinary same-origin POST form. Named submitters,
// override attributes, image buttons and non-self targets retain native behavior.
// The handler runs in a goroutine; only one submission is active at a time.
func BindSubmit[T any](scope *Scope, root Element, model *Form[T], handler func(context.Context, T) (FormResult[T], error), options ...SubmitOptions) (err error) {
	var binding *Scope
	defer formBindingGuard(&binding, &err)
	if !model.open() {
		return ErrFormClosed
	}
	if !ownedFormElement(scope, root) || root.Value.Get("tagName").String() != "FORM" || handler == nil || len(options) > 1 {
		return fmt.Errorf("nextgo: invalid form submit binding")
	}
	if root.Value.Get("method").String() != "post" {
		return fmt.Errorf("nextgo: enhanced forms require POST")
	}
	var opts SubmitOptions
	if len(options) == 1 {
		opts = options[0]
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.FailureMessage == "" {
		opts.FailureMessage = "Could not confirm the submission. Check the server before retrying."
	}
	if opts.Timeout < 0 || opts.Timeout > 5*time.Minute || !validFormMessage(opts.FailureMessage) || !validOptionalFormNode(scope, opts.Status) {
		return fmt.Errorf("nextgo: invalid form submit options")
	}
	registry := model.dom()
	if err := checkFormControls(root.Value, registry); err != nil {
		return err
	}
	if registry.submit != nil {
		return fmt.Errorf("nextgo: form model already has a submit binding")
	}
	controls := root.Value.Get("elements")
	if controls.Length() > 256 {
		return fmt.Errorf("nextgo: form control limit exceeded")
	}
	type buttonState struct {
		node     js.Value
		disabled bool
	}
	buttons := []buttonState{}
	for i := 0; i < controls.Length(); i++ {
		node := controls.Index(i)
		if !scope.Root.Value.Call("contains", node).Bool() || !node.Call("closest", "[data-nextgo-island]").Equal(scope.islandRoot()) {
			return fmt.Errorf("nextgo: associated control is outside the form scope")
		}
		tag := node.Get("tagName").String()
		kind := node.Get("type").String()
		if (tag == "BUTTON" || tag == "INPUT") && (kind == "submit" || kind == "image") {
			buttons = append(buttons, buttonState{node, node.Get("disabled").Bool()})
		}
	}
	binding, err = newFormScope(scope, root)
	if err != nil {
		return err
	}
	registry.submit = binding
	busyAttr := root.Value.Call("getAttribute", "aria-busy")
	binding.Cleanup(func() {
		if registry.submit == binding {
			registry.submit = nil
		}
		model.Cancel()
		for _, button := range buttons {
			button.node.Set("disabled", button.disabled)
		}
		if busyAttr.IsNull() {
			root.Value.Call("removeAttribute", "aria-busy")
		} else {
			root.Attribute("aria-busy", busyAttr.String())
		}
	})
	stop, err := model.Observe(func(state FormSnapshot[T]) {
		if !binding.active() {
			return
		}
		busy := state.Pending || len(state.Validating) > 0
		root.Attribute("aria-busy", strconv.FormatBool(busy))
		for _, button := range buttons {
			button.node.Set("disabled", button.disabled || busy)
		}
		if opts.Status.Value.Truthy() {
			opts.Status.Text(formStatus(state))
		}
	})
	if err != nil {
		return err
	}
	binding.Cleanup(stop)
	binding.On(root, "reset", func(event Event) {
		if event.Value.Get("defaultPrevented").Bool() {
			return
		}
		event.PreventDefault()
		if err := model.Reset(); err != nil && !errors.Is(err, ErrFormClosed) {
			report(err.Error())
		}
	})
	binding.On(root, "submit", func(event Event) {
		if event.Value.Get("defaultPrevented").Bool() {
			return
		}
		target := root.Value.Get("target").String()
		if target != "" && target != "_self" {
			return
		}
		submitter := event.Value.Get("submitter")
		if submitter.Truthy() {
			if submitter.Get("name").String() != "" || submitter.Get("type").String() == "image" || submitter.Call("hasAttribute", "formaction").Bool() || submitter.Call("hasAttribute", "formmethod").Bool() || submitter.Call("hasAttribute", "formtarget").Bool() {
				return
			}
		}
		url := win.Get("URL").New(root.Value.Get("action").String(), win.Get("location").Get("href"))
		if url.Get("origin").String() != win.Get("location").Get("origin").String() || url.Get("username").String() != "" || url.Get("password").String() != "" {
			return
		}
		if err := checkFormControls(root.Value, registry); err != nil {
			return
		}
		event.PreventDefault()

		if err := model.Capture(); err != nil {
			if errors.Is(err, ErrFormBusy) {
				if opts.Status.Value.Truthy() {
					opts.Status.Text("Finish text composition before submitting.")
				}
			} else if !errors.Is(err, ErrFormClosed) {
				report(err.Error())
			}
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
		ticket, err := model.Begin(ctx)
		if err != nil {
			cancel()
			if !errors.Is(err, ErrFormInvalid) && !errors.Is(err, ErrFormPending) && !errors.Is(err, ErrFormValidationPending) && !errors.Is(err, ErrFormClosed) && !errors.Is(err, ErrFormSettled) {
				report(err.Error())
			}
			return
		}
		go func() {
			defer cancel()
			var result FormResult[T]
			var failure error
			if panicErr := lifetimeGuard(func() {
				input, err := ticket.Input()
				if err != nil {
					failure = err
					return
				}
				if !binding.active() {
					failure = ErrFormClosed
					return
				}
				result, failure = handler(ticket.Context(), input)
			}); panicErr != nil {
				failure = panicErr
			}
			if !binding.active() || !model.open() {
				return
			}
			if ticket.Context().Err() != nil {
				_ = ticket.Cancel()
				return
			}
			if failure != nil {
				var validation *FormError
				if errors.As(failure, &validation) && validation != nil {
					err = ticket.Reject(validation.Message, validation.Fields)
				} else {
					err = ticket.Fail(opts.FailureMessage)
				}
			} else {
				err = ticket.Accept(result.Value, result.Message)
			}
			if err != nil && !errors.Is(err, ErrFormSettled) && !errors.Is(err, ErrFormClosed) {
				_ = ticket.Fail(opts.FailureMessage)
				report(err.Error())
			}
		}()
	})
	return nil
}

// Capture adopts all bound DOM values atomically before a custom submission.
// It returns ErrFormBusy while text composition is active and does not submit.
func (f *Form[T]) Capture() error { return f.captureDOMFields(true) }

func (f *Form[T]) captureDOMFields(requireReady bool) error {
	if !f.open() {
		return ErrFormClosed
	}
	if f.busy {
		return ErrFormBusy
	}
	if f.browser == nil {
		return nil
	}
	f.busy = true
	defer func() { f.busy = false }()
	registry := f.dom()
	names := make([]string, 0, len(registry.fields))
	for name := range registry.fields {
		names = append(names, name)
	}
	sort.Strings(names)
	changed := []string{}
	patches := []formFieldPatch[T]{}
	for _, name := range names {
		var patch formFieldPatch[T]
		var err error
		if failure := lifetimeGuard(func() { patch, err = registry.fields[name].capture() }); failure != nil {
			return failure
		}
		if err != nil {
			if !requireReady && errors.Is(err, ErrFormBusy) {
				continue
			}
			return err
		}
		if !f.open() {
			return ErrFormClosed
		}
		matches := false
		if err := lifetimeGuard(func() { matches = patch.matches(f.values) }); err != nil {
			return err
		}
		if !f.open() {
			return ErrFormClosed
		}
		if !matches {
			changed = append(changed, name)
			patches = append(patches, patch)
		}
	}
	f.busy = false
	if len(changed) == 0 {
		return nil
	}
	return f.EditMany(changed, func(input *T) {
		for _, patch := range patches {
			patch.apply(input)
		}
	})
}
