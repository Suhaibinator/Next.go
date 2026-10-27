//go:build js && wasm && !nextgo_module

package client

import (
	"syscall/js"
	"testing"
)

func TestRetainedNodeAdmissionCountsQueuedSiblingsBeforeExpanding(t *testing.T) {
	// Five thousand distinct synthetic nodes exceed the policy. The ownership
	// walk must reject on the second expansion, before reading a queued leaf,
	// rather than building a stack beyond its four-thousand-node ledger.
	fixture := js.Global().Get("Function").New(`
const stats={leafReads:0};
function leaf(){return {get childNodes(){stats.leafReads++;return [];}};}
const branch={childNodes:Array.from({length:2500},leaf)};
const owner={childNodes:[branch,...Array.from({length:2499},leaf)]};
return {owner,stats};`).Invoke()
	if _, _, ok := retainedDOMInfo(fixture.Get("owner"), false); ok || fixture.Get("stats").Get("leafReads").Int() != 0 {
		t.Fatal("DOM admission expanded beyond its queued-node budget")
	}
}
