package client

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mutationQueueResult struct {
	id      int
	release func()
	err     error
}

func mutationQueueWaiting(t *testing.T, q *mutationDispatchQueue, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		q.mu.Lock()
		got := len(q.waiting)
		q.mu.Unlock()
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue waiting=%d, want%d", got, want)
		}
		runtime.Gosched()
	}
}

func mutationQueueReceive(t *testing.T, results <-chan mutationQueueResult) mutationQueueResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("mutation queue did not settle")
		return mutationQueueResult{}
	}
}

func TestMutationQueueFIFOAndCanceledMiddleWaiter(t *testing.T) {
	q := newMutationDispatchQueue(4)
	active, err := q.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer active()
	results := make(chan mutationQueueResult, 4)
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("component was replaced")
	for i := 0; i < 4; i++ {
		requestContext := context.Background()
		if i == 2 {
			requestContext = ctx
		}
		go func(id int, ctx context.Context) {
			release, err := q.acquire(ctx)
			results <- mutationQueueResult{id, release, err}
		}(i, requestContext)
		mutationQueueWaiting(t, q, i+1)
	}
	cancel(cause)
	removed := mutationQueueReceive(t, results)
	if removed.id != 2 || !errors.Is(removed.err, cause) || removed.release != nil {
		t.Fatal("queued cancellation lost its cause", removed)
	}
	mutationQueueWaiting(t, q, 3)
	active()
	for _, want := range []int{0, 1, 3} {
		result := mutationQueueReceive(t, results)
		if result.err != nil || result.id != want || result.release == nil {
			t.Fatalf("FIFO grant=%+v, want%d", result, want)
		}
		result.release()
	}
	mutationQueueWaiting(t, q, 0)
}

func TestMutationQueueBoundIs64WaitingPlusOneActive(t *testing.T) {
	q := newMutationDispatchQueue(64)
	active, err := q.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer active()
	results := make(chan mutationQueueResult, 64)
	for i := 0; i < 64; i++ {
		go func(id int) {
			release, err := q.acquire(context.Background())
			results <- mutationQueueResult{id, release, err}
		}(i)
		mutationQueueWaiting(t, q, i+1)
	}
	if release, err := q.acquire(context.Background()); !errors.Is(err, ErrMutationQueueFull) || release != nil {
		t.Fatal("overflow was admitted", err)
	}
	q.freeze()
	for i := 0; i < 64; i++ {
		result := mutationQueueReceive(t, results)
		if !errors.Is(result.err, ErrMutationQueueFrozen) || result.release != nil {
			t.Fatal("frozen pending work dispatched", result)
		}
	}
	if release, err := q.acquire(context.Background()); !errors.Is(err, ErrMutationQueueFrozen) || release != nil {
		t.Fatal("frozen document admitted new work", err)
	}
	active()
	q.reopen()
	release, err := q.acquire(context.Background())
	if err != nil {
		t.Fatal("restored queue did not reopen", err)
	}
	release()
}

func TestMutationQueueFreezeLeavesActiveLeaseOwnedAcrossReopen(t *testing.T) {
	q := newMutationDispatchQueue(1)
	active, err := q.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer active()
	q.freeze()
	q.reopen()
	results := make(chan mutationQueueResult, 1)
	go func() {
		release, err := q.acquire(context.Background())
		results <- mutationQueueResult{release: release, err: err}
	}()
	mutationQueueWaiting(t, q, 1)
	select {
	case result := <-results:
		t.Fatal("reopen released an unsettled active transport", result)
	default:
	}
	active()
	result := mutationQueueReceive(t, results)
	if result.err != nil {
		t.Fatal(result.err)
	}
	result.release()
}

func TestMutationQueueReleaseIsIdempotentAndCannotReleaseNewOwner(t *testing.T) {
	q := newMutationDispatchQueue(1)
	first, err := q.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first()
	second, err := q.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	first()
	results := make(chan mutationQueueResult, 1)
	go func() {
		release, err := q.acquire(context.Background())
		results <- mutationQueueResult{release: release, err: err}
	}()
	mutationQueueWaiting(t, q, 1)
	second()
	result := mutationQueueReceive(t, results)
	if result.err != nil {
		t.Fatal(result.err)
	}
	result.release()
}

func TestMutationQueueCanceledOrFrozenGrantPassesSlotWithoutDispatch(t *testing.T) {
	for _, frozen := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "freeze"}[frozen], func(t *testing.T) {
			q := newMutationDispatchQueue(1)
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			// Stage the exact handoff window: grant has been signaled, but
			// acquire has not yet returned its lease to a transport owner.
			w := &mutationDispatchWaiter{ready: make(chan struct{}), state: mutationGranted}
			q.active = w
			cause := errors.New("canceled during grant")
			if frozen {
				q.freeze()
				cause = ErrMutationQueueFrozen
			} else {
				cancel(cause)
			}
			if release, err := q.accept(ctx, w); !errors.Is(err, cause) || release != nil {
				t.Fatal("canceled/frozen grant reached dispatch", err)
			}
			q.reopen()
			release, err := q.acquire(context.Background())
			if err != nil {
				t.Fatal("grant retained its abandoned slot", err)
			}
			release()
		})
	}
}

func TestMutationQueueReopenCannotResurrectSignaledUnacceptedGrant(t *testing.T) {
	q := newMutationDispatchQueue(1)
	active, err := q.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Hold acquire immediately after its FIFO grant signal, before accept
	// transfers ownership to a transport. No scheduler timing is involved.
	w := &mutationDispatchWaiter{ctx: context.Background(), ready: make(chan struct{})}
	q.mu.Lock()
	q.waiting = append(q.waiting, w)
	q.mu.Unlock()
	active()
	select {
	case <-w.ready:
	default:
		t.Fatal("queued request was not granted")
	}
	q.freeze()
	q.reopen()
	restoredContext, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	newOwner, err := q.acquire(restoredContext)
	if err != nil {
		t.Fatal("restored document could not admit new work", err)
	}
	defer newOwner()
	if release, err := q.accept(context.Background(), w); !errors.Is(err, ErrMutationQueueFrozen) || release != nil {
		t.Fatal("pre-freeze grant was resurrected after reopen", err)
	}
	q.mu.Lock()
	stillOwned := q.active != nil && q.active != w && q.active.accepted
	q.mu.Unlock()
	if !stillOwned {
		t.Fatal("abandoned grant released the restored document's active owner")
	}
}

func TestMutationQueueConcurrentHandoffsStayExclusive(t *testing.T) {
	q := newMutationDispatchQueue(8)
	var running, violations atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			<-start
			for round := 0; round < 40; round++ {
				ctx, cancel := context.WithCancelCause(context.Background())
				if (id+round)%5 == 0 {
					cancel(context.Canceled)
				}
				release, err := q.acquire(ctx)
				if err != nil {
					if !errors.Is(err, context.Canceled) && !errors.Is(err, ErrMutationQueueFull) {
						violations.Add(1)
					}
					cancel(nil)
					continue
				}
				if running.Add(1) != 1 {
					violations.Add(1)
				}
				runtime.Gosched()
				running.Add(-1)
				release()
				release()
				cancel(nil)
			}
		}(i)
	}
	close(start)
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent queue handoffs did not settle")
	}
	if running.Load() != 0 || violations.Load() != 0 {
		t.Fatal("concurrent transport leases overlapped", running.Load(), violations.Load())
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active != nil || len(q.waiting) != 0 {
		t.Fatal("queue retained completed owners")
	}
}
