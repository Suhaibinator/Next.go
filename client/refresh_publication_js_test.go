//go:build js && wasm && !nextgo_module && nextgo_devrefresh

package client

import (
	"syscall/js"
	"testing"
)

// Go-backed DOM setters can reenter Go before the caller resumes. Observe the
// publication boundary directly instead of relying on a favorable timer turn.
func TestRefreshCoreErrorPhasePublishesAccessibleRecovery(t *testing.T) {
	f := refreshEnvironment(t)
	c := fixtureCoordinator(refreshManifest(0))
	c.initialized = false
	f.attrs["data-nextgo-refresh"] = "99"
	var published, disabled bool
	var role, revision string
	f.onBodySet = func(name, value string) {
		if name == "data-nextgo-refresh-state" && value == "startup-error" {
			disabled = c.startupRetry.Get("disabled").Bool()
			role = c.startupScope.Root.Value.Call("getAttribute", "role").String()
			revision = f.attrs["data-nextgo-refresh"]
			published = true
		}
	}
	mockBrowserFetch(t, func([]js.Value) any {
		return js.Global().Get("Promise").Call("resolve", js.Global().Get("Response").New("unavailable", map[string]any{"status": 503}))
	})
	ready := false
	if !bootstrapDevRefresh(func() { ready = true }) {
		t.Fatal("hydration was not deferred")
	}
	// Finish all old-path writes before an expected baseline assertion failure,
	// so test cleanup does not release its still-needed DOM callback values.
	resourceWait(t, func() bool {
		return published && !c.startupRetry.Get("disabled").Bool() && c.startupScope.Root.Value.Call("getAttribute", "role").String() == "alert"
	})
	if ready || c.disabled || c.initialized || f.reloads != 0 {
		t.Fatal("failed startup changed hydration/reload contract")
	}
	if disabled || role != "alert" || revision != "0" {
		t.Fatalf("missing accessible recovery at phase publication: disabled=%v role=%q revision=%q", disabled, role, revision)
	}
}

func TestRefreshCorePhasePublishesCurrentRevision(t *testing.T) {
	f := refreshEnvironment(t)
	c := fixtureCoordinator(refreshManifest(7))
	f.attrs["data-nextgo-refresh"] = "6"
	var atPhase string
	f.onBodySet = func(name, value string) {
		if name == "data-nextgo-refresh-state" && value == "ready" {
			atPhase = f.attrs["data-nextgo-refresh"]
		}
	}
	c.mark("ready")
	if atPhase != "7" {
		t.Fatalf("phase published before current revision: got %q want 7", atPhase)
	}
}

func TestRefreshCoreFailureThenDisabledClearsRecoveryMarkers(t *testing.T) {
	f := refreshEnvironment(t)
	c := fixtureCoordinator(refreshManifest(0))
	c.initialized = false
	calls := 0
	mockBrowserFetch(t, func([]js.Value) any {
		calls++
		if calls == 1 {
			return js.Global().Get("Promise").Call("resolve", js.Global().Get("Response").New("unavailable", map[string]any{"status": 503}))
		}
		return js.Global().Get("Promise").Call("resolve", js.Global().Get("Response").New(js.Null(), map[string]any{"status": 204}))
	})
	ready := 0
	if !bootstrapDevRefresh(func() { ready++ }) {
		t.Fatal("hydration was not deferred")
	}
	resourceWait(t, func() bool {
		return f.attrs["data-nextgo-refresh-state"] == "startup-error" && !c.startupRetry.Get("disabled").Bool() && c.startupScope.Root.Value.Call("getAttribute", "role").String() == "alert"
	})
	panel := c.startupScope.Root.Value
	c.startupRetry.Call("click")
	resourceWait(t, func() bool { return ready == 1 })
	if calls != 2 || !c.disabled || c.initialized || f.reloads != 0 || !panel.Get("removed").Bool() {
		t.Fatal("explicit204 did not restore ordinary baseline")
	}
	_, phase := f.attrs["data-nextgo-refresh-state"]
	_, revision := f.attrs["data-nextgo-refresh"]
	if phase || revision {
		t.Fatalf("disabled baseline retained recovery markers: phase=%q revision=%q", f.attrs["data-nextgo-refresh-state"], f.attrs["data-nextgo-refresh"])
	}
}
