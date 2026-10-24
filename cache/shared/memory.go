package shared

import (
	"bytes"
	"container/list"
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strings"
	"sync"
	"time"
)

// Memory is a bounded reference Backend. Share it through Handler for multiple
// application processes. It is not durable or replicated: its restart loses
// values and invalidation state. Live leases are never evicted to admit a key.
// Payload accounting is conservative; it does not measure Go heap/RSS.
type Memory struct {
	mu             sync.Mutex
	capacity       int
	maxBytes, used int64
	now            func() time.Time
	entries        map[ID]*list.Element
	lru            *list.List
	seedTickets    map[string]pendingSeed
	seedSessions   map[string]*activeSeed
}
type memoryEntry struct {
	id                     ID
	policy                 Policy
	value                  []byte
	found                  bool
	noExpiry               bool
	loaded, fresh, expires time.Time
	token                  string
	generation             string
	leaseEnd               time.Time
	cost                   int64
}

// NewMemory requires positive entry and payload-accounting limits. At most
// capacity keys (including leases) and maxBytes accounted bytes are retained.
func NewMemory(capacity int, maxBytes int64) (*Memory, error) {
	if capacity <= 0 || maxBytes <= 0 {
		return nil, ErrInvalid
	}
	return &Memory{capacity: capacity, maxBytes: maxBytes, now: time.Now, entries: map[ID]*list.Element{}, lru: list.New(), seedTickets: map[string]pendingSeed{}, seedSessions: map[string]*activeSeed{}}, nil
}
func (m *Memory) remove(e *list.Element) {
	v := e.Value.(*memoryEntry)
	m.used -= v.cost
	delete(m.entries, v.id)
	m.lru.Remove(e)
}
func metadataCost(id ID, p Policy) int64 {
	cost := int64(768 + len(id.Namespace) + len(id.Schema) + len(id.Key))
	for _, labels := range [][]string{p.Tags, p.Paths} {
		for _, s := range labels {
			cost += int64(48 + len(s))
		}
	}
	return cost
}

// makeRoom cannot evict protected or live loading entries. Expired leases can
// be evicted safely; their tokens can never publish into replacement entries.
func (m *Memory) makeRoom(extra int64, newKey bool, protected *list.Element, now time.Time) bool {
	if extra < 0 && !newKey {
		return true
	}
	if extra > m.maxBytes {
		return false
	}
	// Check admission before eviction: a failed write must not discard unrelated
	// entries when live/protected leases leave insufficient space.
	available, count := int64(0), 0
	for e := m.lru.Back(); e != nil && (m.used-available > m.maxBytes-extra || newKey && len(m.entries)-count >= m.capacity); e = e.Prev() {
		v := e.Value.(*memoryEntry)
		if e != protected && (v.token == "" || !now.Before(v.leaseEnd)) {
			available += v.cost
			count++
		}
	}
	if m.used-available > m.maxBytes-extra || newKey && len(m.entries)-count >= m.capacity {
		return false
	}
	for e := m.lru.Back(); (m.used > m.maxBytes-extra || newKey && len(m.entries) >= m.capacity) && e != nil; {
		prev := e.Prev()
		v := e.Value.(*memoryEntry)
		if e != protected && (v.token == "" || !now.Before(v.leaseEnd)) {
			m.remove(e)
		}
		e = prev
	}
	return m.used <= m.maxBytes-extra && (!newKey || len(m.entries) < m.capacity)
}
func view(e *memoryEntry, now time.Time) View {
	v := View{}
	if viewFound(e, now) {
		v.Found, v.Value = true, bytes.Clone(e.value)
		v.Age, v.FreshFor = max(0, now.Sub(e.loaded)), max(0, e.fresh.Sub(now))
		v.NoExpiry = e.noExpiry
		if !v.NoExpiry {
			v.ExpiresIn = max(0, e.expires.Sub(now))
		}
	}
	return v
}
func (m *Memory) Begin(ctx context.Context, id ID, policy Policy, lease time.Duration) (View, error) {
	return m.begin(ctx, id, policy, lease, false)
}
func (m *Memory) BeginStale(ctx context.Context, id ID, policy Policy, lease time.Duration) (View, error) {
	return m.begin(ctx, id, policy, lease, true)
}
func (m *Memory) begin(ctx context.Context, id ID, policy Policy, lease time.Duration, includeStale bool) (View, error) {
	if ctx == nil {
		return View{}, ErrInvalid
	}
	if err := validateID(id); err != nil {
		return View{}, err
	}
	if err := validateLease(lease); err != nil {
		return View{}, err
	}
	p, err := normalize(policy)
	if err != nil {
		return View{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return View{}, err
	}
	now := m.now()
	elem := m.entries[id]
	if elem != nil {
		e := elem.Value.(*memoryEntry)
		if !viewFound(e, now) && (e.token == "" || !now.Before(e.leaseEnd)) {
			m.remove(elem)
			elem = nil
		}
	}
	if elem != nil {
		e := elem.Value.(*memoryEntry)
		if !equalPolicy(e.policy, p) {
			return View{}, ErrPolicy
		}
		m.lru.MoveToFront(elem)
		if viewFound(e, now) && now.Before(e.fresh) {
			return view(e, now), nil
		}
		if e.token != "" && now.Before(e.leaseEnd) {
			// Polling readers need only the lease deadline, not a repeated copy
			// of a potentially large stale value on every busy request.
			v := View{}
			if includeStale {
				v = view(e, now)
			}
			v.Busy = true
			v.LeaseFor = e.leaseEnd.Sub(now)
			return v, nil
		}
	}
	// Random tokens prevent ABA across eviction and service restarts. Acquire
	// before changing storage so entropy failures cannot leave phantom leases.
	var token [32]byte
	if _, err := rand.Read(token[:]); err != nil {
		return View{}, ErrUnavailable
	}
	if elem == nil {
		cost := metadataCost(id, p)
		if !m.makeRoom(cost, true, nil, now) {
			return View{}, ErrCapacity
		}
		e := &memoryEntry{id: id, policy: p, cost: cost}
		elem = m.lru.PushFront(e)
		m.entries[id] = elem
		m.used += cost
	}
	e := elem.Value.(*memoryEntry)
	e.token = hex.EncodeToString(token[:])
	e.leaseEnd = now.Add(lease)
	v := view(e, now)
	v.Lease = e.token
	v.LeaseFor = lease
	return v, nil
}
func viewFound(e *memoryEntry, now time.Time) bool {
	return e.found && (e.noExpiry || now.Before(e.expires))
}

func (m *Memory) Commit(ctx context.Context, id ID, token string, value []byte) (View, error) {
	if ctx == nil || validateID(id) != nil || !validToken(token) || len(value) > MaxValueBytes {
		return View{}, ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return View{}, err
	}
	now := m.now()
	elem := m.entries[id]
	if elem == nil {
		return View{}, nil
	}
	e := elem.Value.(*memoryEntry)
	if e.token != token || !now.Before(e.leaseEnd) {
		return View{}, nil
	}
	var generation [32]byte
	if _, err := rand.Read(generation[:]); err != nil {
		return View{}, ErrUnavailable
	}
	delta := int64(len(value) - len(e.value))
	if !m.makeRoom(delta, false, elem, now) {
		return View{}, ErrCapacity
	}
	e.value = bytes.Clone(value)
	e.generation = hex.EncodeToString(generation[:])
	e.found = true
	e.loaded = now
	e.fresh = now.Add(e.policy.FreshFor)
	e.noExpiry = e.policy.ExpireAfter == Never
	if !e.noExpiry {
		e.expires = now.Add(e.policy.ExpireAfter)
	}
	e.token = ""
	e.leaseEnd = time.Time{}
	e.cost += delta
	m.used += delta
	m.lru.MoveToFront(elem)
	return view(e, now), nil
}
func (m *Memory) Release(ctx context.Context, id ID, token string) error {
	return m.retire(ctx, id, token, false)
}
func (m *Memory) Discard(ctx context.Context, id ID, token string) error {
	return m.retire(ctx, id, token, true)
}
func (m *Memory) retire(ctx context.Context, id ID, token string, discard bool) error {
	if ctx == nil || validateID(id) != nil || !validToken(token) {
		return ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if elem := m.entries[id]; elem != nil {
		e := elem.Value.(*memoryEntry)
		if e.token == token {
			e.token = ""
			e.leaseEnd = time.Time{}
			if discard || !viewFound(e, m.now()) {
				m.remove(elem)
			}
		}
	}
	return nil
}
func (m *Memory) Expire(ctx context.Context, namespace string, selector Selector) (int, error) {
	return m.change(ctx, namespace, selector, 0)
}
func (m *Memory) MarkStale(ctx context.Context, namespace string, selector Selector, window time.Duration) (int, error) {
	if !validWindow(window) {
		return 0, ErrInvalid
	}
	return m.change(ctx, namespace, selector, window)
}
func (m *Memory) change(ctx context.Context, namespace string, selector Selector, window time.Duration) (int, error) {
	if ctx == nil {
		return 0, ErrInvalid
	}
	s, err := normalizeSelector(namespace, selector)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n := 0
	now := m.now()
	m.revokeSeeds(namespace)
	for _, elem := range m.entries {
		e := elem.Value.(*memoryEntry)
		if e.id.Namespace != namespace {
			continue
		}
		matches := false
		switch s.Kind {
		case "all":
			matches = true
		case "key":
			matches = e.id.Key == s.Value
		case "tag":
			matches = slices.Contains(e.policy.Tags, s.Value)
		case "path":
			matches = slices.Contains(e.policy.Paths, s.Value)
		case "prefix":
			for _, p := range e.policy.Paths {
				if s.Value == "/" || p == s.Value || strings.HasPrefix(p, s.Value+"/") {
					matches = true
					break
				}
			}
		}
		if matches {
			if window == 0 || !viewFound(e, now) {
				m.remove(elem)
			} else {
				e.fresh = now
				e.token = ""
				e.leaseEnd = time.Time{}
				if window != Never {
					end := now.Add(window)
					if e.noExpiry || end.Before(e.expires) {
						e.expires = end
						e.noExpiry = false
					}
				}
			}
			n++
		}
	}
	return n, nil
}

// Stats reports currently retained keys/leases and accounted payload bytes.
func (m *Memory) Stats() (keys int, accountedBytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries), m.used
}
