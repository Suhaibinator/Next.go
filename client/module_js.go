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
	Revoked       bool
	Configuration string
}

func syncModule(raw string) error {
	var state moduleState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return err
	}
	// A recognized revocation fences every owned scope before any validation
	// error, resource invalidation or observer can run authored work.
	if state.Revoked {
		syncModuleAuthority(true)
	}
	if state.Configuration != documentConfiguration() {
		return fmt.Errorf("module configuration changed")
	}
	if err := state.Paths.Validate(); err != nil {
		return err
	}
	if !state.Revoked {
		syncModuleAuthority(false)
	}
	setResourceEpoch(state.ResourceEpoch)
	old := selections
	deployment = state.Paths
	routeManifest.Public = state.Public
	committedLocation = state.Location
	propsLocationChanged(committedLocation)
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
	runModule(name, epoch, nil)
}

func runModule(name, epoch string, refresh *moduleRefreshRuntime) {
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
	if !devRefreshBuild || refresh == nil {
		installMotionPreference()
	} else {
		refresh.stopMotion = installMotionPreferenceOwned()
	}
	exports := win.Get("Object").New()
	bind := func(name string, fn func([]js.Value) any) {
		if devRefreshBuild && refresh != nil {
			refresh.bind(exports, name, fn)
			return
		}
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
		if devRefreshBuild && refresh != nil && refresh.frozen {
			return "development refresh has frozen this module"
		}
		if activeTransition != nil {
			skipTransition(activeTransition)
		}
		return ""
	})
	bind("sync", func(args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return "module sync is unavailable"
		}
		if err := syncModuleExport(args[0].String(), devRefreshBuild && refresh != nil && refresh.frozen); err != nil {
			return err.Error()
		}
		return ""
	})
	bind("mount", func(args []js.Value) (result any) {
		previousHandle := 0
		var work *refreshActivityLease
		if devRefreshBuild && refresh != nil {
			if refresh.frozen || len(args) != 3 && len(args) != 4 || args[0].Type() != js.TypeObject || args[0].IsNull() || args[1].Type() != js.TypeString {
				return map[string]any{"error": "development refresh module mount is unavailable"}
			}
			if len(args) == 4 {
				var err error
				previousHandle, err = refreshSourceHandle(args[3])
				if err != nil {
					return map[string]any{"error": "invalid development refresh source handle"}
				}
			}
			mounting := refreshModuleMounting
			refreshModuleMounting = true
			defer func() { refreshModuleMounting = mounting }()
			var err error
			work, err = beginRefreshActivity()
			if err != nil {
				return map[string]any{"error": "development refresh module mount is frozen"}
			}
			defer work.Done()
		}
		if err := syncModule(moduleHost.Call("state").String()); err != nil {
			return map[string]any{"error": err.Error()}
		}
		navigationOpener = args[2]
		scope := &Scope{Root: Element{args[0]}}
		releaseAuthority, err := beginModuleMountAuthority(scope)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		defer releaseAuthority()
		mount := registry[name]
		if mount == nil {
			return map[string]any{"error": "module component is unregistered"}
		}
		if devRefreshBuild && refresh != nil {
			if err := bindRefreshScope(scope, nextModuleScope+1, args[1].String(), previousHandle); err != nil {
				scope.dispose()
				return map[string]any{"error": "development refresh scope identity differs"}
			}
		}
		if err := mountScoped(scope, mount, json.RawMessage(args[1].String())); err != nil {
			scope.dispose()
			if devRefreshBuild && refresh != nil {
				return map[string]any{"error": "development refresh component mount failed"}
			}
			return map[string]any{"error": err.Error()}
		}
		nextModuleScope++
		moduleScopes[nextModuleScope] = scope
		return map[string]any{"id": nextModuleScope, "error": ""}
	})
	bind("dispose", func(args []js.Value) any {
		if devRefreshBuild && refresh != nil && (refresh.frozen || len(args) != 1 || args[0].Type() != js.TypeNumber) {
			return "development refresh module disposal is unavailable"
		}
		id := args[0].Int()
		if scope := moduleScopes[id]; scope != nil {
			scope.dispose()
			delete(moduleScopes, id)
		}
		return ""
	})
	if devRefreshBuild && refresh != nil {
		refresh.install(exports)
	}
	if failure := moduleHost.Call("register", name, epoch, moduleABI, exports).String(); failure != "" {
		panic(failure)
	}
}
