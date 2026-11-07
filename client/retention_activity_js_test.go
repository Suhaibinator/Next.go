//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"errors"
	"sync/atomic"
	"syscall/js"
	"testing"
	"time"
)

func retainedActivityEnvironment(t *testing.T) {
	t.Helper()
	retainedTaskMu.Lock()
	previousAdmission, previousWorkers := retainedTaskAdmission, retainedTaskWorkers
	if previousWorkers != 0 {
		retainedTaskMu.Unlock()
		t.Fatal("a prior retained worker is still charged", previousWorkers)
	}
	retainedTaskAdmission, retainedTaskWorkers = true, 0
	retainedTaskMu.Unlock()
	t.Cleanup(func() {
		resourceWait(t, func() bool { retainedTaskMu.Lock(); defer retainedTaskMu.Unlock(); return retainedTaskWorkers == 0 })
		retainedTaskMu.Lock()
		retainedTaskAdmission, retainedTaskWorkers = previousAdmission, previousWorkers
		retainedTaskMu.Unlock()
	})
}
func retainedWorkers() int {
	retainedTaskMu.Lock()
	defer retainedTaskMu.Unlock()
	return retainedTaskWorkers
}

func TestRetainedScopeListenersAndEffectsRestoreWithoutRemount(t *testing.T) {
	retainedActivityEnvironment(t)
	target := js.Global().Get("Function").New(`return (() => {
 const callbacks = new Set();
 return { adds:0, removes:0,
 addEventListener(type, callback) { this.adds++; callbacks.add(callback); },
 removeEventListener(type, callback) { this.removes++; callbacks.delete(callback); },
 fire() { for (const callback of [...callbacks]) callback({target:{value:""}}); }
 }; })();`).Invoke()
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	value := NewState(0)
	life, _ := scope.Lifetime()
	setups, cleanups, permanent := 0, 0, 0
	last := -1
	life.Effect(func() (func(), error) {
		setups++
		last = value.Get()
		return func() { cleanups++ }, nil
	}, value)
	scope.Cleanup(func() { permanent++ })
	scope.On(Element{target}, "click", func(Event) { value.Set(value.Get() + 1) })
	target.Call("fire")
	if err := scope.suspendForRetention(); err != nil {
		t.Fatal(err)
	}
	if !scope.live() || scope.active() || !scope.retentionCapable() || permanent != 0 || target.Get("removes").Int() != 1 {
		t.Fatal("hide lost ownership or failed to detach", permanent)
	}
	target.Call("fire")
	value.Set(7)
	if value.Get() != 7 || last != 1 || setups != 2 || cleanups != 2 {
		t.Fatal("hidden callback or effect executed", value.Get(), last, setups, cleanups)
	}
	if err := scope.resumeFromRetention(); err != nil {
		t.Fatal(err)
	}
	if err := scope.resumeFromRetention(); err != nil || last != 7 || setups != 3 || target.Get("adds").Int() != 2 {
		t.Fatal("resume did not restore exactly once", err, last, setups)
	}
	target.Call("fire")
	if value.Get() != 8 {
		t.Fatal("restored listener lost retained state")
	}
	scope.dispose()
	scope.dispose()
	if target.Get("removes").Int() != 2 || permanent != 1 || len(scope.listeners) != 0 || len(value.effects) != 0 {
		t.Fatal("permanent disposal leaked retained behavior", permanent)
	}
}

func TestRetainedTasksCaptureTheAdmissionContextAndDoNotReplay(t *testing.T) {
	retainedActivityEnvironment(t)
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	type observation struct {
		first bool
		ctx   context.Context
	}
	observed := make(chan observation, 2)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	if err := scope.Go(func(ctx context.Context) { observed <- observation{true, ctx}; <-release }); err != nil {
		t.Fatal(err)
	}
	if err := scope.suspendForRetention(); err != nil {
		t.Fatal(err)
	}
	if err := scope.Go(func(context.Context) {}); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal("hidden owner admitted work", err)
	}
	if err := scope.resumeFromRetention(); err != nil {
		t.Fatal(err)
	}
	if err := scope.Go(func(ctx context.Context) { observed <- observation{false, ctx}; <-release }); err != nil {
		t.Fatal(err)
	}
	first, second := <-observed, <-observed
	if !first.first {
		first, second = second, first
	}
	if first.ctx.Err() != context.Canceled || second.ctx.Err() != nil || first.ctx == second.ctx || scope.taskWorkers != 2 || retainedWorkers() != 2 {
		t.Fatal("a task acquired a later visibility context or lost its physical charge", first.ctx.Err(), second.ctx.Err(), scope.taskWorkers, retainedWorkers())
	}
	scope.dispose()
	if second.ctx.Err() != context.Canceled || retainedWorkers() != 2 {
		t.Fatal("disposal retired a still-running task")
	}
	close(release)
	resourceWait(t, func() bool { return scope.taskWorkers == 0 && retainedWorkers() == 0 })
}

func TestRetainedCoreTaskCapacitySurvivesHiddenAndDisposedOwners(t *testing.T) {
	retainedActivityEnvironment(t)
	first, second := &Scope{}, &Scope{}
	t.Cleanup(first.dispose)
	t.Cleanup(second.dispose)
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	started := make(chan struct{}, retainedPhysicalTaskLimit)
	for _, scope := range []*Scope{first, second} {
		for i := 0; i < 64; i++ {
			if err := scope.Go(func(context.Context) { started <- struct{}{}; <-release }); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := 0; i < retainedPhysicalTaskLimit; i++ {
		<-started
	}
	if err := first.suspendForRetention(); err != nil {
		t.Fatal(err)
	}
	first.dispose()
	other := &Scope{}
	t.Cleanup(other.dispose)
	if err := other.Go(func(context.Context) {}); err == nil || retainedWorkers() != retainedPhysicalTaskLimit {
		t.Fatal("hidden/disposed owners released unreturned worker capacity", err, retainedWorkers())
	}
	close(release)
	resourceWait(t, func() bool { return retainedWorkers() == 0 })
	done := make(chan struct{})
	if err := other.Go(func(context.Context) { close(done) }); err != nil {
		t.Fatal("returned workers did not restore capacity", err)
	}
	<-done
}

func TestRetainedResourceInvalidationFencesLatePhysicalLoaderAndDoesNotRetry(t *testing.T) {
	retainedActivityEnvironment(t)
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	store, err := scope.ResourceStore(ResourceStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	source := resourceSource(t, store, "retained-public", time.Minute, func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return []byte("late-old"), nil
		}
		return []byte("fresh"), nil
	})
	resource, err := scope.AsyncResource(source, AsyncResourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	notifications := 0
	resource.Subscribe(func(snapshot ResourceSnapshot) {
		notifications++
		if string(snapshot.Body) == "late-old" {
			t.Error("stale loader was exposed")
		}
	})
	if err := resource.Load(); err != nil {
		t.Fatal(err)
	}
	<-started
	if retainedWorkers() != 2 {
		t.Fatal("waiter and physical loader not independently charged", retainedWorkers())
	}
	if err := scope.suspendForRetention(); err != nil {
		t.Fatal(err)
	}
	before := notifications
	store.Invalidate()
	if resource.Snapshot().Phase != ResourceInvalidated || notifications != before {
		t.Fatal("hidden invalidation was lost or delivered")
	}
	resourceWait(t, func() bool { return retainedWorkers() == 1 })
	if err := scope.resumeFromRetention(); err != nil {
		t.Fatal(err)
	}
	if notifications != before+1 || calls.Load() != 1 || resource.Snapshot().Phase != ResourceInvalidated {
		t.Fatal("resume retried or failed to synchronize", notifications, calls.Load())
	}
	close(release)
	resourceWait(t, func() bool { return retainedWorkers() == 0 })
	if string(resource.Snapshot().Body) == "late-old" {
		t.Fatal("late result crossed visible epoch")
	}
	if err := resource.Load(); err != nil {
		t.Fatal(err)
	}
	resourceWait(t, func() bool { return resource.Snapshot().Phase == ResourceReady })
	if string(resource.Snapshot().Body) != "fresh" || calls.Load() != 2 {
		t.Fatal(resource.Snapshot(), calls.Load())
	}
}

func TestRetainedResourceClosureIsDeliveredOnceOnResume(t *testing.T) {
	retainedActivityEnvironment(t)
	scope := &Scope{}
	t.Cleanup(scope.dispose)
	store, _ := scope.ResourceStore(ResourceStoreOptions{})
	source := resourceSource(t, store, "retained-closed", 0, func(context.Context) ([]byte, error) { return nil, nil })
	resource, err := scope.AsyncResource(source, AsyncResourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	closedEvents := 0
	resource.Subscribe(func(snapshot ResourceSnapshot) {
		if snapshot.Phase == ResourceClosed {
			closedEvents++
		}
	})
	if err := scope.suspendForRetention(); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if closedEvents != 0 || resource.Snapshot().Phase != ResourceClosed || resource.closed {
		t.Fatal("closure was delivered while hidden or lost before resume")
	}
	if err := scope.resumeFromRetention(); err != nil {
		t.Fatal(err)
	}
	if closedEvents != 1 || !resource.closed || len(scope.asyncResources) != 0 {
		t.Fatal("closed resource was not finalized after its one visible notification", closedEvents)
	}
}

func TestRetainedScopeCapabilityRejectsUnsupportedOwnershipBeforeHiding(t *testing.T) {
	retainedActivityEnvironment(t)
	for _, configure := range []func(*Scope){
		func(scope *Scope) { scope.clientViewUsed = true },
		func(scope *Scope) { scope.selectionCleanup = true },
		func(scope *Scope) { scope.locationCleanup = true },
		func(scope *Scope) { scope.linkStatusCleanup = true },
	} {
		scope := &Scope{}
		configure(scope)
		if scope.retentionCapable() || scope.suspendForRetention() == nil || scope.suspended || !scope.live() {
			t.Fatal("unsupported ownership partially entered the cache")
		}
		scope.dispose()
	}
	scope := &Scope{}
	life, err := scope.Lifetime()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		child, err := life.Child()
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 200; j++ {
			if _, err := child.Defer(func() {}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if scope.retentionCapable() || scope.suspendForRetention() == nil || scope.suspended || life.paused {
		t.Fatal("aggregate resource capacity rejection partially hid an owner")
	}
	scope.dispose()
}

// An effect unsubscribes on hide and subscribes during Lifetime.resume before
// resource pending resync. Its immediate Subscribe delivery must not be repeated,
// while a persistent observer still receives the same hidden latest snapshot.
func TestRetainedResourceResumeDeduplicatesOnlyRecreatedObserverResync(t *testing.T) {
	for _, closed := range []bool{false, true} {
		name := "invalidated"
		phase := ResourceInvalidated
		if closed {
			name, phase = "closed", ResourceClosed
		}
		t.Run(name, func(t *testing.T) {
			retainedActivityEnvironment(t)
			scope := &Scope{}
			t.Cleanup(scope.dispose)
			store, err := scope.ResourceStore(ResourceStoreOptions{})
			if err != nil {
				t.Fatal(err)
			}
			source := resourceSource(t, store, "mixed-"+name, 0, func(context.Context) ([]byte, error) { return nil, nil })
			resource, err := scope.AsyncResource(source, AsyncResourceOptions{})
			if err != nil {
				t.Fatal(err)
			}
			persistent, recreated, setups, cleanups := 0, 0, 0, 0
			_, err = resource.Subscribe(func(snapshot ResourceSnapshot) {
				if snapshot.Phase == phase {
					persistent++
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			life, err := scope.Lifetime()
			if err != nil {
				t.Fatal(err)
			}
			_, err = life.Effect(func() (func(), error) {
				setups++
				stop, err := resource.Subscribe(func(snapshot ResourceSnapshot) {
					if snapshot.Phase == phase {
						recreated++
					}
				})
				if err != nil {
					return nil, err
				}
				return func() { cleanups++; stop() }, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.suspendForRetention(); err != nil {
				t.Fatal(err)
			}
			if closed {
				store.Close()
			} else {
				store.Invalidate()
			}
			if persistent != 0 || recreated != 0 || setups != 1 || cleanups != 1 {
				t.Fatal("hidden observers executed or cleanup lost", persistent, recreated, setups, cleanups)
			}
			if err := scope.resumeFromRetention(); err != nil {
				t.Fatal(err)
			}
			if persistent != 1 || recreated != 1 || setups != 2 || cleanups != 1 {
				t.Fatal("resume starved persistent observer or duplicated recreated observer", persistent, recreated, setups, cleanups)
			}
			if closed {
				if !resource.closed || len(scope.asyncResources) != 0 {
					t.Fatal("closed pending owner was not finalized")
				}
			} else {
				store.Invalidate()
				store.Invalidate()
				if persistent != 3 || recreated != 3 {
					t.Fatal("ordinary visible invalidation was deduplicated", persistent, recreated)
				}
				newObserver := 0
				stop, err := resource.Subscribe(func(snapshot ResourceSnapshot) {
					if snapshot.Phase == phase {
						newObserver++
					}
				})
				if err != nil || newObserver != 1 {
					t.Fatal("ordinary Subscribe lost immediate snapshot", err, newObserver)
				}
				resource.notify()
				if persistent != 4 || recreated != 4 || newObserver != 2 {
					t.Fatal("ordinary same-revision notification was deduplicated", persistent, recreated, newObserver)
				}
				stop()
			}
			scope.dispose()
			if cleanups != 2 {
				t.Fatal("recreated observer cleanup repeated or leaked", cleanups)
			}
		})
	}
}
