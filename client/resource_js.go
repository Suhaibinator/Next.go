//go:build js && wasm

package client

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"
	"time"
)

// ResourcePhase describes one independently loading browser resource.
type ResourcePhase string

const (
	ResourceIdle        ResourcePhase = "idle"
	ResourceLoading     ResourcePhase = "loading"
	ResourceReady       ResourcePhase = "ready"
	ResourceFailed      ResourcePhase = "failed"
	ResourceInvalidated ResourcePhase = "invalidated"
	ResourceClosed      ResourcePhase = "closed"
)

// ResourceSnapshot owns its Body. Changing it cannot mutate another snapshot,
// subscriber, source seed or store entry. Err is for application handling; avoid
// displaying transport details that may contain sensitive endpoint information.
type ResourceSnapshot struct {
	Phase  ResourcePhase
	Body   []byte
	Err    error
	Seeded bool
}
type AsyncResourceOptions struct{ Seed *ResourceSeed }

// AsyncResource belongs to one Go/WASM event loop and its component lifetime.
// Creation, subscription and invalidation issue no network request. Load and
// Retry explicitly start work in a goroutine; neither retries automatically.
type AsyncResource struct {
	scope              *Scope
	source             *ResourceSource
	state              ResourceSnapshot
	sequence           uint64
	cancel             context.CancelFunc
	stop               func()
	cleanup            *Resource
	observers          map[int]func(ResourceSnapshot)
	next               int
	closed             bool
	notifying, pending bool
}

// The private expando belongs to the DOM node, not a Scope or module runtime.
// Record it only once: lazy retries may create a new Scope for the same old DOM.
const resourceOriginProperty = "__nextgoResourceOriginEpoch"

func recordResourceOrigin(root js.Value) {
	if root.Type() != js.TypeObject || root.IsNull() {
		return
	}
	if root.Get(resourceOriginProperty).IsUndefined() {
		root.Set(resourceOriginProperty, strconv.FormatUint(resourceEpoch, 10))
	}
}
func resourceScopeSeedAllowed(scope *Scope) bool {
	root := js.Undefined()
	for owner := scope; owner != nil; owner = owner.parent {
		candidate := owner.islandRoot()
		if candidate.Type() == js.TypeObject && !candidate.IsNull() {
			root = candidate
		}
	}
	// Headless Scope fixtures have no SSR node. Real islands require a stamp
	// from the document hydrator; missing or malformed metadata fails closed.
	if root.IsUndefined() {
		return true
	}
	value := root.Get(resourceOriginProperty)
	if value.Type() != js.TypeString {
		return false
	}
	epoch, err := strconv.ParseUint(value.String(), 10, 64)
	return err == nil && epoch == resourceEpoch
}

var resourceEpoch uint64
var resourceStores = map[*ResourceStore]*Scope{}

// ResourceStore creates a store owned by this component's lifetime. Stores and
// values are local to this WASM instance; there is no cross-runtime payload
// sharing. The document propagates only invalidation epochs to lazy modules.
func (s *Scope) ResourceStore(options ResourceStoreOptions) (*ResourceStore, error) {
	if !s.active() {
		return nil, ErrLifetimeClosed
	}
	if len(resourceStores) >= 64 {
		return nil, ErrResourceCapacity
	}
	life, err := s.Lifetime()
	if err != nil {
		return nil, err
	}
	store, err := NewResourceStore(options)
	if err != nil {
		return nil, err
	}
	store.seedAllowed = resourceScopeSeedAllowed(s)
	resourceStores[store] = s
	if _, err = life.Defer(func() { delete(resourceStores, store); store.Close() }); err != nil {
		delete(resourceStores, store)
		store.Close()
		return nil, err
	}
	return store, nil
}

// AsyncResource binds a registered source to this scope. Sources can be shared
// explicitly between scoped views; only positive PublicTTL allows reuse.
func (s *Scope) AsyncResource(source *ResourceSource, options AsyncResourceOptions) (*AsyncResource, error) {
	if !s.active() {
		return nil, ErrLifetimeClosed
	}
	if source == nil || source.store == nil {
		return nil, ErrResourceClosed
	}
	owner, ok := resourceStores[source.store]
	if !ok || !owner.active() {
		return nil, fmt.Errorf("nextgo: async resource requires a live scoped store")
	}
	// A scope may consume its own or an ancestor's store, never a sibling's.
	related := false
	for parent := s; parent != nil; parent = parent.parent {
		if parent == owner {
			related = true
			break
		}
	}
	if !related {
		return nil, fmt.Errorf("nextgo: async resource store belongs to another component")
	}
	life, err := s.Lifetime()
	if err != nil {
		return nil, err
	}
	r := &AsyncResource{scope: s, source: source, state: ResourceSnapshot{Phase: ResourceIdle}, observers: map[int]func(ResourceSnapshot){}}
	if options.Seed != nil {
		if err = source.AdoptSeed(*options.Seed); err == nil {
			r.state = ResourceSnapshot{Phase: ResourceReady, Body: bytes.Clone(options.Seed.Body), Seeded: true}
		} else if err != ErrResourceSeed {
			return nil, err
		}
	}
	// A new view on old-origin DOM starts invalidated even if its module only
	// just loaded. A fresh public value loaded afterward remains adoptable.
	if r.state.Phase != ResourceReady {
		source.store.mu.Lock()
		if !source.store.seedAllowed || source.store.epoch != 0 {
			r.state = ResourceSnapshot{Phase: ResourceInvalidated}
		}
		if entry, ok := source.store.entries[source]; ok && time.Now().Before(entry.expires) {
			r.state = ResourceSnapshot{Phase: ResourceReady, Body: bytes.Clone(entry.data)}
		}
		source.store.mu.Unlock()
	}
	r.stop, err = source.observe(func() {
		if !r.active() {
			return
		}
		r.sequence++
		if r.cancel != nil {
			r.cancel()
			r.cancel = nil
		}
		phase := ResourceInvalidated
		source.store.mu.Lock()
		closed := source.store.closed || source.closed
		source.store.mu.Unlock()
		if closed {
			phase = ResourceClosed
		}
		r.state = ResourceSnapshot{Phase: phase}
		r.notify()
		if closed {
			r.Close()
		}
	})
	if err != nil {
		return nil, err
	}
	r.cleanup, err = life.Defer(r.Close)
	if err != nil {
		r.stop()
		return nil, err
	}
	return r, nil
}
func (r *AsyncResource) active() bool { return r != nil && !r.closed && r.scope.active() }
func (r *AsyncResource) Snapshot() ResourceSnapshot {
	if r == nil {
		return ResourceSnapshot{Phase: ResourceClosed}
	}
	snapshot := r.state
	snapshot.Body = bytes.Clone(snapshot.Body)
	return snapshot
}
func (r *AsyncResource) Subscribe(fn func(ResourceSnapshot)) (func(), error) {
	if !r.active() {
		return nil, ErrResourceClosed
	}
	if fn == nil || len(r.observers) >= 64 {
		return nil, ErrResourceCapacity
	}
	r.next++
	id := r.next
	r.observers[id] = fn
	r.call(fn)
	return func() { delete(r.observers, id) }, nil
}
func (r *AsyncResource) call(fn func(ResourceSnapshot)) {
	if !r.active() {
		return
	}
	if err := lifetimeGuard(func() { fn(r.Snapshot()) }); err != nil {
		report(err.Error())
	}
}
func (r *AsyncResource) notify() {
	r.pending = true
	if r.notifying {
		return
	}
	r.notifying = true
	defer func() { r.notifying = false }()
	for turns := 0; r.pending && r.active(); turns++ {
		if turns >= 64 {
			r.Close()
			report("nextgo: async resource exceeded 64 reentrant notifications")
			return
		}
		r.pending = false
		ids := make([]int, 0, len(r.observers))
		for id := range r.observers {
			ids = append(ids, id)
		}
		for _, id := range ids {
			if fn, ok := r.observers[id]; ok {
				r.call(fn)
			}
		}
	}
}
func (r *AsyncResource) Load() error {
	if !r.active() {
		return ErrResourceClosed
	}
	r.source.store.mu.Lock()
	sourceClosed := r.source.store.closed || r.source.closed
	r.source.store.mu.Unlock()
	if sourceClosed {
		return ErrResourceClosed
	}
	r.sequence++
	sequence := r.sequence
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	r.state = ResourceSnapshot{Phase: ResourceLoading}
	r.notify()
	if !r.active() || sequence != r.sequence {
		cancel()
		return nil
	}
	go func() {
		body, err := r.source.Read(ctx)
		cancel()
		if !r.active() || sequence != r.sequence {
			return
		}
		r.cancel = nil
		if err != nil {
			r.state = ResourceSnapshot{Phase: ResourceFailed, Err: err}
		} else {
			r.state = ResourceSnapshot{Phase: ResourceReady, Body: body}
		}
		r.notify()
	}()
	return nil
}
func (r *AsyncResource) Retry() error { return r.Load() }
func (r *AsyncResource) Close() {
	if r == nil || r.closed {
		return
	}
	r.closed = true
	r.sequence++
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
	r.state = ResourceSnapshot{Phase: ResourceClosed}
	r.observers = nil
	if r.cleanup != nil {
		cleanup := r.cleanup
		r.cleanup = nil
		cleanup.Close()
	}
	r.scope = nil
	r.source = nil
}

// InvalidateResources applies the document mutation/cache fence, including
// loaded lazy modules. It never sends a request. Raw API mutations and external
// authentication changes must explicitly call this or Refresh to reconcile.
func InvalidateResources()      { invalidateCache() }
func invalidateResourceStores() { resourceEpoch++; invalidateLocalResourceStores() }
func setResourceEpoch(epoch uint64) {
	if resourceEpoch != epoch {
		resourceEpoch = epoch
		invalidateLocalResourceStores()
	}
}
func invalidateLocalResourceStores() {
	stores := make([]*ResourceStore, 0, len(resourceStores))
	for store := range resourceStores {
		stores = append(stores, store)
	}
	for _, store := range stores {
		store.Invalidate()
	}
}

// ResourceGET returns a size-bounded same-origin GET loader using Fetch. Public
// reuse additionally requires an explicit public response Cache-Control header;
// private/no-store, Vary:* and visible Set-Cookie responses are rejected. Browser
// Fetch hides Set-Cookie; the application's public declaration remains a trust
// boundary and must never cover session-dependent output.
func ResourceGET(target string, maxBytes int64, publicTTL time.Duration) func(context.Context) ([]byte, error) {
	return func(ctx context.Context) (body []byte, err error) {
		defer func() {
			if recover() != nil {
				body = nil
				err = fmt.Errorf("nextgo: resource GET setup failed")
			}
		}()
		if maxBytes <= 0 || maxBytes > 16<<20 || publicTTL < 0 || publicTTL > 5*time.Minute {
			return nil, fmt.Errorf("nextgo: resource GET requires a bounded response size")
		}
		u := win.Get("URL").New(target, win.Get("location").Get("href"))
		if u.Get("origin").String() != win.Get("location").Get("origin").String() || u.Get("username").String() != "" || u.Get("password").String() != "" || u.Get("hash").String() != "" {
			return nil, fmt.Errorf("nextgo: resource endpoint must be same-origin without credentials or fragment")
		}
		requestStarted := time.Now()
		response, err := Fetch(ctx, u.Get("href").String(), FetchOptions{Method: "GET", Cache: "no-store", Redirect: "error", MaxBytes: maxBytes, physicalSettlement: true})
		if err != nil {
			return nil, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("nextgo: resource GET returned HTTP %d", response.StatusCode)
		}
		if publicTTL > 0 && !resourcePublicResponse(response.Header, publicTTL+time.Since(requestStarted)) {
			return nil, fmt.Errorf("nextgo: resource response did not permit public reuse")
		}
		return response.Body, nil
	}
}
func resourcePublicResponse(header Headers, ttl time.Duration) bool {
	public := false
	maxAge := int64(-1)
	for _, line := range header.Values("Cache-Control") {
		for _, directive := range strings.Split(line, ",") {
			parts := strings.SplitN(directive, "=", 2)
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			switch name {
			case "max-age":
				if len(parts) != 2 || maxAge >= 0 {
					return false
				}
				value, err := strconv.ParseUint(strings.TrimSpace(parts[1]), 10, 31)
				if err != nil {
					return false
				}
				maxAge = int64(value)
			case "public":
				public = true
			case "private", "no-store", "no-cache":
				return false
			}
		}
	}
	for _, line := range header.Values("Vary") {
		for _, name := range strings.Split(line, ",") {
			if field := strings.TrimSpace(name); field == "*" || strings.EqualFold(field, "Cookie") || strings.EqualFold(field, "Authorization") {
				return false
			}
		}
	}
	age := int64(0)
	if values := header.Values("Age"); len(values) > 0 {
		if len(values) != 1 {
			return false
		}
		value, err := strconv.ParseUint(strings.TrimSpace(values[0]), 10, 31)
		if err != nil {
			return false
		}
		age = int64(value)
	}
	return public && ttl > 0 && maxAge > age && time.Duration(maxAge-age)*time.Second >= ttl && len(header.Values("Set-Cookie")) == 0
}
