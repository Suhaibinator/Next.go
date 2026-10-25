//go:build js && wasm

package counter

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	"independent.example/alpha/mutations"
	"strconv"
)

const clientProof = "A1"
const incrementStep = 1

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	count, err := scope.Require("[data-independent-count]")
	if err != nil {
		return err
	}
	if count.Value.Get("textContent").String() != strconv.Itoa(p.Initial) {
		return fmt.Errorf("counter markup mismatch")
	}
	increment, err := scope.Require("[data-independent-increment]")
	if err != nil {
		return err
	}
	proof, err := scope.Require("[data-independent-proof]")
	if err != nil {
		return err
	}
	call, err := scope.Require("[data-independent-call]")
	if err != nil {
		return err
	}
	status, err := scope.Require("[data-independent-status]")
	if err != nil {
		return err
	}
	receipt, err := scope.Require("[data-independent-action-result]")
	if err != nil {
		return err
	}
	state := client.NewState(p.Initial)
	scope.Cleanup(state.Subscribe(func(value int) { count.Text(strconv.Itoa(value)) }))
	scope.On(increment, "click", func(client.Event) { state.Set(state.Get() + incrementStep) })
	closed := false
	scope.Cleanup(func() { closed = true })
	scope.On(call, "click", func(client.Event) {
		call.Property("disabled", true)
		status.Text("Submitting a public preview…")
		if err := scope.Go(func(ctx context.Context) {
			result, err := mutations.Preview.Call(ctx, mutations.Input{Amount: 1})
			if closed || ctx.Err() != nil {
				return
			}
			if err != nil {
				status.Text("Preview outcome unconfirmed. Reload before retrying.")
			} else {
				data, _ := json.Marshal(result.Data)
				receipt.Text(string(data))
				status.Text("Public preview confirmed. Nothing durable was saved.")
			}
			call.Property("disabled", false)
		}); err != nil {
			status.Text("Preview unavailable.")
			call.Property("disabled", false)
		}
	})
	proof.Text(clientProof)
	increment.Property("disabled", false)
	call.Property("disabled", false)
	return nil
}
