//go:build js && wasm

package client

import (
	"context"
	"fmt"

	"github.com/Suhaibinator/Next.go/scripts"
)

// WidgetOptions adapts a trusted imperative browser library. Dependencies load
// in declaration order using the document script loader. Mount receives an
// exclusive child scope with an empty host. Register each resource's destructor
// with that scope's Cleanup immediately after acquiring it.
type WidgetOptions struct {
	Scripts []scripts.Script
	// Modules load after Scripts, before the fallback is detached. Mount can
	// call ImportModule again to read the already-cached export namespace.
	Modules []scripts.Module
	// RetryFailedScripts retries failed document downloads, never successful ones.
	RetryFailedScripts bool
	Mount              func(context.Context, *Scope) error
	Ready              func(error)
}

// Widget owns an opaque library subtree. Closing it cancels startup, disposes
// registered resources and restores the exact original SSR fallback nodes.
// Library code must observe its context and keep mutations inside its host;
// this is lifecycle ownership, not a JavaScript sandbox.
type Widget struct {
	scope    *Scope
	cancel   context.CancelFunc
	ready    bool
	starting bool
	settled  chan struct{}
}

// Bounds apply per Go/WASM runtime, including startup workers which continue
// after cancellation. A noncooperative factory retains its physical slot until
// it returns; closing/retrying cannot manufacture new startup capacity.
var liveWidgets, widgetWorkers int
var widgetOwners []*Widget

const maxLiveWidgets, maxWidgetWorkers = 128, 64

func (w *Widget) Close() {
	if w == nil {
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
	if w.scope != nil {
		w.scope.dispose()
	}
}

// Ready is false before startup and as soon as any ancestor starts closing.
func (w *Widget) Ready() bool {
	return w != nil && w.ready && w.scope.active()
}

// Settled closes after startup and any cancellation compensation finish. After
// Close, wait for it before reusing the host; a late constructor retains its
// ownership and startup slot until it returns. A successful live widget may
// already be settled. The zero value is settled.
func (w *Widget) Settled() <-chan struct{} {
	if w != nil && w.settled != nil {
		return w.settled
	}
	done := make(chan struct{})
	close(done)
	return done
}

func (w *Widget) releaseOwnership() {
	if w.starting || !w.scope.closed {
		return
	}
	for i, owner := range widgetOwners {
		if owner == w {
			copy(widgetOwners[i:], widgetOwners[i+1:])
			widgetOwners[len(widgetOwners)-1] = nil
			widgetOwners = widgetOwners[:len(widgetOwners)-1]
			break
		}
	}
}

// Widget reserves an owned, connected descendant host before starting any
// asynchronous work. Plain validated fallback content remains visible while
// scripts load. Nested islands/framework-managed trees and overlapping owners
// are rejected. Ready runs once after success or startup failure while the
// parent remains active; cancellation/unmount suppresses it.
func (s *Scope) Widget(host Element, options WidgetOptions) (*Widget, error) {
	if !s.active() {
		return nil, ErrLifetimeClosed
	}
	if options.Mount == nil || options.Ready == nil || len(options.Scripts)+len(options.Modules) > 16 {
		return nil, fmt.Errorf("nextgo: widget requires mount/ready callbacks and at most 16 dependencies")
	}
	if liveWidgets >= maxLiveWidgets || widgetWorkers >= maxWidgetWorkers {
		return nil, fmt.Errorf("nextgo: widget capacity exceeded")
	}
	dependencies := make([]scripts.Script, len(options.Scripts))
	for i, input := range options.Scripts {
		checked, err := input.Check()
		if err != nil {
			return nil, err
		}
		if checked.Strategy == scripts.BeforeInteractive {
			return nil, fmt.Errorf("nextgo: early widget dependencies belong in Config.EarlyScripts")
		}
		dependencies[i] = checked
	}
	options.Scripts = nil
	modules := make([]scripts.Module, len(options.Modules))
	for i, module := range options.Modules {
		checked, err := module.Check()
		if err != nil {
			return nil, err
		}
		modules[i] = checked
	}
	options.Modules = nil
	child, stop, err := s.Subscope(host)
	if err != nil {
		return nil, err
	}
	for _, owner := range widgetOwners {
		other := owner.scope.Root.Value
		if other.Call("contains", host.Value).Bool() || host.Value.Call("contains", other).Bool() {
			stop()
			return nil, fmt.Errorf("nextgo: widget host overlaps an active or pending owner")
		}
	}
	if !host.Value.Get("isConnected").Bool() {
		stop()
		return nil, fmt.Errorf("nextgo: widget host must be connected")
	}
	if err := validateClientFallback(host.Value); err != nil {
		stop()
		return nil, fmt.Errorf("nextgo: invalid widget fallback: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &Widget{scope: child, cancel: cancel, starting: true, settled: make(chan struct{})}
	child.startupRootHeld = true
	widgetOwners = append(widgetOwners, w)
	liveWidgets++
	child.cleanupFramework(func() {
		cancel()
		w.ready = false
		liveWidgets--
		w.releaseOwnership()
	})
	// Account physical startup in the parent as well as the runtime-wide bound.
	// Disposing the child must not release a still-running constructor's slot.
	widgetWorkers++
	err = s.Go(func(parentContext context.Context) {
		completed, notified := false, false
		var restore func()
		defer func() {
			defer func() {
				w.starting = false
				child.startupRootHeld = false
				if child.closed {
					child.releaseParent()
				}
				w.releaseOwnership()
				widgetWorkers--
				close(w.settled)
			}()
			if !completed {
				wasActive := ctx.Err() == nil && child.active()
				w.Close() // Also handles runtime.Goexit in a user callback.
				if restore != nil {
					restore() // A late constructor may have touched the closed host.
				}
				if wasActive && !notified && s.active() {
					notified = true
					w.notify(options.Ready, fmt.Errorf("nextgo: widget startup did not return"))
				}
			}
		}()
		failure := func(err error) {
			w.Close()
			if s.active() {
				notified = true
				w.notify(options.Ready, err)
			}
			completed = true
		}
		for _, dependency := range dependencies {
			load := LoadScript
			if options.RetryFailedScripts {
				load = RetryScript
			}
			if err := load(ctx, dependency); err != nil {
				if ctx.Err() == nil && child.active() {
					failure(err)
				}
				return
			}
		}
		for _, module := range modules {
			if _, err := ImportModule(ctx, module); err != nil {
				if ctx.Err() == nil && child.active() {
					failure(err)
				}
				return
			}
		}
		if ctx.Err() != nil || parentContext.Err() != nil || !child.active() {
			return
		}
		if !host.Value.Get("isConnected").Bool() || !s.Root.Value.Call("contains", host.Value).Bool() {
			failure(fmt.Errorf("nextgo: widget host lost its owner"))
			return
		}
		if err := validateClientFallback(host.Value); err != nil {
			failure(err)
			return
		}
		fallback := host.Value.Get("ownerDocument").Call("createDocumentFragment")
		var originals []Element
		for host.Value.Get("firstChild").Truthy() {
			originals = append(originals, Element{host.Value.Get("firstChild")})
			fallback.Call("appendChild", host.Value.Get("firstChild"))
		}
		// Constructors register their destructors later, so they run before the
		// fallback is restored. Original nodes retain input values and identity.
		restore = func() {
			for host.Value.Get("firstChild").Truthy() {
				host.Value.Call("removeChild", host.Value.Get("firstChild"))
			}
			for _, node := range originals {
				host.Value.Call("appendChild", node.Value)
			}
		}
		child.cleanupFramework(restore)
		mountErr := mountWidget(ctx, child, options.Mount)
		if ctx.Err() != nil || !child.active() {
			return
		}
		if mountErr == nil && (!host.Value.Get("isConnected").Bool() || !s.Root.Value.Call("contains", host.Value).Bool()) {
			mountErr = fmt.Errorf("nextgo: widget constructor moved its host outside the owner")
		}
		if mountErr != nil {
			failure(mountErr)
			return
		}
		w.ready = true
		notified = true
		w.notify(options.Ready, nil)
		completed = true
	})
	if err != nil {
		widgetWorkers--
		w.starting = false
		child.startupRootHeld = false
		w.Close()
		close(w.settled)
		return nil, err
	}
	return w, nil
}

func mountWidget(ctx context.Context, scope *Scope, mount func(context.Context, *Scope) error) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("nextgo: widget constructor panicked")
		}
	}()
	return mount(ctx, scope)
}

func (w *Widget) notify(callback func(error), err error) {
	defer func() {
		if recover() != nil {
			w.Close()
			report("widget readiness callback failed")
		}
	}()
	callback(err)
}
