//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"syscall/js"
)

var propsRegisteredBytes int
var propsReservations int
var mergeNextInstance uint64

type mergeClientView struct {
	root, view, parent js.Value
	child              *Scope
	instance           uint64
}

func init() {
	propsPhysicalAdmission = func() (func(), error) {
		activity, err := beginRefreshActivity()
		if err != nil {
			return nil, errPropsStale
		}
		lease, err := beginPropsRetainedTask()
		if err != nil {
			activity.Done()
			return nil, errPropsCapacity
		}
		return func() { lease.Done(); activity.Done() }, nil
	}
}
func runMergeCallback(fn func()) error {
	if fn == nil {
		return errPropsInvalid
	}
	_, err := propsInvoke(context.Background(), func() bool { return true }, func() (struct{}, error) {
		release, err := mergeEnterCallbackRead()
		if err != nil {
			return struct{}{}, err
		}
		defer release()
		fn()
		return struct{}{}, nil
	})
	return err
}
func (s *Scope) mergeCallbackGuard() func(func()) error {
	for owner := s; owner != nil; owner = owner.parent {
		if owner.mergeCallbacks {
			return runMergeCallback
		}
	}
	return nil
}
func (s *Scope) enableMergeCallbacks() error {
	if s == nil || !s.propsLive() {
		return errPropsClosed
	}
	var tree []lifetimeActivity
	if s.lifetime != nil {
		var err error
		tree, err = s.lifetime.activityTree()
		if err != nil {
			return errPropsCapacity
		}
	}
	s.mergeCallbacks = true
	for _, entry := range tree {
		entry.owner.callbackGuard = runMergeCallback
	}
	return nil
}
func (s *Scope) mergeInstance() uint64 {
	if s == nil || s.closed || s.mergeRevoked {
		return 0
	}
	if s.mergeID == 0 {
		if mergeNextInstance == ^uint64(0) {
			s.revokeMergeAuthority()
			return 0
		}
		mergeNextInstance++
		s.mergeID = mergeNextInstance
	}
	return s.mergeID
}
func (s *Scope) propsInstance() uint64 {
	if s == nil {
		return 0
	}
	return s.mergeID
}
func (s *Scope) propsLive() bool {
	if s == nil {
		return false
	}
	for owner := s; owner != nil; owner = owner.parent {
		if owner.closed || owner.mergeRevoked {
			return false
		}
	}
	return true
}
func (s *Scope) revokeMergeAuthority() {
	if s == nil || s.mergeRevoked {
		return
	}
	s.mergeRevoked = true
	if s.propsPreparingCancel != nil {
		s.propsPreparingCancel()
	}
	if s.propsSource != nil {
		s.propsSource.propsRevoke()
	}
	if s.lifetime != nil {
		s.lifetime.revoked = true
	}
}
func (s *Scope) mergeClientReady() (ready bool) {
	defer func() {
		if recover() != nil {
			ready = false
		}
	}()
	if moduleMode || s == nil || !s.active() || s.parent != nil || s.mountOpen || !s.clientViewUsed || s.propsSource == nil || !s.propsSource.propsValid() {
		return false
	}
	view := s.mergeView
	if view == nil || len(s.children) != 1 || s.lifetime == nil || !s.lifetime.visible() || view.child == nil || view.child.lifetime == nil || !view.child.lifetime.visible() || !view.child.active() || view.child.parent != s || view.instance == 0 || view.instance != s.mergeID {
		return false
	}
	if _, ok := s.children[view.child]; !ok {
		return false
	}
	return view.root.Equal(s.Root.Value) && view.child.Root.Value.Equal(view.view) && view.root.Get("isConnected").Bool() && view.root.Get("parentNode").Equal(view.parent) && view.root.Get("childNodes").Length() == 1 && view.root.Get("firstChild").Equal(view.view) && view.root.Call("getAttribute", clientBoundaryAttribute).String() == "1"
}

// Props registers one read-only source during this exact island root's Mount.
// Children use the same source explicitly as an Effect dependency; rebinding
// through a child or a second root registration is rejected atomically.
func Props[T any](scope *Scope, raw json.RawMessage, options PropsOptions[T]) (*PropsSource[T], error) {
	if scope == nil || !scope.active() || !scope.mountOpen || scope.parent != nil || scope.propsSource != nil || scope.propsPending || options.Clone == nil || scope.Root.Value.Type() != js.TypeObject || scope.Root.Value.IsNull() || scope.Root.Value.Get("hasAttribute").Type() != js.TypeFunction || !scope.Root.Value.Call("hasAttribute", "data-nextgo-island").Bool() {
		return nil, errPropsInvalid
	}
	if propsReservations >= propsSourceLimit || len(raw) == 0 || len(raw) > propsBytesLimit || propsRegisteredBytes > propsAggregateLimit-len(raw) || propsNextID == ^uint64(0) {
		return nil, errPropsCapacity
	}
	if err := ensureMergeCallbackCapacity(); err != nil {
		return nil, err
	}
	instance := scope.mergeInstance()
	if instance == 0 {
		return nil, errPropsCapacity
	}
	scope.propsPending = true
	propsReservations++
	propsRegisteredBytes += len(raw)
	committed := false
	ctx, cancel := context.WithCancel(context.Background())
	scope.propsPreparingCancel = cancel
	defer func() {
		scope.propsPreparingCancel = nil
		scope.propsPending = false
		if !committed {
			cancel()
			propsReservations--
			propsRegisteredBytes -= len(raw)
		}
	}()
	p := &PropsSource[T]{owner: scope, instance: instance, options: options, ctx: ctx, cancel: cancel, dependencies: map[uint64]func(){}}
	frame := currentPropsFrame
	identity, location, revision := propsIdentity(frame), frame.location, frame.revision
	fence := func() bool {
		return scope.mountOpen && scope.propsSource == nil && scope.propsPending && scope.propsLive() && propsFrameMatches(identity)
	}
	value, err := p.propsPrepare(ctx, raw, location, revision, fence)
	if err != nil {
		return nil, err
	}
	if !fence() || propsNextID == ^uint64(0) {
		return nil, errPropsStale
	}
	if err := scope.enableMergeCallbacks(); err != nil {
		return nil, err
	}
	propsNextID++
	p.id = propsNextID
	p.initial, p.initialRevision, p.weight = value, revision, len(raw)
	p.onClose = func(weight int) { propsReservations--; propsRegisteredBytes -= weight }
	p.onWeight = func(before, after int) { propsRegisteredBytes += after - before }
	propsBindings[p.id] = p
	scope.propsSource = p
	scope.cleanupFramework(p.propsClose)
	committed = true
	return p, nil
}
