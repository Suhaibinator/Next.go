//go:build js && wasm

package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/internal/ascii"
)

// LazyModule describes one compiler-generated, content-addressed Go module.
// Applications select lazy loading with component.json; generated code registers
// these descriptors. Loaded modules retain independent Go runtimes for the document.
type LazyModule struct {
	File, Digest, Epoch string
	Size                int
	// Development-only explicit state/DOM contracts. Production leaves them empty.
	StateSchema, DOMSchema string
}
type lazyState struct {
	descriptor          LazyModule
	ready               chan struct{}
	exports             js.Value
	err                 error
	loading             bool
	runtime, runPromise js.Value
	runDone             chan struct{}
	runError            error
	expectedStop        bool
	url                 string
	mounts              map[*Scope]*lazyMount
}

type lazyMount struct {
	scope                         *Scope
	root, opener, exports, parent js.Value
	props                         string
	handle                        int
}

var lazyModules = map[string]*lazyState{}
var lazyRetryScope *Scope

var lazySlots = make(chan struct{}, 2)

const moduleABI = 6
const moduleBridgeName = "__nextgoModuleHost"

func RegisterLazy(name string, module LazyModule) {
	if registry[name] != nil || lazyModules[name] != nil || len(lazyModules) >= 8 || !ascii.LowerHex(module.Digest, 64) || module.File != "module-"+module.Digest+".wasm" || !ascii.LowerHex(module.Epoch, 64) || module.Size <= 0 || module.Size > 8<<20 {
		panic("invalid lazy component registration")
	}
	lazyModules[name] = &lazyState{descriptor: module, exports: js.Undefined()}
	// Generated registrations precede Run. Select the existing host here, while
	// leaving installation at Run's original point rather than during registration.
	registeredModuleHostInstaller = installModuleHost
}

// lazyHydrate registers the core proxy before appending it or scheduling any
// module work. Its completion joins this waiter, not the shared module runtime.
func lazyHydrate(scope *Scope, name string) (<-chan struct{}, error) {
	done := make(chan struct{})
	started := false
	err := withMergeApplicationMount(scope, func() error {
		if scope == nil || !scope.propsLive() || lazyModules[name] == nil {
			return errPropsClosed
		}
		token := mergeApplicationToken()
		mounted = append(mounted, scope)
		started = true
		beginLazyHydration(scope, name, token, done)
		return nil
	})
	if err != nil {
		scope.dispose()
		mergeRetainMounted([]*Scope{scope})
		if !started {
			close(done)
		}
	}
	return done, err
}
func lazyScopeCurrent(scope *Scope, token *mergeApplicationIdentity) bool {
	if scope == nil || !scope.propsLive() || !mergeApplicationCurrent(token) || !scope.Root.Value.Get("isConnected").Bool() {
		return false
	}
	if mergeApplication == nil {
		return true
	}
	app := doc.Call("getElementById", "nextgo-app")
	return app.Equal(mergeApplication.root) && app.Get("isConnected").Bool() && app.Call("contains", scope.Root.Value).Bool()
}
func beginLazyHydration(scope *Scope, name string, token *mergeApplicationIdentity, done chan struct{}) {
	root := scope.Root.Value
	opener := navigationOpener
	state := lazyModules[name]
	root.Call("setAttribute", "data-nextgo-hydrated", "loading")
	ready := state.ready
	if !state.loading && ready == nil {
		state.loading = true
		ready = make(chan struct{})
		state.ready = ready
		go runLazyLoad(state, ready, func() error { return loadLazyModule(name, state) })
	}
	// A retry may replace state.ready before this goroutine is scheduled.
	// Every admitted waiter and loader closes/awaits its exact generation.
	go finishLazyHydration(scope, token, state, ready, root, opener, done)
}
func runLazyLoad(state *lazyState, ready chan struct{}, load func() error) {
	defer close(ready)
	var loadErr error
	defer func() {
		if failure := recover(); failure != nil {
			loadErr = fmt.Errorf("module startup failed: %v", failure)
		}
		if state.ready == ready {
			state.loading = false
			state.err = loadErr
		}
	}()
	loadErr = load()
}
func finishLazyHydration(scope *Scope, token *mergeApplicationIdentity, state *lazyState, ready <-chan struct{}, root, opener js.Value, done chan struct{}) {
	defer close(done)
	handle, owned, tracked := 0, false, false
	exports := js.Undefined()
	disposed := false
	dispose := func() {
		if disposed {
			return
		}
		disposed = true
		exports.Call("dispose", handle)
	}
	defer func() {
		failure := recover()
		// A returned module handle is owned exactly once even when authority was
		// revoked during its actual Mount. Do not adopt it or stamp late DOM.
		if owned && !tracked {
			func() {
				defer func() {
					if v := recover(); v != nil {
						win.Get("console").Call("error", "Next.go late module cleanup failed")
					}
				}()
				dispose()
			}()
		}
		if failure != nil && lazyScopeCurrent(scope, token) {
			root.Call("setAttribute", "data-nextgo-hydrated", "error")
			report(fmt.Sprintf("Component hydration failed: %v", failure))
		}
	}()
	<-ready
	if state.ready != ready {
		return
	}
	if !lazyScopeCurrent(scope, token) {
		return
	}
	if state.err != nil {
		root.Call("setAttribute", "data-nextgo-hydrated", "error")
		showLazyError(root)
		if lazyScopeCurrent(scope, token) {
			hydrate()
		}
		return
	}
	props := root.Call("getAttribute", "data-nextgo-props").String()
	exports = state.exports
	result := exports.Call("mount", root, props, opener)
	if id := result.Get("id"); id.Type() == js.TypeNumber && id.Int() > 0 {
		handle = id.Int()
		owned = true
	}
	if !lazyScopeCurrent(scope, token) {
		scope.dispose()
		return
	}
	if failure := result.Get("error"); failure.Type() == js.TypeString && failure.String() != "" {
		root.Call("setAttribute", "data-nextgo-hydrated", "error")
		report("Component hydration failed: " + failure.String())
		hydrate()
		return
	}
	if !owned {
		panic("module returned no owned scope")
	}
	trackLazyMountWithCleanup(state, scope, root, props, opener, exports, handle, dispose)
	tracked = true
	if !lazyScopeCurrent(scope, token) {
		scope.dispose()
		return
	}
	root.Call("setAttribute", "data-nextgo-hydrated", "true")
	hydrate()
}

func trackLazyMount(state *lazyState, scope *Scope, root js.Value, props string, opener, exports js.Value, handle int) {
	trackLazyMountWithCleanup(state, scope, root, props, opener, exports, handle, func() { exports.Call("dispose", handle) })
}
func trackLazyMountWithCleanup(state *lazyState, scope *Scope, root js.Value, props string, opener, exports js.Value, handle int, dispose func()) {
	if state.mounts == nil {
		state.mounts = map[*Scope]*lazyMount{}
	}
	mount := &lazyMount{scope: scope, root: root, props: props, opener: opener, exports: exports, handle: handle, parent: root.Get("parentNode")}
	state.mounts[scope] = mount
	cleanup := func() {
		delete(state.mounts, scope)
		// The old cleanup must never call a successor runtime's mutable exports.
		if !devRefreshBuild || !state.expectedStop {
			dispose()
		}
	}
	// This is internal proxy ownership, including during a fenced replacement.
	// Public Scope.Cleanup deliberately refuses application work while frozen.
	if !scope.propsLive() {
		cleanup()
	} else {
		scope.cleanup = append(scope.cleanup, cleanup)
	}
}
func showLazyError(root js.Value) {
	if lazyRetryScope != nil || mergeApplicationRevoked() {
		return
	}
	retry := &Scope{Root: Element{root}}
	err := withMergeApplicationMount(retry, func() error {
		report("A Go component could not load. Reload or retry its download.")
		status := doc.Call("getElementById", "nextgo-status")
		button := doc.Call("createElement", "button")
		button.Set("type", "button")
		button.Set("textContent", "Retry component")
		button.Call("setAttribute", "data-nextgo-lazy-retry", "")
		status.Call("appendChild", button)
		lazyRetryScope = retry
		retry.Cleanup(func() {
			button.Call("remove")
			if lazyRetryScope == retry {
				lazyRetryScope = nil
			}
		})
		token := mergeApplicationToken()
		retry.On(Element{button}, "click", func(Event) {
			if !mergeApplicationCurrent(token) {
				return
			}
			retry.dispose()
			status.Set("hidden", true)
			status.Set("textContent", "")
			if resetLazyFailures(token) {
				hydrate()
			}
		})
		mounted = append(mounted, retry)
		return nil
	})
	if err != nil {
		retry.dispose()
	}
}

// Reset every owned root of a completed failed generation, including a proxy
// whose waiter has not yet published its error. Skipping loading roots would
// strand them after the old waiter correctly declines the replacement channel.
func resetLazyFailures(token *mergeApplicationIdentity) bool {
	if !mergeApplicationCurrent(token) {
		return false
	}
	for _, state := range lazyModules {
		if state.err != nil && !state.loading && !state.exports.Truthy() {
			state.err = nil
			state.ready = nil
		}
	}
	nodes := doc.Call("getElementById", "nextgo-app").Call("querySelectorAll", "[data-nextgo-island]")
	for i := 0; i < nodes.Length(); i++ {
		if !mergeApplicationCurrent(token) {
			return false
		}
		node := nodes.Index(i)
		state := lazyModules[node.Call("getAttribute", "data-nextgo-island").String()]
		phase := node.Call("getAttribute", "data-nextgo-hydrated").String()
		if state != nil && state.ready == nil && (phase == "error" || phase == "loading") {
			disposeWithin(node)
			if !mergeApplicationCurrent(token) {
				return false
			}
			if mergeApplication != nil {
				for scope := range mergeApplication.scopes {
					if scope.closed && node.Call("contains", scope.Root.Value).Bool() {
						delete(mergeApplication.scopes, scope)
					}
				}
			}
			node.Call("removeAttribute", "data-nextgo-hydrated")
		}
	}
	return mergeApplicationCurrent(token)
}

func loadLazyModule(name string, state *lazyState) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	select {
	case lazySlots <- struct{}{}:
		defer func() { <-lazySlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	target := deployment.RuntimeURL(doc.Get("body").Call("getAttribute", "data-nextgo-build").String()) + state.descriptor.File
	if state.url != "" {
		target = state.url
	}
	expected := win.Get("URL").New(target, win.Get("location").Get("href")).Get("href").String()
	response, err := Fetch(ctx, target, FetchOptions{MaxBytes: int64(state.descriptor.Size), Redirect: "error"})
	if err != nil {
		return err
	}
	if response.StatusCode != 200 || response.URL != expected || len(response.Body) != state.descriptor.Size {
		return fmt.Errorf("module response differs from its manifest")
	}
	sum := sha256.Sum256(response.Body)
	if hex.EncodeToString(sum[:]) != state.descriptor.Digest {
		return fmt.Errorf("module digest mismatch")
	}
	return instantiateLazyModule(ctx, name, state, response.Body, js.Undefined())
}

func instantiateLazyModule(ctx context.Context, name string, state *lazyState, body []byte, compiled js.Value) error {
	runtime := win.Get("Go").New()
	controller := win.Get("AbortController").New()
	defer controller.Call("abort")
	var instance js.Value
	if compiled.IsUndefined() || compiled.IsNull() {
		bytes := win.Get("Uint8Array").New(len(body))
		js.CopyBytesToJS(bytes, body)
		result, err := awaitBrowserPromise(ctx, win.Get("WebAssembly").Call("instantiate", bytes, runtime.Get("importObject")), controller)
		if err != nil {
			return err
		}
		instance = result.Get("instance")
	} else {
		var err error
		instance, err = awaitBrowserPromise(ctx, win.Get("WebAssembly").Call("instantiate", compiled, runtime.Get("importObject")), controller)
		if err != nil {
			return err
		}
	}
	managed := devRefreshBuild && devRefreshEnabled() && state.descriptor.StateSchema != ""
	if devRefreshBuild && managed {
		state.runtime = runtime
		state.runDone = make(chan struct{})
	}
	var promise js.Value
	if devRefreshBuild && managed {
		promise = win.Get("__nextgoDevRuntime").Call("start", runtime, instance)
	} else {
		promise = runtime.Call("run", instance)
	}
	if devRefreshBuild && managed {
		state.runPromise = promise
	}
	// Keep settlement callbacks alive for the document lifetime. Returning main
	// unexpectedly invalidates the module; active Go callbacks cannot be unloaded.
	var success, failure js.Func
	finished := false
	settle := func(failed bool) {
		if finished {
			return
		}
		finished = true
		if failed {
			state.runError = fmt.Errorf("module runtime failed")
		}
		if !devRefreshBuild || !state.expectedStop {
			state.err = fmt.Errorf("module runtime exited")
			if failed {
				state.err = state.runError
				if !mergeApplicationRevoked() {
					report("A Go component runtime stopped. Reload the page.")
				}
			}
		}
		if devRefreshBuild && managed {
			close(state.runDone)
		}
		if devRefreshBuild && managed {
			success.Release()
			failure.Release()
		}
		if devRefreshBuild && managed && !state.expectedStop && !mergeApplicationRevoked() {
			devRefresh.reload()
		}
	}
	success = js.FuncOf(func(js.Value, []js.Value) any { settle(false); return nil })
	failure = js.FuncOf(func(js.Value, []js.Value) any { settle(true); return nil })
	if !devRefreshBuild || !managed {
		globalFunctions = append(globalFunctions, success, failure)
	}
	promise.Call("then", success, failure)
	if !state.exports.Truthy() {
		return fmt.Errorf("module did not register its expected ABI")
	}
	return nil
}
func syncLazyModules() {
	if moduleMode {
		return
	}
	for _, state := range lazyModules {
		if state.exports.Truthy() && state.err == nil && !state.expectedStop {
			state.exports.Call("sync", moduleSnapshot())
		}
	}
}
func moduleSnapshot() string {
	encoded, _ := json.Marshal(moduleState{ResourceEpoch: resourceEpoch, Paths: deployment, Public: routeManifest.Public, Location: committedLocation, Selections: selections, Frozen: draftFrozen, Ready: currentURL != "", Revoked: mergeApplicationRevoked() || (mergeCurrent != nil && mergeCurrent.revoked), Configuration: documentConfiguration()})
	return string(encoded)
}
func installModuleHost() {
	if len(lazyModules) == 0 {
		return
	}
	host := win.Get("Object").New()
	host.Set("version", moduleABI)
	bind := func(name string, run func([]js.Value) any) {
		fn := js.FuncOf(func(_ js.Value, args []js.Value) (result any) {
			defer func() {
				if failure := recover(); failure != nil {
					result = fmt.Sprintf("module bridge: %v", failure)
				}
			}()
			if mergeApplicationRevoked() && name != "register" && name != "state" && name != "refreshEnabled" && name != "unobserveLinkStatus" {
				return errPropsClosed.Error()
			}
			if refreshFrozen() && name != "register" && name != "state" && name != "refreshEnabled" && name != "unobserveLinkStatus" {
				return errRefreshFrozen.Error()
			}
			return run(args)
		})
		globalFunctions = append(globalFunctions, fn)
		host.Set(name, fn)
	}
	bind("register", func(args []js.Value) any {
		if len(args) != 4 {
			return "invalid module handshake"
		}
		name, epoch := args[0].String(), args[1].String()
		state := lazyModules[name]
		if state == nil || !state.loading || state.descriptor.Epoch != epoch || args[2].Int() != moduleABI || state.exports.Truthy() {
			return "incompatible module generation"
		}
		exports := args[3]
		for _, method := range []string{"mount", "dispose", "sync", "skipTransition"} {
			if exports.Get(method).Type() != js.TypeFunction {
				return "invalid module exports"
			}
		}
		state.exports = exports
		return ""
	})
	if devRefreshBuild && devRefresh != nil {
		bind("refreshEnabled", func([]js.Value) any {
			return devRefreshEnabled() && devRefresh.initialized && refreshBridgeQualified() && deployment.AssetPrefix == ""
		})
	}
	bind("skipTransition", func([]js.Value) any { skipViewTransition(); return "" })
	bind("script", scriptHostPromise)
	bind("actionView", actionViewHostPromise)
	bind("mutation", mutationHostPromise)
	bind("actionFence", func([]js.Value) any { beginFrameworkAction(); return "" })
	bind("state", func([]js.Value) any { return moduleSnapshot() })
	bind("observeLinkStatus", observeLinkStatusHost)
	bind("unobserveLinkStatus", unobserveLinkStatusHost)
	bind("navigate", func(args []js.Value) any {
		var options NavigationOptions
		if err := json.Unmarshal([]byte(args[1].String()), &options); err != nil {
			return err.Error()
		}
		if err := NavigateWithOptions(args[0].String(), options); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("prefetch", func(args []js.Value) any {
		if err := Prefetch(args[0].String()); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("refresh", func([]js.Value) any { Refresh(); return "" })
	bind("back", func([]js.Value) any {
		if err := Back(); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("forward", func([]js.Value) any {
		if err := Forward(); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("return", func(args []js.Value) any {
		if err := BackOrReplace(args[0].String()); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("invalidate", func([]js.Value) any { invalidateCache(); return "" })
	bind("opener", func([]js.Value) any { return navigationOpener })
	bind("returnFocus", func(args []js.Value) any { modalReturnFocus = args[0]; return "" })
	win.Set(moduleBridgeName, host)
}

func moduleCall(method string, args ...any) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("module host unavailable: %v", failure)
		}
	}()
	result := moduleHost.Call(method, args...)
	if result.Type() == js.TypeString && strings.TrimSpace(result.String()) != "" {
		return fmt.Errorf("%s", result.String())
	}
	return nil
}
