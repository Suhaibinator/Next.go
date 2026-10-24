//go:build js && wasm

package composition

import (
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	"strconv"
)

var sharedCount = client.NewContext[*client.State[int]]()

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	life, err := scope.Lifetime()
	if err != nil {
		return err
	}
	state := client.NewState(p.Initial)
	if err := client.Provide(life, sharedCount, state); err != nil {
		return err
	}
	status, err := scope.Require("[data-owner-status]")
	if err != nil {
		return err
	}
	increment, err := scope.Require("[data-parent-increment]")
	if err != nil {
		return err
	}
	remove, err := scope.Require("[data-remove-child]")
	if err != nil {
		return err
	}
	fail, err := scope.Require("[data-fail-effect]")
	if err != nil {
		return err
	}
	closed := 0
	scope.Cleanup(func() { scope.Root.Attribute("data-composition-closed", "true") })
	var disposeFirst func()
	var firstRoot client.Element
	failed := p.Fail
	for _, name := range []string{"Inherited", "Sibling", "Shadowed"} {
		root, err := scope.Require(`[data-panel="` + name + `"]`)
		if err != nil {
			return err
		}
		child, dispose, err := scope.Subscope(root)
		if err != nil {
			return err
		}
		childLife, err := child.Lifetime()
		if err != nil {
			return err
		}
		if name == "Shadowed" {
			if err := client.Provide(childLife, sharedCount, client.NewState(100)); err != nil {
				return err
			}
		}
		model, ok := client.UseContext(childLife, sharedCount)
		if !ok {
			return fmt.Errorf("missing shared provider")
		}
		value, err := child.Require("[data-value]")
		if err != nil {
			return err
		}
		button, err := child.Require("[data-increment]")
		if err != nil {
			return err
		}
		effectStatus, err := child.Require("[data-effect]")
		if err != nil {
			return err
		}
		if value.Value.Get("textContent").String() != strconv.Itoa(model.Get()) {
			return fmt.Errorf("component markup differs from provider")
		}
		runs, cleanups := 0, 0
		child.Cleanup(func() {
			closed++
			root.Attribute("data-child-closed", "true")
			scope.Root.Attribute("data-closed-children", strconv.Itoa(closed))
		})
		_, err = childLife.Effect(func() (func(), error) {
			if name == "Inherited" && failed {
				return nil, fmt.Errorf("synthetic component effect failure")
			}
			runs++
			value.Text(strconv.Itoa(model.Get()))
			effectStatus.Text(fmt.Sprintf("Runs %d, cleanups %d", runs, cleanups))
			root.Attribute("data-effect-runs", strconv.Itoa(runs))
			return func() { cleanups++; root.Attribute("data-effect-cleanups", strconv.Itoa(cleanups)) }, nil
		}, model)
		if err != nil {
			return err
		}
		child.On(button, "click", func(client.Event) { model.Set(model.Get() + 1) })
		button.Property("disabled", false)
		if name == "Inherited" {
			disposeFirst = dispose
			firstRoot = root
		}
	}
	// The parent query cannot accidentally bind controls owned by child components.
	if _, err := scope.Require("[data-increment]"); err == nil {
		return fmt.Errorf("child query ownership escaped")
	}
	status.Text("Go components ready")
	scope.On(increment, "click", func(client.Event) { state.Set(state.Get() + 1) })
	scope.On(remove, "click", func(client.Event) {
		disposeFirst()
		firstRoot.Value.Call("remove")
		remove.Property("disabled", true)
		status.Text("Inherited child disposed")
	})
	scope.On(fail, "click", func(client.Event) {
		failed = true
		state.Set(state.Get() + 1)
		status.Text("Inherited effect failed; sibling remains active")
	})
	increment.Property("disabled", false)
	remove.Property("disabled", false)
	fail.Property("disabled", false)
	return nil
}
