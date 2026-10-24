package shared

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// FileOptions bounds entries, data accounting, seed-control accounting and the
// active journal. Zero fields select 1024 entries, 64 MiB data, 8 MiB control
// and 256 MiB disk. MaxControlBytes may be at most 1 GiB.
// Atomic compaction may temporarily use up to twice MaxDiskBytes for the active
// and staging journals, plus fixed lock/directory metadata. This is not an RSS
// limit. Reopening with reduced limits may reject an existing journal.
type FileOptions struct {
	Capacity               int
	MaxBytes, MaxDiskBytes int64
	MaxControlBytes        int64
}

// File is a synchronous durable Backend for one authority on a private local
// filesystem. It is not a replicated database. Leases never survive restart.
// An uncertain storage failure poisons the instance until it is closed/reopened.
// Use Linux/macOS local storage with correct durable sync/rename semantics and
// a nondecreasing wall clock. Trusted operators must not edit/restore individual
// files; checksums detect damage, not malicious whole-store rollback.
type File struct {
	gate             chan struct{}
	memory           *Memory
	dir              string
	log              *os.File
	lock             io.Closer
	maxDisk, size    int64
	sequence         uint64
	lastWrite        time.Time
	poisoned, closed bool
	seedFormat       bool
	now              func() time.Time
	syncFile         func(*os.File) error
	syncDir          func(string) error
	rename           func(string, string) error
}

// OpenFile opens an exclusively locked store. Detected malformed or incomplete
// transactions fail closed; recovery never automatically truncates the journal.
// The filesystem must support durable file/directory sync and atomic rename.
func OpenFile(dir string, options FileOptions) (_ *File, result error) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return nil, fmt.Errorf("%w: file backend requires Linux or macOS local storage", ErrUnavailable)
	}
	if options.Capacity == 0 {
		options.Capacity = 1024
	}
	if options.MaxBytes == 0 {
		options.MaxBytes = 64 << 20
	}
	if options.MaxDiskBytes == 0 {
		options.MaxDiskBytes = 256 << 20
	}
	if options.MaxControlBytes == 0 {
		options.MaxControlBytes = DefaultControlBytes
	}
	if options.Capacity < 1 || options.Capacity > 1_000_000 || options.MaxBytes < 1 || options.MaxBytes > 1<<40 || options.MaxDiskBytes < 4096 || options.MaxDiskBytes > 1<<40 || options.MaxControlBytes < 1 || options.MaxControlBytes > maxControlBytes || dir == "" {
		return nil, ErrInvalid
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err = makeDurableDirectory(dir); err != nil {
		return nil, err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !privateDataDirectory(info) {
		return nil, errors.New("shared cache: data directory must be a private directory (0700)")
	}
	_, lockErr := os.Lstat(filepath.Join(dir, "cache.lock"))
	if lockErr != nil && !os.IsNotExist(lockErr) {
		return nil, lockErr
	}
	lock, err := acquireFileLock(filepath.Join(dir, "cache.lock"))
	if err != nil {
		return nil, err
	}
	f := &File{gate: make(chan struct{}, 1), dir: dir, lock: lock, maxDisk: options.MaxDiskBytes, now: time.Now,
		syncFile: func(f *os.File) error { return f.Sync() }, syncDir: syncDirectory, rename: os.Rename}
	defer func() {
		if result != nil {
			if f.log != nil {
				_ = f.log.Close()
			}
			_ = lock.Close()
		}
	}()
	f.memory, _ = NewMemoryWithOptions(MemoryOptions{Capacity: options.Capacity, MaxBytes: options.MaxBytes, MaxControlBytes: options.MaxControlBytes})
	path := filepath.Join(dir, "cache.wal")
	existing, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if os.IsNotExist(err) && lockErr == nil {
		return nil, errors.New("shared cache: initialized store is missing its journal")
	}
	if err == nil && (!privateDataFile(existing) || existing.Size() > f.maxDisk) {
		return nil, errors.New("shared cache: unsafe or oversized journal")
	}
	f.log, err = os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	stat, err := f.log.Stat()
	if err != nil {
		return nil, err
	}
	if !privateDataFile(stat) {
		return nil, errors.New("shared cache: unsafe journal")
	}
	if existing != nil && !os.SameFile(existing, stat) {
		return nil, errors.New("shared cache: journal changed during open")
	}
	f.size = stat.Size()
	if f.size == 0 {
		if existing != nil {
			return nil, errors.New("shared cache: empty existing journal")
		}
		if _, err = f.log.Write([]byte(fileMagic)); err != nil {
			return nil, err
		}
		if err = f.log.Sync(); err != nil {
			return nil, err
		}
		if err = syncDirectory(dir); err != nil {
			return nil, err
		}
		f.size = int64(len(fileMagic))
	} else if err = f.recover(); err != nil {
		// Parser errors can quote cached input. Report the recovery boundary,
		// never application values or user-controlled journal field names.
		return nil, fmt.Errorf("%w: journal recovery refused (integrity, format, configured limits, or clock)", ErrProtocol)
	}
	if _, err = f.log.Seek(0, io.SeekEnd); err != nil {
		return nil, err
	}
	// A previous process may have completed a transaction but failed before its
	// sync/ack. Establish durability before exposing that recovered outcome.
	if err = f.log.Sync(); err != nil {
		return nil, err
	}
	if err = syncDirectory(dir); err != nil {
		return nil, err
	}
	// A crashed compaction is never a recovery source. At most this one fixed
	// staging file may remain; only remove a private regular file under our lock.
	staging := filepath.Join(dir, "cache.next")
	if s, e := os.Lstat(staging); e == nil {
		if !privateDataFile(s) {
			return nil, errors.New("shared cache: unsafe staging file")
		}
		if e = os.Remove(staging); e != nil {
			return nil, e
		}
		if e = syncDirectory(dir); e != nil {
			return nil, e
		}
	} else if !os.IsNotExist(e) {
		return nil, e
	}
	return f, nil
}
func makeDurableDirectory(dir string) error {
	var created []string
	for p := dir; ; p = filepath.Dir(p) {
		_, err := os.Lstat(p)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return err
		}
		created = append(created, p)
		if filepath.Dir(p) == p {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, p := range created {
		if err := syncDirectory(p); err != nil {
			return err
		}
		if err := syncDirectory(filepath.Dir(p)); err != nil {
			return err
		}
	}
	return nil
}
func syncDirectory(dir string) error {
	d, e := os.Open(dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (f *File) enter(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	select {
	case f.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		f.leave()
		return err
	}
	if f.closed || f.poisoned {
		f.leave()
		return ErrUnavailable
	}
	if f.now().UTC().Before(f.lastWrite.UTC()) {
		f.poisoned = true
		f.leave()
		return ErrUnavailable
	}
	return nil
}
func (f *File) leave() { <-f.gate }
func (f *File) Close() error {
	f.gate <- struct{}{}
	defer f.leave()
	if f.closed {
		return nil
	}
	f.closed = true
	return errors.Join(f.log.Close(), f.lock.Close())
}
func cloneMemory(m *Memory, now time.Time) *Memory {
	c, _ := NewMemoryWithOptions(MemoryOptions{Capacity: m.capacity, MaxBytes: m.maxBytes, MaxControlBytes: m.maxControlBytes})
	c.now = func() time.Time { return now }
	for e := m.lru.Back(); e != nil; e = e.Prev() {
		v := *e.Value.(*memoryEntry)
		n := c.lru.PushFront(&v)
		c.entries[v.id] = n
		c.used += v.cost
	}
	for token, ticket := range m.seedTickets {
		c.seedTickets[token] = ticket
	}
	for token, session := range m.seedSessions {
		s := *session
		// InstallSeeds copies this immutable shared map on its first write.
		s.seenShared = true
		c.seedSessions[token] = &s
	}
	return c
}
func (f *File) mutate(ctx context.Context, operation func(*Memory) error) error {
	if err := f.enter(ctx); err != nil {
		return err
	}
	defer f.leave()
	now := f.now()
	candidate := cloneMemory(f.memory, now)
	candidate.pruneSeeds(now)
	if err := operation(candidate); err != nil {
		return err
	}
	records := diskChanges(f.memory, candidate)
	if len(records) > 0 {
		if err := f.persist(candidate, records, now); err != nil {
			return err
		}
	}
	candidate.now = f.now
	f.memory = candidate
	return ctx.Err()
}
func (f *File) begin(ctx context.Context, id ID, p Policy, lease time.Duration, stale bool) (v View, err error) {
	if ctx == nil || validateID(id) != nil || validateLease(lease) != nil {
		return v, ErrInvalid
	}
	p, err = normalize(p)
	if err != nil {
		return v, err
	}
	if err = f.enter(ctx); err != nil {
		return v, err
	}
	defer f.leave()
	now := f.now()
	// Fresh/busy reads change only LRU order. Keep the selection clock fixed so
	// a boundary crossing cannot accidentally turn this into an unlogged eviction.
	if elem := f.memory.entries[id]; elem != nil {
		e := elem.Value.(*memoryEntry)
		if viewFound(e, now) && now.Before(e.fresh) || e.token != "" && now.Before(e.leaseEnd) {
			f.memory.now = func() time.Time { return now }
			v, err = f.memory.begin(ctx, id, p, lease, stale)
			f.memory.now = f.now
			return v, err
		}
	}
	candidate := cloneMemory(f.memory, now)
	candidate.pruneSeeds(now)
	v, err = candidate.begin(ctx, id, p, lease, stale)
	if err != nil {
		return View{}, err
	}
	records := diskChanges(f.memory, candidate)
	if len(records) > 0 {
		if err = f.persist(candidate, records, now); err != nil {
			return View{}, err
		}
	}
	candidate.now = f.now
	f.memory = candidate
	if err = ctx.Err(); err != nil {
		return View{}, err
	}
	return v, nil
}
func (f *File) Begin(c context.Context, id ID, p Policy, l time.Duration) (View, error) {
	return f.begin(c, id, p, l, false)
}
func (f *File) BeginStale(c context.Context, id ID, p Policy, l time.Duration) (View, error) {
	return f.begin(c, id, p, l, true)
}
func (f *File) Commit(c context.Context, id ID, t string, b []byte) (v View, err error) {
	err = f.mutate(c, func(m *Memory) error { var e error; v, e = m.Commit(c, id, t, b); return e })
	if err != nil {
		return View{}, err
	}
	return v, nil
}
func (f *File) Release(c context.Context, id ID, t string) error {
	return f.mutate(c, func(m *Memory) error { return m.Release(c, id, t) })
}
func (f *File) Discard(c context.Context, id ID, t string) error {
	return f.mutate(c, func(m *Memory) error { return m.Discard(c, id, t) })
}
func (f *File) Expire(c context.Context, ns string, s Selector) (n int, err error) {
	err = f.mutate(c, func(m *Memory) error { var e error; n, e = m.Expire(c, ns, s); return e })
	return
}
func (f *File) MarkStale(c context.Context, ns string, s Selector, w time.Duration) (n int, err error) {
	err = f.mutate(c, func(m *Memory) error { var e error; n, e = m.MarkStale(c, ns, s, w); return e })
	return
}
func (f *File) Stats() (int, int64) { f.gate <- struct{}{}; defer f.leave(); return f.memory.Stats() }
func (f *File) Usage() Usage        { f.gate <- struct{}{}; defer f.leave(); return f.memory.Usage() }

type diskEntry struct {
	ID                     ID
	Policy                 Policy
	Value                  []byte
	Generation             string
	Loaded, Fresh, Expires time.Time
	NoExpiry               bool
}
type diskRecord struct {
	Kind   string
	ID     ID
	Entry  *diskEntry      `json:",omitempty"`
	Ticket *diskSeedTicket `json:",omitempty"`
}

type diskSeedTicket struct{ CreatedAt time.Time }

func persisted(e *memoryEntry) *diskEntry {
	return &diskEntry{e.id, e.policy, e.value, e.generation, e.loaded.UTC(), e.fresh.UTC(), e.expires.UTC(), e.noExpiry}
}
func diskChanges(old, next *Memory) []diskRecord {
	var deletes, puts []diskRecord
	for id, elem := range old.entries {
		a := elem.Value.(*memoryEntry)
		if !a.found {
			continue
		}
		b := next.entries[id]
		if b == nil || !b.Value.(*memoryEntry).found {
			deletes = append(deletes, diskRecord{Kind: "delete", ID: id})
		}
	}
	for id, elem := range next.entries {
		b := elem.Value.(*memoryEntry)
		if !b.found {
			continue
		}
		a := old.entries[id]
		if a == nil || !a.Value.(*memoryEntry).found || a.Value.(*memoryEntry).generation != b.generation {
			puts = append(puts, diskRecord{Kind: "put", ID: id, Entry: persisted(b)})
		} else {
			v := a.Value.(*memoryEntry)
			if !v.fresh.Equal(b.fresh) || !v.expires.Equal(b.expires) || v.noExpiry != b.noExpiry {
				d := persisted(b)
				d.Value = nil
				puts = append(puts, diskRecord{Kind: "mark", ID: id, Entry: d})
			}
		}
	}
	less := func(a, b diskRecord) bool {
		if a.ID.Namespace != b.ID.Namespace {
			return a.ID.Namespace < b.ID.Namespace
		}
		if a.ID.Schema != b.ID.Schema {
			return a.ID.Schema < b.ID.Schema
		}
		return a.ID.Key < b.ID.Key
	}
	sort.Slice(deletes, func(i, j int) bool { return less(deletes[i], deletes[j]) })
	sort.Slice(puts, func(i, j int) bool { return less(puts[i], puts[j]) })
	for token, ticket := range old.seedTickets {
		if _, ok := next.seedTickets[token]; !ok {
			deletes = append(deletes, diskRecord{Kind: "seed-delete", ID: ID{ticket.namespace, "seed", token}})
		}
	}
	for token, ticket := range next.seedTickets {
		if _, ok := old.seedTickets[token]; !ok {
			puts = append(puts, diskRecord{Kind: "seed-put", ID: ID{ticket.namespace, "seed", token}, Ticket: &diskSeedTicket{ticket.created.UTC()}})
		}
	}
	sort.Slice(deletes, func(i, j int) bool { return less(deletes[i], deletes[j]) })
	sort.Slice(puts, func(i, j int) bool { return less(puts[i], puts[j]) })
	return append(deletes, puts...)
}
func snapshotRecords(m *Memory) []diskRecord {
	empty := &Memory{entries: map[ID]*list.Element{}}
	return diskChanges(empty, m)
}
