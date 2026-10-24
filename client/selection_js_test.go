//go:build js && wasm

package client

import (
	"strings"
	"syscall/js"
	"testing"
)

func selectionEnvironment(t *testing.T) string {
	t.Helper()
	oldDoc, oldSelections, oldObservers := doc, selections, selectionObservers
	query := js.FuncOf(func(js.Value, []js.Value) any { return []any{} })
	root := js.ValueOf(map[string]any{"querySelectorAll": query})
	get := js.FuncOf(func(js.Value, []js.Value) any { return root })
	doc = js.ValueOf(map[string]any{"getElementById": get})
	selections = map[string]Selection{}
	selectionObservers = map[uint64]selectionObserver{}
	t.Cleanup(func() {
		doc, selections, selectionObservers = oldDoc, oldSelections, oldObservers
		get.Release()
		query.Release()
	})
	return strings.Repeat("a", 64)
}

func TestSelectionObserversOwnSnapshotsAndFollowCommits(t *testing.T) {
	key := selectionEnvironment(t)
	scope := &Scope{}
	calls := 0
	present := false
	stop, err := scope.ObserveSelection(key, func(value Selection, ok bool) {
		calls++
		present = ok
		if ok {
			value.Segments[0] = "mutated"
		}
	})
	if err != nil || calls != 1 || present {
		t.Fatal(err, calls, present)
	}
	value := Selection{Scope: key, Name: "children", Segments: []string{"posts"}}
	installSelections(map[string]Selection{key: value})
	if calls != 2 || !present {
		t.Fatal(calls, present)
	}
	copy, ok := CurrentSelection(key)
	if !ok || copy.Segments[0] != "posts" {
		t.Fatal(copy, ok)
	}
	copy.Segments[0] = "changed"
	installSelections(map[string]Selection{key: value.Clone()})
	if calls != 2 {
		t.Fatal("unchanged commit notified", calls)
	}
	installSelections(map[string]Selection{})
	if calls != 3 || present {
		t.Fatal(calls, present)
	}
	stop()
	stop()
	installSelections(map[string]Selection{key: value})
	if calls != 3 {
		t.Fatal("unsubscribe did not stop callbacks")
	}
	scope.dispose()
	if len(selectionObservers) != 0 {
		t.Fatal("observer survived island disposal")
	}
}

func TestSelectionObserverCleanupAndReentrantRemoval(t *testing.T) {
	key := selectionEnvironment(t)
	scope := &Scope{}
	calls := 0
	_, err := scope.ObserveSelection(key, func(Selection, bool) {
		calls++
		if calls == 2 {
			scope.dispose()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	installSelections(map[string]Selection{key: {Scope: key, Name: "children", Segments: []string{"one"}}})
	installSelections(map[string]Selection{})
	if calls != 2 || len(selectionObservers) != 0 {
		t.Fatal(calls, len(selectionObservers))
	}
	if _, err := scope.ObserveSelection(key, func(Selection, bool) {}); err == nil {
		t.Fatal("closed scope accepted observer")
	}
	open := &Scope{}
	defer open.dispose()
	for i := 0; i < 1000; i++ {
		stop, err := open.ObserveSelection(key, func(Selection, bool) {})
		if err != nil {
			t.Fatal(err)
		}
		stop()
	}
	if len(open.cleanup) != 1 {
		t.Fatal("repeated subscriptions accumulate cleanup closures", len(open.cleanup))
	}
	if _, err := open.ObserveSelection("bad", func(Selection, bool) {}); err == nil {
		t.Fatal("invalid identity accepted")
	}
	if _, err := open.ObserveSelection(key, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	for i := 0; i < maxSelectionObservers; i++ {
		if _, err := open.ObserveSelection(key, func(Selection, bool) {}); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := open.ObserveSelection(key, func(Selection, bool) {}); err == nil {
		t.Fatal("unbounded observers")
	}
}
