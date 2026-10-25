//go:build js && wasm && nextgo_module

package client

import (
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

type refreshModuleFixture struct {
	exports        js.Value
	funcs          []js.Func
	documentFrozen bool
	refreshEnabled bool
}

func moduleRefreshFixture(t *testing.T) *refreshModuleFixture {
	t.Helper()
	fixture := &refreshModuleFixture{refreshEnabled: true}
	oldHost, oldBridge, oldMotion := win.Get(moduleBridgeName), win.Get("__nextgoDevRuntime"), win.Get("matchMedia")
	oldDoc, oldModuleHost := doc, moduleHost
	oldRuntime, oldState, oldActivity, oldFence := moduleRefresh, refreshStateRuntime, refreshActivity, refreshModuleFence
	oldMounting := refreshModuleMounting
	oldScopes, oldNext, oldFunctions, oldRegistry := moduleScopes, nextModuleScope, globalFunctions, registry
	oldDeployment, oldLocation, oldSelections := deployment, committedLocation, selections
	oldURL, oldFrozen, oldPublic := currentURL, draftFrozen, routeManifest.Public
	oldSelectionObservers, oldLocationObservers := selectionObservers, locationObservers
	moduleRefresh, refreshStateRuntime, refreshModuleFence = nil, nil, nil
	refreshModuleMounting = false
	refreshActivity = refreshActivityTracker{}
	moduleScopes, nextModuleScope, globalFunctions, registry = map[int]*Scope{}, 0, nil, map[string]Mount{}
	selectionObservers, locationObservers = map[uint64]selectionObserver{}, map[uint64]*locationObserver{}
	bind := func(object js.Value, name string, fn func([]js.Value) any) {
		callback := js.FuncOf(func(_ js.Value, args []js.Value) any { return fn(args) })
		fixture.funcs = append(fixture.funcs, callback)
		object.Set(name, callback)
	}
	body := win.Get("Object").New()
	bind(body, "getAttribute", func(args []js.Value) any {
		if args[0].String() == "data-nextgo-configuration" {
			return "config"
		}
		return js.Null()
	})
	bind(body, "hasAttribute", func(args []js.Value) any {
		return args[0].String() == "data-nextgo-refresh-frozen" && fixture.documentFrozen
	})
	doc = win.Get("Object").New()
	doc.Set("body", body)
	host := win.Get("Object").New()
	host.Set("version", moduleABI)
	bind(host, "refreshEnabled", func([]js.Value) any { return fixture.refreshEnabled })
	state, _ := json.Marshal(moduleState{ResourceEpoch: resourceEpoch, Paths: paths.Config{}, Location: Location{URL: "https://site.test/current"}, Selections: map[string]Selection{}, Public: map[string]string{}, Ready: true, Configuration: "config"})
	bind(host, "state", func([]js.Value) any { return string(state) })
	bind(host, "register", func(args []js.Value) any {
		if args[0].String() != "counter" || args[2].Int() != moduleABI {
			t.Error("normal ABI identity changed")
		}
		fixture.exports = args[3]
		return ""
	})
	win.Set(moduleBridgeName, host)
	bridge := win.Get("Object").New()
	bridge.Set("version", devrefresh.ExtensionVersion)
	bridge.Set("shimDigest", devrefresh.ShimDigest)
	bind(bridge, "start", func([]js.Value) any { return js.Undefined() })
	win.Set("__nextgoDevRuntime", bridge)
	win.Set("matchMedia", js.Undefined())
	t.Cleanup(func() {
		if moduleRefresh != nil {
			for _, scope := range moduleScopes {
				scope.dispose()
			}
			for _, export := range moduleRefresh.functions {
				export.object.Set(export.name, js.Undefined())
				export.callback.Release()
			}
		}
		for _, function := range globalFunctions {
			function.Release()
		}
		for _, function := range fixture.funcs {
			function.Release()
		}
		win.Set(moduleBridgeName, oldHost)
		win.Set("__nextgoDevRuntime", oldBridge)
		win.Set("matchMedia", oldMotion)
		doc, moduleHost = oldDoc, oldModuleHost
		moduleRefresh, refreshStateRuntime, refreshActivity, refreshModuleFence = oldRuntime, oldState, oldActivity, oldFence
		refreshModuleMounting = oldMounting
		moduleScopes, nextModuleScope, globalFunctions, registry = oldScopes, oldNext, oldFunctions, oldRegistry
		deployment, committedLocation, selections = oldDeployment, oldLocation, oldSelections
		currentURL, draftFrozen, routeManifest.Public = oldURL, oldFrozen, oldPublic
		selectionObservers, locationObservers = oldSelectionObservers, oldLocationObservers
	})
	return fixture
}

func (fixture *refreshModuleFixture) root(t *testing.T) (js.Value, map[string]js.Value) {
	t.Helper()
	root := win.Get("Object").New()
	root.Set("nodeType", 1)
	root.Set("tagName", "DIV")
	listeners := map[string]js.Value{}
	bind := func(name string, fn func([]js.Value) any) {
		callback := js.FuncOf(func(_ js.Value, args []js.Value) any { return fn(args) })
		fixture.funcs = append(fixture.funcs, callback)
		root.Set(name, callback)
	}
	bind("closest", func([]js.Value) any { return js.Null() })
	bind("querySelector", func([]js.Value) any { return js.Null() })
	bind("querySelectorAll", func([]js.Value) any { return js.ValueOf([]any{}) })
	bind("addEventListener", func(args []js.Value) any { listeners[args[0].String()] = args[1]; return nil })
	bind("removeEventListener", func(args []js.Value) any {
		if !listeners[args[0].String()].Equal(args[1]) {
			t.Error("listener removal changed callback identity")
		}
		delete(listeners, args[0].String())
		return nil
	})
	return root, listeners
}

func TestModuleTargetRefreshRetiresOnlyAfterPhysicalDrainAndReleasesExports(t *testing.T) {
	fixture := moduleRefreshFixture(t)
	root, listeners := fixture.root(t)
	var state *State[int]
	registry["counter"] = func(scope *Scope, _ json.RawMessage) error {
		if err := scope.RefreshInstance("one"); err != nil {
			return err
		}
		var err error
		state, err = NewRefreshState(scope, "count", 1)
		if err == nil {
			scope.On(scope.Root, "click", func(Event) { state.Set(state.Get() + 1) })
		}
		return err
	}
	done := RunModuleRefresh("counter", strings.Repeat("a", 64), "state-v1", "dom-v1")
	result := fixture.exports.Call("mount", root, `{}`, js.Null())
	if result.Get("error").String() != "" || len(globalFunctions) != 0 {
		t.Fatal(result, len(globalFunctions))
	}
	fixture.documentFrozen = true
	listeners["click"].Invoke(js.Null())
	if state.Get() != 1 {
		t.Fatal("document-wide fence admitted a module handler")
	}
	fixture.documentFrozen = false
	listeners["click"].Invoke(js.Null())
	if state.Get() != 2 {
		t.Fatal("counter callback did not run")
	}
	extension := fixture.exports.Get("refresh")
	if status, err := devrefresh.ParseStatus([]byte(extension.Call("status").String())); err != nil || status.Phase != devrefresh.PhaseReady || status.Pending != 0 || status.Scopes != 1 || status.Frozen {
		t.Fatal("invalid ready extension status", status, err)
	}
	if extension.Call("stop").String() == "" || moduleRefresh.phase != devrefresh.PhaseReady {
		t.Fatal("unprepared module retired")
	}
	lease, err := beginRefreshActivity()
	if err != nil {
		t.Fatal(err)
	}
	if prepared := extension.Call("prepare"); prepared.Get("error").String() == "" || moduleRefresh.frozen {
		t.Fatal("busy module was captured or frozen")
	}
	lease.Done()
	prepared := extension.Call("prepare")
	if prepared.Get("error").String() != "" || !moduleRefresh.frozen {
		t.Fatal(prepared)
	}
	if repeated := extension.Call("prepare"); repeated.Get("snapshot").String() != prepared.Get("snapshot").String() || repeated.Get("error").String() != "" {
		t.Fatal("repeated prepare recaptured or changed frozen state")
	}
	snapshot, err := devrefresh.ParseModuleSnapshot([]byte(prepared.Get("snapshot").String()))
	if err != nil || string(snapshot.Scopes[0].Cells[0].Value) != "2" {
		t.Fatal(snapshot, err)
	}
	listeners["click"].Invoke(js.Null())
	if state.Get() != 2 {
		t.Fatal("prepared event handler changed captured state")
	}
	if fixture.exports.Call("sync", "{}").String() == "" || fixture.exports.Call("mount", root, `{}`, js.Null()).Get("error").String() == "" {
		t.Fatal("prepared module admitted sync or mount")
	}
	// Simulate a previously admitted browser continuation that becomes observable
	// during cleanup. It is retained even after logical admission has frozen.
	physical := retainRefreshActivity()
	if failure := extension.Call("stop").String(); failure != "" {
		t.Fatal(failure)
	}
	time.Sleep(5 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("logical stop exited before physical settlement")
	default:
	}
	if len(listeners) != 0 || fixture.exports.Get("mount").Type() != js.TypeFunction {
		t.Fatal("scope not disposed or exports released before join")
	}
	physical.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("module main waiter did not become returnable")
	}
	if moduleRefresh.phase != devrefresh.PhaseStopped || len(moduleScopes) != 0 || refreshStateRuntime != nil || len(moduleRefresh.functions) != 0 || !fixture.exports.Get("mount").IsUndefined() || !extension.Get("stop").IsUndefined() {
		t.Fatal("retired module retained callback/state ownership")
	}
}

func TestModuleTargetRefreshImportsExactSourceScopeAndClaimsOnce(t *testing.T) {
	fixture := moduleRefreshFixture(t)
	root, _ := fixture.root(t)
	var state *State[int]
	registry["counter"] = func(scope *Scope, _ json.RawMessage) error {
		if err := scope.RefreshInstance("instance"); err != nil {
			return err
		}
		var err error
		state, err = NewRefreshState(scope, "count", 0)
		return err
	}
	done := RunModuleRefresh("counter", strings.Repeat("a", 64), "state-v1", "dom-v1")
	nextModuleScope = 8
	if result := fixture.exports.Call("mount", root, `{"same":true}`, js.Null()); result.Get("id").Int() != 9 || result.Get("error").String() != "" {
		t.Fatal(result)
	}
	state.Set(41)
	old := fixture.exports.Get("refresh")
	prepared := old.Call("prepare")
	if prepared.Get("error").String() != "" || old.Call("stop").String() != "" {
		t.Fatal(prepared)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source did not retire")
	}
	moduleRefresh, refreshActivity, nextModuleScope = nil, refreshActivityTracker{}, 0
	done = RunModuleRefresh("counter", strings.Repeat("b", 64), "state-v1", "dom-v1")
	candidate := fixture.exports.Get("refresh")
	if candidate.Call("import", prepared.Get("snapshot")).String() != "" {
		t.Fatal("candidate rejected compatible source epoch")
	}
	fixture.documentFrozen = true
	if result := fixture.exports.Call("mount", root, `{"same":true}`, js.Null(), 9); result.Get("id").Int() != 1 || result.Get("error").String() != "" || state.Get() != 41 {
		t.Fatal("source handle/state lost", result, state.Get())
	}
	if candidate.Call("claim").String() != "" {
		t.Fatal("claims did not complete")
	}
	fixture.documentFrozen = false
	if candidate.Call("prepare").Get("error").String() != "" || candidate.Call("stop").String() != "" {
		t.Fatal("candidate could not retire a second generation")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("candidate did not retire")
	}
}

func TestModuleTargetRefreshProductionABI(t *testing.T) {
	fixture := moduleRefreshFixture(t)
	registry["counter"] = func(*Scope, json.RawMessage) error { return nil }
	RunModule("counter", strings.Repeat("a", 64))
	if !fixture.exports.Get("refresh").IsUndefined() || len(globalFunctions) != 4 || refreshStateRuntime != nil || refreshActivity.enabled {
		t.Fatal("ordinary module opted into refresh")
	}
}

func TestModuleTargetRefreshRuntimeFallbackKeepsOrdinaryStateSemantics(t *testing.T) {
	for _, kind := range []string{"disabled", "missing-capability", "invalid-capability", "unqualified-shim", "missing-bridge"} {
		t.Run(kind, func(t *testing.T) {
			fixture := moduleRefreshFixture(t)
			switch kind {
			case "disabled":
				fixture.refreshEnabled = false
			case "missing-capability":
				win.Get(moduleBridgeName).Set("refreshEnabled", js.Undefined())
			case "invalid-capability":
				callback := js.FuncOf(func(js.Value, []js.Value) any { return "true" })
				fixture.funcs = append(fixture.funcs, callback)
				win.Get(moduleBridgeName).Set("refreshEnabled", callback)
			case "unqualified-shim":
				win.Get("__nextgoDevRuntime").Set("shimDigest", strings.Repeat("0", 64))
			case "missing-bridge":
				win.Set("__nextgoDevRuntime", js.Undefined())
			}
			root, _ := fixture.root(t)
			pointer := new(int)
			*pointer = 7
			registry["counter"] = func(scope *Scope, _ json.RawMessage) error {
				if err := scope.RefreshInstance("ordinary"); err != nil {
					return err
				}
				state, err := NewRefreshState(scope, "opaque", pointer)
				if err == nil && state.Get() != pointer {
					t.Fatal("ordinary pointer state changed")
				}
				return err
			}
			if waiter := RunModuleRefresh("counter", strings.Repeat("a", 64), "state-v1", "dom-v1"); waiter != nil {
				t.Fatal("unsupported host made main stoppable")
			}
			result := fixture.exports.Call("mount", root, `{}`, js.Null())
			if result.Get("error").String() != "" || !fixture.exports.Get("refresh").IsUndefined() || moduleRefresh != nil || refreshStateRuntime != nil || refreshActivity.enabled {
				t.Fatal("runtime-disabled module initialized refresh state/codecs", result)
			}
			fixture.exports.Call("dispose", result.Get("id"))
		})
	}
}

func TestModuleTargetRefreshScopedFormRequiresFullReloadWithoutCapturing(t *testing.T) {
	fixture := moduleRefreshFixture(t)
	root, _ := fixture.root(t)
	registry["counter"] = func(scope *Scope, _ json.RawMessage) error {
		if err := scope.RefreshInstance("instance"); err != nil {
			return err
		}
		if _, err := NewRefreshState(scope, "count", 1); err != nil {
			return err
		}
		_, err := NewScopedForm(scope, struct{ Value string }{Value: "private form value"})
		return err
	}
	RunModuleRefresh("counter", strings.Repeat("a", 64), "state-v1", "dom-v1")
	if result := fixture.exports.Call("mount", root, `{}`, js.Null()); result.Get("error").String() != "" {
		t.Fatal(result)
	}
	prepared := fixture.exports.Get("refresh").Call("prepare")
	if prepared.Get("error").String() == "" || prepared.Get("snapshot").String() != "" || moduleRefresh.frozen || moduleScopes[1].closed {
		t.Fatal("unsupported form was captured or destroyed before fallback")
	}
	if strings.Contains(prepared.Get("error").String(), "private form value") {
		t.Fatal("form data escaped a refresh diagnostic")
	}
}

func TestModuleTargetRefreshRejectsUnsupportedDOMAndUnsafeHandles(t *testing.T) {
	fixture := moduleRefreshFixture(t)
	root, _ := fixture.root(t)
	scope := &Scope{Root: Element{root}}
	if !refreshModuleDOMSupported(scope) {
		t.Fatal("plain server DOM rejected")
	}
	for _, tag := range []string{"FORM", "SELECT", "TEXTAREA", "DIALOG"} {
		root.Set("tagName", tag)
		if refreshModuleDOMSupported(scope) {
			t.Fatal("unsupported root accepted", tag)
		}
	}
	root.Set("tagName", "INPUT")
	root.Set("form", js.Null())
	for _, kind := range []string{"text", "checkbox"} {
		root.Set("type", kind)
		if !refreshModuleDOMSupported(scope) {
			t.Fatal("supported standalone input rejected", kind)
		}
	}
	for _, kind := range []string{"file", "password", "hidden", "radio", "email"} {
		root.Set("type", kind)
		if refreshModuleDOMSupported(scope) {
			t.Fatal("unsupported input accepted", kind)
		}
	}
	root.Set("type", "text")
	root.Set("form", win.Get("Object").New())
	if refreshModuleDOMSupported(scope) {
		t.Fatal("form-owned input accepted")
	}
	for _, value := range []js.Value{js.Null(), js.ValueOf("1"), js.ValueOf(-1), js.ValueOf(1.5), js.ValueOf(1 << 32)} {
		if _, err := refreshSourceHandle(value); err == nil {
			t.Fatal("unsafe source handle accepted", value)
		}
	}
}
