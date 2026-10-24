//go:build js && wasm

package counter

import (
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	"strconv"
	"workspace.example/shared"
)

func Mount(s *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	if props.Label != shared.Label || props.Step != shared.Step {
		return fmt.Errorf("workspace server/browser dependency versions differ")
	}
	count, err := s.Require("[data-workspace-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != "0" {
		return fmt.Errorf("workspace markup differs")
	}
	button, err := s.Require("[data-workspace-increment]")
	if err != nil {
		return err
	}
	state := client.NewState(0)
	s.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	s.On(button, "click", func(client.Event) { state.Set(state.Get() + shared.Step) })
	button.Property("disabled", false)
	return nil
}
