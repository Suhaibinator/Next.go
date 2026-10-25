//go:build js && wasm

package refreshcounter

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/examples/refresh/shared"
)

// Development fixtures edit these actual compiled Go constants, never the DOM
// or response bytes. State and DOM schemas remain explicit in component.json.
const clientProof = "v1"
const incrementStep = 1

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	if err := scope.RefreshInstance(p.Instance); err != nil {
		return err
	}
	root, err := scope.Require("[data-refresh-instance]")
	if err != nil {
		return err
	}
	count, err := scope.Require("[data-refresh-count]")
	if err != nil {
		return err
	}
	label, err := scope.Require("[data-refresh-label]")
	if err != nil {
		return err
	}
	title, err := scope.Require("[data-refresh-title]")
	if err != nil {
		return err
	}
	enabled, err := scope.Require("[data-refresh-enabled]")
	if err != nil {
		return err
	}
	increment, err := scope.Require("[data-refresh-increment]")
	if err != nil {
		return err
	}
	proof, err := scope.Require("[data-refresh-proof]")
	if err != nil {
		return err
	}
	sharedNode, err := scope.Require("[data-refresh-shared]")
	if err != nil {
		return err
	}
	countState, err := client.NewRefreshState(scope, "count", p.Initial)
	if err != nil {
		return err
	}
	// The retained DOM already represents restored state on a module replacement.
	// Initial hydration still validates the server's original numeric output.
	if count.Value.Get("textContent").String() != strconv.Itoa(countState.Get()) {
		return fmt.Errorf("counter markup differs from public state")
	}
	liveLabel := label.Value.Get("value").String()
	labelState, err := client.NewRefreshState(scope, "label", liveLabel)
	if err != nil {
		return err
	}
	if labelState.Get() != liveLabel {
		return fmt.Errorf("label markup differs from public state")
	}
	liveEnabled := enabled.Value.Get("checked").Bool()
	enabledState, err := client.NewRefreshState(scope, "enabled", liveEnabled)
	if err != nil {
		return err
	}
	if enabledState.Get() != liveEnabled {
		return fmt.Errorf("toggle markup differs from public state")
	}
	scope.Cleanup(countState.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	scope.Cleanup(labelState.Subscribe(func(value string) {
		title.Text(value)
		if label.Value.Get("value").String() != value {
			label.Property("value", value)
		}
	}))
	scope.Cleanup(enabledState.Subscribe(func(value bool) {
		if enabled.Value.Get("checked").Bool() != value {
			enabled.Property("checked", value)
		}
		increment.Property("disabled", !value)
	}))
	scope.On(label, "input", func(event client.Event) { labelState.Set(event.InputValue()) })
	scope.On(enabled, "change", func(client.Event) { enabledState.Set(enabled.Value.Get("checked").Bool()) })
	scope.On(increment, "click", func(client.Event) {
		if enabledState.Get() {
			countState.Set(countState.Get() + incrementStep)
		}
	})
	proof.Text(clientProof)
	sharedNode.Text(shared.Message)
	root.Attribute("data-refresh-closed", "false")

	// The only asynchronous worker uses the framework's managed Fetch transport.
	// An in-flight physical request makes development replacement fall back to a
	// document reload; arbitrary workers and resource stores are not serialized.
	if p.WorkURL != "" {
		work, err := scope.Require("[data-refresh-work]")
		if err != nil {
			return err
		}
		workStatus, err := scope.Require("[data-refresh-work-status]")
		if err != nil {
			return err
		}
		closed := false
		scope.Cleanup(func() { closed = true })
		scope.On(work, "click", func(client.Event) {
			work.Property("disabled", true)
			workStatus.Attribute("data-state", "loading")
			workStatus.Text("Loading public work…")
			if err := scope.Go(func(ctx context.Context) {
				response, err := client.Fetch(ctx, p.WorkURL, client.FetchOptions{MaxBytes: 2048, Cache: "no-store", Redirect: "error"})
				if ctx.Err() != nil || closed {
					return
				}
				if err != nil || response.StatusCode != 200 {
					workStatus.Attribute("data-state", "failed")
					workStatus.Text("Public work unavailable.")
				} else {
					workStatus.Attribute("data-state", "ready")
					workStatus.Text("Public work complete.")
				}
				work.Property("disabled", false)
			}); err != nil {
				workStatus.Attribute("data-state", "failed")
				workStatus.Text("Public work unavailable.")
				work.Property("disabled", false)
			}
		})
		work.Property("disabled", false)
	}
	scope.Cleanup(func() { root.Attribute("data-refresh-closed", "true") })
	return nil
}
