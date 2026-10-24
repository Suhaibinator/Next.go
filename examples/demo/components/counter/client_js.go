//go:build js && wasm

package counter

import (
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"strconv"
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
		return fmt.Errorf("counter markup does not match initial props")
	}
	inc, err := s.Require("[data-increment]")
	if err != nil {
		return err
	}
	dec, err := s.Require("[data-decrement]")
	if err != nil {
		return err
	}
	reset, err := s.Require("[data-reset]")
	if err != nil {
		return err
	}
	state := client.NewState(p.Initial)
	s.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	s.On(inc, "click", func(client.Event) { state.Set(state.Get() + 1) })
	s.On(dec, "click", func(client.Event) { state.Set(state.Get() - 1) })
	s.On(reset, "click", func(client.Event) { state.Set(p.Initial) })
	inc.Property("disabled", false)
	dec.Property("disabled", false)
	reset.Property("disabled", false)
	return nil
}
