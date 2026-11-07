//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"encoding/json"
	"syscall/js"
	"testing"
)

func lazyPrivacyEnvironment(t *testing.T) (js.Value, js.Value, js.Value) {
	t.Helper()
	fixture, app, status := mergeApplicationTestEnvironment(t)
	oldModules, oldRetry := lazyModules, lazyRetryScope
	t.Cleanup(func() { lazyModules, lazyRetryScope = oldModules, oldRetry })
	lazyModules = map[string]*lazyState{}
	lazyRetryScope = nil
	fixture.Get("root").Call("removeAttribute", clientBoundaryAttribute)
	return fixture, app, status
}
func TestMergeLazyQueuedProxyRegistersBeforeWaitAndDenialPreventsMount(t *testing.T) {
	fixture, app, status := lazyPrivacyEnvironment(t)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	t.Cleanup(scope.dispose)
	ready := make(chan struct{})
	state := &lazyState{loading: true, ready: ready, exports: js.Undefined()}
	lazyModules["counter"] = state
	done, err := lazyHydrate(scope, "counter")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-ready:
		default:
			close(ready)
		}
		<-done
	})
	if _, exists := mergeApplication.scopes[scope]; !exists {
		t.Fatal("loading proxy scheduled before authority registration")
	}
	if len(mounted) != 1 || mounted[0] != scope || propsWorkerCount() != 0 || scope.mergeCallbacks {
		t.Fatal("lazy proxy changed legacy scheduling/worker admission")
	}
	mergeIncomingAuthority(nil, nil, 403)
	message := status.Get("textContent").String()
	calls := 0
	exports := js.Global().Get("Object").New()
	mount := js.FuncOf(func(js.Value, []js.Value) any { calls++; return map[string]any{"id": 1, "error": ""} })
	t.Cleanup(mount.Release)
	exports.Set("mount", mount)
	state.exports = exports
	close(ready)
	<-done
	if calls != 0 || scope.active() || !app.Get("hidden").Bool() || !app.Call("hasAttribute", "inert").Bool() || fixture.Get("root").Call("getAttribute", "data-nextgo-hydrated").String() != "loading" || status.Get("textContent").String() != message {
		t.Fatal("late module readiness mounted or published denied UI", calls)
	}
}
func TestMergeLazyInflightMountLostAuthorityDisposesReturnedHandleExactlyOnce(t *testing.T) {
	fixture, _, status := lazyPrivacyEnvironment(t)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	t.Cleanup(scope.dispose)
	ready := make(chan struct{})
	close(ready)
	exports := js.Global().Get("Object").New()
	calls, disposed, synced := 0, 0, 0
	var callbacks []js.Func
	bind := func(name string, fn func([]js.Value) any) {
		f := js.FuncOf(func(_ js.Value, args []js.Value) any { return fn(args) })
		callbacks = append(callbacks, f)
		exports.Set(name, f)
	}
	t.Cleanup(func() {
		for _, f := range callbacks {
			f.Release()
		}
	})
	bind("sync", func(args []js.Value) any {
		var value moduleState
		if json.Unmarshal([]byte(args[0].String()), &value) != nil || !value.Revoked {
			t.Error("module did not receive immediate revoked snapshot")
		}
		synced++
		return ""
	})
	bind("dispose", func(args []js.Value) any {
		if args[0].Int() != 31 {
			t.Error("wrong late handle")
		}
		disposed++
		return ""
	})
	bind("mount", func([]js.Value) any {
		calls++
		mergeIncomingAuthority(nil, nil, 401)
		return map[string]any{"id": 31, "error": ""}
	})
	state := &lazyState{ready: ready, exports: exports, expectedStop: true}
	lazyModules["counter"] = state
	broken := js.Global().Get("Object").New()
	broken.Set("sync", js.Global().Get("Function").New("throw Error('exited module')"))
	lazyModules["exited"] = &lazyState{exports: broken, expectedStop: true}
	otherSynced := 0
	other := js.Global().Get("Object").New()
	otherSync := js.FuncOf(func(js.Value, []js.Value) any { otherSynced++; return "" })
	callbacks = append(callbacks, otherSync)
	other.Set("sync", otherSync)
	lazyModules["other"] = &lazyState{exports: other, expectedStop: true}
	done, err := lazyHydrate(scope, "counter")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { <-done })
	<-done
	message := status.Get("textContent").String()
	scope.dispose()
	if calls != 1 || disposed != 1 || synced != 1 || otherSynced != 1 || len(state.mounts) != 0 || !scope.closed || !scope.mergeRevoked || fixture.Get("root").Call("getAttribute", "data-nextgo-hydrated").String() != "loading" || status.Get("textContent").String() != message {
		t.Fatal("late lazy Mount escaped authority/cleanup", calls, disposed, synced, len(state.mounts))
	}
}
func TestMergeLazyRetryIsApplicationOwnedAndCannotRetryAfterDenial(t *testing.T) {
	fixture, app, _ := lazyPrivacyEnvironment(t)
	status := fixture.Call("make", "div")
	doc.Get("body").Call("appendChild", status)
	body := doc.Get("body")
	get := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "nextgo-app" {
			return app
		}
		if args[0].String() == "nextgo-status" {
			return status
		}
		return js.Null()
	})
	t.Cleanup(get.Release)
	doc.Set("getElementById", get)
	prototype := js.Global().Get("Object").Call("getPrototypeOf", status)
	prototype.Set("remove", js.Global().Get("Function").New("if(this.parentNode)this.parentNode.removeChild(this)"))
	showLazyError(fixture.Get("root"))
	retry := lazyRetryScope
	if retry == nil {
		t.Fatal("retry not constructed")
	}
	t.Cleanup(retry.dispose)
	if _, exists := mergeApplication.scopes[retry]; !exists {
		t.Fatal("status retry proxy escaped application cohort")
	}
	button := status.Get("firstChild")
	if button.IsNull() {
		t.Fatal("missing actual retry button")
	}
	state := &lazyState{err: errPropsInvalid, exports: js.Undefined(), ready: make(chan struct{})}
	lazyModules["counter"] = state
	mergeIncomingAuthority(nil, nil, 403)
	message := status.Get("textContent").String()
	button.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if retry.active() || state.ready == nil || state.err == nil || status.Get("textContent").String() != message || !body.Call("contains", app).Bool() {
		t.Fatal("denied retry invoked status/reset/hydration work")
	}
}
func TestMergeLazyRuntimePromiseSettlesAfterDenialWithoutReplacingStatus(t *testing.T) {
	_, _, status := lazyPrivacyEnvironment(t)
	oldFunctions := len(globalFunctions)
	promise := js.Global().Get("Function").New("let reject;const promise=new Promise((_,r)=>reject=r);return {promise,reject}").Invoke()
	win.Set("AbortController", js.Global().Get("AbortController"))
	win.Set("Go", js.Global().Get("Function").New("promise", "return function(){return {importObject:{},run(){return promise}}}").Invoke(promise.Get("promise")))
	win.Set("WebAssembly", js.Global().Get("Function").New("return {instantiate(){return Promise.resolve({})}}").Invoke())
	state := &lazyState{exports: js.Global().Get("Object").New()}
	t.Cleanup(func() {
		if len(globalFunctions) > oldFunctions {
			promise.Call("reject", "test shutdown")
			propsWait(t, func() bool { return state.err != nil })
		}
		for _, f := range globalFunctions[oldFunctions:] {
			f.Release()
		}
		globalFunctions = globalFunctions[:oldFunctions]
	})
	if err := instantiateLazyModule(context.Background(), "counter", state, nil, js.Global().Get("Object").New()); err != nil {
		t.Fatal(err)
	}
	mergeIncomingAuthority(nil, nil, 403)
	message := status.Get("textContent").String()
	promise.Call("reject", "late physical runtime failure")
	propsWait(t, func() bool { return state.err != nil })
	if state.runError == nil || status.Get("textContent").String() != message || status.Get("hidden").Bool() {
		t.Fatal("physical runtime settlement erased denied status", state.runError)
	}
}

func TestMergeLazyRetryGenerationClosesExactLoaderAndJoinsBothOldWaiters(t *testing.T) {
	fixture, _, _ := lazyPrivacyEnvironment(t)
	first, next := make(chan struct{}), make(chan struct{})
	state := &lazyState{loading: true, ready: first, exports: js.Undefined()}
	one, two, loader := make(chan struct{}), make(chan struct{}), make(chan struct{})
	bRoot := fixture.Call("make", "div")
	bRoot.Call("setAttribute", "data-nextgo-island", "counter")
	fixture.Get("outer").Call("appendChild", bRoot)
	a, b := &Scope{Root: Element{fixture.Get("root")}}, &Scope{Root: Element{bRoot}}
	for _, scope := range []*Scope{a, b} {
		if err := withMergeApplicationMount(scope, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, ready := range []chan struct{}{first, next} {
			select {
			case <-ready:
			default:
				close(ready)
			}
		}
		<-loader
		<-one
		<-two
		a.dispose()
		b.dispose()
	})
	token := mergeApplicationToken()
	go finishLazyHydration(a, token, state, first, a.Root.Value, js.Undefined(), one)
	go finishLazyHydration(b, token, state, first, b.Root.Value, js.Undefined(), two)
	go func() {
		defer close(loader)
		runLazyLoad(state, first, func() error {
			state.ready = next
			state.loading = true
			state.err = errPropsStale
			return errPropsInvalid
		})
	}()
	<-loader
	select {
	case <-first:
	default:
		t.Fatal("loader did not close its captured generation")
	}
	select {
	case <-next:
		t.Fatal("old loader closed the retry generation")
	default:
	}
	if !state.loading || state.err != errPropsStale {
		t.Fatal("old loader overwrote newer generation result")
	}
	propsWait(t, func() bool {
		select {
		case <-one:
		default:
			return false
		}
		select {
		case <-two:
			return true
		default:
			return false
		}
	})
	if a.Root.Value.Call("hasAttribute", "data-nextgo-hydrated").Bool() {
		t.Fatal("old waiter published a new generation's result")
	}
}

func TestMergeLazyRetryResetsQueuedLoadingOwnersAndFreshHydrationCompletes(t *testing.T) {
	fixture, app, _ := lazyPrivacyEnvironment(t)
	oldSelections, oldRetained := selections, retainedCurrent
	retainedCurrent = nil
	t.Cleanup(func() { selections, retainedCurrent = oldSelections, oldRetained })
	aRoot, bRoot := fixture.Get("root"), fixture.Call("make", "div")
	bRoot.Call("setAttribute", "data-nextgo-island", "counter")
	bRoot.Call("setAttribute", "data-nextgo-props", "null")
	app.Call("appendChild", bRoot)
	oldA, oldB := &Scope{Root: Element{aRoot}}, &Scope{Root: Element{bRoot}}
	for _, scope := range []*Scope{oldA, oldB} {
		if err := withMergeApplicationMount(scope, func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		mounted = append(mounted, scope)
	}
	aRoot.Call("setAttribute", "data-nextgo-hydrated", "error")
	bRoot.Call("setAttribute", "data-nextgo-hydrated", "loading")
	first := make(chan struct{})
	close(first)
	state := &lazyState{ready: first, err: errPropsInvalid, exports: js.Undefined()}
	lazyModules["counter"] = state
	token := mergeApplicationToken()
	if !resetLazyFailures(token) || !oldA.closed || !oldB.closed || state.ready != nil || state.err != nil || aRoot.Call("hasAttribute", "data-nextgo-hydrated").Bool() || bRoot.Call("hasAttribute", "data-nextgo-hydrated").Bool() {
		t.Fatal("retry stranded the unpublished loading proxy")
	}
	oldDoneA, oldDoneB := make(chan struct{}), make(chan struct{})
	go finishLazyHydration(oldA, token, state, first, aRoot, js.Undefined(), oldDoneA)
	go finishLazyHydration(oldB, token, state, first, bRoot, js.Undefined(), oldDoneB)
	ready := make(chan struct{})
	state.ready = ready
	exports := js.Global().Get("Object").New()
	state.exports = exports
	callbacks := []js.Func{}
	counters := map[int]*Scope{}
	buttons := []js.Value{}
	clicks := 0
	bind := func(name string, fn func([]js.Value) any) {
		f := js.FuncOf(func(_ js.Value, args []js.Value) any { return fn(args) })
		callbacks = append(callbacks, f)
		exports.Set(name, f)
	}
	bind("sync", func([]js.Value) any { return "" })
	bind("mount", func(args []js.Value) any {
		root := args[0]
		owner := &Scope{Root: Element{root}}
		button := fixture.Call("make", "button")
		root.Call("appendChild", button)
		owner.On(Element{button}, "click", func(Event) { clicks++ })
		id := len(counters) + 1
		counters[id] = owner
		buttons = append(buttons, button)
		return map[string]any{"id": id, "error": ""}
	})
	bind("dispose", func(args []js.Value) any {
		if owner := counters[args[0].Int()]; owner != nil {
			owner.dispose()
			delete(counters, args[0].Int())
		}
		return ""
	})
	query := js.FuncOf(func(js.Value, []js.Value) any { return js.Null() })
	callbacks = append(callbacks, query)
	doc.Set("querySelector", query)
	t.Cleanup(func() {
		for _, f := range callbacks {
			f.Release()
		}
	})
	nextA, nextB := &Scope{Root: Element{aRoot}}, &Scope{Root: Element{bRoot}}
	var doneA, doneB <-chan struct{}
	t.Cleanup(func() {
		select {
		case <-ready:
		default:
			close(ready)
		}
		<-oldDoneA
		<-oldDoneB
		if doneA != nil {
			<-doneA
		}
		if doneB != nil {
			<-doneB
		}
		nextA.dispose()
		nextB.dispose()
		for _, scope := range counters {
			scope.dispose()
		}
	})
	var err error
	doneA, err = lazyHydrate(nextA, "counter")
	if err != nil {
		t.Fatal(err)
	}
	doneB, err = lazyHydrate(nextB, "counter")
	if err != nil {
		t.Fatal(err)
	}
	close(ready)
	<-doneA
	<-doneB
	<-oldDoneA
	<-oldDoneB
	if aRoot.Call("getAttribute", "data-nextgo-hydrated").String() != "true" || bRoot.Call("getAttribute", "data-nextgo-hydrated").String() != "true" || len(state.mounts) != 2 || len(buttons) != 2 || !nextA.active() || !nextB.active() {
		t.Fatal("fresh retry owners did not become useful", len(state.mounts), len(buttons))
	}
	for _, button := range buttons {
		button.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	}
	if clicks != 2 {
		t.Fatal("fresh retry handlers did not execute", clicks)
	}
}
