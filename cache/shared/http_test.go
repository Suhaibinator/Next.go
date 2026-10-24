package shared

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPAuthenticationNamespaceAndMalformedRequests(t *testing.T) {
	m, _ := memory(t, 8, 2<<20)
	h, err := NewHandler(HandlerOptions{Backend: m, Namespace: "app", Token: secret})
	if err != nil {
		t.Fatal(err)
	}
	base := wireRequest{Version: protocol, Operation: "begin", ID: testID, Policy: &testPolicy, LeaseFor: time.Second}
	good, _ := json.Marshal(base)
	for _, tc := range []struct {
		name, token, method, content, origin, body string
		status                                     int
	}{
		{"no-auth", "", "POST", "application/json", "", string(good), 401},
		{"wrong-auth", strings.Repeat("a", 32), "POST", "application/json", "", string(good), 401},
		{"browser", secret, "POST", "application/json", "http://example.com", string(good), 401},
		{"method", secret, "GET", "application/json", "", string(good), 405},
		{"media", secret, "POST", "text/plain", "", string(good), 415},
		{"version", secret, "POST", "application/json", "", strings.Replace(string(good), `"Version":3`, `"Version":4`, 1), 400},
		{"legacy-version", secret, "POST", "application/json", "", strings.Replace(string(good), `"Version":3`, `"Version":1`, 1), 400},
		{"unexpected-window", secret, "POST", "application/json", "", strings.Replace(string(good), `"Operation":"begin"`, `"Operation":"begin","Window":0`, 1), 400},
		{"duplicate", secret, "POST", "application/json", "", `{"Version":3,"Version":3}`, 400},
		{"alias", secret, "POST", "application/json", "", strings.Replace(string(good), `"Version":3`, `"Version":3,"version":3`, 1), 400},
		{"nested-alias", secret, "POST", "application/json", "", strings.Replace(string(good), `"Schema":"v1"`, `"schema":"v1"`, 1), 400},
		{"unknown", secret, "POST", "application/json", "", strings.Replace(string(good), `"Operation":"begin"`, `"Operation":"begin","Secret":"x"`, 1), 400},
		{"trailing", secret, "POST", "application/json", "", string(good) + `{}`, 400},
		{"other-namespace", secret, "POST", "application/json", "", strings.Replace(string(good), `"Namespace":"app"`, `"Namespace":"tenant"`, 1), 400},
		{"extra-operation-fields", secret, "POST", "application/json", "", strings.Replace(string(good), `"Operation":"begin"`, `"Operation":"begin","Token":"x"`, 1), 400},
		{"null", secret, "POST", "application/json", "", `null`, 400},
		{"oversize", secret, "POST", "application/json", "", strings.Repeat(" ", maxWireBytes+1), 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://cache/", strings.NewReader(tc.body))
			if tc.token != "" {
				r.Header.Set("Authorization", "Bearer "+tc.token)
			}
			r.Header.Set("Content-Type", tc.content)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || strings.Contains(w.Body.String(), secret) || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal(w.Code, w.Body.String(), w.Header())
			}
		})
	}
	if keys, _ := m.Stats(); keys != 0 {
		t.Fatal("invalid requests changed backend", keys)
	}
}
func TestHTTPEndpointPolicyAndNoCredentialRedirect(t *testing.T) {
	for _, endpoint := range []string{"http://example.com/cache", "ftp://localhost/", "https://user:pass@example.com/", "https://example.com/?secret=1", "https://example.com/#fragment", "http://[::1"} {
		if _, err := NewHTTP(endpoint, secret, nil); !errors.Is(err, ErrInvalid) {
			t.Fatal(endpoint, err)
		}
	}
	for _, endpoint := range []string{"http://127.0.0.1/cache", "http://[::1]/cache", "http://localhost/cache", "https://cache.example.com/cache"} {
		if _, err := NewHTTP(endpoint, secret, nil); err != nil {
			t.Fatal(endpoint, err)
		}
	}
	var forwarded atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(204) }))
	defer receiver.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, receiver.URL, 307) }))
	defer redirect.Close()
	client := redirect.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
	b, err := NewHTTP(redirect.URL, secret, client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Begin(context.Background(), testID, testPolicy, time.Second); !errors.Is(err, ErrProtocol) {
		t.Fatal(err)
	}
	if forwarded.Load() != 0 {
		t.Fatal("authorization/body forwarded through redirect")
	}
}
func TestHTTPRejectsIncompatibleResponses(t *testing.T) {
	for name, body := range map[string]string{
		"version":           `{"Version":3,"View":{}}`,
		"legacy-version":    `{"Version":1,"View":{}}`,
		"duplicate":         `{"Version":3,"Version":3,"View":{}}`,
		"alias":             `{"Version":3,"version":3,"View":{}}`,
		"nested-alias":      `{"Version":3,"View":{"busy":true,"LeaseFor":1}}`,
		"unknown":           `{"Version":3,"View":{},"Extra":1}`,
		"missing":           `{"Version":3}`,
		"null-view":         `{"Version":3,"View":null}`,
		"impossible":        `{"Version":3,"View":{"Found":false,"Value":"eA=="}}`,
		"bad-lease":         `{"Version":3,"View":{"Lease":"bad","LeaseFor":1000000000}}`,
		"busy-lease":        fmt.Sprintf(`{"Version":3,"View":{"Busy":true,"Lease":"%s","LeaseFor":1000000000}}`, strings.Repeat("a", 64)),
		"negative-age":      `{"Version":3,"View":{"Age":-1}}`,
		"fresh-past-expiry": `{"Version":3,"View":{"Found":true,"FreshFor":2,"ExpiresIn":1}}`,
		"forever-expiry":    `{"Version":3,"View":{"Found":true,"NoExpiry":true,"ExpiresIn":1}}`,
		"mixed":             `{"Version":3,"View":{},"Count":0}`,
		"trailing":          `{"Version":3,"View":{}} {}`,
		"oversize":          strings.Repeat(" ", maxWireBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, body)
			}))
			defer srv.Close()
			b, err := NewHTTP(srv.URL, secret, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.Begin(context.Background(), testID, testPolicy, time.Second); !errors.Is(err, ErrProtocol) {
				t.Fatal(err)
			}
		})
	}
}
func TestHTTPBodyBoundsAndBinaryRoundTrip(t *testing.T) {
	m, _ := memory(t, 4, 4<<20)
	b, _ := remote(t, m)
	lease := acquire(t, b, testID, testPolicy)
	data := bytes.Repeat([]byte{0, 255, 2, 3}, MaxValueBytes/4)
	v := publish(t, b, testID, lease.Lease, data)
	if !bytes.Equal(v.Value, data) {
		t.Fatal("binary value corrupted")
	}
	got, err := b.Begin(context.Background(), testID, testPolicy, time.Second)
	if err != nil || !bytes.Equal(got.Value, data) {
		t.Fatal(err)
	}
	if _, err := b.Commit(context.Background(), testID, lease.Lease, append(data, 0)); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
func TestHTTPAdmissionAndCanceledRequests(t *testing.T) {
	m, _ := memory(t, 4, 1<<20)
	entered, unblock := make(chan struct{}), make(chan struct{})
	gate := &blockingBackend{Backend: m, entered: entered, unblock: unblock}
	release := sync.OnceFunc(func() { close(unblock) })
	defer release()
	h, err := NewHandler(HandlerOptions{Backend: gate, Namespace: "app", Token: secret, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	b, err := NewHTTP(srv.URL, secret, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := b.Begin(ctx, testID, testPolicy, time.Second); first <- err }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := b.Begin(ctx, testID, testPolicy, time.Second); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
	cancel()
	release()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request ignored cancellation")
	}
}

type blockingBackend struct {
	Backend
	entered, unblock chan struct{}
	once             atomic.Bool
}

func (b *blockingBackend) Begin(ctx context.Context, id ID, p Policy, d time.Duration) (View, error) {
	if b.once.CompareAndSwap(false, true) {
		close(b.entered)
	}
	select {
	case <-ctx.Done():
		return View{}, ctx.Err()
	case <-b.unblock:
		return b.Backend.Begin(ctx, id, p, d)
	}
}
