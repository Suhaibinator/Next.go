//go:build js && wasm

package client

import (
	"errors"
	"fmt"
	"slices"
	"syscall/js"
)

// BindRadioGroup controls a complete native radio group. Empty model text means
// no selected radio; option values must be nonempty and unique. Every peer must
// belong to this scope, share a form owner/root and be present in controls.
func BindRadioGroup[T any](scope *Scope, controls []Element, model *Form[T], name string, get func(T) string, set func(*T, string), options ...FieldBindingOptions) error {
	if get == nil || set == nil {
		return fmt.Errorf("nextgo: radio binding requires getter and setter")
	}
	return bindRadioChoice(scope, controls, model, name, func(input T) RadioChoice {
		value := get(input)
		return RadioChoice{Value: value, Selected: value != ""}
	}, func(input *T, value RadioChoice) { set(input, value.Value) }, false, options...)
}

// BindRadioChoice adopts a complete native group, including an empty-valued
// selected radio. RadioChoice{} means no selection. Option values remain unique
// and fixed; bounds, ownership, hydration, reset and Capture match BindRadioGroup.
func BindRadioChoice[T any](scope *Scope, controls []Element, model *Form[T], name string, get func(T) RadioChoice, set func(*T, RadioChoice), options ...FieldBindingOptions) error {
	return bindRadioChoice(scope, controls, model, name, get, set, true, options...)
}

func bindRadioChoice[T any](scope *Scope, controls []Element, model *Form[T], name string, get func(T) RadioChoice, set func(*T, RadioChoice), allowEmpty bool, options ...FieldBindingOptions) (err error) {
	var binding *Scope
	defer formBindingGuard(&binding, &err)
	if get == nil || set == nil || len(controls) == 0 || len(controls) > 64 {
		return fmt.Errorf("nextgo: radio binding requires 1 to 64 controls")
	}
	controls = slices.Clone(controls)
	values := make([]string, len(controls))
	var owner, tree js.Value
	defaults, live := RadioChoice{}, RadioChoice{}
	for i, control := range controls {
		if !ownedFormElement(scope, control) || control.Value.Get("tagName").String() != "INPUT" || control.Value.Get("type").String() != "radio" || control.Value.Get("name").String() != name {
			return fmt.Errorf("nextgo: radio control name/type/ownership mismatch")
		}
		if i == 0 {
			owner = control.Value.Get("form")
			tree = control.Value.Call("getRootNode")
		} else if !owner.Equal(control.Value.Get("form")) || !tree.Equal(control.Value.Call("getRootNode")) {
			return fmt.Errorf("nextgo: radio controls must share their form owner and tree")
		}
		values[i] = control.Value.Get("value").String()
		if control.Value.Get("defaultChecked").Bool() {
			if defaults.Selected {
				return fmt.Errorf("nextgo: multiple SSR radio defaults")
			}
			defaults = RadioChoice{Value: values[i], Selected: true}
		}
		if control.Value.Get("checked").Bool() {
			if live.Selected {
				return fmt.Errorf("nextgo: multiple selected radios")
			}
			live = RadioChoice{Value: values[i], Selected: true}
		}
	}
	domain, err := formChoiceDomain(values, allowEmpty)
	if err != nil {
		return err
	}
	opts, err := fieldOptions(scope, options)
	if err != nil {
		return err
	}
	check := func() error {
		var peers js.Value
		if owner.Truthy() {
			peers = owner.Get("elements")
		} else {
			escaped := win.Get("CSS").Call("escape", name).String()
			peers = tree.Call("querySelectorAll", "input[type=radio][name="+escaped+"]")
		}
		if peers.Length() > 256 {
			return fmt.Errorf("nextgo: radio peer limit exceeded")
		}
		count := 0
		for i := 0; i < peers.Length(); i++ {
			peer := peers.Index(i)
			if peer.Get("tagName").String() != "INPUT" || peer.Get("type").String() != "radio" || peer.Get("name").String() != name || !peer.Get("form").Equal(owner) {
				continue
			}
			found := false
			for _, control := range controls {
				if control.Value.Equal(peer) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("nextgo: radio group has an unbound peer")
			}
			count++
		}
		if count != len(controls) {
			return fmt.Errorf("nextgo: radio group changed")
		}
		for i, control := range controls {
			if !ownedFormElement(scope, control) || !control.Value.Call("getRootNode").Equal(tree) || control.Value.Get("value").String() != values[i] {
				return fmt.Errorf("nextgo: radio group shape changed")
			}
		}
		return nil
	}
	if err := check(); err != nil {
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
	for _, value := range []RadioChoice{get(initial), get(snapshot.Baseline), get(snapshot.Values)} {
		if !validRadioChoice(value, domain) {
			return fmt.Errorf("nextgo: model contains unknown radio choice")
		}
	}
	if defaults != get(initial) && defaults != get(snapshot.Baseline) && defaults != get(snapshot.Values) {
		return fmt.Errorf("nextgo: radio SSR default differs from form values")
	}
	var field *formDOMField[T]
	binding, field, err = registerFormControls(scope, controls, model, name)
	if err != nil {
		return err
	}
	field.check = check
	read := func() (RadioChoice, error) {
		if !binding.active() {
			return RadioChoice{}, ErrFormClosed
		}
		if err := check(); err != nil {
			return RadioChoice{}, err
		}
		value := RadioChoice{}
		for i, control := range controls {
			if control.Value.Get("checked").Bool() {
				if value.Selected {
					return RadioChoice{}, fmt.Errorf("nextgo: multiple selected radios")
				}
				value = RadioChoice{Value: values[i], Selected: true}
			}
		}
		return value, nil
	}
	sync := func() error { return model.captureDOMFields(false) }
	field.capture = func() (formFieldPatch[T], error) {
		value, err := read()
		if err != nil {
			return formFieldPatch[T]{}, err
		}
		return formFieldPatch[T]{apply: func(input *T) { set(input, value) }, matches: func(input T) bool { return get(input) == value }}, nil
	}
	for _, control := range controls {
		saveFieldUI(binding, control, opts.Error)
	}
	if err := sync(); err != nil {
		return err
	}
	for _, control := range controls {
		binding.On(control, "change", func(Event) {
			if err := sync(); err != nil && !errors.Is(err, ErrFormClosed) {
				report(err.Error())
			}
		})
		binding.On(control, "blur", func(Event) { formFieldBlur(model, name, opts.ValidateOnBlur) })
	}
	stop, err := model.Observe(func(state FormSnapshot[T]) {
		if !binding.active() {
			return
		}
		if err := check(); err != nil {
			report(err.Error())
			return
		}
		value := get(state.Values)
		if !validRadioChoice(value, domain) {
			report("nextgo: model contains unknown radio choice")
			return
		}
		for i, control := range controls {
			want := value.Selected && values[i] == value.Value
			if control.Value.Get("checked").Bool() != want {
				control.Property("checked", want)
			}
			showFormFieldError(control, opts.Error, name, state)
		}
	})
	if err != nil {
		return err
	}
	binding.Cleanup(stop)
	return nil
}

// BindMultiple controls a native multiple-select. Options and their values are
// fixed for this binding's lifetime, including options inside optgroups.
func BindMultiple[T any](scope *Scope, control Element, model *Form[T], name string, get func(T) []string, set func(*T, []string), options ...FieldBindingOptions) error {
	if get == nil || set == nil {
		return fmt.Errorf("nextgo: multiple binding requires getter and setter")
	}
	return bindMultipleOptions(scope, control, model, name, func(input T, values []string) ([]int, error) {
		domain, err := formChoiceDomain(values, true)
		if err != nil {
			return nil, err
		}
		selected, err := formChoiceSet(get(input), domain)
		if err != nil {
			return nil, err
		}
		indices := []int{}
		for index, value := range values {
			if selected[value] {
				indices = append(indices, index)
			}
		}
		return indices, nil
	}, func(input *T, indices []int, values []string) {
		selected, _ := SelectedOptionValues(values, indices)
		set(input, selected)
	}, false, options...)
}

// BindMultipleOptions controls selected native option nodes by zero-based index.
// Values may repeat, including empty strings. Indices must be unique and in
// range; order is membership-only, while adopted selections use DOM order.
// Options, order, values and node identities must remain fixed until cleanup.
// Disabled option/optgroup successful-control filtering is left to native forms.
func BindMultipleOptions[T any](scope *Scope, control Element, model *Form[T], name string, get func(T) []int, set func(*T, []int), options ...FieldBindingOptions) error {
	if get == nil || set == nil {
		return fmt.Errorf("nextgo: multiple binding requires getter and setter")
	}
	return bindMultipleOptions(scope, control, model, name, func(input T, _ []string) ([]int, error) {
		return get(input), nil
	}, func(input *T, indices []int, _ []string) { set(input, slices.Clone(indices)) }, true, options...)
}

func bindMultipleOptions[T any](scope *Scope, control Element, model *Form[T], name string, get func(T, []string) ([]int, error), set func(*T, []int, []string), repeatedValues bool, options ...FieldBindingOptions) (err error) {
	var binding *Scope
	defer formBindingGuard(&binding, &err)
	if get == nil || set == nil || !ownedFormElement(scope, control) || control.Value.Get("tagName").String() != "SELECT" || !control.Value.Get("multiple").Bool() {
		return fmt.Errorf("nextgo: multiple binding requires a multiple-select")
	}
	opts, err := fieldOptions(scope, options)
	if err != nil {
		return err
	}
	nodes := control.Value.Get("options")
	if nodes.Length() == 0 || nodes.Length() > 256 {
		return fmt.Errorf("nextgo: multiple-select option limit exceeded")
	}
	values := make([]string, nodes.Length())
	elements := make([]js.Value, nodes.Length())
	defaults := []int{}
	for i := range values {
		elements[i] = nodes.Index(i)
		values[i] = elements[i].Get("value").String()
		if elements[i].Get("defaultSelected").Bool() {
			defaults = append(defaults, i)
		}
	}
	if err := formOptionValues(values); err != nil {
		return err
	}
	if !repeatedValues {
		if _, err := formChoiceDomain(values, true); err != nil {
			return err
		}
	}
	check := func() error {
		if !ownedFormElement(scope, control) || control.Value.Get("name").String() != name || !control.Value.Get("multiple").Bool() {
			return fmt.Errorf("nextgo: multiple-select shape changed")
		}
		current := control.Value.Get("options")
		if current.Length() != len(elements) {
			return fmt.Errorf("nextgo: multiple-select options changed")
		}
		for i, option := range elements {
			if !option.Equal(current.Index(i)) || option.Get("value").String() != values[i] {
				return fmt.Errorf("nextgo: multiple-select options changed")
			}
		}
		return nil
	}
	snapshot, err := model.Snapshot()
	if err != nil {
		return err
	}
	initial, err := model.Initial()
	if err != nil {
		return err
	}
	matchingDefault := false
	for _, input := range []T{initial, snapshot.Baseline, snapshot.Values} {
		selected, err := get(input, values)
		if err != nil {
			return err
		}
		if _, err := formOptionSelection(selected, len(values)); err != nil {
			return err
		}
		matchingDefault = matchingDefault || sameFormOptions(defaults, selected)
	}
	if !matchingDefault {
		return fmt.Errorf("nextgo: multiple-select SSR default differs from form values")
	}
	var field *formDOMField[T]
	binding, field, err = registerFormField(scope, control, model, name)
	if err != nil {
		return err
	}
	field.check = check
	saveFieldUI(binding, control, opts.Error)
	read := func() ([]int, error) {
		if !binding.active() {
			return nil, ErrFormClosed
		}
		if err := check(); err != nil {
			return nil, err
		}
		selected := []int{}
		for i, option := range elements {
			if option.Get("selected").Bool() {
				selected = append(selected, i)
			}
		}
		return selected, nil
	}
	field.capture = func() (formFieldPatch[T], error) {
		selected, err := read()
		if err != nil {
			return formFieldPatch[T]{}, err
		}
		return formFieldPatch[T]{apply: func(input *T) { set(input, slices.Clone(selected), values) }, matches: func(input T) bool {
			current, err := get(input, values)
			if err != nil {
				return false
			}
			return sameFormOptions(current, selected)
		}}, nil
	}
	sync := func() error { return model.captureDOMFields(false) }
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
		if err := check(); err != nil {
			report(err.Error())
			return
		}
		indices, err := get(state.Values, values)
		if err != nil {
			report(err.Error())
			return
		}
		selected, err := formOptionSelection(indices, len(values))
		if err != nil {
			report(err.Error())
			return
		}
		for i, option := range elements {
			want := selected[i]
			if option.Get("selected").Bool() != want {
				option.Set("selected", want)
			}
		}
		showFormFieldError(control, opts.Error, name, state)
	})
	if err != nil {
		return err
	}
	binding.Cleanup(stop)
	return nil
}
