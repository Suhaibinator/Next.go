//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

type refreshExport struct {
	object   js.Value
	name     string
	callback js.Func
}

type moduleRefreshRuntime struct {
	phase           string
	frozen          bool
	done            chan struct{}
	functions       []refreshExport
	stopMotion      func()
	activeCallbacks int
	snapshot        string
	imported        bool
}

var moduleRefresh *moduleRefreshRuntime

// RunModuleRefresh opts a generated development module into managed retirement.
// The generated main must wait for the returned channel and then return. Normal
// production modules continue using RunModule and do not install this extension.
//
// Refreshable components must use the managed lifecycle APIs and explicit state
// cells. Arbitrary application goroutines, timers, syscall/js callbacks and native
// browser work cannot be discovered or preempted by this contract. An unsupported
// or busy framework lifetime requires a full document reload.
func RunModuleRefresh(name, epoch, stateSchema, domSchema string) <-chan struct{} {
	if !moduleMode || moduleRefresh != nil {
		panic("development module refresh requires one generated module runtime")
	}
	// The compiler opts the source into refresh before effective runtime policy
	// (including deployment paths and startup proof) is known. Unsupported hosts
	// must retain ordinary component/state behavior rather than initialize codecs.
	if !moduleRefreshEnabled() {
		runModule(name, epoch, nil)
		return nil
	}
	if err := initRefreshState(name, epoch, stateSchema, domSchema); err != nil {
		panic("invalid development module refresh identity")
	}
	enableRefreshActivity()
	runtime := &moduleRefreshRuntime{phase: devrefresh.PhaseReady, done: make(chan struct{})}
	moduleRefresh = runtime
	refreshModuleFence = func() bool { return runtime.frozen }
	runModule(name, epoch, runtime)
	return runtime.done
}

func moduleRefreshEnabled() (enabled bool) {
	defer func() { _ = recover() }()
	host := win.Get(moduleBridgeName)
	if !host.Truthy() || host.Get("refreshEnabled").Type() != js.TypeFunction {
		return false
	}
	capability := host.Call("refreshEnabled")
	return capability.Type() == js.TypeBoolean && capability.Bool() && refreshBridgeQualified()
}

// Scope.active uses this additional admission fence. Freezing is distinct from
// disposal: state can still be snapshotted before cleanup releases resources.
func moduleRefreshHandlersFrozen() bool {
	return moduleMode && moduleRefresh != nil && moduleRefresh.frozen
}

func (runtime *moduleRefreshRuntime) bind(object js.Value, name string, fn func([]js.Value) any) {
	callback := js.FuncOf(func(_ js.Value, args []js.Value) (result any) {
		runtime.activeCallbacks++
		defer func() {
			runtime.activeCallbacks--
			if recover() != nil {
				// Never return serialized state, props or application panic details.
				result = map[string]any{"error": "development refresh callback failed"}
			}
		}()
		return fn(args)
	})
	runtime.functions = append(runtime.functions, refreshExport{object, name, callback})
	object.Set(name, callback)
}

func (runtime *moduleRefreshRuntime) install(exports js.Value) {
	extension := win.Get("Object").New()
	extension.Set("version", devrefresh.ExtensionVersion)
	runtime.bind(extension, "status", func(args []js.Value) any {
		if len(args) != 0 {
			return ""
		}
		data, _ := json.Marshal(runtime.status())
		return string(data)
	})
	runtime.bind(extension, "prepare", func(args []js.Value) any {
		if len(args) != 0 {
			return map[string]any{"error": "invalid development refresh prepare", "snapshot": ""}
		}
		snapshot, err := runtime.prepare()
		if err != nil {
			return map[string]any{"error": "development refresh requires a document reload", "snapshot": ""}
		}
		return map[string]any{"error": "", "snapshot": snapshot}
	})
	runtime.bind(extension, "stop", func(args []js.Value) any {
		if len(args) != 0 || runtime.stop() != nil {
			return "development refresh module cannot stop"
		}
		return ""
	})
	runtime.bind(extension, "import", func(args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString || runtime.restore(args[0].String()) != nil {
			return "development refresh state cannot be imported"
		}
		return ""
	})
	runtime.bind(extension, "claim", func(args []js.Value) any {
		if len(args) != 0 || runtime.phase != devrefresh.PhaseReady || runtime.frozen || !runtime.imported || finishRefreshStateClaims() != nil {
			return "development refresh state claims differ"
		}
		return ""
	})
	exports.Set("refresh", extension)
}

func (runtime *moduleRefreshRuntime) status() devrefresh.Status {
	activity := refreshActivityState()
	return devrefresh.Status{Version: devrefresh.ExtensionVersion, Phase: runtime.phase, Pending: activity.Busy, Scopes: len(moduleScopes), Frozen: runtime.frozen}
}

func (runtime *moduleRefreshRuntime) prepare() (string, error) {
	if runtime.phase == devrefresh.PhasePrepared {
		return runtime.snapshot, nil
	}
	if runtime.phase != devrefresh.PhaseReady || runtime.frozen || refreshActivityState().Busy != 0 || len(globalFunctions) != 0 || activeTransition != nil || runtime.activeCallbacks > 1 {
		return "", fmt.Errorf("module runtime is busy or has unowned callbacks")
	}
	for _, scope := range moduleScopes {
		if !refreshScopeSupported(scope) || !refreshModuleDOMSupported(scope) {
			return "", fmt.Errorf("module scope requires a document reload")
		}
	}
	if err := refreshStateEligibility(); err != nil {
		return "", err
	}
	runtime.frozen = true
	if activity := freezeRefreshActivity(); activity.Busy != 0 {
		return "", fmt.Errorf("module physical work changed during prepare")
	}
	snapshot, err := snapshotRefreshState(moduleScopes)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", fmt.Errorf("module state snapshot failed")
	}
	if _, err := devrefresh.ParseModuleSnapshot(data); err != nil {
		return "", fmt.Errorf("module state snapshot is incompatible")
	}
	runtime.snapshot = string(data)
	runtime.phase = devrefresh.PhasePrepared
	return runtime.snapshot, nil
}

func refreshModuleDOMSupported(scope *Scope) (supported bool) {
	defer func() { _ = recover() }()
	root := scope.Root.Value
	if !root.Truthy() || root.Get("nodeType").Int() != 1 {
		return false
	}
	switch root.Get("tagName").String() {
	case "FORM", "SELECT", "TEXTAREA", "DIALOG":
		return false
	}
	if !root.Call("closest", "form,[contenteditable]").IsNull() || !root.Call("querySelector", "form,select,textarea,dialog,[contenteditable]").IsNull() {
		return false
	}
	validInput := func(input js.Value) bool {
		kind := input.Get("type").String()
		return (kind == "text" || kind == "checkbox") && input.Get("form").IsNull()
	}
	if root.Get("tagName").String() == "INPUT" && !validInput(root) {
		return false
	}
	inputs := root.Call("querySelectorAll", "input")
	if inputs.Length() > 256 {
		return false
	}
	for i := 0; i < inputs.Length(); i++ {
		if !validInput(inputs.Index(i)) {
			return false
		}
	}
	return true
}

func (runtime *moduleRefreshRuntime) restore(raw string) error {
	if runtime.phase != devrefresh.PhaseReady || runtime.frozen || runtime.imported || len(moduleScopes) != 0 || len(raw) > devrefresh.MaxStateBytes {
		return fmt.Errorf("module cannot accept state now")
	}
	snapshot, err := devrefresh.ParseModuleSnapshot([]byte(raw))
	if err != nil {
		return err
	}
	if err := importRefreshState(snapshot); err != nil {
		return err
	}
	runtime.imported = true
	return nil
}

func (runtime *moduleRefreshRuntime) stop() error {
	if runtime.phase == devrefresh.PhaseStopping {
		return nil
	}
	if runtime.phase != devrefresh.PhasePrepared || !runtime.frozen {
		return fmt.Errorf("module must prepare before stopping")
	}
	runtime.phase = devrefresh.PhaseStopping
	go runtime.retire()
	return nil
}

func (runtime *moduleRefreshRuntime) retire() {
	// Any failure leaves main waiting and exports alive. The core coordinator
	// times out and reloads; it must never initialize a successor on this path.
	defer func() { _ = recover() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	freezeRefreshActivity()
	handles := make([]int, 0, len(moduleScopes))
	for handle := range moduleScopes {
		handles = append(handles, handle)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(handles)))
	for _, handle := range handles {
		moduleScopes[handle].dispose()
		delete(moduleScopes, handle)
	}
	if runtime.stopMotion != nil {
		runtime.stopMotion()
		runtime.stopMotion = nil
	}
	if err := waitRefreshActivity(ctx); err != nil {
		return
	}
	// A browser timer guarantees the stop export's JS stack has returned before
	// callbacks are released. A Go scheduler yield alone does not establish that.
	for {
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
		if runtime.activeCallbacks == 0 {
			break
		}
	}
	if len(globalFunctions) != 0 || refreshActivityState().Busy != 0 {
		return
	}
	for i := len(runtime.functions) - 1; i >= 0; i-- {
		function := runtime.functions[i]
		function.object.Set(function.name, js.Undefined())
		function.callback.Release()
	}
	runtime.functions = nil
	runtime.snapshot = ""
	resetRefreshState()
	moduleHost = js.Undefined()
	navigationOpener = js.Undefined()
	runtime.phase = devrefresh.PhaseStopped
	cancel()
	close(runtime.done)
}

func refreshSourceHandle(value js.Value) (int, error) {
	if value.Type() != js.TypeNumber {
		return 0, fmt.Errorf("invalid source handle")
	}
	number := value.Float()
	if number < 1 || number > math.MaxInt32 || math.Trunc(number) != number || math.IsNaN(number) {
		return 0, fmt.Errorf("invalid source handle")
	}
	return int(number), nil
}
