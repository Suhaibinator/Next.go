package shared

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type seedLookupStub struct {
	Backend
	reserve                              func(context.Context, string) (SeedTicket, error)
	begin                                func(context.Context, string, string) (SeedSession, error)
	install                              func(context.Context, string, string, []SeedValue) (int, error)
	finish                               func(context.Context, string, string) error
	reserves, begins, installs, finishes atomic.Int32
}

func seedStub(t *testing.T) *seedLookupStub {
	t.Helper()
	m, _ := memory(t, 256, 4<<20)
	return &seedLookupStub{Backend: m,
		reserve: func(context.Context, string) (SeedTicket, error) {
			return SeedTicket{Token: strings.Repeat("a", 64), CreatedAt: time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)}, nil
		},
		begin: func(context.Context, string, string) (SeedSession, error) {
			return SeedSession{Token: strings.Repeat("b", 64), LeaseFor: SeedSessionTTL}, nil
		},
		install: func(_ context.Context, _ string, _ string, v []SeedValue) (int, error) { return len(v), nil },
		finish:  func(context.Context, string, string) error { return nil },
	}
}
func (s *seedLookupStub) ReserveSeeds(ctx context.Context, ns string) (SeedTicket, error) {
	s.reserves.Add(1)
	return s.reserve(ctx, ns)
}
func (s *seedLookupStub) BeginSeeds(ctx context.Context, ns, token string) (SeedSession, error) {
	s.begins.Add(1)
	return s.begin(ctx, ns, token)
}
func (s *seedLookupStub) InstallSeeds(ctx context.Context, ns, token string, v []SeedValue) (int, error) {
	s.installs.Add(1)
	return s.install(ctx, ns, token, v)
}
func (s *seedLookupStub) FinishSeeds(ctx context.Context, ns, token string) error {
	s.finishes.Add(1)
	return s.finish(ctx, ns, token)
}
func seedLookupValues(n int) []Seed[string] {
	values := make([]Seed[string], n)
	for i := range values {
		values[i] = Seed[string]{Key: fmt.Sprint(i), Value: "owned", Policy: testPolicy}
	}
	return values
}

func TestSeedLookupChunksAndOwnsValues(t *testing.T) {
	b := seedStub(t)
	s := store(t, b)
	values := seedLookupValues(130)
	sizes := []int{}
	b.install = func(ctx context.Context, ns, session string, batch []SeedValue) (int, error) {
		if _, ok := ctx.Deadline(); !ok || ctx.Err() != nil || ns != "app" || session != strings.Repeat("b", 64) {
			t.Error("unbounded/incorrect install context")
		}
		if err := ValidateSeedValues(ns, batch); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(batch))
		if string(batch[0].Value) != `"owned"` {
			t.Fatal("JSON ownership/encoding", string(batch[0].Value))
		}
		batch[0].Policy.Tags[0] = "backend mutation"
		batch[0].Value[0] = 'x'
		return len(batch), nil
	}
	if n, err := Bootstrap(context.Background(), s, strings.Repeat("a", 64), values); err != nil || n != 130 {
		t.Fatal(n, err)
	}
	if fmt.Sprint(sizes) != "[128 2]" || b.begins.Load() != 1 || b.finishes.Load() != 1 || values[0].Policy.Tags[0] != "posts" || values[0].Value != "owned" {
		t.Fatal(sizes, b.begins.Load(), b.finishes.Load(), values[0])
	}
	// Raw-byte admission, independently of the per-batch key limit.
	b = seedStub(t)
	s = store(t, b)
	values = seedLookupValues(75)
	for i := range values {
		values[i].Value = strings.Repeat("x", 16000)
	}
	b.install = func(_ context.Context, ns, _ string, batch []SeedValue) (int, error) {
		if err := ValidateSeedValues(ns, batch); err != nil {
			t.Fatal(err)
		}
		return len(batch), nil
	}
	if n, err := Bootstrap(context.Background(), s, strings.Repeat("a", 64), values); err != nil || n != 75 || b.installs.Load() != 2 {
		t.Fatal(n, err, b.installs.Load())
	}
}

type seedLookupCodec struct {
	calls *atomic.Int32
	mode  string
}

func (v seedLookupCodec) MarshalJSON() ([]byte, error) {
	v.calls.Add(1)
	switch v.mode {
	case "panic":
		panic("private codec value")
	case "duplicate":
		return []byte(`{"same":1,"same":2}`), nil
	case "error":
		return nil, errors.New("private codec value")
	}
	return []byte(`{}`), nil
}
func TestSeedLookupCompletePreflightPrecedesConsumption(t *testing.T) {
	for _, kind := range []string{"late-policy", "duplicate", "late-key", "codec-panic", "codec-error", "codec-duplicate", "count"} {
		t.Run(kind, func(t *testing.T) {
			b := seedStub(t)
			s := store(t, b)
			var calls atomic.Int32
			values := make([]Seed[seedLookupCodec], 130)
			for i := range values {
				values[i] = Seed[seedLookupCodec]{Key: fmt.Sprint(i), Value: seedLookupCodec{calls: &calls}, Policy: testPolicy}
			}
			switch kind {
			case "late-policy":
				values[129].Policy = Policy{}
			case "duplicate":
				values[129].Key = values[0].Key
			case "late-key":
				values[129].Key = ""
			case "codec-panic":
				values[129].Value.mode = "panic"
			case "codec-error":
				values[129].Value.mode = "error"
			case "codec-duplicate":
				values[129].Value.mode = "duplicate"
			case "count":
				values = make([]Seed[seedLookupCodec], MaxSeedSessionValues+1)
			}
			_, err := Bootstrap(context.Background(), s, strings.Repeat("a", 64), values)
			if err == nil || b.begins.Load() != 0 || b.installs.Load() != 0 || b.finishes.Load() != 0 || strings.Contains(err.Error(), "private codec value") {
				t.Fatal("preflight failed after remote side effects", err, b.begins.Load(), b.installs.Load(), b.finishes.Load())
			}
			if (kind == "late-policy" || kind == "duplicate" || kind == "late-key" || kind == "count") && calls.Load() != 0 {
				t.Fatal("value codec ran before key/policy preflight", calls.Load())
			}
		})
	}
}
func TestSeedLookupAggregateMetadataAndEncodingBudget(t *testing.T) {
	b := seedStub(t)
	s := store(t, b)
	values := seedLookupValues(60)
	payload := strings.Repeat("x", 512<<10)
	for i := range values {
		values[i].Value = payload
	}
	if _, err := Bootstrap(context.Background(), s, strings.Repeat("a", 64), values); !errors.Is(err, ErrInvalid) || b.begins.Load() != 0 {
		t.Fatal("aggregate encoded budget ignored", err, b.begins.Load())
	}
	var calls atomic.Int32
	policy := testPolicy
	policy.Tags = nil
	policy.Paths = nil
	for i := 0; i < 64; i++ {
		policy.Tags = append(policy.Tags, fmt.Sprintf("%03d", i)+strings.Repeat("\x01", 509))
		policy.Paths = append(policy.Paths, fmt.Sprintf("/p%03d", i)+strings.Repeat("\x01", 507))
	}
	large := make([]Seed[seedLookupCodec], 100)
	for i := range large {
		large[i] = Seed[seedLookupCodec]{Key: fmt.Sprint(i), Value: seedLookupCodec{calls: &calls}, Policy: policy}
	}
	if _, err := Bootstrap(context.Background(), s, strings.Repeat("a", 64), large); !errors.Is(err, ErrInvalid) || b.begins.Load() != 0 || calls.Load() != 0 {
		t.Fatal("metadata budget ignored before value encoding", err, b.begins.Load(), calls.Load())
	}
}

func TestSeedLookupCancellationAndExpiredSessionCleanup(t *testing.T) {
	for _, kind := range []string{"pre-canceled", "cancel-install", "expire-before-install", "empty-expired"} {
		t.Run(kind, func(t *testing.T) {
			b := seedStub(t)
			s := store(t, b)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			values := seedLookupValues(1)
			b.finish = func(cleanup context.Context, _, _ string) error {
				if _, ok := cleanup.Deadline(); !ok || cleanup.Err() != nil {
					t.Error("cleanup inherited canceled/unbounded work context")
				}
				return nil
			}
			switch kind {
			case "pre-canceled":
				cancel()
			case "cancel-install":
				b.install = func(work context.Context, _, _ string, _ []SeedValue) (int, error) {
					cancel()
					<-work.Done()
					return 0, work.Err()
				}
			case "expire-before-install", "empty-expired":
				b.begin = func(context.Context, string, string) (SeedSession, error) {
					time.Sleep(time.Millisecond)
					return SeedSession{Token: strings.Repeat("b", 64), LeaseFor: time.Nanosecond}, nil
				}
				if kind == "empty-expired" {
					values = nil
				}
			}
			_, err := Bootstrap(ctx, s, strings.Repeat("a", 64), values)
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if kind == "pre-canceled" {
				if b.begins.Load() != 0 || b.finishes.Load() != 0 {
					t.Fatal("canceled preflight consumed ticket")
				}
			} else if b.finishes.Load() != 1 {
				t.Fatal("session cleanup missing", b.finishes.Load())
			}
			if (kind == "expire-before-install" || kind == "empty-expired") && b.installs.Load() != 0 {
				t.Fatal("expired session reached installer")
			}
		})
	}
}
func TestSeedLookupPartialErrorAndBoundedFinish(t *testing.T) {
	b := seedStub(t)
	s := store(t, b)
	failure := errors.New("install unavailable")
	cleanupFailure := errors.New("finish unavailable")
	b.install = func(context.Context, string, string, []SeedValue) (int, error) {
		if b.installs.Load() == 1 {
			return 128, nil
		}
		return 0, failure
	}
	b.finish = func(ctx context.Context, _, _ string) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 500*time.Millisecond {
			t.Error("finish cleanup unbounded")
		}
		return cleanupFailure
	}
	n, err := Bootstrap(context.Background(), s, strings.Repeat("a", 64), seedLookupValues(130))
	if n != 128 || !errors.Is(err, failure) || !errors.Is(err, cleanupFailure) || b.installs.Load() != 2 || b.finishes.Load() != 1 {
		t.Fatal(n, err, b.installs.Load(), b.finishes.Load())
	}
	for _, count := range []int{-1, 2} {
		bad := seedStub(t)
		bad.install = func(context.Context, string, string, []SeedValue) (int, error) { return count, nil }
		if n, err := Bootstrap(context.Background(), store(t, bad), strings.Repeat("a", 64), seedLookupValues(1)); !errors.Is(err, ErrProtocol) || n != 0 || bad.finishes.Load() != 1 {
			t.Fatal("invalid count accepted", n, err, bad.finishes.Load())
		}
	}
}
