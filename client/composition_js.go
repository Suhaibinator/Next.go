//go:build js && wasm

package client

import (
	"fmt"
	"syscall/js"
)

// Lifetime returns this scope's lazily allocated effect/context owner. Child
// scopes inherit providers through their Go parent, never through DOM globals.
func (s *Scope) Lifetime() (*Lifetime, error) {
	if !s.active() {
		return nil, ErrLifetimeClosed
	}
	if s.lifetime != nil {
		return s.lifetime, nil
	}
	var life *Lifetime
	if s.parent != nil {
		parent, err := s.parent.Lifetime()
		if err != nil {
			return nil, err
		}
		life, err = parent.Child()
		if err != nil {
			return nil, err
		}
	} else {
		life = NewLifetime(LifetimeOptions{OnError: func(err error) { report(err.Error()) }})
	}
	s.lifetime = life
	s.Cleanup(life.Close)
	return life, nil
}

// Subscope composes a browser component within an island without replacing its
// SSR root. The returned stop disposes behavior, leaving DOM in place. Parents
// automatically dispose children and exclude their DOM from Require queries.
func (s *Scope) Subscope(root Element) (*Scope, func(), error) {
	if !s.active() {
		return nil, nil, ErrLifetimeClosed
	}
	if root.Value.Type() != js.TypeObject || root.Value.IsNull() || root.Value.Get("nodeType").Type() != js.TypeNumber || root.Value.Get("nodeType").Int() != 1 {
		return nil, nil, fmt.Errorf("nextgo: component root must be an element")
	}
	if !s.ownsElement(root.Value, true) || root.Value.Equal(s.Root.Value) {
		return nil, nil, fmt.Errorf("nextgo: component root is outside the parent or already owned")
	}
	if len(s.children) >= 128 {
		return nil, nil, fmt.Errorf("nextgo: component child scope limit exceeded")
	}
	for child := range s.children {
		if root.Value.Call("contains", child.Root.Value).Bool() {
			return nil, nil, fmt.Errorf("nextgo: overlapping component roots")
		}
	}
	parent, err := s.Lifetime()
	if err != nil {
		return nil, nil, err
	}
	life, err := parent.Child()
	if err != nil {
		return nil, nil, err
	}
	child := &Scope{parent: s, owner: s.islandRoot(), Root: root, lifetime: life, keyedDepth: s.keyedDepth}
	child.Cleanup(life.Close)
	if s.children == nil {
		s.children = map[*Scope]struct{}{}
	}
	s.children[child] = struct{}{}
	if !s.childCleanup {
		s.childCleanup = true
		s.Cleanup(func() {
			for child := range s.children {
				child.dispose()
			}
			s.children = nil
		})
	}
	return child, child.dispose, nil
}
