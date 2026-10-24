//go:build js && wasm

package client

import (
	"errors"
	"testing"
)

func TestScopedOptimisticClosesWithoutNotifyingDisposedIsland(t *testing.T) {
	scope := &Scope{}
	options := []OptimisticOptions[int, int]{{}}
	m, err := NewScopedOptimistic(scope, 0, func(n, a int) int { return n + a }, options...)
	if err != nil {
		t.Fatal(err)
	}
	if options[0].OnObserverError != nil {
		t.Fatal("mutated caller options")
	}
	calls := 0
	m.Subscribe(func(OptimisticSnapshot[int]) { calls++ })
	intent, _ := m.Begin(1)
	scope.dispose()
	if calls != 2 || !m.Snapshot().Closed {
		t.Fatal(calls, m.Snapshot())
	}
	if err := intent.Commit(); !errors.Is(err, ErrOptimisticClosed) {
		t.Fatal(err)
	}
	if _, err := NewScopedOptimistic(scope, 0, func(n, a int) int { return n + a }); err == nil {
		t.Fatal("closed scope accepted")
	}
	if _, err := NewScopedOptimistic[int, int](nil, 0, func(n, a int) int { return n + a }); err == nil {
		t.Fatal("nil scope accepted")
	}
}
