package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestDefaultProfileRetainsLastGoodButNeverRenewsBrowserAge(t *testing.T) {
	var clock, revision atomic.Int64
	clock.Store(time.Now().UnixNano())
	revision.Store(1)
	store := cache.New(4, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	var broken atomic.Bool
	app := testApp(t, Config{Cache: store, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{Profile: "default", Tags: []string{"page"}}, Page: func(*Context) (Page, error) {
		if broken.Load() {
			return Page{}, fmt.Errorf("backend failed")
		}
		v := revision.Load()
		return Page{Title: fmt.Sprintf("Revision %d", v), Body: ui.Text(fmt.Sprint(v))}, nil
	}}}})
	read := func() (Envelope, http.Header, *postresponse.Collector) {
		t.Helper()
		ctx, done := postresponse.Collect(context.Background())
		r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
		r.Header.Set("X-Nextgo-Navigate", "1")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		var e Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || w.Code != 200 {
			t.Fatal(err, w.Body.String())
		}
		return e, w.Header(), done
	}
	first, h, done := read()
	done.Finish(context.Background(), true)
	window, err := strconv.ParseInt(h.Get(navigation.StaleHeader), 10, 64)
	if first.HTML != "1" || err != nil || window <= 0 || window > 300000 || h.Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatal(first, h)
	}
	revision.Store(2)
	broken.Store(true)
	clock.Add(int64(2 * 365 * 24 * time.Hour))
	stale, h, done := read()
	if stale.HTML != "1" || stale.Title != "Revision 1" || h.Get(navigation.StaleHeader) != "" || h.Get("X-Nextgo-Prefetch") != "" || h.Get("Cache-Control") != "no-store" {
		t.Fatal("old generation regained browser freshness", stale, h)
	}
	done.Finish(context.Background(), true)
	if store.Len() != 1 {
		t.Fatal("transient error lost non-expiring snapshot")
	}
	broken.Store(false)
	if err := app.RevalidateTagProfile("page", "default"); err != nil {
		t.Fatal(err)
	}
	stale, _, done = read()
	if stale.HTML != "1" {
		t.Fatal(stale)
	}
	done.Finish(context.Background(), true)
	fresh, h, done := read()
	done.Finish(context.Background(), true)
	if fresh.HTML != "2" || fresh.Title != "Revision 2" || h.Get(navigation.StaleHeader) == "" {
		t.Fatal(fresh, h)
	}
	revision.Store(3)
	app.RevalidatePath("/")
	fresh, _, done = read()
	done.Finish(context.Background(), true)
	if fresh.HTML != "3" {
		t.Fatal("strict invalidation retained non-expiring snapshot", fresh)
	}
}

func TestDefaultProfileOverrideOwnsConfigurationAndSnapshots(t *testing.T) {
	store := cache.New(8)
	makeApp := func(profile *cache.Profile, body string) *App {
		cfg := Config{Cache: store, BuildID: "same", Routes: []Route{{Pattern: "/", Cache: &CachePolicy{Profile: "default"}, Page: static(body)}}}
		if profile != nil {
			cfg.CacheProfiles = map[string]cache.Profile{"default": *profile}
		}
		return testApp(t, cfg)
	}
	unlimited := makeApp(nil, "unlimited")
	_, e := runtimeEnvelope(t, unlimited, "/", nil)
	if e.HTML != "unlimited" {
		t.Fatal(e)
	}
	finite := cache.BuiltinProfiles()["default"]
	finite.Expire = time.Hour
	bounded := makeApp(&finite, "bounded")
	if unlimited.configurationID == bounded.configurationID {
		t.Fatal("different hard expiry shared configuration")
	}
	_, e = runtimeEnvelope(t, bounded, "/", nil)
	if e.HTML != "bounded" {
		t.Fatal("different policies shared snapshot", e)
	}
	if store.Len() != 2 {
		t.Fatal("snapshot namespaces collided")
	}
}
