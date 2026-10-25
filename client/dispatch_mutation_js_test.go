//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
	"time"
)

func waitDocumentMutationQueue(t *testing.T, want int) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for {
		q := coreMutationQueue()
		q.mu.Lock()
		got := len(q.waiting)
		q.mu.Unlock()
		if got == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("waiting queue=%d want%d", got, want)
		case <-time.After(time.Millisecond):
		}
	}
}
func receiveMutation(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("typed mutation did not settle")
		return nil
	}
}

func TestDispatchMutationCancellationHoldsLeaseUntilPhysicalFetchSettlement(t *testing.T) {
	actionViewEnvironment(t)
	coreMutationQueue().reopen()
	defer coreMutationQueue().reopen()
	held := newControlledPromise()
	started := make(chan struct{}, 2)
	requests := 0
	mockBrowserFetch(t, func([]js.Value) any {
		requests++
		started <- struct{}{}
		if requests == 1 {
			return held.value
		}
		return actionViewResponse(`{"data":2}`, 200, false)
	})
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("caller abandoned active action")
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		first <- DispatchMutation(ctx, "/app/_nextgo/actions/first", FetchOptions{}, func(Response) error { return nil })
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first POST missing")
	}
	go func() {
		second <- DispatchMutation(context.Background(), "/app/_nextgo/actions/second", FetchOptions{}, func(Response) error { return nil })
	}()
	waitDocumentMutationQueue(t, 1)
	cancel(cause)
	// Context cancellation requests abort, but the deliberately noncooperative
	// native promise still owns the physical transport and active queue lease.
	if requests != 1 {
		t.Fatal("second POST overlapped active transport")
	}
	select {
	case <-first:
		t.Fatal("active lease returned before physical settlement")
	default:
	}
	held.resolve.Invoke(js.Global().Get("Response").New("late physical response"))
	if err := receiveMutation(t, first); !errors.Is(err, cause) {
		t.Fatal("active cancellation cause lost", err)
	}
	if err := receiveMutation(t, second); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal("queued POST never dispatched", requests)
	}
}

func TestDispatchMutationQueuedCancelAndDocumentFreezeNeverPost(t *testing.T) {
	actionViewEnvironment(t)
	coreMutationQueue().reopen()
	defer coreMutationQueue().reopen()
	held := newControlledPromise()
	started := make(chan struct{}, 1)
	requests := 0
	mockBrowserFetch(t, func([]js.Value) any {
		requests++
		if requests == 1 {
			started <- struct{}{}
			return held.value
		}
		return actionViewResponse(`{"data":2}`, 200, false)
	})
	active := make(chan error, 1)
	go func() {
		active <- DispatchMutation(context.Background(), "/app/_nextgo/actions/first", FetchOptions{}, func(Response) error { return nil })
	}()
	<-started
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("queued scope disposed")
	queued := make(chan error, 1)
	go func() {
		queued <- DispatchMutation(ctx, "/app/_nextgo/actions/canceled", FetchOptions{}, func(Response) error { return nil })
	}()
	waitDocumentMutationQueue(t, 1)
	cancel(cause)
	if err := receiveMutation(t, queued); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	go func() {
		queued <- DispatchMutation(context.Background(), "/app/_nextgo/actions/frozen", FetchOptions{}, func(Response) error { return nil })
	}()
	waitDocumentMutationQueue(t, 1)
	coreMutationQueue().freeze()
	if err := receiveMutation(t, queued); !errors.Is(err, ErrMutationQueueFrozen) {
		t.Fatal(err)
	}
	if err := DispatchMutation(context.Background(), "/app/_nextgo/actions/new", FetchOptions{}, func(Response) error { return nil }); !errors.Is(err, ErrMutationQueueFrozen) {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatal("canceled/frozen queued call posted", requests)
	}
	held.resolve.Invoke(js.Global().Get("Response").New("first complete"))
	if err := receiveMutation(t, active); err != nil {
		t.Fatal(err)
	}
	coreMutationQueue().reopen()
	if err := DispatchMutation(context.Background(), "/app/_nextgo/actions/restored", FetchOptions{}, func(Response) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if requests != 2 {
		t.Fatal(requests)
	}
}

func TestOrdinaryTypedAndCombinedViewShareFIFOAndSnapshotOnlyAtDispatch(t *testing.T) {
	actionViewEnvironment(t)
	coreMutationQueue().reopen()
	defer coreMutationQueue().reopen()
	held := newControlledPromise()
	started := make(chan struct{}, 1)
	requests := 0
	source := ""
	mockBrowserFetch(t, func(args []js.Value) any {
		requests++
		if requests == 1 {
			started <- struct{}{}
			return held.value
		}
		source = args[1].Get("headers").Call("get", "X-Nextgo-Action-URL").String()
		return actionViewResponse(confirmedActionFrame+failedActionViewFrame, 200, true)
	})
	first := make(chan error, 1)
	go func() {
		first <- DispatchMutation(context.Background(), "/app/_nextgo/actions/first", FetchOptions{}, func(Response) error { return nil })
	}()
	<-started
	type answer struct {
		view ActionViewOutcome
		err  error
	}
	second := make(chan answer, 1)
	seq := frameworkActionSequence
	go func() {
		view, err := ActionView(context.Background(), "/app/_nextgo/actions/add/v1", FetchOptions{}, func(Response) error { return nil })
		second <- answer{view, err}
	}()
	waitDocumentMutationQueue(t, 1)
	if requests != 1 || frameworkActionSequence != seq {
		t.Fatal("enqueue dispatched/fenced active intent")
	}
	currentURL = "https://site.test/app/next?q=current#fragment"
	win.Get("location").Set("href", currentURL)
	held.resolve.Invoke(js.Global().Get("Response").New("first complete"))
	if err := receiveMutation(t, first); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-second:
		if result.err != nil || result.view.Status != 503 || source != "/app/next?q=current" {
			t.Fatal(result, source)
		}
	case <-time.After(time.Second):
		t.Fatal("combined call did not settle")
	}
}

func TestQueuedMutationRunsSourceScopeGuardBeforeDispatchOrActionFence(t *testing.T) {
	for _, combined := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "combined"}[combined], func(t *testing.T) {
			actionViewEnvironment(t)
			coreMutationQueue().reopen()
			defer coreMutationQueue().reopen()
			held := newControlledPromise()
			started := make(chan struct{}, 1)
			requests := 0
			mockBrowserFetch(t, func([]js.Value) any { requests++; started <- struct{}{}; return held.value })
			first := make(chan error, 1)
			go func() {
				first <- DispatchMutation(context.Background(), "/app/_nextgo/actions/first", FetchOptions{}, func(Response) error { return nil })
			}()
			<-started
			gone := false
			cause := errors.New("source runtime scope already disposed")
			guardCalls := 0
			second := make(chan error, 1)
			request := FetchOptions{beforeMutationDispatch: func() error {
				guardCalls++
				if gone {
					return cause
				}
				return nil
			}}
			seq := frameworkActionSequence
			go func() {
				if combined {
					_, err := ActionView(context.Background(), "/app/_nextgo/actions/add/v1", request, func(Response) error { return nil })
					second <- err
				} else {
					second <- DispatchMutation(context.Background(), "/app/_nextgo/actions/second", request, func(Response) error { return nil })
				}
			}()
			waitDocumentMutationQueue(t, 1)
			gone = true
			held.resolve.Invoke(js.Global().Get("Response").New("first complete"))
			if err := receiveMutation(t, first); err != nil {
				t.Fatal(err)
			}
			if err := receiveMutation(t, second); !errors.Is(err, cause) {
				t.Fatal(err)
			}
			if requests != 1 || guardCalls != 1 || frameworkActionSequence != seq {
				t.Fatal("disposed queued scope posted/fenced", requests, guardCalls, frameworkActionSequence, seq)
			}
		})
	}
}

func TestTypedMutationLeaseIncludesPhysicalReadAndReaderCancellation(t *testing.T) {
	actionViewEnvironment(t)
	coreMutationQueue().reopen()
	defer coreMutationQueue().reopen()
	read, canceled := newControlledPromise(), newControlledPromise()
	reading, canceling := make(chan struct{}, 1), make(chan struct{}, 1)
	locksReleased := 0
	readFn := js.FuncOf(func(js.Value, []js.Value) any { reading <- struct{}{}; return read.value })
	defer readFn.Release()
	cancelFn := js.FuncOf(func(js.Value, []js.Value) any { canceling <- struct{}{}; return canceled.value })
	defer cancelFn.Release()
	unlock := js.FuncOf(func(js.Value, []js.Value) any { locksReleased++; return nil })
	defer unlock.Release()
	getReader := js.FuncOf(func(js.Value, []js.Value) any {
		return map[string]any{"read": readFn, "cancel": cancelFn, "releaseLock": unlock}
	})
	defer getReader.Release()
	requests := 0
	mockBrowserFetch(t, func([]js.Value) any {
		requests++
		if requests > 1 {
			return actionViewResponse(`{"data":2}`, 200, false)
		}
		return js.Global().Get("Promise").Call("resolve", map[string]any{"url": "https://site.test/app/_nextgo/actions/first", "status": 200, "headers": js.Global().Get("Headers").New(), "body": map[string]any{"getReader": getReader}})
	})
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(errors.New("test finished"))
	cause := errors.New("reader canceled")
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		first <- DispatchMutation(ctx, "/app/_nextgo/actions/first", FetchOptions{}, func(Response) error { return nil })
	}()
	select {
	case <-reading:
	case <-time.After(time.Second):
		t.Fatal("reader did not start")
	}
	go func() {
		second <- DispatchMutation(context.Background(), "/app/_nextgo/actions/second", FetchOptions{}, func(Response) error { return nil })
	}()
	waitDocumentMutationQueue(t, 1)
	cancel(cause)
	read.resolve.Invoke(map[string]any{"done": true})
	select {
	case <-canceling:
	case <-time.After(time.Second):
		t.Fatal("reader cancellation did not start")
	}
	if requests != 1 || locksReleased != 0 {
		t.Fatal("native cancel promise not settled before handoff", requests, locksReleased)
	}
	select {
	case <-first:
		t.Fatal("lease released while reader cancellation pending")
	default:
	}
	canceled.resolve.Invoke(js.Undefined())
	if err := receiveMutation(t, first); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if err := receiveMutation(t, second); err != nil {
		t.Fatal(err)
	}
	if requests != 2 || locksReleased != 1 {
		t.Fatal("reader/lease did not settle", requests, locksReleased)
	}
}
