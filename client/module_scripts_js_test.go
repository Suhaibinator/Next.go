//go:build js && wasm && nextgo_module

package client

import (
	"context"
	"encoding/json"
	"github.com/Suhaibinator/Next.go/scripts"
	"syscall/js"
	"testing"
)

func TestModuleTargetScriptDependencyDelegatesToCore(t *testing.T) {
	oldHost := moduleHost
	t.Cleanup(func() { moduleHost = oldHost })
	moduleHost = js.Global().Get("Object").New()
	calls := 0
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		calls++
		var input scripts.Script
		if err := json.Unmarshal([]byte(args[0].String()), &input); err != nil {
			t.Error(err)
		}
		if input.ID != "dependency" || !input.Asset || args[1].Bool() != (calls == 2) {
			t.Error("script host arguments changed", input)
		}
		return js.Global().Get("Promise").Call("resolve", js.Null())
	})
	defer callback.Release()
	moduleHost.Set("script", callback)
	s := scripts.Script{ID: "dependency", Src: "/library.js", Asset: true}
	if err := LoadScript(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if err := RetryScript(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("script API ran in the component runtime", calls)
	}
}
