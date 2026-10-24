package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type refreshTask struct {
	run    func(context.Context) error
	finish func()
}
type refreshQueue struct {
	mu    sync.Mutex
	tasks []refreshTask
}

func (q *refreshQueue) schedule(run func(context.Context) error, finish func()) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.tasks = append(q.tasks, refreshTask{run, finish})
	return nil
}
func (q *refreshQueue) pop(t *testing.T) refreshTask {
	t.Helper()
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.tasks) == 0 {
		t.Fatal("no refresh reserved")
	}
	job := q.tasks[0]
	q.tasks = q.tasks[1:]
	return job
}
func (q *refreshQueue) count() int { q.mu.Lock(); defer q.mu.Unlock(); return len(q.tasks) }
func (job refreshTask) complete() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer job.finish()
	return job.run(ctx)
}
func lifetimeStore() (*Store, *atomic.Int64, *refreshQueue, Options) {
	clock := &atomic.Int64{}
	clock.Store(time.Now().UnixNano())
	store := New(4, WithClock(func() time.Time { return time.Unix(0, clock.Load()) }), WithMaxBytes(4096, nil))
	queue := &refreshQueue{}
	return store, clock, queue, Options{Lifetime: &Lifetime{Revalidate: 10 * time.Second, Expire: time.Minute}, ScheduleRefresh: queue.schedule, Tags: []string{"posts"}, Paths: []string{"/posts"}}
}

func TestLifetimeFreshStaleAndHardExpiry(t *testing.T) {
	s, clock, queue, options := lifetimeStore()
	calls := 0
	load := func(context.Context) (string, error) { calls++; return string(rune('0' + calls)), nil }
	read := func(want string) {
		t.Helper()
		value, err := GetOrLoad(context.Background(), s, "key", options, load)
		if err != nil || value != want {
			t.Fatal("read", value, err, "want", want)
		}
	}
	read("1")
	read("1")
	if calls != 1 || queue.count() != 0 {
		t.Fatal("fresh read reloaded")
	}
	clock.Add(int64(10 * time.Second))
	for i := 0; i < 20; i++ {
		read("1")
	}
	if queue.count() != 1 || calls != 1 {
		t.Fatal("stale refresh did not coalesce", queue.count(), calls)
	}
	if err := queue.pop(t).complete(); err != nil {
		t.Fatal(err)
	}
	read("2")
	clock.Add(int64(10 * time.Second))
	read("2")
	clock.Add(int64(50 * time.Second)) // Hard expiry, while its refresh is still queued.
	read("3")
	if err := queue.pop(t).complete(); err != nil {
		t.Fatal(err)
	}
	read("3")
	if calls != 3 || s.Len() != 1 || s.Bytes() == 0 {
		t.Fatal("obsolete queued job loaded or accounting failed", calls)
	}
}

func TestLifetimeRefreshFailureDoesNotExtendLastGoodDeadline(t *testing.T) {
	s, clock, queue, options := lifetimeStore()
	failure := errors.New("backend unavailable")
	broken := false
	load := func(context.Context) (string, error) {
		if broken {
			return "", failure
		}
		return "good", nil
	}
	if _, err := GetOrLoad(context.Background(), s, "key", options, load); err != nil {
		t.Fatal(err)
	}
	broken = true
	clock.Add(int64(10 * time.Second))
	for i := 0; i < 2; i++ {
		value, err := GetOrLoad(context.Background(), s, "key", options, load)
		if err != nil || value != "good" {
			t.Fatal(value, err)
		}
		if err := queue.pop(t).complete(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		if s.Len() != 1 {
			t.Fatal("last good snapshot lost")
		}
	}
	clock.Add(int64(50 * time.Second))
	value, err := GetOrLoad(context.Background(), s, "key", options, load)
	if !errors.Is(err, failure) || value != "" || s.Len() != 0 || s.Bytes() != 0 {
		t.Fatal("hard-expired value served after failure", value, err)
	}
}

func TestLifetimePermanentFailureAndUnretainableSuccessDropOldValue(t *testing.T) {
	for _, mode := range []string{"policy", "policy-panic", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			s, clock, queue, options := lifetimeStore()
			options.KeepStaleOnError = func(error) bool {
				if mode == "policy-panic" {
					panic("policy")
				}
				return false
			}
			calls := 0
			load := func(context.Context) (string, error) {
				calls++
				if calls == 1 {
					return "old", nil
				}
				if mode == "oversize" {
					return string(make([]byte, 8192)), nil
				}
				return "", errors.New("permanent error")
			}
			_, _ = GetOrLoad(context.Background(), s, "key", options, load)
			clock.Add(int64(10 * time.Second))
			_, _ = GetOrLoad(context.Background(), s, "key", options, load)
			_ = queue.pop(t).complete()
			if s.Len() != 0 || s.Bytes() != 0 {
				t.Fatal("obsolete value retained", s.Len(), s.Bytes())
			}
		})
	}
}

func TestLifetimeSchedulerRejectionAndMissingSchedulerBlockForFreshData(t *testing.T) {
	for _, mode := range []string{"missing", "rejected", "panic"} {
		t.Run(mode, func(t *testing.T) {
			s, clock, _, options := lifetimeStore()
			switch mode {
			case "missing":
				options.ScheduleRefresh = nil
			case "rejected":
				options.ScheduleRefresh = func(func(context.Context) error, func()) error { return errors.New("full") }
			case "panic":
				options.ScheduleRefresh = func(func(context.Context) error, func()) error { panic("failed admission") }
			}
			calls := 0
			load := func(context.Context) (string, error) {
				calls++
				if calls == 1 {
					return "old", nil
				}
				return "fresh", nil
			}
			_, _ = GetOrLoad(context.Background(), s, "key", options, load)
			clock.Add(int64(10 * time.Second))
			value, err := GetOrLoad(context.Background(), s, "key", options, load)
			if err != nil || value != "fresh" || calls != 2 {
				t.Fatal("refresh silently skipped", value, err, calls)
			}
		})
	}
}

func TestLifetimeDiscardedAndOldGenerationPlansDoNotStrandOrReplaceEntries(t *testing.T) {
	s, clock, queue, options := lifetimeStore()
	calls := 0
	load := func(context.Context) (string, error) { calls++; return "value", nil }
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	clock.Add(int64(10 * time.Second))
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	queue.pop(t).finish() // e.g. a Lambda response was rejected before work began.
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	old := queue.pop(t)
	s.ExpireKey("key")
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	clock.Add(int64(10 * time.Second))
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	if err := old.complete(); err != nil {
		t.Fatal(err)
	}
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	if queue.count() != 1 || calls != 2 {
		t.Fatal("old plan cleared a newer reservation", queue.count(), calls)
	}
	if err := queue.pop(t).complete(); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("replacement refresh did not run", calls)
	}
}

func TestLifetimeHardReaderJoinsBackgroundAndStrictExpirationRetriesIt(t *testing.T) {
	s, clock, queue, options := lifetimeStore()
	var calls atomic.Int64
	started := make(chan struct{})
	load := func(ctx context.Context) (string, error) {
		switch calls.Add(1) {
		case 1:
			return "old", nil
		case 2:
			close(started)
			<-ctx.Done()
			return "", ctx.Err()
		default:
			return "fresh", nil
		}
	}
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	clock.Add(int64(10 * time.Second))
	_, _ = GetOrLoad(context.Background(), s, "key", options, load)
	job := queue.pop(t)
	background := make(chan error, 1)
	go func() { background <- job.complete() }()
	<-started
	clock.Add(int64(50 * time.Second))
	result := make(chan string, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		value, err := GetOrLoad(ctx, s, "key", options, load)
		if err != nil {
			result <- err.Error()
		} else {
			result <- value
		}
	}()
	waitWaiters(t, s, "key", 1)
	if calls.Load() != 2 {
		t.Fatal("hard reader started duplicate load")
	}
	s.ExpireTag("posts")
	select {
	case value := <-result:
		if value != "fresh" {
			t.Fatal(value)
		}
	case <-ctx.Done():
		t.Fatal("reader stranded")
	}
	select {
	case err := <-background:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("background load not canceled")
	}
	value, err := GetOrLoad(ctx, s, "key", options, load)
	if err != nil || value != "fresh" || calls.Load() != 3 {
		t.Fatal("obsolete refresh replaced current generation", value, err, calls.Load())
	}
}

func TestLifetimeValidationAndRefreshDeadline(t *testing.T) {
	for _, options := range []Options{{TTL: time.Second, Lifetime: &Lifetime{Expire: time.Minute}}, {Lifetime: &Lifetime{}}, {Lifetime: &Lifetime{Revalidate: -1, Expire: time.Minute}}, {Lifetime: &Lifetime{Revalidate: time.Minute, Expire: time.Minute}}} {
		if _, err := New(1).GetOrLoad(context.Background(), "key", options, func(context.Context) (any, error) { t.Error("invalid policy called loader"); return nil, nil }); err == nil {
			t.Fatal("invalid lifetime accepted", options)
		}
	}
	s, clock, queue, options := lifetimeStore()
	_, _ = s.GetOrLoad(context.Background(), "key", options, func(context.Context) (any, error) { return "old", nil })
	clock.Add(int64(10 * time.Second))
	_, _ = s.GetOrLoad(context.Background(), "key", options, func(context.Context) (any, error) { t.Error("unbounded scheduler ran loader"); return "new", nil })
	job := queue.pop(t)
	err := job.run(context.Background())
	job.finish()
	if err == nil {
		t.Fatal("unbounded refresh accepted")
	}
}

func TestLifetimeCancellationDuringAccountingRetainsLastGoodValue(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	var cancel context.CancelFunc
	s := New(4, WithClock(func() time.Time { return time.Unix(0, clock.Load()) }), WithMaxBytes(4096, func(value any) int64 {
		if value == "new" {
			cancel()
		}
		return 16
	}))
	queue := &refreshQueue{}
	observed := false
	options := Options{Lifetime: &Lifetime{Revalidate: time.Second, Expire: time.Minute}, ScheduleRefresh: queue.schedule, KeepStaleOnError: func(err error) bool { observed = errors.Is(err, context.Canceled); return true }}
	_, _ = s.GetOrLoad(context.Background(), "key", options, func(context.Context) (any, error) { return "old", nil })
	clock.Add(int64(time.Second))
	_, _ = s.GetOrLoad(context.Background(), "key", options, func(context.Context) (any, error) { return "new", nil })
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	cancel = stop
	defer stop()
	job := queue.pop(t)
	err := job.run(ctx)
	job.finish()
	if !errors.Is(err, context.Canceled) || !observed || s.Len() != 1 {
		t.Fatal("late cancellation lost last good snapshot", err, observed, s.Len())
	}
}
