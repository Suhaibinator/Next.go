package shared

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
)

func TestAuditRefreshCanceledWorkerDoesNotStartLoader(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	s := store(t, m)
	warm(t, s, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	var calls atomic.Int32
	v, _, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) {
		calls.Add(1)
		return 2, nil
	}, LoadOptions{ScheduleRefresh: q.schedule})
	if err != nil || v != 1 {
		t.Fatal(v, err)
	}
	job := q.take(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if err := job.run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	job.finish()
	if calls.Load() != 0 {
		t.Fatal("already-canceled worker started user work", calls.Load())
	}
	// Cancellation must also relinquish ownership so an ordinary reader can
	// refresh without waiting for the abandoned remote lease to expire.
	v, info, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 3, nil })
	if err != nil || v != 3 || info.State != cache.Miss {
		t.Fatal(v, info, err)
	}
}

type auditCancelAfterBegin struct {
	Backend
	cancel context.CancelFunc
}

func (b *auditCancelAfterBegin) Begin(ctx context.Context, id ID, policy Policy, lease time.Duration) (View, error) {
	v, err := b.Backend.Begin(ctx, id, policy, lease)
	if err == nil {
		b.cancel()
	}
	return v, err
}

func TestAuditBlockingCancellationAfterLeaseDoesNotStartLoader(t *testing.T) {
	m, _ := memory(t, 4, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := store(t, &auditCancelAfterBegin{Backend: m, cancel: cancel})
	var calls atomic.Int32
	_, info, err := Lookup(ctx, s, "key", testPolicy, func(context.Context) (int, error) {
		calls.Add(1)
		return 1, nil
	})
	if !errors.Is(err, context.Canceled) || info != (cache.Info{}) || calls.Load() != 0 {
		t.Fatal("canceled acquisition invoked loader", info, err, calls.Load())
	}
	if keys, _ := m.Stats(); keys != 0 {
		t.Fatal("canceled acquisition retained its unpublished lease", keys)
	}
}

type auditLostCommitAck struct {
	Backend
	lost atomic.Bool
}

func (b *auditLostCommitAck) Commit(ctx context.Context, id ID, token string, value []byte) (View, error) {
	v, err := b.Backend.Commit(ctx, id, token, value)
	if err == nil && v.Found && b.lost.CompareAndSwap(false, true) {
		return View{}, ErrUnavailable
	}
	return v, err
}

func TestAuditLostCommitAckCannotDiscardCommittedGeneration(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	warm(t, store(t, m), testPolicy)
	advance(2 * time.Second)
	s := store(t, &auditLostCommitAck{Backend: m})
	q := &refreshQueue{}
	v, _, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 2, nil }, LoadOptions{
		ScheduleRefresh:  q.schedule,
		KeepStaleOnError: func(error) bool { return false },
	})
	if err != nil || v != 1 {
		t.Fatal(v, err)
	}
	if err := runJob(t, q.take(t)); !errors.Is(err, ErrUnavailable) {
		t.Fatal("lost acknowledgement was hidden", err)
	}
	v, info, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) {
		t.Error("a successful remote commit was discarded/reloaded after acknowledgement loss")
		return 3, nil
	})
	if err != nil || v != 2 || info.State != cache.Fresh {
		t.Fatal(v, info, err)
	}
}

type auditDiscardOutage struct{ Backend }

func (b *auditDiscardOutage) Discard(context.Context, ID, string) error { return ErrUnavailable }

func TestAuditRefreshDiscardFailurePreservesBothErrors(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	s := store(t, &auditDiscardOutage{Backend: m})
	warm(t, s, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	failed := errors.New("permanent load failure")
	v, _, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 0, failed }, LoadOptions{
		ScheduleRefresh:  q.schedule,
		KeepStaleOnError: func(error) bool { return false },
	})
	if err != nil || v != 1 {
		t.Fatal(v, err)
	}
	err = runJob(t, q.take(t))
	if !errors.Is(err, failed) || !errors.Is(err, ErrUnavailable) {
		t.Fatal("discard transport failure or original loader error was hidden", err)
	}
	// Unknown discard outcome may retain the remote lease, but cannot make it
	// immortal. Once both the old value and lease expire, a fresh read recovers.
	advance(6 * time.Second)
	v, info, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 2, nil })
	if err != nil || v != 2 || info.State != cache.Miss {
		t.Fatal(v, info, err)
	}
}

type auditStaleReadOutage struct {
	Backend
	down atomic.Bool
}

func (b *auditStaleReadOutage) BeginStale(ctx context.Context, id ID, p Policy, d time.Duration) (View, error) {
	if b.down.Load() {
		return View{}, ErrUnavailable
	}
	return b.Backend.BeginStale(ctx, id, p, d)
}

func TestAuditSWRDoesNotHideBackendOutage(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	gate := &auditStaleReadOutage{Backend: m}
	remote, _ := remote(t, gate)
	s := store(t, remote)
	warm(t, s, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	options := LoadOptions{ScheduleRefresh: q.schedule}
	gate.down.Store(true)
	_, info, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) {
		t.Error("backend outage silently ran a local loader")
		return 2, nil
	}, options)
	if !errors.Is(err, ErrUnavailable) || info != (cache.Info{}) || q.len() != 0 {
		t.Fatal("SWR became an outage fallback", info, err, q.len())
	}
	gate.down.Store(false)
	v, info, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 2, nil }, options)
	if err != nil || v != 1 || info.State != cache.Stale || q.len() != 1 {
		t.Fatal(v, info, err, q.len())
	}
	if err := runJob(t, q.take(t)); err != nil {
		t.Fatal(err)
	}
}

func TestAuditScheduledLoaderPanicDisposesRejectedGeneration(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	s := store(t, m)
	warm(t, s, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	v, _, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) {
		panic("private loader detail")
	}, LoadOptions{ScheduleRefresh: q.schedule, KeepStaleOnError: func(error) bool { return false }})
	if err != nil || v != 1 {
		t.Fatal(v, err)
	}
	if err := runJob(t, q.take(t)); !errors.Is(err, ErrLoader) {
		t.Fatal(err)
	}
	if keys, _ := m.Stats(); keys != 0 {
		t.Fatal("rejected panic result retained last-good generation or lease", keys)
	}
}

func TestAuditInfoGenerationAgeSaturates(t *testing.T) {
	const maxAge = time.Duration(1<<63 - 1)
	v := View{Found: true, Age: maxAge - 5, FreshFor: time.Second, ExpiresIn: time.Minute}
	got := info(v, 10, cache.Stale)
	if got.Age != maxAge || got.FreshFor != time.Second-10 || got.ExpiresIn != time.Minute-10 || got.ReuseFor(time.Second) != 0 {
		t.Fatal("generation age wrapped instead of saturating", got)
	}
}

func TestAuditWireGenerationAgeRemainsNonnegative(t *testing.T) {
	const maxAge = time.Duration(1<<63 - 1)
	for _, never := range []bool{false, true} {
		t.Run(map[bool]string{false: "finite", true: "never"}[never], func(t *testing.T) {
			v := View{Found: true, Value: []byte("7"), Age: maxAge - 1, FreshFor: time.Second, ExpiresIn: time.Minute}
			p := Policy{FreshFor: time.Second, ExpireAfter: time.Minute}
			if never {
				v.NoExpiry = true
				v.ExpiresIn = 0
				p.ExpireAfter = Never
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(wireResponse{Version: protocol, View: &v})
			}))
			defer srv.Close()
			b, err := NewHTTP(srv.URL, secret, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			value, got, err := Lookup(context.Background(), store(t, b), "key", p, func(context.Context) (int, error) { t.Error("fresh wire response unexpectedly loaded"); return 0, nil })
			if err != nil || value != 7 || got.State != cache.Fresh || got.Age != maxAge || got.ReuseFor(time.Second) != 0 {
				t.Fatal("wire age escaped nonnegative generation accounting", value, got, err)
			}
		})
	}
}
