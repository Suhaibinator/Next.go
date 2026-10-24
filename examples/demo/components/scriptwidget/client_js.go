//go:build js && wasm

package scriptwidget

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	"strconv"
	"syscall/js"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	result, err := scope.Require("[data-script-result]")
	if err != nil {
		return err
	}
	count, err := scope.Require("[data-script-count]")
	if err != nil {
		return err
	}
	button, err := scope.Require("[data-script-increment]")
	if err != nil {
		return err
	}
	retry, err := scope.Require("[data-script-retry]")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	scope.Cleanup(cancel)
	state := client.NewState(0)
	scope.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	scope.On(button, "click", func(client.Event) { state.Set(state.Get() + 1) })
	ready := func(err error) {
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			result.Text("Library failed: " + err.Error())
			retry.Property("hidden", false)
			return
		}
		library := js.Global().Get(p.Global)
		if !library.Truthy() {
			result.Text("Library API is unavailable")
			return
		}
		result.Text("Ready: " + library.Get("version").String())
		retry.Property("hidden", true)
		button.Property("disabled", false)
	}
	scope.On(retry, "click", func(client.Event) {
		retry.Property("hidden", true)
		result.Text("Retrying trusted library")
		go func() { ready(client.RetryScript(ctx, p.Dependency)) }()
	})
	if p.Global == "" {
		return fmt.Errorf("script widget requires a library name")
	}
	return scope.Script(p.Dependency, ready)
}
