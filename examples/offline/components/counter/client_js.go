//go:build js && wasm

package counter

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Suhaibinator/Next.go/client"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	count, err := scope.Require("[data-offline-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.Itoa(p.Initial) {
		return fmt.Errorf("saved counter markup does not match its public props")
	}
	button, err := scope.Require("[data-offline-increment]")
	if err != nil {
		return err
	}
	proof, err := scope.Require("[data-offline-counter-proof]")
	if err != nil {
		return err
	}
	state := client.NewState(p.Initial)
	scope.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	scope.On(button, "click", func(client.Event) { state.Set(state.Get() + 1) })
	proof.Text("Hydrated by lazy Go")
	button.Property("disabled", false)
	return nil
}
