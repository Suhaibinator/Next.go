//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func propsBrowserEnvironment(t *testing.T) {
	t.Helper()
	propsTestEnvironment(t)
	previousDoc := doc
	doc = js.Global().Get("Function").New("const status={hidden:true,textContent:''};return {getElementById(){return status}};").Invoke()
	t.Cleanup(func() { doc = previousDoc })
	if propsTransientBytes() != 0 {
		t.Fatal("prior prepared/frame charge retained", propsTransientBytes())
	}
	t.Cleanup(func() {
		if propsTransientBytes() != 0 {
			t.Error("prepared/frame charge leaked", propsTransientBytes())
		}
	})
	priorBytes, priorReservations, priorInstance := propsRegisteredBytes, propsReservations, mergeNextInstance
	previousLocation := committedLocation
	committedLocation = currentPropsFrame.location
	propsRegisteredBytes, propsReservations = 0, 0
	t.Cleanup(func() {
		propsRegisteredBytes, propsReservations, mergeNextInstance = priorBytes, priorReservations, priorInstance
		committedLocation = previousLocation
	})
}
func propsClientRoot(t *testing.T, raw string, options PropsOptions[int], bind func(*Scope, *PropsSource[int]) error) (*Scope, *PropsSource[int], js.Value) {
	t.Helper()
	fixture := clientOnlyDOM(t)
	root := fixture.Get("root")
	root.Call("setAttribute", "data-nextgo-props", raw)
	scope := &Scope{Root: Element{root}}
	t.Cleanup(scope.dispose)
	var source *PropsSource[int]
	err := mountScoped(scope, func(s *Scope, raw json.RawMessage) error {
		var err error
		source, err = Props(s, raw, options)
		if err != nil {
			return err
		}
		child, err := s.ClientView(ui.El("button", ui.Attrs{"data-click": ""}, ui.Text("client")))
		if err != nil {
			return err
		}
		if bind != nil {
			return bind(child, source)
		}
		return nil
	}, json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return scope, source, fixture
}
func TestPropsRootRegistrationIsAtomicAndRejectsDuplicateAndChildSources(t *testing.T) {
	propsBrowserEnvironment(t)
	fixture := clientOnlyDOM(t)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	t.Cleanup(scope.dispose)
	var source *PropsSource[int]
	err := mountScoped(scope, func(root *Scope, raw json.RawMessage) error {
		if _, err := Props(root, raw, PropsOptions[int]{Clone: func(v int) int { return v }, Validate: func(PropsSnapshot[int]) error { return errPropsInvalid }}); err == nil || root.propsSource != nil || root.propsPending || propsReservations != 0 {
			return errPropsInvalid
		}
		var err error
		source, err = Props(root, raw, PropsOptions[int]{Clone: func(v int) int { return v }})
		if err != nil {
			return err
		}
		if _, err = Props(root, raw, PropsOptions[int]{Clone: func(v int) int { return v }}); err == nil {
			return errPropsInvalid
		}
		child, err := root.ClientView(ui.El("button", nil, ui.Text("client")))
		if err != nil {
			return err
		}
		if _, err = Props(child, raw, PropsOptions[int]{Clone: func(v int) int { return v }}); err == nil {
			return errPropsInvalid
		}
		return nil
	}, json.RawMessage(`7`))
	if err != nil || source == nil || propsReservations != 1 || scope.propsSource != source {
		t.Fatal(err, propsReservations)
	}
	value, err := source.GetSnapshot()
	if err != nil || value.Value != 7 || value.Revision != 1 {
		t.Fatal(value, err)
	}
	scope.dispose()
	if propsReservations != 0 {
		t.Fatal("terminal source budget leaked")
	}
	if _, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) {
		t.Fatal(err)
	}
}
func TestPropsSuccessfulClientViewKeepsDurableChildWithoutInitialFallback(t *testing.T) {
	propsBrowserEnvironment(t)
	scope, source, fixture := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	if scope.clientView != nil || scope.mergeView == nil || scope.mergeView.child == nil || !scope.mergeView.child.Root.Value.Equal(fixture.Get("fresh")) || !source.propsValid() {
		t.Fatal("successful child ownership was transient")
	}
	if !moduleMode && !scope.mergeClientReady() {
		t.Fatal("successful current child not eligible")
	}
	if scope.mergeView.root.Equal(fixture.Get("fallback")) || scope.mergeView.view.Equal(fixture.Get("fallback")) {
		t.Fatal("durable record retained stale fallback")
	}
	scope.mergeView.child.dispose()
	if scope.mergeClientReady() {
		t.Fatal("disposed child retained merge authority")
	}
}
func TestPropsRevokeImmediatelyFencesReadsEventsAndLifetimeWithoutRunningCleanup(t *testing.T) {
	propsBrowserEnvironment(t)
	cleanups, clicks := 0, 0
	scope, source, fixture := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, p *PropsSource[int]) error {
		life, err := child.Lifetime()
		if err != nil {
			return err
		}
		_, err = life.Effect(func() (func(), error) { return func() { cleanups++ }, nil }, p)
		if err != nil {
			return err
		}
		button, err := child.Require("[data-click]")
		if err != nil {
			return err
		}
		child.On(button, "click", func(Event) { clicks++ })
		return nil
	})
	button := fixture.Get("button")
	button.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	scope.revokeMergeAuthority()
	button.Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if _, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) || clicks != 1 || cleanups != 0 || scope.mergeView.child.active() {
		t.Fatal("logical revocation ran callbacks or left reads/events alive", err, clicks, cleanups)
	}
	if _, err := scope.lifetime.Child(); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal("revoked lifetime admitted children", err)
	}
	scope.dispose()
	if cleanups != 1 {
		t.Fatal("deferred terminal cleanup not released", cleanups)
	}
}
func TestPropsInitialHookCancellationDoesNotPublishOrFreeRunningWorker(t *testing.T) {
	propsBrowserEnvironment(t)
	fixture := clientOnlyDOM(t)
	scope := &Scope{Root: Element{fixture.Get("root")}, mountOpen: true}
	t.Cleanup(scope.dispose)
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := Props(scope, json.RawMessage(`7`), PropsOptions[int]{Clone: func(v int) int { close(started); <-release; return v }})
		result <- err
	}()
	<-started
	scope.revokeMergeAuthority()
	if err := <-result; !errors.Is(err, errPropsStale) || scope.propsSource != nil || propsReservations != 0 || propsWorkerCount() != 1 {
		t.Fatal("canceled root registration published or retired physical work", err, propsReservations, propsWorkerCount())
	}
	close(release)
	propsWait(t, func() bool { return propsWorkerCount() == 0 })
}
func TestPropsInitialSourceNeverGrantsOrdinaryIslandMergeAuthority(t *testing.T) {
	propsBrowserEnvironment(t)
	fixture := clientOnlyDOM(t)
	fixture.Get("root").Call("removeAttribute", clientBoundaryAttribute)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	t.Cleanup(scope.dispose)
	err := mountScoped(scope, func(root *Scope, raw json.RawMessage) error {
		_, err := Props(root, raw, PropsOptions[int]{Clone: func(v int) int { return v }})
		return err
	}, json.RawMessage(`7`))
	if err != nil || scope.mergeClientReady() {
		t.Fatal("ordinary source became an opaque client view", err)
	}
	if _, err := prepareScopeProps(context.Background(), scope, json.RawMessage(`8`), Location{URL: "https://props.test/b"}, 2, func() bool { return true }); err == nil {
		t.Fatal("ordinary SSR source accepted merge prepare")
	}
}

func TestPropsDefaultScopeDisposalKeepsLegacyLifetimeCleanupOrder(t *testing.T) {
	propsBrowserEnvironment(t)
	scope := &Scope{}
	life, err := scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	key := NewContext[int]()
	if err := Provide(life, key, 7); err != nil {
		t.Fatal(err)
	}
	state := NewState(0)
	runs := 0
	if _, err := life.Effect(func() (func(), error) { runs++; return nil, nil }, state); err != nil {
		t.Fatal(err)
	}
	seen := 0
	scope.Cleanup(func() {
		if value, ok := UseContext(life, key); ok {
			seen = value
		}
		state.Set(1)
	})
	scope.dispose()
	if seen != 7 || runs != 2 || !life.Closed() {
		t.Fatal("default disposal was changed into premature logical privacy revocation", seen, runs, life.Closed())
	}
}

func TestPropsOwnedScopeSupervisesEveryAuthoredCleanupAfterParentUnlink(t *testing.T) {
	propsBrowserEnvironment(t)
	scope, _, _ := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(value int) int { return value }}, nil)
	child := scope.mergeView.child
	cleaned := []int{}
	for i := 0; i < 3; i++ {
		index := i
		child.Cleanup(func() {
			cleaned = append(cleaned, index)
			if index == 1 {
				runtime.Goexit()
			}
		})
	}
	child.dispose()
	if child.parent != nil || len(cleaned) != 3 || cleaned[0] != 2 || cleaned[1] != 1 || cleaned[2] != 0 || !child.lifetime.Closed() || propsWorkerCount() != 0 {
		t.Fatal("child unlink lost inherited cleanup guard or stranded cleanup", cleaned, propsWorkerCount())
	}
	scope.dispose()
	if propsReservations != 0 {
		t.Fatal("owned source did not retire after guarded child cleanup")
	}
}

func TestPropsOwnedResourceObserversSuperviseGoexitIndividually(t *testing.T) {
	propsBrowserEnvironment(t)
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	if err := scope.enableMergeCallbacks(); err != nil {
		t.Fatal(err)
	}
	failed, healthy := 0, 0
	resource := &AsyncResource{scope: scope, state: ResourceSnapshot{Phase: ResourceInvalidated}, observers: map[int]func(ResourceSnapshot){
		1: func(ResourceSnapshot) { failed++; runtime.Goexit() },
		2: func(ResourceSnapshot) { healthy++ },
	}}
	resource.notify()
	if failed != 1 || healthy != 1 || resource.notifying || resource.pending || propsWorkerCount() != 0 {
		t.Fatal("owned observer Goexit stranded healthy observer or notify state", failed, healthy, resource.notifying, resource.pending)
	}
}

func TestPropsDefaultResourceSnapshotKeepsCleanupOrderingBeforeLifetimeRelease(t *testing.T) {
	propsBrowserEnvironment(t)
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	life, err := scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	resource := &AsyncResource{scope: scope, state: ResourceSnapshot{Phase: ResourceReady, Body: []byte("ready")}}
	if _, err := life.Defer(resource.Close); err != nil {
		t.Fatal(err)
	}
	phase := ResourceIdle
	scope.Cleanup(func() { phase = resource.Snapshot().Phase })
	scope.dispose()
	if phase != ResourceReady || resource.Snapshot().Phase != ResourceClosed {
		t.Fatal("default resource snapshot revoked before existing Lifetime cleanup", phase, resource.Snapshot().Phase)
	}
}
