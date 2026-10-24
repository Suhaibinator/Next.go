package shared

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSeedHTTPBootstrapAndRevokedTicket(t *testing.T) {
	m, advance := memory(t, 256, 4<<20)
	b, _ := remote(t, m)
	s := store(t, b)
	ctx := context.Background()
	ticket, err := s.ReserveSeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	advance(500 * time.Millisecond)
	seeds := make([]Seed[string], 130)
	for i := range seeds {
		seeds[i] = Seed[string]{Key: fmt.Sprint(i), Value: "owned", Policy: testPolicy}
	}
	count, err := Bootstrap(ctx, s, ticket.Token, seeds)
	if err != nil || count != len(seeds) {
		t.Fatal(count, err)
	}
	v, err := b.Begin(ctx, ID{"app", "v1", "0"}, testPolicy, time.Second)
	if err != nil || !v.Found || v.Age < 500*time.Millisecond || string(v.Value) != `"owned"` {
		t.Fatal(v, err)
	}
	if count, err := Bootstrap(ctx, s, ticket.Token, seeds); err != nil || count != 0 {
		t.Fatal("ticket replay", count, err)
	}
	next, err := s.ReserveSeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Expire(ctx, Selector{Kind: "tag", Value: "no-matching-key"}); err != nil {
		t.Fatal(err)
	}
	if count, err := Bootstrap(ctx, s, next.Token, []Seed[int]{{Key: "after-revocation", Value: 1, Policy: testPolicy}}); err != nil || count != 0 {
		t.Fatal("invalidation did not revoke ticket", count, err)
	}
}

func TestBootstrapValidatesBeforeTicketAndConsumesEmpty(t *testing.T) {
	m, _ := memory(t, 8, 4<<20)
	b, _ := remote(t, m)
	s := store(t, b)
	ctx := context.Background()
	ticket, err := s.ReserveSeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	duplicates := []Seed[int]{{Key: "same", Value: 1, Policy: testPolicy}, {Key: "same", Value: 2, Policy: testPolicy}}
	if _, err := Bootstrap(ctx, s, ticket.Token, duplicates); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if count, err := Bootstrap(ctx, s, ticket.Token, []Seed[int]{{Key: "valid", Value: 1, Policy: testPolicy}}); err != nil || count != 1 {
		t.Fatal("invalid preflight consumed ticket", count, err)
	}
	empty, err := s.ReserveSeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := Bootstrap[int](ctx, s, empty.Token, nil); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if count, err := Bootstrap(ctx, s, empty.Token, []Seed[int]{{Key: "replayed", Value: 2, Policy: testPolicy}}); err != nil || count != 0 {
		t.Fatal("empty import did not consume ticket", count, err)
	}
	large, err := s.ReserveSeeds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := Bootstrap(ctx, s, large.Token, []Seed[string]{{Key: "large", Value: strings.Repeat("x", MaxValueBytes), Policy: testPolicy}}); err != nil || count != 0 {
		t.Fatal("oversized value was not skipped", count, err)
	}
	if count, err := Bootstrap(ctx, s, large.Token, []Seed[int]{{Key: "large", Value: 1, Policy: testPolicy}}); err != nil || count != 0 {
		t.Fatal("oversized-only import did not consume ticket", count, err)
	}
}

type noSeedBackend struct{ Backend }

func TestSeedUnsupportedAndWireVersionMismatch(t *testing.T) {
	m, _ := memory(t, 4, 1<<20)
	b, _ := remote(t, noSeedBackend{m})
	s := store(t, b)
	if _, err := s.ReserveSeeds(context.Background()); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Version":2,"Error":"invalid"}`))
	}))
	defer legacy.Close()
	old, err := NewHTTP(legacy.URL, secret, legacy.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.ReserveSeeds(context.Background(), "app"); !errors.Is(err, ErrProtocol) {
		t.Fatal("legacy response accepted", err)
	}
}
