//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/scripts"
)

func scriptHostEnvironment(t *testing.T) {
	t.Helper()
	oldWin, oldDoc, oldURL := win, doc, currentURL
	oldLoads, oldAliases, oldSources := scriptLoads, scriptAliases, scriptSources
	oldWaiters, oldHostWaiters := scriptWaiters, scriptHostWaiters
	oldImports := importedModules
	win, doc, currentURL = js.Global(), js.ValueOf(map[string]any{"baseURI": "https://site.test/"}), "https://site.test/"
	scriptLoads, scriptAliases, scriptSources = map[string]*scriptLoad{}, map[string]string{}, map[string]string{}
	scriptWaiters, scriptHostWaiters, importedModules = 0, 0, nil
	t.Cleanup(func() {
		win, doc, currentURL = oldWin, oldDoc, oldURL
		scriptLoads, scriptAliases, scriptSources = oldLoads, oldAliases, oldSources
		scriptWaiters, scriptHostWaiters, importedModules = oldWaiters, oldHostWaiters, oldImports
	})
}

func TestScriptHostBoundsPendingRequestsBeforeWorkerScheduling(t *testing.T) {
	scriptHostEnvironment(t)
	s, err := (scripts.Script{ID: "shared", Src: "https://vendor.test/settings.js"}).Check()
	if err != nil {
		t.Fatal(err)
	}
	state := &scriptLoad{declaration: s, done: make(chan struct{})}
	scriptLoads[s.Key()] = state
	raw, _ := json.Marshal(s)
	args := []js.Value{js.ValueOf(string(raw)), js.ValueOf(false)}
	promises := make([]js.Value, maxScriptWaiters)
	for i := range promises {
		promises[i] = scriptHostPromise(args).(js.Value)
	}
	if scriptHostWaiters != maxScriptWaiters {
		t.Fatal("pending host capacity not reserved synchronously", scriptHostWaiters)
	}
	controller := win.Get("AbortController").New()
	overflow := scriptHostPromise(args).(js.Value)
	if _, err := awaitBrowserPromise(context.Background(), overflow, controller); err == nil || !strings.Contains(err.Error(), "host waiter limit") {
		t.Fatal("unbounded pending script requests", err)
	}
	close(state.done)
	for _, promise := range promises {
		if _, err := awaitBrowserPromise(context.Background(), promise, controller); err != nil {
			t.Fatal("admitted host request failed", err)
		}
	}
	importUntil(t, func() bool { return scriptHostWaiters == 0 && scriptWaiters == 0 })
	if len(scriptLoads) != 1 {
		t.Fatal("shared dependency was registered more than once")
	}
}

func TestScriptHostRejectsMalformedInputAndReleasesFailedReservations(t *testing.T) {
	scriptHostEnvironment(t)
	controller := win.Get("AbortController").New()
	for _, args := range [][]js.Value{nil, {js.ValueOf("{}")}, {js.ValueOf(1), js.ValueOf(false)}, {js.ValueOf("{}"), js.ValueOf("false")}, {js.ValueOf("{}"), js.ValueOf(false), js.Null()}, {js.ValueOf(strings.Repeat(" ", 8193)), js.ValueOf(false)}} {
		promise := scriptHostPromise(args).(js.Value)
		if _, err := awaitBrowserPromise(context.Background(), promise, controller); err == nil {
			t.Fatal("malformed host request succeeded")
		}
		if scriptHostWaiters != 0 {
			t.Fatal("invalid host shape reserved a worker")
		}
	}
	for i := 0; i < maxScriptWaiters+1; i++ {
		promise := scriptHostPromise([]js.Value{js.ValueOf(`{"id":"bad","src":"data:text/javascript,invalid"}`), js.ValueOf(false)}).(js.Value)
		if _, err := awaitBrowserPromise(context.Background(), promise, controller); err == nil {
			t.Fatal("invalid descriptor succeeded")
		}
		importUntil(t, func() bool { return scriptHostWaiters == 0 })
	}
	if len(scriptLoads) != 0 || len(scriptAliases) != 0 || len(scriptSources) != 0 {
		t.Fatal("invalid request registered a dependency")
	}
}
