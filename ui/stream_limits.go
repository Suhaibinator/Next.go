package ui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/Suhaibinator/Next.go/internal/streamguard"
)

var (
	// ErrStreamCapacity means no render slot was available. Admission never queues.
	ErrStreamCapacity = errors.New("ui: concurrent stream capacity exhausted")
	// ErrStreamReentrant prevents a deferred callback from synchronously waiting
	// for another callback on the same limiter while it holds a physical slot.
	// Return nested Defer nodes instead; the renderer schedules them afterward.
	// It also refuses a retained request-Memo dependency waiting for the same
	// physical limiter owned by live metadata work, including old inactive frames.
	ErrStreamReentrant = errors.New("ui: reentrant deferred loader on the same stream limiter")
)

// StreamLimiter bounds admitted renders and actual running deferred callbacks.
// It does not limit callback allocations or forcibly stop application code.
// A canceled callback retains its loader slot until it returns or panics.
// Share one limiter across all routes and locale scopes of an application.
type StreamLimiter struct {
	renders chan struct{}
	loaders chan struct{}
}

type streamLimiterKey struct{}
type streamLoaderKey struct{}
type streamLoaderFrame struct {
	limiter *StreamLimiter
	parent  *streamLoaderFrame
	active  atomic.Bool
}

// NewStreamLimiter requires 1..1024 render slots and 1..4096 loader slots.
// Applications should set a deadline before Begin to bound loader-slot waiting.
func NewStreamLimiter(maxStreams, maxLoaders int) (*StreamLimiter, error) {
	if maxStreams < 1 || maxStreams > 1024 || maxLoaders < 1 || maxLoaders > 4096 {
		return nil, fmt.Errorf("ui: stream limits require 1..1024 streams and 1..4096 loaders")
	}
	return &StreamLimiter{renders: make(chan struct{}, maxStreams), loaders: make(chan struct{}, maxLoaders)}, nil
}

// Begin immediately admits a render or returns ErrStreamCapacity. Pass its
// context to RenderEvents, RenderStream or HTMLContext, and defer release.
// Release is idempotent and cancels the admitted context. It never releases a
// loader slot owned by a callback which has not yet returned.
func (l *StreamLimiter) Begin(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil || l == nil || l.renders == nil || l.loaders == nil {
		return nil, nil, fmt.Errorf("ui: constructed stream limiter and context required")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case l.renders <- struct{}{}:
	default:
		return nil, nil, ErrStreamCapacity
	}
	if err := ctx.Err(); err != nil {
		<-l.renders
		return nil, nil, err
	}
	work, cancel := context.WithCancel(ctx)
	work = context.WithValue(work, streamLimiterKey{}, l)
	var once sync.Once
	return work, func() { once.Do(func() { cancel(); <-l.renders }) }, nil
}

func acquireStreamLoader(ctx context.Context) (context.Context, func(), error) {
	limiter, _ := ctx.Value(streamLimiterKey{}).(*StreamLimiter)
	if limiter == nil {
		return ctx, func() {}, nil
	}
	previous, _ := ctx.Value(streamLoaderKey{}).(*streamLoaderFrame)
	for held := previous; held != nil; held = held.parent {
		if held.limiter == limiter && held.active.Load() {
			return nil, nil, ErrStreamReentrant
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		blocked, changed := streamguard.State(ctx, limiter)
		if blocked {
			return nil, nil, ErrStreamReentrant
		}
		select {
		case limiter.loaders <- struct{}{}:
			if err := ctx.Err(); err != nil {
				<-limiter.loaders
				return nil, nil, err
			}
			if blocked, _ := streamguard.State(ctx, limiter); blocked {
				<-limiter.loaders
				return nil, nil, ErrStreamReentrant
			}
		case <-changed:
			continue
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
		break
	}
	frame := &streamLoaderFrame{limiter: limiter, parent: previous}
	frame.active.Store(true)
	work := streamguard.WithOwner(context.WithValue(ctx, streamLoaderKey{}, frame), limiter)
	return work, func() { frame.active.Store(false); <-limiter.loaders }, nil
}

// RunStreamLoader runs one callback using the same physical slots and reentrancy
// checks as Defer. A canceled caller returns promptly, but the callback keeps its
// slot until it actually returns, panics, or exits. The callback must join any
// framework-owned physical dependencies before returning. Arbitrary application
// goroutines are not tracked or forcibly stopped by this helper.
//
// Pass a context returned by StreamLimiter.Begin to enforce shared application
// limits. Without an attached limiter the behavior matches ordinary Defer: no
// physical admission limit is introduced by this wrapper.
func RunStreamLoader(ctx context.Context, load func(context.Context) error) error {
	if ctx == nil || load == nil {
		return fmt.Errorf("ui: stream loader requires a context and callback")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	work, release, err := acquireStreamLoader(ctx)
	if err != nil {
		return err
	}
	result := make(chan error, 1)
	go func() {
		resultErr := errors.New("ui: stream loader exited without returning")
		defer func() {
			if failure := recover(); failure != nil {
				resultErr = errors.New("ui: deferred loader panic")
			}
			release()
			// A buffered single result cannot strand a sender after HTTP exit.
			result <- resultErr
		}()
		if err := work.Err(); err != nil {
			resultErr = err
			return
		}
		resultErr = load(work)
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-result:
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return err
	}
}
