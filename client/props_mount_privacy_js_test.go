//go:build js && wasm

package client

import (
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestPropsRevokedInflightClientMountCannotRestoreFallbackFocusOrLateEvents(t *testing.T) {
	propsReadEnvironment(t)
	fixture := clientOnlyDOM(t)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	mounted, releaseMount, cleanupStarted, releaseCleanup, settled := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	type outcome struct{ mount, worker error }
	result := make(chan outcome, 1)
	var cleanup sync.Once
	t.Cleanup(func() {
		cleanup.Do(func() {
			scope.revokeMergeAuthority()
			select {
			case <-releaseMount:
			default:
				close(releaseMount)
			}
			select {
			case <-releaseCleanup:
			default:
				close(releaseCleanup)
			}
			scope.dispose()
			<-settled
			for propsWorkerCount() != 0 || propsWorkerBytes() != 0 || mergeCallbackReads != 0 || refreshActivityState().Busy != 0 {
				runtime.Gosched()
			}
		})
	})
	clicks, cleanups := 0, 0
	go func() {
		defer close(settled)
		var mountErr error
		workerErr := runMergeCallback(func() {
			mountErr = mountScoped(scope, func(root *Scope, raw json.RawMessage) error {
				if _, err := Props(root, raw, PropsOptions[int]{Clone: func(value int) int { return value }}); err != nil {
					return err
				}
				child, err := root.ClientView(ui.El("button", ui.Attrs{"data-click": ""}, ui.Text("live")))
				if err != nil {
					return err
				}
				button, err := child.Require("[data-click]")
				if err != nil {
					return err
				}
				child.On(button, "click", func(Event) { clicks++ })
				child.Cleanup(func() { cleanups++; close(cleanupStarted); <-releaseCleanup })
				close(mounted)
				<-releaseMount
				return nil
			}, json.RawMessage(`7`))
		})
		result <- outcome{mount: mountErr, worker: workerErr}
	}()
	select {
	case <-mounted:
	case out := <-result:
		t.Fatal("Mount failed before held ClientView", out.mount, out.worker)
	}
	if scope.clientView == nil || scope.clientView.child == nil || propsWorkerCount() != 1 {
		t.Fatal("Mount was not held with a live ClientView transaction", propsWorkerCount())
	}
	scope.revokeMergeAuthority()
	fixture.Get("outer").Call("appendChild", fixture.Get("extra"))
	fixture.Get("extra").Call("focus")
	fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if clicks != 0 || cleanups != 0 || !scope.mountOpen || propsWorkerCount() != 1 {
		t.Fatal("revocation ran cleanup early, lost held physical Mount or admitted stale event", clicks, cleanups, propsWorkerCount())
	}
	close(releaseMount)
	select {
	case <-cleanupStarted:
	case out := <-result:
		t.Fatal("Mount completed without held authored cleanup", out.mount, out.worker)
	}
	if propsWorkerCount() != 2 || cleanups != 1 {
		t.Fatal("held cleanup did not keep its own physical callback charge", propsWorkerCount(), cleanups)
	}
	fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if clicks != 0 || fixture.Get("fallback").Get("isConnected").Bool() || !fixture.Get("owner").Get("activeElement").Equal(fixture.Get("extra")) {
		t.Fatal("stale Mount exposed fallback/focus or events while cleanup remained held")
	}
	close(releaseCleanup)
	out := <-result
	if !errors.Is(out.mount, errPropsClosed) || out.worker != nil || !scope.closed || scope.clientView != nil || propsWorkerCount() != 0 {
		t.Fatal("revoked Mount completed with authority or leaked physical work", out.mount, out.worker, scope.closed, propsWorkerCount())
	}
	if !scope.Root.Value.Get("firstChild").Equal(fixture.Get("fresh")) || fixture.Get("fallback").Get("isConnected").Bool() || !fixture.Get("owner").Get("activeElement").Equal(fixture.Get("extra")) {
		t.Fatal("revoked Mount rolled back old fallback or focus after cleanup settled")
	}
	fixture.Get("button").Call("dispatchEvent", js.Global().Get("Event").New("click"))
	if clicks != 0 || cleanups != 1 || propsReservations != 0 {
		t.Fatal("late settled Mount listener or source ownership remained", clicks, cleanups, propsReservations)
	}
}
func TestPropsMountRevocationFailsLateOrdinarySuccessButPreservesDefaultRollback(t *testing.T) {
	propsBrowserEnvironment(t)
	ordinaryFixture := clientOnlyDOM(t)
	ordinaryFixture.Get("root").Call("removeAttribute", clientBoundaryAttribute)
	ordinary := &Scope{Root: Element{ordinaryFixture.Get("root")}}
	t.Cleanup(ordinary.dispose)
	if err := mountScoped(ordinary, func(scope *Scope, _ json.RawMessage) error { scope.revokeMergeAuthority(); return nil }, nil); !errors.Is(err, errPropsClosed) || !ordinary.closed {
		t.Fatal("revoked ordinary Mount reported late success", err, ordinary.closed)
	}
	plain := &Scope{}
	t.Cleanup(plain.dispose)
	if err := mountScoped(plain, func(*Scope, json.RawMessage) error { return nil }, nil); err != nil || plain.closed {
		t.Fatal("default ordinary successful Mount changed", err, plain.closed)
	}
	fixture := clientOnlyDOM(t)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	t.Cleanup(scope.dispose)
	err := mountScoped(scope, func(root *Scope, _ json.RawMessage) error {
		if _, err := root.ClientView(ui.Text("client")); err != nil {
			return err
		}
		return errPropsInvalid
	}, nil)
	if !errors.Is(err, errPropsInvalid) || !scope.closed || !scope.Root.Value.Get("firstChild").Equal(fixture.Get("fallback")) || !fixture.Get("owner").Get("activeElement").Equal(fixture.Get("fallback")) {
		t.Fatal("ordinary failed ClientView stopped restoring its exact fallback/focus", err)
	}
}
