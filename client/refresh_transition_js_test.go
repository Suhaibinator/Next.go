//go:build js && wasm && nextgo_devrefresh

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
)

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
