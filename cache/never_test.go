package cache

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestNeverLifetimeRefreshesWithoutHardExpiry(t *testing.T) {
	var clock atomic.Value
	clock.Store(time.Time{})
	s := New(4, WithClock(func() time.Time { return clock.Load().(time.Time) }), WithMaxBytes(4096, nil))
	queue := &refreshQueue{}
	options := Options{Lifetime: &Lifetime{Revalidate: time.Minute, Expire: Never}, ScheduleRefresh: queue.schedule, Tags: []string{"posts"}}
	broken := false
	calls := 0
	failure := errors.New("unavailable")
	load := func(context.Context) (string, error) {
		calls++
		if broken {
			return "", failure
		}
		if calls == 1 {
			return "old", nil
		}
		return "new", nil
	}
	value, info, err := Lookup(context.Background(), s, "key", options, load)
	if err != nil || value != "old" || !info.NoExpiry || info.ExpiresIn != 0 || info.FreshFor != time.Minute || info.ReuseFor(5*time.Minute) != 5*time.Minute {
		t.Fatal(value, info, err)
	}
	bytes := s.Bytes()
	clock.Store(time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC))
	if s.Len() != 1 || s.Bytes() != bytes || bytes <= 0 {
		t.Fatal("non-expiring entry was pruned")
	}
	broken = true
	for i := 0; i < 2; i++ {
		value, info, err = Lookup(context.Background(), s, "key", options, load)
		if err != nil || value != "old" || info.State != Stale || !info.NoExpiry || info.ExpiresIn != 0 || info.ReuseFor(MaxClientStale) != 0 {
			t.Fatal(value, info, err)
		}
		if err := queue.pop(t).complete(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if s.Len() != 1 || s.Bytes() != bytes {
			t.Fatal("transient failure lost retained entry")
		}
	}
	broken = false
	_, _, _ = Lookup(context.Background(), s, "key", options, load)
	if err := queue.pop(t).complete(); err != nil {
		t.Fatal(err)
	}
	value, info, err = Lookup(context.Background(), s, "key", options, load)
	if err != nil || value != "new" || !info.NoExpiry || info.Age != 0 || info.State != Fresh {
		t.Fatal(value, info, err)
	}
	options.KeepStaleOnError = func(error) bool { return false }
	broken = true
	clock.Store(clock.Load().(time.Time).Add(time.Minute))
	_, _, _ = Lookup(context.Background(), s, "key", options, load)
	if err := queue.pop(t).complete(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if s.Len() != 0 || s.Bytes() != 0 {
		t.Fatal("permanent failure retained non-expiring value")
	}
}

func TestNeverMarksPreserveOrClipOriginalDeadline(t *testing.T) {
	for _, expire := range []time.Duration{Never, time.Minute} {
		t.Run(expire.String(), func(t *testing.T) {
			s, clock, queue, options := lifetimeStore()
			options.Lifetime.Expire = expire
			load := func(context.Context) (string, error) { return "old", nil }
			_, _, _ = Lookup(context.Background(), s, "key", options, load)
			clock.Add(int64(2 * time.Second))
			if n, err := s.MarkStaleTag("posts", Never); err != nil || n != 1 {
				t.Fatal(n, err)
			}
			_, info, err := Lookup(context.Background(), s, "key", options, load)
			if err != nil || info.State != Stale || info.Age != 2*time.Second || info.NoExpiry != (expire == Never) {
				t.Fatal(info, err)
			}
			if expire != Never && info.ExpiresIn != 58*time.Second {
				t.Fatal("unlimited mark extended finite deadline", info)
			}
			old := queue.pop(t)
			_, _ = s.MarkStaleKey("key", 5*time.Second)
			_, info, _ = Lookup(context.Background(), s, "key", options, load)
			if info.NoExpiry || info.ExpiresIn != 5*time.Second {
				t.Fatal("finite mark did not clip", info)
			}
			current := queue.pop(t)
			if err := old.complete(); err != nil {
				t.Fatal(err)
			}
			_, _ = s.MarkStalePathPrefix("/posts", Never)
			_, info, _ = Lookup(context.Background(), s, "key", options, load)
			if info.NoExpiry || info.ExpiresIn != 5*time.Second {
				t.Fatal("later mark restored infinity", info)
			}
			// Keep all reserved jobs unexecuted until hard expiry. None may resurrect it.
			clock.Add(int64(5 * time.Second))
			if s.Len() != 0 || s.Bytes() != 0 {
				t.Fatal("clipped entry not pruned")
			}
			if err := current.complete(); err != nil {
				t.Fatal(err)
			}
			if err := queue.pop(t).complete(); err != nil {
				t.Fatal(err)
			}
			value, info, err := Lookup(context.Background(), s, "key", options, load)
			if err != nil || value != "old" || info.State != Miss || info.NoExpiry != (expire == Never) {
				t.Fatal(value, info, err)
			}
			if n := s.ExpireTag("posts"); n != 1 || s.Len() != 0 || s.Bytes() != 0 {
				t.Fatal("strict expiration failed", n)
			}
		})
	}
}

func TestNeverRetainsEntryAndByteBounds(t *testing.T) {
	s := New(2, WithMaxBytes(1024, nil))
	options := Options{Lifetime: &Lifetime{Revalidate: time.Hour, Expire: Never}}
	for _, key := range []string{"a", "b", "c"} {
		_, err := GetOrLoad(context.Background(), s, key, options, func(context.Context) (string, error) { return key, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	if s.Len() != 2 || s.Bytes() <= 0 || s.Bytes() > 1024 {
		t.Fatal(s.Len(), s.Bytes())
	}
	s.mu.Lock()
	_, oldest := s.entries["a"]
	s.mu.Unlock()
	if oldest {
		t.Fatal("non-expiring entry escaped LRU")
	}
	large := string(make([]byte, 2048))
	value, err := GetOrLoad(context.Background(), s, "large", options, func(context.Context) (string, error) { return large, nil })
	if err != nil || value != large || s.Len() != 2 || s.Bytes() > 1024 {
		t.Fatal("oversize value retained", err, s.Len(), s.Bytes())
	}
	s.ExpireAll()
	if s.Bytes() != 0 {
		t.Fatal("expiration leaked accounted bytes")
	}
}

func TestNeverValidationAndLegacyNonpositiveTTL(t *testing.T) {
	for _, life := range []Lifetime{{Revalidate: Never, Expire: Never}, {Expire: -2}, {Expire: 0}} {
		if life.Validate() == nil {
			t.Fatal("invalid lifetime", life)
		}
	}
	if (Profile{Stale: Never, Expire: Never}).Validate() == nil {
		t.Fatal("unbounded browser window accepted")
	}
	p := BuiltinProfiles()["default"]
	if p.Stale != 5*time.Minute || p.Revalidate != 15*time.Minute || p.Expire != Never || p.Validate() != nil {
		t.Fatal(p)
	}
	for _, ttl := range []time.Duration{0, Never, -2} {
		s := New(2, WithClock(func() time.Time { return time.Time{} }))
		_, info, err := Lookup(context.Background(), s, "key", Options{TTL: ttl}, func(context.Context) (string, error) { return "value", nil })
		if err != nil || info.NoExpiry || s.Len() != 0 {
			t.Fatal("legacy nonpositive TTL retained", ttl, info, err)
		}
	}
}

func TestDetachedBackgroundWithoutReadersRemainsCancellable(t *testing.T) {
	for _, mode := range []string{"expire", "mark"} {
		for _, reader := range []bool{false, true} {
			t.Run(mode+"/reader="+fmt.Sprint(reader), func(t *testing.T) {
				s, clock, queue, options := lifetimeStore()
				options.Lifetime.Expire = Never
				started := make(chan struct{})
				var calls atomic.Int64
				load := func(ctx context.Context) (string, error) {
					if calls.Add(1) == 1 {
						return "old", nil
					}
					close(started)
					<-ctx.Done()
					return "late", nil
				}
				_, _ = GetOrLoad(context.Background(), s, "key", options, load)
				clock.Add(int64(10 * time.Second))
				_, _ = GetOrLoad(context.Background(), s, "key", options, load)
				job := queue.pop(t)
				done := make(chan error, 1)
				go func() { done <- job.complete() }()
				<-started
				if reader {
					options.ScheduleRefresh = nil
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					result := make(chan error, 1)
					go func() { _, err := GetOrLoad(ctx, s, "key", options, load); result <- err }()
					waitWaiters(t, s, "key", 1)
					cancel()
					if err := <-result; !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				}
				s.InvalidateKey("key") // Intentionally detach, without canceling the job.
				select {
				case err := <-done:
					t.Fatal("weak invalidation canceled worker", err)
				default:
				}
				var affected int
				if mode == "expire" {
					affected = s.ExpireTag("posts")
				} else {
					var err error
					affected, err = s.MarkStaleTag("posts", Never)
					if err != nil {
						t.Fatal(err)
					}
				}
				if affected != 1 {
					t.Fatal("detached background generation not indexed", affected)
				}
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatal(err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("strict publication did not cancel detached background work")
				}
				s.mu.Lock()
				pending := len(s.awaiting)
				s.mu.Unlock()
				if pending != 0 || s.Len() != 0 || s.Bytes() != 0 {
					t.Fatal("completed background retained bookkeeping/value", pending)
				}
			})
		}
	}
}
