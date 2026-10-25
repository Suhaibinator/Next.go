package client

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrResourceClosed      = errors.New("nextgo: async resource is closed")
	ErrResourceInvalidated = errors.New("nextgo: async resource was invalidated")
	ErrResourceCapacity    = errors.New("nextgo: async resource capacity exceeded")
	ErrResourceSeed        = errors.New("nextgo: async resource seed was not adopted")
)

// ResourceStoreOptions bounds framework-owned values and admission metadata.
// Zero fields use the documented defaults. An uncooperative loader continues
// owning its physical slot until it returns, even after cancellation.
type ResourceStoreOptions struct {
	MaxSources, MaxEntries, MaxInFlight, MaxWaiters int
	MaxBytes, MaxValueBytes                         int
	Timeout                                         time.Duration
}

// ResourceSourceOptions permanently binds a key, loader and reuse policy to one
// source. Duplicate live keys in a store are rejected, including equal loaders.
// PublicTTL explicitly promises request-independent public output. Zero disables
// both cached reuse and in-flight sharing. Never include private data in public
// sources. There is no global payload store.
type ResourceSourceOptions struct {
	Key       string
	PublicTTL time.Duration
	Load      func(context.Context) ([]byte, error)
}

type ResourceSeed struct {
	Key              string
	Body             []byte
	ExpiresUnixMilli int64
}

type resourceEntry struct {
	data    []byte
	expires time.Time
	used    uint64
	cost    int
}
type resourceFlight struct {
	refreshDone func()
	source      *ResourceSource
	ctx         context.Context
	cancel      context.CancelFunc
	done        chan struct{}
	data        []byte
	err         error
	epoch       uint64
	waiters     int
	notified    bool
}

// ResourceStore is an explicitly owned, concurrency-safe bounded byte store.
// Close it when its owner disappears. Source registration establishes loader
// authority; callers cannot substitute another loader while reading a key.
type ResourceStore struct {
	mu              sync.Mutex
	options         ResourceStoreOptions
	sources         map[string]*ResourceSource
	entries         map[*ResourceSource]resourceEntry
	flights         map[*resourceFlight]struct{}
	shared          map[*ResourceSource]*resourceFlight
	bytes, waiters  int
	sequence, epoch uint64
	closed          bool
	seedAllowed     bool
}
type ResourceSource struct {
	store           *ResourceStore
	key             string
	ttl             time.Duration
	load            func(context.Context) ([]byte, error)
	closed, started bool
	next            int
	observers       map[int]func()
}

func NewResourceStore(options ResourceStoreOptions) (*ResourceStore, error) {
	if options.MaxSources == 0 {
		options.MaxSources = 64
	}
	if options.MaxEntries == 0 {
		options.MaxEntries = 32
	}
	if options.MaxInFlight == 0 {
		options.MaxInFlight = 8
	}
	if options.MaxWaiters == 0 {
		options.MaxWaiters = 128
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 1 << 20
	}
	if options.MaxValueBytes == 0 {
		options.MaxValueBytes = 256 << 10
	}
	if options.Timeout == 0 {
		options.Timeout = 10 * time.Second
	}
	if options.MaxSources < 1 || options.MaxSources > 256 || options.MaxEntries < 1 || options.MaxEntries > 256 || options.MaxInFlight < 1 || options.MaxInFlight > 32 || options.MaxWaiters < 1 || options.MaxWaiters > 512 || options.MaxBytes < 1 || options.MaxBytes > 16<<20 || options.MaxValueBytes < 1 || options.MaxValueBytes > 16<<20 || options.Timeout < time.Millisecond || options.Timeout > time.Minute {
		return nil, fmt.Errorf("nextgo: invalid async resource bounds")
	}
	return &ResourceStore{options: options, seedAllowed: true, sources: map[string]*ResourceSource{}, entries: map[*ResourceSource]resourceEntry{}, flights: map[*resourceFlight]struct{}{}, shared: map[*ResourceSource]*resourceFlight{}}, nil
}
func (s *ResourceStore) Source(options ResourceSourceOptions) (*ResourceSource, error) {
	if s == nil {
		return nil, ErrResourceClosed
	}
	if options.Key == "" || len(options.Key) > 2048 || options.Load == nil || options.PublicTTL < 0 || options.PublicTTL > 5*time.Minute {
		return nil, fmt.Errorf("nextgo: invalid async resource source")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrResourceClosed
	}
	if _, exists := s.sources[options.Key]; exists {
		return nil, fmt.Errorf("nextgo: resource key already has a registered source")
	}
	if len(s.sources) >= s.options.MaxSources {
		return nil, ErrResourceCapacity
	}
	source := &ResourceSource{store: s, key: strings.Clone(options.Key), ttl: options.PublicTTL, load: options.Load, observers: map[int]func(){}}
	s.sources[source.key] = source
	return source, nil
}

// AdoptSeed copies an explicit public SSR seed without issuing a request.
// Adoption is once-only, before the first read, and forbidden after invalidation.
// Expiry is measured from the server timestamp rather than hydration time.
func (r *ResourceSource) AdoptSeed(seed ResourceSeed) error {
	if r == nil || r.store == nil {
		return ErrResourceClosed
	}
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	until := time.UnixMilli(seed.ExpiresUnixMilli)
	if s.closed || r.closed {
		return ErrResourceClosed
	}
	if r.started || !s.seedAllowed || s.epoch != 0 || r.ttl <= 0 || seed.Key != r.key || len(seed.Body) > s.options.MaxValueBytes || len(seed.Body)+len(r.key)+128 > s.options.MaxBytes || !now.Before(until) || until.After(now.Add(r.ttl)) {
		return ErrResourceSeed
	}
	r.started = true
	s.putLocked(r, seed.Body, until)
	return nil
}

func (r *ResourceSource) Read(ctx context.Context) ([]byte, error) {
	if r == nil || r.store == nil {
		return nil, ErrResourceClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("nextgo: resource read requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	s := r.store
	s.mu.Lock()
	if s.closed || r.closed {
		s.mu.Unlock()
		return nil, ErrResourceClosed
	}
	r.started = true
	if entry, ok := s.entries[r]; ok {
		if time.Now().Before(entry.expires) {
			s.sequence++
			entry.used = s.sequence
			s.entries[r] = entry
			data := bytes.Clone(entry.data)
			s.mu.Unlock()
			return data, nil
		}
		s.removeLocked(r)
	}
	if s.waiters >= s.options.MaxWaiters {
		s.mu.Unlock()
		return nil, ErrResourceCapacity
	}
	f := s.shared[r]
	if f == nil {
		if len(s.flights) >= s.options.MaxInFlight {
			s.mu.Unlock()
			return nil, ErrResourceCapacity
		}
		refreshDone, err := beginResourceRefreshActivity()
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		loadCtx, cancel := context.WithTimeout(context.Background(), s.options.Timeout)
		f = &resourceFlight{source: r, ctx: loadCtx, cancel: cancel, done: make(chan struct{}), epoch: s.epoch, refreshDone: refreshDone}
		s.flights[f] = struct{}{}
		if r.ttl > 0 {
			s.shared[r] = f
		}
		go s.run(f, r.load)
	}
	f.waiters++
	s.waiters++
	s.mu.Unlock()
	select {
	case <-f.done:
	case <-ctx.Done():
	case <-f.ctx.Done():
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f.waiters--
	s.waiters--
	if !f.notified && (f.waiters == 0 || context.Cause(f.ctx) != nil) {
		reason := context.Cause(f.ctx)
		if reason == nil {
			reason = context.Canceled
		}
		s.finishLocked(f, nil, reason)
		f.cancel()
	}
	if err := ctx.Err(); err != nil {
		return nil, context.Cause(ctx)
	}
	return bytes.Clone(f.data), f.err
}
func (s *ResourceStore) run(f *resourceFlight, load func(context.Context) ([]byte, error)) {
	if f.refreshDone != nil {
		defer f.refreshDone()
	}
	var data []byte
	var err error
	func() {
		defer func() {
			if recover() != nil {
				err = errors.New("nextgo: resource loader panicked")
			}
		}()
		data, err = load(f.ctx)
	}()
	if err == nil {
		if len(data) > s.options.MaxValueBytes {
			err = ErrResourceCapacity
			data = nil
		} else {
			data = bytes.Clone(data)
		}
	}
	if err != nil {
		data = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.flights, f)
	if err == nil {
		err = context.Cause(f.ctx)
	}
	if !f.notified {
		if s.closed || f.source.closed {
			err = ErrResourceClosed
		} else if f.epoch != s.epoch {
			err = ErrResourceInvalidated
		}
		if err == nil && f.source.ttl > 0 {
			s.putLocked(f.source, data, time.Now().Add(f.source.ttl))
		}
		s.finishLocked(f, data, err)
	}
	f.cancel()
}
func (s *ResourceStore) finishLocked(f *resourceFlight, data []byte, err error) {
	if f.notified {
		return
	}
	f.notified = true
	f.data, f.err = data, err
	if s.shared[f.source] == f {
		delete(s.shared, f.source)
	}
	close(f.done)
}
func (s *ResourceStore) removeLocked(r *ResourceSource) {
	if entry, ok := s.entries[r]; ok {
		s.bytes -= entry.cost
		delete(s.entries, r)
	}
}
func (s *ResourceStore) putLocked(r *ResourceSource, data []byte, until time.Time) {
	s.removeLocked(r)
	cost := len(r.key) + len(data) + 128
	if cost > s.options.MaxBytes {
		return
	}
	for source, entry := range s.entries {
		if !time.Now().Before(entry.expires) {
			s.removeLocked(source)
		}
	}
	for len(s.entries) >= s.options.MaxEntries || s.bytes > s.options.MaxBytes-cost {
		var oldest *ResourceSource
		var used uint64
		for source, entry := range s.entries {
			if oldest == nil || entry.used < used {
				oldest, used = source, entry.used
			}
		}
		s.removeLocked(oldest)
	}
	s.sequence++
	s.entries[r] = resourceEntry{data: bytes.Clone(data), expires: until, used: s.sequence, cost: cost}
	s.bytes += cost
}

// Invalidate drops reuse, cancels active reads and notifies mounted views. It
// starts no request. Late loader completion cannot republish across this fence.
func (s *ResourceStore) Invalidate() { s.invalidate(false) }
func (s *ResourceStore) Close()      { s.invalidate(true) }
func (s *ResourceStore) invalidate(closeStore bool) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.epoch++
	s.closed = closeStore
	s.entries = map[*ResourceSource]resourceEntry{}
	s.bytes = 0
	for f := range s.flights {
		err := ErrResourceInvalidated
		if closeStore {
			err = ErrResourceClosed
		}
		s.finishLocked(f, nil, err)
		f.cancel()
	}
	var callbacks []func()
	for _, source := range s.sources {
		for _, fn := range source.observers {
			callbacks = append(callbacks, fn)
		}
		if closeStore {
			source.closed = true
			source.load = nil
			source.observers = nil
		}
	}
	if closeStore {
		s.sources = nil
	}
	s.mu.Unlock()
	for _, fn := range callbacks {
		fn()
	}
}
func (r *ResourceSource) Close() {
	if r == nil || r.store == nil {
		return
	}
	s := r.store
	s.mu.Lock()
	if r.closed {
		s.mu.Unlock()
		return
	}
	r.closed = true
	r.load = nil
	callbacks := make([]func(), 0, len(r.observers))
	for _, fn := range r.observers {
		callbacks = append(callbacks, fn)
	}
	r.observers = nil
	delete(s.sources, r.key)
	s.removeLocked(r)
	for f := range s.flights {
		if f.source == r {
			s.finishLocked(f, nil, ErrResourceClosed)
			f.cancel()
		}
	}
	s.mu.Unlock()
	for _, fn := range callbacks {
		fn()
	}
}
func (r *ResourceSource) observe(fn func()) (func(), error) {
	if r == nil || r.store == nil {
		return nil, ErrResourceClosed
	}
	s := r.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || r.closed {
		return nil, ErrResourceClosed
	}
	if len(r.observers) >= 128 {
		return nil, ErrResourceCapacity
	}
	r.next++
	id := r.next
	r.observers[id] = fn
	return func() { s.mu.Lock(); delete(r.observers, id); s.mu.Unlock() }, nil
}
