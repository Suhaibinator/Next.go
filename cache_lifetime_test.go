package nextgo

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

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func lifetimeApp(t *testing.T, load PageFunc, metadata []MetadataLayer) (*App, *atomic.Int64) {
	t.Helper()
	clock := &atomic.Int64{}
	clock.Store(time.Now().UnixNano())
	store := cache.New(8, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) }), cache.WithMaxBytes(1<<20, nil))
	app := testApp(t, Config{Cache: store, Headers: http.Header{"Cache-Control": {"public, max-age=1000"}, "X-Nextgo-Prefetch": {"public"}}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{Lifetime: &cache.Lifetime{Revalidate: 10 * time.Second, Expire: time.Minute}, Tags: []string{"page"}}, Page: load, Metadata: metadata}}})
	return app, clock
}

func collectedLifetime(t *testing.T, app *App) (*httptest.ResponseRecorder, Envelope, *postresponse.Collector) {
	t.Helper()
	ctx, collector := postresponse.Collect(context.Background())
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	r.Header.Set("X-Nextgo-Navigate", "1")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	var data Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || strings.Contains(w.Header().Get("Cache-Control"), "public") || w.Header().Get("X-Nextgo-Prefetch") != "" {
		t.Fatal("server lifetime advertised browser/CDN reuse", w.Header())
	}
	return w, data, collector
}

func TestPageLifetimeRefreshKeepsBodyHeadAndMemoTogether(t *testing.T) {
	var revision, reads atomic.Int64
	revision.Store(1)
	read := func(c *Context) (int64, error) {
		return Memo(c.Context, "revision", func(context.Context) (int64, error) { reads.Add(1); return revision.Load(), nil })
	}
	app, clock := lifetimeApp(t, func(c *Context) (Page, error) {
		if !errors.Is(After(c.Context, func(context.Context) error { return nil }), ErrAfterCache) {
			t.Error("public renderer registered nested lifecycle work")
		}
		value, err := read(c)
		return Page{Body: ui.Text(fmt.Sprint(value))}, err
	}, []MetadataLayer{{Load: func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
		value, err := read(c)
		return seo.Metadata{Title: fmt.Sprintf("Revision %d", value)}, err
	}}})
	_, first, done := collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	if first.HTML != "1" || first.Title != "Revision 1" {
		t.Fatal(first)
	}
	revision.Store(2)
	clock.Add(int64(10 * time.Second))
	_, stale, done := collectedLifetime(t, app)
	if stale.HTML != "1" || reads.Load() != 1 {
		t.Fatal("stale response blocked or ran work before publication", stale, reads.Load())
	}
	done.Finish(context.Background(), true)
	_, fresh, done := collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	if fresh.HTML != "2" || fresh.Title != "Revision 2" || reads.Load() != 2 {
		t.Fatal("background refresh reused stale Memo/head", fresh, reads.Load())
	}
}

func TestPageLifetimeFailurePolicyAndHardExpiry(t *testing.T) {
	for _, status := range []int{500, 404, 401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var broken atomic.Bool
			app, clock := lifetimeApp(t, func(*Context) (Page, error) {
				if broken.Load() {
					switch status {
					case 404:
						return Page{}, ErrNotFound
					case 401:
						return Page{}, ErrUnauthorized
					case 403:
						return Page{}, ErrForbidden
					}
					return Page{}, errors.New("private backend error")
				}
				return Page{Title: "Good snapshot", Body: ui.Text("good")}, nil
			}, nil)
			_, _, done := collectedLifetime(t, app)
			done.Finish(context.Background(), true)
			broken.Store(true)
			clock.Add(int64(10 * time.Second))
			_, stale, done := collectedLifetime(t, app)
			if stale.HTML != "good" {
				t.Fatal("last good value lost before refresh", stale)
			}
			done.Finish(context.Background(), true)
			want := 0
			if status == 500 {
				want = 1
			}
			if app.config.Cache.Len() != want {
				t.Fatal("permanent/auth error retained or transient failure dropped snapshot", status, app.config.Cache.Len())
			}
			clock.Add(int64(50 * time.Second))
			w, failed, done := collectedLifetime(t, app)
			done.Finish(context.Background(), true)
			if w.Code != status || failed.HTML == "good" || failed.Title == "Good snapshot" {
				t.Fatal("hard/permanent failure returned old page", w.Code, failed)
			}
		})
	}
}

func TestPageLifetimeRejectedPublicationReleasesRefreshPlan(t *testing.T) {
	var calls atomic.Int64
	app, clock := lifetimeApp(t, func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(calls.Add(1)))}, nil }, nil)
	_, _, done := collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	clock.Add(int64(10 * time.Second))
	_, _, done = collectedLifetime(t, app)
	done.Finish(context.Background(), false)
	if calls.Load() != 1 {
		t.Fatal("rejected publication refreshed")
	}
	_, _, done = collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	_, data, done := collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	if calls.Load() != 2 || data.HTML != "2" {
		t.Fatal("discarded reservation stranded future refresh", calls.Load(), data)
	}
}

func TestPageLifetimeClosedSchedulerFallsBackToFreshBlockingRead(t *testing.T) {
	var calls atomic.Int64
	app, clock := lifetimeApp(t, func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(calls.Add(1)))}, nil }, nil)
	_, _, done := collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	if err := app.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	clock.Add(int64(10 * time.Second))
	_, fresh, done := collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	if calls.Load() != 2 || fresh.HTML != "2" {
		t.Fatal("closed scheduler silently skipped refresh", fresh, calls.Load())
	}
}

func TestPageLifetimeConfigurationValidation(t *testing.T) {
	for _, policy := range []*CachePolicy{{TTL: time.Second, Lifetime: &cache.Lifetime{Expire: time.Minute}}, {Lifetime: &cache.Lifetime{}}, {Lifetime: &cache.Lifetime{Revalidate: time.Minute, Expire: time.Second}}} {
		app, err := New(Config{Routes: []Route{{Pattern: "/", Page: static("page"), Cache: policy}}})
		if err == nil {
			_ = app.Close(context.Background())
			t.Fatal("invalid page lifetime accepted")
		}
	}
}

func TestPageLifetimeFreezesInputsBeforeMiddlewareReturns(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	store := cache.New(4, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	app := testApp(t, Config{Cache: store, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Input", "public")
			next.ServeHTTP(w, r)
			r.Header.Set("X-Input", "private")
			r.URL.RawQuery = "q=private"
		})
	}}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{Lifetime: &cache.Lifetime{Revalidate: time.Second, Expire: time.Minute}}, Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text(c.Request.Header.Get("X-Input") + "|" + c.Request.URL.RawQuery)}, nil
	}}}})
	_, first, done := collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	if first.HTML != "public|" {
		t.Fatal(first)
	}
	clock.Add(int64(time.Second))
	_, _, done = collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	_, fresh, done := collectedLifetime(t, app)
	done.Finish(context.Background(), true)
	if fresh.HTML != "public|" {
		t.Fatal("refresh observed request mutation after response", fresh)
	}
}

func TestPublicCacheBypassesRequestsCarryingBodies(t *testing.T) {
	app, _ := lifetimeApp(t, func(c *Context) (Page, error) {
		body, err := io.ReadAll(c.Request.Body)
		return Page{Body: ui.Text(string(body))}, err
	}, nil)
	for _, value := range []string{"first", "second"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "/", strings.NewReader(value)))
		if w.Code != 200 || !strings.Contains(w.Body.String(), value) || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Nextgo-Prefetch") != "" {
			t.Fatal("body-dependent output shared", w.Code, w.Body.String())
		}
	}
	if app.config.Cache.Len() != 0 {
		t.Fatal("request body entered public cache")
	}
}
