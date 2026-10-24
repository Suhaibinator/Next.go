package shared

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
)

type refreshJob struct {
	run    func(context.Context) error
	finish func()
}
type refreshQueue struct {
	mu   sync.Mutex
	jobs []refreshJob
}

func (q *refreshQueue) schedule(run func(context.Context) error, finish func()) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.jobs = append(q.jobs, refreshJob{run, finish})
	return nil
}
func (q *refreshQueue) len() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.jobs) }
func (q *refreshQueue) take(t *testing.T) refreshJob {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.jobs) == 0 {
		t.Fatal("no reserved refresh")
	}
	job := q.jobs[0]
	q.jobs = q.jobs[1:]
	return job
}
func runJob(t *testing.T, job refreshJob) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer job.finish()
	return job.run(ctx)
}
func warm(t *testing.T, s *Store, p Policy) {
	t.Helper()
	v, _, err := Lookup(context.Background(), s, "key", p, func(context.Context) (int, error) { return 1, nil })
	if err != nil || v != 1 {
		t.Fatal(v, err)
	}
}

func TestScheduledRefreshCoalescesAcrossHTTPReplicas(t *testing.T) {
	m, advance := memory(t, 8, 1<<20)
	b, srv := remote(t, m)
	other, err := NewHTTP(srv.URL, secret, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	a, c := store(t, b), store(t, other)
	warm(t, a, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	var calls atomic.Int32
	loader := func(context.Context) (int, error) { calls.Add(1); return 2, nil }
	options := LoadOptions{ScheduleRefresh: q.schedule}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	type result struct {
		v    int
		info cache.Info
		err  error
	}
	results := make(chan result, 24)
	for i := 0; i < cap(results); i++ {
		go func(i int) {
			s := a
			if i%2 != 0 {
				s = c
			}
			v, info, err := Lookup(ctx, s, "key", testPolicy, loader, options)
			results <- result{v, info, err}
		}(i)
	}
	for i := 0; i < cap(results); i++ {
		select {
		case r := <-results:
			if r.err != nil || r.v != 1 || r.info.State != cache.Stale || r.info.Age < 2*time.Second {
				t.Fatal(r)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if q.len() != 1 || calls.Load() != 0 {
		t.Fatal("refresh not coalesced/reserved", q.len(), calls.Load())
	}
	if err := runJob(t, q.take(t)); err != nil {
		t.Fatal(err)
	}
	v, info, err := Lookup(ctx, c, "key", testPolicy, loader, options)
	if err != nil || v != 2 || info.State != cache.Fresh || calls.Load() != 1 || q.len() != 0 {
		t.Fatal(v, info, err, calls.Load(), q.len())
	}
}
func TestScheduledRefreshRegistrationFailureBlocks(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			m, advance := memory(t, 4, 1<<20)
			s := store(t, m)
			warm(t, s, testPolicy)
			advance(2 * time.Second)
			var loads, reservations int
			options := LoadOptions{ScheduleRefresh: func(func(context.Context) error, func()) error {
				reservations++
				if panics {
					panic("private scheduler detail")
				}
				return errors.New("full")
			}}
			v, info, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { loads++; return 2, nil }, options)
			if err != nil || v != 2 || info.State != cache.Miss || loads != 1 || reservations != 1 {
				t.Fatal(v, info, err, loads, reservations)
			}
		})
	}
}
func TestScheduledRefreshDiscardAndWorkerContext(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	s := store(t, m)
	warm(t, s, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	var calls int
	type key struct{}
	loader := func(ctx context.Context) (int, error) {
		calls++
		if ctx.Err() != nil || ctx.Value(key{}) != "worker" {
			t.Error("request cancellation/context leaked into worker", ctx.Err(), ctx.Value(key{}))
		}
		return 2, nil
	}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "request"))
	v, _, err := Lookup(ctx, s, "key", testPolicy, loader, LoadOptions{ScheduleRefresh: q.schedule})
	if err != nil || v != 1 {
		t.Fatal(v, err)
	}
	cancel()
	q.take(t).finish() // Accepted work discarded before execution.
	v, _, err = Lookup(context.Background(), s, "key", testPolicy, loader, LoadOptions{ScheduleRefresh: q.schedule})
	if err != nil || v != 1 || q.len() != 1 {
		t.Fatal(v, err, q.len())
	}
	job := q.take(t)
	work, stop := context.WithTimeout(context.WithValue(context.Background(), key{}, "worker"), time.Second)
	defer stop()
	if err := job.run(work); err != nil {
		t.Fatal(err)
	}
	job.finish()
	if calls != 1 {
		t.Fatal(calls)
	}
}
func TestScheduledRefreshExpiredQueueLeaseSkipsLoader(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	s, err := New(m, "app", "v1", Options{Lease: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	warm(t, s, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	calls := 0
	loader := func(context.Context) (int, error) { calls++; return 2, nil }
	if _, _, err := Lookup(context.Background(), s, "key", testPolicy, loader, LoadOptions{ScheduleRefresh: q.schedule}); err != nil {
		t.Fatal(err)
	}
	advance(200 * time.Millisecond)
	time.Sleep(120 * time.Millisecond)
	if v, _, err := Lookup(context.Background(), s, "key", testPolicy, loader); err != nil || v != 2 {
		t.Fatal(v, err)
	}
	if err := runJob(t, q.take(t)); err != nil || calls != 1 {
		t.Fatal("expired queue job ran old loader", err, calls)
	}
}
func TestScheduledRefreshLateFailureCannotDiscardReplacement(t *testing.T) {
	for _, mode := range []string{"expire", "mark"} {
		t.Run(mode, func(t *testing.T) {
			m, advance := memory(t, 4, 1<<20)
			b, _ := remote(t, m)
			s := store(t, b)
			warm(t, s, testPolicy)
			advance(2 * time.Second)
			q := &refreshQueue{}
			failed := errors.New("permanent")
			v, _, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 0, failed }, LoadOptions{ScheduleRefresh: q.schedule, KeepStaleOnError: func(error) bool { return false }})
			if err != nil || v != 1 {
				t.Fatal(v, err)
			}
			if mode == "expire" {
				_, err = s.Expire(context.Background(), Selector{"tag", "posts"})
			} else {
				_, err = s.MarkStale(context.Background(), Selector{"tag", "posts"}, Never)
			}
			if err != nil {
				t.Fatal(err)
			}
			loader := func(context.Context) (int, error) { return 2, nil }
			if v, _, err := Lookup(context.Background(), s, "key", testPolicy, loader); err != nil || v != 2 {
				t.Fatal(v, err)
			}
			if err := runJob(t, q.take(t)); !errors.Is(err, failed) {
				t.Fatal(err)
			}
			if v, info, err := Lookup(context.Background(), s, "key", testPolicy, loader); err != nil || v != 2 || info.State != cache.Fresh {
				t.Fatal(v, info, err)
			}
		})
	}
}
func TestScheduledRefreshErrorRetentionPolicies(t *testing.T) {
	for _, mode := range []string{"keep", "discard", "panic"} {
		t.Run(mode, func(t *testing.T) {
			m, advance := memory(t, 4, 1<<20)
			s := store(t, m)
			warm(t, s, testPolicy)
			advance(2 * time.Second)
			q := &refreshQueue{}
			options := LoadOptions{ScheduleRefresh: q.schedule}
			if mode != "keep" {
				options.KeepStaleOnError = func(error) bool {
					if mode == "panic" {
						panic("private")
					}
					return false
				}
			}
			failed := errors.New("loader failed")
			v, info, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 0, failed }, options)
			if err != nil || v != 1 || info.State != cache.Stale {
				t.Fatal(v, info, err)
			}
			if err := runJob(t, q.take(t)); !errors.Is(err, failed) {
				t.Fatal(err)
			}
			keys, _ := m.Stats()
			want := 0
			if mode == "keep" {
				want = 1
			}
			if keys != want {
				t.Fatal("last-good retention", keys, want)
			}
			advance(4 * time.Second)
			if v, info, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 2, nil }, options); err != nil || v != 2 || info.State != cache.Miss {
				t.Fatal("hard expiry served old data", v, info, err)
			}
		})
	}
}
func TestScheduledRefreshHardExpiryAndMalformedStale(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	s := store(t, m)
	warm(t, s, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	options := LoadOptions{ScheduleRefresh: q.schedule}
	if _, _, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 2, nil }, options); err != nil {
		t.Fatal(err)
	}
	advance(4 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, _, err := Lookup(ctx, s, "key", testPolicy, func(context.Context) (int, error) { t.Error("live lease lost"); return 2, nil }, options); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	q.take(t).finish()
	id := ID{"app", "v1", "corrupt"}
	lease := acquire(t, m, id, testPolicy)
	publish(t, m, id, lease.Lease, []byte("broken JSON"))
	advance(2 * time.Second)
	v, info, err := Lookup(context.Background(), s, "corrupt", testPolicy, func(context.Context) (int, error) { return 3, nil }, options)
	if err != nil || v != 3 || info.State != cache.Miss || q.len() != 0 {
		t.Fatal("malformed stale was served/scheduled", v, info, err, q.len())
	}
}
func TestScheduledRefreshRequiresWorkerDeadline(t *testing.T) {
	m, advance := memory(t, 4, 1<<20)
	s := store(t, m)
	warm(t, s, testPolicy)
	advance(2 * time.Second)
	q := &refreshQueue{}
	if _, _, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { t.Error("unbounded worker ran"); return 2, nil }, LoadOptions{ScheduleRefresh: q.schedule}); err != nil {
		t.Fatal(err)
	}
	job := q.take(t)
	if err := job.run(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	job.finish()
	v, _, err := Lookup(context.Background(), s, "key", testPolicy, func(context.Context) (int, error) { return 2, nil })
	if err != nil || v != 2 {
		t.Fatal("invalid worker retained lease", v, err)
	}
}
func TestScheduledRefreshSynchronousDelayCannotExtendStale(t *testing.T) {
	m, err := NewMemory(4, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	s := store(t, m)
	p := Policy{ExpireAfter: 50 * time.Millisecond}
	warm(t, s, p)
	schedules := 0
	options := LoadOptions{ScheduleRefresh: func(run func(context.Context) error, finish func()) error {
		schedules++
		defer finish()
		if schedules == 1 {
			time.Sleep(60 * time.Millisecond)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			return run(ctx)
		}
		return nil
	}}
	v, info, err := Lookup(context.Background(), s, "key", p, func(context.Context) (int, error) { return 2, nil }, options)
	if err != nil || v != 2 || info.State != cache.Stale || schedules != 2 {
		t.Fatal("returned expired last-good generation", v, info, err, schedules)
	}
}
