//go:build js && wasm

package worker

import "syscall/js"

// Run registers and owns the dedicated worker lifetime; no application or document hydration code is linked.
func Run() {
	bridge := js.Global().Get("__nextgoOfflineBridge")
	if bridge.Type() != js.TypeObject {
		return
	}
	if installRuntime(bridge) {
		select {}
	}
}
