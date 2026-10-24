package cache

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestLookupReportsGenerationAgeWithoutExtendingIt(t *testing.T) {
	s, clock, queue, options := lifetimeStore()
	load := func(context.Context) (string, error) { return "snapshot", nil }
	_, first, err := Lookup(context.Background(), s, "key", options, load)
	if err != nil || first.State != Miss || first.Age != 0 || first.FreshFor != 10*time.Second || first.ExpiresIn != time.Minute {
		t.Fatal(first, err)
	}
	clock.Add(int64(3 * time.Second))
	_, fresh, err := Lookup(context.Background(), s, "key", options, load)
	if err != nil || fresh.State != Fresh || fresh.Age != 3*time.Second || fresh.FreshFor != 7*time.Second || fresh.ExpiresIn != 57*time.Second || fresh.ReuseFor(5*time.Second) != 2*time.Second {
		t.Fatal(fresh, err)
	}
	clock.Add(int64(7 * time.Second))
	_, stale, err := Lookup(context.Background(), s, "key", options, load)
	if err != nil || stale.State != Stale || stale.Age != 10*time.Second || stale.FreshFor != 0 || stale.ExpiresIn != 50*time.Second || stale.ReuseFor(2*time.Minute) != 50*time.Second {
		t.Fatal(stale, err)
	}
	if err := queue.pop(t).complete(); err != nil {
		t.Fatal(err)
	}
	_, updated, err := Lookup(context.Background(), s, "key", options, load)
	if err != nil || updated.Age != 0 || updated.ExpiresIn != time.Minute {
		t.Fatal(updated, err)
	}
}

func TestLookupCompletionMetadataAndErrors(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	s := New(2, WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	_, info, err := s.Lookup(context.Background(), "key", Options{TTL: time.Second}, func(context.Context) (any, error) { clock.Add(int64(time.Hour)); return "value", nil })
	if err != nil || info.Age != 0 || info.ExpiresIn != time.Second {
		t.Fatal("age started before completion", info, err)
	}
	failure := errors.New("failed")
	value, info, err := s.Lookup(context.Background(), "error", Options{TTL: time.Second}, func(context.Context) (any, error) { return "partial", failure })
	if value != "partial" || !errors.Is(err, failure) || info != (Info{}) {
		t.Fatal("partial/error contract changed", value, info, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, info, err := s.Lookup(ctx, "key", Options{}, func(context.Context) (any, error) { return nil, nil }); !errors.Is(err, context.Canceled) || info != (Info{}) {
		t.Fatal(info, err)
	}
}

func TestProfileValidationAndOwnedBuiltins(t *testing.T) {
	for name, p := range BuiltinProfiles() {
		if err := p.Validate(); err != nil {
			t.Fatal(name, err)
		}
	}
	a := BuiltinProfiles()
	a["minutes"] = Profile{}
	if BuiltinProfiles()["minutes"].Revalidate != time.Minute {
		t.Fatal("builtins share mutable state")
	}
	for _, p := range []Profile{{}, {Stale: -1, Expire: time.Minute}, {Stale: MaxClientStale + 1, Expire: time.Minute}, {Revalidate: time.Minute, Expire: time.Second}} {
		if p.Validate() == nil {
			t.Fatal("invalid profile accepted", p)
		}
	}
	if (Info{Age: time.Minute, ExpiresIn: time.Second}).ReuseFor(time.Second) != 0 {
		t.Fatal("expired browser window extended")
	}
}
