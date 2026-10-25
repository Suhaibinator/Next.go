//go:build js && wasm && nextgo_module && !nextgo_devrefresh

package client

import (
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"
)

func TestModuleTargetProductionRefreshEntryRemainsOrdinaryABI(t *testing.T) {
	oldDoc, oldWin, oldHost, oldRegistry, oldScopes, oldNext, oldState, oldActivity := doc, win, moduleHost, registry, moduleScopes, nextModuleScope, refreshStateRuntime, refreshActivity
	count := len(globalFunctions)
	var functions []js.Func
	t.Cleanup(func() {
		for _, scope := range moduleScopes {
			scope.dispose()
		}
		for _, fn := range globalFunctions[count:] {
			fn.Release()
		}
		globalFunctions = globalFunctions[:count]
		for _, fn := range functions {
			fn.Release()
		}
		doc, win, moduleHost, registry, moduleScopes, nextModuleScope, refreshStateRuntime, refreshActivity = oldDoc, oldWin, oldHost, oldRegistry, oldScopes, oldNext, oldState, oldActivity
	})
	fn := func(callback func(js.Value, []js.Value) any) js.Func {
		f := js.FuncOf(callback)
		functions = append(functions, f)
		return f
	}
	configuration := strings.Repeat("a", 64)
	doc = js.ValueOf(map[string]any{"body": map[string]any{"getAttribute": fn(func(js.Value, []js.Value) any { return configuration })}})
	state, _ := json.Marshal(moduleState{Configuration: configuration})
	exports := js.Undefined()
	host := js.ValueOf(map[string]any{"version": moduleABI, "state": fn(func(js.Value, []js.Value) any { return string(state) }), "refreshEnabled": fn(func(js.Value, []js.Value) any { t.Error("production queried development capability"); return true }), "register": fn(func(_ js.Value, args []js.Value) any { exports = args[3]; return "" })})
	win = js.ValueOf(map[string]any{moduleBridgeName: host, "Object": js.Global().Get("Object")})
	registry = map[string]Mount{}
	moduleScopes = map[int]*Scope{}
	nextModuleScope = 0
	refreshStateRuntime = nil
	refreshActivity = refreshActivityTracker{}
	pointer := new(int)
	*pointer = 7
	registry["counter"] = func(scope *Scope, _ json.RawMessage) error {
		if err := scope.RefreshInstance("opaque"); err != nil {
			return err
		}
		value, err := NewRefreshState(scope, "value", pointer)
		if err == nil && value.Get() != pointer {
			t.Fatal("ordinary pointer state changed")
		}
		return err
	}
	if done := RunModuleRefresh("counter", strings.Repeat("b", 64), "state-v1", "dom-v1"); done != nil {
		t.Fatal("production main became stoppable")
	}
	if !exports.Get("refresh").IsUndefined() || len(globalFunctions) != count+4 {
		t.Fatal("production changed module ABI")
	}
	result := exports.Call("mount", js.Global().Get("Object").New(), "{}", js.Null())
	if result.Get("error").String() != "" {
		t.Fatal(result)
	}
	if refreshStateRuntime != nil || refreshActivity.enabled {
		t.Fatal("production initialized codec/physical registry")
	}
	exports.Call("dispose", result.Get("id"))
}
