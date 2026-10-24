package serverless

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRuntimeInvocationLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var count atomic.Int32
	var runtimeFailure atomic.Bool
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case runtimePrefix + "/invocation/next":
			n := count.Add(1)
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(int(n)))
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
			if n == 1 {
				_, _ = io.WriteString(w, `{broken`)
				return
			}
			if n > 2 {
				t.Error("unexpected extra next request")
				cancel()
				return
			}
			_ = json.NewEncoder(w).Encode(event())
		case runtimePrefix + "/invocation/1/error":
			if r.Method != "POST" || r.Header.Get("Lambda-Runtime-Function-Error-Type") != "Unhandled" {
				runtimeFailure.Store(true)
			}
			var failure map[string]any
			if json.NewDecoder(r.Body).Decode(&failure) != nil || failure["errorType"] != "NextGoInvocationError" {
				runtimeFailure.Store(true)
			}
			w.WriteHeader(202)
		case runtimePrefix + "/invocation/2/response":
			var result Response
			if json.NewDecoder(r.Body).Decode(&result) != nil || result.StatusCode != 202 || result.Body != "ok" {
				runtimeFailure.Store(true)
			}
			w.WriteHeader(202)
			cancel()
		default:
			t.Errorf("unexpected Runtime path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer runtime.Close()
	var called atomic.Bool
	err := run(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Store(true)
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) <= 0 {
			runtimeFailure.Store(true)
		}
		w.WriteHeader(202)
		_, _ = w.Write([]byte("ok"))
	}), runtime.URL, runtime.Client())
	if !errors.Is(err, context.Canceled) || !called.Load() || runtimeFailure.Load() {
		t.Fatalf("runtime failed: %v, called %v, failed %v", err, called.Load(), runtimeFailure.Load())
	}
}

func TestRuntimeDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var called atomic.Bool
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", "deadline")
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(25*time.Millisecond).UnixMilli(), 10))
			_ = json.NewEncoder(w).Encode(event())
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/deadline/error") {
			t.Errorf("expected deadline error, got %s", r.URL.Path)
		}
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), "deadline exceeded") {
			t.Errorf("error: %s", data)
		}
		w.WriteHeader(202)
		cancel()
	}))
	defer runtime.Close()
	err := run(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called.Store(true); <-r.Context().Done() }), runtime.URL, runtime.Client())
	if !errors.Is(err, context.Canceled) || !called.Load() {
		t.Fatalf("deadline run: %v", err)
	}
}

func TestRuntimeShutdownCancelsLongPoll(t *testing.T) {
	started := make(chan struct{})
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer runtime.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, http.NotFoundHandler(), runtime.URL, runtime.Client()) }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("shutdown: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown stuck")
	}
}

func TestRuntimeFailures(t *testing.T) {
	for _, test := range []struct {
		name         string
		nextStatus   int
		id, deadline string
		postStatus   int
	}{
		{"next failure", 500, "1", "valid", 202},
		{"no invocation id", 200, "", "valid", 202},
		{"invalid invocation id", 200, "../evil", "valid", 202},
		{"failed error report", 200, "1", "invalid", 500},
		{"failed response report", 200, "1", "valid", 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls > 2 {
					t.Error("continued after runtime failure")
					w.WriteHeader(500)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/next") {
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", test.id)
					deadline := test.deadline
					if deadline == "valid" {
						deadline = strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10)
					}
					w.Header().Set("Lambda-Runtime-Deadline-Ms", deadline)
					w.WriteHeader(test.nextStatus)
					_ = json.NewEncoder(w).Encode(event())
					return
				}
				w.WriteHeader(test.postStatus)
			}))
			defer runtime.Close()
			err := run(context.Background(), http.NotFoundHandler(), runtime.URL, runtime.Client())
			if err == nil {
				t.Fatal("expected runtime error")
			}
		})
	}
}

func TestRuntimeEndpoint(t *testing.T) {
	for _, value := range []string{"", "https://host:80", "host", "host:0", "host:65536", "host:abc", "user@host:80", "host/path:80"} {
		if _, err := runtimeEndpoint(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	for _, value := range []string{"127.0.0.1:9001", "[::1]:9001", "runtime.internal:9001"} {
		if got, err := runtimeEndpoint(value); err != nil || got != "http://"+value {
			t.Fatalf("endpoint %q %s %v", value, got, err)
		}
	}
	t.Setenv("AWS_LAMBDA_RUNTIME_API", "bad")
	if err := Run(context.Background(), http.NotFoundHandler()); err == nil {
		t.Fatal("invalid env accepted")
	}
	if err := Run(context.Background(), nil); err == nil {
		t.Fatal("nil handler accepted")
	}
}

func TestRunUsesEnvironmentAndCancels(t *testing.T) {
	started := make(chan struct{})
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer runtime.Close()
	t.Setenv("AWS_LAMBDA_RUNTIME_API", strings.TrimPrefix(runtime.URL, "http://"))
	// Proxy environment variables must not affect the link-local Runtime API.
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, http.NotFoundHandler()) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Run did not reach local Runtime API")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not exit")
	}
}

func TestRuntimeRejectsOversizedEnvelope(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", "large")
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
			_ = json.NewEncoder(w).Encode(event())
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/large/error") || r.Header.Get("Lambda-Runtime-Function-Error-Type") != "Unhandled" {
			t.Errorf("incorrect envelope failure: %s %#v", r.URL.Path, r.Header)
		}
		data, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(data), "envelope exceeds") {
			t.Errorf("expected bounded error report: %s", data)
		}
		w.WriteHeader(202)
		cancel()
	}))
	defer runtime.Close()
	err := run(ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Large", strings.Repeat("a", 6*1024*1024))
	}), runtime.URL, runtime.Client())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("oversized run: %v", err)
	}
}
