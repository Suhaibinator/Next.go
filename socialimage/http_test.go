//go:build !js

package socialimage

import (
	"bytes"
	"context"
	"errors"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func smallDocument() Document { return Document{Width: 32, Height: 32, Root: Panel{Color: red}} }

func newHandler(t *testing.T, c Config, build Builder) *Handler {
	t.Helper()
	h, err := NewHandler(c, build)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func get(h http.Handler, method, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.test/social", nil)
	if etag != "" {
		r.Header.Set("If-None-Match", etag)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHTTPETagsCacheAndAuthorizationEveryRequest(t *testing.T) {
	calls := 0
	denied := false
	h := newHandler(t, Config{}, func(context.Context, *http.Request) (Document, error) {
		calls++
		if denied {
			return Document{}, ErrNotFound
		}
		return smallDocument(), nil
	})
	w := get(h, "GET", "")
	if w.Code != 200 || w.Header().Get("Content-Type") != "image/png" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(w.Header().Get("Cache-Control"), "private,") {
		t.Fatal(w.Code, w.Header())
	}
	if _, err := png.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Fatal(err)
	}
	etag := w.Header().Get("ETag")
	if etag == "" || h.lru.Len() != 1 || h.cacheBytes != w.Body.Len() {
		t.Fatal("cache accounting", h.cacheBytes, w.Body.Len())
	}
	for _, value := range []string{etag, "W/" + etag, `"other", W/` + etag, "*"} {
		w = get(h, "GET", value)
		if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "" {
			t.Fatal("conditional", value, w.Code, w.Header())
		}
	}
	w = get(h, "HEAD", "")
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") == "" || w.Header().Get("ETag") != etag {
		t.Fatal("HEAD", w.Code, w.Header())
	}
	denied = true
	w = get(h, "GET", etag)
	if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" || calls != 7 {
		t.Fatal("authorization was skipped on a cached response", w.Code, calls)
	}
	if w.Header().Get("ETag") != "" {
		t.Fatal("denial exposed success ETag")
	}
}

func TestHTTPPrivacyErrorsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{errors.New("secret database password"), 500}, {ErrBadRequest, 400}, {ErrNotFound, 404}} {
		h := newHandler(t, Config{}, func(context.Context, *http.Request) (Document, error) { return Document{}, tc.err })
		w := get(h, "GET", "")
		if w.Code != tc.status || strings.Contains(w.Body.String(), "secret") || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(w.Code, w.Body.String(), w.Header())
		}
		w = get(h, "HEAD", "")
		if w.Code != tc.status || w.Body.Len() != 0 {
			t.Fatal("HEAD error body", w.Code, w.Body.String())
		}
	}
	h := newHandler(t, Config{Public: true, MaxRequestBytes: 20}, func(context.Context, *http.Request) (Document, error) { return smallDocument(), nil })
	w := get(h, "GET", "")
	if !strings.HasPrefix(w.Header().Get("Cache-Control"), "public,") {
		t.Fatal(w.Header())
	}
	w = get(h, "POST", "")
	if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal(w.Code, w.Header())
	}
	r := httptest.NewRequest("GET", "http://example.test/social?"+strings.Repeat("x", 21), nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 414 {
		t.Fatal("request bound", w.Code)
	}
	invalid := newHandler(t, Config{}, func(context.Context, *http.Request) (Document, error) { return Document{Width: 99999}, nil })
	w = get(invalid, "GET", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), "dimensions") || invalid.lru.Len() != 0 {
		t.Fatal("invalid documents leak or cache", w.Code, w.Body.String())
	}
	for _, c := range []Config{{MaxConcurrent: 65}, {MaxCacheBytes: -1}, {MaxCacheEntries: 4097}, {MaxRequestBytes: 65537}} {
		if _, err := NewHandler(c, func(context.Context, *http.Request) (Document, error) { return smallDocument(), nil }); !errors.Is(err, ErrInvalid) {
			t.Fatal("configuration", c, err)
		}
	}
	if _, err := NewHandler(Config{}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal("nil builder", err)
	}
}

func TestContentCacheIsolationEvictionAndDisable(t *testing.T) {
	n := uint8(0)
	build := func(context.Context, *http.Request) (Document, error) {
		d := smallDocument()
		d.Root = Panel{Color: color.NRGBA{R: n, A: 255}}
		return d, nil
	}
	h := newHandler(t, Config{MaxCacheEntries: 2}, build)
	a := get(h, "GET", "")
	n = 1
	b := get(h, "GET", "")
	n = 2
	get(h, "GET", "")
	if a.Header().Get("ETag") == b.Header().Get("ETag") || h.lru.Len() != 2 {
		t.Fatal("content cache key/entry bound")
	}
	var sum int
	for _, e := range h.entries {
		sum += len(e.Value.(*encoded).data)
	}
	if sum != h.cacheBytes {
		t.Fatal("eviction accounting")
	}
	tiny := newHandler(t, Config{MaxCacheBytes: 1}, build)
	get(tiny, "GET", "")
	if tiny.cacheBytes != 0 || tiny.lru.Len() != 0 {
		t.Fatal("byte cache bound")
	}
	disabled := newHandler(t, Config{DisableCache: true}, build)
	get(disabled, "GET", "")
	if disabled.cacheBytes != 0 || disabled.lru.Len() != 0 {
		t.Fatal("disable cache")
	}
}

func TestCanceledNoncooperativeBuilderRetainsAdmission(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h := newHandler(t, Config{MaxConcurrent: 1}, func(context.Context, *http.Request) (Document, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return smallDocument(), nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("GET", "http://example.test/social", nil).WithContext(ctx)
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); h.ServeHTTP(first, r) }()
	<-entered
	cancel()
	waitCtx, waitCancel := context.WithCancel(context.Background())
	waitDone := make(chan struct{})
	go func() { defer close(waitDone); h.ServeHTTP(httptest.NewRecorder(), r.WithContext(waitCtx)) }()
	// The first callback is still physically running. Its slot must stay held.
	waitCancel()
	select {
	case <-waitDone:
	case <-time.After(time.Second):
		t.Fatal("queued cancellation did not return")
	}
	if calls.Load() != 1 || len(h.workers) != 1 {
		t.Fatal("canceled callback released a live physical slot")
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("builder did not finish")
	}
	if first.Body.Len() != 0 || h.lru.Len() != 0 || len(h.workers) != 0 {
		t.Fatal("canceled work was published/cached")
	}
	if w := get(h, "GET", ""); w.Code != 200 {
		t.Fatal("slot did not recover", w.Code)
	}
}

func TestConcurrentHTTP(t *testing.T) {
	h := newHandler(t, Config{MaxConcurrent: 2}, func(context.Context, *http.Request) (Document, error) { return smallDocument(), nil })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := get(h, "GET", ""); w.Code != 200 {
				t.Errorf("status %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if h.lru.Len() != 1 || len(h.workers) != 0 {
		t.Fatal("concurrent cache/admission accounting")
	}
}
