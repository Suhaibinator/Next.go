//go:build js && wasm && nextgo_module

package client

import (
	"encoding/json"
	"reflect"
	"strings"
	"syscall/js"
	"testing"
)

func TestModuleTargetDelegatesDocumentAPIs(t *testing.T) {
	oldHost := moduleHost
	t.Cleanup(func() { moduleHost = oldHost })
	host := js.Global().Get("Object").New()
	moduleHost = host
	var calls []string
	for _, name := range []string{"navigate", "prefetch", "refresh", "back", "forward", "return", "invalidate"} {
		name := name
		callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
			calls = append(calls, name)
			if name == "navigate" {
				if args[0].String() != "/about?q=one#part" {
					t.Error("navigation target changed")
				}
				var options NavigationOptions
				if err := json.Unmarshal([]byte(args[1].String()), &options); err != nil {
					t.Error(err)
				}
				if options.Replace != (len(calls) == 2) {
					t.Error("replace policy changed across ABI")
				}
			}
			return ""
		})
		t.Cleanup(callback.Release)
		host.Set(name, callback)
	}
	for _, call := range []func() error{
		func() error { return Navigate("/about?q=one#part") },
		func() error { return Replace("/about?q=one#part") },
		func() error { return Prefetch("/about") },
		func() error { Refresh(); return nil },
		Back, Forward,
		func() error { return BackOrReplace("/about") },
		func() error { invalidateCache(); return nil },
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(calls, []string{"navigate", "navigate", "prefetch", "refresh", "back", "forward", "return", "invalidate"}) {
		t.Fatal("document API used a local router", calls)
	}
}

func TestModuleTargetPropagatesHostFailureAndRejectsDocumentOwnership(t *testing.T) {
	oldHost := moduleHost
	t.Cleanup(func() { moduleHost = oldHost })
	moduleHost = js.Global().Get("Object").New()
	failure := js.FuncOf(func(js.Value, []js.Value) any { return "navigation denied by core" })
	defer failure.Release()
	moduleHost.Set("navigate", failure)
	if err := Navigate("/about"); err == nil || !strings.Contains(err.Error(), "denied by core") {
		t.Fatal("host failure was lost", err)
	}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		Run()
	}()
	if !panicked {
		t.Fatal("module target installed a second document router")
	}
}
