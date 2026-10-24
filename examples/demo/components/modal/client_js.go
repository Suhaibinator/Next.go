//go:build js && wasm

package modal

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	dialog, err := scope.Require("[data-modal]")
	if err != nil {
		return err
	}
	refresh, err := scope.Require("[data-refresh]")
	if err != nil {
		return err
	}
	close, err := scope.Require("[data-close]")
	if err != nil {
		return err
	}
	message, err := scope.Require("[data-modal-error]")
	if err != nil {
		return err
	}
	dismiss := func() {
		if err := client.BackOrReplace(p.ReturnTo); err != nil {
			message.Text("Unable to close: " + err.Error())
			message.Property("hidden", false)
		}
	}
	if err := scope.Modal(dialog, dismiss); err != nil {
		return err
	}
	close.Property("disabled", false)
	refresh.Property("disabled", false)
	scope.On(close, "click", func(client.Event) { dismiss() })
	scope.On(refresh, "click", func(client.Event) { client.Refresh() })
	return nil
}
