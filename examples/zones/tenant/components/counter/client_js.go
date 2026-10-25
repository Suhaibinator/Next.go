//go:build js && wasm

package counter

import (
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"strconv"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	count, err := scope.Require("[data-zone-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.Itoa(p.Initial) {
		return fmt.Errorf("counter markup mismatch")
	}
	button, err := scope.Require("[data-zone-increment]")
	if err != nil {
		return err
	}
	state := client.NewState(p.Initial)
	scope.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	scope.On(button, "click", func(client.Event) { state.Set(state.Get() + 100) })
	proof, err := scope.Require("[data-zone-client]")
	if err != nil {
		return err
	}
	proof.Text("tenant")
	button.Property("disabled", false)
	return nil
}
