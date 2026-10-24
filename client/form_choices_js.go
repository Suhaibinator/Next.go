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
func BindRadioGroup[T any](scope *Scope, controls []Element, model *Form[T], name string, get func(T) string, set func(*T, string), options ...FieldBindingOptions) (err error) {
	var binding *Scope
	defer formBindingGuard(&binding, &err)
	if get == nil || set == nil || len(controls) == 0 || len(controls) > 64 {
		return fmt.Errorf("nextgo: radio binding requires 1 to 64 controls")
	}
	controls = slices.Clone(controls)
	values := make([]string, len(controls))
	var owner, tree js.Value
	defaults, live := "", ""
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
			if defaults != "" {
				return fmt.Errorf("nextgo: multiple SSR radio defaults")
			}
			defaults = values[i]
		}
		if control.Value.Get("checked").Bool() {
			if live != "" {
				return fmt.Errorf("nextgo: multiple selected radios")
			}
			live = values[i]
		}
	}
	domain, err := formChoiceDomain(values, false)
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
	for _, value := range []string{get(initial), get(snapshot.Baseline), get(snapshot.Values)} {
		if value != "" && !domain[value] {
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
	read := func() (string, error) {
		if !binding.active() {
			return "", ErrFormClosed
		}
		if err := check(); err != nil {
			return "", err
		}
		value := ""
		for i, control := range controls {
			if control.Value.Get("checked").Bool() {
				if value != "" {
					return "", fmt.Errorf("nextgo: multiple selected radios")
				}
				value = values[i]
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
		if value != "" && !domain[value] {
			report("nextgo: model contains unknown radio choice")
			return
		}
		for i, control := range controls {
			want := values[i] == value
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
func BindMultiple[T any](scope *Scope, control Element, model *Form[T], name string, get func(T) []string, set func(*T, []string), options ...FieldBindingOptions) (err error) {
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
	defaults := []string{}
	for i := range values {
		elements[i] = nodes.Index(i)
		values[i] = elements[i].Get("value").String()
		if elements[i].Get("defaultSelected").Bool() {
			defaults = append(defaults, values[i])
		}
	}
	domain, err := formChoiceDomain(values, true)
	if err != nil {
		return err
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
	for _, selected := range [][]string{get(initial), get(snapshot.Baseline), get(snapshot.Values)} {
		if _, err := formChoiceSet(selected, domain); err != nil {
			return err
		}
	}
	if !sameFormChoices(defaults, get(initial)) && !sameFormChoices(defaults, get(snapshot.Baseline)) && !sameFormChoices(defaults, get(snapshot.Values)) {
		return fmt.Errorf("nextgo: multiple-select SSR default differs from form values")
	}
	var field *formDOMField[T]
	binding, field, err = registerFormField(scope, control, model, name)
	if err != nil {
		return err
	}
	field.check = check
	saveFieldUI(binding, control, opts.Error)
	read := func() ([]string, error) {
		if !binding.active() {
			return nil, ErrFormClosed
		}
		if err := check(); err != nil {
			return nil, err
		}
		selected := []string{}
		for i, option := range elements {
			if option.Get("selected").Bool() {
				selected = append(selected, values[i])
			}
		}
		return selected, nil
	}
	field.capture = func() (formFieldPatch[T], error) {
		selected, err := read()
		if err != nil {
			return formFieldPatch[T]{}, err
		}
		return formFieldPatch[T]{apply: func(input *T) { set(input, slices.Clone(selected)) }, matches: func(input T) bool { return sameFormChoices(get(input), selected) }}, nil
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
		selected, err := formChoiceSet(get(state.Values), domain)
		if err != nil {
			report(err.Error())
			return
		}
		for i, option := range elements {
			want := selected[values[i]]
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
