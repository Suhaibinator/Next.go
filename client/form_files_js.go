//go:build js && wasm

package client

import (
	"errors"
	"fmt"
	"slices"
	"syscall/js"
)

func selectedFiles(control Element) ([]File, error) {
	list := control.Value.Get("files")
	if list.IsNull() || list.IsUndefined() || list.Length() > 64 {
		return nil, fmt.Errorf("nextgo: file selection requires at most 64 files")
	}
	files := make([]File, 0, list.Length())
	for i := 0; i < list.Length(); i++ {
		file, err := browserFile(list.Index(i))
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}
func checkedFiles(files []File, multiple bool) error {
	if len(files) > 64 || (!multiple && len(files) > 1) {
		return fmt.Errorf("nextgo: invalid file selection size")
	}
	for _, file := range files {
		actual, err := browserFile(file.value)
		if err != nil || actual.info != file.info {
			return ErrFileRead
		}
	}
	return nil
}

// BindFiles controls an existing file input with immutable File handles. Initial
// SSR selection must be empty; live pre-WASM choices are adopted. Model updates
// can clear or restore previously selected handles through native DataTransfer.
// Supply a Clone for the slice and an Equal using SameFiles in FormOptions.
func BindFiles[T any](scope *Scope, control Element, model *Form[T], name string, get func(T) []File, set func(*T, []File), options ...FieldBindingOptions) (err error) {
	var binding *Scope
	defer formBindingGuard(&binding, &err)
	if get == nil || set == nil || !ownedFormElement(scope, control) || control.Value.Get("tagName").String() != "INPUT" || control.Value.Get("type").String() != "file" || control.Value.Get("defaultValue").String() != "" {
		return fmt.Errorf("nextgo: files binding requires an empty SSR file input")
	}
	opts, err := fieldOptions(scope, options)
	if err != nil {
		return err
	}
	multiple := control.Value.Get("multiple").Bool()
	initial, err := model.Initial()
	if err != nil {
		return err
	}
	if len(get(initial)) != 0 {
		return fmt.Errorf("nextgo: file selections cannot be server rendered")
	}
	snapshot, err := model.Snapshot()
	if err != nil {
		return err
	}
	for _, files := range [][]File{get(snapshot.Values), get(snapshot.Baseline)} {
		if err := checkedFiles(files, multiple); err != nil {
			return err
		}
	}
	check := func() error {
		if !ownedFormElement(scope, control) || control.Value.Get("name").String() != name || control.Value.Get("type").String() != "file" || control.Value.Get("multiple").Bool() != multiple {
			return fmt.Errorf("nextgo: file input shape changed")
		}
		_, err := selectedFiles(control)
		return err
	}
	if err := check(); err != nil {
		return err
	}
	var field *formDOMField[T]
	binding, field, err = registerFormField(scope, control, model, name)
	if err != nil {
		return err
	}
	field.check = check
	saveFieldUI(binding, control, opts.Error)
	field.capture = func() (formFieldPatch[T], error) {
		if !binding.active() {
			return formFieldPatch[T]{}, ErrFormClosed
		}
		if err := check(); err != nil {
			return formFieldPatch[T]{}, err
		}
		files, err := selectedFiles(control)
		if err != nil {
			return formFieldPatch[T]{}, err
		}
		return formFieldPatch[T]{matches: func(input T) bool { return SameFiles(get(input), files) }, apply: func(input *T) { set(input, slices.Clone(files)) }}, nil
	}
	if err := model.captureDOMFields(false); err != nil {
		return err
	}
	binding.On(control, "change", func(Event) {
		if err := model.captureDOMFields(false); err != nil && !errors.Is(err, ErrFormClosed) {
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
		desired := get(state.Values)
		if err := checkedFiles(desired, multiple); err != nil {
			report(err.Error())
			return
		}
		current, err := selectedFiles(control)
		if err != nil {
			report(err.Error())
			return
		}
		if !SameFiles(desired, current) {
			if len(desired) == 0 {
				control.Property("value", "")
			} else {
				constructor := js.Global().Get("DataTransfer")
				if constructor.Type() != js.TypeFunction {
					report("nextgo: restoring files requires DataTransfer")
					return
				}
				transfer := constructor.New()
				for _, file := range desired {
					transfer.Get("items").Call("add", file.value)
				}
				control.Property("files", transfer.Get("files"))
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
