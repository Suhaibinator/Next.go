//go:build js && wasm

package settings

import (
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
)

func Mount(s *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	message, err := s.Require("[data-setting-message]")
	if err != nil {
		return err
	}
	value, ok := client.PublicValue("demo.message")
	if !ok || value != p.Message || message.Value.Get("textContent").String() != p.Message {
		return fmt.Errorf("public configuration differs from server markup")
	}
	button, err := s.Require("[data-setting-read]")
	if err != nil {
		return err
	}
	result, err := s.Require("[data-setting-result]")
	if err != nil {
		return err
	}
	s.On(button, "click", func(client.Event) { value, _ := client.PublicValue("demo.message"); result.Text(value) })
	button.Property("disabled", false)
	return nil
}
