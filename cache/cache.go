// Package cache provides an explicitly keyed, bounded in-memory data cache.
//
// It never caches HTTP requests automatically. Keys must include every input
// which can affect a value, including tenant or user identity when appropriate.
// Values are shared by reference: callers must treat stored values as immutable
// or arrange their own copying. Capacity bounds the number of stored entries,
// not their size in bytes unless WithMaxBytes is configured. The cache is local
// to a single process.
package cache

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

// Options controls one load. Choose TTL or Lifetime. Ages start when the loader
// completes successfully. Without Lifetime, a non-positive TTL shares concurrent
// loads but does not retain their result.
// Tags and Paths are optional invalidation labels. Paths are URL paths; query
// strings, fragments, duplicate slashes, and trailing slashes are ignored.
// All callers of a key should use the same options and loader: the first caller
// controls a shared in-progress load, and cache hits do not change its policy.
type Options struct {
	TTL             time.Duration
	Lifetime        *Lifetime
	ScheduleRefresh RefreshScheduler
	// KeepStaleOnError can discard the last good entry on a refresh failure.
	// Nil keeps it until hard expiry. Errors themselves are never cached.
	KeepStaleOnError func(error) bool
	Tags             []string
	Paths            []string
}

// Loader computes a value. Its context retains the initiating caller's context
// values, but its deadline and cancellation are detached. It is canceled when
// every waiting caller cancels or strict expiration retires the load. A scheduled
// refresh instead belongs to its bounded scheduler context, even without readers.
// A loader
// must respect context cancellation. Loaders should be read-only/idempotent:
// expiration can retry them, and a canceled loader may briefly overlap its
// replacement. Commit writes outside the loader before expiring its key/labels.
type Loader func(context.Context) (any, error)

// StoreOption configures a Store at construction.
type StoreOption func(*Store)

// WithClock supplies a clock for expiration checks. It must be concurrency safe
// and should be monotonic. The default is time.Now.
func WithClock(now func() time.Time) StoreOption {
	return func(s *Store) {
		if now == nil {
			panic("cache: nil clock")
		}
		s.now = now
	}
}

// WithMaxConcurrentLoads limits loader execution across distinct keys. Pending
// callers still wait with their own cancellation contexts. The default imposes
// no limit. This bounds active loaders, not incoming requests. When a limit is
// configured, loaders must not recursively call this Store for uncached keys:
// all slots could be occupied by loaders waiting for other slots.
func WithMaxConcurrentLoads(limit int) StoreOption {
	return func(s *Store) {
		if limit <= 0 {
			panic("cache: loader concurrency must be positive")
		}
		s.slots = make(chan struct{}, limit)
	}
}

// Sizer supplies an estimate of retained payload bytes. Include all reachable
// data owned by the value; estimates do not measure the Go heap. The method must
// be concurrency safe and nonblocking. A negative size disables retention.
type Sizer interface{ CacheSize() int64 }

// WithMaxBytes adds a byte-accounting LRU budget alongside the entry limit.
// weigh receives the original value, including when using typed GetOrLoad. A
// nil weigh uses Sizer, strings and []byte; unsupported values are not retained.
// Key/label bytes and conservative entry overhead are added automatically.
// Oversized/negative weights and recovered weigher panics skip retention while
// returning the successfully loaded value. Weighing happens outside Store locks.
// This limits accounted retained data, not transient loaders, total heap usage,
// or data omitted by a custom weigher. Values must remain immutable after load.
func WithMaxBytes(limit int64, weigh func(any) int64) StoreOption {
	if weigh == nil {
		weigh = defaultWeight
	}
	return func(s *Store) {
		if limit <= 0 {
			panic("cache: byte budget must be positive")
		}
		s.maxBytes = limit
		s.weigh = weigh
	}
}
func defaultWeight(value any) int64 {
	switch value := value.(type) {
	case Sizer:
		return value.CacheSize()
	case string:
		return int64(len(value))
	case []byte:
		return int64(cap(value))
	default:
		return -1
	}
}

type entry struct {
	key        string
	cost       int64
	value      any
	loadedAt   time.Time
	expiresAt  time.Time
	noExpiry   bool
	freshUntil time.Time
	refresh    *refreshPlan
	tags       map[string]struct{}
	paths      map[string]struct{}
}

func (e *entry) unexpired(now time.Time) bool { return e.noExpiry || now.Before(e.expiresAt) }

type flight struct {
	done       chan struct{}
	expired    chan struct{}
	cancel     context.CancelFunc
	waiters    int
	value      any
	loadedAt   time.Time
	err        error
	options    Options
	tags       map[string]struct{}
	paths      map[string]struct{}
	background bool
}

// Store is safe for concurrent use. Construct it with New; its zero value is
// not usable. Invalidate methods detach matching in-progress loads: existing
// waiters can still receive their results, but those results cannot repopulate
// the cache, and subsequent callers start a new load. Expire methods also cancel
// those loads and retry outstanding readers before delivering a result.
type Store struct {
	// used prevents late build-seed import after reads or invalidation.
	used     bool
	mu       sync.Mutex
	capacity int
	now      func() time.Time
	entries  map[string]*list.Element
	lru      *list.List
	flights  map[string]*flight
	// Track waiters until result delivery, including completed/detached flights.
	// Also track running background loads with no readers: weak invalidation can
	// detach them before a later strict expiration needs to cancel their work.
	awaiting map[*flight]string
	slots    chan struct{}
	maxBytes int64
	bytes    int64
	weigh    func(any) int64
}

// New returns an empty Store. It panics if capacity is not positive. Options
// apply before the store is used and must not be modified afterward.
func New(capacity int, options ...StoreOption) *Store {
	if capacity <= 0 {
		panic("cache: capacity must be positive")
	}
	s := &Store{
		capacity: capacity,
		now:      time.Now,
		entries:  make(map[string]*list.Element),
		lru:      list.New(),
		flights:  make(map[string]*flight),
		awaiting: make(map[*flight]string),
	}
	for _, option := range options {
		option(s)
	}
	return s
}

// GetOrLoad returns a cached value or coalesces concurrent calls for the same
// key. Errors (including loader panics) are never cached. Canceling one caller
// cannot cancel a load needed by other waiters. If all waiters cancel, a later
// caller starts a fresh load. A canceled context never returns a cached value.
// Strict expiration transparently retries outstanding readers with their own
// loader/options. Result delivery is linearized against expiration under the
// Store lock; an already delivered result cannot be recalled.
func (s *Store) GetOrLoad(ctx context.Context, key string, options Options, loader Loader) (any, error) {
	value, _, err := s.Lookup(ctx, key, options, loader)
	return value, err
}

// Lookup is GetOrLoad with generation age/freshness information selected under
// the same lock as its value. Failed reads return zero Info.
func (s *Store) Lookup(ctx context.Context, key string, options Options, loader Loader) (any, Info, error) {
	if err := ctx.Err(); err != nil {
		return nil, Info{}, err
	}
	if loader == nil {
		return nil, Info{}, errors.New("cache: nil loader")
	}
	var err error
	options, err = normalizeOptions(options)
	if err != nil {
		return nil, Info{}, err
	}
	if Bypassed(ctx) {
		return s.bypass(ctx, loader)
	}
	for {
		value, info, err, retry := s.getOrLoad(ctx, key, options, loader)
		if !retry {
			return value, info, err
		}
		if err := ctx.Err(); err != nil {
			return nil, Info{}, err
		}
	}
}

func (s *Store) getOrLoad(ctx context.Context, key string, options Options, loader Loader) (any, Info, error, bool) {
	s.mu.Lock()
	s.used = true
	now := s.now()
	if elem, ok := s.entries[key]; ok {
		e := elem.Value.(*entry)
		info := readInfo(now, e.loadedAt, e.freshUntil, e.expiresAt, Fresh, e.noExpiry)
		if e.unexpired(now) {
			if !now.Before(e.freshUntil) {
				info.State = Stale
				if options.ScheduleRefresh == nil {
					// Without lifecycle scheduling, refresh synchronously. Keep the
					// old entry until success/error policy or hard expiry decides it.
					goto load
				}
				var plan *refreshPlan
				if e.refresh == nil && s.flights[key] == nil {
					plan = &refreshPlan{}
					e.refresh = plan
				}
				s.lru.MoveToFront(elem)
				s.mu.Unlock()
				if plan != nil {
					if err := s.planRefresh(key, plan, options, loader); err != nil {
						// A rejected reservation cannot silently disable refresh.
						// Retry as a blocking reader with its original cancellation.
						options.ScheduleRefresh = nil
						return s.getOrLoad(ctx, key, options, loader)
					}
				}
				if err := ctx.Err(); err != nil {
					return nil, Info{}, err, false
				}
				return e.value, info, nil, false
			}
			s.lru.MoveToFront(elem)
			s.mu.Unlock()
			if err := ctx.Err(); err != nil {
				return nil, Info{}, err, false
			}
			return e.value, info, nil, false
		}
		s.remove(elem)
	}
load:
	f := s.flights[key]
	if f == nil {
		loadCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		f = s.newFlightLocked(key, options, cancel)
		go s.load(loadCtx, key, f, loader)
	}
	f.waiters++
	s.awaiting[f] = key
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		s.releaseWaiter(key, f)
		return nil, Info{}, ctx.Err(), false
	case <-f.expired:
		s.releaseWaiter(key, f)
		return nil, Info{}, nil, true
	case <-f.done:
		if err := ctx.Err(); err != nil {
			s.releaseWaiter(key, f)
			return nil, Info{}, err, false
		}
		// Linearize result delivery against expiration. done may already have
		// closed when a writer expires the key; those unconsumed results must
		// still retry. Holding the lock also keeps the awaiting index accurate.
		s.mu.Lock()
		retry := false
		select {
		case <-f.expired:
			retry = true
		default:
		}
		s.releaseWaiterLocked(key, f)
		info := readInfo(s.now(), f.loadedAt, s.freshUntil(f.loadedAt, f.options), f.options.expiresAt(f.loadedAt), Miss, f.options.noExpiry())
		s.mu.Unlock()
		if retry {
			return nil, Info{}, nil, true
		}
		if f.err != nil {
			return f.value, Info{}, f.err, false
		}
		return f.value, info, nil, false
	}
}

func (s *Store) newFlightLocked(key string, options Options, cancel context.CancelFunc) *flight {
	f := &flight{done: make(chan struct{}), expired: make(chan struct{}), cancel: cancel, options: options, tags: labels(options.Tags, false), paths: labels(options.Paths, true)}
	// Functions/options belong only to an active load, never to a retained entry.
	f.options.Tags, f.options.Paths, f.options.ScheduleRefresh = nil, nil, nil
	s.flights[key] = f
	return f
}

func (s *Store) releaseWaiter(key string, f *flight) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseWaiterLocked(key, f)
}

func (s *Store) releaseWaiterLocked(key string, f *flight) {
	f.waiters--
	if f.waiters == 0 {
		if f.background {
			select {
			case <-f.done:
				delete(s.awaiting, f)
			default:
				// The scheduler still owns this active generation.
			}
			return
		}
		delete(s.awaiting, f)
		if s.flights[key] == f {
			delete(s.flights, key)
		}
		f.cancel()
	}
}

func (s *Store) load(ctx context.Context, key string, f *flight, loader Loader) {
	defer f.cancel()
	completed := false
	cost := int64(0)
	retainStale := false
	// Finalize even if application code calls runtime.Goexit. Waiters and load
	// slots must never be stranded by a loader or custom weigher exiting early.
	defer func() {
		if !completed {
			f.value = nil
			f.err = errors.New("cache: load exited without returning")
		}
		s.mu.Lock()
		f.loadedAt = s.now()
		// Identity protects against invalidation and replacement flights.
		if s.flights[key] == f {
			delete(s.flights, key)
			if f.err == nil && ctx.Err() == nil && f.options.retains() && cost >= 0 {
				if previous := s.entries[key]; previous != nil {
					s.remove(previous)
				}
				now := f.loadedAt
				e := &entry{key: key, value: f.value, loadedAt: now, expiresAt: f.options.expiresAt(now), noExpiry: f.options.noExpiry(), freshUntil: s.freshUntil(now, f.options), tags: f.tags, paths: f.paths, cost: cost}
				// Prune before adding to avoid accounting overflow even for huge budgets.
				if len(s.entries) >= s.capacity || (s.maxBytes > 0 && s.bytes > s.maxBytes-cost) {
					s.pruneExpired(s.now())
				}
				for len(s.entries) >= s.capacity || (s.maxBytes > 0 && s.bytes > s.maxBytes-cost) {
					s.remove(s.lru.Back())
				}
				s.entries[key] = s.lru.PushFront(e)
				s.bytes += cost
			} else if f.err == nil && ctx.Err() == nil || f.err != nil && !retainStale {
				if previous := s.entries[key]; previous != nil {
					s.remove(previous)
				}
			}
		}
		if f.waiters == 0 {
			delete(s.awaiting, f)
		}
		close(f.done)
		s.mu.Unlock()
	}()
	if s.slots == nil {
		f.value, f.err = invoke(ctx, loader)
	} else {
		func() {
			select {
			case s.slots <- struct{}{}:
				defer func() { <-s.slots }()
				if err := ctx.Err(); err != nil {
					f.err = err
				} else {
					f.value, f.err = invoke(ctx, loader)
				}
			case <-ctx.Done():
				f.err = ctx.Err()
			}
		}()
	}
	if f.err == nil && f.options.retains() && s.maxBytes > 0 {
		cost = s.entryCost(key, f)
	}
	if f.err == nil && ctx.Err() != nil {
		f.err = ctx.Err()
	}
	if f.err != nil {
		retainStale = keepStale(f.options.KeepStaleOnError, f.err)
	}
	completed = true
}

func (s *Store) entryCost(key string, f *flight) (cost int64) {
	cost = -1 // a panicking user weigher disables retention, not the response
	defer func() {
		if recover() != nil {
			cost = -1
		}
	}()
	value := f.value
	if boxed, ok := value.(interface{ cacheValue() any }); ok {
		value = boxed.cacheValue()
	}
	payload := s.weigh(value)
	if payload < 0 || payload > s.maxBytes {
		return -1
	}
	cost = payload
	add := func(n int64) bool {
		if n > s.maxBytes-cost {
			return false
		}
		cost += n
		return true
	}
	if !add(256) || !add(int64(len(key))) {
		return -1
	}
	for label := range f.tags {
		if !add(64) || !add(int64(len(label))) {
			return -1
		}
	}
	for label := range f.paths {
		if !add(64) || !add(int64(len(label))) {
			return -1
		}
	}
	return cost
}

func invoke(ctx context.Context, loader Loader) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = nil
			err = fmt.Errorf("cache: loader panicked: %v", recovered)
		}
	}()
	return loader(ctx)
}

// GetOrLoad is the typed form of Store.GetOrLoad. Using the same key with
// different types returns an error; type identity is part of the key contract.
func GetOrLoad[T any](ctx context.Context, store *Store, key string, options Options, loader func(context.Context) (T, error)) (T, error) {
	value, _, err := Lookup(ctx, store, key, options, loader)
	return value, err
}

// Lookup is the typed, generation-consistent form of Store.Lookup.
func Lookup[T any](ctx context.Context, store *Store, key string, options Options, loader func(context.Context) (T, error)) (T, Info, error) {
	var zero T
	if loader == nil {
		return zero, Info{}, errors.New("cache: nil loader")
	}
	// Boxing also handles a nil interface value, whose direct any assertion
	// would otherwise lose the type T.
	value, info, err := store.Lookup(ctx, key, options, func(ctx context.Context) (any, error) {
		v, err := loader(ctx)
		return typedValue[T]{v}, err
	})
	if err != nil {
		return zero, Info{}, err
	}
	boxed, ok := value.(typedValue[T])
	if !ok {
		return zero, Info{}, fmt.Errorf("cache: inconsistent value type for key %q", key)
	}
	return boxed.value, info, nil
}

type typedValue[T any] struct{ value T }

func (v typedValue[T]) cacheValue() any { return v.value }

// InvalidateKey removes key and detaches its in-progress load, if any. It
// returns the number of distinct affected keys (zero or one).
func (s *Store) InvalidateKey(key string) int {
	return s.invalidate(func(candidate string, _, _ map[string]struct{}) bool { return candidate == key }, false)
}

// InvalidateTag removes every entry or in-progress load with the exact tag.
// It returns the number of distinct affected keys.
func (s *Store) InvalidateTag(tag string) int {
	return s.invalidate(func(_ string, tags, _ map[string]struct{}) bool { _, ok := tags[tag]; return ok }, false)
}

// InvalidatePath removes entries labeled with the normalized URL path.
func (s *Store) InvalidatePath(value string) int {
	value = cleanPath(value)
	return s.invalidate(func(_ string, _, paths map[string]struct{}) bool { _, ok := paths[value]; return ok }, false)
}

// InvalidatePathPrefix removes entries labeled with a path at or beneath
// prefix. For example /posts matches /posts and /posts/a, but not /posts-old.
func (s *Store) InvalidatePathPrefix(prefix string) int {
	prefix = cleanPath(prefix)
	return s.invalidate(pathPrefixMatch(prefix), false)
}

func pathPrefixMatch(prefix string) func(string, map[string]struct{}, map[string]struct{}) bool {
	return func(_ string, _, paths map[string]struct{}) bool {
		for value := range paths {
			if prefix == "/" || value == prefix || strings.HasPrefix(value, prefix+"/") {
				return true
			}
		}
		return false
	}
}

// Clear removes all stored values and detaches all in-progress loads.
func (s *Store) Clear() {
	s.invalidate(func(string, map[string]struct{}, map[string]struct{}) bool { return true }, false)
}

// ExpireKey removes a key and cancels matching loads. Callers still waiting for
// their result retry their own loader in a fresh generation instead of receiving
// the expired value/error. Publish the backing-data write before expiration.
// Reads selected for delivery before expiration cannot be recalled and may
// finish afterward. This is a
// process-local guarantee; coordinate external stores/instances separately.
func (s *Store) ExpireKey(key string) int {
	return s.invalidate(func(candidate string, _, _ map[string]struct{}) bool { return candidate == key }, true)
}

// ExpireTag is ExpireKey for every entry/load carrying the exact tag.
func (s *Store) ExpireTag(tag string) int {
	return s.invalidate(func(_ string, tags, _ map[string]struct{}) bool { _, ok := tags[tag]; return ok }, true)
}

// ExpirePath expires entries/loads labeled with the normalized exact URL path.
func (s *Store) ExpirePath(value string) int {
	value = cleanPath(value)
	return s.invalidate(func(_ string, _, paths map[string]struct{}) bool { _, ok := paths[value]; return ok }, true)
}

// ExpirePathPrefix expires entries/loads at or beneath the normalized prefix.
func (s *Store) ExpirePathPrefix(prefix string) int {
	return s.invalidate(pathPrefixMatch(cleanPath(prefix)), true)
}

// ExpireAll expires stored values and every result still awaited by a caller.
func (s *Store) ExpireAll() {
	s.invalidate(func(string, map[string]struct{}, map[string]struct{}) bool { return true }, true)
}

func (s *Store) invalidate(match func(string, map[string]struct{}, map[string]struct{}) bool, strict bool) int {
	return s.changeEntries(match, strict, nil)
}

func (s *Store) changeEntries(match func(string, map[string]struct{}, map[string]struct{}) bool, strict bool, staleWindow *time.Duration) int {
	s.mu.Lock()
	s.used = true
	defer s.mu.Unlock()
	now := s.now()
	affected := make(map[string]struct{})
	for key, elem := range s.entries {
		e := elem.Value.(*entry)
		if match(key, e.tags, e.paths) {
			if staleWindow != nil && *staleWindow != 0 && e.unexpired(now) {
				e.freshUntil = now
				if *staleWindow != Never {
					if limit := now.Add(*staleWindow); e.noExpiry || limit.Before(e.expiresAt) {
						e.expiresAt = limit
						e.noExpiry = false
					}
				}
				// A queued plan belongs to the previous data generation. Its
				// callback will no-op; a later read can reserve a new refresh.
				e.refresh = nil
			} else {
				s.remove(elem)
			}
			affected[key] = struct{}{}
		}
	}
	for key, f := range s.flights {
		if match(key, f.tags, f.paths) {
			delete(s.flights, key)
			if strict {
				close(f.expired)
				f.cancel()
			}
			affected[key] = struct{}{}
		}
	}
	if strict {
		for f, key := range s.awaiting {
			if !match(key, f.tags, f.paths) {
				continue
			}
			select {
			case <-f.expired:
				continue
			default:
				close(f.expired)
			}
			if s.flights[key] == f {
				delete(s.flights, key)
			}
			f.cancel()
			affected[key] = struct{}{}
		}
	}
	return len(affected)
}

// Len returns the number of unexpired stored entries, pruning expired values.
// It does not count in-progress loads.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpired(s.now())
	return len(s.entries)
}

// Bytes returns accounted unexpired retained bytes. It is zero when no byte
// budget is configured and excludes active loads and external/shared references.
func (s *Store) Bytes() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneExpired(s.now())
	return s.bytes
}

func (s *Store) pruneExpired(now time.Time) {
	for _, elem := range s.entries {
		if !elem.Value.(*entry).unexpired(now) {
			s.remove(elem)
		}
	}
}

func (s *Store) remove(elem *list.Element) {
	e := elem.Value.(*entry)
	s.bytes -= e.cost
	delete(s.entries, e.key)
	s.lru.Remove(elem)
}

func labels(values []string, paths bool) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if paths {
			value = cleanPath(value)
		}
		result[value] = struct{}{}
	}
	return result
}

func cleanPath(value string) string {
	value, _, _ = strings.Cut(value, "?")
	value, _, _ = strings.Cut(value, "#")
	return path.Clean("/" + strings.TrimPrefix(value, "/"))
}
