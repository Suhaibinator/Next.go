//go:build js && wasm && nextgo_devrefresh

package client

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync"
	"testing"
)

func TestPropsDevOrdinaryInitialSourceRequiresReloadWithoutLifetimeOrClientView(t *testing.T) {
	propsBrowserEnvironment(t)
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	fixture := clientOnlyDOM(t)
	fixture.Get("root").Call("removeAttribute", clientBoundaryAttribute)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	t.Cleanup(scope.dispose)
	if err := mountScoped(scope, func(root *Scope, raw json.RawMessage) error {
		_, err := Props(root, raw, PropsOptions[int]{Clone: func(value int) int { return value }})
		return err
	}, json.RawMessage(`7`)); err != nil {
		t.Fatal(err)
	}
	if scope.lifetime != nil || scope.clientViewUsed || len(scope.children) != 0 || scope.propsSource == nil || refreshScopeSupported(scope) {
		t.Fatal("ordinary Props scope incorrectly qualified for current HMR transfer")
	}
	if refreshActivityState().Busy != 0 {
		t.Fatal("completed initial props worker retained dev activity")
	}
}
func TestPropsDevCanceledFailedMountCannotRetireUntilPhysicalCloneReturns(t *testing.T) {
	propsBrowserEnvironment(t)
	refreshActivityTestRuntime(t)
	enableRefreshActivity()
	fixture := clientOnlyDOM(t)
	scope := &Scope{Root: Element{fixture.Get("root")}}
	started, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	var cleanup sync.Once
	// Register after both global helpers. Their restoration must never race
	// either the logical Mount's deferred ownership cleanup or its worker.
	t.Cleanup(func() {
		cleanup.Do(func() {
			scope.dispose()
			select {
			case <-release:
			default:
				close(release)
			}
			<-settled
			// Fail closed under the test process timeout if settlement regresses;
			// do not restore a tracker that a surviving lease could still mutate.
			for propsWorkerCount() != 0 || propsWorkerBytes() != 0 || refreshActivityState().Busy != 0 {
				runtime.Gosched()
			}
		})
	})
	go func() {
		defer close(settled)
		result <- mountScoped(scope, func(root *Scope, raw json.RawMessage) error {
			_, err := Props(root, raw, PropsOptions[int]{Clone: func(value int) int { close(started); <-release; return value }})
			return err
		}, json.RawMessage(`7`))
	}()
	<-started
	if !scope.propsPending || refreshScopeSupported(scope) || refreshActivityState().Busy != 1 {
		t.Fatal("pending source admitted HMR or lost physical dev activity", scope.propsPending, refreshActivityState())
	}
	scope.dispose()
	if err := <-result; !errors.Is(err, errPropsStale) {
		t.Fatal(err)
	}
	if scope.propsSource != nil || propsReservations != 0 || propsWorkerCount() != 1 || refreshActivityState().Busy != 1 {
		t.Fatal("failed logical Mount erased unreturned Clone", propsReservations, propsWorkerCount(), refreshActivityState())
	}
	if status := freezeRefreshActivity(); status.Busy != 1 || !status.Frozen {
		t.Fatal(status)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitRefreshActivity(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("runtime drain ignored live callback", err)
	}
	called := false
	if _, err := propsInvoke(context.Background(), func() bool { return true }, func() (int, error) { called = true; return 1, nil }); !errors.Is(err, errPropsStale) || called || refreshActivityState().Busy != 1 {
		t.Fatal("new props work admitted after freeze", err, called, refreshActivityState())
	}
	close(release)
	propsWait(t, func() bool { return propsWorkerCount() == 0 && refreshActivityState().Busy == 0 })
	if err := thawRefreshActivity(); err != nil {
		t.Fatal(err)
	}
}
