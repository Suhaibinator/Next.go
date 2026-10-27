//go:build js && wasm

package client

import (
	"errors"
	"fmt"
	"sync"
	"syscall/js"
)

// Enabled only by the opt-in core router after document validation. A charge
// belongs to a physical worker, not to its logical waiter or visibility epoch.
var retainedTaskAdmission bool
var retainedTaskWorkers int
var retainedTaskMu sync.Mutex

const retainedPhysicalTaskLimit = 128

type retainedTaskLease struct{ done bool }

func beginRetainedTask() (*retainedTaskLease, error) {
	retainedTaskMu.Lock()
	defer retainedTaskMu.Unlock()
	if !retainedTaskAdmission {
		return nil, nil
	}
	if retainedTaskWorkers >= retainedPhysicalTaskLimit {
		return nil, fmt.Errorf("nextgo: retained component physical task capacity exceeded")
	}
	retainedTaskWorkers++
	return &retainedTaskLease{}, nil
}
func (lease *retainedTaskLease) Done() {
	if lease == nil {
		return
	}
	retainedTaskMu.Lock()
	defer retainedTaskMu.Unlock()
	if lease.done {
		return
	}
	lease.done = true
	retainedTaskWorkers--
}

type scopeListener struct {
	target           js.Value
	event            string
	callback         js.Func
	attached, closed bool
}

func (listener *scopeListener) attach() {
	if listener.closed || listener.attached {
		return
	}
	listener.target.Call("addEventListener", listener.event, listener.callback)
	listener.attached = true
}
func (listener *scopeListener) detach() {
	if !listener.attached {
		return
	}
	listener.target.Call("removeEventListener", listener.event, listener.callback)
	listener.attached = false
}
func (listener *scopeListener) close() {
	if listener.closed {
		return
	}
	listener.closed = true
	defer listener.callback.Release()
	listener.detach()
}

// live deliberately ignores visibility and runtime freeze. It is used only for
// ownership/fence bookkeeping; application callbacks must still use active.
func (s *Scope) live() bool {
	if s == nil {
		return false
	}
	for owner := s; owner != nil; owner = owner.parent {
		if owner.closed {
			return false
		}
	}
	return true
}
func (s *Scope) retentionHidden() bool {
	for owner := s; owner != nil; owner = owner.parent {
		if owner.suspended {
			return true
		}
	}
	return false
}
func (s *Scope) stopVisibilityTasks() {
	if s.taskCancel != nil {
		s.taskCancel()
		s.taskCancel = nil
	}
	s.taskContext = nil
}

func (s *Scope) retainedScopeTree() ([]*Scope, error) {
	if !s.live() {
		return nil, ErrLifetimeClosed
	}
	pending := []*Scope{s}
	seen := map[*Scope]bool{}
	var result []*Scope
	listeners, resources := 0, 0
	for len(pending) > 0 {
		owner := pending[0]
		pending = pending[1:]
		if seen[owner] || len(result)+len(pending)+len(owner.children) >= 128 {
			return nil, fmt.Errorf("nextgo: retained component scope limit exceeded")
		}
		seen[owner] = true
		if !owner.live() || owner.mountOpen || owner.clientViewUsed || len(owner.keyedLists) != 0 || owner.selectionCleanup || owner.locationCleanup || owner.linkStatusCleanup {
			return nil, fmt.Errorf("nextgo: component ownership is not supported by route retention")
		}
		listeners += len(owner.listeners)
		resources += len(owner.asyncResources)
		if listeners > 512 || resources > 64 {
			return nil, fmt.Errorf("nextgo: retained component registration limit exceeded")
		}
		result = append(result, owner)
		for child := range owner.children {
			pending = append(pending, child)
		}
	}
	return result, nil
}
func (s *Scope) retentionCapable() bool {
	if moduleMode || moduleRefreshHandlersFrozen() || refreshFrozen() {
		return false
	}
	if _, err := s.retainedScopeTree(); err != nil {
		return false
	}
	if s.lifetime != nil {
		if _, err := s.lifetime.activityTree(); err != nil {
			return false
		}
	}
	return true
}

func (s *Scope) suspendForRetention() error {
	if !s.live() {
		return ErrLifetimeClosed
	}
	if s.suspended {
		return nil
	}
	if !s.retentionCapable() {
		return fmt.Errorf("nextgo: component cannot be retained")
	}
	tree, err := s.retainedScopeTree()
	if err != nil {
		return err
	}
	for _, owner := range tree {
		owner.suspended = true
		owner.activityEpoch++
	}
	epoch := s.activityEpoch
	var failures []error
	for i := len(tree) - 1; i >= 0; i-- {
		owner := tree[i]
		owner.stopVisibilityTasks()
		for resource := range owner.asyncResources {
			resource.suspendForRetention()
		}
		for j := len(owner.listeners) - 1; j >= 0; j-- {
			if failure := lifetimeGuard(owner.listeners[j].detach); failure != nil {
				failures = append(failures, failure)
			}
		}
	}
	if s.lifetime != nil {
		if failure := s.lifetime.suspend(); failure != nil {
			failures = append(failures, failure)
		}
	}
	if !s.live() || !s.suspended || epoch != s.activityEpoch {
		failures = append(failures, errLifetimeActivityChanged)
	}
	return errors.Join(failures...)
}

func (s *Scope) resumeFromRetention() error {
	if !s.live() {
		return ErrLifetimeClosed
	}
	if moduleMode || moduleRefreshHandlersFrozen() || refreshFrozen() {
		return errLifetimeActivityChanged
	}
	if !s.suspended {
		return nil
	}
	if s.parent != nil && !s.parent.active() {
		return errLifetimeActivityChanged
	}
	tree, err := s.retainedScopeTree()
	if err != nil {
		return err
	}
	for _, owner := range tree {
		owner.suspended = false
		owner.activityEpoch++
	}
	epoch := s.activityEpoch
	var failures []error
	if s.lifetime != nil {
		if failure := s.lifetime.resume(); failure != nil {
			failures = append(failures, failure)
		}
		if !s.lifetime.visible() {
			return errors.Join(append(failures, errLifetimeActivityChanged)...)
		}
	}
	for _, owner := range tree {
		if !s.active() || s.activityEpoch != epoch {
			failures = append(failures, errLifetimeActivityChanged)
			break
		}
		if !owner.active() {
			continue
		}
		for _, listener := range owner.listeners {
			if failure := lifetimeGuard(listener.attach); failure != nil {
				failures = append(failures, failure)
			}
		}
		for resource := range owner.asyncResources {
			resource.resumeFromRetention()
		}
	}
	if !s.active() || s.activityEpoch != epoch {
		failures = append(failures, errLifetimeActivityChanged)
	}
	return errors.Join(failures...)
}
