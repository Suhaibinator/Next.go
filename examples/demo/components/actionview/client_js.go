//go:build js && wasm

package actionview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/examples/demo/actionrefs"
	"github.com/Suhaibinator/Next.go/mutation"
)

// The example deliberately retains only its last public receipt. Its page
// subscriptions are scoped: replacing the originating island removes its
// subscriber before the new island subscribes to this same Go state.
var receipts = client.NewState(Receipt{})

func publish(receipt Receipt) {
	if !receipt.Confirmed || receipt.Count < 0 || receipt.Count > 1_000_000 || receipt.Revision < 1 || receipt.Revision > 1_000_000 || receipt.Status < 0 || receipt.Status > 599 || len(receipt.Mode) > 32 {
		return
	}
	if current := receipts.Get(); current.Confirmed && current.Revision > receipt.Revision {
		return
	}
	receipts.Set(receipt)
}

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	nodes := map[string]client.Element{}
	for _, key := range []string{"count", "revision", "amount", "error", "status", "json", "multipart", "only", "invalid", "fail", "viewfail", "refresh", "receipt", "confirmed-count", "confirmed-revision"} {
		node, err := scope.Require("[data-av-" + key + "]")
		if err != nil {
			return err
		}
		nodes[key] = node
	}
	if nodes["count"].Value.Get("textContent").String() != strconv.FormatInt(props.Count, 10) || nodes["revision"].Value.Get("textContent").String() != strconv.FormatInt(props.Revision, 10) {
		return fmt.Errorf("action view state does not match server props")
	}
	// This public code comes only from the typed mutation decoder. Keep it
	// alongside the displayed status so observers never need a second body read.
	setStatus := func(message, publicErrorCode string) {
		nodes["status"].Attribute("data-av-error-code", publicErrorCode)
		nodes["status"].Text(message)
	}
	nodes["status"].Attribute("data-av-error-code", "")
	ctx, cancel := context.WithCancel(context.Background())
	closed := false
	scope.Cleanup(cancel)
	scope.Cleanup(func() { closed = true; scope.Root.Attribute("data-av-closed", "true") })
	scope.Cleanup(receipts.Subscribe(func(receipt Receipt) {
		if closed || !receipt.Confirmed || receipt.Revision < props.Revision {
			return
		}
		nodes["receipt"].Attribute("data-confirmed", "true")
		nodes["receipt"].Attribute("data-applied", strconv.FormatBool(receipt.Applied))
		nodes["receipt"].Attribute("data-view-status", strconv.Itoa(receipt.Status))
		nodes["receipt"].Attribute("data-view-error", strconv.FormatBool(receipt.ViewError))
		nodes["confirmed-count"].Text(strconv.FormatInt(receipt.Count, 10))
		nodes["confirmed-revision"].Text(strconv.FormatInt(receipt.Revision, 10))
		nodes["count"].Text(strconv.FormatInt(receipt.Count, 10))
		nodes["revision"].Text(strconv.FormatInt(receipt.Revision, 10))
		message := "Saved and refreshed streamed view."
		if receipt.Mode == "only" {
			message = "Saved without refreshing view."
		} else if receipt.ViewError || !receipt.Applied {
			message = "Saved; refreshed view could not finish. Refresh without repeating the action."
		}
		setStatus(message, "")
	}))
	// A lazy Go executable has its own package globals. Its bounded public
	// receipt crosses the document bridge as data, then joins this eager state.
	scope.On(client.Element{Value: js.Global().Get("document")}, ReceiptEvent, func(event client.Event) {
		detail := event.Value.Get("detail")
		if detail.Type() != js.TypeString || len(detail.String()) > 1024 {
			return
		}
		var receipt Receipt
		if json.Unmarshal([]byte(detail.String()), &receipt) != nil {
			return
		}
		if receipt.Applied && receipt.Revision != props.Revision {
			return
		}
		publish(receipt)
	})

	combinedBusy, ordinaryBusy := false, false
	setBusy := func() {
		for _, key := range []string{"json", "multipart", "invalid", "fail", "viewfail"} {
			nodes[key].Property("disabled", combinedBusy)
		}
		nodes["only"].Property("disabled", ordinaryBusy)
		nodes["refresh"].Property("disabled", false)
		scope.Root.Attribute("aria-busy", strconv.FormatBool(combinedBusy || ordinaryBusy))
	}
	start := func(mode string) {
		if mode == "only" && ordinaryBusy || mode != "only" && combinedBusy {
			return
		}
		nodes["status"].Attribute("data-av-error-code", "")
		amount, err := strconv.ParseInt(nodes["amount"].Value.Get("value").String(), 10, 64)
		if err != nil {
			nodes["error"].Text("Enter a whole number from 1 to 5.")
			return
		}
		input := actionrefs.ViewSaveInput{Amount: amount, Fail: mode == "fail", ViewFail: mode == "viewfail"}
		if mode == "invalid" {
			input.Amount = 0
		}
		if mode == "only" {
			ordinaryBusy = true
		} else {
			combinedBusy = true
		}
		nodes["error"].Text("")
		setStatus("Saving; waiting for confirmed data and the view…", "")
		setBusy()
		go func() {
			var receipt Receipt
			var callErr error
			if mode == "only" {
				var response mutation.Response[actionrefs.ViewSaveOutput]
				response, callErr = actionrefs.ViewSave(ctx, input)
				receipt = Receipt{Confirmed: callErr == nil, Count: response.Data.Count, Revision: response.Data.Revision, Mode: mode}
			} else {
				var response mutation.RefreshResponse[actionrefs.ViewSaveOutput]
				if mode == "multipart" {
					var form *client.Multipart
					form, callErr = client.NewMultipart(4096)
					if callErr == nil {
						defer form.Close()
						callErr = form.AddField("amount", strconv.FormatInt(amount, 10))
						if callErr == nil {
							response, callErr = actionrefs.ViewSaveMultipartAndRefresh(ctx, form)
						}
					}
				} else {
					response, callErr = actionrefs.ViewSaveAndRefresh(ctx, input)
				}
				receipt = Receipt{Confirmed: callErr == nil, Count: response.Data.Count, Revision: response.Data.Revision, Applied: response.View.Applied, Status: response.View.Status, ViewError: response.View.Err != nil, Mode: mode}
			}
			// Confirmation is independent of source-scope cancellation caused by
			// this request's own shell replacement. Publish even after unmount;
			// only the new live subscriber receives the receipt.
			if callErr == nil {
				publish(receipt)
			}
			if closed {
				return
			}
			if mode == "only" {
				ordinaryBusy = false
			} else {
				combinedBusy = false
			}
			setBusy()
			if callErr != nil {
				message := "Save outcome is unknown. Refresh to reconcile before retrying."
				publicErrorCode := ""
				var public *mutation.Error
				if errors.As(callErr, &public) {
					message = public.Message
					publicErrorCode = public.Code
					nodes["error"].Text(public.Fields["amount"])
				}
				setStatus(message, publicErrorCode)
			}
		}()
	}
	for _, mode := range []string{"json", "multipart", "only", "invalid", "fail", "viewfail"} {
		scope.On(nodes[mode], "click", func(client.Event) { start(mode) })
	}
	scope.On(nodes["refresh"], "click", func(client.Event) { client.Refresh() })
	setBusy()
	return nil
}
