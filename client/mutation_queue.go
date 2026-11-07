package client

import (
	"context"
	"errors"
	"sync"
)

// ErrMutationQueueFull rejects admission before a typed mutation is dispatched.
var ErrMutationQueueFull = errors.New("nextgo: typed mutation queue is full")

// ErrMutationQueueFrozen rejects queued/new work while its document is frozen.
// It does not establish whether an already dispatched mutation committed.
var ErrMutationQueueFrozen = errors.New("nextgo: typed mutation queue is frozen")

type mutationWaiterState uint8

const (
	mutationWaiting mutationWaiterState = iota
	mutationGranted
	mutationFinished
)

type mutationDispatchWaiter struct {
	// Done is captured once before admission; queue locks never invoke Context.
	done  <-chan struct{}
	ready chan struct{}
	state mutationWaiterState
	err   error
	// accepted is the lock-protected handoff from queued admission to the
	// active transport owner. A signaled grant can still be revoked by freeze.
	accepted bool
}

// mutationDispatchQueue owns only admission metadata, never application input
// or callbacks. The browser core creates one queue with 64 waiting positions;
// its single active lease remains owned by the physical transport operation.
type mutationDispatchQueue struct {
	mu         sync.Mutex
	maxWaiting int
	frozen     bool
	active     *mutationDispatchWaiter
	waiting    []*mutationDispatchWaiter
}

func newMutationDispatchQueue(maxWaiting int) *mutationDispatchQueue {
	if maxWaiting < 0 {
		panic("nextgo: mutation queue capacity cannot be negative")
	}
	return &mutationDispatchQueue{maxWaiting: maxWaiting}
}

// Context methods may be authored Go code. Call ONLY outside the queue lock.
// Keep the existing cause-first cancellation precedence; Cause itself may
// call a custom Value or Err method and must not be treated as inert.
func mutationContextError(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}

// Receiving from the cached channel invokes no Context method or callback.
// Context guarantees successive Done calls return the same channel and a
// closed Done accompanies a stable non-nil Err. Cache it before admission.
func mutationDoneClosed(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func mutationCanceledError(ctx context.Context) error {
	if err := mutationContextError(ctx); err != nil {
		return err
	}
	// Defensive refusal for a Context that violates the Done/Err contract.
	return context.Canceled
}

func (q *mutationDispatchQueue) acquire(ctx context.Context) (func(), error) {
	if ctx == nil {
		return nil, errors.New("nextgo: mutation dispatch requires a context")
	}
	if err := mutationContextError(ctx); err != nil {
		return nil, err
	}
	done := ctx.Done()
	q.mu.Lock()
	if mutationDoneClosed(done) {
		q.mu.Unlock()
		return nil, mutationCanceledError(ctx)
	}
	if q.frozen {
		q.mu.Unlock()
		return nil, ErrMutationQueueFrozen
	}
	w := &mutationDispatchWaiter{done: done, ready: make(chan struct{})}
	if q.active == nil {
		w.state = mutationGranted
		q.active = w
		q.mu.Unlock()
		return q.accept(ctx, w)
	}
	if len(q.waiting) >= q.maxWaiting {
		q.mu.Unlock()
		return nil, ErrMutationQueueFull
	}
	q.waiting = append(q.waiting, w)
	q.mu.Unlock()
	select {
	case <-w.ready:
	case <-done:
	}
	return q.accept(ctx, w)
}

// accept resolves cancellation/grant races under the same lock as release.
// A canceled request that was just granted immediately passes its slot onward.
func (q *mutationDispatchQueue) accept(ctx context.Context, w *mutationDispatchWaiter) (func(), error) {
	q.mu.Lock()
	if w.accepted {
		// Admission is a one-way handoff. A duplicate accept, even with a
		// canceled context, cannot surrender a physically owned transport.
		q.mu.Unlock()
		return nil, errors.New("nextgo: mutation admission was not granted")
	}
	if mutationDoneClosed(w.done) {
		switch w.state {
		case mutationWaiting:
			for i, item := range q.waiting {
				if item == w {
					copy(q.waiting[i:], q.waiting[i+1:])
					q.waiting[len(q.waiting)-1] = nil
					q.waiting = q.waiting[:len(q.waiting)-1]
					break
				}
			}
			w.state = mutationFinished
		case mutationGranted:
			if q.active == w {
				q.active = nil
				w.state = mutationFinished
				q.grantNextLocked()
			}
		}
		q.mu.Unlock()
		// The exact canceled ticket has surrendered its slot before authored
		// Cause/Err/Value can reenter. Preserve its cancellation cause.
		return nil, mutationCanceledError(ctx)
	}
	if w.err != nil {
		err := w.err
		q.mu.Unlock()
		return nil, err
	}
	if q.frozen && w.state == mutationGranted && q.active == w {
		q.active = nil
		w.state = mutationFinished
		q.mu.Unlock()
		return nil, ErrMutationQueueFrozen
	}
	if w.state != mutationGranted || q.active != w {
		q.mu.Unlock()
		return nil, errors.New("nextgo: mutation admission was not granted")
	}
	w.accepted = true
	q.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { q.release(w) }) }, nil
}

func (q *mutationDispatchQueue) release(w *mutationDispatchWaiter) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active != w {
		return
	}
	w.state, w.done = mutationFinished, nil
	q.active = nil
	q.grantNextLocked()
}

func (q *mutationDispatchQueue) grantNextLocked() {
	if q.frozen {
		return
	}
	for len(q.waiting) > 0 {
		w := q.waiting[0]
		q.waiting[0] = nil
		q.waiting = q.waiting[1:]
		if mutationDoneClosed(w.done) {
			// Only the owning acquire observes the authored cause, outside
			// q.mu. Wake it without retaining its Context in the FIFO.
			w.state = mutationFinished
			close(w.ready)
			continue
		}
		w.state = mutationGranted
		q.active = w
		close(w.ready)
		return
	}
}

func (q *mutationDispatchQueue) freeze() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.frozen = true
	if w := q.active; w != nil && !w.accepted {
		// A grant wakeup is not yet a dispatched/owned transport. Remember
		// its rejection on the waiter itself so reopen cannot resurrect it.
		// Its ready channel may already be closed by grantNextLocked.
		w.state, w.err = mutationFinished, ErrMutationQueueFrozen
		q.active = nil
	}
	for i, w := range q.waiting {
		w.state, w.err = mutationFinished, ErrMutationQueueFrozen
		close(w.ready)
		q.waiting[i] = nil
	}
	q.waiting = nil
}

func (q *mutationDispatchQueue) reopen() {
	q.mu.Lock()
	q.frozen = false
	q.mu.Unlock()
}
