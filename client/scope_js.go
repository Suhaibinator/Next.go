//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"
)

type Element struct{ Value js.Value }

func (e Element) Text(value string) {
	if e.Value.Get("textContent").String() == value {
		return
	}
	child := e.Value.Get("firstChild")
	if !child.IsNull() && !child.IsUndefined() && child.Get("nodeType").Int() == 3 && e.Value.Get("childNodes").Length() == 1 {
		child.Set("nodeValue", value)
		return
	}
	e.Value.Set("textContent", value)
}
func (e Element) Property(name string, value any) { e.Value.Set(name, value) }
func (e Element) Attribute(name, value string)    { e.Value.Call("setAttribute", name, value) }

type Event struct{ Value js.Value }

func (e Event) PreventDefault()    { e.Value.Call("preventDefault") }
func (e Event) InputValue() string { return e.Value.Get("target").Get("value").String() }

type Scope struct {
	mountOpen              bool
	mountClient            bool
	clientViewUsed         bool
	clientView             *clientViewTransaction
	lifetime               *Lifetime
	parent                 *Scope
	children               map[*Scope]struct{}
	childCleanup           bool
	owner                  js.Value
	keyedCount, keyedDepth int
	keyedLists             map[keyedCloser]struct{}
	Root                   Element
	cleanup                []func()
	closed                 bool
	suspended              bool
	activityEpoch          uint64
	retainedReport         *retainedScopeReport
	listeners              []*scopeListener
	asyncResources         map[*AsyncResource]struct{}
	selectionCleanup       bool
	locationCleanup        bool
	linkStatusCleanup      bool
	taskContext            context.Context
	taskCancel             context.CancelFunc
	taskCleanup            bool
	taskWorkers            int
}

// Require fails hydration if expected server markup is absent. It does not create nodes.
func (s *Scope) Require(selector string) (Element, error) {
	nodes := s.Root.Value.Call("querySelectorAll", selector)
	for i := 0; i < nodes.Length(); i++ {
		v := nodes.Index(i)
		if s.ownsElement(v, false) {
			return Element{v}, nil
		}
	}
	return Element{}, fmt.Errorf("missing island element %q", selector)
}
func (s *Scope) Cleanup(fn func()) {
	if !s.active() {
		fn()
		return
	}
	s.cleanup = append(s.cleanup, fn)
}

// On callbacks must return promptly; start a goroutine for network operations.
func (s *Scope) On(el Element, event string, fn func(Event)) {
	if !s.active() {
		return
	}
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		defer func() {
			if v := recover(); v != nil {
				report(fmt.Sprintf("event error: %v", v))
			}
		}()
		if s.active() {
			lease, err := beginRefreshActivity()
			if err != nil {
				return nil
			}
			defer lease.Done()
			fn(Event{args[0]})
		}
		return nil
	})
	listener := &scopeListener{target: el.Value, event: event, callback: callback}
	registered := false
	defer func() {
		if !registered && !listener.closed {
			callback.Release()
		}
	}()
	listener.attach()
	s.listeners = append(s.listeners, listener)
	s.Cleanup(func() {
		defer func() { s.listeners = removeOwned(s.listeners, listener) }()
		listener.close()
	})
	registered = true
}
func (s *Scope) dispose() {
	if s.closed {
		return
	}
	reportOwner := s
	for reportOwner.parent != nil {
		reportOwner = reportOwner.parent
	}
	s.closed = true
	if s.parent != nil {
		delete(s.parent.children, s)
		s.parent = nil
	}
	for i := len(s.cleanup) - 1; i >= 0; i-- {
		func() {
			defer func() {
				if v := recover(); v != nil {
					reportScopeError(reportOwner, fmt.Sprint(v))
				}
			}()
			s.cleanup[i]()
		}()
	}
	s.cleanup = nil
}
func DecodeProps[T any](raw json.RawMessage) (T, error) {
	var p T
	err := json.Unmarshal(raw, &p)
	return p, err
}

// mountScoped gives eager and module islands the same failure/cleanup contract.
func mountScoped(scope *Scope, mount Mount, raw json.RawMessage) (err error) {
	if scope == nil || !scope.active() || scope.mountOpen {
		return fmt.Errorf("nextgo: component mount is unavailable")
	}
	boundary, err := validateClientBoundary(scope.Root.Value)
	if err != nil {
		scope.dispose()
		return err
	}
	scope.mountOpen = true
	scope.mountClient = boundary
	returned := false
	defer func() {
		scope.mountOpen = false
		scope.mountClient = false
		if failure := recover(); failure != nil {
			err = fmt.Errorf("mount panic: %v", failure)
		}
		if !returned && err == nil {
			err = fmt.Errorf("nextgo: component Mount did not return")
		}
		if err == nil && boundary {
			if scope.clientView == nil {
				err = fmt.Errorf("nextgo: client island Mount must call ClientView")
			} else {
				err = scope.clientView.validateCommit(scope)
			}
		}
		if err != nil {
			scope.dispose()
			if scope.clientView != nil {
				scope.clientView.rollback()
			}
		}
		// Successful mounting releases the detached fallback. On failure rollback
		// has already restored its exact nodes after scoped behavior was disposed.
		scope.clientView = nil
	}()
	err = mount(scope, raw)
	returned = true
	return err
}

func (s *Scope) islandRoot() js.Value {
	if !s.owner.IsUndefined() && !s.owner.IsNull() {
		return s.owner
	}
	return s.Root.Value
}
func (s *Scope) ownsElement(node js.Value, includeRoot bool) bool {
	if node.IsUndefined() || node.IsNull() || s.Root.Value.IsUndefined() || s.Root.Value.IsNull() {
		return false
	}
	if !s.Root.Value.Call("contains", node).Bool() && !(includeRoot && node.Equal(s.Root.Value)) {
		return false
	}
	for child := range s.children {
		if child.Root.Value.Call("contains", node).Bool() {
			return false
		}
	}
	nearest := node.Call("closest", "[data-nextgo-island]")
	if s.owner.IsUndefined() || s.owner.IsNull() {
		return nearest.Equal(s.Root.Value)
	}
	outer := s.Root.Value.Call("closest", "[data-nextgo-island]")
	return (outer.IsNull() || outer.Equal(s.owner)) && nearest.Equal(outer)
}

// active suppresses callbacks while an owning component is hidden or closing.
func (s *Scope) active() bool {
	if s == nil || moduleRefreshHandlersFrozen() || refreshFrozen() {
		return false
	}
	for owner := s; owner != nil; owner = owner.parent {
		if owner.closed || owner.suspended {
			return false
		}
	}
	return true
}

// Refresh initially supports explicit state cells and plain scoped listeners.
// Effects, providers, child scopes and keyed ownership need separate transfer
// contracts before a runtime containing them can be retired in place.
func refreshScopeSupported(s *Scope) bool {
	return s != nil && !s.closed && !s.clientViewUsed && s.lifetime == nil && len(s.children) == 0 &&
		len(s.keyedLists) == 0 && !s.selectionCleanup && !s.locationCleanup && !s.linkStatusCleanup
}
