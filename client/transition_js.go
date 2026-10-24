//go:build js && wasm

package client

import (
	"context"
	"fmt"
	"syscall/js"
	"time"
)

type browserTransition struct{ value js.Value }

var activeTransition *browserTransition

func linkTransition(link js.Value) TransitionOptions {
	options := TransitionOptions{Disabled: link.Call("getAttribute", "data-nextgo-transition").String() == "none"}
	if kind := link.Call("getAttribute", "data-nextgo-transition-type"); !kind.IsNull() {
		options.Types = []string{kind.String()}
		if options.validate() != nil {
			return TransitionOptions{Disabled: true}
		}
	}
	return options
}

func installMotionPreference() {
	defer func() { _ = recover() }()
	if win.Get("matchMedia").Type() == js.TypeFunction {
		preference := win.Call("matchMedia", "(prefers-reduced-motion: reduce)")
		listen(preference, "change", func(e js.Value) {
			if e.Get("matches").Bool() {
				skipViewTransition()
			}
		})
	}
}

func skipViewTransition() {
	if moduleMode {
		_ = moduleCall("skipTransition")
	} else {
		for _, state := range lazyModules {
			if state.exports.Truthy() && state.err == nil {
				state.exports.Call("skipTransition")
			}
		}
	}
	if activeTransition != nil {
		skipTransition(activeTransition)
	}
}
func skipTransition(transition *browserTransition) {
	defer func() { _ = recover() }()
	if !transition.value.IsUndefined() && !transition.value.IsNull() {
		transition.value.Call("skipTransition")
	}
}
func reducedMotion() (reduced bool) {
	// A failed preference query conservatively avoids animation.
	reduced = true
	defer func() { _ = recover() }()
	if win.Get("matchMedia").Type() == js.TypeFunction {
		reduced = win.Call("matchMedia", "(prefers-reduced-motion: reduce)").Get("matches").Bool()
	}
	return
}

// observeTransition consumes both promise outcomes and releases both Go
// callbacks only when the browser has settled the promise. Animation rejection
// (including skipTransition or duplicate CSS names) is a normal fallback.
func observeTransition(promise js.Value, settled func()) {
	var success, failure js.Func
	finish := func() {
		defer success.Release()
		defer failure.Release()
		settled()
	}
	success = js.FuncOf(func(js.Value, []js.Value) any { finish(); return nil })
	failure = js.FuncOf(func(js.Value, []js.Value) any { finish(); return nil })
	promise.Call("then", success, failure)
}

// Transition runs one synchronous DOM/state update inside a native same-document
// view transition. Call it from a goroutine, not a synchronous browser callback.
// It returns when the update completes, without waiting for animation. Missing
// support, reduced motion and snapshot/animation failures use an ordinary update.
// Cancel ctx when the owning component unmounts; canceled updates do not run.
// Native browser callbacks are retained until their promises settle, even when
// cancellation lets this call return first. The update itself must not block.
func Transition(ctx context.Context, options TransitionOptions, update func() error) (err error) {
	if ctx == nil || update == nil {
		return fmt.Errorf("nextgo: transition requires context and update")
	}
	if err := options.validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	skipViewTransition()
	done := make(chan error, 1)
	updated := false
	var updateErr error
	invoke := func() error {
		if updated {
			return updateErr
		}
		updated = true
		defer func() { done <- updateErr }()
		defer func() {
			if failure := recover(); failure != nil {
				updateErr = fmt.Errorf("nextgo: transition update: %v", failure)
			}
		}()
		if updateErr = ctx.Err(); updateErr == nil {
			updateErr = update()
		}
		return updateErr
	}
	if options.Disabled || reducedMotion() || doc.Get("startViewTransition").Type() != js.TypeFunction {
		_ = invoke()
		return <-done
	}
	transition := &browserTransition{value: js.Undefined()}
	var callback js.Func
	released := false
	release := func() {
		if !released {
			released = true
			callback.Release()
		}
	}
	callback = js.FuncOf(func(js.Value, []js.Value) any {
		defer release()
		_ = invoke()
		if updateErr != nil {
			skipTransition(transition)
		}
		return nil
	})
	// Standard implementations schedule the callback asynchronously. If starting
	// the API throws before registering it, update immediately without animation.
	started := false
	func() {
		defer func() { _ = recover() }()
		transition.value = doc.Call("startViewTransition", callback)
		started = true
	}()
	if !started {
		release()
		_ = invoke()
		return <-done
	}
	activeTransition = transition
	observeTransition(transition.value.Get("ready"), func() {})
	observeTransition(transition.value.Get("finished"), func() {
		if activeTransition == transition {
			activeTransition = nil
		}
	})
	observeTransition(transition.value.Get("updateCallbackDone"), func() {
		// If snapshot capture was rejected before update, run the ordinary update.
		// The native promise establishes that it no longer owns the callback.
		_ = invoke()
		release()
	})
	func() {
		defer func() { _ = recover() }()
		types := transition.value.Get("types")
		if !types.IsUndefined() && types.Get("add").Type() == js.TypeFunction {
			for _, name := range options.Types {
				types.Call("add", name)
			}
		}
	}()
	// A slow snapshot must not indefinitely hold navigation/content. Skipping
	// still invokes the native callback later; invoke's guard prevents repetition.
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		skipTransition(transition)
		return ctx.Err()
	case <-timer.C:
		skipTransition(transition)
		_ = invoke()
		return <-done
	}
}
