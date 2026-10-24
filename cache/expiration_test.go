package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExpirationMovesExistingWaitersToFreshGeneration(t *testing.T) {
	for _, mode := range []string{"key", "tag", "path", "prefix", "all", "mark", "mark-zero", "mark-never"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			s := New(4, WithMaxBytes(4096, nil))
			options := Options{TTL: time.Minute, Tags: []string{"posts"}, Paths: []string{"/posts/a"}}
			var calls atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			releaseOld := sync.OnceFunc(func() { close(release) })
			defer releaseOld()
			loader := func(ctx context.Context) (string, error) {
				if calls.Add(1) == 1 {
					close(started)
					// Deliberately ignore cancellation: expiration must not wait
					// for a noncooperative previous loader to finish.
					<-release
					return "old", nil
				}
				return "fresh", nil
			}
			type result struct {
				value string
				err   error
			}
			results := make(chan result, 8)
			for i := 0; i < cap(results); i++ {
				go func() {
					value, err := GetOrLoad(ctx, s, "posts:v1", options, loader)
					results <- result{value, err}
				}()
			}
			<-started
			waitWaiters(t, s, "posts:v1", cap(results))
			s.mu.Lock()
			oldDone := s.flights["posts:v1"].done
			s.mu.Unlock()
			affected := 1
			switch mode {
			case "key":
				affected = s.ExpireKey("posts:v1")
			case "tag":
				affected = s.ExpireTag("posts")
			case "path":
				affected = s.ExpirePath("//posts/a/?discard=1#fragment")
			case "prefix":
				affected = s.ExpirePathPrefix("/posts/")
			case "all":
				s.ExpireAll()
			case "mark":
				affected, _ = s.MarkStaleTag("posts", time.Minute)
			case "mark-never":
				affected, _ = s.MarkStaleTag("posts", Never)
			case "mark-zero":
				affected, _ = s.MarkStaleKey("posts:v1", 0)
			}
			if affected != 1 {
				t.Fatal("affected keys", affected)
			}
			for i := 0; i < cap(results); i++ {
				select {
				case got := <-results:
					if got.err != nil || got.value != "fresh" {
						t.Fatal(got)
					}
				case <-ctx.Done():
					t.Fatal("waiter remained on expired loader")
				}
			}
			releaseOld()
			select {
			case <-oldDone:
			case <-ctx.Done():
				t.Fatal("obsolete loader did not finish")
			}
			value, err := GetOrLoad(ctx, s, "posts:v1", options, loader)
			if err != nil || value != "fresh" {
				t.Fatal("obsolete result replaced current cache", value, err)
			}
			if calls.Load() != 2 || s.Len() != 1 || s.Bytes() == 0 {
				t.Fatal("replacement did not coalesce/cache", calls.Load())
			}
			s.mu.Lock()
			pending := len(s.awaiting)
			s.mu.Unlock()
			if pending != 0 {
				t.Fatal("delivered results retained", pending)
			}
		})
	}
}

// Pause the caller just after completion is selected but before it consumes the
// result. This models a descheduled request across a writer's expiration.
type deliveryGate struct {
	context.Context
	calls            atomic.Int32
	entered, release chan struct{}
}

func (c *deliveryGate) Err() error {
	if c.calls.Add(1) == 2 {
		close(c.entered)
		<-c.release
	}
	return c.Context.Err()
}

func TestExpirationAlsoReachesCompletedUndeliveredResults(t *testing.T) {
	for _, mark := range []bool{false, true} {
		t.Run(fmt.Sprint(mark), func(t *testing.T) {
			s := New(2)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			gate := &deliveryGate{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
			results := make(chan string, 1)
			var calls atomic.Int32
			go func() {
				value, err := GetOrLoad(gate, s, "key", Options{}, func(context.Context) (string, error) {
					if calls.Add(1) == 1 {
						return "old", nil
					}
					return "fresh", nil
				})
				if err != nil {
					results <- err.Error()
					return
				}
				results <- value
			}()
			select {
			case <-gate.entered:
			case <-ctx.Done():
				t.Fatal("result did not reach delivery")
			}
			// There is neither an entry (TTL=0) nor an active loader at this point.
			// An index containing only active loads would miss this waiting reader.
			var affected int
			if mark {
				affected, _ = s.MarkStaleKey("key", time.Minute)
			} else {
				affected = s.ExpireKey("key")
			}
			close(gate.release)
			if affected != 1 {
				t.Fatal("completed result was not expired", affected)
			}
			select {
			case got := <-results:
				if got != "fresh" || calls.Load() != 2 {
					t.Fatal(got, calls.Load())
				}
			case <-ctx.Done():
				t.Fatal("delivery stranded")
			}

		})
	}
}

func TestStrictExpirationReachesPreviouslyDetachedFlights(t *testing.T) {
	s := New(2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started := make(chan struct{}, 2)
	result := make(chan string, 2)
	var calls atomic.Int32
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
	if affected := s.ExpireTag("posts"); affected != 1 {
		t.Fatal("keys counted per generation", affected)
	}
	for i := 0; i < 2; i++ {
		select {
		case got := <-result:
			if got != "fresh" {
				t.Fatal(got)
			}
		case <-ctx.Done():
			t.Fatal("detached reader stranded")
		}
	}
	if calls.Load() != 3 {
		t.Fatal("replacement loads", calls.Load())
	}
}

func TestExpirationPreservesWaiterCancellationAndLoadLimits(t *testing.T) {
	s := New(2, WithMaxConcurrentLoads(1))
	ctx, cancel := context.WithCancel(context.Background())
	started, retried := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	var calls atomic.Int32
	go func() {
		_, err := s.GetOrLoad(ctx, "key", Options{}, func(ctx context.Context) (any, error) {
			if calls.Add(1) == 1 {
				close(started)
			} else {
				close(retried)
			}
			<-ctx.Done()
			return nil, ctx.Err()
		})
		result <- err
	}()
	<-started
	s.ExpireAll()
	select {
	case <-retried:
	case <-time.After(5 * time.Second):
		t.Fatal("expired load slot not released")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation stranded")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.awaiting) != 0 || len(s.flights) != 0 {
		t.Fatal("canceled waiter retained")
	}
}

func TestExpirationLeavesUnrelatedLabelsAndAccountsEntries(t *testing.T) {
	s := New(4, WithMaxBytes(4096, nil))
	for _, path := range []string{"/posts", "/posts/a", "/posts-old"} {
		_, err := s.GetOrLoad(context.Background(), path, Options{TTL: time.Hour, Tags: []string{path}, Paths: []string{path}}, func(context.Context) (any, error) { return path, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	before := s.Bytes()
	if affected := s.ExpirePathPrefix("/posts"); affected != 2 || s.Len() != 1 || s.Bytes() <= 0 || s.Bytes() >= before {
		t.Fatal("prefix isolation/accounting", affected, s.Len(), s.Bytes())
	}
	if s.ExpireTag("missing") != 0 || s.ExpireKey("missing") != 0 {
		t.Fatal("unknown labels affected entries")
	}
	if s.ExpireTag("/posts-old") != 1 || s.Bytes() != 0 {
		t.Fatal("entry not released")
	}
}
