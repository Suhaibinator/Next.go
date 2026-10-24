package cache

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Lifetime sets server-side ages measured from successful load completion.
// Revalidate is the fresh window; Expire is the hard maximum age. Between them,
// a read may return the last good value and schedule one coalesced refresh.
// Expire must exceed Revalidate and be positive, or be Never. Zero Revalidate
// is allowed. Never disables hard expiry, not refresh, eviction or invalidation.
// Client/browser freshness is separate and is not configured by this type.
type Lifetime struct{ Revalidate, Expire time.Duration }

// Never disables a Lifetime/Profile's hard expiry or a MarkStale window limit.
// It is not valid for Revalidate or Stale. TTL <= 0 retains its existing
// coalescing-only behavior; use Lifetime{Expire: Never} to retain a value.
const Never time.Duration = -1

func (l Lifetime) Validate() error {
	if l.Revalidate < 0 || l.Expire != Never && (l.Expire <= 0 || l.Expire <= l.Revalidate) {
		return errors.New("cache: lifetime requires 0 <= Revalidate < Expire, or Expire = cache.Never")
	}
	return nil
}

func (o Options) noExpiry() bool { return o.Lifetime != nil && o.Lifetime.Expire == Never }
func (o Options) retains() bool  { return o.TTL > 0 || o.noExpiry() }
func (o Options) expiresAt(now time.Time) time.Time {
	if o.noExpiry() {
		return time.Time{}
	}
	return now.Add(o.TTL)
}

// RefreshScheduler reserves bounded lifecycle work. On success it must call
// finish exactly once after run returns or the job is discarded, including queue
// timeout/shutdown. It must never call run after finish. Registration errors must
// leave neither work nor cleanup pending. run may be synchronous. The work context
// must have a deadline and respect platform shutdown. The Store owns no workers.
type RefreshScheduler func(run func(context.Context) error, finish func()) error

type refreshPlan struct{ token byte }

func normalizeOptions(o Options) (Options, error) {
	if o.Lifetime != nil {
		life := *o.Lifetime
		if o.TTL != 0 {
			return Options{}, errors.New("cache: choose TTL or Lifetime, not both")
		}
		if err := life.Validate(); err != nil {
			return Options{}, err
		}
		o.Lifetime = &life
		o.TTL = life.Expire
	}
	return o, nil
}

func (s *Store) freshUntil(now time.Time, options Options) time.Time {
	if options.Lifetime != nil {
		return now.Add(options.Lifetime.Revalidate)
	}
	return now.Add(options.TTL)
}

// planRefresh runs outside the Store lock: a scheduler may execute immediately.
// Cleanup also covers discarded/never-started work, allowing the next stale read
// to try again instead of leaving an entry permanently marked as refreshing.
func (s *Store) planRefresh(key string, plan *refreshPlan, options Options, loader Loader) error {
	finish := sync.OnceFunc(func() {
		s.mu.Lock()
		if elem := s.entries[key]; elem != nil && elem.Value.(*entry).refresh == plan {
			elem.Value.(*entry).refresh = nil
		}
		s.mu.Unlock()
	})
	err := invokeSchedule(options.ScheduleRefresh, func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("cache: refresh scheduler must provide a deadline")
		}
		s.mu.Lock()
		elem := s.entries[key]
		now := s.now()
		if ctx.Err() != nil || elem == nil || elem.Value.(*entry).refresh != plan || now.Before(elem.Value.(*entry).freshUntil) || !elem.Value.(*entry).unexpired(now) || s.flights[key] != nil {
			s.mu.Unlock()
			return ctx.Err()
		}
		loadCtx, cancel := context.WithCancel(ctx)
		f := s.newFlightLocked(key, options, cancel)
		f.background = true
		s.awaiting[f] = key
		s.mu.Unlock()
		// Run on the scheduler's worker, retaining its deadline/slot/lifecycle.
		s.load(loadCtx, key, f, loader)
		return f.err
	}, finish)
	if err != nil {
		finish()
	}
	return err
}

func invokeSchedule(schedule RefreshScheduler, run func(context.Context) error, finish func()) (err error) {
	completed := false
	defer func() {
		if !completed {
			finish()
			err = errors.New("cache: refresh scheduler exited unexpectedly")
			_ = recover()
		}
	}()
	err = schedule(run, finish)
	completed = true
	return err
}

// A policy can reject retaining a stale snapshot after permanent/auth failures.
// Policy code runs outside Store locks, and a panic rejects stale retention.
func keepStale(policy func(error) bool, err error) (keep bool) {
	if policy == nil {
		return true
	}
	defer func() {
		if recover() != nil {
			keep = false
		}
	}()
	return policy(err)
}
