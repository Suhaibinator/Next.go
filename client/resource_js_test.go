//go:build js && wasm

package client

import (
	"context"
	"errors"
	"sync/atomic"
	"syscall/js"
	"testing"
	"time"
)

func TestResourceScopedSeedSnapshotsInvalidationAndDisposal(t *testing.T) {
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	before := len(resourceStores)
	store, err := scope.ResourceStore(ResourceStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	source := resourceSource(t, store, "public", time.Minute, func(context.Context) ([]byte, error) { calls.Add(1); return []byte("network"), nil })
	body := []byte("SSR")
	r, err := scope.AsyncResource(source, AsyncResourceOptions{Seed: &ResourceSeed{Key: "public", Body: body, ExpiresUnixMilli: time.Now().Add(30 * time.Second).UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	body[0] = 'x'
	if snapshot := r.Snapshot(); snapshot.Phase != ResourceReady || !snapshot.Seeded || string(snapshot.Body) != "SSR" {
		t.Fatal(snapshot)
	}
	snapshot := r.Snapshot()
	snapshot.Body[0] = 'x'
	var phases []ResourcePhase
	stop, err := r.Subscribe(func(snapshot ResourceSnapshot) {
		phases = append(phases, snapshot.Phase)
		if len(snapshot.Body) > 0 {
			snapshot.Body[0] = 'x'
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(r.Snapshot().Body) != "SSR" || calls.Load() != 0 {
		t.Fatal("seed not owned or creation fetched")
	}
	invalidateResourceStores()
	if snapshot := r.Snapshot(); snapshot.Phase != ResourceInvalidated || len(snapshot.Body) != 0 || calls.Load() != 0 {
		t.Fatal(snapshot, calls.Load())
	}
	if err := r.Retry(); err != nil {
		t.Fatal(err)
	}
	resourceWait(t, func() bool { return r.Snapshot().Phase == ResourceReady })
	if string(r.Snapshot().Body) != "network" || calls.Load() != 1 {
		t.Fatal(r.Snapshot(), calls.Load())
	}
	stop()
	count := len(phases)
	scope.dispose()
	if len(resourceStores) != before || r.Snapshot().Phase != ResourceClosed {
		t.Fatal("scoped store or view retained")
	}
	if err := r.Load(); !errors.Is(err, ErrResourceClosed) {
		t.Fatal(err)
	}
	if len(phases) != count {
		t.Fatal("callback ran during disposal")
	}
}
func TestResourceScopeCancellationSupersessionAndExplicitRetry(t *testing.T) {
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	store, _ := scope.ResourceStore(ResourceStoreOptions{})
	var calls atomic.Int32
	started := make(chan struct{}, 2)
	canceled := make(chan struct{}, 2)
	source := resourceSource(t, store, "private", 0, func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		canceled <- struct{}{}
		return nil, context.Cause(ctx)
	})
	r, err := scope.AsyncResource(source, AsyncResourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || r.Snapshot().Phase != ResourceIdle {
		t.Fatal("resource requested implicitly")
	}
	r.Load()
	<-started
	r.Retry()
	<-canceled
	<-started
	scope.dispose()
	<-canceled
	if calls.Load() != 2 || r.Snapshot().Phase != ResourceClosed {
		t.Fatal(calls.Load(), r.Snapshot())
	}
}
func TestResourceScopeOwnershipExpiryAndModuleEpoch(t *testing.T) {
	scope, sibling := &Scope{}, &Scope{}
	t.Cleanup(scope.dispose)
	t.Cleanup(sibling.dispose)
	store, _ := scope.ResourceStore(ResourceStoreOptions{})
	source := resourceSource(t, store, "public", time.Minute, func(context.Context) ([]byte, error) { return []byte("unused"), nil })
	if _, err := sibling.AsyncResource(source, AsyncResourceOptions{}); err == nil {
		t.Fatal("sibling consumed another component's store")
	}
	child := &Scope{parent: scope}
	t.Cleanup(child.dispose)
	r, err := child.AsyncResource(source, AsyncResourceOptions{Seed: &ResourceSeed{Key: "public", Body: []byte("old"), ExpiresUnixMilli: time.Now().Add(-time.Second).UnixMilli()}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Snapshot().Phase != ResourceIdle {
		t.Fatal("expired seed reused")
	}
	previous := resourceEpoch
	setResourceEpoch(previous + 1)
	if r.Snapshot().Phase != ResourceInvalidated {
		t.Fatal("module epoch did not invalidate")
	}
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	resourceWait(t, func() bool { return r.Snapshot().Phase == ResourceReady })
	setResourceEpoch(previous + 1)
	if r.Snapshot().Phase != ResourceReady {
		t.Fatal("unchanged module epoch invalidated")
	}
}
func TestResourceGETSameOriginStatusPrivacyAndBound(t *testing.T) {
	locationEnvironment(t)
	setLocation("https://site.test/resources")
	win.Get("location").Set("origin", "https://site.test")
	var calls int
	mockBrowserFetch(t, func(args []js.Value) any {
		calls++
		options := args[1]
		if options.Get("method").String() != "GET" || options.Get("cache").String() != "no-store" || options.Get("redirect").String() != "error" {
			t.Error("resource transport policy changed")
		}
		return browserResponse([]byte("public"), 200, map[string]any{"Cache-Control": "public, max-age=60"})
	})
	if got, err := ResourceGET("/api/resources", 64, 30*time.Second)(context.Background()); err != nil || string(got) != "public" {
		t.Fatal(string(got), err)
	}
	for _, target := range []string{"https://other.test/api", "https://user@site.test/api", "/api#fragment"} {
		if _, err := ResourceGET(target, 64, 0)(context.Background()); err == nil {
			t.Fatal("unsafe target accepted", target)
		}
	}
	if calls != 1 {
		t.Fatal("unsafe target dispatched")
	}
	for _, header := range []Headers{{}, {"Cache-Control": {"public, private"}}, {"Cache-Control": {"public, no-store"}}, {"Cache-Control": {"public, no-cache"}}, {"Cache-Control": {"public"}, "Vary": {"*"}}, {"Cache-Control": {"public"}, "Set-Cookie": {"value=secret"}}} {
		if resourcePublicResponse(header, time.Second) {
			t.Fatal("private response reusable", header)
		}
	}
	if !resourcePublicResponse(Headers{"Cache-Control": {"public, max-age=30"}}, time.Second) {
		t.Fatal("public response rejected")
	}
}

func TestResourceGETCancellationRetainsSlotUntilNativeSettlement(t *testing.T) {
	locationEnvironment(t)
	win.Get("location").Set("origin", "https://site.test")
	var resolve, signal js.Value
	executor := js.FuncOf(func(_ js.Value, args []js.Value) any { resolve = args[0]; return nil })
	pending := js.Global().Get("Promise").New(executor)
	executor.Release()
	started := make(chan struct{}, 1)
	mockBrowserFetch(t, func(args []js.Value) any { signal = args[1].Get("signal"); started <- struct{}{}; return pending })
	store := resourceStore(t, ResourceStoreOptions{MaxInFlight: 1})
	source := resourceSource(t, store, "physical-get", 0, ResourceGET("/api/resource", 64, 0))
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := source.Read(ctx); result <- err }()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	resourceWait(t, func() bool { return signal.Get("aborted").Bool() })
	if _, err := source.Read(context.Background()); !errors.Is(err, ErrResourceCapacity) {
		t.Fatal("unsettled browser GET released physical slot", err)
	}
	response := js.Global().Get("Response").New("late", map[string]any{"status": 200})
	resolve.Invoke(response)
	resourceWait(t, func() bool { store.mu.Lock(); defer store.mu.Unlock(); return len(store.flights) == 0 })
}

func TestResourceSourceCloseIsTerminalAndAncestorStopsCallbacks(t *testing.T) {
	parent := &Scope{}
	child := &Scope{parent: parent}
	t.Cleanup(parent.dispose)
	t.Cleanup(child.dispose)
	store, _ := parent.ResourceStore(ResourceStoreOptions{})
	source := resourceSource(t, store, "private", 0, func(context.Context) ([]byte, error) { return []byte("value"), nil })
	resource, err := child.AsyncResource(source, AsyncResourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err = resource.Subscribe(func(ResourceSnapshot) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	if resource.Snapshot().Phase != ResourceClosed {
		t.Fatal("source close left a live resource")
	}
	if err := resource.Load(); !errors.Is(err, ErrResourceClosed) {
		t.Fatal(err)
	}
	before := calls
	store.Invalidate()
	if calls != before {
		t.Fatal("source close retained callback")
	}
	source = resourceSource(t, store, "another-private", 0, func(context.Context) ([]byte, error) { return []byte("value"), nil })
	resource, err = child.AsyncResource(source, AsyncResourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = resource.Subscribe(func(ResourceSnapshot) { calls++ })
	// Disposal marks the ancestor closed before cleanup code can invalidate.
	parent.Cleanup(store.Invalidate)
	before = calls
	parent.dispose()
	if calls != before {
		t.Fatal("inactive ancestor dispatched resource callbacks")
	}
}
