package shared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

func TestSeedHTTPRejectsMalformedSeedRequestsBeforeBackend(t *testing.T) {
	b := seedStub(t)
	h, err := NewHandler(HandlerOptions{Backend: b, Namespace: "app", Token: secret})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	seed, _ := json.Marshal(SeedValue{ID: testID, Policy: testPolicy, Value: []byte(`"private seed value"`)})
	install := fmt.Sprintf(`{"Version":3,"Operation":"seed-install","Namespace":"app","Token":%q,"Seeds":[%s]}`, token, seed)
	reserve := `{"Version":3,"Operation":"seed-reserve","Namespace":"app"}`
	for name, body := range map[string]string{
		"v2":               strings.Replace(reserve, `"Version":3`, `"Version":2`, 1),
		"forbidden-null":   strings.Replace(reserve, `"Namespace":"app"`, `"Namespace":"app","Selector":null`, 1),
		"null-namespace":   strings.Replace(reserve, `"app"`, `null`, 1),
		"null-id":          strings.Replace(reserve, `"Namespace":"app"`, `"Namespace":"app","ID":null`, 1),
		"incomplete-id":    strings.Replace(reserve, `"Namespace":"app"`, `"Namespace":"app","ID":{}`, 1),
		"null-token":       fmt.Sprintf(`{"Version":3,"Operation":"seed-begin","Namespace":"app","Token":null}`),
		"missing-token":    `{"Version":3,"Operation":"seed-finish","Namespace":"app"}`,
		"null-seeds":       strings.Replace(install, `"Seeds":[`+string(seed)+`]`, `"Seeds":null`, 1),
		"missing-seeds":    fmt.Sprintf(`{"Version":3,"Operation":"seed-install","Namespace":"app","Token":%q}`, token),
		"seed-alias":       strings.Replace(install, `"Policy":`, `"policy":`, 1),
		"nested-duplicate": strings.Replace(install, `"Schema":"v1"`, `"Schema":"v1","Schema":"v1"`, 1),
		"nested-null":      strings.Replace(install, `"Key":"post:a"`, `"Key":null`, 1),
		"policy-null":      strings.Replace(install, `"FreshFor":1000000000`, `"FreshFor":null`, 1),
		"value-array":      strings.Replace(install, `"Value":"InByaXZhdGUgc2VlZCB2YWx1ZSI="`, `"Value":[1,2]`, 1),
		"too-many":         fmt.Sprintf(`{"Version":3,"Operation":"seed-install","Namespace":"app","Token":%q,"Seeds":[%s]}`, token, strings.TrimSuffix(strings.Repeat(string(seed)+",", MaxSeedBatch+1), ",")),
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://cache/", strings.NewReader(body))
			r.Header.Set("Authorization", "Bearer "+secret)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 400 || strings.Contains(w.Body.String(), token) || strings.Contains(w.Body.String(), secret) || strings.Contains(w.Body.String(), "private seed value") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	if b.reserves.Load()+b.begins.Load()+b.installs.Load()+b.finishes.Load() != 0 {
		t.Fatal("malformed input reached backend")
	}
}

func TestSeedHTTPRejectsMalformedAuthorityResponses(t *testing.T) {
	token := strings.Repeat("b", 64)
	for name, tc := range map[string]struct{ operation, body string }{
		"missing-ticket":  {"reserve", `{"Version":3}`},
		"null-ticket":     {"reserve", `{"Version":3,"Ticket":null}`},
		"missing-created": {"reserve", fmt.Sprintf(`{"Version":3,"Ticket":{"Token":%q}}`, token)},
		"bad-created":     {"reserve", fmt.Sprintf(`{"Version":3,"Ticket":{"Token":%q,"CreatedAt":"0001-01-01T00:00:00Z"}}`, token)},
		"created-alias":   {"reserve", fmt.Sprintf(`{"Version":3,"Ticket":{"Token":%q,"createdAt":"2099-01-01T00:00:00Z"}}`, token)},
		"empty-session":   {"begin", `{"Version":3,"Session":{}}`},
		"null-fields":     {"begin", `{"Version":3,"Session":{"Token":null,"LeaseFor":null}}`},
		"missing-lease":   {"begin", fmt.Sprintf(`{"Version":3,"Session":{"Token":%q}}`, token)},
		"tokenless-lease": {"begin", `{"Version":3,"Session":{"Token":"","LeaseFor":1}}`},
		"lease-expired":   {"begin", fmt.Sprintf(`{"Version":3,"Session":{"Token":%q,"LeaseFor":-1}}`, token)},
		"lease-too-long":  {"begin", fmt.Sprintf(`{"Version":3,"Session":{"Token":%q,"LeaseFor":%d}}`, token, SeedSessionTTL+1)},
		"mixed":           {"begin", `{"Version":3,"Session":{"Token":"","LeaseFor":0},"Count":0}`},
		"negative-count":  {"install", `{"Version":3,"Count":-1}`},
		"excess-count":    {"install", `{"Version":3,"Count":2}`},
		"null-count":      {"install", `{"Version":3,"Count":null}`},
		"finish-payload":  {"finish", `{"Version":3,"Session":{"Token":"","LeaseFor":0}}`},
		"private-error":   {"begin", `{"Version":3,"Error":"private ticket value"}`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			b, err := NewHTTP(srv.URL, secret, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			switch tc.operation {
			case "reserve":
				_, err = b.ReserveSeeds(context.Background(), "app")
			case "begin":
				_, err = b.BeginSeeds(context.Background(), "app", token)
			case "install":
				_, err = b.InstallSeeds(context.Background(), "app", token, []SeedValue{{ID: testID, Policy: testPolicy, Value: []byte("1")}})
			case "finish":
				err = b.FinishSeeds(context.Background(), "app", token)
			}
			if !errors.Is(err, ErrProtocol) || strings.Contains(err.Error(), token) || strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "private ticket value") {
				t.Fatal(err)
			}
		})
	}
	// A denial has an explicit canonical shape and is not a malformed lease.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Version":3,"Session":{"Token":"","LeaseFor":0}}`)
	}))
	defer srv.Close()
	b, _ := NewHTTP(srv.URL, secret, srv.Client())
	if session, err := b.BeginSeeds(context.Background(), "app", token); err != nil || session != (SeedSession{}) {
		t.Fatal(session, err)
	}
}

func TestSeedHTTPUnknownInstallAcknowledgmentFinishesWithoutRetry(t *testing.T) {
	m, _ := memory(t, 256, 4<<20)
	h, err := NewHandler(HandlerOptions{Backend: m, Namespace: "app", Token: secret})
	if err != nil {
		t.Fatal(err)
	}
	var installs, finishes atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var request wireRequest
		_ = json.Unmarshal(data, &request)
		r.Body = io.NopCloser(strings.NewReader(string(data)))
		if request.Operation == "seed-install" && installs.Add(1) == 2 {
			accepted := httptest.NewRecorder()
			h.ServeHTTP(accepted, r)
			if accepted.Code != 200 {
				t.Error(accepted.Code, accepted.Body.String())
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Version":3,"Count":`)
			return
		}
		if request.Operation == "seed-finish" {
			finishes.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()
	b, _ := NewHTTP(srv.URL, secret, srv.Client())
	s := store(t, b)
	ticket, err := s.ReserveSeeds(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	count, err := Bootstrap(context.Background(), s, ticket.Token, seedLookupValues(130))
	if count != 128 || !errors.Is(err, ErrProtocol) || installs.Load() != 2 || finishes.Load() != 1 {
		t.Fatal(count, err, installs.Load(), finishes.Load())
	}
	view, err := b.Begin(context.Background(), ID{"app", "v1", "129"}, testPolicy, time.Second)
	if err != nil || !view.Found {
		t.Fatal("unknown acknowledgment lost already-published value", view, err)
	}
	if n, err := Bootstrap(context.Background(), s, ticket.Token, seedLookupValues(130)); err != nil || n != 0 || installs.Load() != 2 {
		t.Fatal("consumed ticket retried", n, err, installs.Load())
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
