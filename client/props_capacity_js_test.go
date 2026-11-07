//go:build js && wasm

package client

import (
	"context"
	"errors"
	"testing"
)

func propsCapacityEnvironment(t *testing.T) {
	t.Helper()
	propsTestEnvironment(t)
	retainedTaskMu.Lock()
	priorAdmission := retainedTaskAdmission
	clean := retainedTaskWorkers == 0 && retainedTaskReservedIdle == 0 && mergeCallbackCapacity == nil
	retainedTaskMu.Unlock()
	if !clean {
		t.Fatal("prior physical task or callback reservation still owned")
	}
	t.Cleanup(func() {
		propsWait(t, func() bool { workers, idle := propsCapacityCounts(); return workers == 0 && idle == 0 })
		retainedTaskMu.Lock()
		defer retainedTaskMu.Unlock()
		if retainedTaskWorkers != 0 || retainedTaskReservedIdle != 0 || mergeCallbackCapacity != nil {
			t.Error("physical task or idle reservation leaked", retainedTaskWorkers, retainedTaskReservedIdle)
		}
		retainedTaskAdmission = priorAdmission
	})
}
func propsCapacityCounts() (int, int) {
	retainedTaskMu.Lock()
	defer retainedTaskMu.Unlock()
	return retainedTaskWorkers, retainedTaskReservedIdle
}
func TestPropsCallbackReservationPreservesDefaultAndModuleAdmission(t *testing.T) {
	propsCapacityEnvironment(t)
	retainedTaskAdmission = false
	release, err := reserveMergeCallbackCapacity()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	lease, err := beginRetainedTask()
	if err != nil || lease != nil {
		t.Fatal("default task admission changed", lease, err)
	}
	if workers, idle := propsCapacityCounts(); workers != 0 || idle != 0 {
		t.Fatal("disabled opt-in reserved physical policy", workers, idle)
	}
	if moduleMode {
		retainedTaskAdmission = true
		moduleRelease, err := reserveMergeCallbackCapacity()
		if err != nil {
			t.Fatal(err)
		}
		defer moduleRelease()
		if workers, idle := propsCapacityCounts(); workers != 0 || idle != 0 {
			t.Fatal("module unexpectedly acquired core document reservation", workers, idle)
		}
	}
}
func TestPropsCallbackReservationCountsIdleSeparatelyAndNeverAdmitsWorker129(t *testing.T) {
	propsCapacityEnvironment(t)
	if moduleMode {
		return
	} // Core document policy is absent from this module personality.
	retainedTaskAdmission = true
	ordinary := []*retainedTaskLease{}
	borrowed := []*retainedTaskLease{}
	t.Cleanup(func() {
		for _, lease := range ordinary {
			lease.Done()
		}
		for _, lease := range borrowed {
			lease.Done()
		}
	})
	for i := 0; i < retainedPhysicalTaskLimit-mergeCallbackReserve; i++ {
		lease, err := beginRetainedTask()
		if err != nil {
			t.Fatal(i, err)
		}
		ordinary = append(ordinary, lease)
	}
	release, err := reserveMergeCallbackCapacity()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if workers, idle := propsCapacityCounts(); workers != 94 || idle != 34 {
		t.Fatal("idle reservation mislabeled as physical workers", workers, idle)
	}
	if _, err := beginRetainedTask(); err == nil {
		t.Fatal("ordinary task consumed callback reservation")
	}
	if _, err := reserveMergeCallbackCapacity(); !errors.Is(err, errPropsCapacity) {
		t.Fatal("second document reservation accepted", err)
	}
	for i := 0; i < mergeCallbackReserve; i++ {
		lease, err := beginPropsRetainedTask()
		if err != nil {
			t.Fatal(i, err)
		}
		borrowed = append(borrowed, lease)
	}
	if workers, idle := propsCapacityCounts(); workers != 128 || idle != 0 {
		t.Fatal(workers, idle)
	}
	if _, err := beginPropsRetainedTask(); err == nil {
		t.Fatal("callback worker129 admitted")
	}
	borrowed[0].Done()
	borrowed[0].Done()
	if workers, idle := propsCapacityCounts(); workers != 127 || idle != 1 {
		t.Fatal("physical settlement did not restore exactly one reserved slot", workers, idle)
	}
	if _, err := beginRetainedTask(); err == nil {
		t.Fatal("ordinary task stole returned callback slot")
	}
	release()
	release()
	if workers, idle := propsCapacityCounts(); workers != 127 || idle != 0 {
		t.Fatal("document release retired actual borrowed jobs", workers, idle)
	}
	lease, err := beginRetainedTask()
	if err != nil {
		t.Fatal("released idle policy did not return normal capacity", err)
	}
	ordinary = append(ordinary, lease)
	for _, lease := range borrowed {
		lease.Done()
	}
	if workers, idle := propsCapacityCounts(); workers != 95 || idle != 0 {
		t.Fatal("late workers recreated ended reservation", workers, idle)
	}
}
func TestPropsCanceledBorrowedCallbackRetainsPhysicalChargeAfterDocumentRelease(t *testing.T) {
	propsCapacityEnvironment(t)
	if moduleMode {
		return
	}
	retainedTaskAdmission = true
	releaseDocument, err := reserveMergeCallbackCapacity()
	if err != nil {
		t.Fatal(err)
	}
	defer releaseDocument()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
		_, err := propsInvoke(ctx, func() bool { return true }, func() (int, error) { close(started); <-release; return 1, nil })
		result <- err
	}()
	<-started
	cancel()
	if err := <-result; !errors.Is(err, errPropsStale) {
		t.Fatal(err)
	}
	if workers, idle := propsCapacityCounts(); workers != 1 || idle != 33 || propsWorkerCount() != 1 {
		t.Fatal("waiter cancellation erased actual job", workers, idle, propsWorkerCount())
	}
	releaseDocument()
	if workers, idle := propsCapacityCounts(); workers != 1 || idle != 0 {
		t.Fatal("document release erased physical job", workers, idle)
	}
	close(release)
	propsWait(t, func() bool {
		workers, idle := propsCapacityCounts()
		return workers == 0 && idle == 0 && propsWorkerCount() == 0
	})
}
