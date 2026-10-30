package client

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"weak"
)

type propsTestOwner struct {
	id    uint64
	alive atomic.Bool
}

func (o *propsTestOwner) propsLive() bool       { return o.alive.Load() }
func (o *propsTestOwner) propsInstance() uint64 { return o.id }
func propsWait(t *testing.T, condition func() bool) {
	t.Helper()
	end := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(end) {
			t.Fatal("props condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func propsWorkerCount() int { propsWorkerMu.Lock(); defer propsWorkerMu.Unlock(); return propsWorkers }
func propsTestEnvironment(t *testing.T) {
	t.Helper()
	if propsWorkerCount() != 0 || propsWorkerBytes() != 0 {
		t.Fatal("prior props callback still physically charged")
	}
	previousFrame, previousBindings, previousID := currentPropsFrame, propsBindings, propsNextID
	currentPropsFrame = &propsFrame{revision: 1, location: Location{URL: "https://props.test/a?q=old"}, entries: map[uint64]propsDatum{}}
	propsBindings, propsNextID = map[uint64]propsBinding{}, 0
	t.Cleanup(func() {
		propsWait(t, func() bool { return propsWorkerCount() == 0 && propsWorkerBytes() == 0 })
		for _, binding := range propsBindings {
			binding.propsClose()
		}
		currentPropsFrame, propsBindings, propsNextID = previousFrame, previousBindings, previousID
	})
}
func portableProps[T any](t *testing.T, raw string, options PropsOptions[T]) (*PropsSource[T], *propsTestOwner) {
	t.Helper()
	owner := &propsTestOwner{id: 7}
	owner.alive.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	propsNextID++
	p := &PropsSource[T]{owner: owner, instance: owner.id, id: propsNextID, options: options, ctx: ctx, cancel: cancel, dependencies: map[uint64]func(){}}
	value, err := p.propsPrepare(ctx, json.RawMessage(raw), currentPropsFrame.location, 1, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	p.initial, p.initialRevision = value, 1
	propsBindings[p.id] = p
	t.Cleanup(p.propsClose)
	return p, owner
}

func TestPropsCallbacksPanicGoexitAndErrorsSettlePhysicalCharges(t *testing.T) {
	propsTestEnvironment(t)
	for _, mode := range []string{"panic", "goexit", "error", "value"} {
		value, err := propsInvoke(context.Background(), func() bool { return true }, func() (int, error) {
			switch mode {
			case "panic":
				panic("private")
			case "goexit":
				runtime.Goexit()
			case "error":
				return 0, errPropsInvalid
			}
			return 9, nil
		})
		if mode == "value" {
			if err != nil || value != 9 {
				t.Fatal(mode, value, err)
			}
		} else if err == nil {
			t.Fatal("aborted callback looked successful", mode)
		}
		if propsWorkerCount() != 0 {
			t.Fatal("settled callback retained a physical charge", mode)
		}
	}
}
func TestPropsCancellationCannotRetirePhysicalCallbackCapacity(t *testing.T) {
	propsTestEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	started := make(chan struct{}, propsWorkerLimit)
	results := make(chan error, propsWorkerLimit)
	for i := 0; i < propsWorkerLimit; i++ {
		go func() {
			_, err := propsInvoke(ctx, func() bool { return true }, func() (int, error) { started <- struct{}{}; <-release; return 1, nil })
			results <- err
		}()
	}
	for i := 0; i < propsWorkerLimit; i++ {
		<-started
	}
	if _, err := propsInvoke(context.Background(), func() bool { return true }, func() (int, error) { t.Error("overflow work entered"); return 1, nil }); !errors.Is(err, errPropsCapacity) {
		t.Fatal(err)
	}
	cancel()
	for i := 0; i < propsWorkerLimit; i++ {
		if err := <-results; !errors.Is(err, errPropsStale) {
			t.Fatal(err)
		}
	}
	if propsWorkerCount() != propsWorkerLimit {
		t.Fatal("logical cancellation released unreturned workers", propsWorkerCount())
	}
	close(release)
	propsWait(t, func() bool { return propsWorkerCount() == 0 })
}
func TestPropsOwnValidatorStoredRawAndReturnedValues(t *testing.T) {
	propsTestEnvironment(t)
	type dto struct{ Values map[string]string }
	clone := func(v dto) dto {
		out := dto{Values: map[string]string{}}
		for k, value := range v.Values {
			out.Values[k] = value
		}
		return out
	}
	p, _ := portableProps(t, `{"Values":{"price":"10"}}`, PropsOptions[dto]{Clone: clone, Validate: func(v PropsSnapshot[dto]) error { v.Value.Values["price"] = "validator"; return nil }})
	first, err := p.GetSnapshot()
	if err != nil || first.Value.Values["price"] != "10" {
		t.Fatal(first, err)
	}
	first.Value.Values["price"] = "reader"
	second, err := p.GetSnapshot()
	if err != nil || second.Value.Values["price"] != "10" {
		t.Fatal("reader alias escaped", second, err)
	}
	raw := json.RawMessage(`{"Values":{"price":"11"}}`)
	next, err := p.propsPrepare(context.Background(), raw, Location{URL: "https://props.test/a?q=new"}, 2, func() bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	if string(next.raw) != `{"Values":{"price":"11"}}` {
		t.Fatal("prepared raw aliases caller")
	}
	still, _ := p.GetSnapshot()
	if still.Revision != 1 || still.Value.Values["price"] != "10" {
		t.Fatal("prepare published props", still)
	}
}
func TestPropsPreparedCallbacksSeeIncomingLocationWhileCommittedFrameStaysOld(t *testing.T) {
	propsTestEnvironment(t)
	p, _ := portableProps(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }})
	old := currentPropsFrame
	p.options.Validate = func(v PropsSnapshot[int]) error {
		if v.Location.URL != "https://props.test/a?q=new" || v.Revision != 2 || currentPropsFrame != old {
			return errPropsInvalid
		}
		return nil
	}
	if _, err := p.propsPrepare(context.Background(), json.RawMessage(`2`), Location{URL: "https://props.test/a?q=new"}, 2, func() bool { return true }); err != nil {
		t.Fatal(err)
	}
	if currentPropsFrame != old {
		t.Fatal("validator published frame")
	}
}
func TestPropsReadRejectsFrameReplacementDuringOwnedClone(t *testing.T) {
	propsTestEnvironment(t)
	p, _ := portableProps(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }})
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	p.options.Clone = func(v int) int { close(started); <-release; return v }
	result := make(chan error, 1)
	go func() { _, err := p.GetSnapshot(); result <- err }()
	<-started
	currentPropsFrame = &propsFrame{revision: 2, entries: map[uint64]propsDatum{}}
	close(release)
	if err := <-result; !errors.Is(err, errPropsStale) {
		t.Fatal("old clone returned after frame changed", err)
	}
}
func TestPropsRevocationRejectsReadsWhileOldCloneRemainsCharged(t *testing.T) {
	propsTestEnvironment(t)
	p, owner := portableProps(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }})
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	p.options.Clone = func(v int) int { close(started); <-release; return v }
	result := make(chan error, 1)
	go func() { _, err := p.GetSnapshot(); result <- err }()
	<-started
	owner.alive.Store(false)
	p.propsRevoke()
	if err := <-result; !errors.Is(err, errPropsStale) {
		t.Fatal(err)
	}
	if _, err := p.GetSnapshot(); !errors.Is(err, errPropsClosed) || propsWorkerCount() != 1 {
		t.Fatal("revocation retained reads or erased physical charge", err, propsWorkerCount())
	}
	close(release)
	propsWait(t, func() bool { return propsWorkerCount() == 0 })
}
func TestPropsSilentLocationPreservesRevisionWithoutDependencyPublication(t *testing.T) {
	propsTestEnvironment(t)
	p, _ := portableProps(t, `1`, PropsOptions[int]{Clone: func(v int) int { return v }})
	calls := 0
	stop, err := p.subscribeEffect(func() { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	propsLocationChanged(Location{URL: "https://props.test/a?q=old#anchor"})
	value, err := p.GetSnapshot()
	if err != nil || value.Revision != 1 || value.Location.URL != "https://props.test/a?q=old#anchor" || calls != 0 {
		t.Fatal(value, err, calls)
	}
}
func TestPropsBatchCoalescesExistingIndependentRootsAndKeepsUnrelatedEffects(t *testing.T) {
	first, second := NewLifetime(), NewLifetime()
	defer first.Close()
	defer second.Close()
	shared := NewState(0)
	aRuns, bRuns, unrelated, latest := 0, 0, 0, 0
	a, _ := first.Effect(func() (func(), error) {
		aRuns++
		if aRuns == 2 {
			shared.Set(1)
			shared.Set(2)
		}
		return nil, nil
	})
	b, _ := second.Effect(func() (func(), error) { bRuns++; latest = shared.Get(); return nil, nil }, shared)
	_, _ = second.Effect(func() (func(), error) { unrelated++; return nil, nil })
	batch := &effectPublicationBatch{token: &lifetimeResumeBarrier{}, valid: func() bool { return true }}
	defer batch.close()
	if err := batch.enroll(a); err != nil {
		t.Fatal(err)
	}
	if err := batch.enroll(b); err != nil {
		t.Fatal(err)
	}
	_ = a.Invalidate()
	_ = b.Invalidate()
	if aRuns != 1 || bRuns != 1 {
		t.Fatal("queue ran authored code")
	}
	if failures := batch.flush(); len(failures) != 0 || aRuns != 2 || bRuns != 2 || latest != 2 || unrelated != 1 {
		t.Fatal("cross-root queue tore or restarted unrelated effects", failures, aRuns, bRuns, latest, unrelated)
	}
	batch.close()
	shared.Set(3)
	if bRuns != 3 || latest != 3 {
		t.Fatal("default immediate State semantics changed")
	}
}
func TestPropsBatchNewEffectsKeepInitialSetupAndRevocationSkipsOtherCallbacks(t *testing.T) {
	life := NewLifetime()
	defer life.Close()
	runs, newRuns := 0, 0
	effect, _ := life.Effect(func() (func(), error) {
		runs++
		if runs == 2 {
			_, err := life.Effect(func() (func(), error) { newRuns++; return nil, nil })
			if err != nil {
				return nil, err
			}
			if newRuns != 1 {
				return nil, errPropsInvalid
			}
		}
		return nil, nil
	})
	batch := &effectPublicationBatch{token: &lifetimeResumeBarrier{}, valid: func() bool { return true }}
	defer batch.close()
	if err := batch.enroll(effect); err != nil {
		t.Fatal(err)
	}
	_ = effect.Invalidate()
	if failures := batch.flush(); len(failures) != 0 || newRuns != 1 {
		t.Fatal(failures, newRuns)
	}
	life.revoked = true
	if err := effect.Invalidate(); !errors.Is(err, ErrEffectClosed) || runs != 2 || effect.closed {
		t.Fatal("logical revocation ran cleanup/setup before terminal disposal", err, runs, effect.closed)
	}
}

func TestPropsWorkerSerializedBytesRemainChargedUntilPhysicalSettlement(t *testing.T) {
	propsTestEnvironment(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	result := make(chan error, 1)
	go func() {
		_, err := propsInvokeWeighted(ctx, func() bool { return true }, propsWorkerByteLimit, func() (int, error) { close(started); <-release; return 1, nil })
		result <- err
	}()
	<-started
	if propsWorkerBytes() != propsWorkerByteLimit {
		t.Fatal("serialized worker weight missing", propsWorkerBytes())
	}
	if _, err := propsInvokeWeighted(context.Background(), func() bool { return true }, 1, func() (int, error) { t.Error("overflow entered"); return 1, nil }); !errors.Is(err, errPropsCapacity) {
		t.Fatal(err)
	}
	cancel()
	if err := <-result; !errors.Is(err, errPropsStale) || propsWorkerBytes() != propsWorkerByteLimit {
		t.Fatal("logical cancel erased worker bytes", err, propsWorkerBytes())
	}
	close(release)
	propsWait(t, func() bool { return propsWorkerCount() == 0 && propsWorkerBytes() == 0 })
}

func TestPropsPublicationCycleIsBoundedAndDoesNotSkipHealthySibling(t *testing.T) {
	first, second := NewLifetime(), NewLifetime()
	defer first.Close()
	defer second.Close()
	x, y := NewState(0), NewState(0)
	armed, healthy := false, 0
	a, _ := first.Effect(func() (func(), error) {
		if armed {
			y.Set(y.Get() + 1)
		}
		return nil, nil
	}, x)
	b, _ := second.Effect(func() (func(), error) {
		if armed {
			x.Set(x.Get() + 1)
		}
		return nil, nil
	}, y)
	h, _ := second.Effect(func() (func(), error) { healthy++; return nil, nil })
	batch := &effectPublicationBatch{token: &lifetimeResumeBarrier{}, valid: func() bool { return true }}
	defer batch.close()
	for _, effect := range []*Effect{a, b, h} {
		if err := batch.enroll(effect); err != nil {
			t.Fatal(err)
		}
		_ = effect.Invalidate()
	}
	armed = true
	failures := batch.flush()
	if len(failures) != 1 || !a.closed || healthy != 2 || x.Get() != 64 || y.Get() != 64 {
		t.Fatal("cycle escaped bounded cohort or skipped useful sibling", len(failures), a.closed, healthy, x.Get(), y.Get())
	}
}
func TestPropsPublicationBoundsNewChildrenAndPermanentRegistrations(t *testing.T) {
	life := NewLifetime()
	defer life.Close()
	effect, _ := life.Effect(func() (func(), error) { return nil, nil })
	batch := &effectPublicationBatch{token: &lifetimeResumeBarrier{}, valid: func() bool { return true }}
	defer batch.close()
	if err := batch.enroll(effect); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 256; i++ {
		if _, err := life.Child(); err != nil {
			t.Fatal(i, err)
		}
	}
	if _, err := life.Child(); !errors.Is(err, errPropsCapacity) {
		t.Fatal("cohort child cap bypassed", err)
	}
	for _, owner := range batch.owners[:2] {
		for i := 0; i < 256; i++ {
			if _, err := owner.Defer(func() {}); err != nil {
				t.Fatal(i, err)
			}
		}
	}
	if _, err := batch.owners[2].Defer(func() {}); !errors.Is(err, errPropsCapacity) {
		t.Fatal("aggregate permanent registration cap bypassed", err)
	}
	resource := batch.owners[0].resources[0]
	resource.Close()
	if _, err := batch.owners[2].Defer(func() {}); err != nil {
		t.Fatal("actual release did not restore owned resource capacity", err)
	}
}

func TestPropsNilInterfaceDTOIsAnOwnedTypedValueRatherThanMissingFrameEntry(t *testing.T) {
	propsTestEnvironment(t)
	source, _ := portableProps(t, `null`, PropsOptions[any]{Clone: func(value any) any { return value }})
	value, err := source.GetSnapshot()
	if err != nil || value.Value != nil || value.Revision != 1 {
		t.Fatal(value, err)
	}
}

func propsTestCallbackGuard(fn func()) error {
	_, err := propsInvoke(context.Background(), func() bool { return true }, func() (struct{}, error) { fn(); return struct{}{}, nil })
	return err
}
func TestPropsPublicationGoexitReturnsOwnedFailureAndContinuesSibling(t *testing.T) {
	propsTestEnvironment(t)
	first, second := NewLifetime(), NewLifetime()
	defer first.Close()
	defer second.Close()
	first.callbackGuard, second.callbackGuard = propsTestCallbackGuard, propsTestCallbackGuard
	aRuns, bRuns := 0, 0
	a, _ := first.Effect(func() (func(), error) {
		aRuns++
		if aRuns == 2 {
			runtime.Goexit()
		}
		return nil, nil
	})
	b, _ := second.Effect(func() (func(), error) { bRuns++; return nil, nil })
	batch := &effectPublicationBatch{token: &lifetimeResumeBarrier{}, valid: func() bool { return true }}
	defer batch.close()
	for _, effect := range []*Effect{a, b} {
		if err := batch.enroll(effect); err != nil {
			t.Fatal(err)
		}
		_ = effect.Invalidate()
	}
	failures := batch.flush()
	if len(failures) != 1 || failures[0].owner != first || !errors.Is(failures[0].err, errPropsCallback) || !a.closed || aRuns != 2 || bRuns != 2 {
		t.Fatal("aborted owned effect did not return failure or stranded sibling", len(failures), a.closed, aRuns, bRuns)
	}
}
func TestPropsOwnedLifetimeSupervisesEachCleanupAcrossGoexit(t *testing.T) {
	propsTestEnvironment(t)
	life := NewLifetime(LifetimeOptions{OnError: func(error) {}})
	life.callbackGuard = propsTestCallbackGuard
	cleaned := []int{}
	for i := 0; i < 3; i++ {
		index := i
		if _, err := life.Defer(func() {
			cleaned = append(cleaned, index)
			if index == 1 {
				runtime.Goexit()
			}
		}); err != nil {
			t.Fatal(err)
		}
	}
	life.Close()
	if len(cleaned) != 3 || cleaned[0] != 2 || cleaned[1] != 1 || cleaned[2] != 0 || len(life.resources) != 0 || propsWorkerCount() != 0 {
		t.Fatal("one resource Goexit skipped remaining owned cleanup", cleaned, len(life.resources), propsWorkerCount())
	}
}

func TestPropsOwnedEffectCleanupGoexitClosesOnlyFailedEffectAndContinuesSiblings(t *testing.T) {
	propsTestEnvironment(t)
	life := NewLifetime(LifetimeOptions{OnError: func(error) {}})
	life.callbackGuard = propsTestCallbackGuard
	defer life.Close()
	firstRuns, secondRuns, cleanups := 0, 0, 0
	first, _ := life.Effect(func() (func(), error) { firstRuns++; return func() { cleanups++; runtime.Goexit() }, nil })
	second, _ := life.Effect(func() (func(), error) { secondRuns++; return nil, nil })
	batch := &effectPublicationBatch{token: &lifetimeResumeBarrier{}, valid: func() bool { return true }}
	defer batch.close()
	for _, effect := range []*Effect{first, second} {
		if err := batch.enroll(effect); err != nil {
			t.Fatal(err)
		}
		_ = effect.Invalidate()
	}
	failures := batch.flush()
	if len(failures) != 1 || !errors.Is(failures[0].err, errPropsCallback) || !first.closed || firstRuns != 1 || secondRuns != 2 || cleanups != 1 || propsWorkerCount() != 0 {
		t.Fatal("cleanup Goexit aborted publication or reran failed setup", len(failures), firstRuns, secondRuns, cleanups)
	}
}

func TestPropsCallbackRechecksAuthorityAtWorkerDispatch(t *testing.T) {
	propsTestEnvironment(t)
	previous := propsPhysicalAdmission
	t.Cleanup(func() { propsPhysicalAdmission = previous })
	admitted, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	propsPhysicalAdmission = func() (func(), error) { close(admitted); <-release; return nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls atomic.Int32
	result := make(chan error, 1)
	go func() {
		_, err := propsInvoke(ctx, func() bool { return true }, func() (int, error) { calls.Add(1); return 1, nil })
		result <- err
	}()
	<-admitted
	cancel()
	close(release)
	if err := <-result; !errors.Is(err, errPropsStale) {
		t.Fatal(err)
	}
	propsWait(t, func() bool { return propsWorkerCount() == 0 })
	if calls.Load() != 0 {
		t.Fatal("revoked callback started after dispatch", calls.Load())
	}
}
func TestPropsOwnedEffectRechecksVisibilityBeforeAuthoredSetupDispatch(t *testing.T) {
	propsTestEnvironment(t)
	life := NewLifetime(LifetimeOptions{OnError: func(error) {}})
	defer life.Close()
	runs, cleanups := 0, 0
	effect, err := life.Effect(func() (func(), error) { runs++; return func() { cleanups++ }, nil })
	if err != nil {
		t.Fatal(err)
	}
	life.callbackGuard = func(fn func()) error { life.revoked = true; return propsTestCallbackGuard(fn) }
	if err := effect.Invalidate(); err != nil {
		t.Fatal(err)
	}
	if runs != 1 || cleanups != 1 || effect.closed || !effect.pending || propsWorkerCount() != 0 {
		t.Fatal("revoked owner dispatched new setup or prematurely closed definition", runs, cleanups, effect.closed)
	}
	life.Close()
	if !effect.closed {
		t.Fatal("terminal owner cleanup did not release revoked definition")
	}
}

func propsWeakSiblingFrame(source *PropsSource[int]) weak.Pointer[propsFrame] {
	frame := &propsFrame{identity: &propsFrameIdentity{}, revision: 1, location: currentPropsFrame.location, entries: map[uint64]propsDatum{source.id: source.initial}}
	for i := 0; i < 15; i++ {
		frame.entries[uint64(i)+source.id+1] = propsDatum{raw: make([]byte, propsBytesLimit)}
	}
	currentPropsFrame = frame
	return weak.Make(frame)
}
func TestPropsCanceledTinyCloneDoesNotRetainWholeSiblingFrame(t *testing.T) {
	propsTestEnvironment(t)
	source, _ := portableProps(t, `1`, PropsOptions[int]{Clone: func(value int) int { return value }})
	old := propsWeakSiblingFrame(source)
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	source.options.Clone = func(value int) int { close(started); <-release; return value }
	result := make(chan error, 1)
	go func() { _, err := source.GetSnapshot(); result <- err }()
	<-started
	source.propsRevoke()
	if err := <-result; !errors.Is(err, errPropsStale) {
		t.Fatal(err)
	}
	currentPropsFrame = &propsFrame{identity: &propsFrameIdentity{}, revision: 2, entries: map[uint64]propsDatum{}}
	propsWait(t, func() bool { runtime.GC(); return old.Value() == nil })
	if propsWorkerCount() != 1 || propsWorkerBytes() != 1 {
		t.Fatal("old frame test lost actual tiny-worker charge", propsWorkerCount(), propsWorkerBytes())
	}
	close(release)
	propsWait(t, func() bool { return propsWorkerCount() == 0 && propsWorkerBytes() == 0 })
}
