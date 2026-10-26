package ui

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunStreamLoaderCancellationRetainsDeferPhysicalCapacity(t *testing.T) {
	limiter := testStreamLimiter(t, 1, 1)
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	work, release := testAdmitStream(t, limiter, request)
	defer release()
	entered, exit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	var calls atomic.Int32
	returned := make(chan error, 1)
	go func() {
		returned <- RunStreamLoader(work, func(context.Context) error {
			calls.Add(1)
			close(entered)
			<-exit // Deliberately ignore request cancellation.
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("loader did not start")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter did not return")
	}
	release()
	waiting, stop := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer stop()
	next, free := testAdmitStream(t, limiter, waiting)
	_, err := HTMLContext(next, Defer("ordinary", nil, func(context.Context) (Node, error) {
		calls.Add(1)
		return Text("must not start"), nil
	}))
	free()
	if !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 1 {
		t.Fatal("metadata cancellation released a physical Defer slot", err, calls.Load())
	}
	finish()
	recovered, end := context.WithTimeout(context.Background(), 2*time.Second)
	defer end()
	last, lastFree := testAdmitStream(t, limiter, recovered)
	defer lastFree()
	if err := RunStreamLoader(last, func(context.Context) error { calls.Add(1); return nil }); err != nil || calls.Load() != 2 {
		t.Fatal("physical slot did not recover", err, calls.Load())
	}
}

func TestRunStreamLoaderSharesExistingDeferCapacity(t *testing.T) {
	limiter := testStreamLimiter(t, 2, 1)
	work, release := testAdmitStream(t, limiter, context.Background())
	defer release()
	entered, exit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	rendered := make(chan error, 1)
	go func() {
		rendered <- RenderEvents(work, Defer("ordinary", nil, func(context.Context) (Node, error) {
			close(entered)
			<-exit
			return Text("finished"), nil
		}), func(StreamEvent) error { return nil })
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary deferred callback did not start")
	}
	request, stop := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer stop()
	other, free := testAdmitStream(t, limiter, request)
	defer free()
	var calls atomic.Int32
	if err := RunStreamLoader(other, func(context.Context) error { calls.Add(1); return nil }); !errors.Is(err, context.DeadlineExceeded) || calls.Load() != 0 {
		t.Fatal("wrapper bypassed ordinary Defer capacity", err, calls.Load())
	}
	finish()
	select {
	case err := <-rendered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ordinary render did not join")
	}
}

func TestRunStreamLoaderReentrancyAndCompletedContext(t *testing.T) {
	limiter := testStreamLimiter(t, 1, 1)
	work, release := testAdmitStream(t, limiter, context.Background())
	defer release()
	var captured context.Context
	var inner atomic.Int32
	err := RunStreamLoader(work, func(ctx context.Context) error {
		captured = ctx
		return RunStreamLoader(ctx, func(context.Context) error { inner.Add(1); return nil })
	})
	if !errors.Is(err, ErrStreamReentrant) || inner.Load() != 0 {
		t.Fatal("nested loader ran", err, inner.Load())
	}
	if err := RunStreamLoader(captured, func(context.Context) error { inner.Add(1); return nil }); err != nil || inner.Load() != 1 {
		t.Fatal("finished callback left a phantom reentrancy frame", err, inner.Load())
	}
}

func TestRunStreamLoaderRejectsActiveAncestorAcrossLimiters(t *testing.T) {
	a, b := testStreamLimiter(t, 2, 1), testStreamLimiter(t, 1, 1)
	work, release := testAdmitStream(t, a, context.Background())
	defer release()
	err := RunStreamLoader(work, func(ctx context.Context) error {
		other, free, err := b.Begin(ctx)
		if err != nil {
			return err
		}
		defer free()
		return RunStreamLoader(other, func(ctx context.Context) error {
			ancestor, end, err := a.Begin(ctx)
			if err != nil {
				return err
			}
			defer end()
			return RunStreamLoader(ancestor, func(context.Context) error { return errors.New("active ancestor invoked") })
		})
	})
	if !errors.Is(err, ErrStreamReentrant) {
		t.Fatal(err)
	}
}

type streamLoaderPanicError struct {
	called *atomic.Bool
	exit   bool
}

func (p streamLoaderPanicError) Error() string { return streamLoaderUnsafeFormat(p.called, p.exit) }

type streamLoaderPanicStringer struct {
	called *atomic.Bool
	exit   bool
}

func (p streamLoaderPanicStringer) String() string { return streamLoaderUnsafeFormat(p.called, p.exit) }

func streamLoaderUnsafeFormat(called *atomic.Bool, exit bool) string {
	called.Store(true)
	if exit {
		runtime.Goexit()
	}
	panic("application panic formatter invoked")
}

func TestRunStreamLoaderAbnormalCallbacksReleaseCapacity(t *testing.T) {
	var errorCalls, stringCalls [2]atomic.Bool
	for _, tc := range []struct {
		name            string
		load            func(context.Context) error
		formatterCalled *atomic.Bool
	}{
		{"panic", func(context.Context) error { panic("controlled loader panic") }, nil},
		{"goexit", func(context.Context) error { runtime.Goexit(); return nil }, nil},
		{"error-goexit", func(context.Context) error { panic(streamLoaderPanicError{&errorCalls[0], true}) }, &errorCalls[0]},
		{"error-panic", func(context.Context) error { panic(streamLoaderPanicError{&errorCalls[1], false}) }, &errorCalls[1]},
		{"string-goexit", func(context.Context) error { panic(streamLoaderPanicStringer{&stringCalls[0], true}) }, &stringCalls[0]},
		{"string-panic", func(context.Context) error { panic(streamLoaderPanicStringer{&stringCalls[1], false}) }, &stringCalls[1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limiter := testStreamLimiter(t, 1, 1)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			work, release := testAdmitStream(t, limiter, ctx)
			defer release()
			if err := RunStreamLoader(work, tc.load); err == nil || tc.name == "panic" && !strings.Contains(err.Error(), "panic") {
				t.Fatal("abnormal callback was not reported", err)
			}
			if tc.formatterCalled != nil && tc.formatterCalled.Load() {
				t.Fatal("cleanup invoked application Error/String method")
			}
			if err := RunStreamLoader(work, func(context.Context) error { return nil }); err != nil {
				t.Fatal("abnormal callback stranded a physical slot", err)
			}
		})
	}
}

func TestRunStreamLoaderInputCancellationAndErrorIdentity(t *testing.T) {
	if err := RunStreamLoader(nil, func(context.Context) error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := RunStreamLoader(context.Background(), nil); err == nil {
		t.Fatal("nil callback accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called atomic.Bool
	if err := RunStreamLoader(ctx, func(context.Context) error { called.Store(true); return nil }); !errors.Is(err, context.Canceled) || called.Load() {
		t.Fatal("already canceled callback invoked", err, called.Load())
	}
	want := errors.New("callback error identity")
	if err := RunStreamLoader(context.Background(), func(context.Context) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestRunStreamLoaderRenderEventsAbnormalCallbackCompletes(t *testing.T) {
	var errorCalled, stringCalled atomic.Bool
	for _, tc := range []struct {
		name            string
		load            func(context.Context) (Node, error)
		formatterCalled *atomic.Bool
	}{
		{"goexit", func(context.Context) (Node, error) { runtime.Goexit(); return nil, nil }, nil},
		{"panic-error-goexit", func(context.Context) (Node, error) { panic(streamLoaderPanicError{&errorCalled, true}) }, &errorCalled},
		{"panic-string-goexit", func(context.Context) (Node, error) { panic(streamLoaderPanicStringer{&stringCalled, true}) }, &stringCalled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request, cancel := context.WithCancel(context.Background())
			defer cancel()
			limiter := testStreamLimiter(t, 1, 1)
			work, release := testAdmitStream(t, limiter, request)
			defer release()
			var events []StreamEvent
			completed := make(chan error, 1)
			go func() {
				completed <- RenderEvents(work, Fragment(
					Defer("abnormal", Text("waiting"), tc.load),
					Defer("following", Text("next waiting"), func(context.Context) (Node, error) { return Text("next loaded"), nil }),
				), func(event StreamEvent) error { events = append(events, event); return nil })
			}()
			select {
			case err := <-completed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("abnormal deferred worker exited without delivering its boundary result")
			}
			var failed, following bool
			for _, event := range events {
				if event.ID == "abnormal" {
					failed = event.Err != nil && strings.Contains(string(event.HTML), "Unable to load content.")
				}
				if event.ID == "following" {
					following = event.Err == nil && string(event.HTML) == "next loaded"
				}
			}
			if !failed || !following {
				t.Fatal("missing failed boundary or subsequent successful task", failed, following)
			}
			if tc.formatterCalled != nil && tc.formatterCalled.Load() {
				t.Fatal("RenderEvents invoked application panic formatter")
			}
			if err := RunStreamLoader(work, func(context.Context) error { return nil }); err != nil {
				t.Fatal("physical slot leaked", err)
			}
		})
	}
}

func TestRunStreamLoaderRenderEventsCancellationDoesNotReadLateNode(t *testing.T) {
	for iteration := 0; iteration < 32; iteration++ {
		request, cancel := context.WithCancel(context.Background())
		limiter := testStreamLimiter(t, 1, 1)
		work, release := testAdmitStream(t, limiter, request)
		entered, exit := make(chan struct{}), make(chan struct{})
		var returned atomic.Bool
		var lateEvents atomic.Int32
		completed := make(chan error, 1)
		go func() {
			completed <- RenderEvents(work, Defer("late", Text("waiting"), func(context.Context) (Node, error) {
				close(entered)
				<-exit
				return Text("late node"), nil
			}), func(StreamEvent) error {
				if returned.Load() {
					lateEvents.Add(1)
				}
				return nil
			})
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			cancel()
			close(exit)
			release()
			t.Fatal("late callback did not start")
		}
		// Do not join the canceled renderer before allowing the callback to write
		// its captured node: these conflicting paths must be safe under -race.
		cancel()
		close(exit)
		select {
		case err := <-completed:
			if !errors.Is(err, context.Canceled) {
				release()
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			release()
			t.Fatal("canceled renderer did not return")
		}
		returned.Store(true)
		release()
		last, end := context.WithTimeout(context.Background(), 2*time.Second)
		again, free := testAdmitStream(t, limiter, last)
		err := RunStreamLoader(again, func(context.Context) error { return nil })
		free()
		end()
		if err != nil || lateEvents.Load() != 0 {
			t.Fatal("late callback emitted or leaked capacity", err, lateEvents.Load())
		}
	}
}
