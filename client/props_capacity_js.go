//go:build js && wasm

package client

// Idle reservation is admission policy, not a running physical worker. A
// borrowed job becomes an actual retainedTaskWorkers charge until settlement.
// Framework depth32 plus setup/Clone nesting2 is reserved; arbitrary authored
// cross-owner recursion or a nonreturning callback has no progress guarantee.
const mergeCallbackReserve = 34

type mergeCallbackReservation struct {
	idle   int
	active bool
}

var mergeCallbackCapacity *mergeCallbackReservation
var retainedTaskReservedIdle int

func reserveMergeCallbackCapacity() (func(), error) {
	retainedTaskMu.Lock()
	defer retainedTaskMu.Unlock()
	if moduleMode || !retainedTaskAdmission {
		return func() {}, nil
	}
	if mergeCallbackCapacity != nil || retainedTaskWorkers+retainedTaskReservedIdle > retainedPhysicalTaskLimit-mergeCallbackReserve {
		return nil, errPropsCapacity
	}
	reservation := &mergeCallbackReservation{idle: mergeCallbackReserve, active: true}
	mergeCallbackCapacity = reservation
	retainedTaskReservedIdle += reservation.idle
	return func() {
		retainedTaskMu.Lock()
		defer retainedTaskMu.Unlock()
		if !reservation.active {
			return
		}
		reservation.active = false
		retainedTaskReservedIdle -= reservation.idle
		reservation.idle = 0
		if mergeCallbackCapacity == reservation {
			mergeCallbackCapacity = nil
		}
	}, nil
}
func beginPropsRetainedTask() (*retainedTaskLease, error) {
	retainedTaskMu.Lock()
	reservation := mergeCallbackCapacity
	if reservation != nil && reservation.active && reservation.idle > 0 {
		reservation.idle--
		retainedTaskReservedIdle--
		retainedTaskWorkers++
		retainedTaskMu.Unlock()
		return &retainedTaskLease{reservation: reservation}, nil
	}
	retainedTaskMu.Unlock()
	return beginRetainedTask()
}
