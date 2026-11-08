//go:build js && wasm && nextgo_module

package client

import (
	"context"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/scripts"
)

func TestModuleImportForwardsDescriptorAndReturnsHostNamespace(t *testing.T) {
	oldHost, oldWin := moduleHost, win
	t.Cleanup(func() { moduleHost, win = oldHost, oldWin })
	win = js.Global()
	namespace := js.ValueOf(map[string]any{"default": "exported", "named": 42})
	then := js.FuncOf(func(js.Value, []js.Value) any {
		t.Error("host namespace was assimilated as another promise")
		return nil
	})
	defer then.Release()
	namespace.Set("then", then)
	calls := 0
	forward := js.FuncOf(func(_ js.Value, args []js.Value) any {
		calls++
		input, err := scripts.ParseModule(args[0].String())
		if err != nil || !input.Asset || input.ID != "module-v1" || input.Src != "/vendor/module-v1.mjs" {
			t.Error(input, err)
		}
		holder := js.Global().Get("Object").New()
		holder.Set("namespace", namespace)
		return js.Global().Get("Promise").Call("resolve", holder)
	})
	defer forward.Release()
	moduleHost = js.ValueOf(map[string]any{"importModule": forward})
	value, err := ImportModule(context.Background(), scripts.Module{ID: "module-v1", Src: "/vendor/module-v1.mjs", Asset: true})
	if err != nil || !value.Equal(namespace) || calls != 1 {
		t.Fatal("module did not use the shared host namespace", value, err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ImportModule(ctx, scripts.Module{ID: "module-v1", Src: "/vendor/module-v1.mjs", Asset: true}); err != context.Canceled || calls != 1 {
		t.Fatal("canceled consumer contacted the host", err, calls)
	}
	if _, err := ImportModule(context.Background(), scripts.Module{}); err == nil || !strings.Contains(err.Error(), "ID") {
		t.Fatal("invalid descriptor contacted the host", err)
	}
}
