package client

import (
	"errors"
	"fmt"
	"log"
	"sync"
)

var (
	ErrOptimisticClosed  = errors.New("nextgo: optimistic state is closed")
	ErrOptimisticSettled = errors.New("nextgo: optimistic intent is already settled or reset")
	ErrOptimisticLimit   = errors.New("nextgo: optimistic intent limit reached")
)

const maxOptimisticIntents = 1024
const maxOptimisticObservers = 256

// OptimisticOptions controls a bounded optimistic ledger. State and actions
// must be immutable values; supply clone functions for reference-containing
// values. Reducers and clones must be pure, return promptly, and never call the
// model. Observer errors are isolated; the default error handler logs them.
type OptimisticOptions[T, A any] struct {
	Limit           int // zero selects 64; accepted range is 1..1024
	CloneValue      func(T) T
	CloneAction     func(A) A
	OnObserverError func(error)
}

type OptimisticSnapshot[T any] struct {
	Value     T
	Pending   int // unresolved intents, including those awaiting transport
	Available int // remaining ledger capacity, including buffered confirmations
	Closed    bool
}

type optimisticEntry[A any] struct {
	id     uint64
	action A
	status uint8 // 0 pending, 1 confirmed, 2 rejected
}
type optimisticObserver[T any] struct {
	fn      func(OptimisticSnapshot[T])
	version uint64
}

// Optimistic derives a view by reducing pending and confirmed intents in start
// order over a confirmed prefix. Out-of-order confirmations remain buffered
// until preceding intents settle. The reducer must apply an operation, rather
// than an unversioned full server snapshot that may include other intents.
// Methods are safe for concurrent callers; observers run serially outside the
// lock. Concurrent/reentrant changes may coalesce to the latest snapshot.
type Optimistic[T, A any] struct {
	mu                          sync.Mutex
	base, value                 T
	reduce                      func(T, A) T
	options                     OptimisticOptions[T, A]
	entries                     []optimisticEntry[A]
	next, version, nextObserver uint64
	pending                     int
	closed, publishing          bool
	observers                   map[uint64]*optimisticObserver[T]
}

func NewOptimistic[T, A any](initial T, reduce func(T, A) T, options ...OptimisticOptions[T, A]) (*Optimistic[T, A], error) {
	if reduce == nil || len(options) > 1 {
		return nil, fmt.Errorf("nextgo: optimistic state requires a reducer and at most one options value")
	}
	var opts OptimisticOptions[T, A]
	if len(options) == 1 {
		opts = options[0]
	}
	if opts.Limit == 0 {
		opts.Limit = 64
	}
	if opts.Limit < 1 || opts.Limit > maxOptimisticIntents {
		return nil, fmt.Errorf("nextgo: optimistic limit must be 1..%d", maxOptimisticIntents)
	}
	if opts.CloneValue == nil {
		opts.CloneValue = func(v T) T { return v }
	}
	if opts.CloneAction == nil {
		opts.CloneAction = func(v A) A { return v }
	}
	if opts.OnObserverError == nil {
		opts.OnObserverError = func(err error) { log.Print(err) }
	}
	m := &Optimistic[T, A]{reduce: reduce, options: opts, version: 1, observers: map[uint64]*optimisticObserver[T]{}}
	if err := optimisticGuard(func() { m.base = opts.CloneValue(initial); m.value = opts.CloneValue(m.base) }); err != nil {
		return nil, err
	}
	return m, nil
}

// OptimisticIntent owns one tentative operation. Resolve replaces its action
// with a canonical accepted operation; Commit accepts its original operation.
// Reject removes it. Repeated/late settlements return ErrOptimisticSettled.
type OptimisticIntent[T, A any] struct {
	model *Optimistic[T, A]
	id    uint64
}

func (p *OptimisticIntent[T, A]) ID() uint64    { return p.id }
func (p *OptimisticIntent[T, A]) Commit() error { return p.model.settle(p.id, nil, true) }
func (p *OptimisticIntent[T, A]) Resolve(accepted A) error {
	return p.model.settle(p.id, &accepted, true)
}
func (p *OptimisticIntent[T, A]) Reject() error { return p.model.settle(p.id, nil, false) }

// Begin updates derived state synchronously before network work starts.
// Observer delivery follows the serialized/coalescing Subscribe contract.
func (m *Optimistic[T, A]) Begin(action A) (*OptimisticIntent[T, A], error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrOptimisticClosed
	}
	if len(m.entries) >= m.options.Limit {
		m.mu.Unlock()
		return nil, ErrOptimisticLimit
	}
	if m.next == ^uint64(0) {
		m.mu.Unlock()
		return nil, fmt.Errorf("nextgo: optimistic intent identifiers exhausted")
	}
	id := m.next + 1
	candidate := append([]optimisticEntry[A](nil), m.entries...)
	var err error
	err = optimisticGuard(func() {
		candidate = append(candidate, optimisticEntry[A]{id: id, action: m.options.CloneAction(action)})
	})
	if err == nil {
		err = m.install(candidate)
	}
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.next = id
	m.mu.Unlock()
	m.publish()
	return &OptimisticIntent[T, A]{model: m, id: id}, nil
}

func (m *Optimistic[T, A]) settle(id uint64, accepted *A, confirm bool) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrOptimisticClosed
	}
	index := -1
	for i, e := range m.entries {
		if e.id == id && e.status == 0 {
			index = i
			break
		}
	}
	if index < 0 {
		m.mu.Unlock()
		return ErrOptimisticSettled
	}
	candidate := append([]optimisticEntry[A](nil), m.entries...)
	err := optimisticGuard(func() {
		if confirm {
			candidate[index].status = 1
			if accepted != nil {
				candidate[index].action = m.options.CloneAction(*accepted)
			}
		} else {
			candidate[index].status = 2
			var zero A
			candidate[index].action = zero
		}
	})
	if err == nil {
		err = m.install(candidate)
	}
	m.mu.Unlock()
	if err == nil {
		m.publish()
	}
	return err
}

// install computes everything before publishing state, so a reducer/clone panic
// leaves the previous ledger intact. The caller holds the model lock.
func (m *Optimistic[T, A]) install(candidate []optimisticEntry[A]) error {
	var base, value T
	start, pending := 0, 0
	err := optimisticGuard(func() {
		base = m.options.CloneValue(m.base)
		for start < len(candidate) && candidate[start].status != 0 {
			if candidate[start].status == 1 {
				base = m.reduce(m.options.CloneValue(base), m.options.CloneAction(candidate[start].action))
			}
			start++
		}
		value = m.options.CloneValue(base)
		for _, e := range candidate[start:] {
			if e.status == 0 {
				pending++
			}
			if e.status != 2 {
				value = m.reduce(m.options.CloneValue(value), m.options.CloneAction(e.action))
			}
		}
	})
	if err != nil {
		return err
	}
	m.base, m.value, m.pending = base, value, pending
	// Copy the live tail so accepted/rejected prefix actions can be collected.
	m.entries = nil
	for _, entry := range candidate[start:] {
		if entry.status != 2 {
			m.entries = append(m.entries, entry)
		}
	}
	m.version++
	return nil
}

// Reset installs a fresh authoritative value and invalidates every old intent.
// It does not cancel their transports or roll back server-side mutations.
func (m *Optimistic[T, A]) Reset(value T) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return ErrOptimisticClosed
	}
	var base, next T
	err := optimisticGuard(func() { base = m.options.CloneValue(value); next = m.options.CloneValue(base) })
	if err == nil {
		m.base, m.value = base, next
		m.entries = nil
		m.pending = 0
		m.version++
	}
	m.mu.Unlock()
	if err == nil {
		m.publish()
	}
	return err
}

func (m *Optimistic[T, A]) Snapshot() OptimisticSnapshot[T] {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked()
}
func (m *Optimistic[T, A]) snapshotLocked() OptimisticSnapshot[T] {
	if m.closed {
		return OptimisticSnapshot[T]{Closed: true}
	}
	return OptimisticSnapshot[T]{Value: m.options.CloneValue(m.value), Pending: m.pending, Available: m.options.Limit - len(m.entries)}
}

// Subscribe synchronizes before returning unless a notification is already in
// progress. In that case it receives the latest snapshot after that callback.
// Unsubscription is idempotent; a callback already dispatched may finish.
func (m *Optimistic[T, A]) Subscribe(fn func(OptimisticSnapshot[T])) (func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrOptimisticClosed
	}
	if fn == nil || len(m.observers) >= maxOptimisticObservers || m.nextObserver == ^uint64(0) {
		m.mu.Unlock()
		return nil, fmt.Errorf("nextgo: invalid or excessive optimistic observers")
	}
	m.nextObserver++
	id := m.nextObserver
	m.observers[id] = &optimisticObserver[T]{fn: fn, version: m.version - 1}
	m.mu.Unlock()
	m.publish()
	return func() { m.mu.Lock(); delete(m.observers, id); m.mu.Unlock() }, nil
}

func (m *Optimistic[T, A]) publish() {
	m.mu.Lock()
	if m.publishing || m.closed {
		m.mu.Unlock()
		return
	}
	m.publishing = true
	for {
		var observer *optimisticObserver[T]
		for _, value := range m.observers {
			if value.version != m.version {
				observer = value
				break
			}
		}
		if observer == nil || m.closed {
			m.publishing = false
			m.mu.Unlock()
			return
		}
		observer.version = m.version
		var snapshot OptimisticSnapshot[T]
		err := optimisticGuard(func() { snapshot = m.snapshotLocked() })
		m.mu.Unlock()
		if err == nil {
			err = optimisticGuard(func() { observer.fn(snapshot) })
		}
		if err != nil {
			// A failing error handler must not strand the notification loop.
			if failure := optimisticGuard(func() { m.options.OnObserverError(err) }); failure != nil {
				log.Print(failure)
			}
		}
		m.mu.Lock()
	}
}

// Close drops state, intents and observers without invoking callbacks, suitable
// for island disposal. Future mutations fail; the closed snapshot has zero data.
func (m *Optimistic[T, A]) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	var zero T
	m.base, m.value = zero, zero
	m.entries = nil
	m.pending = 0
	m.observers = nil
}

func optimisticGuard(fn func()) (err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: optimistic callback failed: %v", failure)
		}
	}()
	fn()
	return nil
}
