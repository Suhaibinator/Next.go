//go:build js && wasm && nextgo_devrefresh

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/scripts"
)

func refreshActivityTestRuntime(t *testing.T) {
	t.Helper()
	previous := refreshActivity
	refreshActivity = refreshActivityTracker{}
	t.Cleanup(func() {
		if refreshActivity.busy != 0 {
			t.Errorf("physical work retained: %d", refreshActivity.busy)
		}
		refreshActivity = previous
	})
}
func refreshActivityAwaitCount(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for refreshActivityState().Busy != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := refreshActivityState().Busy; got != want {
		t.Fatalf("physical count=%d, want%d", got, want)
	}
}
func TestRefreshActivityProductionNoopAndDevFreezeDrain(t *testing.T) {
	refreshActivityTestRuntime(t)
	lease, err := beginRefreshActivity()
	if lease != nil || err != nil || retainRefreshActivity() != nil {
		t.Fatal("production allocated accounting")
	}
	if status := freezeRefreshActivity(); status != (refreshActivityStatus{}) {
		t.Fatal(status)
	}
	lease.Done()
	enableRefreshActivity()
	first, err := beginRefreshActivity()
	if err != nil {
		t.Fatal(err)
	}
	second, err := beginRefreshActivity()
	if err != nil {
		t.Fatal(err)
	}
	if status := freezeRefreshActivity(); status.Busy != 2 || !status.Frozen {
		t.Fatal(status)
	}
	if _, err := beginRefreshActivity(); !errors.Is(err, errRefreshActivityFrozen) {
		t.Fatal("frozen admission", err)
	}
	if err := thawRefreshActivity(); err == nil {
		t.Fatal("thawed live work")
	}
	first.Done()
	first.Done()
	refreshActivityAwaitCount(t, 1)
	// Cleanup continuations remain counted even after new admission is frozen.
	continuation := retainRefreshActivity()
	second.Done()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := waitRefreshActivity(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	continuation.Done()
	if err := waitRefreshActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := thawRefreshActivity(); err != nil {
		t.Fatal(err)
	}
	if status := refreshActivityState(); status.Busy != 0 || status.Frozen {
		t.Fatal(status)
	}
	if err := waitRefreshActivity(nil); err == nil {
		t.Fatal("nil drain context")
	}
}
func TestRefreshActivityPromiseCancellationWaitsForPhysicalRejection(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	var reject js.Value
	executor := js.FuncOf(func(_ js.Value, args []js.Value) any { reject = args[1]; return nil })
	promise := js.Global().Get("Promise").New(executor)
	executor.Release()
	controller := js.Global().Get("AbortController").New()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := awaitBrowserPromise(ctx, promise, controller); done <- err }()
	refreshActivityAwaitCount(t, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !controller.Get("signal").Get("aborted").Bool() {
		t.Fatal("not aborted")
	}
	if status := freezeRefreshActivity(); status.Busy != 1 {
		t.Fatal("logical cancel released callbacks", status)
	}
	mockBrowserFetch(t, func([]js.Value) any { t.Error("frozen Fetch reached browser"); return js.Undefined() })
	if _, err := Fetch(context.Background(), "/frozen", FetchOptions{}); !errors.Is(err, errRefreshActivityFrozen) {
		t.Fatal(err)
	}
	reject.Invoke(js.Global().Get("Error").New("late physical settlement"))
	drain, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := waitRefreshActivity(drain); err != nil {
		t.Fatal(err)
	}
}
func TestRefreshActivityFileAbortCleansPhysicalListenerBeforeIdle(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	fakeFileReader(t, "hold")
	file := testBrowserFile(t, []byte("data"), "data.txt")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := file.Bytes(ctx, 16); done <- err }()
	refreshActivityAwaitCount(t, 1)
	freezeRefreshActivity()
	if _, err := file.Bytes(context.Background(), 16); !errors.Is(err, errRefreshActivityFrozen) {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := waitRefreshActivity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if activeFileReads != 0 || js.Global().Get("fileReaderListeners").Int() != 0 {
		t.Fatal("idle before callback removal")
	}
}
func TestRefreshActivityPromiseSetupFailureDoesNotRetainLease(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("invalid Promise did not fail")
			}
		}()
		_, _ = awaitBrowserPromise(context.Background(), js.Null(), js.Global().Get("AbortController").New())
	}()
	if got := refreshActivityState().Busy; got != 0 {
		t.Fatal("setup failure retained callbacks", got)
	}
}

func TestRefreshActivityValidatorCloseRetainsWorkerUntilPostedSettlement(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	validationDocument(t)
	fixture := validationDOM(t, "held")
	started, release := make(chan struct{}, 1), make(chan struct{})
	validator := validationBind(t, fixture, func(context.Context, validationDraft) (string, error) {
		started <- struct{}{}
		<-release
		return "stale", nil
	}, FieldValidationOptions{Debounce: time.Millisecond})
	if err := validator.Trigger(); err != nil {
		t.Fatal(err)
	}
	<-started
	if state := freezeRefreshActivity(); state.Busy != 1 {
		t.Fatal(state)
	}
	fixture.scope.dispose()
	if state := refreshActivityState(); state.Busy != 1 {
		t.Fatal("disposal released active worker", state)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitRefreshActivity(ctx); err != nil {
		t.Fatal(err)
	}
	if validator.running != nil {
		t.Fatal("idle before posted callback cleanup")
	}
}

func TestRefreshActivityScriptCallerCancelRetainsDownloadCallbacks(t *testing.T) {
	if moduleMode {
		t.Skip("document script downloads belong to the core host")
	}
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	oldDoc, oldLoads, oldAliases, oldSources, oldURL := doc, scriptLoads, scriptAliases, scriptSources, currentURL
	doc = js.Global().Get("Object").New()
	scriptLoads = map[string]*scriptLoad{}
	scriptAliases = map[string]string{}
	scriptSources = map[string]string{}
	currentURL = "https://site.test/"
	t.Cleanup(func() {
		doc, scriptLoads, scriptAliases, scriptSources, currentURL = oldDoc, oldLoads, oldAliases, oldSources, oldURL
	})
	doc.Set("baseURI", currentURL)
	node := js.Global().Get("Object").New()
	head := js.Global().Get("Object").New()
	doc.Set("head", head)
	var loaded js.Value
	callbacks := []js.Func{}
	add := func(target js.Value, name string, fn func(js.Value, []js.Value) any) {
		callback := js.FuncOf(fn)
		callbacks = append(callbacks, callback)
		target.Set(name, callback)
	}
	add(doc, "querySelector", func(js.Value, []js.Value) any { return js.Null() })
	add(doc, "createElement", func(js.Value, []js.Value) any { return node })
	add(node, "setAttribute", func(js.Value, []js.Value) any { return nil })
	add(node, "addEventListener", func(_ js.Value, args []js.Value) any {
		if args[0].String() == "load" {
			loaded = args[1]
		}
		return nil
	})
	removed := 0
	add(node, "removeEventListener", func(js.Value, []js.Value) any { removed++; return nil })
	add(node, "remove", func(js.Value, []js.Value) any { return nil })
	appended := make(chan struct{}, 1)
	add(head, "appendChild", func(js.Value, []js.Value) any { appended <- struct{}{}; return nil })
	t.Cleanup(func() {
		for _, callback := range callbacks {
			callback.Release()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- LoadScript(ctx, scripts.Script{ID: "refresh-held", Src: "https://cdn.test/held.js"}) }()
	<-appended
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if state := freezeRefreshActivity(); state.Busy != 1 {
		t.Fatal("caller cancellation released script worker", state)
	}
	loaded.Invoke()
	drain, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := waitRefreshActivity(drain); err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatal("idle before download listener cleanup", removed)
	}
}

func TestRefreshActivityResourceLoaderIgnoresCancellationKeepsPhysicalLease(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	store := resourceStore(t, ResourceStoreOptions{})
	started, release := make(chan struct{}, 1), make(chan struct{})
	source := resourceSource(t, store, "held", 0, func(context.Context) ([]byte, error) { started <- struct{}{}; <-release; return []byte("stale"), nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := source.Read(ctx); done <- err }()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	store.Close()
	if status := freezeRefreshActivity(); status.Busy != 1 {
		t.Fatal("logical close released physical resource worker", status)
	}
	other := resourceStore(t, ResourceStoreOptions{})
	next := resourceSource(t, other, "new", 0, func(context.Context) ([]byte, error) { t.Error("frozen resource loader invoked"); return nil, nil })
	if _, err := next.Read(context.Background()); !errors.Is(err, errRefreshActivityFrozen) {
		t.Fatal(err)
	}
	close(release)
	drain, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := waitRefreshActivity(drain); err != nil {
		t.Fatal(err)
	}
}
func TestRefreshActivityScopedResourceLoadAccountsWrapperAndWorker(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	store, err := scope.ResourceStore(ResourceStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}, 1), make(chan struct{})
	source := resourceSource(t, store, "held", 0, func(context.Context) ([]byte, error) { started <- struct{}{}; <-release; return nil, nil })
	resource, err := scope.AsyncResource(source, AsyncResourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := resource.Load(); err != nil {
		t.Fatal(err)
	}
	<-started
	if status := freezeRefreshActivity(); status.Busy != 2 {
		t.Fatal("resource wrapper/worker not both accounted", status)
	}
	scope.dispose()
	close(release)
	drain, cancelDrain := context.WithTimeout(context.Background(), time.Second)
	defer cancelDrain()
	if err := waitRefreshActivity(drain); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshActivityDocumentFenceRejectsAdmissionButRetainsCleanup(t *testing.T) {
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	oldCoordinator, oldFence := devRefresh, refreshModuleFence
	t.Cleanup(func() { devRefresh, refreshModuleFence = oldCoordinator, oldFence })
	devRefresh = &refreshCoordinator{frozen: true}
	refreshModuleFence = func() bool { return true }
	if _, err := beginRefreshActivity(); !errors.Is(err, errRefreshActivityFrozen) {
		t.Fatal("document/module fence did not reject new work", err)
	}
	if state := refreshActivityState(); state.Busy != 0 {
		t.Fatal(state)
	}
	continuation := retainRefreshActivity()
	if state := refreshActivityState(); state.Busy != 1 {
		t.Fatal("fence abandoned existing cleanup", state)
	}
	continuation.Done()
}
