//go:build js && wasm

package clientpanel

import (
	"encoding/json"
	"fmt"
	"strconv"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/ui"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	if p.Failure != "" && p.Failure != "error" && p.Failure != "panic" {
		return fmt.Errorf("invalid client panel demonstration mode")
	}
	view, err := scope.ClientView(ui.El("section", ui.Attrs{"class": "counter", "aria-label": p.Label, "data-client-panel": ""},
		ui.El("h2", nil, ui.Text(p.Label)),
		ui.El("p", nil, ui.Text("This view was created by Go in the browser. Browser language: "+js.Global().Get("navigator").Get("language").String())),
		ui.El("output", ui.Attrs{"aria-live": "polite", "data-client-panel-count": ""}, ui.Text(strconv.Itoa(p.Initial))),
		ui.El("button", ui.Attrs{"type": "button", "data-client-panel-increment": ""}, ui.Text("Increase "+p.Label))))
	if err != nil {
		return err
	}
	count, err := view.Require("[data-client-panel-count]")
	if err != nil {
		return err
	}
	button, err := view.Require("[data-client-panel-increment]")
	if err != nil {
		return err
	}
	state := client.NewState(p.Initial)
	view.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	view.On(button, "click", func(client.Event) { state.Set(state.Get() + 1) })
	// These optional, public demonstration modes deliberately fail after setup.
	// The framework reports the failure, removes the new listener, and restores
	// the exact original fallback nodes. They are not the page's default mode.
	switch p.Failure {
	case "error":
		return fmt.Errorf("client panel demonstration rejected its mount")
	case "panic":
		panic("client panel demonstration mount panic")
	}
	return nil
}
