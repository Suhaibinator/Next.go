package nextgo

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/streamguard"
	"github.com/Suhaibinator/Next.go/ui"
)

func metadataTestLease(t *testing.T, ctx context.Context) *metadataMemoLease {
	t.Helper()
	lease, err := beginMetadataMemoLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
func metadataTestPending(t *testing.T, lease *metadataMemoLease) {
	t.Helper()
	select {
	case <-lease.done:
		t.Fatal("lease completed before its physical dependencies")
	default:
	}
}
func metadataTestJoined(t *testing.T, lease *metadataMemoLease) {
	t.Helper()
	joined := make(chan struct{})
	go func() { lease.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(2 * time.Second):
		t.Fatal("metadata Memo lease did not join")
	}
}

func TestMetadataMemoLeaseRequiresScopeAndCallbackCompletion(t *testing.T) {
	for _, ctx := range []context.Context{nil, context.Background()} {
		if lease, err := beginMetadataMemoLease(ctx); lease != nil || !errors.Is(err, ErrMemoScope) {
			t.Fatal(lease, err)
		}
	}
	root := WithRequestMemo(context.Background())
	lease := metadataTestLease(t, root)
	metadataTestPending(t, lease) // Zero entries does not mean callback returned.
	lease.FinishCallback()
	lease.FinishCallback()
	metadataTestJoined(t, lease)
	// Finishing twice remains safe after the observer is detached.
	lease.FinishCallback()
}

func TestMetadataMemoLeaseRetainsAlreadyPendingCanceledWaiter(t *testing.T) {
	root := WithRequestMemo(context.Background())
	waiter, cancel := context.WithCancel(root)
	defer cancel()
	entered, exit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	returned := make(chan error, 1)
	go func() {
		_, err := Memo(waiter, "prior", func(context.Context) (int, error) {
			close(entered)
			<-exit
			return 7, nil
		})
		returned <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("prior Memo did not start")
	}
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A canceled context still identifies live physical work in its scope.
	lease := metadataTestLease(t, waiter)
	lease.FinishCallback()
	metadataTestPending(t, lease)
	finish()
	metadataTestJoined(t, lease)
	value, err := Memo(root, "prior", func(context.Context) (int, error) { return 99, nil })
	if err != nil || value != 7 {
		t.Fatal("lease changed Memo result/deduplication", value, err)
	}
}

func TestMetadataMemoLeaseSubsequentSharedEntryAndConcurrentFinish(t *testing.T) {
	root := WithRequestMemo(context.Background())
	lease := metadataTestLease(t, root)
	entered, exit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	var calls atomic.Int32
	loader := func(context.Context) (int, error) { calls.Add(1); close(entered); <-exit; return 11, nil }
	results := make(chan error, 32)
	for i := 0; i < 32; i++ {
		go func() {
			value, err := Memo(root, "shared", loader)
			if err == nil && value != 11 {
				err = fmt.Errorf("shared value %d", value)
			}
			results <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("shared Memo did not start")
	}
	var finished sync.WaitGroup
	finished.Add(16)
	for i := 0; i < 16; i++ {
		go func() { defer finished.Done(); lease.FinishCallback() }()
	}
	finished.Wait()
	metadataTestPending(t, lease)
	finish()
	for i := 0; i < 32; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	metadataTestJoined(t, lease)
	if calls.Load() != 1 {
		t.Fatal("lease created duplicate physical entries", calls.Load())
	}
}

func TestMetadataMemoLeaseTracksNestedWorkFromOlderLoaderContext(t *testing.T) {
	root := WithRequestMemo(context.Background())
	parentEntered, startNested := make(chan struct{}), make(chan struct{})
	childEntered, childExit := make(chan struct{}), make(chan struct{})
	releaseChild := sync.OnceFunc(func() { close(childExit) })
	defer releaseChild()
	start := sync.OnceFunc(func() { close(startNested) })
	defer start()
	parentResult := make(chan error, 1)
	go func() {
		_, err := Memo(root, "parent", func(older context.Context) (int, error) {
			close(parentEntered)
			<-startNested
			waiter, cancel := context.WithCancel(older)
			defer cancel()
			childResult := make(chan error, 1)
			go func() {
				_, err := Memo(waiter, "nested", func(context.Context) (int, error) {
					close(childEntered)
					<-childExit
					return 23, nil
				})
				childResult <- err
			}()
			<-childEntered
			cancel()
			if err := <-childResult; !errors.Is(err, context.Canceled) {
				return 0, fmt.Errorf("nested canceled waiter: %v", err)
			}
			return 1, nil // Physical child still running after this parent returns.
		})
		parentResult <- err
	}()
	select {
	case <-parentEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("parent did not start")
	}
	lease := metadataTestLease(t, root)
	lease.FinishCallback() // Existing parent keeps observer live for future work.
	start()
	select {
	case err := <-parentResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent did not finish after its waiter canceled")
	}
	metadataTestPending(t, lease)
	scope := root.Value(memoScopeKey{}).(*memoScope)
	scope.mu.Lock()
	pending := scope.pending
	scope.mu.Unlock()
	if pending != 1 {
		t.Fatal("older nested physical dependency was not retained", pending)
	}
	releaseChild()
	metadataTestJoined(t, lease)
}

func TestMetadataMemoLeasesShareDependenciesButFinishIndependently(t *testing.T) {
	root := WithRequestMemo(context.Background())
	entered, exit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	result := make(chan error, 1)
	go func() {
		_, err := Memo(root, "one", func(context.Context) (int, error) { close(entered); <-exit; return 1, nil })
		result <- err
	}()
	<-entered
	first, second := metadataTestLease(t, root), metadataTestLease(t, root)
	first.FinishCallback()
	finish()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	metadataTestJoined(t, first)
	metadataTestPending(t, second)
	second.FinishCallback()
	metadataTestJoined(t, second)
	third := metadataTestLease(t, root)
	third.FinishCallback()
	metadataTestJoined(t, third) // Completed entries are not phantom dependencies.
}

type metadataMemoPanicError struct {
	called *atomic.Bool
	exit   bool
}

func (p metadataMemoPanicError) Error() string { return metadataMemoUnsafeFormat(p.called, p.exit) }

type metadataMemoPanicStringer struct {
	called *atomic.Bool
	exit   bool
}

func (p metadataMemoPanicStringer) String() string { return metadataMemoUnsafeFormat(p.called, p.exit) }

func metadataMemoUnsafeFormat(called *atomic.Bool, exit bool) string {
	called.Store(true)
	if exit {
		runtime.Goexit()
	}
	panic("application Memo panic formatter invoked")
}

func TestMetadataMemoLeaseAbnormalEntryCompletionAndCapacity(t *testing.T) {
	var errorCalls, stringCalls [2]atomic.Bool
	for _, tc := range []struct {
		name            string
		load            func(context.Context) (int, error)
		formatterCalled *atomic.Bool
	}{
		{"panic", func(context.Context) (int, error) { panic("controlled Memo panic") }, nil},
		{"goexit", func(context.Context) (int, error) { runtime.Goexit(); return 0, nil }, nil},
		{"error-goexit", func(context.Context) (int, error) { panic(metadataMemoPanicError{&errorCalls[0], true}) }, &errorCalls[0]},
		{"error-panic", func(context.Context) (int, error) { panic(metadataMemoPanicError{&errorCalls[1], false}) }, &errorCalls[1]},
		{"string-goexit", func(context.Context) (int, error) { panic(metadataMemoPanicStringer{&stringCalls[0], true}) }, &stringCalls[0]},
		{"string-panic", func(context.Context) (int, error) { panic(metadataMemoPanicStringer{&stringCalls[1], false}) }, &stringCalls[1]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			root := WithRequestMemo(ctx)
			lease := metadataTestLease(t, root)
			if _, err := Memo(root, tc.name, tc.load); err == nil {
				t.Fatal("abnormal entry was accepted")
			}
			if tc.formatterCalled != nil && tc.formatterCalled.Load() {
				t.Fatal("Memo cleanup invoked application Error/String method")
			}
			lease.FinishCallback()
			metadataTestJoined(t, lease)
		})
	}
	root := WithRequestMemo(context.Background())
	lease := metadataTestLease(t, root)
	for i := 0; i < 128; i++ {
		if _, err := Memo(root, fmt.Sprint(i), func(context.Context) (int, error) { return 1, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Memo(root, "overflow", func(context.Context) (int, error) { return 2, nil }); !errors.Is(err, ErrMemoCapacity) {
		t.Fatal("lease changed request entry capacity", err)
	}
	lease.FinishCallback()
	metadataTestJoined(t, lease)
}

func TestMetadataMemoLeaseHoldsStreamSlotAfterRequestCancellation(t *testing.T) {
	limiter, err := ui.NewStreamLimiter(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := WithRequestMemo(request)
	work, release, err := limiter.Begin(root)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	entered, exit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	callbackWaiting := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- ui.RunStreamLoader(work, func(ctx context.Context) error {
			lease, err := beginMetadataMemoLease(ctx)
			if err != nil {
				return err
			}
			defer func() { lease.FinishCallback(); close(callbackWaiting); lease.Wait() }()
			_, err = Memo(ctx, "physical", func(context.Context) (int, error) {
				close(entered)
				<-exit
				return 5, nil
			})
			return err
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("physical Memo dependency did not start")
	}
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP-side waiter did not cancel")
	}
	select {
	case <-callbackWaiting:
	case <-time.After(2 * time.Second):
		t.Fatal("metadata callback did not reach physical join")
	}
	release()
	deadline, stop := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer stop()
	next, free, err := limiter.Begin(deadline)
	if err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	err = ui.RunStreamLoader(next, func(context.Context) error { called.Store(true); return nil })
	free()
	if !errors.Is(err, context.DeadlineExceeded) || called.Load() {
		t.Fatal("canceled Memo waiter released live physical capacity", err, called.Load())
	}
	finish()
	last, end := context.WithTimeout(context.Background(), 2*time.Second)
	defer end()
	again, againFree, err := limiter.Begin(last)
	if err != nil {
		t.Fatal(err)
	}
	defer againFree()
	if err := ui.RunStreamLoader(again, func(context.Context) error { return nil }); err != nil {
		t.Fatal("physical dependency did not release after exit", err)
	}
}

func TestMetadataMemoLeaseFinishRacesPhysicalCompletion(t *testing.T) {
	for iteration := 0; iteration < 16; iteration++ {
		root := WithRequestMemo(context.Background())
		lease := metadataTestLease(t, root)
		entered, exit := make(chan struct{}), make(chan struct{})
		result := make(chan error, 1)
		go func() {
			_, err := Memo(root, "physical", func(context.Context) (int, error) {
				close(entered)
				<-exit
				return 1, nil
			})
			result <- err
		}()
		<-entered
		start := make(chan struct{})
		var joined sync.WaitGroup
		joined.Add(9)
		for i := 0; i < 8; i++ {
			go func() { defer joined.Done(); <-start; lease.FinishCallback() }()
		}
		go func() { defer joined.Done(); <-start; close(exit) }()
		close(start)
		joined.Wait()
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		metadataTestJoined(t, lease)
		scope := root.Value(memoScopeKey{}).(*memoScope)
		scope.mu.Lock()
		pending := scope.pending
		scope.mu.Unlock()
		if pending != 0 {
			t.Fatal("completed physical entries retained scope pending count", pending)
		}
	}
}

func metadataTestStreamContext(t *testing.T, limiter *ui.StreamLimiter, ctx context.Context) (context.Context, func()) {
	t.Helper()
	work, release, err := limiter.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return work, release
}

func TestMetadataMemoLeaseRejectsOldSameLimiterWaitQueuedBeforeLease(t *testing.T) {
	metadataTestOldLimiterAdmission(t, false)
}

func TestMetadataMemoLeaseRejectsNestedFreshScopeWaitQueuedBeforeLease(t *testing.T) {
	metadataTestOldLimiterAdmission(t, true)
}

func metadataTestOldLimiterAdmission(t *testing.T, freshScope bool) {
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := WithRequestMemo(request)
	limiter, err := ui.NewStreamLimiter(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	first, freeFirst := metadataTestStreamContext(t, limiter, root)
	defer freeFirst()
	entered, tryUI, snapshotChecked := make(chan struct{}), make(chan struct{}), make(chan struct{})
	try := sync.OnceFunc(func() { close(tryUI) })
	defer try()
	var checked sync.Once
	var called atomic.Bool
	oldPhysicalResult := make(chan error, 1)
	captured := make(chan context.Context, 1)
	if err := ui.RunStreamLoader(first, func(ctx context.Context) error {
		waiter, stop := context.WithCancel(ctx)
		defer stop()
		waiterResult := make(chan error, 1)
		go func() {
			_, err := Memo(waiter, "older-stream", func(older context.Context) (int, error) {
				captured <- older
				close(entered)
				select {
				case <-tryUI:
				case <-older.Done():
					return 0, older.Err()
				}
				invoke := func(current context.Context) (int, error) {
					// Observe, but do not alter, the real guard snapshot. Metadata
					// already owns the sole slot; its lease is deliberately not live yet.
					observed := streamguard.WithGuard(current, func(owner any) (bool, <-chan struct{}) {
						blocked, changed := streamguard.State(current, owner)
						if !blocked {
							checked.Do(func() { close(snapshotChecked) })
						}
						return blocked, changed
					})
					err := ui.RunStreamLoader(observed, func(context.Context) error { called.Store(true); return nil })
					oldPhysicalResult <- err
					return 0, err
				}
				if freshScope {
					// Reuse the same key: values/cycle stacks MUST be isolated even
					// though the physical guard remains inherited from the parent.
					return Memo(WithRequestMemo(older), "older-stream", invoke)
				}
				return invoke(older)
			})
			waiterResult <- err
		}()
		select {
		case <-entered:
		case <-ctx.Done():
			return ctx.Err()
		}
		stop()
		if err := <-waiterResult; !errors.Is(err, context.Canceled) {
			return fmt.Errorf("old waiter: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	older := <-captured
	freeFirst() // Old stream frame is inactive; its Memo dependency still exists.
	metadata, freeMetadata := metadataTestStreamContext(t, limiter, root)
	defer freeMetadata()
	completed := make(chan error, 1)
	go func() {
		completed <- ui.RunStreamLoader(metadata, func(ctx context.Context) error {
			try()
			select {
			case <-snapshotChecked:
			case <-ctx.Done():
				return ctx.Err()
			}
			lease, err := beginMetadataMemoLease(ctx)
			if err != nil {
				return err
			}
			defer func() { lease.FinishCallback(); lease.Wait() }()
			return nil
		})
	}()
	// The request itself has no deadline: a timeout cannot resolve this cycle.
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old Memo admission did not wake when its dependency lease began")
	}
	select {
	case err := <-oldPhysicalResult:
		if !errors.Is(err, ui.ErrStreamReentrant) {
			t.Fatal("dependency was not refused", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Memo physical callback did not finish")
	}
	if called.Load() {
		t.Fatal("same-limiter dependency callback was invoked")
	}
	if blocked, _ := streamguard.State(older, limiter); blocked {
		t.Fatal("completed lease left a phantom guard")
	}
	if err := ui.RunStreamLoader(metadata, func(context.Context) error { return nil }); err != nil {
		t.Fatal("physical capacity did not recover", err)
	}
}

func TestMetadataMemoLeaseDoesNotRefuseDifferentLimiter(t *testing.T) {
	t.Run("same-scope", func(t *testing.T) { metadataTestDifferentLimiter(t, false) })
	t.Run("nested-fresh-scope", func(t *testing.T) { metadataTestDifferentLimiter(t, true) })
}

func metadataTestDifferentLimiter(t *testing.T, freshScope bool) {
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := WithRequestMemo(request)
	a, err := ui.NewStreamLimiter(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ui.NewStreamLimiter(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	first, freeFirst := metadataTestStreamContext(t, a, root)
	defer freeFirst()
	var older context.Context
	if err := ui.RunStreamLoader(first, func(ctx context.Context) error { older = ctx; return nil }); err != nil {
		t.Fatal(err)
	}
	entered, tryUI := make(chan struct{}), make(chan struct{})
	try := sync.OnceFunc(func() { close(tryUI) })
	defer try()
	physicalEntered, physicalExit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(physicalExit) })
	defer finish()
	var called atomic.Bool
	physicalResult := make(chan error, 1)
	memoResult := make(chan error, 1)
	go func() {
		_, err := Memo(older, "other-limiter", func(ctx context.Context) (int, error) {
			close(entered)
			select {
			case <-tryUI:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
			invoke := func(current context.Context) (int, error) {
				err := ui.RunStreamLoader(current, func(context.Context) error {
					called.Store(true)
					close(physicalEntered)
					<-physicalExit
					return nil
				})
				physicalResult <- err
				return 1, err
			}
			if freshScope {
				return Memo(WithRequestMemo(ctx), "other-limiter", invoke)
			}
			return invoke(ctx)
		})
		memoResult <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("old Memo did not start")
	}
	freeFirst()
	// Releasing the old render cancels its waiter, not the physical Memo entry.
	// Keep that expected outcome separate from actual different-limiter work.
	select {
	case err := <-memoResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("old waiter did not retain cancellation semantics", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("old waiter did not return after cancellation")
	}
	metadata, freeMetadata := metadataTestStreamContext(t, b, root)
	defer freeMetadata()
	leaseResult := make(chan *metadataMemoLease, 1)
	completed := make(chan error, 1)
	go func() {
		completed <- ui.RunStreamLoader(metadata, func(ctx context.Context) error {
			lease, err := beginMetadataMemoLease(ctx)
			if err != nil {
				return err
			}
			leaseResult <- lease
			defer func() { lease.FinishCallback(); lease.Wait() }()
			try()
			return nil
		})
	}()
	select {
	case <-physicalEntered:
	case err := <-completed:
		t.Fatal("metadata completed before its different-limiter dependency", err)
	case <-time.After(2 * time.Second):
		t.Fatal("different-limiter callback was not admitted")
	}
	lease := <-leaseResult
	metadataTestPending(t, lease)
	select {
	case err := <-completed:
		t.Fatal("metadata did not join physical different-limiter work", err)
	default:
	}
	finish()
	select {
	case err := <-physicalResult:
		if err != nil || !called.Load() {
			t.Fatal("unrelated physical limiter was refused", err, called.Load())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("different-limiter physical invocation did not finish")
	}
	select {
	case err := <-completed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("unrelated limiter was stalled by metadata")
	}
	metadataTestJoined(t, lease)
	// Both actual callback slots and the original render admission must recover.
	recovery, stopRecovery := context.WithTimeout(root, 2*time.Second)
	defer stopRecovery()
	recovered, freeRecovered := metadataTestStreamContext(t, a, recovery)
	defer freeRecovered()
	metadataRecovery, stopMetadataRecovery := context.WithTimeout(metadata, 2*time.Second)
	defer stopMetadataRecovery()
	for _, work := range []context.Context{recovered, metadataRecovery} {
		if err := ui.RunStreamLoader(work, func(context.Context) error { return nil }); err != nil {
			t.Fatal("different-limiter physical capacity did not recover", err)
		}
	}
}

func TestMetadataMemoLeaseRetainsCanceledNestedFreshScopePhysicalWork(t *testing.T) {
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := WithRequestMemo(request)
	limiter, err := ui.NewStreamLimiter(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	work, release := metadataTestStreamContext(t, limiter, root)
	defer release()
	childEntered, childExit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(childExit) })
	defer finish()
	parentAndCallbackReturned := make(chan struct{})
	leaseValue := make(chan *metadataMemoLease, 1)
	returned := make(chan error, 1)
	go func() {
		returned <- ui.RunStreamLoader(work, func(ctx context.Context) error {
			lease, err := beginMetadataMemoLease(ctx)
			if err != nil {
				return err
			}
			leaseValue <- lease
			defer func() { lease.FinishCallback(); close(parentAndCallbackReturned); lease.Wait() }()
			_, err = Memo(ctx, "parent", func(parent context.Context) (int, error) {
				fresh := WithRequestMemo(parent)
				waiter, stop := context.WithCancel(fresh)
				defer stop()
				childResult := make(chan error, 1)
				go func() {
					_, err := Memo(waiter, "parent", func(context.Context) (int, error) {
						close(childEntered)
						<-childExit
						return 2, nil
					})
					childResult <- err
				}()
				<-childEntered
				stop()
				if err := <-childResult; !errors.Is(err, context.Canceled) {
					return 0, fmt.Errorf("nested waiter: %v", err)
				}
				return 1, nil
			})
			return err
		})
	}()
	select {
	case <-parentAndCallbackReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("parent/callback did not finish")
	}
	lease := <-leaseValue
	metadataTestPending(t, lease)
	scope := root.Value(memoScopeKey{}).(*memoScope)
	scope.mu.Lock()
	pending := scope.pending
	scope.mu.Unlock()
	if pending != 0 {
		t.Fatal("original scope did not become idle", pending)
	}
	// Descendant physical work must retain the slot even though scope A and its
	// callback are now both idle. Cancellation cannot turn that into completion.
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("caller did not cancel")
	}
	release()
	bounded, stop := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer stop()
	next, free := metadataTestStreamContext(t, limiter, bounded)
	var invoked atomic.Bool
	err = ui.RunStreamLoader(next, func(context.Context) error { invoked.Store(true); return nil })
	free()
	if !errors.Is(err, context.DeadlineExceeded) || invoked.Load() {
		t.Fatal("descendant released physical capacity early", err, invoked.Load())
	}
	finish()
	metadataTestJoined(t, lease)
	recovered, end := context.WithTimeout(context.Background(), 2*time.Second)
	defer end()
	last, lastFree := metadataTestStreamContext(t, limiter, recovered)
	defer lastFree()
	if err := ui.RunStreamLoader(last, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestMetadataMemoLeaseDoesNotRetainIndependentRequestRoot(t *testing.T) {
	independent, cancelIndependent := context.WithCancel(context.Background())
	defer cancelIndependent()
	other := WithRequestMemo(independent)
	entered, exit := make(chan struct{}), make(chan struct{})
	finish := sync.OnceFunc(func() { close(exit) })
	defer finish()
	otherResult := make(chan error, 1)
	go func() {
		_, err := Memo(other, "same-key", func(context.Context) (int, error) { close(entered); <-exit; return 2, nil })
		otherResult <- err
	}()
	<-entered
	root := WithRequestMemo(context.Background())
	lease := metadataTestLease(t, root)
	lease.FinishCallback()
	metadataTestJoined(t, lease) // An unrelated request's work is still alive.
	finish()
	if err := <-otherResult; err != nil {
		t.Fatal(err)
	}
}

func TestMetadataMemoLeaseRefusesActiveSameLineageSelfDrain(t *testing.T) {
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := WithRequestMemo(request)
	limiter, err := ui.NewStreamLimiter(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	var providerCalled atomic.Bool
	completed := make(chan error, 1)
	go func() {
		_, err := Memo(root, "active-ancestor", func(ctx context.Context) (int, error) {
			fresh := WithRequestMemo(ctx) // New cache does not erase the active ancestor.
			work, release, err := limiter.Begin(fresh)
			if err != nil {
				return 0, err
			}
			defer release()
			err = ui.RunStreamLoader(work, func(work context.Context) error {
				lease, err := beginMetadataMemoLease(work)
				if err != nil {
					return err
				}
				defer func() { lease.FinishCallback(); lease.Wait() }()
				providerCalled.Store(true)
				return nil
			})
			return 0, err
		})
		completed <- err
	}()
	select {
	case err := <-completed:
		if !errors.Is(err, ui.ErrStreamReentrant) {
			t.Fatal("self-drain not refused", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("metadata waited for its own active Memo ancestor")
	}
	if providerCalled.Load() {
		t.Fatal("cyclic metadata provider was invoked")
	}
	work, release := metadataTestStreamContext(t, limiter, root)
	defer release()
	// No active Memo ancestor now; ordinary metadata must still run and the
	// failed admission must not retain a slot or an owner-count/lease.
	if err := ui.RunStreamLoader(work, func(work context.Context) error {
		lease, err := beginMetadataMemoLease(work)
		if err != nil {
			return err
		}
		defer func() { lease.FinishCallback(); lease.Wait() }()
		providerCalled.Store(true)
		return nil
	}); err != nil || !providerCalled.Load() {
		t.Fatal("ordinary metadata/capacity did not recover", err)
	}
}
