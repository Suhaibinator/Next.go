package postresponse

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func closeScheduler(t *testing.T, s *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestOptions(t *testing.T) {
	for _, options := range []Options{{Concurrency: -1}, {Concurrency: 65}, {MaxPending: 3}, {MaxPending: 4097}, {Timeout: -1}, {Timeout: 6 * time.Minute}} {
		if _, err := New(options, nil); err == nil {
			t.Fatalf("accepted %+v", options)
		}
	}
}
func TestRequestLimitAndCollectorRejection(t *testing.T) {
	s, _ := New(Options{}, nil)
	ctx, collector := Collect(context.Background())
	ctx, scope := s.Request(ctx)
	var calls atomic.Int32
	var cleaned atomic.Int32
	for i := 0; i < 16; i++ {
		if err := RegisterFinally(ctx, func(context.Context) error { calls.Add(1); return nil }, func() { cleaned.Add(1) }); err != nil {
			t.Fatal(err)
		}
	}
	if !errors.Is(Register(ctx, func(context.Context) error { return nil }), ErrCapacity) {
		t.Fatal("request limit")
	}
	scope.Finish()
	scope.Finish()
	if calls.Load() != 0 {
		t.Fatal("work ran before platform completion")
	}
	collector.Finish(context.Background(), false)
	collector.Finish(context.Background(), true)
	closeScheduler(t, s)
	if calls.Load() != 0 || cleaned.Load() != 16 {
		t.Fatal("rejected response ran callbacks")
	}
}
func TestAlreadyFinishedCollectorDiscardsLateHandler(t *testing.T) {
	s, _ := New(Options{}, nil)
	ctx, collector := Collect(context.Background())
	ctx, scope := s.Request(ctx)
	var calls atomic.Int32
	if err := Register(ctx, func(context.Context) error { calls.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	collector.Finish(context.Background(), false)
	scope.Finish()
	closeScheduler(t, s)
	if calls.Load() != 0 {
		t.Fatal("late completion bypassed rejection")
	}
}
func TestWorkerBudgetIncludesQueueTime(t *testing.T) {
	s, _ := New(Options{Concurrency: 1, MaxPending: 2, Timeout: 40 * time.Millisecond}, nil)
	var calls atomic.Int32
	var cleaned atomic.Int32
	// Independent request batches share one worker bound.
	started := make(chan struct{})
	release := make(chan struct{})
	ctx, first := s.Request(context.Background())
	if err := Register(ctx, func(context.Context) error { close(started); <-release; return nil }); err != nil {
		t.Fatal(err)
	}
	first.Finish()
	<-started
	ctx, second := s.Request(context.Background())
	if err := RegisterFinally(ctx, func(context.Context) error { calls.Add(1); return nil }, func() { cleaned.Add(1) }); err != nil {
		t.Fatal(err)
	}
	second.Finish()
	time.Sleep(75 * time.Millisecond) // first deliberately ignores its budget until released
	close(release)
	closeScheduler(t, s)
	if calls.Load() != 0 || cleaned.Load() != 1 {
		t.Fatal("expired queued work executed")
	}
}
func TestPlatformDeadlineAndWait(t *testing.T) {
	s, _ := New(Options{}, nil)
	parent, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	ctx, collector := Collect(parent)
	ctx, scope := s.Request(ctx)
	observed := make(chan time.Time, 1)
	finished := make(chan struct{})
	if err := Register(ctx, func(work context.Context) error {
		deadline, _ := work.Deadline()
		observed <- deadline
		<-work.Done()
		close(finished)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	scope.Finish()
	collector.Finish(parent, true)
	deadline := <-observed
	expected, _ := parent.Deadline()
	if !deadline.Equal(expected) {
		t.Fatal("ignored invocation deadline")
	}
	closeScheduler(t, s)
	select {
	case <-finished:
	default:
		t.Fatal("callback was not canceled")
	}
}
