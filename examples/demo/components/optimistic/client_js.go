//go:build js && wasm

package optimistic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/client"
	_ "github.com/Suhaibinator/Next.go/clientonly"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
)

type request struct {
	intent *client.OptimisticIntent[int64, int64]
	cancel context.CancelFunc
}

func Mount(scope *client.Scope, raw json.RawMessage) error {
	props, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	nodes := map[string]client.Element{}
	for _, key := range []string{"value", "pending", "add", "canonical", "reject", "cancel", "refresh", "error", "notice"} {
		node, err := scope.Require("[data-optimistic-" + key + "]")
		if err != nil {
			return err
		}
		nodes[key] = node
	}
	if nodes["value"].Value.Get("textContent").String() != strconv.FormatInt(props.Initial, 10) {
		return fmt.Errorf("optimistic counter does not match server props")
	}
	model, err := client.NewScopedOptimistic(scope, props.Initial, func(n, delta int64) int64 { return n + delta }, client.OptimisticOptions[int64, int64]{Limit: 8})
	if err != nil {
		return err
	}
	ctx, cancelAll := context.WithCancel(context.Background())
	scope.Cleanup(cancelAll)
	var mu sync.Mutex
	active := map[uint64]request{}
	showError := func(err error) { nodes["error"].Text(err.Error()); nodes["error"].Property("hidden", false) }
	stop, err := model.Subscribe(func(state client.OptimisticSnapshot[int64]) {
		nodes["value"].Text(strconv.FormatInt(state.Value, 10))
		nodes["value"].Attribute("aria-busy", strconv.FormatBool(state.Pending > 0))
		pending := "No pending changes."
		if state.Pending == 1 {
			pending = "Saving 1 change…"
		} else if state.Pending > 1 {
			pending = fmt.Sprintf("Saving %d changes…", state.Pending)
		}
		nodes["pending"].Text(pending)
		nodes["cancel"].Property("disabled", state.Pending == 0)
		for _, key := range []string{"add", "canonical", "reject"} {
			nodes[key].Property("disabled", state.Available == 0)
		}
	})
	if err != nil {
		return err
	}
	scope.Cleanup(stop)
	start := func(tentative, submitted int64) {
		intent, err := model.Begin(tentative)
		if err != nil {
			showError(err)
			return
		}
		nodes["error"].Property("hidden", true)
		nodes["notice"].Text("Waiting for server confirmation…")
		requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		mu.Lock()
		active[intent.ID()] = request{intent: intent, cancel: cancel}
		mu.Unlock()
		go func() {
			defer cancel()
			accepted, err := submit(requestCtx, submitted)
			mu.Lock()
			delete(active, intent.ID())
			mu.Unlock()
			if ctx.Err() != nil {
				return
			}
			if requestCtx.Err() != nil {
				if intent.Reject() == nil {
					nodes["notice"].Text("Request cancelled locally. Refresh to reconcile with the server.")
				}
				return
			}
			if err != nil {
				if intent.Reject() == nil {
					showError(err)
					nodes["notice"].Text("Refresh to reconcile before retrying.")
				}
				return
			}
			if err := intent.Resolve(accepted); err != nil {
				if !errors.Is(err, client.ErrOptimisticSettled) && !errors.Is(err, client.ErrOptimisticClosed) {
					showError(err)
				}
				return
			}
			nodes["notice"].Text("Server confirmation received.")
		}()
	}
	scope.On(nodes["add"], "click", func(client.Event) { start(1, 1) })
	scope.On(nodes["canonical"], "click", func(client.Event) { start(5, 5) })
	scope.On(nodes["reject"], "click", func(client.Event) { start(1, 0) })
	scope.On(nodes["cancel"], "click", func(client.Event) {
		mu.Lock()
		requests := make([]request, 0, len(active))
		for _, r := range active {
			requests = append(requests, r)
		}
		mu.Unlock()
		changed := false
		for _, r := range requests {
			r.cancel()
			if r.intent.Reject() == nil {
				changed = true
			}
		}
		if changed {
			nodes["notice"].Text("Cancelled locally. Refresh to reconcile with the server.")
		}
	})
	scope.On(nodes["refresh"], "click", func(client.Event) { client.Refresh() })
	nodes["refresh"].Property("disabled", false)
	return nil
}

func submit(ctx context.Context, amount int64) (int64, error) {
	response, err := mutations.Increment.Call(ctx, mutations.IncrementInput{Amount: amount})
	if err != nil {
		return 0, err
	}
	if response.Data.Applied < 1 || response.Data.Applied > 2 {
		return 0, fmt.Errorf("The server returned an invalid confirmation. Refresh to reconcile.")
	}
	return response.Data.Applied, nil
}
