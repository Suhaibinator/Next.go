package shared

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
)

// Options bounds one Lookup, including polling and cooperative loader work.
// Zero values select a 5s lease, 30s total timeout and 25ms poll interval. Leases
// are not renewed. A loader that outlives its lease cannot publish its result.
type Options struct{ Lease, Timeout, PollInterval time.Duration }

// LoadOptions optionally serves an unexpired last-good value while reserving
// one fenced refresh through a bounded lifecycle scheduler. Nil scheduling
// keeps blocking reads. Scheduler rejection falls back to a blocking read.
// KeepStaleOnError defaults to retention until hard expiry. False (or panic)
// discards only the failing owner's generation, never a replacement replica's.
type LoadOptions struct {
	ScheduleRefresh  cache.RefreshScheduler
	KeepStaleOnError func(error) bool
}

// Store binds a Backend to an application/tenant namespace and JSON schema.
// It retains no values or goroutines. Each hit contacts the authoritative backend.
// Multiple stores/processes can share that backend without an invalidation bus.
type Store struct {
	backend           Backend
	namespace, schema string
	options           Options
}

// ScopeID is a stable opaque identity for namespace/schema, not credentials or
// physical endpoints. Use a new namespace/schema for independent data realms.
// Empty denotes a nil or unconstructed Store.
func (s *Store) ScopeID() string {
	if s == nil || s.backend == nil || !validName(s.namespace) || !validName(s.schema) {
		return ""
	}
	digest := sha256.Sum256([]byte("nextgo.shared.scope.v1\x00" + s.namespace + "\x00" + s.schema))
	return hex.EncodeToString(digest[:])
}

func New(backend Backend, namespace, schema string, options Options) (*Store, error) {
	if backend == nil || !validName(namespace) || !validName(schema) {
		return nil, ErrInvalid
	}
	if options.Lease == 0 {
		options.Lease = 5 * time.Second
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	if options.PollInterval == 0 {
		options.PollInterval = 25 * time.Millisecond
	}
	if validateLease(options.Lease) != nil || options.Timeout <= 0 || options.Timeout > 5*time.Minute || options.PollInterval < time.Millisecond || options.PollInterval > time.Second {
		return nil, ErrInvalid
	}
	return &Store{backend: backend, namespace: namespace, schema: schema, options: options}, nil
}

// Expire waits for authoritative namespace-wide invalidation, including pending
// leases and every schema/build. Invoke after the backing write commits. A
// transport error has an unknown outcome; retrying Expire is safe. This does not
// clear browser/CDN caches or other namespaces and is not a write transaction.
func (s *Store) Expire(ctx context.Context, selector Selector) (int, error) {
	if s == nil || ctx == nil {
		return 0, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.options.Timeout)
	defer cancel()
	return s.backend.Expire(ctx, s.namespace, selector)
}

// MarkStale acknowledges a namespace-wide stale mark after a backing commit.
// It retires pre-write leases and never extends a generation's hard deadline.
// Zero strictly expires; Never adds no deadline; finite windows are <=365 days.
func (s *Store) MarkStale(ctx context.Context, selector Selector, window time.Duration) (int, error) {
	if s == nil || ctx == nil || !validWindow(window) {
		return 0, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, s.options.Timeout)
	defer cancel()
	return s.backend.MarkStale(ctx, s.namespace, selector, window)
}

// Lookup serializes successful loads as JSON and decodes an owned result on
// misses and hits. JSON types, not arbitrary Go object identity, are the public
// contract; schema and every key variant are the application's responsibility.
// Stale reads wait for a new generation unless a refresh scheduler is supplied.
// Backend outages fail closed; an unexpired stale value is not an outage cache.
// Caller cancellation releases its own lease; peers retry with their own loader.
// Scheduled work belongs to the caller's bounded scheduler, not hidden workers.
// Loaders must be read-only/idempotent and honor context cancellation: lease
// expiry/invalidation can cause concurrent replacement loads. Bypassed contexts
// run the loader directly without touching the backend or JSON conversion.
func Lookup[T any](ctx context.Context, s *Store, key string, policy Policy, loader func(context.Context) (T, error), loadOptions ...LoadOptions) (T, cache.Info, error) {
	var zero T
	if ctx == nil || s == nil || loader == nil {
		return zero, cache.Info{}, ErrInvalid
	}
	if len(loadOptions) > 1 {
		return zero, cache.Info{}, ErrInvalid
	}
	options := LoadOptions{}
	if len(loadOptions) == 1 {
		options = loadOptions[0]
	}
	id := ID{s.namespace, s.schema, key}
	if err := validateID(id); err != nil {
		return zero, cache.Info{}, err
	}
	p, err := normalize(policy)
	if err != nil {
		return zero, cache.Info{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, s.options.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return zero, cache.Info{}, err
	}
	if cache.Bypassed(ctx) {
		value, err := invokeLoader(ctx, loader)
		if err != nil {
			return zero, cache.Info{}, err
		}
		if err := ctx.Err(); err != nil {
			return zero, cache.Info{}, err
		}
		return value, cache.Info{State: cache.Bypass}, nil
	}
	for {
		started := time.Now()
		var v View
		if options.ScheduleRefresh != nil {
			v, err = s.backend.BeginStale(ctx, id, p, s.options.Lease)
		} else {
			v, err = s.backend.Begin(ctx, id, p, s.options.Lease)
		}
		if err != nil {
			return zero, cache.Info{}, err
		}
		if !validView(v) || !v.Found && !v.Busy && v.Lease == "" {
			return zero, cache.Info{}, ErrProtocol
		}
		elapsed := time.Since(started)
		var owner *leaseOwnership
		if v.Lease != "" {
			owner = &leaseOwnership{store: s, id: id, token: v.Lease, deadline: started.Add(v.LeaseFor)}
		}
		if v.Found && v.FreshFor > elapsed {
			value, info, err := decode[T](ctx, v, elapsed, cache.Fresh)
			if errors.Is(err, errGenerationExpired) {
				continue
			}
			return value, info, err
		}
		if options.ScheduleRefresh != nil && v.Found && (owner != nil || v.Busy) {
			value, _, decodeErr := decode[T](ctx, v, elapsed, cache.Stale)
			if decodeErr == nil {
				if owner != nil {
					if scheduleErr := schedule(owner, loader, options); scheduleErr != nil {
						options.ScheduleRefresh = nil
						continue
					}
				}
				if err := ctx.Err(); err != nil {
					return zero, cache.Info{}, err
				}
				elapsed = time.Since(started)
				if v.NoExpiry || v.ExpiresIn > elapsed {
					return value, info(v, elapsed, cache.Stale), nil
				}
				// A synchronous scheduler or slow decoding can consume the hard
				// window. Never return that old value after its deadline.
				continue
			}
			if err := ctx.Err(); err != nil {
				if owner != nil {
					_ = owner.finish(false)
				}
				return zero, cache.Info{}, err
			}
			// Malformed/expired stale data cannot be served. The owner repairs
			// it synchronously; a busy reader waits for a new generation.
		}
		if owner != nil {
			result, committed, loadErr := load(ctx, owner, loader, options)
			if loadErr != nil {
				if errors.Is(loadErr, errGenerationExpired) {
					continue
				}
				return zero, cache.Info{}, loadErr
			}
			if committed.Found {
				return result, info(committed, 0, cache.Miss), nil
			}
			// A retired lease must never deliver its old loader result. Acquire
			// again, allowing a replacement committed by another replica to win.
		} else {
			if err := wait(ctx, s.options.PollInterval); err != nil {
				return zero, cache.Info{}, err
			}
		}
	}
}
func wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ctx.Err()
	}
}
func invokeLoader[T any](ctx context.Context, loader func(context.Context) (T, error)) (value T, err error) {
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			err = ErrLoader
		}
	}()
	value, err = loader(ctx)
	completed = true
	return
}
func load[T any](ctx context.Context, owner *leaseOwnership, loader func(context.Context) (T, error), options LoadOptions) (T, View, error) {
	defer owner.finish(false)
	value, v, err := loadValue(ctx, owner, loader)
	if err != nil && !keepStale(options.KeepStaleOnError, err) {
		if discardErr := owner.finish(true); discardErr != nil {
			err = errors.Join(err, discardErr)
		}
	}
	return value, v, err
}
func loadValue[T any](ctx context.Context, owner *leaseOwnership, loader func(context.Context) (T, error)) (T, View, error) {
	var zero T
	remaining := time.Until(owner.deadline)
	if remaining <= 0 {
		return zero, View{}, nil
	}
	work, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()
	if err := work.Err(); err != nil {
		return zero, View{}, err
	}
	value, err := invokeLoader(work, loader)
	if ctx.Err() != nil {
		return zero, View{}, ctx.Err()
	}
	if work.Err() != nil {
		return zero, View{}, work.Err()
	}
	if err != nil {
		return zero, View{}, err
	}
	data, err := encode(value)
	if err != nil || len(data) > MaxValueBytes {
		return zero, View{}, ErrInvalid
	}
	// Decode before publication: reject ambiguous/deep or incompatible custom
	// JSON output rather than poisoning the shared generation for later readers.
	if _, _, err := decode[T](ctx, View{Found: true, Value: data, NoExpiry: true}, 0, cache.Miss); err != nil {
		return zero, View{}, err
	}
	started := time.Now()
	committed, err := owner.store.backend.Commit(work, owner.id, owner.token, data)
	if err != nil {
		return zero, View{}, err
	}
	if !validView(committed) || committed.Lease != "" || committed.Busy {
		return zero, View{}, ErrProtocol
	}
	if !committed.Found {
		return zero, View{}, nil
	}
	owner.complete()
	owned, _, err := decode[T](ctx, committed, time.Since(started), cache.Miss)
	if err != nil {
		return zero, View{}, err
	}
	// Keep conservative backend-relative ages after transport; no clock sync
	// between app hosts and backend is assumed.
	adjust(&committed, time.Since(started))
	return owned, committed, nil
}

type leaseOwnership struct {
	store    *Store
	id       ID
	token    string
	deadline time.Time
	once     sync.Once
	err      error
}

func (o *leaseOwnership) complete() { o.once.Do(func() {}) }
func (o *leaseOwnership) finish(discard bool) error {
	o.once.Do(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if discard {
			o.err = o.store.backend.Discard(cleanup, o.id, o.token)
		} else {
			o.err = o.store.backend.Release(cleanup, o.id, o.token)
		}
	})
	return o.err
}
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
func schedule[T any](owner *leaseOwnership, loader func(context.Context) (T, error), options LoadOptions) (err error) {
	finish := sync.OnceFunc(func() { _ = owner.finish(false) })
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			finish()
			err = ErrInvalid
		}
	}()
	err = options.ScheduleRefresh(func(work context.Context) error {
		defer finish()
		if work == nil {
			return ErrInvalid
		}
		if _, ok := work.Deadline(); !ok {
			return ErrInvalid
		}
		if err := work.Err(); err != nil {
			return err
		}
		if time.Until(owner.deadline) <= 0 {
			return nil
		}
		_, _, err := load(work, owner, loader, options)
		if errors.Is(err, errGenerationExpired) {
			return nil
		}
		return err
	}, finish)
	completed = true
	if err != nil {
		finish()
	}
	return err
}
func decode[T any](ctx context.Context, v View, elapsed time.Duration, state cache.State) (T, cache.Info, error) {
	var value T
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return value, cache.Info{}, err
	}
	if decodeJSON(v.Value, &value) != nil {
		var zero T
		return zero, cache.Info{}, ErrProtocol
	}
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, cache.Info{}, err
	}
	elapsed += time.Since(started)
	if !v.NoExpiry && v.ExpiresIn <= elapsed {
		var zero T
		return zero, cache.Info{}, errGenerationExpired
	}
	return value, info(v, elapsed, state), nil
}
func adjust(v *View, elapsed time.Duration) {
	const maxAge time.Duration = 1<<63 - 1
	if elapsed > maxAge-v.Age {
		v.Age = maxAge
	} else {
		v.Age += elapsed
	}
	v.FreshFor = max(0, v.FreshFor-elapsed)
	if !v.NoExpiry {
		v.ExpiresIn = max(0, v.ExpiresIn-elapsed)
	}
}
func info(v View, elapsed time.Duration, state cache.State) cache.Info {
	adjust(&v, elapsed)
	return cache.Info{State: state, Age: v.Age, FreshFor: v.FreshFor, ExpiresIn: v.ExpiresIn, NoExpiry: v.NoExpiry}
}

var errGenerationExpired = errors.New("shared cache: selected generation expired in transit")

func encode(value any) (data []byte, err error) {
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			err = ErrInvalid
		}
	}()
	data, err = json.Marshal(value)
	completed = true
	return
}

func decodeJSON(data []byte, value any) (err error) {
	completed := false
	defer func() {
		if !completed {
			_ = recover()
			err = ErrProtocol
		}
	}()
	if err = wirejson.Unique(data); err == nil {
		err = wirejson.Strict(data, value)
	}
	completed = true
	return
}
