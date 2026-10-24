package shared

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMarkStaleClipsNeverAndPreservesAge(t *testing.T) {
	m, advance := memory(t, 8, 1<<20)
	b, _ := remote(t, m)
	ctx := context.Background()
	p := testPolicy
	p.ExpireAfter = Never
	l := acquire(t, b, testID, p)
	publish(t, b, testID, l.Lease, []byte("1"))
	advance(2 * time.Second)
	if n, err := b.MarkStale(ctx, "app", Selector{"tag", "posts"}, time.Second); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	marked := acquire(t, b, testID, p)
	if !marked.Found || marked.NoExpiry || marked.Age != 2*time.Second || marked.FreshFor != 0 || marked.ExpiresIn != time.Second {
		t.Fatal(marked)
	}
	advance(500 * time.Millisecond)
	for _, window := range []time.Duration{10 * time.Second, Never} {
		if n, err := b.MarkStale(ctx, "app", Selector{"prefix", "/posts"}, window); err != nil || n != 1 {
			t.Fatal(n, err)
		}
	}
	old, err := b.Commit(ctx, testID, marked.Lease, []byte("obsolete"))
	if err != nil || old.Found {
		t.Fatal(old, err)
	}
	v := acquire(t, b, testID, p)
	if v.NoExpiry || v.ExpiresIn != 500*time.Millisecond || v.Age != 2500*time.Millisecond {
		t.Fatal("mark extended/reset generation", v)
	}
	advance(500 * time.Millisecond)
	if n, err := b.MarkStale(ctx, "app", Selector{"tag", "posts"}, Never); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	next := acquire(t, b, testID, p)
	if next.Found {
		t.Fatal("expired last good survived marking", next)
	}
	if err := b.Discard(ctx, testID, next.Lease); err != nil {
		t.Fatal(err)
	}
}
func TestMarkStaleSelectorsFenceUnpublishedLeases(t *testing.T) {
	for _, kind := range []string{"all", "key", "tag", "path", "prefix"} {
		t.Run(kind, func(t *testing.T) {
			m, _ := memory(t, 8, 1<<20)
			ctx := context.Background()
			l := acquire(t, m, testID, testPolicy)
			v2 := testID
			v2.Schema = "v2"
			l2 := acquire(t, m, v2, testPolicy)
			publish(t, m, v2, l2.Lease, []byte("2"))
			other := testID
			other.Namespace = "other"
			o := acquire(t, m, other, testPolicy)
			publish(t, m, other, o.Lease, []byte("3"))
			values := map[string]string{"all": "", "key": testID.Key, "tag": "posts", "path": "//posts/a/?q#f", "prefix": "/posts/"}
			if n, err := m.MarkStale(ctx, "app", Selector{kind, values[kind]}, Never); err != nil || n != 2 {
				t.Fatal(n, err)
			}
			old, err := m.Commit(ctx, testID, l.Lease, []byte("old"))
			if err != nil || old.Found {
				t.Fatal(old, err)
			}
			fresh := acquire(t, m, v2, testPolicy)
			if !fresh.Found || fresh.FreshFor != 0 {
				t.Fatal(fresh)
			}
			unchanged, err := m.Begin(ctx, other, testPolicy, time.Second)
			if err != nil || unchanged.FreshFor != time.Second {
				t.Fatal(unchanged, err)
			}
		})
	}
}
func TestMarkStaleStrictAndInvalidWindows(t *testing.T) {
	m, _ := memory(t, 4, 1<<20)
	b, _ := remote(t, m)
	ctx := context.Background()
	l := acquire(t, b, testID, testPolicy)
	publish(t, b, testID, l.Lease, []byte("1"))
	for _, window := range []time.Duration{-2, maxDuration + 1} {
		if _, err := b.MarkStale(ctx, "app", Selector{"all", ""}, window); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := m.MarkStale(ctx, "app", Selector{"all", ""}, window); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	before, err := b.Begin(ctx, testID, testPolicy, time.Second)
	if err != nil || before.FreshFor != time.Second {
		t.Fatal(before, err)
	}
	if n, err := b.MarkStale(ctx, "app", Selector{"all", ""}, 0); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if keys, _ := m.Stats(); keys != 0 {
		t.Fatal(keys)
	}
	if _, err := store(t, m).MarkStale(nil, Selector{"all", ""}, Never); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
