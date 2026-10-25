//go:build js && wasm

package client

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

var moduleHost js.Value
var moduleScopes = map[int]*Scope{}
var nextModuleScope int

type moduleState struct {
	ResourceEpoch uint64
	Paths         paths.Config
	Public        map[string]string
	Location      Location
	Selections    map[string]Selection
	Frozen, Ready bool
	Configuration string
}

func syncModule(raw string) error {
	var state moduleState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return err
	}
	if state.Configuration != documentConfiguration() {
		return fmt.Errorf("module configuration changed")
	}
	if err := state.Paths.Validate(); err != nil {
		return err
	}
	setResourceEpoch(state.ResourceEpoch)
	old := selections
	deployment = state.Paths
	routeManifest.Public = state.Public
	committedLocation = state.Location
	selections = state.Selections
	draftFrozen = state.Frozen
	if state.Ready {
		currentURL = state.Location.URL
	}
	for _, observer := range selectionObservers {
		before, was := old[observer.scope]
		after, now := selections[observer.scope]
		if was != now || !before.Equal(after) {
			dispatchSelection(observer)
		}
	}
	notifyLocationObservers()
	return nil
}

// RunModule starts the compiler-generated island runtime without installing a
// second router or document observer. Go values remain inside this instance;
// only DOM references, JSON snapshots and numeric scope handles cross the ABI.
func RunModule(name, epoch string) {
	if !moduleMode {
		panic("client.RunModule requires the generated nextgo_module build target")
	}
	moduleHost = win.Get(moduleBridgeName)
	if !moduleHost.Truthy() || moduleHost.Get("version").Int() != moduleABI {
		panic("incompatible Go component host")
	}
	if err := syncModule(moduleHost.Call("state").String()); err != nil {
		panic(err)
	}
	installMotionPreference()
	exports := win.Get("Object").New()
	bind := func(name string, fn func([]js.Value) any) {
		callback := js.FuncOf(func(_ js.Value, args []js.Value) (result any) {
			defer func() {
				if failure := recover(); failure != nil {
					result = map[string]any{"error": fmt.Sprint(failure)}
				}
			}()
			return fn(args)
		})
		globalFunctions = append(globalFunctions, callback)
		exports.Set(name, callback)
	}
	bind("skipTransition", func([]js.Value) any {
		if activeTransition != nil {
			skipTransition(activeTransition)
		}
		return ""
	})
	bind("sync", func(args []js.Value) any {
		if err := syncModule(args[0].String()); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("mount", func(args []js.Value) (result any) {
		if err := syncModule(moduleHost.Call("state").String()); err != nil {
			return map[string]any{"error": err.Error()}
		}
		navigationOpener = args[2]
		scope := &Scope{Root: Element{args[0]}}
		mount := registry[name]
		if mount == nil {
			return map[string]any{"error": "module component is unregistered"}
		}
		if err := mountScoped(scope, mount, json.RawMessage(args[1].String())); err != nil {
			scope.dispose()
			return map[string]any{"error": err.Error()}
		}
		nextModuleScope++
		moduleScopes[nextModuleScope] = scope
		return map[string]any{"id": nextModuleScope, "error": ""}
	})
	bind("dispose", func(args []js.Value) any {
		id := args[0].Int()
		if scope := moduleScopes[id]; scope != nil {
			scope.dispose()
			delete(moduleScopes, id)
		}
		return ""
	})
	if failure := moduleHost.Call("register", name, epoch, moduleABI, exports).String(); failure != "" {
		panic(failure)
	}
}
