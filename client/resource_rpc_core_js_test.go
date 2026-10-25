//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

func TestResourceMutationCoreAndModuleRPCFenceDispatchAndSettlement(t *testing.T) {
	for _, transport := range []string{"core", "module-rpc"} {
		for _, failure := range []bool{false, true} {
			t.Run(transport+map[bool]string{true: "-failed", false: "-accepted"}[failure], func(t *testing.T) {
				actionViewEnvironment(t)
				coreMutationQueue().reopen()
				win.Set("Promise", js.Global().Get("Promise"))
				win.Set("Object", js.Global().Get("Object"))
				scope := &Scope{}
				t.Cleanup(scope.dispose)
				store, _ := scope.ResourceStore(ResourceStoreOptions{})
				source := resourceSource(t, store, "public", time.Minute, func(context.Context) ([]byte, error) { return []byte("read while pending"), nil })
				resource, err := scope.AsyncResource(source, AsyncResourceOptions{Seed: &ResourceSeed{Key: "public", Body: []byte("before"), ExpiresUnixMilli: time.Now().Add(30 * time.Second).UnixMilli()}})
				if err != nil {
					t.Fatal(err)
				}
				oldLazy := lazyModules
				t.Cleanup(func() { lazyModules = oldLazy })
				var epochs []uint64
				sync := js.FuncOf(func(_ js.Value, args []js.Value) any {
					var state moduleState
					if err := json.Unmarshal([]byte(args[0].String()), &state); err != nil {
						t.Error(err)
					}
					epochs = append(epochs, state.ResourceEpoch)
					return ""
				})
				defer sync.Release()
				exports := js.Global().Get("Object").New()
				exports.Set("sync", sync)
				lazyModules = map[string]*lazyState{"observer": {exports: exports}}
				before := resourceEpoch
				held := newControlledPromise()
				started := make(chan struct{}, 1)
				mockBrowserFetch(t, func([]js.Value) any {
					if resource.Snapshot().Phase != ResourceInvalidated {
						t.Error("dispatch retained old resource")
					}
					started <- struct{}{}
					return held.value
				})
				result := make(chan error, 1)
				go func() {
					if transport == "core" {
						result <- DispatchMutation(context.Background(), "/app/_nextgo/actions/save/v1", FetchOptions{}, func(Response) error { return nil })
						return
					}
					metadata, _ := json.Marshal(moduleActionViewRequest{Method: "POST", MaxBytes: 2048})
					validate := js.FuncOf(func(js.Value, []js.Value) any { return "" })
					defer validate.Release()
					controller := js.Global().Get("AbortController").New()
					promise := mutationHostPromise([]js.Value{js.ValueOf("/app/_nextgo/actions/save/v1"), js.ValueOf(string(metadata)), js.Undefined(), validate.Value, controller.Get("signal")}).(js.Value)
					value, err := awaitBrowserPromise(context.Background(), promise, controller)
					if err == nil {
						var outcome moduleActionViewResult
						err = json.Unmarshal([]byte(value.String()), &outcome)
						if outcome.PreError != "" {
							err = &resourceRPCError{outcome.PreError}
						}
					}
					result <- err
				}()
				<-started
				if resourceEpoch != before+1 || len(epochs) != 1 || epochs[0] != before+1 {
					t.Fatal("dispatch epoch not propagated", resourceEpoch, epochs)
				}
				resource.Load()
				resourceWait(t, func() bool { return resource.Snapshot().Phase == ResourceReady })
				if failure {
					held.reject.Invoke(js.Global().Get("Error").New("network failure"))
				} else {
					held.resolve.Invoke(js.Global().Get("Response").New(`{"data":true}`, map[string]any{"status": 200}))
				}
				err = receiveMutation(t, result)
				if failure != (err != nil) {
					t.Fatal("unexpected transport result", err)
				}
				if resource.Snapshot().Phase != ResourceInvalidated || len(resource.Snapshot().Body) != 0 || resourceEpoch != before+2 || len(epochs) != 2 || epochs[1] != before+2 {
					t.Fatal("settlement failed to invalidate/propagate", resource.Snapshot(), resourceEpoch, epochs)
				}
			})
		}
	}
}

type resourceRPCError struct{ message string }

func (e *resourceRPCError) Error() string { return e.message }

func TestResourceModuleABIRejectsOldRegistrationBeforeMount(t *testing.T) {
	actionViewEnvironment(t)
	win.Set("Object", js.Global().Get("Object"))
	previous := lazyModules
	functions := len(globalFunctions)
	t.Cleanup(func() {
		lazyModules = previous
		for _, fn := range globalFunctions[functions:] {
			fn.Release()
		}
		globalFunctions = globalFunctions[:functions]
	})
	epoch := strings.Repeat("a", 64)
	lazyModules = map[string]*lazyState{"resourcepanel": {loading: true, descriptor: LazyModule{Epoch: epoch}, exports: js.Undefined()}}
	installModuleHost()
	host := win.Get(moduleBridgeName)
	if host.Get("version").Int() != 5 {
		t.Fatal("module ABI link-status contract not raised")
	}
	for _, oldABI := range []int{4, 3} {
		// Each legacy registration starts from fresh state, so rejection of one
		// version cannot mask installation by the next attempt.
		state := &lazyState{loading: true, descriptor: LazyModule{Epoch: epoch}, exports: js.Undefined()}
		lazyModules = map[string]*lazyState{"resourcepanel": state}
		if result := host.Call("register", "resourcepanel", epoch, oldABI, js.Global().Get("Object").New()).String(); result != "incompatible module generation" {
			t.Fatal("old ABI accepted", oldABI, result)
		}
		if !state.exports.IsUndefined() {
			t.Fatal("old exports were installed", oldABI)
		}
	}
}
