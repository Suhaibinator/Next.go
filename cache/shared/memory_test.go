package shared

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var testPolicy = Policy{FreshFor: time.Second, ExpireAfter: 5 * time.Second, Tags: []string{"posts"}, Paths: []string{"/posts/a"}}
var testID = ID{"app", "v1", "post:a"}

func memory(t *testing.T, capacity int, budget int64) (*Memory, func(time.Duration)) {
	t.Helper()
	m, err := NewMemory(capacity, budget)
	if err != nil {
		t.Fatal(err)
	}
	var nanos atomic.Int64
	origin := time.Now()
	m.now = func() time.Time { return origin.Add(time.Duration(nanos.Load())) }
	return m, func(d time.Duration) { nanos.Add(int64(d)) }
}
func acquire(t *testing.T, b Backend, id ID, p Policy) View {
	t.Helper()
	v, err := b.Begin(context.Background(), id, p, time.Second)
	if err != nil || !validToken(v.Lease) || v.Busy {
		t.Fatalf("acquire: %+v %v", v, err)
	}
	return v
}
func publish(t *testing.T, b Backend, id ID, token string, data []byte) View {
	t.Helper()
	v, err := b.Commit(context.Background(), id, token, data)
	if err != nil || !v.Found || v.Lease != "" || v.Busy {
		t.Fatalf("publish: %+v %v", v, err)
	}
	return v
}
func TestMemoryAgesOwnershipPolicyAndExpiry(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	ctx := context.Background()
	p := testPolicy
	p.Tags = []string{"z", "posts", "posts"}
	p.Paths = []string{"//posts/a/?discard#fragment"}
	lease := acquire(t, m, testID, p)
	// Mutation after Begin must not change stored policy or labels.
	p.Tags[0] = "changed"
	p.Paths[0] = "/other"
	data := []byte("hello")
	result := publish(t, m, testID, lease.Lease, data)
	data[0] = 'x'
	result.Value[1] = 'x'
	canonical := testPolicy
	canonical.Tags = []string{"posts", "z"}
	v, err := m.Begin(ctx, testID, canonical, time.Second)
	if err != nil || string(v.Value) != "hello" || v.Age != 0 || v.FreshFor != time.Second || v.ExpiresIn != 5*time.Second {
		t.Fatal(v, err)
	}
	v.Value[0] = 'x'
	advance(1500 * time.Millisecond)
	stale := acquire(t, m, testID, canonical)
	if !stale.Found || string(stale.Value) != "hello" || stale.Age != 1500*time.Millisecond || stale.FreshFor != 0 || stale.ExpiresIn != 3500*time.Millisecond {
		t.Fatal(stale)
	}
	busy, err := m.Begin(ctx, testID, canonical, time.Second)
	if err != nil || !busy.Busy || busy.Lease != "" || busy.Found || len(busy.Value) != 0 {
		t.Fatal(busy, err)
	}
	changed := canonical
	changed.FreshFor = 2 * time.Second
	if _, err := m.Begin(ctx, testID, changed, time.Second); !errors.Is(err, ErrPolicy) {
		t.Fatal(err)
	}
	advance(4 * time.Second)
	next := acquire(t, m, testID, canonical)
	if next.Found || next.Lease == stale.Lease {
		t.Fatal(next)
	}
	old, err := m.Commit(ctx, testID, stale.Lease, []byte("old"))
	if err != nil || old.Found {
		t.Fatal(old, err)
	}
	fresh := publish(t, m, testID, next.Lease, []byte("new"))
	if fresh.Age != 0 || fresh.ExpiresIn != 5*time.Second {
		t.Fatal(fresh)
	}
}
func TestMemoryInvalidationFencesPendingAndPublishedSchemas(t *testing.T) {
	for _, kind := range []string{"all", "key", "tag", "path", "prefix"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := memory(t, 8, 1<<20)
			ctx := context.Background()
			pending := acquire(t, m, testID, testPolicy)
			v2 := testID
			v2.Schema = "v2"
			p := acquire(t, m, v2, testPolicy)
			publish(t, m, v2, p.Lease, []byte("v2"))
			other := testID
			other.Namespace = "other"
			o := acquire(t, m, other, testPolicy)
			publish(t, m, other, o.Lease, []byte("tenant"))
			values := map[string]string{"all": "", "key": testID.Key, "tag": "posts", "path": "//posts/a/?ignored#f", "prefix": "/posts/"}
			n, err := m.Expire(ctx, "app", Selector{kind, values[kind]})
			if err != nil || n != 2 {
				t.Fatal(n, err)
			}
			replacement := acquire(t, m, testID, testPolicy)
			if err := m.Release(ctx, testID, pending.Lease); err != nil {
				t.Fatal(err)
			}
			old, err := m.Commit(ctx, testID, pending.Lease, []byte("obsolete"))
			if err != nil || old.Found {
				t.Fatal(old, err)
			}
			publish(t, m, testID, replacement.Lease, []byte("replacement"))
			kept, err := m.Begin(ctx, other, testPolicy, time.Second)
			if err != nil || string(kept.Value) != "tenant" {
				t.Fatal(kept, err)
			}
			if count, _ := m.Stats(); count != 2 {
				t.Fatal(count)
			}
		})
	}
}
func TestMemoryPrefixBoundariesAndEmptyRoot(t *testing.T) {
	m, _ := memory(t, 8, 1<<20)
	ctx := context.Background()
	for _, path := range []string{"/posts", "/posts/a", "/postscript", "/"} {
		id := testID
		id.Key = path
		p := testPolicy
		p.Paths = []string{path}
		l := acquire(t, m, id, p)
		publish(t, m, id, l.Lease, nil)
	}
	if n, err := m.Expire(ctx, "app", Selector{"prefix", "posts"}); err != nil || n != 2 {
		t.Fatal(n, err)
	}
	if n, err := m.Expire(ctx, "app", Selector{"prefix", "/"}); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}
func TestMemoryLeaseLossCapacityAndEviction(t *testing.T) {
	m, advance := memory(t, 1, 4096)
	ctx := context.Background()
	first := acquire(t, m, testID, testPolicy)
	other := testID
	other.Key = "other"
	if _, err := m.Begin(ctx, other, testPolicy, time.Second); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	advance(time.Second)
	second := acquire(t, m, other, testPolicy)
	old, err := m.Commit(ctx, testID, first.Lease, []byte("expired"))
	if err != nil || old.Found {
		t.Fatal(old, err)
	}
	if err := m.Release(ctx, other, second.Lease); err != nil {
		t.Fatal(err)
	}
	next := acquire(t, m, testID, testPolicy)
	publish(t, m, testID, next.Lease, []byte("small"))
	advance(2 * time.Second)
	refresh := acquire(t, m, testID, testPolicy)
	if _, err := m.Commit(ctx, testID, refresh.Lease, make([]byte, 4096)); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	if err := m.Release(ctx, testID, refresh.Lease); err != nil {
		t.Fatal(err)
	}
	again := acquire(t, m, testID, testPolicy)
	if string(again.Value) != "small" {
		t.Fatal("oversized replacement discarded last value", again)
	}
	publish(t, m, testID, again.Lease, []byte("x"))
	keys, used := m.Stats()
	if keys != 1 || used != metadataCost(testID, testPolicy)+1 || used > 4096 {
		t.Fatal(keys, used)
	}
}
func TestMemoryNeverAndCancellation(t *testing.T) {
	m, advance := memory(t, 2, 1<<20)
	p := testPolicy
	p.ExpireAfter = Never
	l := acquire(t, m, testID, p)
	publish(t, m, testID, l.Lease, nil)
	advance(20 * 365 * 24 * time.Hour)
	v := acquire(t, m, testID, p)
	if !v.Found || !v.NoExpiry || v.ExpiresIn != 0 {
		t.Fatal(v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Begin(ctx, testID, p, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := m.Commit(ctx, testID, v.Lease, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := m.Release(ctx, testID, v.Lease); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := m.Expire(ctx, "app", Selector{"all", ""}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	busy, err := m.Begin(context.Background(), testID, p, time.Second)
	if err != nil || !busy.Busy {
		t.Fatal(busy, err)
	}
}
func TestMemoryInvalidOperations(t *testing.T) {
	m, _ := memory(t, 2, 1<<20)
	ctx := context.Background()
	if _, err := NewMemory(0, 1); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for i, id := range []ID{{"", "v1", "key"}, {"a/b", "v1", "k"}, {"app", "v1", ""}, {"app", "v1", strings.Repeat("k", 4097)}, {"app", "v1", "bad\x00"}} {
		if _, err := m.Begin(ctx, id, testPolicy, time.Second); !errors.Is(err, ErrInvalid) {
			t.Fatal(i, err)
		}
	}
	for i, p := range []Policy{{}, {FreshFor: -1, ExpireAfter: time.Second}, {FreshFor: 2 * time.Second, ExpireAfter: time.Second}, {FreshFor: time.Hour, ExpireAfter: -2}, {FreshFor: time.Hour, ExpireAfter: 2 * time.Hour, Tags: make([]string, 65)}, {ExpireAfter: time.Second, Paths: []string{""}}} {
		if _, err := m.Begin(ctx, testID, p, time.Second); !errors.Is(err, ErrInvalid) {
			t.Fatal(i, err)
		}
	}
	for _, d := range []time.Duration{0, time.Millisecond, 2 * time.Minute} {
		if _, err := m.Begin(ctx, testID, testPolicy, d); !errors.Is(err, ErrInvalid) {
			t.Fatal(d, err)
		}
	}
	for _, selector := range []Selector{{"unknown", ""}, {"all", "x"}, {"path", ""}, {"tag", strings.Repeat("x", 513)}} {
		if _, err := m.Expire(ctx, "app", selector); !errors.Is(err, ErrInvalid) {
			t.Fatal(selector, err)
		}
	}
	l := acquire(t, m, testID, testPolicy)
	for _, token := range []string{"", strings.Repeat("x", 64)} {
		if _, err := m.Commit(ctx, testID, token, nil); !errors.Is(err, ErrInvalid) {
			t.Fatal(token, err)
		}
	}
	if _, err := m.Commit(ctx, testID, l.Lease, make([]byte, MaxValueBytes+1)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if keys, _ := m.Stats(); keys != 1 {
		t.Fatal(fmt.Sprint(keys))
	}
}

func TestMemoryRestartNeverAcceptsOldLease(t *testing.T) {
	before, _ := memory(t, 2, 1<<20)
	old := acquire(t, before, testID, testPolicy)
	after, _ := memory(t, 2, 1<<20)
	current := acquire(t, after, testID, testPolicy)
	if old.Lease == current.Lease {
		t.Fatal("lease token reused across storage loss")
	}
	v, err := after.Commit(context.Background(), testID, old.Lease, []byte("old"))
	if err != nil || v.Found {
		t.Fatal(v, err)
	}
	publish(t, after, testID, current.Lease, []byte("new"))
}
