//go:build js && wasm

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
)

func TestRefreshMotionListenerDetachesBeforeRelease(t *testing.T) {
	previous := win
	defer func() { win = previous }()
	var listener js.Value
	removed := 0
	add := js.FuncOf(func(_ js.Value, args []js.Value) any {
		listener = args[1]
		return nil
	})
	remove := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if !args[1].Equal(listener) {
			t.Error("removed a different listener")
		}
		// The callback must still be callable while the browser detaches it.
		listener.Invoke(map[string]any{"matches": false})
		removed++
		listener = js.Undefined()
		return nil
	})
	media := js.FuncOf(func(js.Value, []js.Value) any {
		return map[string]any{"addEventListener": add, "removeEventListener": remove}
	})
	defer add.Release()
	defer remove.Release()
	defer media.Release()
	win = js.ValueOf(map[string]any{"matchMedia": media})
	stop := installMotionPreferenceOwned()
	if listener.Type() != js.TypeFunction {
		t.Fatal("listener was not attached")
	}
	stop()
	stop()
	if removed != 1 || !listener.IsUndefined() {
		t.Fatal("listener outlived its module", removed)
	}
}

func TestRefreshTransitionCancellationRetainsBrowserWork(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	transitionBrowser(t, false)
	ready, updated, finished := newControlledPromise(), newControlledPromise(), newControlledPromise()
	callbacks := make(chan js.Value, 1)
	skip := js.FuncOf(func(js.Value, []js.Value) any { return nil })
	start := js.FuncOf(func(_ js.Value, args []js.Value) any {
		callbacks <- args[0]
		return map[string]any{"ready": ready.value, "updateCallbackDone": updated.value, "finished": finished.value, "skipTransition": skip}
	})
	defer start.Release()
	defer skip.Release()
	doc.Set("startViewTransition", start)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	updates := 0
	go func() { result <- Transition(ctx, TransitionOptions{}, func() error { updates++; return nil }) }()
	callback := <-callbacks
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if status := freezeRefreshActivity(); status.Busy != 3 {
		t.Fatal("canceled call lost physical browser promises", status)
	}
	callback.Invoke()
	updated.resolve.Invoke(js.Undefined())
	ready.reject.Invoke("skipped")
	finished.resolve.Invoke(js.Undefined())
	refreshActivityAwaitCount(t, 0)
	if updates != 0 {
		t.Fatal("canceled update executed")
	}
}
