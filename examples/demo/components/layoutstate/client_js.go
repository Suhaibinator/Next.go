//go:build js && wasm

package layoutstate

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
	count, err := s.Require("[data-layout-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.Itoa(p.Initial) {
		return fmt.Errorf("layout counter markup mismatch")
	}
	button, err := s.Require("[data-layout-increment]")
	if err != nil {
		return err
	}
	value := client.NewState(p.Initial)
	s.Cleanup(value.Subscribe(func(n int) { count.Text(strconv.Itoa(n)) }))
	s.On(button, "click", func(client.Event) { value.Set(value.Get() + 1) })
	button.Property("disabled", false)
	location, err := s.Require("[data-route-location]")
	if err != nil {
		return err
	}
	_, err = s.ObserveLocation(func(value client.Location) {
		location.Text(value.Pathname + value.Search + value.Hash)
	})
	if err != nil {
		return err
	}
	return nil
}
