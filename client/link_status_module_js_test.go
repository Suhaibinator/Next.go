//go:build js && wasm && nextgo_module

package client

import (
	"reflect"
	"syscall/js"
	"testing"
)

func TestModuleLinkStatusLifecycleAndRejectedRegistration(t *testing.T) {
	f := linkStatusEnvironment(t)
	oldHost := moduleHost
	t.Cleanup(func() { f.scope.dispose(); moduleHost = oldHost })
	var remote js.Value
	registered, rejected, removals := false, false, 0
	moduleHost = js.ValueOf(map[string]any{
		"version": moduleABI,
		"observeLinkStatus": f.function(func(_ js.Value, args []js.Value) any {
			if rejected {
				return map[string]any{"error": "full"}
			}
			if registered || !args[0].Equal(f.scope.Root.Value) || !args[1].Equal(f.links[0].Value) {
				t.Error("invalid registration")
			}
			remote, registered = args[2], true
			return map[string]any{"error": "", "id": 42, "pending": false}
		}),
		"unobserveLinkStatus": f.function(func(_ js.Value, args []js.Value) any {
			if !registered || args[1].Int() != 42 || moduleLinkCallbackCount != 1 {
				t.Error("callback released before physical unregistration")
			}
			registered = false
			removals++
			return ""
		}),
	})
	var seen []bool
	stop, err := f.scope.ObserveLinkStatus(f.links[0], func(value LinkStatus) { seen = append(seen, value.Pending) })
	if err != nil {
		t.Fatal(err)
	}
	remote.Invoke(true)
	remote.Invoke(true)
	remote.Invoke(false)
	if !reflect.DeepEqual(seen, []bool{false, true, false}) {
		t.Fatal("module status or deduplication failed", seen)
	}
	stop()
	stop()
	if registered || removals != 1 || moduleLinkCallbackCount != 0 {
		t.Fatal("module callback retained", registered, removals, moduleLinkCallbackCount)
	}
	rejected = true
	if _, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {}); err == nil || moduleLinkCallbackCount != 0 {
		t.Fatal("rejected registration leaked callback", err, moduleLinkCallbackCount)
	}
	rejected = false
	if _, err := f.scope.ObserveLinkStatus(f.links[0], func(LinkStatus) {}); err != nil {
		t.Fatal(err)
	}
	f.scope.dispose()
	if registered || removals != 2 || moduleLinkCallbackCount != 0 || len(linkStatusObservers) != 0 {
		t.Fatal("scope disposal retained registration")
	}
}
