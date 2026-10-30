//go:build js && wasm

package client

import (
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// The malformed manifest deliberately ends Run after the existing host/lifecycle
// installation point. Empty DOM collections avoid mounting or starting work.
func moduleHostRunDocument(t *testing.T, earlyCount int) (func() js.Value, *int) {
	t.Helper()
	oldWin, oldDoc := win, doc
	oldModules, oldRegistry, oldInstaller := lazyModules, registry, registeredModuleHostInstaller
	oldFunctions, oldManifest, oldDeployment := globalFunctions, routeManifest, deployment
	oldLocation, oldSelections, oldSelectionObservers := committedLocation, selections, selectionObservers
	oldLocationObservers, oldModuleHost, oldMounted := locationObservers, moduleHost, mounted
	oldActionFrozen, oldDraftFrozen := actionViewFrozen, draftFrozen
	oldRefresh, oldURL := devRefresh, currentURL
	oldScripts, oldAliases, oldSources := scriptLoads, scriptAliases, scriptSources
	globalFunctions = nil
	lazyModules, registry = map[string]*lazyState{}, map[string]Mount{}
	registeredModuleHostInstaller = nil
	currentURL, committedLocation = "", Location{}
	locationObservers = map[uint64]*locationObserver{}
	moduleHost, mounted = js.Undefined(), nil
	actionViewFrozen, draftFrozen = false, false
	routeManifest = navigation.Manifest{}
	selections, selectionObservers = map[string]Selection{}, map[uint64]selectionObserver{}
	devRefresh = nil
	scriptLoads, scriptAliases, scriptSources = map[string]*scriptLoad{}, map[string]string{}, map[string]string{}
	var callbacks []js.Func
	t.Cleanup(func() {
		for _, callback := range globalFunctions {
			callback.Release()
		}
		win, doc = oldWin, oldDoc
		lazyModules, registry, registeredModuleHostInstaller = oldModules, oldRegistry, oldInstaller
		globalFunctions, routeManifest, deployment = oldFunctions, oldManifest, oldDeployment
		committedLocation, selections, selectionObservers = oldLocation, oldSelections, oldSelectionObservers
		locationObservers, moduleHost, mounted = oldLocationObservers, oldModuleHost, oldMounted
		actionViewFrozen, draftFrozen = oldActionFrozen, oldDraftFrozen
		devRefresh, currentURL = oldRefresh, oldURL
		scriptLoads, scriptAliases, scriptSources = oldScripts, oldAliases, oldSources
		for _, callback := range callbacks {
			callback.Release()
		}
	})
	bind := func(fn func(js.Value, []js.Value) any) js.Func {
		callback := js.FuncOf(fn)
		callbacks = append(callbacks, callback)
		return callback
	}
	object := js.Global().Get("Object")
	window, document, body, app, status := object.New(), object.New(), object.New(), object.New(), object.New()
	attributes := map[string]string{"data-nextgo-navigation": "{"}
	body.Set("getAttribute", bind(func(_ js.Value, args []js.Value) any {
		if value, ok := attributes[args[0].String()]; ok {
			return value
		}
		return js.Null()
	}))
	body.Set("setAttribute", bind(func(_ js.Value, args []js.Value) any {
		attributes[args[0].String()] = args[1].String()
		return nil
	}))
	empty := js.Global().Get("Array").New()
	app.Set("querySelectorAll", bind(func(js.Value, []js.Value) any { return empty }))
	document.Set("querySelectorAll", bind(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "script[data-nextgo-early]" {
			return js.Global().Get("Array").New(earlyCount)
		}
		return empty
	}))
	document.Set("getElementById", bind(func(_ js.Value, args []js.Value) any {
		switch args[0].String() {
		case "nextgo-app":
			return app
		case "nextgo-status":
			return status
		}
		return js.Null()
	}))
	document.Set("body", body)
	window.Set("Object", object)
	window.Set("URL", js.Global().Get("URL"))
	window.Set("location", js.ValueOf(map[string]any{"href": "https://site.test/", "origin": "https://site.test"}))
	window.Set("addEventListener", bind(func(js.Value, []js.Value) any { return nil }))
	console := object.New()
	console.Set("error", bind(func(js.Value, []js.Value) any { return nil }))
	window.Set("console", console)
	bridge, installs := js.Undefined(), 0
	object.Call("defineProperty", window, moduleBridgeName, js.ValueOf(map[string]any{
		"get": bind(func(js.Value, []js.Value) any { return bridge }),
		"set": bind(func(_ js.Value, args []js.Value) any { installs++; bridge = args[0]; return nil }),
	}))
	win, doc = window, document
	return func() js.Value { return bridge }, &installs
}

func moduleHostTestDescriptor() LazyModule {
	digest := strings.Repeat("a", 64)
	return LazyModule{File: "module-" + digest + ".wasm", Digest: digest, Epoch: strings.Repeat("b", 64), Size: 123}
}

func TestModuleHostRegistrationRunLeavesNoDescriptorBridgeAbsent(t *testing.T) {
	if moduleMode {
		t.Skip("document Run belongs to the core target")
	}
	bridge, installs := moduleHostRunDocument(t, 0)
	Run()
	if *installs != 0 || !bridge().IsUndefined() || registeredModuleHostInstaller != nil {
		t.Fatal("a document without lazy descriptors installed a bridge")
	}
	if doc.Get("body").Call("getAttribute", "data-nextgo-hydration").String() != "ready" {
		t.Fatal("Run did not reach its existing manifest-error hydration fallback")
	}
}

func TestModuleHostRegistrationRunInstallsExistingABIOnce(t *testing.T) {
	if moduleMode {
		t.Skip("document Run belongs to the core target")
	}
	bridge, installs := moduleHostRunDocument(t, 0)
	RegisterLazy("one", moduleHostTestDescriptor())
	RegisterLazy("two", moduleHostTestDescriptor())
	if *installs != 0 || !bridge().IsUndefined() {
		t.Fatal("registration installed the host before Run")
	}
	Run()
	if *installs != 1 || bridge().Get("version").Int() != 6 {
		t.Fatal("Run did not install one existing ABI6 host", *installs)
	}
	location, ready := CurrentLocation()
	if !ready || location.URL != "https://site.test/" || currentURL != "" {
		t.Fatal("committed document readiness enabled or inherited a SPA router", location, ready, currentURL)
	}
	var snapshot moduleState
	if err := json.Unmarshal([]byte(bridge().Call("state").String()), &snapshot); err != nil || snapshot.Location.URL != location.URL || snapshot.Ready {
		t.Fatal("ABI6 native-document location and router readiness diverged", snapshot, err)
	}
	for _, name := range []string{"register", "skipTransition", "script", "actionView", "mutation", "actionFence", "state", "observeLinkStatus", "unobserveLinkStatus", "navigate", "prefetch", "refresh", "back", "forward", "return", "invalidate", "opener", "returnFocus"} {
		if bridge().Get(name).Type() != js.TypeFunction {
			t.Fatalf("ABI6 method %q changed or disappeared", name)
		}
	}
}

func TestModuleHostRegistrationFailureDoesNotEnableInstaller(t *testing.T) {
	if moduleMode {
		t.Skip("document Run belongs to the core target")
	}
	bridge, installs := moduleHostRunDocument(t, 0)
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		RegisterLazy("bad", LazyModule{})
	}()
	if !panicked || registeredModuleHostInstaller != nil || len(lazyModules) != 0 {
		t.Fatal("invalid registration changed host admission")
	}
	Run()
	if *installs != 0 || !bridge().IsUndefined() {
		t.Fatal("invalid registration enabled the host")
	}
	t.Run("eager-name-collision", func(t *testing.T) {
		bridge, installs := moduleHostRunDocument(t, 0)
		Register("eager", func(*Scope, json.RawMessage) error { return nil })
		panicked := false
		func() {
			defer func() { panicked = recover() != nil }()
			RegisterLazy("eager", moduleHostTestDescriptor())
		}()
		if !panicked || registeredModuleHostInstaller != nil || len(lazyModules) != 0 {
			t.Fatal("eager collision changed host admission")
		}
		Run()
		if *installs != 0 || !bridge().IsUndefined() {
			t.Fatal("eager collision enabled the host")
		}
	})
}

func TestModuleHostRegistrationPreservesEarlyFailureAndLateRegistration(t *testing.T) {
	if moduleMode {
		t.Skip("document Run belongs to the core target")
	}
	t.Run("early-script-validation-precedes-host", func(t *testing.T) {
		bridge, installs := moduleHostRunDocument(t, 17)
		RegisterLazy("one", moduleHostTestDescriptor())
		Run()
		if *installs != 0 || !bridge().IsUndefined() {
			t.Fatal("host moved before early script validation")
		}
	})
	t.Run("late-registration-does-not-install", func(t *testing.T) {
		bridge, installs := moduleHostRunDocument(t, 0)
		Run()
		RegisterLazy("late", moduleHostTestDescriptor())
		if *installs != 0 || !bridge().IsUndefined() {
			t.Fatal("late registration acquired new implicit installation behavior")
		}
	})
}

func TestModuleHostRegistrationPreservesDocumentModuleOwnership(t *testing.T) {
	oldInstaller := registeredModuleHostInstaller
	t.Cleanup(func() { registeredModuleHostInstaller = oldInstaller })
	called := 0
	registeredModuleHostInstaller = func() { called++ }
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		if moduleMode {
			Run()
		} else {
			RunModule("one", strings.Repeat("b", 64))
		}
	}()
	if !panicked || called != 0 {
		t.Fatal("wrong target acquired document ownership", panicked, called)
	}
}
