//go:build js && wasm && nextgo_devrefresh && !nextgo_module

package client

import (
	"context"
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/scripts"
)

func TestScriptHostRefreshDrainRetainsAdmittedWaiter(t *testing.T) {
	scriptHostEnvironment(t)
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	s, err := (scripts.Script{ID: "held", Src: "https://vendor.test/held.js"}).Check()
	if err != nil {
		t.Fatal(err)
	}
	state := &scriptLoad{declaration: s, done: make(chan struct{})}
	scriptLoads[s.Key()] = state
	raw, _ := json.Marshal(s)
	args := []js.Value{js.ValueOf(string(raw)), js.ValueOf(false)}
	admitted := scriptHostPromise(args).(js.Value)
	importUntil(t, func() bool { return scriptWaiters == 1 })
	if status := freezeRefreshActivity(); status.Busy == 0 || !status.Frozen || scriptHostWaiters != 1 {
		t.Fatal("pending host work escaped refresh accounting", status, scriptHostWaiters)
	}
	controller := win.Get("AbortController").New()
	rejected := scriptHostPromise(args).(js.Value)
	if _, err := awaitBrowserPromise(context.Background(), rejected, controller); err == nil || !strings.Contains(err.Error(), "stopping this runtime") {
		t.Fatal("frozen core admitted a new host request", err)
	}
	if scriptHostWaiters != 1 {
		t.Fatal("frozen refusal changed pending capacity")
	}
	close(state.done)
	if _, err := awaitBrowserPromise(context.Background(), admitted, controller); err != nil {
		t.Fatal("pre-freeze request could not complete", err)
	}
	if err := waitRefreshActivity(context.Background()); err != nil || scriptHostWaiters != 0 || scriptWaiters != 0 {
		t.Fatal("pending request did not physically drain", err, scriptHostWaiters, scriptWaiters)
	}
	if err := thawRefreshActivity(); err != nil {
		t.Fatal(err)
	}
}
