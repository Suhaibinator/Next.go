//go:build js && wasm

package client

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/routeinfo"
)

type Selection = routeinfo.Selection

var selections = map[string]Selection{}

type selectionObserver struct {
	scope string
	owner *Scope
	fn    func(Selection, bool)
}

var selectionObservers = map[uint64]selectionObserver{}
var nextSelectionObserver uint64

const maxSelectionObservers = 512

// CurrentSelection returns an owned snapshot. Missing means the scope is not
// present in the committed view (for example after leaving a layout or denial).
func CurrentSelection(scope string) (Selection, bool) {
	value, ok := selections[scope]
	return value.Clone(), ok
}

// ObserveSelection synchronizes immediately, then observes successful commits.
// The subscription belongs to the island and is removed automatically on cleanup.
// Callbacks run on the Go/WASM event loop and must return promptly.
func (s *Scope) ObserveSelection(scope string, fn func(Selection, bool)) (func(), error) {
	if !s.active() {
		return nil, fmt.Errorf("selection observer requires an open scope")
	}
	if fn == nil || len(scope) != 64 {
		return nil, fmt.Errorf("invalid selection observer")
	}
	for _, r := range scope {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return nil, fmt.Errorf("invalid selection scope")
		}
	}
	if len(selectionObservers) >= maxSelectionObservers {
		return nil, fmt.Errorf("selection observer bound exceeded")
	}
	nextSelectionObserver++
	id := nextSelectionObserver
	observer := selectionObserver{scope: scope, owner: s, fn: fn}
	selectionObservers[id] = observer
	stop := func() { delete(selectionObservers, id) }
	if !s.selectionCleanup {
		s.selectionCleanup = true
		s.Cleanup(func() {
			for id, observer := range selectionObservers {
				if observer.owner == s {
					delete(selectionObservers, id)
				}
			}
		})
	}
	dispatchSelection(observer)
	return stop, nil
}
func dispatchSelection(observer selectionObserver) {
	if !observer.owner.active() {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			report(fmt.Sprintf("selection observer: %v", recovered))
		}
	}()
	value, ok := CurrentSelection(observer.scope)
	observer.fn(value, ok)
}
func selectionNodes(root js.Value) (map[string]Selection, error) {
	result := map[string]Selection{}
	nodes := root.Call("querySelectorAll", "[data-nextgo-selection]")
	if nodes.Length() > routeinfo.MaxSelections {
		return nil, fmt.Errorf("selection count bound exceeded")
	}
	total := 0
	for i := 0; i < nodes.Length(); i++ {
		raw := nodes.Index(i).Call("getAttribute", "data-nextgo-selection").String()
		total += len(raw)
		if total > routeinfo.MaxSelectionBytes {
			return nil, fmt.Errorf("selection snapshot byte bound exceeded")
		}
		value, err := routeinfo.Parse(raw)
		if err != nil {
			return nil, err
		}
		if _, exists := result[value.Scope]; exists {
			return nil, fmt.Errorf("duplicate selection scope")
		}
		result[value.Scope] = value
	}
	return result, nil
}
func installSelections(snapshot map[string]Selection) {
	notify := installSelectionsSilent(snapshot)
	notify()
}

func installSelectionsSilent(snapshot map[string]Selection) func() {
	previous := selections
	selections = snapshot
	// Retained layout/slot roots need fresh metadata even when their body stays.
	nodes := doc.Call("getElementById", "nextgo-app").Call("querySelectorAll", "[data-nextgo-selection]")
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		old, err := routeinfo.Parse(node.Call("getAttribute", "data-nextgo-selection").String())
		if err != nil {
			continue
		}
		if value, ok := snapshot[old.Scope]; ok {
			node.Call("setAttribute", "data-nextgo-selection", selectionEncoding(value))
		}
	}
	// Snapshot IDs, then consult the live map: cleanup/reentrant unsubscription
	// cannot trigger a callback that was removed earlier in this dispatch.
	ids := []uint64{}
	for id, observer := range selectionObservers {
		old, was := previous[observer.scope]
		next, now := snapshot[observer.scope]
		if was != now || !old.Equal(next) {
			ids = append(ids, id)
		}
	}
	return func() {
		for _, id := range ids {
			if observer, ok := selectionObservers[id]; ok {
				dispatchSelection(observer)
			}
		}
	}
}

func selectionEncoding(value Selection) string { data, _ := json.Marshal(value); return string(data) }
