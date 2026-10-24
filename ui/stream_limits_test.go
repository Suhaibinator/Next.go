package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func testStreamLimiter(t *testing.T, streams, loaders int) *StreamLimiter {
	t.Helper()
	l, err := NewStreamLimiter(streams, loaders)
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func testAdmitStream(t *testing.T, l *StreamLimiter, ctx context.Context) (context.Context, func()) {
	t.Helper()
	work, release, err := l.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return work, release
}
func TestStreamLimiterAdmissionFailsFastAndReleaseIsIdempotent(t *testing.T) {
	for _, p := range [][2]int{{0, 1}, {1, 0}, {-1, 1}, {1025, 1}, {1, 4097}} {
		if _, err := NewStreamLimiter(p[0], p[1]); err == nil {
			t.Fatal("invalid limits accepted", p)
		}
	}
	l := testStreamLimiter(t, 1, 1)
	work, release := testAdmitStream(t, l, context.Background())
	if next, free, err := l.Begin(context.Background()); !errors.Is(err, ErrStreamCapacity) || next != nil || free != nil {
		t.Fatal(next, free != nil, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := l.Begin(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release()
	release()
	if !errors.Is(work.Err(), context.Canceled) {
		t.Fatal("release did not end admitted scope", work.Err())
	}
	_, again := testAdmitStream(t, l, context.Background())
	again()
	var zero StreamLimiter
	if _, _, err := zero.Begin(context.Background()); err == nil {
		t.Fatal("unconstructed limiter admitted a render")
	}
	if _, _, err := l.Begin(nil); err == nil {
		t.Fatal("nil context accepted")
	}
}

func TestStreamLimiterCanceledRenderDoesNotReleaseRunningCallback(t *testing.T) {
	l := testStreamLimiter(t, 1, 1)
	entered, exit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	var running, peak, calls atomic.Int32
	callback := func(block bool) func(context.Context) (Node, error) {
		return func(context.Context) (Node, error) {
			calls.Add(1)
			active := running.Add(1)
			defer running.Add(-1)
			for {
				old := peak.Load()
				if active <= old || peak.CompareAndSwap(old, active) {
					break
				}
			}
			if block {
				close(entered)
				<-exit
			}
			return Text("loaded"), nil
		}
	}
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	work, release := testAdmitStream(t, l, request)
	defer release()
	done := make(chan error, 1)
	go func() {
		done <- RenderEvents(work, Defer("first", Text("waiting"), callback(true)), func(StreamEvent) error { return nil })
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first callback did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("render did not honor cancellation")
	}
	release() // The HTTP request ended; its callback still owns the physical slot.
	second, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stop()
	next, free := testAdmitStream(t, l, second)
	_, err := HTMLContext(next, Defer("second", nil, callback(false)))
	free()
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 || running.Load() != 1 {
		t.Fatal("cancellation freed a live physical callback slot", err, calls.Load(), running.Load())
	}
	finish()
	third, end := context.WithTimeout(context.Background(), 2*time.Second)
	defer end()
	last, lastFree := testAdmitStream(t, l, third)
	defer lastFree()
	html, err := HTMLContext(last, Defer("third", nil, callback(false)))
	if err != nil || html != "loaded" || calls.Load() != 2 || peak.Load() != 1 {
		t.Fatal("physical slot did not recover after return", html, err, calls.Load(), peak.Load())
	}
}

func TestStreamLimiterRejectsReentrantCallbacksButSchedulesReturnedChildren(t *testing.T) {
	l := testStreamLimiter(t, 1, 1)
	work, release := testAdmitStream(t, l, context.Background())
	defer release()
	var innerCalls atomic.Int32
	child := Defer("inner", Text("inner pending"), func(context.Context) (Node, error) { innerCalls.Add(1); return Text("inner loaded"), nil })
	_, err := HTMLContext(work, Defer("outer", nil, func(ctx context.Context) (Node, error) { _, err := HTMLContext(ctx, child); return nil, err }))
	if !errors.Is(err, ErrStreamReentrant) || innerCalls.Load() != 0 {
		t.Fatal("reentrant callback was invoked", err, innerCalls.Load())
	}
	var events []StreamEvent
	outer := Defer("parent", Text("parent pending"), func(context.Context) (Node, error) { return child, nil })
	if err := RenderEvents(work, outer, func(e StreamEvent) error { events = append(events, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[1].ID != "parent" || events[2].ID != "inner" || string(events[2].HTML) != "inner loaded" || innerCalls.Load() != 1 {
		t.Fatal("returned child could not use released parent slot", events, innerCalls.Load())
	}
}

func TestStreamLimiterPanicReleasesSlotAndOldCallbackContextIsNotReentrant(t *testing.T) {
	l := testStreamLimiter(t, 1, 1)
	work, release := testAdmitStream(t, l, context.Background())
	defer release()
	var completed context.Context
	_, err := HTMLContext(work, Defer("panic", nil, func(ctx context.Context) (Node, error) { completed = ctx; panic("controlled panic") }))
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatal(err)
	}
	html, err := HTMLContext(completed, Defer("later", nil, func(context.Context) (Node, error) { return Text("recovered"), nil }))
	if err != nil || html != "recovered" {
		t.Fatal("returned callback retained a phantom reentrant slot", html, err)
	}
}

func TestStreamLimiterDifferentLimitersCannotReenterAnActiveAncestor(t *testing.T) {
	a, b := testStreamLimiter(t, 2, 1), testStreamLimiter(t, 1, 1)
	work, release := testAdmitStream(t, a, context.Background())
	defer release()
	_, err := HTMLContext(work, Defer("a", nil, func(ctx context.Context) (Node, error) {
		other, free, err := b.Begin(ctx)
		if err != nil {
			return nil, err
		}
		defer free()
		_, err = HTMLContext(other, Defer("b", nil, func(child context.Context) (Node, error) {
			ancestor, end, err := a.Begin(child)
			if err != nil {
				return nil, err
			}
			defer end()
			_, err = HTMLContext(ancestor, Defer("again", nil, func(context.Context) (Node, error) { t.Error("reentered active ancestor"); return nil, nil }))
			return nil, err
		}))
		return nil, err
	}))
	if !errors.Is(err, ErrStreamReentrant) {
		t.Fatal(err)
	}
}

func TestStreamLimiterSharesPhysicalCapacityAcrossConcurrentStreams(t *testing.T) {
	l := testStreamLimiter(t, 2, 2)
	entered, exit := make(chan struct{}, 8), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	var running, peak, calls atomic.Int32
	callback := func(context.Context) (Node, error) {
		calls.Add(1)
		active := running.Add(1)
		defer running.Add(-1)
		for {
			old := peak.Load()
			if active <= old || peak.CompareAndSwap(old, active) {
				break
			}
		}
		entered <- struct{}{}
		<-exit
		return Text("loaded"), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		work, release := testAdmitStream(t, l, ctx)
		node := Fragment(Defer("one", nil, callback), Defer("two", nil, callback), Defer("three", nil, callback), Defer("four", nil, callback))
		go func() { defer release(); done <- RenderEvents(work, node, func(StreamEvent) error { return nil }) }()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("global loader capacity did not run", ctx.Err())
		}
	}
	if _, _, err := l.Begin(ctx); !errors.Is(err, ErrStreamCapacity) {
		t.Fatal("third render queued or admitted", err)
	}
	if running.Load() != 2 {
		t.Fatal("global loader limit was not shared", running.Load())
	}
	finish()
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if peak.Load() != 2 || calls.Load() != 8 {
		t.Fatal("per-stream workers bypassed physical admission", peak.Load(), calls.Load())
	}
}
