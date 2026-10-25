//go:build js && wasm && nextgo_module

package client

import (
	"context"
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

func resourceModuleConfig(t *testing.T) string {
	t.Helper()
	old := doc
	t.Cleanup(func() { doc = old })
	config := strings.Repeat("a", 64)
	get := js.FuncOf(func(js.Value, []js.Value) any { return config })
	t.Cleanup(get.Release)
	doc = js.ValueOf(map[string]any{"body": map[string]any{"getAttribute": get}})
	return config
}
func TestModuleTargetResourceMutationEpochAndScopedCleanup(t *testing.T) {
	config := resourceModuleConfig(t)
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	before := len(resourceStores)
	store, _ := scope.ResourceStore(ResourceStoreOptions{})
	source := resourceSource(t, store, "public", time.Minute, func(context.Context) ([]byte, error) { return []byte("pending read"), nil })
	resource, err := scope.AsyncResource(source, AsyncResourceOptions{Seed: &ResourceSeed{Key: "public", Body: []byte("seed"), ExpiresUnixMilli: time.Now().Add(30 * time.Second).UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	epoch := resourceEpoch
	syncEpoch := func(value uint64) {
		state, _ := json.Marshal(moduleState{ResourceEpoch: value, Configuration: config})
		if err := syncModule(string(state)); err != nil {
			t.Error(err)
		}
	}
	started, release := make(chan struct{}), make(chan struct{})
	moduleTransportHostFixture(t, "mutation", func(args []js.Value, resolve, reject js.Value) {
		if result := args[3].Invoke(js.Null()).String(); result != "" {
			reject.Invoke(result)
			return
		}
		syncEpoch(epoch + 1)
		close(started)
		go func() {
			<-release
			syncEpoch(epoch + 2)
			if result := moduleValidateResponse(t, args[3], 200, `{"data":1}`); result != "" {
				reject.Invoke(result)
				return
			}
			moduleFinishAction(resolve, moduleActionViewResult{})
		}()
	})
	result := make(chan error, 1)
	go func() {
		result <- DispatchMutation(context.Background(), "/_nextgo/actions/save/v1", FetchOptions{}, func(Response) error { return nil })
	}()
	<-started
	if resource.Snapshot().Phase != ResourceInvalidated || len(resource.Snapshot().Body) != 0 {
		t.Fatal("module dispatch epoch retained old value")
	}
	resource.Load()
	resourceWait(t, func() bool { return resource.Snapshot().Phase == ResourceReady })
	syncEpoch(epoch + 1)
	if resource.Snapshot().Phase != ResourceReady {
		t.Fatal("unchanged epoch discarded data")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if resource.Snapshot().Phase != ResourceInvalidated || resourceEpoch != epoch+2 {
		t.Fatal("module settlement failed to invalidate", resource.Snapshot())
	}
	scope.dispose()
	if len(resourceStores) != before || resource.Snapshot().Phase != ResourceClosed {
		t.Fatal("module scoped resource retained")
	}
}
func TestModuleTargetResourceRejectsABI3HostBeforeStateOrMount(t *testing.T) {
	oldWin, oldHost := win, moduleHost
	t.Cleanup(func() { win, moduleHost = oldWin, oldHost })
	host := js.Global().Get("Object").New()
	host.Set("version", 3)
	calls := 0
	state := js.FuncOf(func(js.Value, []js.Value) any { calls++; return "" })
	defer state.Release()
	host.Set("state", state)
	win = js.ValueOf(map[string]any{moduleBridgeName: host})
	panicked := false
	func() { defer func() { panicked = recover() != nil }(); RunModule("resourcepanel", "epoch") }()
	if !panicked || calls != 0 {
		t.Fatal("mixed ABI host reached state or mount", panicked, calls)
	}
}

func TestModuleTargetResourceLateSSRSeedOrigin(t *testing.T) {
	checkResourceLateSSRSeedOrigin(t)
}
