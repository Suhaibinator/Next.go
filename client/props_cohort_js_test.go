//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"testing"
)

func TestPropsCohortPublishesIndependentRootsBeforeAnyDependencyAndKeepsTasks(t *testing.T) {
	propsBrowserEnvironment(t)
	aRuns, bRuns, unrelated := 0, 0, 0
	var b *PropsSource[int]
	shared := NewState(0)
	first, a, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, source *PropsSource[int]) error {
		life, err := child.Lifetime()
		if err != nil {
			return err
		}
		_, err = life.Effect(func() (func(), error) {
			aRuns++
			if b != nil {
				av, err := source.GetSnapshot()
				if err != nil {
					return nil, err
				}
				bv, err := b.GetSnapshot()
				if err != nil {
					return nil, err
				}
				location, _ := CurrentLocation()
				if av.Revision != bv.Revision || av.Location != bv.Location || location != av.Location {
					return nil, errPropsInvalid
				}
				if aRuns == 2 {
					shared.Set(1)
					shared.Set(2)
				}
			}
			return nil, nil
		}, source)
		return err
	})
	second, sourceB, _ := propsClientRoot(t, `10`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, source *PropsSource[int]) error {
		life, err := child.Lifetime()
		if err != nil {
			return err
		}
		_, err = life.Effect(func() (func(), error) { bRuns++; return nil, nil }, source, shared)
		if err != nil {
			return err
		}
		_, err = life.Effect(func() (func(), error) { unrelated++; return nil, nil })
		return err
	})
	b = sourceB
	started, release := make(chan context.Context, 1), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	if err := first.Go(func(ctx context.Context) { started <- ctx; <-release }); err != nil {
		t.Fatal(err)
	}
	taskContext := <-started
	activity := first.activityEpoch
	location := Location{URL: "https://props.test/a?q=new", Pathname: "/a", Search: "?q=new"}
	for revision := uint64(2); revision <= 3; revision++ {
		preparedA, err := prepareScopeProps(context.Background(), first, json.RawMessage(`2`), location, revision, func() bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		preparedB, err := prepareScopeProps(context.Background(), second, json.RawMessage(`20`), location, revision, func() bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		cohort, err := preparePropsCohort([]*Scope{first, second}, []*preparedProps{preparedA, preparedB}, location, revision, propsGuards{Precommit: func() bool { return true }, Published: func() bool { return true }})
		if err != nil {
			t.Fatal(err)
		}
		if err := cohort.InstallFrame(); err != nil {
			cohort.Close()
			t.Fatal(err)
		}
		setLocation(location.URL)
		if aRuns != int(revision)-1 || bRuns != int(revision)-1 {
			t.Fatal("frame install entered authored effects")
		}
		if failures := cohort.Publish(); len(failures) != 0 {
			cohort.Close()
			t.Fatal(failures)
		}
		if aRuns != int(revision) || bRuns != int(revision) || unrelated != 1 || first.activityEpoch != activity || taskContext.Err() != nil || !first.active() || !second.active() {
			t.Fatal("cross-root props tear, duplicate update, or hidden lifecycle restart", aRuns, bRuns, unrelated)
		}
		if failures := cohort.Publish(); len(failures) != 0 || aRuns != int(revision) {
			t.Fatal("same frame published twice")
		}
		cohort.Close()
		value, err := a.GetSnapshot()
		if err != nil || value.Value != 2 || value.Revision != revision {
			t.Fatal(value, err)
		}
	}
	close(release)
	propsWait(t, func() bool { return first.taskWorkers == 0 })
}
func TestPropsCohortNewRequestIntentCannotAbortPublishedSibling(t *testing.T) {
	propsBrowserEnvironment(t)
	precommit, published := true, true
	aRuns, bRuns := 0, 0
	first, _, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, p *PropsSource[int]) error {
		life, _ := child.Lifetime()
		_, err := life.Effect(func() (func(), error) {
			aRuns++
			if aRuns == 2 {
				precommit = false
			}
			return nil, nil
		}, p)
		return err
	})
	second, _, _ := propsClientRoot(t, `2`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, p *PropsSource[int]) error {
		life, _ := child.Lifetime()
		_, err := life.Effect(func() (func(), error) { bRuns++; return nil, nil }, p)
		return err
	})
	location := Location{URL: "https://props.test/a?q=B"}
	a, err := prepareScopeProps(context.Background(), first, json.RawMessage(`3`), location, 2, func() bool { return precommit })
	if err != nil {
		t.Fatal(err)
	}
	b, err := prepareScopeProps(context.Background(), second, json.RawMessage(`4`), location, 2, func() bool { return precommit })
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := preparePropsCohort([]*Scope{first, second}, []*preparedProps{a, b}, location, 2, propsGuards{Precommit: func() bool { return precommit }, Published: func() bool { return published }})
	if err != nil {
		t.Fatal(err)
	}
	defer cohort.Close()
	if err := cohort.InstallFrame(); err != nil {
		t.Fatal(err)
	}
	if failures := cohort.Publish(); len(failures) != 0 || aRuns != 2 || bRuns != 2 || precommit {
		t.Fatal("newer intent tore useful B", failures, aRuns, bRuns)
	}
}
func TestPropsCohortPrivilegedRevocationStopsLaterEffectsAndReads(t *testing.T) {
	propsBrowserEnvironment(t)
	published := true
	var second *Scope
	bRuns, aRuns := 0, 0
	first, sourceA, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, p *PropsSource[int]) error {
		life, _ := child.Lifetime()
		_, err := life.Effect(func() (func(), error) {
			aRuns++
			if aRuns == 2 {
				published = false
				child.parent.revokeMergeAuthority()
				second.revokeMergeAuthority()
			}
			return nil, nil
		}, p)
		return err
	})
	second, sourceB, _ := propsClientRoot(t, `2`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, p *PropsSource[int]) error {
		life, _ := child.Lifetime()
		_, err := life.Effect(func() (func(), error) { bRuns++; return nil, nil }, p)
		return err
	})
	location := Location{URL: "https://props.test/a?q=B"}
	a, err := prepareScopeProps(context.Background(), first, json.RawMessage(`3`), location, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	b, err := prepareScopeProps(context.Background(), second, json.RawMessage(`4`), location, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := preparePropsCohort([]*Scope{first, second}, []*preparedProps{a, b}, location, 2, propsGuards{Precommit: func() bool { return true }, Published: func() bool { return published }})
	if err != nil {
		t.Fatal(err)
	}
	defer cohort.Close()
	if err := cohort.InstallFrame(); err != nil {
		t.Fatal(err)
	}
	cohort.Publish()
	if bRuns != 1 {
		t.Fatal("revoked B entered effect", bRuns)
	}
	for _, source := range []*PropsSource[int]{sourceA, sourceB} {
		if _, err := source.GetSnapshot(); !errors.Is(err, errPropsClosed) {
			t.Fatal("revoked source exposed data", err)
		}
	}
}
func TestPropsCohortStalePrecommitAndReboundInstanceCannotInstall(t *testing.T) {
	propsBrowserEnvironment(t)
	scope, source, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	location := Location{URL: "https://props.test/a?q=B"}
	prepared, err := prepareScopeProps(context.Background(), scope, json.RawMessage(`2`), location, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	valid := true
	cohort, err := preparePropsCohort([]*Scope{scope}, []*preparedProps{prepared}, location, 2, propsGuards{Precommit: func() bool { return valid }, Published: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer cohort.Close()
	valid = false
	if err := cohort.InstallFrame(); !errors.Is(err, errPropsStale) {
		t.Fatal(err)
	}
	value, err := source.GetSnapshot()
	if err != nil || value.Value != 1 || value.Revision != 1 {
		t.Fatal("rejected commit changed props", value, err)
	}
	cohort.Close()
	scope.mergeID++
	if _, err := preparePropsCohort([]*Scope{scope}, []*preparedProps{prepared}, location, 2, propsGuards{Precommit: func() bool { return true }, Published: func() bool { return true }}); err == nil {
		t.Fatal("same compatibility key inherited old source instance")
	}
}
func TestPropsEmptyColdFrameRetiresOldSourcesAndSilentLocationKeepsToken(t *testing.T) {
	propsBrowserEnvironment(t)
	_, source, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	location := Location{URL: "https://props.test/b", Pathname: "/b"}
	cohort, err := preparePropsCohort(nil, nil, location, 2, propsGuards{Precommit: func() bool { return true }, Published: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer cohort.Close()
	if err := cohort.InstallFrame(); err != nil {
		t.Fatal(err)
	}
	before := currentPropsFrame
	propsLocationChanged(location)
	if currentPropsFrame != before || propsDocumentRevision() != 2 {
		t.Fatal("equal location hook changed installed frame authority")
	}
	if _, err := source.GetSnapshot(); !errors.Is(err, errPropsStale) {
		t.Fatal("cold frame re-stamped old source", err)
	}
	_, fresh, _ := propsClientRoot(t, `9`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	value, err := fresh.GetSnapshot()
	if err != nil || value.Revision != 2 || value.Location != location || value.Value != 9 {
		t.Fatal("fresh Mount did not bind accepted frame", value, err)
	}
}

func TestPropsAbortCloseDrainsCurrentDirtyStateDespiteNewRequestIntent(t *testing.T) {
	propsBrowserEnvironment(t)
	state := NewState(0)
	runs, latest := 0, 0
	scope, source, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, p *PropsSource[int]) error {
		life, _ := child.Lifetime()
		_, err := life.Effect(func() (func(), error) {
			runs++
			latest = state.Get()
			snapshot, err := p.GetSnapshot()
			if err != nil {
				return nil, err
			}
			if snapshot.Revision != 1 {
				return nil, errPropsInvalid
			}
			return nil, nil
		}, state, p)
		return err
	})
	location := Location{URL: "https://props.test/a?q=B"}
	prepared, err := prepareScopeProps(context.Background(), scope, json.RawMessage(`2`), location, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	precommit := true
	cohort, err := preparePropsCohort([]*Scope{scope}, []*preparedProps{prepared}, location, 2, propsGuards{Precommit: func() bool { return precommit }, Published: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	state.Set(9)
	precommit = false
	if runs != 1 {
		t.Fatal("held scheduling token ran dirty effect")
	}
	if failures := cohort.Close(); len(failures) != 0 || runs != 2 || latest != 9 {
		t.Fatal("abort stranded useful current state", failures, runs, latest)
	}
	value, err := source.GetSnapshot()
	if err != nil || value.Value != 1 || value.Revision != 1 {
		t.Fatal("abort published prepared props", value, err)
	}
	if failures := cohort.Close(); len(failures) != 0 || runs != 2 {
		t.Fatal("close repeated callback work")
	}
}
func TestPropsCloseReportsLateDirtyFailureOnceWithoutRepeatingPublishFailure(t *testing.T) {
	propsBrowserEnvironment(t)
	state := NewState(0)
	runs := 0
	scope, _, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, func(child *Scope, p *PropsSource[int]) error {
		life, _ := child.Lifetime()
		_, err := life.Effect(func() (func(), error) {
			runs++
			if state.Get() == 1 {
				return nil, errPropsInvalid
			}
			return nil, nil
		}, state, p)
		return err
	})
	location := Location{URL: "https://props.test/a?q=B"}
	prepared, err := prepareScopeProps(context.Background(), scope, json.RawMessage(`2`), location, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := preparePropsCohort([]*Scope{scope}, []*preparedProps{prepared}, location, 2, propsGuards{Precommit: func() bool { return true }, Published: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	if err := cohort.InstallFrame(); err != nil {
		t.Fatal(err)
	}
	if failures := cohort.Publish(); len(failures) != 0 || runs != 2 {
		t.Fatal(failures, runs)
	}
	state.Set(1)
	failures := cohort.Close()
	if len(failures) != 1 || failures[0].Scope != scope || !errors.Is(failures[0].Err, errPropsInvalid) || runs != 3 {
		t.Fatal("late pending failure not owned", failures, runs)
	}
	if failures := cohort.Close(); len(failures) != 0 || runs != 3 {
		t.Fatal("failure was repeated")
	}
}

func TestPropsPreparedDiscardReleasesOnlyItsOwnedRawAndCannotDiscardAdoptedTicket(t *testing.T) {
	propsBrowserEnvironment(t)
	scope, _, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	location := Location{URL: "https://props.test/a?q=B"}
	ticket, err := prepareScopeProps(context.Background(), scope, json.RawMessage(`222`), location, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if propsTransientBytes() != 3 {
		t.Fatal("prepared raw not charged", propsTransientBytes())
	}
	ticket.Close()
	ticket.Close()
	if propsTransientBytes() != 0 {
		t.Fatal("discard charge repeated or leaked")
	}
	ticket, err = prepareScopeProps(context.Background(), scope, json.RawMessage(`222`), location, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	cohort, err := preparePropsCohort([]*Scope{scope}, []*preparedProps{ticket}, location, 2, propsGuards{Precommit: func() bool { return true }, Published: func() bool { return true }})
	if err != nil {
		ticket.Close()
		t.Fatal(err)
	}
	ticket.Close()
	if ticket.closed || propsTransientBytes() != 3 {
		t.Fatal("caller discarded cohort-owned source")
	}
	cohort.Close()
	ticket.Close()
	if propsTransientBytes() != 0 || !ticket.closed {
		t.Fatal("adopted raw was not released")
	}
}
func TestPropsAcceptedWeightChangesAndPriorFrameChargeReleaseExactly(t *testing.T) {
	propsBrowserEnvironment(t)
	scope, source, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	if propsRegisteredBytes != 1 {
		t.Fatal(propsRegisteredBytes)
	}
	for index, raw := range []string{`22222`, `3`} {
		revision := uint64(index + 2)
		location := Location{URL: "https://props.test/a?q=B"}
		ticket, err := prepareScopeProps(context.Background(), scope, json.RawMessage(raw), location, revision, func() bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		cohort, err := preparePropsCohort([]*Scope{scope}, []*preparedProps{ticket}, location, revision, propsGuards{Precommit: func() bool { return true }, Published: func() bool { return true }})
		if err != nil {
			ticket.Close()
			t.Fatal(err)
		}
		if err := cohort.InstallFrame(); err != nil {
			cohort.Close()
			t.Fatal(err)
		}
		if propsRegisteredBytes != len(raw) || source.propsWeight() != len(raw) || source.initial.value != nil || source.initial.raw != nil {
			t.Fatal("source retained old weight/DTO", propsRegisteredBytes, source.propsWeight())
		}
		if index == 1 && propsTransientBytes() != 5 {
			t.Fatal("previous published raw lost its held-frame charge", propsTransientBytes())
		}
		cohort.Publish()
		cohort.Close()
		if propsTransientBytes() != 0 {
			t.Fatal("previous frame retained past close")
		}
	}
	scope.dispose()
	if propsRegisteredBytes != 0 {
		t.Fatal("close subtracted initial instead of current weight", propsRegisteredBytes)
	}
}
func TestPropsRetiringSourcesCloseOnlyAtInstallAndFreeColdRegistrationBudget(t *testing.T) {
	propsBrowserEnvironment(t)
	old, source, _ := propsClientRoot(t, `12345`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	location := Location{URL: "https://props.test/b"}
	precommit := true
	cohort, err := preparePropsCohort(nil, nil, location, 2, propsGuards{Precommit: func() bool { return precommit }, Published: func() bool { return true }, Retiring: []*Scope{old}})
	if err != nil {
		t.Fatal(err)
	}
	if propsRegisteredBytes != 5 || !source.propsValid() {
		t.Fatal("prepare retired useful prior source")
	}
	precommit = false
	cohort.Close()
	if propsRegisteredBytes != 5 || !source.propsValid() {
		t.Fatal("abort retired current source")
	}
	precommit = true
	cohort, err = preparePropsCohort(nil, nil, location, 2, propsGuards{Precommit: func() bool { return precommit }, Published: func() bool { return true }, Retiring: []*Scope{old}})
	if err != nil {
		t.Fatal(err)
	}
	old.revokeMergeAuthority()
	if err := cohort.InstallFrame(); err != nil {
		cohort.Close()
		t.Fatal(err)
	}
	if propsRegisteredBytes != 0 || !source.closed || old.closed {
		t.Fatal("captured source retirement was not callback-free and exact", propsRegisteredBytes, source.closed, old.closed)
	}
	cohort.Publish()
	cohort.Close()
	_, fresh, _ := propsClientRoot(t, `7`, PropsOptions[int]{Clone: func(v int) int { return v }}, nil)
	if value, err := fresh.GetSnapshot(); err != nil || value.Revision != 2 || propsRegisteredBytes != 1 {
		t.Fatal(value, err, propsRegisteredBytes)
	}
	old.dispose()
	if propsRegisteredBytes != 1 {
		t.Fatal("old terminal cleanup subtracted newer source budget")
	}
}

func TestPropsOwnedLinkObserversSuperviseGoexitIndividually(t *testing.T) {
	propsBrowserEnvironment(t)
	fixture := linkStatusEnvironment(t)
	if err := fixture.scope.enableMergeCallbacks(); err != nil {
		t.Fatal(err)
	}
	failed, healthy := 0, 0
	if _, err := fixture.scope.ObserveLinkStatus(fixture.links[0], func(value LinkStatus) {
		if value.Pending {
			failed++
			runtime.Goexit()
		}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.scope.ObserveLinkStatus(fixture.links[0], func(value LinkStatus) {
		if value.Pending {
			healthy++
		}
	}); err != nil {
		t.Fatal(err)
	}
	beginLinkNavigation(90, fixture.links[0].Value)
	waitLinkNavigation(90)
	if failed != 1 || healthy != 1 || propsWorkerCount() != 0 {
		t.Fatal("one owned link observer aborted later observers", failed, healthy, propsWorkerCount())
	}
	finishLinkNavigation(90)
}

func TestPropsNewChildUnlinkDoesNotLosePublicationFailureRoot(t *testing.T) {
	propsBrowserEnvironment(t)
	runs := 0
	scope, _, _ := propsClientRoot(t, `1`, PropsOptions[int]{Clone: func(value int) int { return value }}, func(child *Scope, source *PropsSource[int]) error {
		life, err := child.Lifetime()
		if err != nil {
			return err
		}
		_, err = life.Effect(func() (func(), error) {
			runs++
			if runs == 1 {
				return nil, nil
			}
			created, err := life.Child()
			if err != nil {
				return nil, err
			}
			_, _ = created.Effect(func() (func(), error) { created.Close(); return nil, errPropsInvalid })
			return nil, nil
		}, source)
		return err
	})
	location := Location{URL: "https://props.test/a?q=B"}
	ticket, err := prepareScopeProps(context.Background(), scope, json.RawMessage(`2`), location, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	defer ticket.Close()
	cohort, err := preparePropsCohort([]*Scope{scope}, []*preparedProps{ticket}, location, 2, propsGuards{Precommit: func() bool { return true }, Published: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	defer cohort.Close()
	if err := cohort.InstallFrame(); err != nil {
		t.Fatal(err)
	}
	failures := cohort.Publish()
	if len(failures) != 1 || failures[0].Scope != scope || !errors.Is(failures[0].Err, errPropsInvalid) {
		t.Fatal("new child unlink lost exact root recovery attribution", failures)
	}
}
