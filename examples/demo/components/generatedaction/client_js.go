//go:build js && wasm

package generatedaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/examples/demo/actionrefs"
	"github.com/Suhaibinator/Next.go/mutation"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	nodes := map[string]client.Element{}
	for _, key := range []string{"count", "revision", "amount", "error", "status", "json", "multipart", "fail", "refresh"} {
		node, err := scope.Require("[data-generated-" + key + "]")
		if err != nil {
			return err
		}
		nodes[key] = node
	}
	if nodes["count"].Value.Get("textContent").String() != strconv.FormatInt(props.Count, 10) || nodes["revision"].Value.Get("textContent").String() != strconv.FormatInt(props.Revision, 10) {
		return fmt.Errorf("generated action state does not match server props")
	}
	ctx, cancel := context.WithCancel(context.Background())
	scope.Cleanup(cancel)
	scope.Cleanup(func() { scope.Root.Attribute("data-generated-closed", "true") })
	busy := false
	controls := []string{"json", "multipart", "fail", "refresh"}
	setBusy := func(value bool) {
		busy = value
		for _, key := range controls {
			nodes[key].Property("disabled", value)
		}
		scope.Root.Attribute("aria-busy", strconv.FormatBool(value))
	}
	start := func(mode string) {
		if busy {
			return
		}
		amount, err := strconv.ParseInt(nodes["amount"].Value.Get("value").String(), 10, 64)
		if err != nil {
			nodes["error"].Text("Enter a whole number from 1 to 5.")
			return
		}
		nodes["error"].Text("")
		nodes["status"].Text("Saving…")
		setBusy(true)
		go func() {
			input := actionrefs.GeneratedAddInput{Amount: amount, Fail: mode == "fail"}
			var response mutation.Response[actionrefs.GeneratedAddOutput]
			var callErr error
			if mode == "multipart" {
				var form *client.Multipart
				form, callErr = client.NewMultipart(4096)
				if callErr == nil {
					defer form.Close()
					callErr = form.AddField("amount", strconv.FormatInt(amount, 10))
					if callErr == nil {
						response, callErr = actionrefs.GeneratedAddMultipart(ctx, form)
					}
				}
			} else {
				response, callErr = actionrefs.GeneratedAdd(ctx, input)
			}
			if ctx.Err() != nil {
				return
			}
			setBusy(false)
			if callErr != nil {
				var public *mutation.Error
				message := "Unable to save change. Refresh before retrying."
				if errors.As(callErr, &public) {
					message = public.Message
					nodes["error"].Text(public.Fields["amount"])
				}
				nodes["status"].Text(message)
				return
			}
			nodes["count"].Text(strconv.FormatInt(response.Data.Count, 10))
			nodes["revision"].Text(strconv.FormatInt(response.Data.Revision, 10))
			nodes["status"].Text("Saved with generated " + mode + " caller.")
		}()
	}
	for _, mode := range []string{"json", "multipart", "fail"} {
		scope.On(nodes[mode], "click", func(client.Event) { start(mode) })
	}
	scope.On(nodes["refresh"], "click", func(client.Event) { client.Refresh() })
	setBusy(false)
	return nil
}
