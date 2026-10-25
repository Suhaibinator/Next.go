package client

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func resourceStore(t *testing.T, options ResourceStoreOptions) *ResourceStore {
	t.Helper()
	s, err := NewResourceStore(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}
func resourceSource(t *testing.T, s *ResourceStore, key string, ttl time.Duration, load func(context.Context) ([]byte, error)) *ResourceSource {
	t.Helper()
	r, err := s.Source(ResourceSourceOptions{Key: key, PublicTTL: ttl, Load: load})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func resourceWait(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("resource condition timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
func resourceWaiters(s *ResourceStore) int { s.mu.Lock(); defer s.mu.Unlock(); return s.waiters }

func TestResourceSourceAuthorityAndBounds(t *testing.T) {
	for _, options := range []ResourceStoreOptions{{MaxSources: -1}, {MaxEntries: 257}, {MaxInFlight: 33}, {MaxWaiters: 513}, {MaxBytes: 17 << 20}, {MaxValueBytes: -1}, {Timeout: 2 * time.Minute}} {
		if _, err := NewResourceStore(options); err == nil {
			t.Fatal("invalid bounds accepted", options)
		}
	}
	s := resourceStore(t, ResourceStoreOptions{MaxSources: 1})
	load := func(context.Context) ([]byte, error) { return []byte("first"), nil }
	r := resourceSource(t, s, "public:v1", time.Second, load)
	if _, err := s.Source(ResourceSourceOptions{Key: "public:v1", PublicTTL: time.Second, Load: func(context.Context) ([]byte, error) { return []byte("other authority"), nil }}); err == nil {
		t.Fatal("same-key loader substitution accepted")
	}
	if _, err := s.Source(ResourceSourceOptions{Key: "another", Load: load}); !errors.Is(err, ErrResourceCapacity) {
		t.Fatal(err)
	}
	r.Close()
	r.Close()
	if _, err := r.Read(context.Background()); !errors.Is(err, ErrResourceClosed) {
		t.Fatal(err)
	}
	replacement := resourceSource(t, s, "public:v1", 0, load)
	if _, err := replacement.Read(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	s.Close()
	if _, err := replacement.Read(context.Background()); !errors.Is(err, ErrResourceClosed) {
		t.Fatal(err)
	}
}
func TestResourcePublicDedupOwnedBytesAndCancellation(t *testing.T) {
	s := resourceStore(t, ResourceStoreOptions{})
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	payload := []byte("owned")
	var loaderContext context.Context
	source := resourceSource(t, s, "public", time.Minute, func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		loaderContext = ctx
		close(started)
		<-release
		return payload, nil
	})
	firstCtx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := source.Read(firstCtx); first <- err }()
	<-started
	second := make(chan []byte, 1)
	go func() {
		data, err := source.Read(context.Background())
		if err != nil {
			second <- []byte(err.Error())
			return
		}
		second <- data
	}()
	resourceWait(t, func() bool { return resourceWaiters(s) == 2 })
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if loaderContext.Err() != nil {
		t.Fatal("one canceled consumer aborted another")
	}
	close(release)
	got := <-second
	if string(got) != "owned" || calls.Load() != 1 {
		t.Fatal(string(got), calls.Load())
	}
	payload[0] = 'x'
	got[1] = 'x'
	cached, err := source.Read(context.Background())
	if err != nil || string(cached) != "owned" {
		t.Fatal(string(cached), err)
	}
	cached[2] = 'x'
	again, _ := source.Read(context.Background())
	if string(again) != "owned" {
		t.Fatal("cache did not own bytes")
	}
}
func TestResourcePrivateReadsNeverShare(t *testing.T) {
	s := resourceStore(t, ResourceStoreOptions{})
	started, release := make(chan struct{}, 2), make(chan struct{})
	var calls atomic.Int32
	source := resourceSource(t, s, "private-account", 0, func(context.Context) ([]byte, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return []byte("private"), nil
	})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := source.Read(context.Background()); results <- err }()
	}
	<-started
	<-started
	close(release)
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("private flights shared")
	}
	s.mu.Lock()
	entries := len(s.entries)
	s.mu.Unlock()
	if entries != 0 {
		t.Fatal("private value cached")
	}
}
func TestResourceInvalidationRetainsPhysicalAdmissionUntilLoaderReturns(t *testing.T) {
	s := resourceStore(t, ResourceStoreOptions{MaxInFlight: 1})
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	source := resourceSource(t, s, "public", time.Minute, func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return []byte("stale"), nil
		}
		return []byte("fresh"), nil
	})
	result := make(chan error, 1)
	go func() { _, err := source.Read(context.Background()); result <- err }()
	<-started
	s.Invalidate()
	if err := <-result; !errors.Is(err, ErrResourceInvalidated) {
		t.Fatal(err)
	}
	if _, err := source.Read(context.Background()); !errors.Is(err, ErrResourceCapacity) {
		t.Fatal("ignored cancellation released physical capacity", err)
	}
	close(release)
	resourceWait(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.flights) == 0 })
	data, err := source.Read(context.Background())
	if err != nil || string(data) != "fresh" || calls.Load() != 2 {
		t.Fatal(string(data), err, calls.Load())
	}
}
func TestResourceTimeoutAndWaiterCapacity(t *testing.T) {
	s := resourceStore(t, ResourceStoreOptions{MaxInFlight: 1, MaxWaiters: 1, Timeout: 20 * time.Millisecond})
	started, release := make(chan struct{}), make(chan struct{})
	source := resourceSource(t, s, "public", time.Minute, func(context.Context) ([]byte, error) { close(started); <-release; return []byte("late"), nil })
	result := make(chan error, 1)
	go func() { _, err := source.Read(context.Background()); result <- err }()
	<-started
	if _, err := source.Read(context.Background()); !errors.Is(err, ErrResourceCapacity) {
		t.Fatal("waiter bound ignored", err)
	}
	if err := <-result; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("store timeout did not release waiter", err)
	}
	if _, err := source.Read(context.Background()); !errors.Is(err, ErrResourceCapacity) {
		t.Fatal("physical timeout released loader slot", err)
	}
	close(release)
}
func TestResourceSeedExpiryOwnershipAndFences(t *testing.T) {
	s := resourceStore(t, ResourceStoreOptions{})
	source := resourceSource(t, s, "seed:v1", time.Minute, func(context.Context) ([]byte, error) { t.Error("seed caused a request"); return nil, nil })
	body := []byte("SSR-owned")
	seed := ResourceSeed{Key: "different", Body: body, ExpiresUnixMilli: time.Now().Add(30 * time.Second).UnixMilli()}
	if err := source.AdoptSeed(seed); !errors.Is(err, ErrResourceSeed) {
		t.Fatal(err)
	}
	seed.Key = "seed:v1"
	seed.ExpiresUnixMilli = time.Now().Add(-time.Second).UnixMilli()
	if err := source.AdoptSeed(seed); !errors.Is(err, ErrResourceSeed) {
		t.Fatal("expired seed adopted", err)
	}
	seed.ExpiresUnixMilli = time.Now().Add(30 * time.Second).UnixMilli()
	if err := source.AdoptSeed(seed); err != nil {
		t.Fatal(err)
	}
	body[0] = 'x'
	got, err := source.Read(context.Background())
	if err != nil || string(got) != "SSR-owned" {
		t.Fatal(string(got), err)
	}
	if err := source.AdoptSeed(seed); !errors.Is(err, ErrResourceSeed) {
		t.Fatal("seed adopted twice", err)
	}
	fresh := resourceSource(t, s, "fresh", time.Minute, func(context.Context) ([]byte, error) { return nil, nil })
	s.Invalidate()
	seed.Key = "fresh"
	if err := fresh.AdoptSeed(seed); !errors.Is(err, ErrResourceSeed) {
		t.Fatal("seed crossed mutation fence", err)
	}
	private := resourceSource(t, s, "private", 0, func(context.Context) ([]byte, error) { return nil, nil })
	seed.Key = "private"
	if err := private.AdoptSeed(seed); !errors.Is(err, ErrResourceSeed) {
		t.Fatal("private seed cached", err)
	}
}
func TestResourceValueCacheAdmissionAndLoaderFailures(t *testing.T) {
	s := resourceStore(t, ResourceStoreOptions{MaxEntries: 2, MaxBytes: 512, MaxValueBytes: 32})
	var calls atomic.Int32
	sources := []*ResourceSource{}
	for _, key := range []string{"a", "b", "c"} {
		sources = append(sources, resourceSource(t, s, key, time.Minute, func(context.Context) ([]byte, error) { calls.Add(1); return []byte("value"), nil }))
	}
	for _, source := range sources {
		if _, err := source.Read(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	entries, cost := len(s.entries), s.bytes
	s.mu.Unlock()
	if entries != 2 || cost > 512 {
		t.Fatal(entries, cost)
	}
	sources[0].Read(context.Background())
	if calls.Load() != 4 {
		t.Fatal("LRU entry not evicted")
	}
	large := resourceSource(t, s, "large", time.Minute, func(context.Context) ([]byte, error) { return bytes.Repeat([]byte{'x'}, 33), nil })
	if body, err := large.Read(context.Background()); !errors.Is(err, ErrResourceCapacity) || len(body) != 0 {
		t.Fatal(len(body), err)
	}
	panicSource := resourceSource(t, s, "panic", time.Minute, func(context.Context) ([]byte, error) { panic("secret panic detail") })
	if body, err := panicSource.Read(context.Background()); err == nil || len(body) != 0 || strings.Contains(err.Error(), "secret") {
		t.Fatal(string(body), err)
	}
}
