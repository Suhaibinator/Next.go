//go:build js && wasm && !nextgo_module

package client

import "testing"

func TestCoreTargetRejectsModuleEntrypoint(t *testing.T) {
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		RunModule("component", "epoch")
	}()
	if !panicked {
		t.Fatal("ordinary browser target entered module mode")
	}
}
