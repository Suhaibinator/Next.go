// Package postresponse coordinates bounded, best-effort request completion work.
// It is shared by the HTTP application and platform response adapters.
package postresponse

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrScope    = errors.New("nextgo: After requires an active application request")
	ErrCapacity = errors.New("nextgo: After capacity exhausted")
	ErrClosed   = errors.New("nextgo: application is closing")
)

// Options bounds both queued closures and running callbacks. A zero field uses
// its default. Cancellation is cooperative; callbacks must observe their context.
type Options struct {
	Concurrency int
	MaxPending  int
	Timeout     time.Duration
}

type Scheduler struct {
	mu      sync.Mutex
	closed  bool
	pending int
	idle    chan struct{}
	workers chan struct{}
	root    context.Context
	cancel  context.CancelFunc
	options Options
	report  func(string, ...any)
}

func New(options Options, report func(string, ...any)) (*Scheduler, error) {
	if options.Concurrency == 0 {
		options.Concurrency = 4
	}
	if options.MaxPending == 0 {
		options.MaxPending = 64
	}
	if options.Timeout == 0 {
		options.Timeout = 10 * time.Second
	}
	if options.Concurrency < 1 || options.Concurrency > 64 || options.MaxPending < options.Concurrency || options.MaxPending > 4096 || options.Timeout < time.Millisecond || options.Timeout > 5*time.Minute {
		return nil, fmt.Errorf("nextgo: After requires concurrency 1..64, pending concurrency..4096, timeout 1ms..5m")
	}
	root, cancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	return &Scheduler{workers: make(chan struct{}, options.Concurrency), root: root, cancel: cancel, options: options, idle: idle, report: report}, nil
}

// Close stops reservations, drains accepted work, and cancels work if the drain
// deadline expires. It cannot forcibly terminate an uncooperative Go callback.
func (s *Scheduler) Close(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	idle := s.idle
	s.mu.Unlock()
	select {
	case <-idle:
		s.cancel()
		return nil
	case <-ctx.Done():
		s.cancel()
		return ctx.Err()
	}
}
func (s *Scheduler) reserve() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.pending >= s.options.MaxPending {
		return ErrCapacity
	}
	if s.pending == 0 {
		s.idle = make(chan struct{})
	}
	s.pending++
	return nil
}
func (s *Scheduler) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending--
	if s.pending == 0 {
		close(s.idle)
	}
}

type scopeKey struct{}
type blockKey struct{}

// BlockRegistration marks shared/build work that has no individual response.
func BlockRegistration(ctx context.Context, reason error) context.Context {
	return context.WithValue(ctx, blockKey{}, reason)
}

type Scope struct {
	mu        sync.Mutex
	scheduler *Scheduler
	ctx       context.Context
	sealed    bool
	jobs      []job
}
type job struct {
	ctx    context.Context
	run    func(context.Context) error
	finish func()
}

func (s *Scheduler) Request(ctx context.Context) (context.Context, *Scope) {
	scope := &Scope{scheduler: s, ctx: ctx}
	return context.WithValue(ctx, scopeKey{}, scope), scope
}

func Register(ctx context.Context, run func(context.Context) error) error {
	return RegisterFinally(ctx, run, nil)
}

// RegisterFinally releases framework-owned reservations even if accepted work
// is dropped before execution. finish must be nonblocking and must not panic.
// Registration errors leave cleanup to the caller.
func RegisterFinally(ctx context.Context, run func(context.Context) error, finish func()) error {
	if ctx == nil || run == nil {
		return ErrScope
	}
	if reason, _ := ctx.Value(blockKey{}).(error); reason != nil {
		return reason
	}
	scope, _ := ctx.Value(scopeKey{}).(*Scope)
	if scope == nil {
		return ErrScope
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.sealed {
		return ErrScope
	}
	if err := scope.ctx.Err(); err != nil {
		return err
	}
	if len(scope.jobs) >= 16 {
		return ErrCapacity
	}
	if err := scope.scheduler.reserve(); err != nil {
		return err
	}
	scope.jobs = append(scope.jobs, job{ctx: context.WithoutCancel(ctx), run: run, finish: finish})
	return nil
}

// Finish seals registration. A platform collector delays work until it accepts
// the response. Native HTTP starts work when the application handler finishes.
func (s *Scope) Finish() {
	s.mu.Lock()
	if s.sealed {
		s.mu.Unlock()
		return
	}
	s.sealed = true
	jobs := s.jobs
	s.jobs = nil
	s.mu.Unlock()
	if len(jobs) == 0 {
		return
	}
	start := func(parent context.Context, accepted bool) <-chan struct{} {
		done := make(chan struct{})
		if !accepted {
			for _, work := range jobs {
				s.scheduler.finishJob(work)
				s.scheduler.release()
			}
			close(done)
			return done
		}
		var pending sync.WaitGroup
		pending.Add(len(jobs))
		for _, work := range jobs {
			// Include time spent waiting for a worker in the callback's budget.
			deadline := time.Now().Add(s.scheduler.options.Timeout)
			if platformDeadline, ok := parent.Deadline(); ok && platformDeadline.Before(deadline) {
				deadline = platformDeadline
			}
			ctx, cancel := context.WithDeadline(work.ctx, deadline)
			stopPlatform := context.AfterFunc(parent, func() {
				// The child owns an equal or earlier deadline. Let that timer
				// preserve DeadlineExceeded instead of racing it with Cancel.
				if parent.Err() != context.DeadlineExceeded {
					cancel()
				}
			})
			stopShutdown := context.AfterFunc(s.scheduler.root, cancel)
			if parent.Err() != nil || s.scheduler.root.Err() != nil {
				cancel()
			}
			go func(work job) {
				defer pending.Done()
				defer s.scheduler.release()
				defer s.scheduler.finishJob(work)
				defer cancel()
				defer stopPlatform()
				defer stopShutdown()
				completed := false
				defer func() {
					failure := recover()
					if s.scheduler.report != nil && !completed {
						s.scheduler.report("nextgo after: callback exited unexpectedly (%T)", failure)
					}
				}()
				select {
				case s.scheduler.workers <- struct{}{}:
				case <-ctx.Done():
					completed = true
					return
				}
				defer func() { <-s.scheduler.workers }()
				if ctx.Err() != nil {
					completed = true
					return
				}
				err := work.run(ctx)
				completed = true
				if err != nil && s.scheduler.report != nil {
					s.scheduler.report("nextgo after: callback failed (%T)", err)
				}
			}(work)
		}
		go func() { pending.Wait(); close(done) }()
		return done
	}
	if collector, _ := s.ctx.Value(collectorKey{}).(*Collector); collector != nil {
		if !collector.add(start) {
			start(context.Background(), false)
		}
	} else {
		start(context.Background(), true)
	}
}

func (s *Scheduler) finishJob(work job) {
	defer func() {
		if failure := recover(); failure != nil && s.report != nil {
			s.report("nextgo after: cleanup failed (%T)", failure)
		}
	}()
	if work.finish != nil {
		work.finish()
	}
}
