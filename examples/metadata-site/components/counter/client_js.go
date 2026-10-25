//go:build js && wasm

package counter

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
)

func Mount(s *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	count, err := s.Require("[data-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.Itoa(p.Initial) {
		return fmt.Errorf("counter markup does not match props")
	}
	button, err := s.Require("[data-increment]")
	if err != nil {
		return err
	}
	state := client.NewState(p.Initial)
	s.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	s.On(button, "click", func(client.Event) { state.Set(state.Get() + 1) })
	button.Property("disabled", false)
	return nil
}
