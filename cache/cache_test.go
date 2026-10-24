package cache

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTTLAndLRU(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	s := New(2, WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	var loads int
	load := func(context.Context) (any, error) { loads++; return loads, nil }
	get := func(key string) int {
		t.Helper()
		v, err := s.GetOrLoad(context.Background(), key, Options{TTL: time.Minute}, load)
		if err != nil {
			t.Fatal(err)
		}
		return v.(int)
	}
	if get("a") != 1 || get("b") != 2 || get("a") != 1 {
		t.Fatal("cache misses or hits wrong")
	}
	get("c") // a was recently read, so b must be evicted.
	if get("a") != 1 || get("b") != 4 || s.Len() != 2 {
		t.Fatal("LRU eviction failed")
	}
	clock.Add(int64(time.Minute)) // Expiration is exclusive at its boundary.
	if s.Len() != 0 {
		t.Fatal("expired entries were counted")
	}
	if get("b") != 5 {
		t.Fatal("expired value returned")
	}
}

func TestTTLStartsAtCompletionAndPrunesExpiredBeforeEviction(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	s := New(2, WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	get := func(key string, ttl time.Duration, load Loader) any {
		t.Helper()
		v, err := s.GetOrLoad(context.Background(), key, Options{TTL: ttl}, load)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	get("live", time.Hour, func(context.Context) (any, error) { return "live", nil })
	get("short", time.Minute, func(context.Context) (any, error) {
		clock.Add(int64(time.Hour))
		return "short", nil
	})
	if get("short", time.Minute, func(context.Context) (any, error) { return "wrong", nil }) != "short" {
		t.Fatal("load time consumed TTL")
	}
	// Restore a live long-lived entry, then make the soon-expiring entry most
	// recently used. It must be pruned before the still-live LRU is evicted.
	get("live", time.Hour, func(context.Context) (any, error) { return "live2", nil })
	get("short", time.Minute, func(context.Context) (any, error) { return "wrong", nil })
	clock.Add(int64(time.Minute))
	get("third", time.Hour, func(context.Context) (any, error) { return "third", nil })
	if get("live", time.Hour, func(context.Context) (any, error) { return "wrong", nil }) != "live2" {
		t.Fatal("expired entry displaced live LRU entry")
	}
}

func TestErrorsPanicsAndZeroTTLAreNotCached(t *testing.T) {
	s := New(2)
	ctx := context.Background()
	want := errors.New("load failed")
	for i := 0; i < 2; i++ {
		_, err := s.GetOrLoad(ctx, "error", Options{TTL: time.Minute}, func(context.Context) (any, error) { return nil, want })
		if !errors.Is(err, want) {
			t.Fatalf("error = %v", err)
		}
		_, err = s.GetOrLoad(ctx, "panic", Options{TTL: time.Minute}, func(context.Context) (any, error) { panic("bad load") })
		if err == nil {
			t.Fatal("panic was not reported")
		}
	}
	loads := 0
	for i := 0; i < 2; i++ {
		_, err := s.GetOrLoad(ctx, "zero", Options{}, func(context.Context) (any, error) { loads++; return loads, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	if loads != 2 || s.Len() != 0 {
		t.Fatal("non-cacheable results retained")
	}
}

func TestConcurrentCallsCoalesce(t *testing.T) {
	s := New(4)
	var calls atomic.Int32
	release := make(chan struct{})
	loader := func(context.Context) (any, error) { calls.Add(1); <-release; return "result", nil }
	const count = 100
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := s.GetOrLoad(context.Background(), "same", Options{TTL: time.Minute}, loader)
			if err != nil || v != "result" {
				t.Errorf("result = %v, %v", v, err)
			}
		}()
	}
	waitWaiters(t, s, "same", count)
	close(release)
	wg.Wait()
	if calls.Load() != 1 || s.Len() != 1 {
		t.Fatalf("calls %d, length %d", calls.Load(), s.Len())
	}
}

func TestCancellationDoesNotPoisonOtherWaiters(t *testing.T) {
	s := New(2)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), contextKey{}, "retained"))
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	first := make(chan error, 1)
	second := make(chan error, 1)
	loader := func(ctx context.Context) (any, error) { started <- ctx; <-release; return "ok", nil }
	go func() { _, err := s.GetOrLoad(ctx, "shared", Options{TTL: time.Minute}, loader); first <- err }()
	loadCtx := <-started
	go func() {
		_, err := s.GetOrLoad(context.Background(), "shared", Options{TTL: time.Minute}, loader)
		second <- err
	}()
	waitWaiters(t, s, "shared", 2)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first error = %v", err)
	}
	if loadCtx.Err() != nil || loadCtx.Value(contextKey{}) != "retained" {
		t.Fatal("shared context canceled or lost values")
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatal("successful shared result not cached")
	}
}

type contextKey struct{}

func TestAllCanceledStartsNewGeneration(t *testing.T) {
	s := New(2)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan context.Context, 1)
	oldRelease := make(chan struct{})
	oldFinished := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := s.GetOrLoad(ctx, "key", Options{TTL: time.Minute}, func(ctx context.Context) (any, error) {
			started <- ctx
			<-oldRelease // Deliberately demonstrate a loader slow to honor cancellation.
			defer close(oldFinished)
			return "old", nil
		})
		result <- err
	}()
	loadCtx := <-started
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if loadCtx.Err() != context.Canceled {
		t.Fatal("unused shared load not canceled")
	}
	value, err := s.GetOrLoad(context.Background(), "key", Options{TTL: time.Minute}, func(context.Context) (any, error) { return "new", nil })
	if err != nil || value != "new" {
		t.Fatalf("new result = %v, %v", value, err)
	}
	close(oldRelease)
	<-oldFinished
	value, err = s.GetOrLoad(context.Background(), "key", Options{TTL: time.Minute}, func(context.Context) (any, error) { return "wrong", nil })
	if err != nil || value != "new" {
		t.Fatal("canceled generation poisoned cache")
	}
}

func TestInvalidationLabels(t *testing.T) {
	s := New(8)
	ctx := context.Background()
	put := func(key string, options Options) {
		t.Helper()
		options.TTL = time.Hour
		if _, err := s.GetOrLoad(ctx, key, options, func(context.Context) (any, error) { return key, nil }); err != nil {
			t.Fatal(err)
		}
	}
	put("one", Options{Tags: []string{"posts"}, Paths: []string{"/posts/a?sort=asc"}})
	put("two", Options{Tags: []string{"posts"}, Paths: []string{"/posts/b/"}})
	put("three", Options{Tags: []string{"users"}, Paths: []string{"/posts-old"}})
	if got := s.InvalidatePath("//posts//a/#part"); got != 1 {
		t.Fatalf("path invalidated %d", got)
	}
	if got := s.InvalidatePathPrefix("/posts/"); got != 1 {
		t.Fatalf("prefix invalidated %d", got)
	}
	if s.Len() != 1 || s.InvalidateTag("posts") != 0 || s.InvalidateTag("users") != 1 {
		t.Fatal("label invalidation mismatch")
	}
	put("four", Options{})
	if s.InvalidateKey("missing") != 0 || s.InvalidateKey("four") != 1 {
		t.Fatal("key invalidation mismatch")
	}
	put("five", Options{})
	s.Clear()
	if s.Len() != 0 {
		t.Fatal("clear retained entries")
	}
}

func TestInvalidateDuringFlightCannotRepopulate(t *testing.T) {
	for _, mode := range []string{"key", "tag", "path", "prefix", "clear"} {
		t.Run(mode, func(t *testing.T) {
			s := New(2)
			options := Options{TTL: time.Hour, Tags: []string{"tag"}, Paths: []string{"/posts/a"}}
			started, release, result := make(chan struct{}), make(chan struct{}), make(chan any, 1)
			go func() {
				value, _ := s.GetOrLoad(context.Background(), "key", options, func(context.Context) (any, error) { close(started); <-release; return "old", nil })
				result <- value
			}()
			<-started
			switch mode {
			case "key":
				s.InvalidateKey("key")
			case "tag":
				s.InvalidateTag("tag")
			case "path":
				s.InvalidatePath("/posts/a")
			case "prefix":
				s.InvalidatePathPrefix("/posts")
			case "clear":
				s.Clear()
			}
			value, err := s.GetOrLoad(context.Background(), "key", options, func(context.Context) (any, error) { return "new", nil })
			if err != nil || value != "new" {
				t.Fatal("new caller joined invalidated load")
			}
			close(release)
			if <-result != "old" {
				t.Fatal("existing waiter lost its result")
			}
			value, err = s.GetOrLoad(context.Background(), "key", options, func(context.Context) (any, error) { return "wrong", nil })
			if err != nil || value != "new" {
				t.Fatal("old flight overwrote new cached value")
			}
		})
	}
}

func TestConcurrentLoaderLimitAndQueueCancellation(t *testing.T) {
	s := New(10, WithMaxConcurrentLoads(1))
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.GetOrLoad(context.Background(), "first", Options{}, func(context.Context) (any, error) { close(started); <-release; return 1, nil })
		done <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	var called atomic.Bool
	queuedDone := make(chan error, 1)
	go func() {
		_, err := s.GetOrLoad(ctx, "queued", Options{}, func(context.Context) (any, error) { called.Store(true); return 2, nil })
		queuedDone <- err
	}()
	waitWaiters(t, s, "queued", 1)
	cancel()
	if err := <-queuedDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if called.Load() {
		t.Fatal("canceled queued loader ran")
	}
}

func TestTypedValuesAndCanceledHit(t *testing.T) {
	s := New(2)
	options := Options{TTL: time.Minute}
	for i := 0; i < 2; i++ {
		v, err := GetOrLoad[any](context.Background(), s, "nil", options, func(context.Context) (any, error) { return nil, nil })
		if err != nil || v != nil {
			t.Fatalf("typed nil = %v, %v", v, err)
		}
	}
	_, err := GetOrLoad[string](context.Background(), s, "nil", options, func(context.Context) (string, error) { return "bad", nil })
	if err == nil {
		t.Fatal("inconsistent typed key accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.GetOrLoad(ctx, "nil", options, func(context.Context) (any, error) { return "bad", nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled hit returned value")
	}
}

func TestConcurrentMutation(t *testing.T) {
	s := New(7)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := fmt.Sprintf("key-%d", (worker+j)%11)
				_, err := s.GetOrLoad(context.Background(), key, Options{TTL: time.Minute, Tags: []string{"all"}}, func(context.Context) (any, error) { return key, nil })
				if err != nil {
					t.Error(err)
				}
				if j%7 == 0 {
					s.InvalidateKey(key)
				}
				if j%13 == 0 {
					s.InvalidateTag("all")
				}
				if j%11 == 0 {
					s.ExpireKey(key)
				}
				if j%17 == 0 {
					s.ExpireTag("all")
				}
				if j%19 == 0 {
					if _, err := s.MarkStaleTag("all", time.Minute); err != nil {
						t.Error(err)
					}
				}
				if j%23 == 0 {
					if _, err := s.MarkStaleKey(key, 0); err != nil {
						t.Error(err)
					}
				}
				if s.Len() > 7 {
					t.Error("capacity exceeded")
				}
			}
		}(i)
	}
	wg.Wait()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.awaiting) != 0 || len(s.flights) != 0 {
		t.Fatal("finished readers retained", len(s.awaiting), len(s.flights))
	}
}

func waitWaiters(t *testing.T, store *Store, key string, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		store.mu.Lock()
		f := store.flights[key]
		ready := f != nil && f.waiters == count
		store.mu.Unlock()
		if ready {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("key %s did not reach %d waiters", key, count)
}
