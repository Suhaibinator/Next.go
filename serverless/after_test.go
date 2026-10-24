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

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestRuntimeAfterResponseOrdering(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(strconv.FormatBool(streaming), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			published := make(chan struct{})
			started := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan struct{})
			nextAgain := make(chan struct{})
			var nexts atomic.Int32
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					if nexts.Add(1) > 1 {
						close(nextAgain)
						w.WriteHeader(503)
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "after")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(afterEvent())
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/after/response") {
					t.Errorf("unexpected report: %s", r.URL.Path)
				}
				if _, err := io.Copy(io.Discard, r.Body); err != nil {
					t.Error(err)
				}
				// A job may start only after this report has been accepted.
				close(published)
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			app, err := nextgo.New(nextgo.Config{Routes: []nextgo.Route{{Pattern: "/", API: func(w http.ResponseWriter, r *http.Request) {
				if err := nextgo.After(r.Context(), func(work context.Context) error {
					select {
					case <-published:
					default:
						t.Error("callback started before response accepted")
					}
					if work.Err() != nil {
						t.Error("callback inherited ended request")
					}
					close(started)
					select {
					case <-release:
					case <-work.Done():
						return work.Err()
					}
					close(finished)
					return nil
				}); err != nil {
					t.Error(err)
				}
				_, _ = w.Write([]byte("response before work"))
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				done <- runWithOptions(ctx, app, runtime.URL, runtime.Client(), runtimeOptions{streaming: streaming})
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("work never started")
			}
			select {
			case <-nextAgain:
				t.Fatal("requested next invocation before work drained")
			case <-time.After(25 * time.Millisecond):
			}
			close(release)
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("expected final Runtime API error")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("runtime did not drain work")
			}
			select {
			case <-finished:
			default:
				t.Fatal("work not finished before next invocation")
			}
			drain, cancelDrain := context.WithTimeout(context.Background(), time.Second)
			defer cancelDrain()
			if err := app.Close(drain); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeAfterRejectedResponse(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming", "envelope error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var called atomic.Bool
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "reject")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(afterEvent())
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				if mode == "envelope error" {
					if !strings.HasSuffix(r.URL.Path, "/error") {
						t.Error("expected error envelope")
					}
					w.WriteHeader(202)
					cancel()
					return
				}
				w.WriteHeader(503)
			}))
			defer runtime.Close()
			app, err := nextgo.New(nextgo.Config{Routes: []nextgo.Route{{Pattern: "/", API: func(w http.ResponseWriter, r *http.Request) {
				if err := nextgo.After(r.Context(), func(context.Context) error { called.Store(true); return nil }); err != nil {
					t.Error(err)
				}
				if mode == "envelope error" {
					w.Header().Set("X-Large", strings.Repeat("x", 6<<20))
				}
				_, _ = w.Write([]byte("ok"))
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			err = runWithOptions(ctx, app, runtime.URL, runtime.Client(), runtimeOptions{streaming: mode == "streaming"})
			if err == nil {
				t.Fatal("expected runtime failure or cancellation")
			}
			drain, cancelDrain := context.WithTimeout(context.Background(), time.Second)
			defer cancelDrain()
			if err := app.Close(drain); err != nil {
				t.Fatal(err)
			}
			if called.Load() {
				t.Fatal("rejected response ran work")
			}
		})
	}
}

func TestRuntimeAfterDeadline(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(strconv.FormatBool(streaming), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var nexts atomic.Int32
			doneWork := make(chan error, 1)
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					if nexts.Add(1) > 1 {
						w.WriteHeader(503)
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "deadline")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(150*time.Millisecond).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(afterEvent())
					return
				}
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			app, err := nextgo.New(nextgo.Config{Routes: []nextgo.Route{{Pattern: "/", API: func(w http.ResponseWriter, r *http.Request) {
				if err := nextgo.After(r.Context(), func(work context.Context) error { <-work.Done(); doneWork <- work.Err(); return work.Err() }); err != nil {
					t.Error(err)
				}
				_, _ = w.Write([]byte("ok"))
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			if err := runWithOptions(ctx, app, runtime.URL, runtime.Client(), runtimeOptions{streaming: streaming}); err == nil {
				t.Fatal("expected final Runtime API error")
			}
			drain, cancelDrain := context.WithTimeout(context.Background(), time.Second)
			defer cancelDrain()
			if err := app.Close(drain); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-doneWork:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("work deadline: %v", err)
				}
			default:
				t.Fatal("work did not observe invocation deadline")
			}
		})
	}
}

func afterEvent() Request {
	e := event()
	e.RawPath = "/"
	e.RequestContext.HTTP.Method = "GET"
	return e
}
