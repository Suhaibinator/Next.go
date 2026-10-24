package client

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"sync"
	"testing"
)

func sumOptimistic(t *testing.T, limit int) *Optimistic[int, int] {
	t.Helper()
	m, err := NewOptimistic(10, func(n, delta int) int { return n + delta }, OptimisticOptions[int, int]{Limit: limit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	return m
}
func TestOptimisticCanonicalConfirmationRollbackAndCapacity(t *testing.T) {
	m := sumOptimistic(t, 3)
	var snapshots []OptimisticSnapshot[int]
	stop, err := m.Subscribe(func(s OptimisticSnapshot[int]) { snapshots = append(snapshots, s) })
	if err != nil || len(snapshots) != 1 || snapshots[0].Value != 10 {
		t.Fatal(err, snapshots)
	}
	a, _ := m.Begin(1)
	b, _ := m.Begin(5)
	c, _ := m.Begin(2)
	if m.Snapshot() != (OptimisticSnapshot[int]{Value: 18, Pending: 3}) {
		t.Fatal(m.Snapshot())
	}
	if _, err := m.Begin(1); !errors.Is(err, ErrOptimisticLimit) {
		t.Fatal(err)
	}
	if err := b.Resolve(2); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Value != 15 || m.Snapshot().Pending != 2 || m.Snapshot().Available != 0 {
		t.Fatal(m.Snapshot())
	}
	if err := c.Reject(); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Value != 13 || m.Snapshot().Available != 1 {
		t.Fatal("rejection did not free capacity", m.Snapshot())
	}
	if err := a.Commit(); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot() != (OptimisticSnapshot[int]{Value: 13, Available: 3}) {
		t.Fatal(m.Snapshot())
	}
	for _, settle := range []func() error{a.Commit, b.Reject, c.Reject} {
		if err := settle(); !errors.Is(err, ErrOptimisticSettled) {
			t.Fatal("late result accepted", err)
		}
	}
	stop()
	stop()
	before := len(snapshots)
	m.Begin(1)
	if len(snapshots) != before {
		t.Fatal("unsubscribe failed")
	}
}

func TestOptimisticNoncommutativeOperationsKeepIntentOrder(t *testing.T) {
	for _, rejectFirst := range []bool{false, true} {
		t.Run(map[bool]string{true: "reject earlier", false: "confirm earlier"}[rejectFirst], func(t *testing.T) {
			m, err := NewOptimistic("initial", func(_ string, set string) string { return set })
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			a, _ := m.Begin("first tentative")
			b, _ := m.Begin("second tentative")
			if err := b.Resolve("second accepted"); err != nil {
				t.Fatal(err)
			}
			if m.Snapshot().Value != "second accepted" || m.Snapshot().Pending != 1 {
				t.Fatal(m.Snapshot())
			}
			if rejectFirst {
				err = a.Reject()
			} else {
				err = a.Resolve("first accepted")
			}
			if err != nil || m.Snapshot().Value != "second accepted" || m.Snapshot().Pending != 0 {
				t.Fatal(err, m.Snapshot())
			}
		})
	}
}

func TestOptimisticResetCloseAndCloneOwnership(t *testing.T) {
	type action struct{ values map[string]int }
	m, err := NewOptimistic(map[string]int{"one": 1}, func(values map[string]int, a action) map[string]int {
		for k, v := range a.values {
			values[k] = v
		}
		return values
	}, OptimisticOptions[map[string]int, action]{
		CloneValue: maps.Clone[map[string]int], CloneAction: func(a action) action { return action{maps.Clone(a.values)} },
	})
	if err != nil {
		t.Fatal(err)
	}
	a := action{map[string]int{"two": 2}}
	intent, _ := m.Begin(a)
	a.values["two"] = 200
	snap := m.Snapshot()
	snap.Value["one"] = 99
	if got := m.Snapshot().Value; !reflect.DeepEqual(got, map[string]int{"one": 1, "two": 2}) {
		t.Fatal(got)
	}
	if err := m.Reset(map[string]int{"fresh": 5}); err != nil {
		t.Fatal(err)
	}
	if err := intent.Commit(); !errors.Is(err, ErrOptimisticSettled) {
		t.Fatal(err)
	}
	if got := m.Snapshot(); got.Pending != 0 || !reflect.DeepEqual(got.Value, map[string]int{"fresh": 5}) {
		t.Fatal(got)
	}
	calls := 0
	m.Subscribe(func(OptimisticSnapshot[map[string]int]) { calls++ })
	m.Close()
	m.Close()
	if calls != 1 || !m.Snapshot().Closed || m.Snapshot().Value != nil {
		t.Fatal("close retained data or notified observers", calls, m.Snapshot())
	}
	if _, err := m.Begin(action{}); !errors.Is(err, ErrOptimisticClosed) {
		t.Fatal(err)
	}
	if err := m.Reset(nil); !errors.Is(err, ErrOptimisticClosed) {
		t.Fatal(err)
	}
	if _, err := m.Subscribe(func(OptimisticSnapshot[map[string]int]) {}); !errors.Is(err, ErrOptimisticClosed) {
		t.Fatal(err)
	}
}

func TestOptimisticCallbackFailureIsolationAndReentrantObservers(t *testing.T) {
	reported := 0
	m, err := NewOptimistic(0, func(n, delta int) int {
		if delta == 99 {
			panic("invalid reducer input")
		}
		return n + delta
	}, OptimisticOptions[int, int]{OnObserverError: func(error) { reported++ }})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Begin(99); err == nil || m.Snapshot().Value != 0 || m.Snapshot().Pending != 0 {
		t.Fatal("panic committed a partial begin", err, m.Snapshot())
	}
	intent, _ := m.Begin(1)
	if err := intent.Resolve(99); err == nil || m.Snapshot().Value != 1 || m.Snapshot().Pending != 1 {
		t.Fatal("panic committed a partial settlement", err, m.Snapshot())
	}
	m.Subscribe(func(OptimisticSnapshot[int]) { panic("broken view") })
	seen := []int{}
	var stop func()
	stop, err = m.Subscribe(func(s OptimisticSnapshot[int]) {
		seen = append(seen, s.Value)
		if s.Value == 3 {
			stop()
			m.Reset(7)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Begin(2)
	if m.Snapshot().Value != 7 || !reflect.DeepEqual(seen, []int{1, 3}) || reported < 2 {
		t.Fatal(m.Snapshot(), seen, reported)
	}
	if err := intent.Reject(); !errors.Is(err, ErrOptimisticSettled) {
		t.Fatal(err)
	}
}

func TestOptimisticBoundsAndConcurrentSettlements(t *testing.T) {
	if _, err := NewOptimistic[int, int](0, nil); err == nil {
		t.Fatal("nil reducer accepted")
	}
	for _, limit := range []int{-1, maxOptimisticIntents + 1} {
		if _, err := NewOptimistic(0, func(n, a int) int { return n + a }, OptimisticOptions[int, int]{Limit: limit}); err == nil {
			t.Fatal("invalid limit", limit)
		}
	}
	m := sumOptimistic(t, 128)
	if _, err := m.Subscribe(nil); err == nil {
		t.Fatal("nil observer accepted")
	}
	for i := 0; i < maxOptimisticObservers; i++ {
		if _, err := m.Subscribe(func(OptimisticSnapshot[int]) {}); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := m.Subscribe(func(OptimisticSnapshot[int]) {}); err == nil {
		t.Fatal("observer bound exceeded")
	}
	intents := make([]*OptimisticIntent[int, int], 128)
	for i := range intents {
		intents[i], _ = m.Begin(1)
	}
	var workers sync.WaitGroup
	for i, intent := range intents {
		workers.Add(1)
		go func(i int, p *OptimisticIntent[int, int]) {
			defer workers.Done()
			var err error
			if i%2 == 0 {
				err = p.Commit()
			} else {
				err = p.Reject()
			}
			if err != nil {
				t.Error(err)
			}
		}(i, intent)
	}
	workers.Wait()
	if got := m.Snapshot(); got.Value != 74 || got.Pending != 0 || got.Available != 128 {
		t.Fatal(got)
	}
}

func TestOptimisticAllFourIntentSettlementOrdersAndRejectionMasks(t *testing.T) {
	var visit func([]int, []int)
	visit = func(order, remaining []int) {
		if len(remaining) > 0 {
			for i, id := range remaining {
				next := append([]int(nil), remaining[:i]...)
				next = append(next, remaining[i+1:]...)
				visit(append(append([]int(nil), order...), id), next)
			}
			return
		}
		for mask := 0; mask < 16; mask++ {
			m, err := NewOptimistic("root", func(value, token string) string { return value + "/" + token }, OptimisticOptions[string, string]{Limit: 4})
			if err != nil {
				t.Fatal(err)
			}
			intents := make([]*OptimisticIntent[string, string], 4)
			statuses := [4]int{}
			for i := range intents {
				intents[i], err = m.Begin(fmt.Sprintf("pending%d", i))
				if err != nil {
					t.Fatal(err)
				}
			}
			for step, id := range order {
				if mask&(1<<id) != 0 {
					statuses[id] = 2
					err = intents[id].Reject()
				} else {
					statuses[id] = 1
					err = intents[id].Resolve(fmt.Sprintf("accepted%d", id))
				}
				if err != nil {
					t.Fatal(order, mask, err)
				}
				expected := "root"
				firstPending := 4
				for i, status := range statuses {
					if status == 0 && firstPending == 4 {
						firstPending = i
					}
					if status == 0 {
						expected += fmt.Sprintf("/pending%d", i)
					} else if status == 1 {
						expected += fmt.Sprintf("/accepted%d", i)
					}
				}
				buffered := 0
				for _, status := range statuses[firstPending:] {
					if status != 2 {
						buffered++
					}
				}
				actual := m.Snapshot()
				if actual.Value != expected || actual.Pending != 3-step || actual.Available != 4-buffered {
					t.Fatalf("order %v mask %d step %d: got %+v, want value %q pending %d available %d", order, mask, step, actual, expected, 3-step, 4-buffered)
				}
			}
			m.Close()
		}
	}
	visit(nil, []int{0, 1, 2, 3})
}
