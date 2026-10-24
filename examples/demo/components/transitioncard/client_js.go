//go:build js && wasm

package transitioncard

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Suhaibinator/Next.go/client"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	if _, err := client.DecodeProps[Props](raw); err != nil {
		return err
	}
	count, err := scope.Require("[data-transition-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != "0" {
		return fmt.Errorf("transition count does not match server markup")
	}
	button, err := scope.Require("[data-transition-increment]")
	if err != nil {
		return err
	}
	navigate, err := scope.Require("[data-transition-navigate]")
	if err != nil {
		return err
	}
	message, err := scope.Require("[data-transition-error]")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	scope.Cleanup(cancel)
	state := client.NewState(0)
	scope.Cleanup(state.Subscribe(func(n int) { count.Text(strconv.Itoa(n)) }))
	scope.On(button, "click", func(client.Event) {
		go func() {
			if err := client.Transition(ctx, client.TransitionOptions{Types: []string{"counter"}}, func() error {
				state.Set(state.Get() + 1)
				return nil
			}); err != nil && ctx.Err() == nil {
				message.Text(err.Error())
			}
		}()
	})
	scope.On(navigate, "click", func(client.Event) {
		if err := client.NavigateWithOptions(client.MustURL("/about"), client.NavigationOptions{Transition: client.TransitionOptions{Disabled: true}}); err != nil {
			message.Text(err.Error())
		}
	})
	button.Property("disabled", false)
	navigate.Property("disabled", false)
	return nil
}
