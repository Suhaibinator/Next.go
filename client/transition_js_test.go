//go:build js && wasm

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
	"time"
)

type controlledPromise struct{ value, resolve, reject js.Value }

func newControlledPromise() controlledPromise {
	p := controlledPromise{}
	executor := js.FuncOf(func(_ js.Value, args []js.Value) any { p.resolve, p.reject = args[0], args[1]; return nil })
	p.value = js.Global().Get("Promise").New(executor)
	executor.Release()
	return p
}

func transitionBrowser(t *testing.T, reduced bool) {
	t.Helper()
	previousDoc, previousWin, previousActive := doc, win, activeTransition
	media := js.FuncOf(func(js.Value, []js.Value) any { return map[string]any{"matches": reduced} })
	doc = js.ValueOf(map[string]any{})
	win = js.ValueOf(map[string]any{"matchMedia": media})
	activeTransition = nil
	t.Cleanup(func() { doc, win, activeTransition = previousDoc, previousWin, previousActive; media.Release() })
}

func TestTransitionFallbackAndUpdateFailure(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		t.Run(map[bool]string{true: "reduced", false: "unsupported"}[reduced], func(t *testing.T) {
			transitionBrowser(t, reduced)
			calls := 0
			if err := Transition(context.Background(), TransitionOptions{}, func() error { calls++; return nil }); err != nil || calls != 1 {
				t.Fatal(err, calls)
			}
			if err := Transition(context.Background(), TransitionOptions{}, func() error { panic("update failure") }); err == nil {
				t.Fatal("update panic escaped or was ignored")
			}
		})
	}
}

func TestTransitionCanceledCallbackNeverUpdatesAndSettlesAfterReturn(t *testing.T) {
	transitionBrowser(t, false)
	ready, updated, finished := newControlledPromise(), newControlledPromise(), newControlledPromise()
	callback := make(chan js.Value, 1)
	skips := 0
	skip := js.FuncOf(func(js.Value, []js.Value) any { skips++; return nil })
	start := js.FuncOf(func(_ js.Value, args []js.Value) any {
		callback <- args[0]
		return map[string]any{"ready": ready.value, "updateCallbackDone": updated.value, "finished": finished.value, "skipTransition": skip}
	})
	defer start.Release()
	defer skip.Release()
	doc.Set("startViewTransition", start)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	calls := 0
	go func() { result <- Transition(ctx, TransitionOptions{}, func() error { calls++; return nil }) }()
	var update js.Value
	select {
	case update = <-callback:
	case <-time.After(time.Second):
		t.Fatal("transition did not start")
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Native skip still invokes update. It must remain callable after cancellation.
	update.Invoke()
	updated.resolve.Invoke(js.Undefined())
	ready.reject.Invoke(js.Global().Get("Error").New("animation skipped"))
	finished.resolve.Invoke(js.Undefined())
	for until := time.Now().Add(time.Second); activeTransition != nil && time.Now().Before(until); {
		time.Sleep(time.Millisecond)
	}
	if calls != 0 || skips == 0 || activeTransition != nil {
		t.Fatal(calls, skips, activeTransition)
	}
}

func TestTransitionSlowSnapshotFallsBackOnceAndLateCallbackIsSafe(t *testing.T) {
	transitionBrowser(t, false)
	ready, updated, finished := newControlledPromise(), newControlledPromise(), newControlledPromise()
	var callback js.Value
	skip := js.FuncOf(func(js.Value, []js.Value) any { return nil })
	start := js.FuncOf(func(_ js.Value, args []js.Value) any {
		callback = args[0]
		return map[string]any{"ready": ready.value, "updateCallbackDone": updated.value, "finished": finished.value, "skipTransition": skip}
	})
	defer start.Release()
	defer skip.Release()
	doc.Set("startViewTransition", start)
	calls := 0
	if err := Transition(context.Background(), TransitionOptions{}, func() error { calls++; return nil }); err != nil || calls != 1 {
		t.Fatal(err, calls)
	}
	callback.Invoke()
	updated.resolve.Invoke(js.Undefined())
	ready.reject.Invoke("skipped")
	finished.resolve.Invoke(js.Undefined())
	for until := time.Now().Add(time.Second); activeTransition != nil && time.Now().Before(until); {
		time.Sleep(time.Millisecond)
	}
	if calls != 1 || activeTransition != nil {
		t.Fatal(calls, activeTransition)
	}
}
