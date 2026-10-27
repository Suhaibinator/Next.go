//go:build js && wasm

package client

import (
	"errors"
	"runtime"
	"syscall/js"
	"testing"
)

func propsReadEnvironment(t *testing.T) {
	t.Helper()
	propsBrowserEnvironment(t)
	if mergeCallbackReads != 0 {
		t.Fatal("prior owned callback read still held", mergeCallbackReads)
	}
	t.Cleanup(func() {
		if mergeCallbackReads != 0 {
			t.Error("owned callback read leaked", mergeCallbackReads)
		}
	})
}
func propsHoldReadCapacity(t *testing.T) func() {
	t.Helper()
	releases := []func(){}
	release := func() {
		for _, done := range releases {
			done()
		}
		releases = nil
	}
	t.Cleanup(release)
	for i := 0; i < mergeCallbackReadLimit; i++ {
		done, err := mergeEnterCallbackRead()
		if err != nil {
			t.Fatal(i, err)
		}
		releases = append(releases, done)
	}
	return release
}
func TestPropsOwnedEventKeepsNativeSynchronousCancellationAndPropagation(t *testing.T) {
	propsReadEnvironment(t)
	scope, _, fixture := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(value int) int { return value }}, nil)
	child := scope.mergeView.child
	button, err := child.Require("[data-click]")
	if err != nil {
		t.Fatal(err)
	}
	calls, later := 0, 0
	child.On(button, "click", func(event Event) {
		calls++
		event.PreventDefault()
		event.Value.Call("stopPropagation")
		event.Value.Call("stopImmediatePropagation")
	})
	after := js.FuncOf(func(js.Value, []js.Value) any { later++; return nil })
	defer after.Release()
	fixture.Get("button").Call("addEventListener", "click", after)
	defer fixture.Get("button").Call("removeEventListener", "click", after)
	event := js.Global().Get("Event").New("click", map[string]any{"cancelable": true})
	accepted := fixture.Get("button").Call("dispatchEvent", event).Bool()
	if accepted || !event.Get("defaultPrevented").Bool() || calls != 1 || later != 0 || mergeCallbackReads != 0 {
		t.Fatal("owned event left native cancellation stack or leaked its read", accepted, calls, later, mergeCallbackReads)
	}
}
func TestPropsOwnedEventReadRefusalCancelsDefaultWithoutReplayAndDefaultScopesStillRun(t *testing.T) {
	propsReadEnvironment(t)
	scope, _, fixture := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(value int) int { return value }}, nil)
	button, err := scope.mergeView.child.Require("[data-click]")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	scope.mergeView.child.On(button, "click", func(Event) { calls++ })
	release := propsHoldReadCapacity(t)
	event := js.Global().Get("Event").New("click", map[string]any{"cancelable": true})
	accepted := fixture.Get("button").Call("dispatchEvent", event).Bool()
	if moduleMode {
		if !accepted || calls != 1 {
			t.Fatal("module noop read admission changed native event", accepted, calls)
		}
	} else {
		if accepted || !event.Get("defaultPrevented").Bool() || calls != 0 {
			t.Fatal("read refusal ran or replayed owned mutation", accepted, calls)
		}
		plainFixture := clientOnlyDOM(t)
		plain := &Scope{Root: Element{plainFixture.Get("root")}}
		t.Cleanup(plain.dispose)
		plainCalls := 0
		plain.On(Element{plainFixture.Get("fallback")}, "click", func(Event) { plainCalls++ })
		if !plainFixture.Get("fallback").Call("dispatchEvent", js.Global().Get("Event").New("click", map[string]any{"cancelable": true})).Bool() || plainCalls != 1 {
			t.Fatal("unowned default event was subjected to new read admission", plainCalls)
		}
	}
	release()
	if calls != 0 && !moduleMode {
		t.Fatal("releasing read capacity replayed canceled event", calls)
	}
	fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click", map[string]any{"cancelable": true}))
	want := 1
	if moduleMode {
		want = 2
	}
	if calls != want {
		t.Fatal("fresh native event did not recover admission", calls)
	}
}
func TestPropsOwnedCallbackAndNativeEventReleaseReadsOnPanicAndGoexit(t *testing.T) {
	propsReadEnvironment(t)
	for _, exit := range []bool{false, true} {
		err := runMergeCallback(func() {
			if exit {
				runtime.Goexit()
			}
			panic("private")
		})
		if !errors.Is(err, errPropsCallback) || mergeCallbackReads != 0 || propsWorkerCount() != 0 {
			t.Fatal("actual callback settlement lost read or physical charge", err, mergeCallbackReads, propsWorkerCount())
		}
	}
	scope, _, fixture := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(value int) int { return value }}, nil)
	button, err := scope.mergeView.child.Require("[data-click]")
	if err != nil {
		t.Fatal(err)
	}
	scope.mergeView.child.On(button, "click", func(Event) { panic("event private") })
	fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if mergeCallbackReads != 0 {
		t.Fatal("native event panic leaked read", mergeCallbackReads)
	}
	release := propsHoldReadCapacity(t)
	release()
}
