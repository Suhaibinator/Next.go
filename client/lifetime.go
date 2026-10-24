package client

import (
	"errors"
	"fmt"
	"log"
	"reflect"
)

var ErrLifetimeClosed = errors.New("nextgo: component lifetime is closed")
var ErrEffectClosed = errors.New("nextgo: effect is closed")

// Lifetime owns explicit effects, contexts and composed component resources.
// Use it on one event loop. It is not a concurrent store or a request context.
type Lifetime struct {
	parent    *Lifetime
	children  []*Lifetime
	effects   []*Effect
	resources []*Resource
	values    map[any]any
	depth     int
	closed    bool
	onError   func(error)
}
type LifetimeOptions struct{ OnError func(error) }

func NewLifetime(options ...LifetimeOptions) *Lifetime {
	l := &Lifetime{}
	if len(options) > 1 {
		panic("nextgo: at most one lifetime options value")
	}
	if len(options) == 1 {
		l.onError = options[0].OnError
	}
	if l.onError == nil {
		l.onError = func(err error) { log.Print(err) }
	}
	return l
}
func (l *Lifetime) Child() (*Lifetime, error) {
	if !l.active() {
		return nil, ErrLifetimeClosed
	}
	if len(l.children) >= 2048 || l.depth >= 32 {
		return nil, fmt.Errorf("nextgo: component lifetime nesting/child limit exceeded")
	}
	child := &Lifetime{parent: l, depth: l.depth + 1, onError: l.onError}
	l.children = append(l.children, child)
	return child, nil
}
func (l *Lifetime) Closed() bool { return !l.active() }
func (l *Lifetime) active() bool {
	if l == nil {
		return false
	}
	for owner := l; owner != nil; owner = owner.parent {
		if owner.closed {
			return false
		}
	}
	return true
}
func (l *Lifetime) report(err error) {
	if err == nil {
		return
	}
	if l.onError == nil {
		log.Print(err)
		return
	}
	if failure := lifetimeGuard(func() { l.onError(err) }); failure != nil {
		log.Print(failure)
	}
}
func lifetimeGuard(fn func()) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: component callback panic (%T)", failure)
		}
	}()
	fn()
	return nil
}
func removeOwned[T comparable](items []T, item T) []T {
	for i, value := range items {
		if value == item {
			copy(items[i:], items[i+1:])
			var zero T
			items[len(items)-1] = zero
			return items[:len(items)-1]
		}
	}
	return items
}

// Close releases children, effects and resources, each in reverse creation order.
// All cleanups run even when one panics. Repeated Close is harmless.
func (l *Lifetime) Close() {
	if l == nil || l.closed {
		return
	}
	l.closed = true
	if l.parent != nil {
		l.parent.children = removeOwned(l.parent.children, l)
		l.parent = nil
	}
	for len(l.children) > 0 {
		l.children[len(l.children)-1].Close()
	}
	for len(l.effects) > 0 {
		l.effects[len(l.effects)-1].Close()
	}
	for len(l.resources) > 0 {
		l.resources[len(l.resources)-1].Close()
	}
	l.values = nil
	l.onError = nil
}

// Resource is an unregistering cleanup handle. Close immediately releases it.
type Resource struct {
	owner   *Lifetime
	cleanup func()
}

func (l *Lifetime) Defer(cleanup func()) (*Resource, error) {
	if !l.active() {
		return nil, ErrLifetimeClosed
	}
	if cleanup == nil || len(l.resources) >= 256 {
		return nil, fmt.Errorf("nextgo: invalid cleanup or component resource limit exceeded")
	}
	resource := &Resource{owner: l, cleanup: cleanup}
	l.resources = append(l.resources, resource)
	return resource, nil
}
func (r *Resource) Close() {
	if r == nil || r.owner == nil {
		return
	}
	owner, cleanup := r.owner, r.cleanup
	r.owner = nil
	r.cleanup = nil
	owner.resources = removeOwned(owner.resources, r)
	if err := lifetimeGuard(cleanup); err != nil {
		owner.report(err)
	}
}

// ContextKey has identity independent of its value type. Keys and values stay
// within one Go runtime; they are not serialized or shared across WASM modules.
type ContextKey[T any] struct{ identity *contextIdentity }
type contextIdentity struct{ marker byte }
type contextBinding[T any] struct{ value T }

func NewContext[T any]() ContextKey[T] { return ContextKey[T]{identity: &contextIdentity{}} }
func Provide[T any](l *Lifetime, key ContextKey[T], value T) error {
	if !l.active() {
		return ErrLifetimeClosed
	}
	if key.identity == nil {
		return fmt.Errorf("nextgo: context key must be created with NewContext")
	}
	if l.values == nil {
		l.values = map[any]any{}
	}
	if _, exists := l.values[key.identity]; !exists && len(l.values) >= 128 {
		return fmt.Errorf("nextgo: component provider limit exceeded")
	}
	l.values[key.identity] = contextBinding[T]{value: value}
	return nil
}
func UseContext[T any](l *Lifetime, key ContextKey[T]) (T, bool) {
	var zero T
	if !l.active() || key.identity == nil {
		return zero, false
	}
	for owner := l; owner != nil; owner = owner.parent {
		if owner.closed {
			return zero, false
		}
		if value, ok := owner.values[key.identity]; ok {
			typed, ok := value.(contextBinding[T])
			return typed.value, ok
		}
	}
	return zero, false
}

// Dependency is an explicit effect source. *State[T] implements it. Reads from
// an effect do not implicitly subscribe; list every state that invalidates it.
type Dependency interface{ subscribeEffect(func()) (func(), error) }
type Effect struct {
	owner                 *Lifetime
	run                   func() (func(), error)
	cleanup               func()
	stops                 []func()
	busy, pending, closed bool
	err                   error
}

func (l *Lifetime) Effect(run func() (func(), error), dependencies ...Dependency) (*Effect, error) {
	if !l.active() {
		return nil, ErrLifetimeClosed
	}
	if run == nil || len(dependencies) > 16 || len(l.effects) >= 128 {
		return nil, fmt.Errorf("nextgo: invalid effect or component effect/dependency limit exceeded")
	}
	effect := &Effect{owner: l, run: run}
	l.effects = append(l.effects, effect)
	seen := map[Dependency]bool{}
	for _, dependency := range dependencies {
		if dependency == nil || !reflect.TypeOf(dependency).Comparable() {
			effect.Close()
			return nil, fmt.Errorf("nextgo: nil effect dependency")
		}
		if seen[dependency] {
			continue
		}
		seen[dependency] = true
		var stop func()
		var err error
		if failure := lifetimeGuard(func() {
			stop, err = dependency.subscribeEffect(func() {
				if err := effect.Invalidate(); err != nil && !errors.Is(err, ErrEffectClosed) {
					l.report(err)
				}
			})
		}); failure != nil {
			effect.Close()
			return nil, failure
		}
		if err != nil {
			effect.Close()
			return nil, err
		}
		effect.stops = append(effect.stops, stop)
	}
	if err := effect.Invalidate(); err != nil {
		return nil, err
	}
	return effect, nil
}
func (e *Effect) Err() error {
	if e == nil {
		return ErrEffectClosed
	}
	return e.err
}

// Invalidate runs immediately, with cleanup before the next setup. Reentrant
// invalidations coalesce and drain at most 64 runs before the effect is closed.
func (e *Effect) Invalidate() (err error) {
	if e == nil || e.closed {
		return ErrEffectClosed
	}
	if !e.owner.active() {
		e.Close()
		return ErrEffectClosed
	}
	e.pending = true
	if e.busy {
		return nil
	}
	e.busy = true
	defer func() {
		e.busy = false
		if err != nil {
			e.err = err
			e.Close()
		}
	}()
	for turns := 0; e.pending && !e.closed; turns++ {
		if turns >= 64 {
			return fmt.Errorf("nextgo: effect exceeded 64 reentrant runs")
		}
		e.pending = false
		cleanup := e.cleanup
		e.cleanup = nil
		if cleanup != nil {
			if err := lifetimeGuard(cleanup); err != nil {
				return err
			}
		}
		if e.closed {
			break
		}
		var next func()
		var failure error
		if err := lifetimeGuard(func() { next, failure = e.run() }); err != nil {
			return err
		}
		if e.closed {
			if next != nil {
				if err := lifetimeGuard(next); err != nil {
					return err
				}
			}
			break
		}
		e.cleanup = next
		if failure != nil {
			return failure
		}
	}
	return nil
}
func (e *Effect) Close() {
	if e == nil || e.closed {
		return
	}
	e.closed = true
	owner := e.owner
	owner.effects = removeOwned(owner.effects, e)
	e.owner = nil
	for _, stop := range e.stops {
		if err := lifetimeGuard(stop); err != nil {
			owner.report(err)
		}
	}
	e.stops = nil
	e.run = nil
	cleanup := e.cleanup
	e.cleanup = nil
	if cleanup != nil {
		if err := lifetimeGuard(cleanup); err != nil {
			owner.report(err)
		}
	}
}
