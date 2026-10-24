package cache

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrAlreadyUsed = errors.New("cache: bootstrap requires an unused store")

// Seed supplies an immutable initial value and its original generation time.
// Bootstrap does not extend freshness to process startup. Normal eviction,
// revalidation, expiration and invalidation apply after publication.
type Seed[T any] struct {
	Key      string
	Value    T
	LoadedAt time.Time
	Options  Options
}

// Bootstrap atomically initializes a new Store once. It never overwrites live
// work or resurrects values after an earlier invalidation, even on an empty
// Store. Admission uses the normal entry/byte budgets; older/oversized entries
// may be dropped. Subsequent misses must call their loader, not reimport seeds.
func Bootstrap[T any](ctx context.Context, s *Store, seeds []Seed[T]) (int, error) {
	if ctx == nil || s == nil || s.now == nil {
		return 0, fmt.Errorf("cache: bootstrap requires a context and constructed store")
	}
	if len(seeds) > 10000 {
		return 0, fmt.Errorf("cache: bootstrap exceeds 10000 entries")
	}
	prepared := make([]*entry, 0, len(seeds))
	keys := map[string]bool{}
	for _, seed := range seeds {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if seed.Key == "" || len(seed.Key) > 16384 || keys[seed.Key] || seed.LoadedAt.IsZero() {
			return 0, fmt.Errorf("cache: invalid or duplicate bootstrap entry")
		}
		keys[seed.Key] = true
		options, err := normalizeOptions(seed.Options)
		if err != nil {
			return 0, err
		}
		if !options.retains() {
			return 0, fmt.Errorf("cache: bootstrap entries require a retained lifetime")
		}
		f := &flight{value: typedValue[T]{seed.Value}, options: options, tags: labels(options.Tags, false), paths: labels(options.Paths, true)}
		cost := int64(0)
		if s.maxBytes > 0 {
			cost = s.entryCost(seed.Key, f)
		}
		if cost < 0 {
			continue
		}
		prepared = append(prepared, &entry{key: seed.Key, value: f.value, loadedAt: seed.LoadedAt, expiresAt: options.expiresAt(seed.LoadedAt), freshUntil: s.freshUntil(seed.LoadedAt, options), noExpiry: options.noExpiry(), tags: f.tags, paths: f.paths, cost: cost})
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.used || len(s.entries) != 0 || len(s.flights) != 0 || len(s.awaiting) != 0 {
		return 0, ErrAlreadyUsed
	}
	s.used = true
	now := s.now()
	for _, e := range prepared {
		if !e.unexpired(now) {
			continue
		}
		for len(s.entries) >= s.capacity || s.maxBytes > 0 && s.bytes > s.maxBytes-e.cost {
			s.remove(s.lru.Back())
		}
		s.entries[e.key] = s.lru.PushFront(e)
		s.bytes += e.cost
	}
	return len(s.entries), nil
}
