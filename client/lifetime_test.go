package client

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestLifetimeEffectDependencyCleanupAndDisposal(t *testing.T) {
	life := NewLifetime()
	value := NewState(1)
	other := NewState(10)
	events := []string{}
	effect, err := life.Effect(func() (func(), error) {
		snapshot := value.Get() + other.Get()
		events = append(events, "setup")
		return func() {
			if snapshot < 0 {
				t.Fatal(snapshot)
			}
			events = append(events, "cleanup")
		}, nil
	}, value, other, value)
	if err != nil {
		t.Fatal(err)
	}
	value.Set(2)
	other.Set(11)
	effect.Close()
	effect.Close()
	value.Set(3)
	life.Close()
	if !reflect.DeepEqual(events, []string{"setup", "cleanup", "setup", "cleanup", "setup", "cleanup"}) {
		t.Fatal(events)
	}
	if len(value.effects) != 0 || len(other.effects) != 0 || len(life.effects) != 0 {
		t.Fatal("retained subscriptions")
	}
	if !errors.Is(effect.Invalidate(), ErrEffectClosed) {
		t.Fatal("closed effect ran")
	}
}
func TestLifetimeContextIdentityShadowNilAndClosed(t *testing.T) {
	root := NewLifetime()
	first, _ := root.Child()
	second, _ := root.Child()
	grandchild, _ := first.Child()
	key := NewContext[int]()
	sameType := NewContext[int]()
	if err := Provide(root, key, 1); err != nil {
		t.Fatal(err)
	}
	Provide(first, key, 2)
	for life, want := range map[*Lifetime]int{root: 1, first: 2, second: 1, grandchild: 2} {
		if got, ok := UseContext(life, key); !ok || got != want {
			t.Fatal(got, ok, want)
		}
	}
	if _, ok := UseContext(root, sameType); ok {
		t.Fatal("distinct key leaked")
	}
	nilKey := NewContext[any]()
	Provide(root, nilKey, "outer")
	Provide(first, nilKey, nil)
	if got, ok := UseContext(grandchild, nilKey); !ok || got != nil {
		t.Fatal("nil shadow lost", got, ok)
	}
	first.Close()
	if !grandchild.Closed() {
		t.Fatal("child stayed open")
	}
	if _, ok := UseContext(first, key); ok {
		t.Fatal("closed read")
	}
	if !errors.Is(Provide(first, key, 9), ErrLifetimeClosed) {
		t.Fatal("closed write")
	}
	var zero ContextKey[int]
	if Provide(root, zero, 1) == nil {
		t.Fatal("zero key")
	}
	root.Close()
	if !second.Closed() || len(root.values) != 0 {
		t.Fatal("close retained values")
	}
}
func TestLifetimeReentrantEffectsAndLoopBound(t *testing.T) {
	life := NewLifetime()
	value := NewState(0)
	runs, cleanups := 0, 0
	e, err := life.Effect(func() (func(), error) {
		runs++
		if value.Get() < 3 {
			value.Set(value.Get() + 1)
		}
		return func() { cleanups++ }, nil
	}, value)
	if err != nil || runs != 4 || cleanups != 3 {
		t.Fatal(err, runs, cleanups)
	}
	e.Close()
	if cleanups != 4 {
		t.Fatal(cleanups)
	}
	_, err = life.Effect(func() (func(), error) { value.Set(value.Get() + 1); return func() { cleanups++ }, nil }, value)
	if err == nil || len(value.effects) != 0 || len(life.effects) != 0 {
		t.Fatal("unbounded recursive effect", err)
	}
}
func TestLifetimeFailuresReleaseResourcesAndIsolateObservers(t *testing.T) {
	reported := 0
	life := NewLifetime(LifetimeOptions{OnError: func(error) { reported++ }})
	value := NewState(0)
	cleaned := 0
	e, err := life.Effect(func() (func(), error) {
		if value.Get() == 1 {
			return func() { cleaned++ }, errors.New("setup failed")
		}
		return func() { cleaned++ }, nil
	}, value)
	if err != nil {
		t.Fatal(err)
	}
	value.Set(1)
	if reported != 1 || cleaned != 2 || e.Err() == nil || len(value.effects) != 0 {
		t.Fatal(reported, cleaned, e.Err())
	}
	if _, err := life.Effect(func() (func(), error) { panic("setup") }, value); err == nil || len(value.effects) != 0 {
		t.Fatal("setup panic leaked")
	}
	e, err = life.Effect(func() (func(), error) { return func() { panic("cleanup") }, nil }, value)
	if err != nil {
		t.Fatal(err)
	}
	value.Set(2)
	if reported != 2 || e.Err() == nil || len(value.effects) != 0 {
		t.Fatal("cleanup panic leaked")
	}
	var nilState *State[int]
	if _, err := life.Effect(func() (func(), error) { return nil, nil }, value, nilState); err == nil || len(value.effects) != 0 {
		t.Fatal("partial registration leaked")
	}
	life.Close()
}
func TestLifetimeCloseDuringSetupAndCleanup(t *testing.T) {
	life := NewLifetime()
	cleaned := 0
	e, err := life.Effect(func() (func(), error) { life.Close(); return func() { cleaned++ }, nil })
	if err != nil || cleaned != 1 || !e.closed {
		t.Fatal(err, cleaned)
	}
	life = NewLifetime()
	value := NewState(0)
	setups := 0
	_, err = life.Effect(func() (func(), error) { setups++; return life.Close, nil }, value)
	if err != nil {
		t.Fatal(err)
	}
	value.Set(1)
	if setups != 1 || !life.Closed() || len(value.effects) != 0 {
		t.Fatal(setups)
	}
}
func TestLifetimeCleanupOrderAndUnregistrationBounds(t *testing.T) {
	life := NewLifetime()
	events := []int{}
	child, _ := life.Child()
	child.Defer(func() { events = append(events, 1) })
	life.Defer(func() { events = append(events, 2) })
	life.Defer(func() { events = append(events, 3) })
	for i := 0; i < 2000; i++ {
		r, err := life.Defer(func() {})
		if err != nil {
			t.Fatal(err)
		}
		r.Close()
		c, err := life.Child()
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
		e, err := life.Effect(func() (func(), error) { return nil, nil })
		if err != nil {
			t.Fatal(err)
		}
		e.Close()
	}
	if len(life.resources) != 2 || len(life.children) != 1 || len(life.effects) != 0 {
		t.Fatal("unregistration leaked")
	}
	life.Close()
	life.Close()
	if !reflect.DeepEqual(events, []int{1, 3, 2}) {
		t.Fatal(events)
	}
	if _, err := life.Defer(func() {}); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal(err)
	}
}
func TestLifetimeLimitsAndCleanupPanicIsolation(t *testing.T) {
	life := NewLifetime(LifetimeOptions{OnError: func(error) {}})
	cursor := life
	for i := 0; i < 32; i++ {
		cursor, _ = cursor.Child()
	}
	if _, err := cursor.Child(); err == nil {
		t.Fatal("unbounded depth")
	}
	for i := 0; i < 128; i++ {
		if err := Provide(life, NewContext[int](), i); err != nil {
			t.Fatal(err)
		}
	}
	if Provide(life, NewContext[int](), 1) == nil {
		t.Fatal("unbounded providers")
	}
	for i := 0; i < 128; i++ {
		if _, err := life.Effect(func() (func(), error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := life.Effect(func() (func(), error) { return nil, nil }); err == nil {
		t.Fatal("unbounded effects")
	}
	for i := 0; i < 256; i++ {
		life.Defer(func() {})
	}
	if _, err := life.Defer(func() {}); err == nil {
		t.Fatal("unbounded resources")
	}
	life.Close()
	cleaned := 0
	life = NewLifetime(LifetimeOptions{OnError: func(error) { panic("report") }})
	life.Defer(func() { cleaned++ })
	life.Defer(func() { panic("release") })
	life.Close()
	if cleaned != 1 {
		t.Fatal("cleanup skipped")
	}
}

func TestLifetimeEffectCancelsPreviousWorkBeforeNextSetup(t *testing.T) {
	life := NewLifetime()
	value := NewState(0)
	var previous context.Context
	runs := 0
	_, err := life.Effect(func() (func(), error) {
		if previous != nil && previous.Err() != context.Canceled {
			t.Fatal("old work remained active")
		}
		ctx, cancel := context.WithCancel(context.Background())
		previous = ctx
		runs++
		return cancel, nil
	}, value)
	if err != nil {
		t.Fatal(err)
	}
	value.Set(1)
	life.Close()
	if runs != 2 || previous.Err() != context.Canceled {
		t.Fatal(runs, previous.Err())
	}
}
func TestLifetimeEffectRejectsInvalidDependenciesAndBounds(t *testing.T) {
	life := NewLifetime()
	value := NewState(0)
	type wrapped struct{ Dependency }
	type noncomparable struct {
		Dependency
		data []int
	}
	for _, deps := range [][]Dependency{{nil}, {value, wrapped{}}, {noncomparable{Dependency: value}}, make([]Dependency, 17)} {
		if _, err := life.Effect(func() (func(), error) { return nil, nil }, deps...); err == nil || len(life.effects) != 0 || len(value.effects) != 0 {
			t.Fatal("invalid registration retained resources", err)
		}
	}
	owners := []*Lifetime{}
	for i := 0; i < 256; i++ {
		owner := NewLifetime()
		owners = append(owners, owner)
		if _, err := owner.Effect(func() (func(), error) { return nil, nil }, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := life.Effect(func() (func(), error) { return nil, nil }, value); err == nil {
		t.Fatal("state dependency limit ignored")
	}
	for _, owner := range owners {
		owner.Close()
	}
	if len(value.effects) != 0 {
		t.Fatal("state observers retained")
	}
}

func TestChildCleanupMayCloseParentWithoutRetainingClosingChild(t *testing.T) {
	root := NewLifetime()
	child, _ := root.Child()
	sibling, _ := root.Child()
	cleaned := make(chan struct{})
	sibling.Defer(func() {})
	child.Defer(root.Close)
	go func() { child.Close(); close(cleaned) }()
	select {
	case <-cleaned:
	case <-time.After(2 * time.Second):
		t.Fatal("reentrant parent close did not finish")
	}
	if !root.Closed() || !child.Closed() || !sibling.Closed() || len(root.children) != 0 {
		t.Fatal("family not disposed")
	}
}

func TestClosingAncestorSuppressesSiblingEffectsAndNewResources(t *testing.T) {
	root := NewLifetime()
	first, _ := root.Child()
	second, _ := root.Child()
	state := NewState(0)
	runs, cleanups := 0, 0
	first.Effect(func() (func(), error) { runs++; return func() { cleanups++ }, nil }, state)
	second.Defer(func() {
		if !first.Closed() {
			t.Error("closing ancestor still appeared open")
		}
		state.Set(1)
		if _, err := first.Child(); !errors.Is(err, ErrLifetimeClosed) {
			t.Error(err)
		}
		if _, err := first.Defer(func() {}); !errors.Is(err, ErrLifetimeClosed) {
			t.Error(err)
		}
		if _, err := first.Effect(func() (func(), error) { return nil, nil }); !errors.Is(err, ErrLifetimeClosed) {
			t.Error(err)
		}
		if err := Provide(first, NewContext[int](), 1); !errors.Is(err, ErrLifetimeClosed) {
			t.Error(err)
		}
	})
	root.Close()
	if runs != 1 || cleanups != 1 || len(state.effects) != 0 {
		t.Fatal("effect ran under closing ancestor", runs, cleanups)
	}
}
