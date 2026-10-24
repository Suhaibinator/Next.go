package nextgo

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func drainAfter(t *testing.T, a *App) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAfterRequestLifecycle(t *testing.T) {
	type key struct{}
	requestCtx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "request value"))
	var handlerDone atomic.Bool
	var loads atomic.Int32
	var original context.Context
	result := make(chan error, 1)
	a, err := New(Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		original = c.Context
		_, err := Memo(c.Context, "load", func(context.Context) (int32, error) { return loads.Add(1), nil })
		if err != nil {
			return Page{}, err
		}
		if err := After(c.Context, func(work context.Context) error {
			if !handlerDone.Load() {
				result <- errors.New("started before handler finished")
				return nil
			}
			if original.Err() == nil { // request cleanup may run after Finish dispatch, but cannot cancel this work.
				<-original.Done()
			}
			if work.Err() != nil || work.Value(key{}) != "request value" {
				result <- errors.New("lost independent context or values")
				return nil
			}
			if _, ok := work.Deadline(); !ok {
				result <- errors.New("missing work budget")
				return nil
			}
			count, err := Memo(work, "load", func(context.Context) (int32, error) { return loads.Add(1), nil })
			if err == nil && count != 2 {
				err = errors.New("reused request memo scope")
			}
			if err == nil && !errors.Is(After(work, func(context.Context) error { return nil }), ErrAfterScope) {
				err = errors.New("allowed nested registration after completion")
			}
			result <- err
			return nil
		}); err != nil {
			return Page{}, err
		}
		cancel()
		handlerDone.Store(true)
		return Page{Body: ui.Text("useful HTML")}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil).WithContext(requestCtx))
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("work did not complete")
	}
	drainAfter(t, a)
	if !errors.Is(After(original, func(context.Context) error { return nil }), ErrAfterScope) {
		t.Fatal("late registration allowed")
	}
	if !errors.Is(After(context.Background(), func(context.Context) error { return nil }), ErrAfterScope) || !errors.Is(After(nil, nil), ErrAfterScope) {
		t.Fatal("scope validation")
	}
}

func TestAfterCapacityDrainAndCancellation(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	var registration error
	a, err := New(Config{After: AfterOptions{Concurrency: 1, MaxPending: 1, Timeout: time.Second}, Routes: []Route{{Pattern: "/", API: func(w http.ResponseWriter, r *http.Request) {
		if err := After(r.Context(), func(work context.Context) error { close(started); <-work.Done(); close(finished); return work.Err() }); err != nil {
			t.Error(err)
		}
		registration = After(r.Context(), func(context.Context) error { return nil })
		w.WriteHeader(204)
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	a.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !errors.Is(registration, ErrAfterCapacity) {
		t.Fatalf("capacity: %v", registration)
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if !errors.Is(a.Close(ctx), context.DeadlineExceeded) {
		t.Fatal("drain did not wait")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel work")
	}
	drainAfter(t, a)
	// Closing affects only new work reservations, not normal serving.
	a.config.Routes = nil
	a.routes = []Route{{Pattern: "/", API: func(w http.ResponseWriter, r *http.Request) {
		registration = After(r.Context(), func(context.Context) error { return nil })
		w.WriteHeader(204)
	}}}
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	if rr.Code != 204 || !errors.Is(registration, ErrAppClosed) {
		t.Fatalf("closed behavior: %d %v", rr.Code, registration)
	}
}

func TestAfterFailuresReleaseCapacity(t *testing.T) {
	var output bytes.Buffer
	a, err := New(Config{Logger: log.New(&output, "", 0), After: AfterOptions{Concurrency: 1, MaxPending: 4, Timeout: time.Second}, Routes: []Route{{Pattern: "/", API: func(w http.ResponseWriter, r *http.Request) {
		callbacks := []func(context.Context) error{
			func(context.Context) error { panic("secret panic text") },
			func(context.Context) error { runtime.Goexit(); return nil },
			func(context.Context) error { return errors.New("secret error text") },
			func(context.Context) error { return nil },
		}
		for _, callback := range callbacks {
			if err := After(r.Context(), callback); err != nil {
				t.Error(err)
			}
		}
		w.WriteHeader(202)
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
	drainAfter(t, a)
	if rr.Code != 202 || strings.Count(output.String(), "nextgo after:") != 3 || strings.Contains(output.String(), "secret") {
		t.Fatalf("failure handling: %d %s", rr.Code, output.String())
	}
}

func TestAfterRejectsSharedPageLoadButMiddlewareRunsPerRequest(t *testing.T) {
	var middlewareCalls atomic.Int32
	var loaderCalls atomic.Int32
	var registration error
	a, err := New(Config{Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := After(r.Context(), func(context.Context) error { middlewareCalls.Add(1); return nil }); err != nil {
				t.Error(err)
			}
			next.ServeHTTP(w, r)
		})
	}}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) {
		loaderCalls.Add(1)
		registration = After(c.Context, func(context.Context) error { t.Error("shared page scheduled response work"); return nil })
		return Page{Body: ui.Text("public page")}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		rr := httptest.NewRecorder()
		a.ServeHTTP(rr, httptest.NewRequest("GET", "/", nil))
		if rr.Code != 200 {
			t.Fatal(rr.Code)
		}
	}
	drainAfter(t, a)
	if !errors.Is(registration, ErrAfterCache) || loaderCalls.Load() != 1 || middlewareCalls.Load() != 2 {
		t.Fatalf("registration=%v loaders=%d middleware=%d", registration, loaderCalls.Load(), middlewareCalls.Load())
	}
}
