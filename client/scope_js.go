//go:build js && wasm

package client

import (
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
	selectionCleanup       bool
	locationCleanup        bool
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
			fn(Event{args[0]})
		}
		return nil
	})
	el.Value.Call("addEventListener", event, callback)
	s.Cleanup(func() { el.Value.Call("removeEventListener", event, callback); callback.Release() })
}
func (s *Scope) dispose() {
	if s.closed {
		return
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
					report(fmt.Sprint(v))
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
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("mount panic: %v", failure)
		}
		if err != nil {
			scope.dispose()
		}
	}()
	return mount(scope, raw)
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

// active suppresses callbacks once any owning component begins disposal.
func (s *Scope) active() bool {
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
