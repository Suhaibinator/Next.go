//go:build js && wasm

package actionviewlazy

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/examples/demo/actionrefs"
	"github.com/Suhaibinator/Next.go/examples/demo/components/actionview"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	count, err := scope.Require("[data-av-lazy-count]")
	if err != nil {
		return err
	}
	revision, err := scope.Require("[data-av-lazy-revision]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.FormatInt(props.Count, 10) || revision.Value.Get("textContent").String() != strconv.FormatInt(props.Revision, 10) {
		return fmt.Errorf("lazy action view does not match server props")
	}
	button, err := scope.Require("[data-av-lazy-save]")
	if err != nil {
		return err
	}
	status, err := scope.Require("[data-av-lazy-status]")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	closed := false
	scope.Cleanup(cancel)
	scope.Cleanup(func() { closed = true; scope.Root.Attribute("data-av-lazy-closed", "true") })
	busy := false
	scope.On(button, "click", func(client.Event) {
		if busy {
			return
		}
		busy = true
		button.Property("disabled", true)
		status.Text("Saving through the core document runtime…")
		go func() {
			response, callErr := actionrefs.ViewSaveAndRefresh(ctx, actionrefs.ViewSaveInput{Amount: 1})
			if callErr == nil {
				// Its source island can already be disposed. This public receipt
				// joins the eager live subscriber, without touching detached DOM.
				receipt := actionview.Receipt{Confirmed: true, Count: response.Data.Count, Revision: response.Data.Revision, Applied: response.View.Applied, Status: response.View.Status, ViewError: response.View.Err != nil, Mode: "lazy"}
				encoded, err := json.Marshal(receipt)
				if err == nil {
					event := js.Global().Get("CustomEvent").New(actionview.ReceiptEvent, map[string]any{"detail": string(encoded)})
					js.Global().Get("document").Call("dispatchEvent", event)
				}
			}
			if closed {
				return
			}
			busy = false
			button.Property("disabled", false)
			if callErr != nil {
				status.Text("Save outcome is unknown. Refresh to reconcile before retrying.")
			} else if response.View.Err != nil {
				status.Text("Saved; the view could not finish. Refresh without repeating the action.")
			} else {
				status.Text("Saved and refreshed through core Go.")
			}
		}()
	})
	button.Property("disabled", false)
	status.Text("Lazy Go caller ready.")
	return nil
}
