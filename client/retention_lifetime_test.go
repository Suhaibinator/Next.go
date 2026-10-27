package client

import (
	"errors"
	"reflect"
	"testing"
)

func TestRetainedLifetimePreservesStateAndProvidersWhileEffectsPause(t *testing.T) {
	life := NewLifetime()
	value := NewState(0)
	key := NewContext[*State[int]]()
	if err := Provide(life, key, value); err != nil {
		t.Fatal(err)
	}
	var setups []int
	cleanups, permanent := 0, 0
	life.Defer(func() { permanent++ })
	effect, err := life.Effect(func() (func(), error) {
		setups = append(setups, value.Get())
		return func() { cleanups++ }, nil
	}, value)
	if err != nil {
		t.Fatal(err)
	}
	value.Set(1)
	if err := life.suspend(); err != nil {
		t.Fatal(err)
	}
	if life.Closed() || life.visible() || permanent != 0 || cleanups != 2 || effect.closed {
		t.Fatal("visibility was treated as permanent disposal", permanent, cleanups)
	}
	if stored, ok := UseContext(life, key); !ok || stored != value {
		t.Fatal("provider state was lost while hidden")
	}
	value.Set(2)
	value.Set(3)
	if err := effect.Invalidate(); err != nil || len(setups) != 2 || cleanups != 2 {
		t.Fatal("hidden effect performed setup or rejected dirty state", err, setups, cleanups)
	}
	if err := life.suspend(); err != nil || cleanups != 2 {
		t.Fatal("repeated suspension repeated cleanup", err, cleanups)
	}
	if err := life.resume(); err != nil {
		t.Fatal(err)
	}
	if err := life.resume(); err != nil || !reflect.DeepEqual(setups, []int{0, 1, 3}) || cleanups != 2 {
		t.Fatal("resume did not use exactly one latest setup", err, setups, cleanups)
	}
	life.Close()
	life.Close()
	if cleanups != 3 || permanent != 1 || len(value.effects) != 0 || !effect.closed {
		t.Fatal("terminal disposal leaked or repeated cleanup", cleanups, permanent)
	}
}

func TestRetainedLifetimeSuspendsChildrenFirstAndDoesNotDoubleRunNewEffects(t *testing.T) {
	life := NewLifetime()
	child, _ := life.Child()
	var events []string
	parentRuns, newRuns := 0, 0
	_, err := life.Effect(func() (func(), error) {
		parentRuns++
		events = append(events, "parent setup")
		if parentRuns == 2 {
			newChild, err := life.Child()
			if err != nil {
				return nil, err
			}
			if _, err := newChild.Effect(func() (func(), error) { newRuns++; return nil, nil }); err != nil {
				return nil, err
			}
			if newRuns != 1 {
				return nil, errors.New("new visible effect lost synchronous initial setup")
			}
		}
		return func() { events = append(events, "parent cleanup") }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	child.Effect(func() (func(), error) {
		events = append(events, "child setup")
		return func() { events = append(events, "child cleanup") }, nil
	})
	if err := life.suspend(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"parent setup", "child setup", "child cleanup", "parent cleanup"}) {
		t.Fatal("suspension cleanup order changed", events)
	}
	if err := life.resume(); err != nil || newRuns != 1 || parentRuns != 2 {
		t.Fatal("resume repeated a setup created by another setup", err, parentRuns, newRuns)
	}
	life.Close()
}

func TestRetainedLifetimeCleansSetupThatHidesItsOwnOwner(t *testing.T) {
	life := NewLifetime()
	runs, cleaned := 0, 0
	effect, err := life.Effect(func() (func(), error) {
		runs++
		if runs == 1 {
			if err := life.suspend(); err != nil {
				return nil, err
			}
		}
		return func() { cleaned++ }, nil
	})
	if err != nil || life.visible() || effect.closed || runs != 1 || cleaned != 1 {
		t.Fatal("setup survived hiding its owner", err, runs, cleaned)
	}
	if err := life.resume(); err != nil || runs != 2 || cleaned != 1 {
		t.Fatal("hidden definition was not resumable", err, runs, cleaned)
	}
	life.Close()
	if cleaned != 2 {
		t.Fatal(cleaned)
	}
}

func TestRetainedLifetimeCleanupFailureDoesNotSkipOthersOrPermanentRelease(t *testing.T) {
	reported, cleaned, permanent := 0, 0, 0
	life := NewLifetime(LifetimeOptions{OnError: func(error) { reported++ }})
	for i := 0; i < 3; i++ {
		index := i
		life.Effect(func() (func(), error) {
			return func() {
				cleaned++
				if index == 1 {
					panic("cleanup")
				}
			}, nil
		})
	}
	life.Defer(func() { permanent++ })
	if err := life.suspend(); err == nil || cleaned != 3 || permanent != 0 || reported != 1 {
		t.Fatal("failed hide skipped cleanup or disposed permanent state", err, cleaned, permanent, reported)
	}
	if err := life.resume(); err != nil || len(life.effects) != 2 {
		t.Fatal("healthy effects were lost", err, len(life.effects))
	}
	life.Close()
	if cleaned != 5 || permanent != 1 {
		t.Fatal("release was lost after failure", cleaned, permanent)
	}
}

func TestRetainedLifetimeDetectsReentrantVisibilityChangeAndClosedOwner(t *testing.T) {
	life := NewLifetime()
	first := true
	life.Effect(func() (func(), error) {
		return func() {
			if first {
				first = false
				_ = life.resume()
			}
		}, nil
	})
	if err := life.suspend(); !errors.Is(err, errLifetimeActivityChanged) {
		t.Fatal("reentrant resume was labeled a completed suspension", err)
	}
	life.Close()
	if err := life.resume(); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal("closed lifetime resumed", err)
	}
	if err := life.suspend(); !errors.Is(err, ErrLifetimeClosed) {
		t.Fatal("closed lifetime suspended", err)
	}
}

func TestRetainedLifetimeCapacityCheckDoesNotPartiallyHide(t *testing.T) {
	life := NewLifetime()
	for i := 0; i < 256; i++ {
		if _, err := life.Child(); err != nil {
			t.Fatal(err)
		}
	}
	if err := life.suspend(); err == nil || life.paused {
		t.Fatal("oversized tree partially changed visibility", err)
	}
	for _, child := range life.children {
		if child.paused {
			t.Fatal("capacity rejection hid a child")
		}
	}
	life.Close()
}

// Both effects exist before suspension. A must not make B run before B's
// scheduled resume turn, including when B belongs to an already existing child.
func TestRetainedResumeCoalescesAlreadyExistingDependentEffects(t *testing.T) {
	for _, descendant := range []bool{false, true} {
		name := "same owner"
		if descendant {
			name = "existing child"
		}
		t.Run(name, func(t *testing.T) {
			life := NewLifetime()
			t.Cleanup(life.Close)
			stateB := NewState(0)
			aRuns, bRuns, bCleanups, latest := 0, 0, 0, -1
			_, err := life.Effect(func() (func(), error) {
				aRuns++
				if aRuns == 2 {
					stateB.Set(1)
					stateB.Set(2)
					if bRuns != 1 {
						return nil, errors.New("existing B escaped the resume barrier")
					}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			owner := life
			if descendant {
				owner, err = life.Child()
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = owner.Effect(func() (func(), error) {
				bRuns++
				latest = stateB.Get()
				return func() { bCleanups++ }, nil
			}, stateB)
			if err != nil {
				t.Fatal(err)
			}
			if err := life.suspend(); err != nil {
				t.Fatal(err)
			}
			if err := life.resume(); err != nil {
				t.Fatal(err)
			}
			if aRuns != 2 || bRuns != 2 || bCleanups != 1 || latest != 2 {
				t.Fatal("existing dependent effect did not coalesce to one latest setup", aRuns, bRuns, bCleanups, latest)
			}
			// Ordinary visible updates keep their existing immediate semantics.
			stateB.Set(3)
			if bRuns != 3 || latest != 3 || bCleanups != 2 {
				t.Fatal(bRuns, latest, bCleanups)
			}
			life.Close()
			if bCleanups != 3 {
				t.Fatal("resumed cleanup repeated or leaked", bCleanups)
			}
		})
	}
}

func TestRetainedResumeIncludesEffectsCreatedWhileAncestorHidden(t *testing.T) {
	life := NewLifetime()
	t.Cleanup(life.Close)
	if err := life.suspend(); err != nil {
		t.Fatal(err)
	}
	child, err := life.Child()
	if err != nil {
		t.Fatal(err)
	}
	state := NewState(1)
	runs, latest := 0, 0
	_, err = child.Effect(func() (func(), error) { runs++; latest = state.Get(); return nil, nil }, state)
	if err != nil {
		t.Fatal(err)
	}
	state.Set(2)
	if runs != 0 {
		t.Fatal("hidden new child ran setup", runs)
	}
	if err := life.resume(); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || latest != 2 {
		t.Fatal("hidden new child was not drained once", runs, latest)
	}
}

func TestRetainedResumeDrainsBackEdgesWithBoundedCycleAndHealthyOwners(t *testing.T) {
	life := NewLifetime()
	t.Cleanup(life.Close)
	x, y := NewState(0), NewState(0)
	armed, reported, healthyRuns := false, 0, 0
	life.onError = func(error) { reported++ }
	a, err := life.Effect(func() (func(), error) {
		if armed {
			y.Set(y.Get() + 1)
		}
		return nil, nil
	}, x)
	if err != nil {
		t.Fatal(err)
	}
	_, err = life.Effect(func() (func(), error) {
		if armed {
			x.Set(x.Get() + 1)
		}
		return nil, nil
	}, y)
	if err != nil {
		t.Fatal(err)
	}
	_, err = life.Effect(func() (func(), error) { healthyRuns++; return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := life.suspend(); err != nil {
		t.Fatal(err)
	}
	armed = true
	if err := life.resume(); err == nil || !a.closed || reported == 0 || healthyRuns != 2 {
		t.Fatal("cycle was not bounded independently from healthy setup", err, a.closed, reported, healthyRuns)
	}
	if x.Get() != 64 || y.Get() != 64 {
		t.Fatal("resume follow-up work exceeded its bounded passes", x.Get(), y.Get())
	}
}

func TestRetainedResumeSetupFailureStillDrainsHealthyDefinitions(t *testing.T) {
	reported := 0
	life := NewLifetime(LifetimeOptions{OnError: func(error) { reported++ }})
	t.Cleanup(life.Close)
	failure := errors.New("resume setup failed")
	runs, healthyRuns, cleaned := 0, 0, 0
	broken, err := life.Effect(func() (func(), error) {
		runs++
		if runs == 2 {
			return func() { cleaned++ }, failure
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = life.Effect(func() (func(), error) { healthyRuns++; return nil, nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := life.suspend(); err != nil {
		t.Fatal(err)
	}
	if err := life.resume(); !errors.Is(err, failure) || !broken.closed || healthyRuns != 2 || cleaned != 1 || reported != 1 {
		t.Fatal("resume failure skipped healthy work or returned cleanup", err, broken.closed, healthyRuns, cleaned, reported)
	}
}
