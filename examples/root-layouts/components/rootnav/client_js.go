//go:build js && wasm

package rootnav

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/client"
)

func Mount(s *client.Scope, raw json.RawMessage) error {
	if _, err := client.DecodeProps[Props](raw); err != nil {
		return err
	}
	out, err := s.Require("[data-root-error]")
	if err != nil {
		return err
	}
	for _, mode := range []struct {
		selector string
		replace  bool
	}{{"[data-root-push]", false}, {"[data-root-replace]", true}} {
		button, err := s.Require(mode.selector)
		if err != nil {
			return err
		}
		replace := mode.replace
		s.On(button, "click", func(client.Event) {
			var err error
			if replace {
				err = client.Replace(client.MustURL("/dashboard"))
			} else {
				err = client.Navigate(client.MustURL("/dashboard"))
			}
			if err != nil {
				out.Text(err.Error())
			}
		})
		button.Property("disabled", false)
	}
	return nil
}
