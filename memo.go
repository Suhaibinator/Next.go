package nextgo

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/Suhaibinator/Next.go/internal/streamguard"
	"github.com/Suhaibinator/Next.go/ui"
)

var (
	ErrMemoScope    = errors.New("nextgo: request memoization requires an application request or WithRequestMemo")
	ErrMemoCapacity = errors.New("nextgo: request memoization exceeds 128 keys")
	ErrMemoType     = errors.New("nextgo: memo key reused with a different result type")
	ErrMemoCycle    = errors.New("nextgo: cyclic memoized loader dependency")
)

type memoScopeKey struct{}
type memoStackKey struct{}
type memoScope struct {
	mu          sync.Mutex
	root        context.Context
	entries     map[string]*memoEntry
	pending     int
	streamGroup *streamguard.Group
}
type memoEntry struct {
	done         chan struct{}
	resultType   reflect.Type
	value        any
	err          error
	complete     bool
	dependencies map[string]int
}
type memoValue[T any] struct{ value T }

// Cancellation belongs to the request; values belong to the initiating call.
// WithCancelCause supplies a real cancellation context, preserving Cause while
// this wrapper reports the request deadline without a duplicate timer.
type memoLoadContext struct {
	context.Context
	request context.Context
}

func (c memoLoadContext) Deadline() (time.Time, bool) { return c.request.Deadline() }

// WithRequestMemo creates an isolated request cache. App installs one for every
// request, including API/action handlers. The context's cancellation/deadline
// governs shared loaders. Calling this explicitly starts a fresh key/value
// scope; inherited physical stream ownership is intentionally preserved.
func WithRequestMemo(ctx context.Context) context.Context {
	group := streamguard.GroupFrom(ctx)
	if group == nil {
		group = streamguard.NewGroup()
	}
	scope := &memoScope{entries: make(map[string]*memoEntry), streamGroup: group}
	scope.root = streamguard.WithLineage(context.WithValue(context.WithValue(ctx, memoScopeKey{}, scope), memoStackKey{}, []string(nil)), group)
	return scope.root
}

// Memo deduplicates a typed loader for one request. Successes and errors are
// retained until the request context is discarded. Keys must describe all
// loader inputs and values must be treated as immutable. A scope retains at
// most 128 distinct keys (not bytes); keys are limited to 1024 bytes.
//
// A waiter's cancellation does not cancel work needed by another caller. The
// loader receives the root request deadline/cancellation and must respect it.
// Forward that provided context to nested Memo calls: dependency tracking then
// rejects cycles, including concurrent cross-loader cycles. Do not call Memo
// recursively using a captured outer context instead of the supplied one.
func Memo[T any](ctx context.Context, key string, load func(context.Context) (T, error)) (T, error) {
	var zero T
	if ctx == nil {
		return zero, ErrMemoScope
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	scope, ok := ctx.Value(memoScopeKey{}).(*memoScope)
	if !ok {
		return zero, ErrMemoScope
	}
	if err := scope.root.Err(); err != nil {
		return zero, err
	}
	if key == "" || len(key) > 1024 || load == nil {
		return zero, fmt.Errorf("nextgo: memo requires a nonempty key up to 1024 bytes and a loader")
	}
	stack, _ := ctx.Value(memoStackKey{}).([]string)
	scope.mu.Lock()
	entry := scope.entries[key]
	if entry != nil && entry.resultType != reflect.TypeFor[T]() {
		scope.mu.Unlock()
		return zero, ErrMemoType
	}
	if entry != nil && entry.complete {
		value, err := entry.value.(memoValue[T]), entry.err
		scope.mu.Unlock()
		return value.value, err
	}
	if entry == nil && len(scope.entries) >= 128 {
		scope.mu.Unlock()
		return zero, ErrMemoCapacity
	}
	var dependencyParent *memoEntry
	if len(stack) > 0 {
		parent := scope.entries[stack[len(stack)-1]]
		if parent != nil && !parent.complete {
			if stack[len(stack)-1] == key || scope.reaches(key, stack[len(stack)-1], make(map[string]bool)) {
				scope.mu.Unlock()
				return zero, ErrMemoCycle
			}
			parent.dependencies[key]++
			dependencyParent = parent
		}
	}
	if entry == nil {
		entry = &memoEntry{done: make(chan struct{}), resultType: reflect.TypeFor[T](), dependencies: make(map[string]int)}
		physical := scope.streamGroup.EntryStarted(ctx)
		scope.entries[key] = entry
		scope.pending++
		chain := append(append([]string(nil), stack...), key)
		base, cancel := context.WithCancelCause(context.WithoutCancel(ctx))
		stop := context.AfterFunc(scope.root, func() { cancel(context.Cause(scope.root)) })
		if err := scope.root.Err(); err != nil {
			cancel(context.Cause(scope.root))
		}
		loaderCtx := streamguard.WithEntry(context.WithValue(memoLoadContext{Context: base, request: scope.root}, memoStackKey{}, chain), physical)
		go func() {
			var value T
			err := errors.New("memoized loader exited without returning")
			defer func() {
				physical.Deactivate()
				if failure := recover(); failure != nil {
					err = errors.New("memoized loader panic")
				}
				stop()
				cancel(context.Canceled)
				scope.mu.Lock()
				entry.value = memoValue[T]{value: value}
				entry.err = err
				entry.complete = true
				entry.dependencies = nil
				close(entry.done)
				scope.pending--
				physical.Finish()
				scope.mu.Unlock()
			}()
			value, err = load(loaderCtx)
		}()
	}
	scope.mu.Unlock()
	if dependencyParent != nil {
		defer func() {
			scope.mu.Lock()
			defer scope.mu.Unlock()
			if !dependencyParent.complete {
				dependencyParent.dependencies[key]--
				if dependencyParent.dependencies[key] == 0 {
					delete(dependencyParent.dependencies, key)
				}
			}
		}()
	}
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case <-scope.root.Done():
		return zero, scope.root.Err()
	case <-entry.done:
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if err := scope.root.Err(); err != nil {
			return zero, err
		}
		return entry.value.(memoValue[T]).value, entry.err
	}
}

// Called under the scope mutex; at most 128 nodes can be visited.
func (s *memoScope) reaches(from, to string, seen map[string]bool) bool {
	if from == to {
		return true
	}
	if seen[from] {
		return false
	}
	seen[from] = true
	entry := s.entries[from]
	if entry == nil || entry.complete {
		return false
	}
	for next := range entry.dependencies {
		if s.reaches(next, to, seen) {
			return true
		}
	}
	return false
}

// metadataMemoLease observes physical Memo completion across the inherited
// request lineage, while each explicit WithRequestMemo keeps its own key cache.
// There is one shared pending count and one channel per lease, no observer
// goroutine per entry, and no promise to track arbitrary future app goroutines.
type metadataMemoLease struct {
	physical *streamguard.Lease
	done     <-chan struct{}
}

// Use the exact admitted RunStreamLoader callback context: its physical owner
// and request Memo lineage must survive readonly/value wrappers. Cancellation
// does not prevent joining dependencies which are still physically alive.
// A lease inside an active same-lineage Memo would drain its own ancestor;
// reject that cycle before invoking a metadata provider.
func beginMetadataMemoLease(ctx context.Context) (*metadataMemoLease, error) {
	if ctx == nil {
		return nil, ErrMemoScope
	}
	scope, _ := ctx.Value(memoScopeKey{}).(*memoScope)
	if scope == nil {
		return nil, ErrMemoScope
	}
	if streamguard.ActiveAncestor(ctx, scope.streamGroup) {
		return nil, ui.ErrStreamReentrant
	}
	physical := scope.streamGroup.BeginLease(streamguard.Owner(ctx))
	return &metadataMemoLease{physical: physical, done: physical.Done()}, nil
}

// FinishCallback is idempotent and must precede Wait, including panic/Goexit.
func (lease *metadataMemoLease) FinishCallback() {
	if lease != nil {
		lease.physical.FinishCallback()
	}
}

// Wait joins physical dependencies, including canceled descendants in fresh
// Memo scopes. It does not follow waiter cancellation or release a loader slot.
func (lease *metadataMemoLease) Wait() {
	if lease != nil {
		lease.physical.Wait()
	}
}
