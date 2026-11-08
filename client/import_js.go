//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/scripts"
)

type importedModule struct {
	done      chan struct{}
	namespace js.Value
	err       error
}

var importedModules map[string]*importedModule
var importAliases map[string]string
var importWaiters, importHostWaiters int

const maxImportedModules, maxImportAliases, maxImportWaiters = 32, 128, 256

// ImportModule returns a native module namespace, including default and named
// exports. Call from a goroutine. Root modules, aliases and waiters are bounded
// by the document core, shared with lazy Go components. Native import owns
// transitive dependencies/evaluation and cannot be aborted or unloaded.
// Cancellation stops this consumer, not execution/physical activity. Failed
// URLs remain reserved; retry requires a new versioned URL and identity.
func ImportModule(ctx context.Context, input scripts.Module) (namespace js.Value, err error) {
	namespace = js.Undefined()
	if ctx == nil {
		return namespace, fmt.Errorf("modules: context is required")
	}
	if err := ctx.Err(); err != nil {
		return namespace, err
	}
	checked, err := input.Check()
	if err != nil {
		return namespace, err
	}
	activity, err := beginRefreshActivity()
	if err != nil {
		return namespace, err
	}
	defer activity.Done()
	defer func() {
		if recover() != nil {
			namespace, err = js.Undefined(), fmt.Errorf("modules: browser import setup failed")
		}
	}()
	if moduleMode {
		data, _ := json.Marshal(checked)
		promise := moduleHost.Call("importModule", string(data))
		controller := win.Get("AbortController").New()
		defer controller.Call("abort")
		holder, err := awaitBrowserPromise(ctx, promise, controller)
		if err != nil {
			return namespace, err
		}
		return holder.Get("namespace"), nil
	}
	if currentURL == "" {
		return namespace, fmt.Errorf("modules: document runtime has not started")
	}
	if importWaiters >= maxImportWaiters {
		return namespace, fmt.Errorf("modules: waiter limit reached")
	}
	resolved, err := resolveScript(scripts.Script{ID: checked.ID, Src: checked.Src, Asset: checked.Asset, Type: "module"})
	if err != nil {
		return namespace, err
	}
	if old, exists := importAliases[checked.ID]; exists && old != resolved.Src {
		return namespace, fmt.Errorf("modules: ID already names another source")
	}
	if scriptSources[resolved.Src] != "" {
		return namespace, fmt.Errorf("modules: source already belongs to the script loader")
	}
	if _, exists := importAliases[checked.ID]; !exists && len(importAliases) >= maxImportAliases {
		return namespace, fmt.Errorf("modules: document alias limit reached")
	}
	state := importedModules[resolved.Src]
	if state == nil && len(importedModules) >= maxImportedModules {
		return namespace, fmt.Errorf("modules: document root-module limit reached")
	}
	bridge := win.Get("__nextgoImportModule")
	if state == nil && bridge.Type() != js.TypeFunction {
		return namespace, fmt.Errorf("modules: matching generated bootstrap is required")
	}
	if importedModules == nil {
		importedModules = map[string]*importedModule{}
		importAliases = map[string]string{}
	}
	importAliases[checked.ID] = resolved.Src
	if state == nil {
		state = &importedModule{done: make(chan struct{}), namespace: js.Undefined()}
		importedModules[resolved.Src] = state
		physical := retainRefreshActivity()
		go func() {
			defer physical.Done()
			defer close(state.done)
			defer func() {
				if recover() != nil {
					state.err = fmt.Errorf("modules: browser import failed")
				}
			}()
			controller := win.Get("AbortController").New()
			defer controller.Call("abort")
			// One physical waiter per URL survives consumer cancellation and
			// top-level await; native import has no AbortSignal option.
			state.namespace, state.err = awaitBrowserPromise(context.Background(), bridge.Invoke(resolved.Src), controller)
			if state.err == nil && (state.namespace.Type() != js.TypeObject || state.namespace.IsNull()) {
				state.namespace, state.err = js.Undefined(), fmt.Errorf("modules: import did not return a namespace")
			}
		}()
	}
	importWaiters++
	defer func() { importWaiters-- }()
	select {
	case <-state.done:
		if err := ctx.Err(); err != nil {
			return namespace, err
		}
		return state.namespace, state.err
	case <-ctx.Done():
		return namespace, ctx.Err()
	}
}

func importModuleHostPromise(args []js.Value) any {
	var executor js.Func
	executor = js.FuncOf(func(_ js.Value, callbacks []js.Value) any {
		resolve, reject := callbacks[0], callbacks[1]
		if len(args) != 1 || args[0].Type() != js.TypeString {
			reject.Invoke("modules: invalid host request")
			return nil
		}
		if importHostWaiters >= maxImportWaiters {
			reject.Invoke("modules: host waiter limit reached")
			return nil
		}
		activity, err := beginRefreshActivity()
		if err != nil {
			reject.Invoke(err.Error())
			return nil
		}
		importHostWaiters++
		raw := args[0].String()
		go func() {
			defer activity.Done()
			defer func() { importHostWaiters-- }()
			module, err := scripts.ParseModule(raw)
			value := js.Undefined()
			if err == nil {
				value, err = ImportModule(context.Background(), module)
			}
			if err != nil {
				reject.Invoke(err.Error())
			} else {
				// Avoid an additional Promise assimilation if an export is
				// named "then". Native import's own semantics apply upstream.
				holder := win.Get("Object").New()
				holder.Set("namespace", value)
				resolve.Invoke(holder)
			}
		}()
		return nil
	})
	promise := win.Get("Promise").New(executor)
	executor.Release()
	return promise
}
