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
}
type lazyState struct {
	descriptor LazyModule
	ready      chan struct{}
	exports    js.Value
	err        error
	loading    bool
}

var lazyModules = map[string]*lazyState{}
var lazyRetryScope *Scope

var lazySlots = make(chan struct{}, 2)

const moduleABI = 4
const moduleBridgeName = "__nextgoModuleHost"

func RegisterLazy(name string, module LazyModule) {
	if registry[name] != nil || lazyModules[name] != nil || len(lazyModules) >= 8 || !ascii.LowerHex(module.Digest, 64) || module.File != "module-"+module.Digest+".wasm" || !ascii.LowerHex(module.Epoch, 64) || module.Size <= 0 || module.Size > 8<<20 {
		panic("invalid lazy component registration")
	}
	lazyModules[name] = &lazyState{descriptor: module, exports: js.Undefined()}
}
func lazyHydrate(scope *Scope, name string) {
	root := scope.Root.Value
	opener := navigationOpener
	state := lazyModules[name]
	root.Call("setAttribute", "data-nextgo-hydrated", "loading")
	if !state.loading && state.ready == nil {
		state.loading = true
		state.ready = make(chan struct{})
		go func() {
			defer close(state.ready)
			defer func() {
				state.loading = false
				if failure := recover(); failure != nil {
					state.err = fmt.Errorf("module startup failed: %v", failure)
				}
			}()
			state.err = loadLazyModule(name, state)
		}()
	}
	go func() {
		defer func() {
			if failure := recover(); failure != nil && !scope.closed && root.Get("isConnected").Bool() {
				root.Call("setAttribute", "data-nextgo-hydrated", "error")
				report(fmt.Sprintf("Component hydration failed: %v", failure))
			}
		}()
		<-state.ready
		if scope.closed || !root.Get("isConnected").Bool() {
			return
		}
		if state.err != nil {
			root.Call("setAttribute", "data-nextgo-hydrated", "error")
			showLazyError(root)
			hydrate()
			return
		}
		result := state.exports.Call("mount", root, root.Call("getAttribute", "data-nextgo-props").String(), opener)
		if failure := result.Get("error"); failure.Type() == js.TypeString && failure.String() != "" {
			root.Call("setAttribute", "data-nextgo-hydrated", "error")
			report("Component hydration failed: " + failure.String())
			hydrate()
			return
		}
		handle := result.Get("id").Int()
		scope.Cleanup(func() { state.exports.Call("dispose", handle) })
		root.Call("setAttribute", "data-nextgo-hydrated", "true")
		hydrate()
	}()
}
func showLazyError(root js.Value) {
	if lazyRetryScope != nil {
		return
	}
	report("A Go component could not load. Reload or retry its download.")
	status := doc.Call("getElementById", "nextgo-status")
	button := doc.Call("createElement", "button")
	button.Set("type", "button")
	button.Set("textContent", "Retry component")
	button.Call("setAttribute", "data-nextgo-lazy-retry", "")
	status.Call("appendChild", button)
	retry := &Scope{Root: Element{root}}
	lazyRetryScope = retry
	retry.Cleanup(func() {
		button.Call("remove")
		if lazyRetryScope == retry {
			lazyRetryScope = nil
		}
	})
	retry.On(Element{button}, "click", func(Event) {
		retry.dispose()
		status.Set("hidden", true)
		status.Set("textContent", "")
		for _, state := range lazyModules {
			if state.err != nil && !state.exports.Truthy() {
				state.err = nil
				state.ready = nil
			}
		}
		nodes := doc.Call("querySelectorAll", "[data-nextgo-island]")
		for i := 0; i < nodes.Length(); i++ {
			node := nodes.Index(i)
			state := lazyModules[node.Call("getAttribute", "data-nextgo-island").String()]
			if state != nil && state.ready == nil && node.Call("getAttribute", "data-nextgo-hydrated").String() == "error" {
				disposeWithin(node)
				node.Call("removeAttribute", "data-nextgo-hydrated")
			}
		}
		hydrate()
	})
	mounted = append(mounted, retry)
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
	bytes := win.Get("Uint8Array").New(len(response.Body))
	js.CopyBytesToJS(bytes, response.Body)
	runtime := win.Get("Go").New()
	controller := win.Get("AbortController").New()
	defer controller.Call("abort")
	result, err := awaitBrowserPromise(ctx, win.Get("WebAssembly").Call("instantiate", bytes, runtime.Get("importObject")), controller)
	if err != nil {
		return err
	}
	promise := runtime.Call("run", result.Get("instance"))
	// Keep settlement callbacks alive for the document lifetime. Returning main
	// unexpectedly invalidates the module; active Go callbacks cannot be unloaded.
	success := js.FuncOf(func(js.Value, []js.Value) any { state.err = fmt.Errorf("module runtime exited"); return nil })
	failure := js.FuncOf(func(js.Value, []js.Value) any {
		state.err = fmt.Errorf("module runtime failed")
		report("A Go component runtime stopped. Reload the page.")
		return nil
	})
	globalFunctions = append(globalFunctions, success, failure)
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
		if state.exports.Truthy() && state.err == nil {
			state.exports.Call("sync", moduleSnapshot())
		}
	}
}
func moduleSnapshot() string {
	encoded, _ := json.Marshal(moduleState{ResourceEpoch: resourceEpoch, Paths: deployment, Public: routeManifest.Public, Location: committedLocation, Selections: selections, Frozen: draftFrozen, Ready: currentURL != "", Configuration: documentConfiguration()})
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
	bind("skipTransition", func([]js.Value) any { skipViewTransition(); return "" })
	bind("script", scriptHostPromise)
	bind("actionView", actionViewHostPromise)
	bind("mutation", mutationHostPromise)
	bind("actionFence", func([]js.Value) any { beginFrameworkAction(); return "" })
	bind("state", func([]js.Value) any { return moduleSnapshot() })
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
