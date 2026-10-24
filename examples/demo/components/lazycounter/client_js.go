//go:build js && wasm

package lazycounter

import (
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	"strconv"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	count, err := scope.Require("[data-lazy-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.Itoa(props.Initial) {
		return fmt.Errorf("lazy counter markup differs")
	}
	increment, err := scope.Require("[data-lazy-increment]")
	if err != nil {
		return err
	}
	navigate, err := scope.Require("[data-lazy-navigate]")
	if err != nil {
		return err
	}
	location, err := scope.Require("[data-lazy-location]")
	if err != nil {
		return err
	}
	proof, err := scope.Require("[data-lazy-proof]")
	if err != nil {
		return err
	}
	state := client.NewState(props.Initial)
	scope.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	scope.On(increment, "click", func(client.Event) { state.Set(state.Get() + 1) })
	scope.On(navigate, "click", func(client.Event) {
		if err := client.Navigate(props.Target); err != nil {
			proof.Text(err.Error())
		}
	})
	if _, err := scope.ObserveLocation(func(value client.Location) { location.Text(value.Pathname + value.Search + value.Hash) }); err != nil {
		return err
	}
	if props.Target == "" {
		return fmt.Errorf("lazy counter requires a navigation target")
	}
	proof.Text("lazy-client-only-proof-7d91")
	if props.Dependency != nil {
		if err := scope.Script(*props.Dependency, func(err error) {
			if err != nil {
				proof.Text("Library failed")
			} else {
				proof.Text("Shared library ready")
			}
		}); err != nil {
			return err
		}
	}
	increment.Property("disabled", false)
	navigate.Property("disabled", false)
	return nil
}
