//go:build js && wasm

package counter

import (
	"encoding/json"
	"fmt"
	"strconv"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/client"
)

func Mount(s *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	output, err := s.Require("[data-count]")
	if err != nil {
		return err
	}
	if output.Value.Get("textContent").String() != strconv.Itoa(p.Initial) {
		return fmt.Errorf("comparison counter SSR/props mismatch")
	}
	button, err := s.Require("[data-increment]")
	if err != nil {
		return err
	}
	section, err := s.Require("[data-comparison-counter]")
	if err != nil {
		return err
	}
	count := client.NewState(p.Initial)
	s.Cleanup(count.Subscribe(func(value int) { output.Text(strconv.Itoa(value)) }))
	s.On(button, "click", func(client.Event) { count.Set(count.Get() + 1) })
	button.Property("disabled", false)
	section.Value.Call("setAttribute", "data-ready", "true")
	js.Global().Get("performance").Call("mark", "comparison:ready")
	return nil
}
