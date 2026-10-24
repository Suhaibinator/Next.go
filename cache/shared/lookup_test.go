package shared

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
)

const secret = "shared-test-secret-32-bytes-or-more"

func remote(t *testing.T, b Backend) (*HTTP, *httptest.Server) {
	t.Helper()
	h, err := NewHandler(HandlerOptions{Backend: b, Namespace: "app", Token: secret})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	r, err := NewHTTP(server.URL, secret, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return r, server
}
func store(t *testing.T, b Backend) *Store {
	t.Helper()
	s, err := New(b, "app", "v1", Options{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestLookupHTTPReplicasCoalesceAndOwnJSON(t *testing.T) {
	m, _ := memory(t, 8, 1<<20)
	first, server := remote(t, m)
	second, err := NewHTTP(server.URL, secret, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	stores := []*Store{store(t, first), store(t, second)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var calls atomic.Int32
	started := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	type model struct{ Items []string }
	loader := func(context.Context) (model, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-proceed
		return model{[]string{"owned"}}, nil
	}
	type result struct {
		value model
		err   error
	}
	results := make(chan result, 24)
	for i := 0; i < cap(results); i++ {
		go func(i int) {
			v, _, err := Lookup(ctx, stores[i%2], "same", testPolicy, loader)
			results <- result{v, err}
		}(i)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	close(proceed)
	for i := 0; i < cap(results); i++ {
		select {
		case r := <-results:
			if r.err != nil || len(r.value.Items) != 1 || r.value.Items[0] != "owned" {
				t.Fatal(r)
			}
			r.value.Items[0] = "changed"
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if calls.Load() != 1 {
		t.Fatal("replicas did not share loading", calls.Load())
	}
	v, info, err := Lookup(ctx, stores[1], "same", testPolicy, loader)
	if err != nil || v.Items[0] != "owned" || info.State != cache.Fresh || info.ExpiresIn <= 0 {
		t.Fatal(v, info, err)
	}
}
func TestLookupInvalidationRetriesObsoleteOwner(t *testing.T) {
	m, _ := memory(t, 8, 1<<20)
	backend, _ := remote(t, m)
	a, b := store(t, backend), store(t, backend)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	releaseOld := sync.OnceFunc(func() { close(release) })
	defer releaseOld()
	var calls atomic.Int32
	loader := func(context.Context) (string, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return "old", nil
		}
		return "fresh", nil
	}
	result := make(chan string, 1)
	go func() {
		v, _, err := Lookup(ctx, a, "key", testPolicy, loader)
		if err != nil {
			result <- err.Error()
		} else {
			result <- v
		}
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if count, err := b.Expire(ctx, Selector{"tag", "posts"}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	v, _, err := Lookup(ctx, b, "key", testPolicy, loader)
	if err != nil || v != "fresh" {
		t.Fatal(v, err)
	}
	releaseOld()
	select {
	case v := <-result:
		if v != "fresh" {
			t.Fatal("retired owner delivered old result", v)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
func TestLookupOutageRecoveryAndNoSilentFallback(t *testing.T) {
	m, _ := memory(t, 4, 1<<20)
	gate := &switchBackend{Backend: m}
	remote, _ := remote(t, gate)
	s := store(t, remote)
	var calls atomic.Int32
	loader := func(context.Context) (int, error) { return int(calls.Add(1)), nil }
	ctx := context.Background()
	if v, _, err := Lookup(ctx, s, "key", testPolicy, loader); err != nil || v != 1 {
		t.Fatal(v, err)
	}
	gate.down.Store(true)
	if _, info, err := Lookup(ctx, s, "key", testPolicy, loader); !errors.Is(err, ErrUnavailable) || info != (cache.Info{}) {
		t.Fatal(info, err)
	}
	if _, err := s.Expire(ctx, Selector{"tag", "posts"}); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("outage silently ran loader", calls.Load())
	}
	gate.down.Store(false)
	if count, err := s.Expire(ctx, Selector{"tag", "posts"}); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if v, _, err := Lookup(ctx, s, "key", testPolicy, loader); err != nil || v != 2 {
		t.Fatal(v, err)
	}
}

type switchBackend struct {
	Backend
	down atomic.Bool
}

func (b *switchBackend) Begin(ctx context.Context, id ID, p Policy, d time.Duration) (View, error) {
	if b.down.Load() {
		return View{}, ErrUnavailable
	}
	return b.Backend.Begin(ctx, id, p, d)
}
func (b *switchBackend) Expire(ctx context.Context, ns string, s Selector) (int, error) {
	if b.down.Load() {
		return 0, ErrUnavailable
	}
	return b.Backend.Expire(ctx, ns, s)
}

func TestLookupCancellationErrorsAndBypass(t *testing.T) {
	m, _ := memory(t, 4, 1<<20)
	b, _ := remote(t, m)
	s := store(t, b)
	ctx := context.Background()
	started := make(chan struct{})
	owner, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		_, _, err := Lookup(owner, s, "key", testPolicy, func(work context.Context) (int, error) { close(started); <-work.Done(); return 0, work.Err() })
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled loader retained lease")
	}
	if keys, _ := m.Stats(); keys != 0 {
		t.Fatal("cancellation retained empty lease", keys)
	}
	for _, loader := range []func(context.Context) (int, error){func(context.Context) (int, error) { return 0, errors.New("failed") }, func(context.Context) (int, error) { panic("private") }} {
		if _, _, err := Lookup(ctx, s, "key", testPolicy, loader); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal(err)
		}
		if keys, _ := m.Stats(); keys != 0 {
			t.Fatal(keys)
		}
	}
	v, info, err := Lookup(cache.WithBypass(ctx), s, "key", testPolicy, func(context.Context) ([]byte, error) { return []byte("bypass"), nil })
	if err != nil || string(v) != "bypass" || info.State != cache.Bypass {
		t.Fatal(v, info, err)
	}
	if keys, _ := m.Stats(); keys != 0 {
		t.Fatal("bypass touched backend", keys)
	}
}
func TestLookupBlockingStaleAndFailedLoadKeepsNoFreshResult(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	s := store(t, m)
	ctx := context.Background()
	calls := 0
	loader := func(context.Context) (int, error) { calls++; return calls, nil }
	if v, info, err := Lookup(ctx, s, "key", testPolicy, loader); err != nil || v != 1 || info.State != cache.Miss {
		t.Fatal(v, info, err)
	}
	advance(2 * time.Second)
	if _, _, err := Lookup(ctx, s, "key", testPolicy, func(context.Context) (int, error) { return 0, errors.New("no stale fallback") }); err == nil {
		t.Fatal("served stale on loader failure")
	}
	if v, info, err := Lookup(ctx, s, "key", testPolicy, loader); err != nil || v != 2 || info.State != cache.Miss {
		t.Fatal(v, info, err)
	}
}
func TestLookupLeaseTimeoutAndBusyDeadline(t *testing.T) {
	m, _ := memory(t, 4, 1<<20)
	s, err := New(m, "app", "v1", Options{Lease: 100 * time.Millisecond, Timeout: 300 * time.Millisecond, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Lookup(context.Background(), s, "slow", testPolicy, func(ctx context.Context) (string, error) { <-ctx.Done(); return "never publish", nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	// An unrelated caller waiting behind a live lease obeys its own deadline.
	acquire(t, m, ID{"app", "v1", "busy"}, testPolicy)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, _, err = Lookup(ctx, s, "busy", testPolicy, func(context.Context) (int, error) { t.Error("ran while another lease was live"); return 0, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

type brokenJSON struct{}

func (brokenJSON) MarshalJSON() ([]byte, error) { panic("secret marshal") }

type brokenDecode struct{}

func (brokenDecode) UnmarshalJSON([]byte) error { panic("secret decode") }
func TestLookupSerializationBoundsAndPanics(t *testing.T) {
	m, _ := memory(t, 4, 4<<20)
	s := store(t, m)
	ctx := context.Background()
	if _, _, err := Lookup(ctx, s, "huge", testPolicy, func(context.Context) (string, error) { return strings.Repeat("x", MaxValueBytes), nil }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, _, err := Lookup(ctx, s, "marshal", testPolicy, func(context.Context) (brokenJSON, error) { return brokenJSON{}, nil }); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, _, err := Lookup(ctx, s, "decode", testPolicy, func(context.Context) (brokenDecode, error) { return brokenDecode{}, nil }); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	if keys, _ := m.Stats(); keys != 0 {
		t.Fatal("invalid serialization poisoned entries", keys)
	}
}

type slowCommit struct{ Backend }

func (b slowCommit) Commit(ctx context.Context, id ID, token string, value []byte) (View, error) {
	v, err := b.Backend.Commit(ctx, id, token, value)
	if err == nil && v.Found {
		time.Sleep(5 * time.Millisecond)
	}
	return v, err
}
func TestLookupDoesNotReturnGenerationExpiredDuringCommitTransport(t *testing.T) {
	m, err := NewMemory(4, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	s := store(t, slowCommit{m})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, _, err = Lookup(ctx, s, "short", Policy{ExpireAfter: time.Millisecond}, func(context.Context) (int, error) { return 1, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("delivered expired generation", err)
	}
}

func TestLookupSchemaSeparationAndTypeMismatch(t *testing.T) {
	m, _ := memory(t, 4, 1<<20)
	s := store(t, m)
	ctx := context.Background()
	if _, _, err := Lookup(ctx, s, "key", testPolicy, func(context.Context) (int, error) { return 3, nil }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Lookup(ctx, s, "key", testPolicy, func(context.Context) (string, error) { t.Error("mismatched schema bypassed hit"); return "", nil }); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	other, err := New(m, "app", "v2", Options{})
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := Lookup(ctx, other, "key", testPolicy, func(context.Context) (string, error) { return "new schema", nil })
	if err != nil || v != "new schema" {
		t.Fatal(v, err)
	}
	if n, err := s.Expire(ctx, Selector{Kind: "key", Value: "key"}); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}
