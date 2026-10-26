// Package streamguard shares private physical ownership across request-Memo
// context lineage. Fresh scopes keep independent caches but retain inherited
// physical work and loader-admission guards. It starts no goroutine.
package streamguard

import (
	"context"
	"sync"
	"sync/atomic"
)

type Snapshot func(owner any) (blocked bool, changed <-chan struct{})
type guardKey struct{}
type ownerKey struct{}
type groupKey struct{}
type entryKey struct{}

type Group struct {
	mu      sync.Mutex
	owners  map[any]int
	changed chan struct{}
	pending int
	leases  map[*Lease]struct{}
}

// Entry is physical ancestry, independent of fresh memo key scopes.
type Entry struct {
	group  *Group
	parent *Entry
	active atomic.Bool
}

type Lease struct {
	group            *Group
	owner            any
	done             chan struct{}
	callbackFinished bool // group.mu
}

func NewGroup() *Group { return &Group{} }
func GroupFrom(ctx context.Context) *Group {
	if ctx == nil {
		return nil
	}
	group, _ := ctx.Value(groupKey{}).(*Group)
	return group
}

// WithLineage preserves physical ownership without activating a loader guard on
// ordinary request contexts. Only Memo loaders use WithGroup to enable refusal.
func WithLineage(ctx context.Context, group *Group) context.Context {
	return context.WithValue(ctx, groupKey{}, group)
}
func WithGroup(ctx context.Context, group *Group) context.Context {
	return WithGuard(WithLineage(ctx, group), group.State)
}
func WithGuard(ctx context.Context, snapshot Snapshot) context.Context {
	return context.WithValue(ctx, guardKey{}, snapshot)
}
func State(ctx context.Context, owner any) (bool, <-chan struct{}) {
	if ctx != nil {
		if snapshot, _ := ctx.Value(guardKey{}).(Snapshot); snapshot != nil {
			return snapshot(owner)
		}
	}
	return false, nil
}
func (g *Group) State(owner any) (bool, <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	return g.owners[owner] > 0, g.changed
}

// EntryStarted linearizes physical entry admission before publication/launch.
// Entry.Finish is called only after the actual Memo entry.done is closed.
// Scope mutexes may acquire group.mu; group code never acquires a scope mutex.
func (g *Group) EntryStarted(ctx context.Context) *Entry {
	parent, _ := ctx.Value(entryKey{}).(*Entry)
	entry := &Entry{group: g, parent: parent}
	entry.active.Store(true)
	g.mu.Lock()
	g.pending++
	g.mu.Unlock()
	return entry
}
func WithEntry(ctx context.Context, entry *Entry) context.Context {
	return WithGroup(context.WithValue(ctx, entryKey{}, entry), entry.group)
}
func (entry *Entry) Deactivate() { entry.active.Store(false) }
func (entry *Entry) Finish() {
	entry.Deactivate()
	entry.group.mu.Lock()
	entry.group.pending--
	entry.group.finishLocked()
	entry.group.mu.Unlock()
}

// A group lease cannot wait for a still-active ancestor which awaits that
// metadata operation. Completed frames and unrelated groups are permitted.
func ActiveAncestor(ctx context.Context, group *Group) bool {
	entry, _ := ctx.Value(entryKey{}).(*Entry)
	for current := entry; current != nil; current = current.parent {
		if current.group == group && current.active.Load() {
			return true
		}
	}
	return false
}

func (g *Group) BeginLease(owner any) *Lease {
	lease := &Lease{group: g, owner: owner, done: make(chan struct{})}
	g.mu.Lock()
	if g.leases == nil {
		g.leases = make(map[*Lease]struct{})
	}
	g.leases[lease] = struct{}{}
	if owner != nil {
		if g.owners == nil {
			g.owners = make(map[any]int)
		}
		g.owners[owner]++
		g.notifyLocked()
	}
	g.mu.Unlock()
	return lease
}
func (lease *Lease) FinishCallback() {
	lease.group.mu.Lock()
	lease.callbackFinished = true
	lease.group.finishLocked()
	lease.group.mu.Unlock()
}
func (lease *Lease) Wait()                 { <-lease.done }
func (lease *Lease) Done() <-chan struct{} { return lease.done }

func (g *Group) finishLocked() {
	if g.pending != 0 {
		return
	}
	changed := false
	for lease := range g.leases {
		if !lease.callbackFinished {
			continue
		}
		delete(g.leases, lease)
		if lease.owner != nil {
			g.owners[lease.owner]--
			if g.owners[lease.owner] == 0 {
				delete(g.owners, lease.owner)
			}
			changed = true
		}
		close(lease.done)
	}
	if len(g.leases) == 0 {
		g.leases = nil
	}
	if changed {
		g.notifyLocked()
	}
}
func (g *Group) notifyLocked() {
	if g.changed != nil {
		close(g.changed)
	}
	g.changed = make(chan struct{})
}

// WithOwner is installed only after acquiring an actual physical limiter slot;
// the opaque comparable limiter pointer is never exposed in wire/request data.
func WithOwner(ctx context.Context, owner any) context.Context {
	return context.WithValue(ctx, ownerKey{}, owner)
}
func Owner(ctx context.Context) any {
	if ctx == nil {
		return nil
	}
	return ctx.Value(ownerKey{})
}
