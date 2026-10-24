package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBootstrapPreservesAgeAndRefreshes(t *testing.T) {
	s, clock, q, opts := lifetimeStore()
	generated := time.Unix(0, clock.Load()).Add(-11 * time.Second)
	if n, err := Bootstrap(context.Background(), s, []Seed[string]{{Key: "seed", Value: "built", LoadedAt: generated, Options: opts}}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	calls := 0
	load := func(context.Context) (string, error) { calls++; return "runtime", nil }
	for i := 0; i < 10; i++ {
		v, info, err := Lookup(context.Background(), s, "seed", opts, load)
		if err != nil || v != "built" || info.State != Stale || info.Age != 11*time.Second {
			t.Fatal(v, info, err)
		}
	}
	if q.count() != 1 || calls != 0 {
		t.Fatal("refresh not coalesced", q.count(), calls)
	}
	if err := q.pop(t).complete(); err != nil {
		t.Fatal(err)
	}
	v, info, err := Lookup(context.Background(), s, "seed", opts, load)
	if err != nil || v != "runtime" || info.State != Fresh || calls != 1 {
		t.Fatal(v, info, err, calls)
	}
	s.ExpireTag("posts")
	v, err = GetOrLoad(context.Background(), s, "seed", opts, load)
	if err != nil || v != "runtime" || calls != 2 {
		t.Fatal(v, err, calls)
	}
	if _, err := Bootstrap(context.Background(), s, []Seed[string]{{Key: "seed", Value: "obsolete", LoadedAt: generated, Options: opts}}); !errors.Is(err, ErrAlreadyUsed) {
		t.Fatal(err)
	}
}
func TestBootstrapNeverResurrectsAfterUse(t *testing.T) {
	for name, use := range map[string]func(*Store){
		"clear":     func(s *Store) { s.ExpireAll() },
		"tag miss":  func(s *Store) { s.ExpireTag("missing") },
		"path miss": func(s *Store) { s.ExpirePathPrefix("/missing") },
		"failed load": func(s *Store) {
			_, _ = s.GetOrLoad(context.Background(), "x", Options{}, func(context.Context) (any, error) { return nil, errors.New("failed") })
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := New(2)
			use(s)
			if _, err := Bootstrap(context.Background(), s, []Seed[string]{{Key: "x", Value: "old", LoadedAt: time.Now(), Options: Options{TTL: time.Hour}}}); !errors.Is(err, ErrAlreadyUsed) {
				t.Fatal(err)
			}
		})
	}
}
func TestBootstrapValidationIsAtomicAndBounded(t *testing.T) {
	now := time.Now()
	s := New(2, WithMaxBytes(600, nil))
	opts := Options{TTL: time.Hour}
	seeds := []Seed[string]{{"x", "x", now, opts}, {"x", "duplicate", now, opts}}
	if _, err := Bootstrap(context.Background(), s, seeds); err == nil || s.Len() != 0 {
		t.Fatal("partial malformed batch", err)
	}
	seeds = []Seed[string]{{"expired", "expired", now.Add(-2 * time.Hour), opts}, {"big", string(make([]byte, 1000)), now, opts}, {"a", "a", now, opts}, {"b", "b", now, opts}, {"c", "c", now, opts}}
	n, err := Bootstrap(context.Background(), s, seeds)
	if err != nil || n != 2 || s.Bytes() > 600 {
		t.Fatal(n, err, s.Bytes())
	}
	v, info, err := Lookup(context.Background(), s, "c", opts, func(context.Context) (string, error) { t.Fatal("retained seed reloaded"); return "", nil })
	if err != nil || v != "c" || info.State != Fresh {
		t.Fatal(v, info, err)
	}
	panicStore := New(2, WithMaxBytes(1024, func(any) int64 { panic("weigher") }))
	if n, err := Bootstrap(context.Background(), panicStore, seeds[:1]); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	unused := New(1)
	if _, err := Bootstrap(canceled, unused, seeds[:1]); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := Bootstrap(context.Background(), unused, seeds[2:3]); err != nil {
		t.Fatal("canceled bootstrap consumed store", err)
	}
}
func TestBootstrapConcurrentReadAndInvalidation(t *testing.T) {
	for i := 0; i < 100; i++ {
		s := New(2)
		var start sync.WaitGroup
		start.Add(1)
		var wg sync.WaitGroup
		wg.Add(3)
		var reads atomic.Int64
		go func() {
			defer wg.Done()
			start.Wait()
			_, err := Bootstrap(context.Background(), s, []Seed[string]{{"x", "build", time.Now(), Options{TTL: time.Hour}}})
			if err != nil && !errors.Is(err, ErrAlreadyUsed) {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			start.Wait()
			_, err := GetOrLoad(context.Background(), s, "x", Options{TTL: time.Hour}, func(context.Context) (string, error) { reads.Add(1); return "runtime", nil })
			if err != nil {
				t.Error(err)
			}
		}()
		go func() { defer wg.Done(); start.Wait(); s.ExpireAll() }()
		start.Done()
		wg.Wait()
		s.ExpireAll()
		v, err := GetOrLoad(context.Background(), s, "x", Options{TTL: time.Hour}, func(context.Context) (string, error) { return "final", nil })
		if err != nil || v != "final" {
			t.Fatal(v, err, reads.Load())
		}
	}
}
