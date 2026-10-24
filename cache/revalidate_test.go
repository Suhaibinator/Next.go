package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestMarkStaleLabelsRefreshOnRead(t *testing.T) {
	for _, mode := range []string{"key", "tag", "path", "prefix"} {
		t.Run(mode, func(t *testing.T) {
			s, clock, queue, options := lifetimeStore()
			calls := 0
			load := func(context.Context) (string, error) {
				calls++
				if calls == 1 {
					return "old", nil
				}
				return "new", nil
			}
			_, _, _ = Lookup(context.Background(), s, "key", options, load)
			bytes := s.Bytes()
			clock.Add(int64(2 * time.Second))
			var affected int
			var err error
			switch mode {
			case "key":
				affected, err = s.MarkStaleKey("key", time.Minute)
			case "tag":
				affected, err = s.MarkStaleTag("posts", time.Minute)
			case "path":
				affected, err = s.MarkStalePath("//posts/?q=1#x", time.Minute)
			case "prefix":
				affected, err = s.MarkStalePathPrefix("/posts/", time.Minute)
			}
			if err != nil || affected != 1 || calls != 1 || queue.count() != 0 || s.Bytes() != bytes {
				t.Fatal("mark eagerly loaded/removed entry", affected, err, calls)
			}
			for i := 0; i < 5; i++ {
				value, info, err := Lookup(context.Background(), s, "key", options, load)
				if err != nil || value != "old" || info.State != Stale || info.Age != 2*time.Second || info.ExpiresIn != 58*time.Second || info.ReuseFor(3*time.Second) != time.Second {
					t.Fatal(value, info, err)
				}
			}
			if queue.count() != 1 || calls != 1 {
				t.Fatal("refresh not coalesced")
			}
			if err := queue.pop(t).complete(); err != nil {
				t.Fatal(err)
			}
			value, info, err := Lookup(context.Background(), s, "key", options, load)
			if err != nil || value != "new" || info.State != Fresh || info.Age != 0 || calls != 2 {
				t.Fatal(value, info, err, calls)
			}
		})
	}
}

func TestMarkStaleDeadlineNeverExtendsAndErrorsDoNotRevive(t *testing.T) {
	s, clock, queue, options := lifetimeStore()
	broken := false
	failure := errors.New("offline")
	load := func(context.Context) (string, error) {
		if broken {
			return "", failure
		}
		return "old", nil
	}
	_, _, _ = Lookup(context.Background(), s, "key", options, load)
	clock.Add(int64(2 * time.Second))
	_, _ = s.MarkStaleKey("key", 5*time.Second)
	clock.Add(int64(time.Second))
	_, _ = s.MarkStaleKey("key", time.Hour)
	broken = true
	value, info, err := Lookup(context.Background(), s, "key", options, load)
	if err != nil || value != "old" || info.ExpiresIn != 4*time.Second || info.Age != 3*time.Second {
		t.Fatal(value, info, err)
	}
	if err := queue.pop(t).complete(); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	clock.Add(int64(4 * time.Second))
	value, _, err = Lookup(context.Background(), s, "key", options, load)
	if !errors.Is(err, failure) || value != "" || s.Len() != 0 || s.Bytes() != 0 {
		t.Fatal("hard expired fallback", value, err)
	}
}

func TestMarkStaleValidationAndQueuedGeneration(t *testing.T) {
	s, clock, queue, options := lifetimeStore()
	calls := 0
	load := func(context.Context) (string, error) { calls++; return "value", nil }
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	clock.Add(int64(10 * time.Second))
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	old := queue.pop(t)
	if n, err := s.MarkStaleKey("key", -2); n != 0 || !errors.Is(err, ErrRevalidationWindow) {
		t.Fatal(n, err)
	}
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	if queue.count() != 0 {
		t.Fatal("invalid mark reset reservation")
	}
	_, _ = s.MarkStaleKey("key", time.Minute)
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	if err := old.complete(); err != nil {
		t.Fatal(err)
	}
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	if queue.count() != 1 || calls != 1 {
		t.Fatal("old cleanup changed new reservation", queue.count(), calls)
	}
	if err := queue.pop(t).complete(); err != nil {
		t.Fatal(err)
	}
	_, _ = s.MarkStaleKey("key", 0)
	if s.Len() != 0 || s.Bytes() != 0 {
		t.Fatal("zero did not expire")
	}
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	if calls != 3 {
		t.Fatal("zero did not reload", calls)
	}
}

func TestMarkStaleRetiresColdAndDetachedLoads(t *testing.T) {
	s := New(4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	result := make(chan string, 2)
	var calls atomic.Int64
	load := func(ctx context.Context) (string, error) {
		if calls.Add(1) <= 2 {
			started <- struct{}{}
			<-ctx.Done()
			return "", ctx.Err()
		}
		return "fresh", nil
	}
	read := func() {
		v, err := GetOrLoad(ctx, s, "key", Options{TTL: time.Minute, Tags: []string{"posts"}}, load)
		if err != nil {
			result <- err.Error()
		} else {
			result <- v
		}
	}
	go read()
	<-started
	s.InvalidateKey("key")
	go read()
	<-started
	if n, err := s.MarkStaleTag("posts", time.Minute); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case v := <-result:
			if v != "fresh" {
				t.Fatal(v)
			}
		case <-ctx.Done():
			t.Fatal("retired reader stranded")
		}
	}
	if calls.Load() != 3 {
		t.Fatal("retry not coalesced", calls.Load())
	}
}

func TestMarkStaleCancelsRunningBackgroundRefresh(t *testing.T) {
	s, clock, queue, options := lifetimeStore()
	started := make(chan struct{})
	var calls atomic.Int64
	load := func(ctx context.Context) (string, error) {
		switch calls.Add(1) {
		case 1:
			return "old", nil
		case 2:
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		default:
			return "new", nil
		}
	}
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	clock.Add(int64(10 * time.Second))
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	job := queue.pop(t)
	done := make(chan error, 1)
	go func() { done <- job.complete() }()
	<-started
	_, _ = s.MarkStaleTag("posts", time.Minute)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background not canceled")
	}
	value, err := GetOrLoad(context.Background(), s, "key", options, load)
	if err != nil || value != "old" {
		t.Fatal(value, err)
	}
	if err := queue.pop(t).complete(); err != nil {
		t.Fatal(err)
	}
	value, err = GetOrLoad(context.Background(), s, "key", options, load)
	if err != nil || value != "new" || calls.Load() != 3 {
		t.Fatal(value, err, calls.Load())
	}
}

func TestMarkStalePrefixIsolationAndBlockingFallback(t *testing.T) {
	s, _, _, options := lifetimeStore()
	options.ScheduleRefresh = nil
	for _, path := range []string{"/posts", "/posts/a", "/posts-old"} {
		o := options
		o.Paths = []string{path}
		_, _ = GetOrLoad(context.Background(), s, path, o, func(context.Context) (string, error) { return "old", nil })
	}
	before := s.Bytes()
	if n, err := s.MarkStalePathPrefix("/posts", time.Second); err != nil || n != 2 || s.Bytes() != before {
		t.Fatal(n, err)
	}
	for _, path := range []string{"/posts", "/posts/a", "/posts-old"} {
		o := options
		o.Paths = []string{path}
		v, err := GetOrLoad(context.Background(), s, path, o, func(context.Context) (string, error) { return "new", nil })
		want := "new"
		if path == "/posts-old" {
			want = "old"
		}
		if err != nil || v != want {
			t.Fatal(path, v, err)
		}
	}
}
