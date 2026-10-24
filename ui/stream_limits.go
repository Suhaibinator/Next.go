package ui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	// ErrStreamCapacity means no render slot was available. Admission never queues.
	ErrStreamCapacity = errors.New("ui: concurrent stream capacity exhausted")
	// ErrStreamReentrant prevents a deferred callback from synchronously waiting
	// for another callback on the same limiter while it holds a physical slot.
	// Return nested Defer nodes instead; the renderer schedules them afterward.
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
	select {
	case limiter.loaders <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-limiter.loaders
		return nil, nil, err
	}
	frame := &streamLoaderFrame{limiter: limiter, parent: previous}
	frame.active.Store(true)
	work := context.WithValue(ctx, streamLoaderKey{}, frame)
	return work, func() { frame.active.Store(false); <-limiter.loaders }, nil
}
