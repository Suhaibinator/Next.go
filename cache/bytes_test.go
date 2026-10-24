package cache

import (
	"context"
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type sizedValue string

func (v sizedValue) CacheSize() int64 { return int64(len(v)) }

func TestByteBudgetLRUAndInvalidation(t *testing.T) {
	s := New(10, WithMaxBytes(1120, nil))
	get := func(key string) {
		t.Helper()
		_, err := GetOrLoad(context.Background(), s, key, Options{TTL: time.Minute}, func(context.Context) (sizedValue, error) { return sizedValue(strings.Repeat("x", 300)), nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	get("a")
	get("b")
	get("a")
	get("c")
	if s.Len() != 2 || s.Bytes() != 1114 {
		t.Fatalf("accounting: %d %d", s.Len(), s.Bytes())
	}
	s.mu.Lock()
	_, a := s.entries["a"]
	_, b := s.entries["b"]
	_, c := s.entries["c"]
	s.mu.Unlock()
	if !a || b || !c {
		t.Fatal("byte pressure ignored LRU")
	}
	s.InvalidateKey("a")
	if s.Bytes() != 557 {
		t.Fatal("invalidation did not release bytes")
	}
	s.Clear()
	if s.Bytes() != 0 || s.Len() != 0 {
		t.Fatal("clear retained bytes")
	}
}
func TestByteBudgetExpiryLabelsAndUntypedValues(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	s := New(4, WithMaxBytes(2048, nil), WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	_, err := s.GetOrLoad(context.Background(), "key", Options{TTL: time.Second, Tags: []string{"tag", "tag"}, Paths: []string{"/posts?x=1", "/posts/"}}, func(context.Context) (any, error) { return "data", nil })
	if err != nil {
		t.Fatal(err)
	}
	if s.Bytes() != 256+3+4+64+3+64+6 {
		t.Fatalf("metadata charge: %d", s.Bytes())
	}
	clock.Add(int64(time.Second))
	if s.Bytes() != 0 || s.Len() != 0 {
		t.Fatal("expired bytes retained")
	}
	bytes := make([]byte, 1, 100)
	_, err = s.GetOrLoad(context.Background(), "slice", Options{TTL: time.Second}, func(context.Context) (any, error) { return bytes, nil })
	if err != nil {
		t.Fatal(err)
	}
	if s.Bytes() != 256+5+100 {
		t.Fatal("slice capacity not charged")
	}
	s.InvalidatePath("/posts")
	s.Clear()
	if s.Bytes() != 0 {
		t.Fatal("byte underflow")
	}
}
func TestOversizedUnsupportedAndPanickingWeightsSkipRetention(t *testing.T) {
	for _, test := range []struct {
		name   string
		value  any
		weigh  func(any) int64
		budget int64
	}{
		{"oversized", strings.Repeat("x", 1024), nil, 512},
		{"unsupported", struct{ N int }{1}, nil, 512},
		{"negative", "ok", func(any) int64 { return -1 }, 512},
		{"panic", "ok", func(any) int64 { panic("broken estimator") }, 512},
		{"overflow", "ok", func(any) int64 { return math.MaxInt64 }, math.MaxInt64},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := New(4, WithMaxBytes(test.budget, test.weigh))
			calls := 0
			for i := 0; i < 2; i++ {
				value, err := s.GetOrLoad(context.Background(), "key", Options{TTL: time.Minute}, func(context.Context) (any, error) { calls++; return test.value, nil })
				if err != nil || value == nil {
					t.Fatalf("load failed: %v", err)
				}
			}
			if calls != 2 || s.Len() != 0 || s.Bytes() != 0 {
				t.Fatal("unsafe weight retained")
			}
		})
	}
}
func TestTypedWeigherRunsOutsideLock(t *testing.T) {
	var s *Store
	s = New(4, WithMaxBytes(1024, func(value any) int64 {
		s.Len() // deadlocks if called inside the cache mutex
		text, ok := value.(sizedValue)
		if !ok {
			t.Errorf("weigher received %T", value)
			return -1
		}
		return int64(len(text))
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := GetOrLoad(ctx, s, "key", Options{TTL: time.Minute}, func(context.Context) (sizedValue, error) { return "payload", nil })
	if err != nil || s.Len() != 1 {
		t.Fatalf("typed weighing: %v", err)
	}
}
func TestInvalidationDuringWeightingCannotRepopulate(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	s := New(2, WithMaxBytes(1024, func(any) int64 { close(started); <-release; return 1 }))
	go func() {
		_, err := s.GetOrLoad(context.Background(), "key", Options{TTL: time.Minute}, func(context.Context) (any, error) { return "value", nil })
		done <- err
	}()
	<-started
	s.InvalidateKey("key")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.Len() != 0 || s.Bytes() != 0 {
		t.Fatal("invalidated estimator populated cache")
	}
}
func TestGoexitReleasesWaitersAndLoadSlots(t *testing.T) {
	for _, exitFrom := range []string{"loader", "weigher"} {
		t.Run(exitFrom, func(t *testing.T) {
			s := New(2, WithMaxConcurrentLoads(1), WithMaxBytes(1024, func(value any) int64 {
				if value == "exit" {
					runtime.Goexit()
				}
				return 1
			}))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, err := s.GetOrLoad(ctx, "broken", Options{TTL: time.Minute}, func(context.Context) (any, error) {
				if exitFrom == "loader" {
					runtime.Goexit()
				}
				return "exit", nil
			})
			if err == nil || ctx.Err() != nil {
				t.Fatalf("exit stranded waiter: %v", err)
			}
			_, err = s.GetOrLoad(ctx, "healthy", Options{TTL: time.Minute}, func(context.Context) (any, error) { return "healthy", nil })
			if err != nil || s.Len() != 1 {
				t.Fatalf("slot was stranded: %v", err)
			}
		})
	}
}

func TestReusableByteBudgetOptionAndValidation(t *testing.T) {
	for _, limit := range []int64{0, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("accepted nonpositive byte budget")
				}
			}()
			New(2, WithMaxBytes(limit, nil))
		}()
	}
	option := WithMaxBytes(1024, nil)
	var done atomic.Int32
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			s := New(2, option)
			_, err := s.GetOrLoad(context.Background(), "key", Options{TTL: time.Minute}, func(context.Context) (any, error) { return "ok", nil })
			if err == nil && s.Len() == 1 {
				done.Add(1)
			}
			results <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if done.Load() != 16 {
		t.Fatal("reused option failed")
	}
}
