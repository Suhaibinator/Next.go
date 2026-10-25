//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

type refreshFixture struct {
	attrs       map[string]string
	statusAttrs map[string]bool
	status      js.Value
	functions   []js.Func
	reloads     int
}

func refreshEnvironment(t *testing.T) *refreshFixture {
	t.Helper()
	f := &refreshFixture{attrs: map[string]string{"data-nextgo-build": strings.Repeat("a", 20), "data-nextgo-dev-generation": strings.Repeat("b", 32), "data-nextgo-hydration": "ready"}, statusAttrs: map[string]bool{}}
	oldDoc, oldWin, oldC, oldLazy, oldMounted, oldURL, oldPaths, oldManifest, oldActivity := doc, win, devRefresh, lazyModules, mounted, currentURL, deployment, routeManifest, refreshActivity
	globalCount := len(globalFunctions)
	t.Cleanup(func() {
		for _, fn := range globalFunctions[globalCount:] {
			fn.Release()
		}
		globalFunctions = globalFunctions[:globalCount]
		doc, win, devRefresh, lazyModules, mounted, currentURL, deployment, routeManifest, refreshActivity = oldDoc, oldWin, oldC, oldLazy, oldMounted, oldURL, oldPaths, oldManifest, oldActivity
		for _, fn := range f.functions {
			fn.Release()
		}
	})
	body := js.Global().Get("Object").New()
	body.Set("getAttribute", f.fn(func(_ js.Value, args []js.Value) any {
		value, ok := f.attrs[args[0].String()]
		if !ok {
			return js.Null()
		}
		return value
	}))
	body.Set("hasAttribute", f.fn(func(_ js.Value, args []js.Value) any { _, ok := f.attrs[args[0].String()]; return ok }))
	body.Set("setAttribute", f.fn(func(_ js.Value, args []js.Value) any { f.attrs[args[0].String()] = args[1].String(); return nil }))
	body.Set("removeAttribute", f.fn(func(_ js.Value, args []js.Value) any { delete(f.attrs, args[0].String()); return nil }))
	f.status = js.Global().Get("Object").New()
	f.status.Set("textContent", "unrelated error")
	f.status.Set("hidden", false)
	f.status.Set("hasAttribute", f.fn(func(_ js.Value, args []js.Value) any { return f.statusAttrs[args[0].String()] }))
	f.status.Set("removeAttribute", f.fn(func(_ js.Value, args []js.Value) any { delete(f.statusAttrs, args[0].String()); return nil }))
	doc = js.ValueOf(map[string]any{"body": body, "baseURI": "https://site.test/", "activeElement": js.Null(), "getElementById": f.fn(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "nextgo-status" {
			return f.status
		}
		return js.Null()
	}), "querySelector": f.fn(func(js.Value, []js.Value) any { return js.Null() })})
	doc.Set("addEventListener", f.fn(func(js.Value, []js.Value) any { return nil }))
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL"), "Uint8Array": js.Global().Get("Uint8Array"), "AbortController": js.Global().Get("AbortController"), "location": map[string]any{"href": "https://site.test/", "origin": "https://site.test", "reload": f.fn(func(js.Value, []js.Value) any { f.reloads++; return nil })}})
	win.Set("history", map[string]any{"state": map[string]any{"nextgoEntry": "entry"}})
	win.Set("JSON", js.Global().Get("JSON"))
	devRefresh = nil
	lazyModules = map[string]*lazyState{}
	mounted = nil
	currentURL = "https://site.test/"
	deployment = paths.Config{}
	routeManifest = navigation.Manifest{Development: true}
	refreshActivity = refreshActivityTracker{}
	return f
}

func TestRefreshCoreBootstrapUsesLatestEventBeforeFirstHydration(t *testing.T) {
	f := refreshEnvironment(t)
	base := refreshManifest(0)
	c := fixtureCoordinator(base)
	c.initialized = false
	f.statusAttrs["data-nextgo-dev-build-error"] = true
	f.status.Set("textContent", "Build failed: current source")
	win.Set("__nextgoDevRuntime", map[string]any{"version": 1, "shimDigest": devrefresh.ShimDigest, "start": f.fn(func(js.Value, []js.Value) any { return nil })})
	latest := refreshManifest(2)
	latest.Modules[0].Digest = strings.Repeat("1", 64)
	latest.Modules[0].URL = "/_nextgo/" + strings.Repeat("2", 20) + "/module-" + latest.Modules[0].Digest + ".wasm"
	latestBytes, _ := json.Marshal(latest)
	baseBytes, _ := json.Marshal(base)
	mockBrowserFetch(t, func(args []js.Value) any {
		if !strings.HasPrefix(args[0].String(), devrefresh.ManifestPath+"?build=") {
			t.Error("startup fetched unexpected resource", args[0])
		}
		hook := win.Get("__nextgoRefresh")
		if hook.Type() != js.TypeFunction {
			t.Error("event hook installed after initial fetch")
		} else {
			hook.Invoke(string(latestBytes))
		}
		return js.Global().Get("Promise").Call("resolve", js.Global().Get("Response").New(string(baseBytes), map[string]any{"status": 200}))
	})
	ready := make(chan struct{}, 1)
	if !bootstrapDevRefresh(func() { ready <- struct{}{} }) {
		t.Fatal("supported startup did not defer hydration")
	}
	resourceWait(t, func() bool { return len(ready) == 1 })
	if !c.initialized || c.current.Revision != 2 || lazyModules["counter"].url != latest.Modules[0].URL || f.reloads != 0 {
		t.Fatal("startup used stale descriptors/reload loop", c.current.Revision, f.reloads)
	}
	if f.status.Get("hidden").Bool() || f.status.Get("textContent").String() != "Build failed: current source" {
		t.Fatal("startup hid current compile failure")
	}
}
func (f *refreshFixture) fn(callback func(js.Value, []js.Value) any) js.Func {
	fn := js.FuncOf(callback)
	f.functions = append(f.functions, fn)
	return fn
}
func refreshManifest(revision uint64) devrefresh.Manifest {
	previous := uint64(0)
	if revision > 0 {
		previous = revision - 1
	}
	digest := strings.Repeat("c", 64)
	return devrefresh.Manifest{Version: 1, Revision: revision, Previous: previous, ServingBuild: strings.Repeat("a", 20), ServingGeneration: strings.Repeat("b", 32), Compatibility: strings.Repeat("d", 64), ShimDigest: devrefresh.ShimDigest, ModuleABI: moduleABI, Modules: []devrefresh.Module{{Name: "counter", URL: "/_nextgo/" + strings.Repeat("e", 20) + "/module-" + digest + ".wasm", Digest: digest, Epoch: strings.Repeat("f", 64), Size: 8, StateSchema: "counter-v1", DOMSchema: "counter-dom-v1"}}}
}
func fixtureCoordinator(m devrefresh.Manifest) *refreshCoordinator {
	c := &refreshCoordinator{compatibility: m.Compatibility, current: m, seen: m, initialized: true}
	devRefresh = c
	state := &lazyState{exports: js.Undefined()}
	overlayLazyDescriptor(state, m.Modules[0])
	lazyModules[m.Modules[0].Name] = state
	return c
}

func TestRefreshCoreLatestQueueChecksEveryParentAndCoalesces(t *testing.T) {
	f := refreshEnvironment(t)
	base := refreshManifest(0)
	c := fixtureCoordinator(base)
	c.running = true
	first, second := refreshManifest(1), refreshManifest(2)
	c.receiveManifest(first)
	c.receiveManifest(second)
	if c.pending == nil || c.pending.Revision != 2 || c.seen.Revision != 2 || f.reloads != 0 {
		t.Fatal("latest queue lost parent chain", c.pending, c.seen.Revision, f.reloads)
	}
	c.receiveManifest(second)
	if f.reloads != 0 {
		t.Fatal("identical replay reloaded")
	}
	gap := refreshManifest(4)
	c.receiveManifest(gap)
	if f.reloads != 1 || !c.frozen || c.pending != nil {
		t.Fatal("missing parent did not fail closed", f.reloads)
	}
}
func TestRefreshCoreNoOpRepointAndUnloadedUpdateStayLazy(t *testing.T) {
	f := refreshEnvironment(t)
	base := refreshManifest(0)
	c := fixtureCoordinator(base)
	f.statusAttrs["data-nextgo-dev-build-error"] = true
	f.status.Set("textContent", "Build failed: syntax error")
	mockBrowserFetch(t, func([]js.Value) any { t.Error("unloaded/no-op update downloaded WASM"); return js.Undefined() })
	next := refreshManifest(1)
	next.Modules[0].URL = strings.Replace(next.Modules[0].URL, strings.Repeat("e", 20), strings.Repeat("1", 20), 1)
	if err := c.apply(next); err != nil {
		t.Fatal(err)
	}
	if c.current.Revision != 1 || c.swaps != 0 || lazyModules["counter"].url != next.Modules[0].URL || !f.status.Get("hidden").Bool() || f.status.Get("textContent").String() != "" {
		t.Fatal("no-op did not advance/clear owned build error")
	}
	f.status.Set("textContent", "unrelated error")
	f.status.Set("hidden", false)
	changed := refreshManifest(2)
	changed.Modules[0].Digest = strings.Repeat("2", 64)
	changed.Modules[0].URL = "/_nextgo/" + strings.Repeat("3", 20) + "/module-" + changed.Modules[0].Digest + ".wasm"
	changed.Modules[0].Epoch = strings.Repeat("4", 64)
	if err := c.apply(changed); err != nil {
		t.Fatal(err)
	}
	if lazyModules["counter"].descriptor.Digest != changed.Modules[0].Digest || f.status.Get("hidden").Bool() || f.status.Get("textContent").String() != "unrelated error" {
		t.Fatal("lazy overlay changed unrelated status")
	}
}
func TestRefreshCoreLatestDescriptorURLUsedForInitialLoad(t *testing.T) {
	refreshEnvironment(t)
	m := refreshManifest(7)
	fixtureCoordinator(m)
	var target string
	mockBrowserFetch(t, func(args []js.Value) any {
		target = args[0].String()
		return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("fixture stops before runtime start"))
	})
	if err := loadLazyModule("counter", lazyModules["counter"]); err == nil {
		t.Fatal("fixture load unexpectedly succeeded")
	}
	if target != m.Modules[0].URL {
		t.Fatal("latest descriptor fetched under stale serving build", target)
	}
}
func TestRefreshCoreProxyOwnsImmutableExportsWhileFenced(t *testing.T) {
	f := refreshEnvironment(t)
	c := fixtureCoordinator(refreshManifest(0))
	c.frozen = true
	oldCalls, newCalls := 0, 0
	oldExports := js.ValueOf(map[string]any{"dispose": f.fn(func(js.Value, []js.Value) any { oldCalls++; return nil })})
	newExports := js.ValueOf(map[string]any{"dispose": f.fn(func(js.Value, []js.Value) any { newCalls++; return nil })})
	root := js.ValueOf(map[string]any{"parentNode": js.Global().Get("Object").New()})
	state := lazyModules["counter"]
	state.exports = oldExports
	scope := &Scope{}
	trackLazyMount(state, scope, root, "{}", js.Undefined(), oldExports, 1)
	if len(state.mounts) != 1 || oldCalls != 0 {
		t.Fatal("fenced internal proxy disposed immediately")
	}
	state.exports = newExports
	scope.dispose()
	if oldCalls != 1 || newCalls != 0 || len(state.mounts) != 0 {
		t.Fatal("cleanup called successor exports", oldCalls, newCalls)
	}
	scope = &Scope{}
	state.expectedStop = true
	trackLazyMount(state, scope, root, "{}", js.Undefined(), oldExports, 2)
	scope.dispose()
	if oldCalls != 1 {
		t.Fatal("retiring proxy called prepared module dispose")
	}
}
func TestRefreshCoreRetirementRequiresActualSettlementZeroExitAndEmptyTimers(t *testing.T) {
	refreshEnvironment(t)
	s := &lazyState{expectedStop: true, runDone: make(chan struct{}), runPromise: js.Global().Get("Promise").Call("resolve"), runtime: js.ValueOf(map[string]any{"__nextgoDevExitCode": 0, "_scheduledTimeouts": map[string]any{"size": 0}})}
	if validateRetiredRuntime(s) == nil {
		t.Fatal("accepted stop acknowledgement without run settlement")
	}
	close(s.runDone)
	if err := validateRetiredRuntime(s); err != nil {
		t.Fatal(err)
	}
	for _, code := range []any{js.Undefined(), 1, -1, "0"} {
		s.runtime.Set("__nextgoDevExitCode", code)
		if validateRetiredRuntime(s) == nil {
			t.Fatal("accepted unsuccessful exit", code)
		}
	}
	s.runtime.Set("__nextgoDevExitCode", 0)
	s.runtime.Get("_scheduledTimeouts").Set("size", 1)
	if validateRetiredRuntime(s) == nil {
		t.Fatal("accepted unresolved shim timer")
	}
	s.runtime.Get("_scheduledTimeouts").Set("size", 0)
	s.runError = context.Canceled
	if validateRetiredRuntime(s) == nil {
		t.Fatal("accepted rejected run promise")
	}
}
func TestRefreshCoreSnapshotBindsSourceEpochHandleAndExactProps(t *testing.T) {
	refreshEnvironment(t)
	m := refreshManifest(0)
	fixtureCoordinator(m)
	state := lazyModules["counter"]
	props := "{\"initial\":3}"
	sum := sha256.Sum256([]byte(props))
	r := &refreshReplacement{old: state, module: m.Modules[0], mounts: []*lazyMount{{handle: 7, props: props}}, snapshot: devrefresh.ModuleSnapshot{Version: 1, Name: "counter", Epoch: state.descriptor.Epoch, StateSchema: state.descriptor.StateSchema, DOMSchema: state.descriptor.DOMSchema, Scopes: []devrefresh.ScopeSnapshot{{Handle: 7, Instance: "first", PropsDigest: hex.EncodeToString(sum[:]), Cells: []devrefresh.Cell{}}}}}
	if err := validateRefreshSnapshot(r); err != nil {
		t.Fatal(err)
	}
	r.snapshot.Scopes[0].Handle = 8
	if validateRefreshSnapshot(r) == nil {
		t.Fatal("accepted exchanged root handle")
	}
	r.snapshot.Scopes[0].Handle = 7
	r.snapshot.Scopes[0].PropsDigest = strings.Repeat("0", 64)
	if validateRefreshSnapshot(r) == nil {
		t.Fatal("accepted changed props")
	}
}
func TestRefreshCoreDOMBoundRejectsWideChildrenBeforeIndexing(t *testing.T) {
	refreshEnvironment(t)
	root := js.ValueOf(map[string]any{"childNodes": map[string]any{"length": 1 << 20}})
	if nodes, err := refreshDOMNodes(root, 8192); err == nil || nodes != nil {
		t.Fatal("wide DOM exceeded retained-reference budget")
	}
}
func TestRefreshCoreControlShapeAndSelectionStayOwned(t *testing.T) {
	f := refreshEnvironment(t)
	input := js.ValueOf(map[string]any{"type": "text", "form": js.Null(), "value": "draft", "checked": false, "selectionStart": 1, "selectionEnd": 3, "selectionDirection": "forward", "isConnected": true})
	doc.Set("activeElement", input)
	doc.Set("querySelectorAll", f.fn(func(js.Value, []js.Value) any { return []any{input} }))
	controls, err := captureRefreshControls()
	if err != nil || !controls.unchanged() {
		t.Fatal(err)
	}
	input.Set("type", "checkbox")
	if controls.unchanged() {
		t.Fatal("changed input type bypassed selection checks")
	}
	input.Set("type", "text")
	input.Set("selectionStart", 0)
	if controls.unchanged() {
		t.Fatal("selection changed")
	}
	input.Set("selectionStart", 1)
	input.Set("form", js.Global().Get("Object").New())
	if controls.unchanged() {
		t.Fatal("new form association accepted")
	}
}
func TestRefreshCoreBusyAndUnmanagedModulesRejectHotSwap(t *testing.T) {
	refreshEnvironment(t)
	c := fixtureCoordinator(refreshManifest(0))
	q := coreMutationQueue()
	q.reopen()
	if err := c.idle(); err != nil {
		t.Fatal(err)
	}
	c.composing = true
	if c.idle() == nil {
		t.Fatal("IME work accepted")
	}
	c.composing = false
	lazyModules["unmanaged"] = &lazyState{exports: js.Global().Get("Object").New()}
	if c.idle() == nil {
		t.Fatal("unmanaged runtime accepted")
	}
	delete(lazyModules, "unmanaged")
	lease, err := q.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if c.idle() == nil {
		t.Fatal("active mutation accepted")
	}
	lease()
	if err := c.idle(); err != nil {
		t.Fatal(err)
	}
}
func TestRefreshCoreMalformedCandidateDoesNotDisturbOldScopes(t *testing.T) {
	f := refreshEnvironment(t)
	m := refreshManifest(0)
	c := fixtureCoordinator(m)
	state := lazyModules["counter"]
	state.exports = js.Global().Get("Object").New()
	scope := &Scope{}
	state.mounts = map[*Scope]*lazyMount{scope: {scope: scope}}
	next := refreshManifest(1)
	next.Modules[0].Digest = strings.Repeat("0", 64)
	next.Modules[0].URL = "/_nextgo/" + strings.Repeat("e", 20) + "/module-" + next.Modules[0].Digest + ".wasm"
	mockBrowserFetch(t, func([]js.Value) any {
		response := js.Global().Get("Response").New("badbytes", map[string]any{"status": 200})
		js.Global().Get("Object").Call("defineProperty", response, "url", map[string]any{"value": "https://site.test" + next.Modules[0].URL})
		return js.Global().Get("Promise").Call("resolve", response)
	})
	if err := c.apply(next); err == nil {
		t.Fatal("invalid WASM digest accepted")
	}
	if scope.closed || state.expectedStop || c.frozen || f.reloads != 0 {
		t.Fatal("bad candidate disturbed existing scope before fallback")
	}
}
