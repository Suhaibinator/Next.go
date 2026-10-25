//go:build js && wasm

package client

import "testing"

func TestRefreshStateNonHotPreservesOrdinaryStateBehavior(t *testing.T) {
	before := refreshStateRuntime
	refreshStateRuntime = nil
	t.Cleanup(func() { refreshStateRuntime = before })
	scope := &Scope{}
	defer scope.dispose()
	if err := scope.RefreshInstance("production"); err != nil {
		t.Fatal(err)
	}
	pointer := new(int)
	*pointer = 7
	state, err := NewRefreshState(scope, "count", pointer)
	if err != nil || state.Get() != pointer {
		t.Fatal(err)
	}
	state.Set(new(int))
	if refreshStateRuntime != nil {
		t.Fatal("allocated migration registry outside hot mode")
	}
}
