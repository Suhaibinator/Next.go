//go:build js && wasm

package client

import (
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
