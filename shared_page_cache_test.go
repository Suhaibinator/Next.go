package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func sharedPageStore(t *testing.T, backend shared.Backend, namespace, schema string) *shared.Store {
	t.Helper()
	value, err := shared.New(backend, namespace, schema, shared.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func sharedPageMemory(t *testing.T) (*shared.Memory, *shared.Store) {
	t.Helper()
	m, err := shared.NewMemory(32, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	return m, sharedPageStore(t, m, "page-tests", "snapshot-v1")
}
func sharedPageApp(t *testing.T, c Config) *App {
	t.Helper()
	if c.BuildID == "" {
		c.BuildID = "shared-page-tests-v1"
	}
	a := testApp(t, c)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := a.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return a
}
func sharedPageRead(t *testing.T, a *App, target string, nav bool, mutate func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	w, done := sharedPageCollected(t, a, target, nav, mutate)
	done.Finish(context.Background(), false)
	return w
}
func sharedPageCollected(t *testing.T, a *App, target string, nav bool, mutate func(*http.Request)) (*httptest.ResponseRecorder, *postresponse.Collector) {
	t.Helper()
	ctx, done := postresponse.Collect(context.Background())
	r := httptest.NewRequest("GET", target, nil).WithContext(ctx)
	if nav {
		r.Header.Set("X-Nextgo-Navigate", "1")
	}
	if mutate != nil {
		mutate(r)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w, done
}
func sharedPageEnvelope(t *testing.T, w *httptest.ResponseRecorder) Envelope {
	t.Helper()
	var e Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err, w.Code, w.Body.String())
	}
	return e
}
func sharedPageRoute(page PageFunc, policy *CachePolicy) Route {
	return Route{Pattern: "/", Page: page, Cache: policy}
}

func TestSharedPageCacheReusesHTMLHeadDocumentAcrossApps(t *testing.T) {
	m, s := sharedPageMemory(t)
	var firstLoads, secondLoads atomic.Int32
	data, err := seo.JSONLD(map[string]string{"name": "a </script><img src=x onerror=1>"})
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{"robots": "index"}
	first := func(*Context) (Page, error) {
		firstLoads.Add(1)
		return Page{
			Title: "Snapshot <title>&", Description: "SSR description", Meta: meta, JSONLD: []seo.StructuredData{data},
			Metadata:  seo.Metadata{Language: "ar", Direction: "rtl", Canonical: "https://public.example/article", Viewport: &seo.Viewport{InitialScale: 1.25, ThemeColors: []seo.ThemeColor{{Color: "#112233"}}}},
			Resources: []assets.Resource{assets.Stylesheet("/assets/app.css")}, Body: ui.Text("SSR <p>&"),
		}, nil
	}
	layout := func(_ *Context, n ui.Node) (ui.Node, error) {
		return ui.El("article", ui.Attrs{"data-shared-layout": ""}, n), nil
	}
	makeApp := func(page PageFunc) *App {
		r := sharedPageRoute(page, &CachePolicy{Shared: true, TTL: time.Minute, Tags: []string{"pages"}})
		r.Layouts = []LayoutFunc{layout}
		r.LayoutIDs = []string{"shared-root"}
		return sharedPageApp(t, Config{SharedCache: s, PreserveLayouts: true, Routes: []Route{r}})
	}
	a := makeApp(first)
	b := makeApp(func(*Context) (Page, error) {
		secondLoads.Add(1)
		return Page{Title: "Wrong replica", Body: ui.Text("not cached")}, nil
	})
	initial := sharedPageRead(t, a, "http://public.example/", false, nil)
	if initial.Code != 200 || !strings.Contains(initial.Body.String(), "Snapshot &lt;title&gt;&amp;") || !strings.Contains(initial.Body.String(), `<link data-nextgo-head="" href="/assets/app.css" rel="stylesheet">`) || strings.Contains(initial.Body.String(), `</script><img src=x`) {
		t.Fatal(initial.Code, initial.Body.String())
	}
	meta["robots"] = "noindex" // Neither local mutable maps nor another replica may alter the snapshot.
	nav := sharedPageRead(t, b, "http://public.example/", true, nil)
	e := sharedPageEnvelope(t, nav)
	if nav.Code != 200 || e.Title != "Snapshot <title>&" || e.Description != "SSR description" || e.Meta["robots"] != "index" || !strings.Contains(e.HTML, "SSR &lt;p&gt;&amp;") || !strings.Contains(e.HTML, "data-shared-layout") || e.Document.Language != "ar" || e.Document.Direction != "rtl" || !strings.Contains(e.Document.Viewport, "1.25") {
		t.Fatal(nav.Code, e)
	}
	var canonical, stylesheet, jsonld, theme bool
	for _, element := range e.Head {
		canonical = canonical || element.Tag == "link" && element.Attrs["rel"] == "canonical" && element.Attrs["href"] == "https://public.example/article"
		stylesheet = stylesheet || element.Attrs["rel"] == "stylesheet"
		jsonld = jsonld || element.Tag == "script" && element.Attrs["type"] == "application/ld+json"
		theme = theme || element.Attrs["name"] == "theme-color"
	}
	if !canonical || !stylesheet || !jsonld || !theme || firstLoads.Load() != 1 || secondLoads.Load() != 0 {
		t.Fatal("snapshot omitted head or ran replica loader", e.Head, firstLoads.Load(), secondLoads.Load())
	}
	if keys, _ := m.Stats(); keys != 1 {
		t.Fatal(keys)
	}
	if a.config.Cache.Len() != 0 || b.config.Cache.Len() != 0 {
		t.Fatal("shared snapshots also entered process-local L1")
	}
}

func TestSharedPageCacheIsolatesBuildConfigHostQueryAndScope(t *testing.T) {
	m, s := sharedPageMemory(t)
	var loads atomic.Int32
	makeApp := func(build string, store *shared.Store, public string) *App {
		return sharedPageApp(t, Config{BuildID: build, SharedCache: store, Public: map[string]string{"variant": public}, Routes: []Route{sharedPageRoute(func(*Context) (Page, error) {
			return Page{Body: ui.Text(fmt.Sprintf("generation:%d", loads.Add(1)))}, nil
		}, &CachePolicy{Shared: true, TTL: time.Minute})}})
	}
	a := makeApp("one", s, "a")
	first := sharedPageEnvelope(t, sharedPageRead(t, a, "http://first.example/?q=one", true, nil)).HTML
	if same := sharedPageEnvelope(t, sharedPageRead(t, makeApp("one", s, "a"), "http://first.example/?q=one", true, nil)).HTML; same != first {
		t.Fatal("equivalent replicas did not share", same, first)
	}
	for _, tc := range []struct {
		app    *App
		target string
	}{
		{a, "http://first.example/?q=two"}, {a, "http://second.example/?q=one"}, {makeApp("two", s, "a"), "http://first.example/?q=one"}, {makeApp("one", s, "b"), "http://first.example/?q=one"},
		{makeApp("one", sharedPageStore(t, m, "other-pages", "snapshot-v1"), "a"), "http://first.example/?q=one"}, {makeApp("one", sharedPageStore(t, m, "page-tests", "snapshot-v2"), "a"), "http://first.example/?q=one"},
	} {
		w := sharedPageRead(t, tc.app, tc.target, true, nil)
		if w.Code != 200 || sharedPageEnvelope(t, w).HTML == first {
			t.Fatal("cache binding crossed key scope", tc.target, w.Code, w.Body.String())
		}
	}
	if loads.Load() != 7 {
		t.Fatal(loads.Load())
	}
}

func TestSharedPageCacheLongURIDoesNotExceedBackendKeyLimit(t *testing.T) {
	_, s := sharedPageMemory(t)
	var loads atomic.Int32
	a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("long query snapshot")}, nil }, &CachePolicy{Shared: true, TTL: time.Minute})}})
	for i := 0; i < 2; i++ {
		w := sharedPageRead(t, a, "http://public.example/?q="+strings.Repeat("x", 6000), true, nil)
		if w.Code != 200 || !strings.Contains(sharedPageEnvelope(t, w).HTML, "long query snapshot") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if loads.Load() != 1 {
		t.Fatal("long valid URI bypassed key-safe shared reuse", loads.Load())
	}
}

func TestSharedPageScopeIdentityDoesNotExposeConnectionSecrets(t *testing.T) {
	const tokenA = "opaque-private-shared-page-token-aaaaaaaa"
	const tokenB = "opaque-private-shared-page-token-bbbbbbbb"
	a, err := shared.NewHTTP("http://127.0.0.1:45381", tokenA, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := shared.NewHTTP("http://127.0.0.1:45382", tokenB, nil)
	if err != nil {
		t.Fatal(err)
	}
	x, y := sharedPageStore(t, a, "private-tenant", "snapshot-v1"), sharedPageStore(t, b, "private-tenant", "snapshot-v1")
	if x.ScopeID() != y.ScopeID() || !navigation.ValidConfigurationID(x.ScopeID()) {
		t.Fatal("logical scope depended on endpoint or credential", x.ScopeID(), y.ScopeID())
	}
	makeApp := func(s *shared.Store) *App {
		return sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(static("scope"), &CachePolicy{Shared: true, TTL: time.Minute})}})
	}
	ax, ay := makeApp(x), makeApp(y)
	if ax.configurationID != ay.configurationID {
		t.Fatal("connection rotation changed logical page ownership")
	}
	for _, secret := range []string{tokenA, tokenB, "45381", "45382", "private-tenant", "snapshot-v1"} {
		if strings.Contains(ax.navigationJSON, secret) {
			t.Fatal("browser manifest exposed cache configuration", secret, ax.navigationJSON)
		}
	}
}

func TestSharedPagePrivacyBypassesNeverTouchStorage(t *testing.T) {
	for _, kind := range []string{"cookie", "authorization", "body", "development", "nonce", "bypass", "set-cookie"} {
		t.Run(kind, func(t *testing.T) {
			m, s := sharedPageMemory(t)
			var loads atomic.Int32
			cfg := Config{SharedCache: s, Dev: kind == "development", Headers: http.Header{navigation.StaleHeader: {"9999"}, "X-Nextgo-Prefetch": {"public"}}, Routes: []Route{sharedPageRoute(func(c *Context) (Page, error) {
				loads.Add(1)
				if err := c.SetHeader("X-Private-Render", "ok"); err != nil {
					return Page{}, err
				}
				return Page{Body: ui.Text("private bypass")}, nil
			}, &CachePolicy{Shared: true, Profile: "seconds"})}}
			if kind == "set-cookie" {
				cfg.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						http.SetCookie(w, &http.Cookie{Name: "session", Value: "private"})
						next.ServeHTTP(w, r)
					})
				}}
			}
			a := sharedPageApp(t, cfg)
			for i := 0; i < 2; i++ {
				w := sharedPageRead(t, a, "http://public.example/", true, func(r *http.Request) {
					switch kind {
					case "cookie":
						r.Header.Set("Cookie", "session=private")
					case "authorization":
						r.Header.Set("Authorization", "Bearer private")
					case "body":
						r.Body = io.NopCloser(strings.NewReader("private"))
						r.ContentLength = 7
					case "nonce":
						ctx, err := scripts.WithNonce(r.Context(), "abcdefghijklmnopqrstuv")
						if err != nil {
							t.Fatal(err)
						}
						*r = *r.WithContext(ctx)
					case "bypass":
						*r = *r.WithContext(cache.WithBypass(r.Context()))
					}
				})
				if w.Code != 200 || w.Header().Get("X-Private-Render") != "ok" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get(navigation.StaleHeader) != "" || w.Header().Get("X-Nextgo-Prefetch") != "" {
					t.Fatal(kind, w.Code, w.Header(), w.Body.String())
				}
			}
			if keys, _ := m.Stats(); keys != 0 || loads.Load() != 2 || a.config.Cache.Len() != 0 {
				t.Fatal("private render retained shared/local state", keys, loads.Load(), a.config.Cache.Len())
			}
		})
	}
}

func TestSharedPageStreamAndParallelBypass(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(fmt.Sprint(parallel), func(t *testing.T) {
			m, s := sharedPageMemory(t)
			var loads atomic.Int32
			r := sharedPageRoute(func(*Context) (Page, error) {
				loads.Add(1)
				return Page{Stream: !parallel, Body: ui.Text("not a shared snapshot")}, nil
			}, &CachePolicy{Shared: true, TTL: time.Minute})
			if parallel {
				r.Layouts = []LayoutFunc{func(c *Context, n ui.Node) (ui.Node, error) {
					slot, err := c.Slot("panel")
					return ui.Fragment(n, slot), err
				}}
				r.LayoutIDs = []string{"owner"}
				r.Slots = []Slot{{ID: "owner.panel", Name: "panel", Base: "/", Layout: 0, Default: static("default panel")}}
			}
			a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{r}})
			for i := 0; i < 2; i++ {
				w := sharedPageRead(t, a, "http://public.example/", true, nil)
				if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get("X-Nextgo-Prefetch") != "" {
					t.Fatal(w.Code, w.Header(), w.Body.String())
				}
			}
			if keys, _ := m.Stats(); keys != 0 || loads.Load() != 2 {
				t.Fatal("stream/parallel snapshot retained", keys, loads.Load())
			}
		})
	}
}

func TestSharedPageTTLHasExactBlockingExpiry(t *testing.T) {
	_, s := sharedPageMemory(t)
	var loads atomic.Int32
	a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(loads.Add(1)))}, nil }, &CachePolicy{Shared: true, TTL: time.Second})}})
	for i := 0; i < 2; i++ {
		w := sharedPageRead(t, a, "http://public.example/", true, nil)
		if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "1" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	time.Sleep(1200 * time.Millisecond)
	w := sharedPageRead(t, a, "http://public.example/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "2" || loads.Load() != 2 {
		t.Fatal(w.Code, w.Body.String(), loads.Load())
	}
}

func TestSharedPageLifetimeRefreshKeepsMemoBodyHeadAndPublicationTogether(t *testing.T) {
	_, s := sharedPageMemory(t)
	var revision, reads atomic.Int64
	revision.Store(1)
	read := func(c *Context) (int64, error) {
		return Memo(c.Context, "shared-revision", func(context.Context) (int64, error) { reads.Add(1); return revision.Load(), nil })
	}
	makeApp := func() *App {
		r := sharedPageRoute(func(c *Context) (Page, error) {
			if err := After(c.Context, func(context.Context) error { return nil }); !errors.Is(err, ErrAfterCache) {
				t.Error("cached public loader registered completion work", err)
			}
			v, err := read(c)
			return Page{Body: ui.Text(fmt.Sprint(v))}, err
		}, &CachePolicy{Shared: true, Lifetime: &cache.Lifetime{Revalidate: time.Minute, Expire: time.Hour}, Tags: []string{"pages"}})
		r.Metadata = []MetadataLayer{{Load: func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
			v, err := read(c)
			return seo.Metadata{Title: fmt.Sprintf("Revision %d", v)}, err
		}}}
		return sharedPageApp(t, Config{SharedCache: s, Routes: []Route{r}})
	}
	a, b := makeApp(), makeApp()
	first, done := sharedPageCollected(t, a, "http://public.example/", true, nil)
	done.Finish(context.Background(), true)
	if e := sharedPageEnvelope(t, first); first.Code != 200 || e.HTML != "1" || e.Title != "Revision 1" || reads.Load() != 1 {
		t.Fatal(first.Code, e, reads.Load())
	}
	revision.Store(2)
	if err := a.MarkCacheStale(context.Background(), shared.Selector{Kind: "tag", Value: "pages"}, shared.Never); err != nil {
		t.Fatal(err)
	}
	stale, done := sharedPageCollected(t, a, "http://public.example/", true, nil)
	if e := sharedPageEnvelope(t, stale); stale.Code != 200 || e.HTML != "1" || e.Title != "Revision 1" || reads.Load() != 1 {
		t.Fatal("work ran before publication", stale.Code, e, reads.Load())
	}
	other, otherDone := sharedPageCollected(t, b, "http://public.example/", true, nil)
	otherDone.Finish(context.Background(), false)
	if e := sharedPageEnvelope(t, other); e.HTML != "1" || reads.Load() != 1 {
		t.Fatal("other replica duplicated pending refresh", e, reads.Load())
	}
	done.Finish(context.Background(), true)
	fresh := sharedPageRead(t, b, "http://public.example/", true, nil)
	if e := sharedPageEnvelope(t, fresh); fresh.Code != 200 || e.HTML != "2" || e.Title != "Revision 2" || reads.Load() != 2 {
		t.Fatal(fresh.Code, e, reads.Load())
	}
}

func TestSharedPageProfileReuseHonorsClippedNeverDeadline(t *testing.T) {
	_, s := sharedPageMemory(t)
	a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(static("profile snapshot"), &CachePolicy{Shared: true, Profile: "default", Tags: []string{"pages"}})}})
	first := sharedPageRead(t, a, "http://public.example/", true, nil)
	if first.Code != 200 || first.Header().Get(navigation.StaleHeader) == "" {
		t.Fatal(first.Code, first.Header())
	}
	if err := a.MarkCacheStale(context.Background(), shared.Selector{Kind: "tag", Value: "pages"}, time.Second); err != nil {
		t.Fatal(err)
	}
	stale, done := sharedPageCollected(t, a, "http://public.example/", true, nil)
	defer done.Finish(context.Background(), false)
	var millis int64
	if _, err := fmt.Sscan(stale.Header().Get(navigation.StaleHeader), &millis); err != nil || millis <= 0 || millis > 1000 {
		t.Fatal("clipped Never generation received a renewed browser window", stale.Header(), millis, err)
	}
}

func TestSharedPageOversizeBypassesOnceWithoutRetention(t *testing.T) {
	m, s := sharedPageMemory(t)
	var loads atomic.Int32
	// 800KiB of escaped HTML expands beyond 1MiB after JSON escapes ampersands.
	text := strings.Repeat("<", 200000)
	a := sharedPageApp(t, Config{SharedCache: s, Headers: http.Header{navigation.StaleHeader: {"9999"}, "X-Nextgo-Prefetch": {"public"}}, Routes: []Route{sharedPageRoute(func(*Context) (Page, error) {
		loads.Add(1)
		return Page{Title: "Large valid page", Body: ui.Text(text)}, nil
	}, &CachePolicy{Shared: true, TTL: time.Minute})}})
	for i := int32(1); i <= 2; i++ {
		w := sharedPageRead(t, a, "http://public.example/", true, nil)
		if w.Code != 200 || sharedPageEnvelope(t, w).HTML != strings.Repeat("&lt;", 200000) || loads.Load() != i || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get(navigation.StaleHeader) != "" || w.Header().Get("X-Nextgo-Prefetch") != "" {
			t.Fatal("oversized valid response failed, rerendered or advertised reuse", w.Code, w.Header(), loads.Load())
		}
		if keys, _ := m.Stats(); keys != 0 {
			t.Fatal("oversized snapshot retained", keys)
		}
	}
}

func TestSharedPageOversizedRefreshDiscardsLastGoodSnapshot(t *testing.T) {
	m, s := sharedPageMemory(t)
	var loads atomic.Int32
	var large atomic.Bool
	a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(func(*Context) (Page, error) {
		loads.Add(1)
		if large.Load() {
			return Page{Title: "Large", Body: ui.Text(strings.Repeat("<", 200000))}, nil
		}
		return Page{Title: "Small", Body: ui.Text("small")}, nil
	}, &CachePolicy{Shared: true, Lifetime: &cache.Lifetime{Revalidate: time.Minute, Expire: time.Hour}, Tags: []string{"pages"}})}})
	first := sharedPageRead(t, a, "http://public.example/", true, nil)
	if first.Code != 200 || sharedPageEnvelope(t, first).HTML != "small" {
		t.Fatal(first.Code)
	}
	large.Store(true)
	if err := a.MarkCacheStale(context.Background(), shared.Selector{Kind: "tag", Value: "pages"}, shared.Never); err != nil {
		t.Fatal(err)
	}
	stale, done := sharedPageCollected(t, a, "http://public.example/", true, nil)
	if e := sharedPageEnvelope(t, stale); e.HTML != "small" || loads.Load() != 1 {
		t.Fatal(e, loads.Load())
	}
	done.Finish(context.Background(), true)
	if keys, _ := m.Stats(); keys != 0 || loads.Load() != 2 {
		t.Fatal("oversized refresh retained old snapshot or rendered twice", keys, loads.Load())
	}
	next := sharedPageRead(t, a, "http://public.example/", true, nil)
	if next.Code != 200 || sharedPageEnvelope(t, next).Title != "Large" || loads.Load() != 3 || !strings.Contains(next.Header().Get("Cache-Control"), "no-store") {
		t.Fatal(next.Code, next.Header(), loads.Load())
	}
}

func TestSharedPageOptInAndLayoutIdentity(t *testing.T) {
	m, s := sharedPageMemory(t)
	makeApp := func(enabled bool, id, base string) *App {
		r := sharedPageRoute(static("layout content"), &CachePolicy{Shared: enabled, TTL: time.Minute})
		r.Pattern = "/dash/:id"
		r.Layouts = []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return ui.El("main", nil, n), nil }}
		r.LayoutIDs = []string{id}
		r.LayoutBases = []string{base}
		return sharedPageApp(t, Config{SharedCache: s, PreserveLayouts: true, Routes: []Route{r}})
	}
	local := makeApp(false, "root", "/dash")
	if w := sharedPageRead(t, local, "http://public.example/dash/one", true, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if keys, _ := m.Stats(); keys != 0 || local.config.Cache.Len() != 1 {
		t.Fatal("configured shared store changed legacy policy", keys, local.config.Cache.Len())
	}
	a, b, c := makeApp(true, "root", "/dash"), makeApp(true, "other-root", "/dash"), makeApp(true, "root", "/dash/:id")
	if a.configurationID == local.configurationID || a.configurationID == b.configurationID || a.configurationID == c.configurationID {
		t.Fatal("storage/layout ownership missing from configuration identity")
	}
	for _, app := range []*App{a, b, c} {
		w := sharedPageRead(t, app, "http://public.example/dash/one", true, nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if keys, _ := m.Stats(); keys != 3 {
		t.Fatal("layout identities reused another render", keys)
	}
}

func TestSharedPageCodeEpochAndContextRequirements(t *testing.T) {
	_, s := sharedPageMemory(t)
	cfg := Config{SharedCache: s, Routes: []Route{sharedPageRoute(static("page"), &CachePolicy{Shared: true, TTL: time.Minute})}}
	if a, err := New(cfg); err == nil {
		_ = a.Close(context.Background())
		t.Fatal("production shared pages accepted missing code epoch")
	}
	cfg.Dev = true
	a := testApp(t, cfg)
	t.Cleanup(func() {
		if err := a.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, ctx := range []context.Context{nil, context.Background()} {
		if err := ExpireCache(ctx, shared.Selector{Kind: "all"}); !errors.Is(err, ErrCacheScope) {
			t.Fatal(err)
		}
		if err := MarkCacheStale(ctx, shared.Selector{Kind: "all"}, shared.Never); !errors.Is(err, ErrCacheScope) {
			t.Fatal(err)
		}
	}
}

func TestSharedPageMutationHelpersUseRequestScopeAndRejectPublicLoaders(t *testing.T) {
	m, s := sharedPageMemory(t)
	local := cache.New(16)
	var loads atomic.Int32
	seedLocal := func() {
		_, err := cache.GetOrLoad(context.Background(), local, "independent-data", cache.Options{TTL: time.Minute, Tags: []string{"pages"}}, func(context.Context) (string, error) { return "local value", nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	seedLocal()
	a := sharedPageApp(t, Config{Cache: local, SharedCache: s, Routes: []Route{sharedPageRoute(func(c *Context) (Page, error) {
		loads.Add(1)
		if err := ExpireCache(c.Context, shared.Selector{Kind: "all"}); !errors.Is(err, ErrPublicCacheResponse) {
			t.Error("public snapshot mutated stores", err)
		}
		return Page{Body: ui.Text("public snapshot")}, nil
	}, &CachePolicy{Shared: true, TTL: time.Minute, Tags: []string{"pages"}})}, Handlers: map[string]http.Handler{
		"/expire": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := ExpireCache(r.Context(), shared.Selector{Kind: "tag", Value: "pages"}); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
		}),
		"/mark": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if err := MarkCacheStale(r.Context(), shared.Selector{Kind: "all"}, shared.Never); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			w.WriteHeader(204)
		}),
	}})
	if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if keys, _ := m.Stats(); keys != 1 || local.Len() != 1 {
		t.Fatal(keys, local.Len())
	}
	if w := sharedPageRead(t, a, "http://public.example/expire", false, nil); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if keys, _ := m.Stats(); keys != 0 || local.Len() != 0 {
		t.Fatal("scoped invalidation omitted a configured store", keys, local.Len())
	}
	seedLocal()
	if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := sharedPageRead(t, a, "http://public.example/mark", false, nil); w.Code != 204 {
		t.Fatal(w.Code)
	}
	_, info, err := cache.Lookup(context.Background(), local, "independent-data", cache.Options{TTL: time.Minute, Tags: []string{"pages"}, ScheduleRefresh: func(_ func(context.Context) error, finish func()) error { finish(); return nil }}, func(context.Context) (string, error) {
		t.Error("stale mark all dropped independent data without paths")
		return "wrong", nil
	})
	if err != nil || info.State != cache.Stale {
		t.Fatal("local all selector approximated path invalidation", info, err)
	}
	if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 || loads.Load() != 3 {
		t.Fatal("TTL stale mark did not block for replacement", w.Code, loads.Load())
	}
}

type sharedPageFailBackend struct {
	shared.Backend
	down atomic.Bool
}

func (b *sharedPageFailBackend) Begin(ctx context.Context, id shared.ID, p shared.Policy, d time.Duration) (shared.View, error) {
	if b.down.Load() {
		return shared.View{}, shared.ErrUnavailable
	}
	return b.Backend.Begin(ctx, id, p, d)
}
func (b *sharedPageFailBackend) BeginStale(ctx context.Context, id shared.ID, p shared.Policy, d time.Duration) (shared.View, error) {
	if b.down.Load() {
		return shared.View{}, shared.ErrUnavailable
	}
	return b.Backend.BeginStale(ctx, id, p, d)
}
func (b *sharedPageFailBackend) Expire(ctx context.Context, namespace string, selector shared.Selector) (int, error) {
	if b.down.Load() {
		return 0, shared.ErrUnavailable
	}
	return b.Backend.Expire(ctx, namespace, selector)
}
func (b *sharedPageFailBackend) MarkStale(ctx context.Context, namespace string, selector shared.Selector, window time.Duration) (int, error) {
	if b.down.Load() {
		return 0, shared.ErrUnavailable
	}
	return b.Backend.MarkStale(ctx, namespace, selector, window)
}

func TestSharedPageInvalidationValidatesBeforeMutationAndReportsPartialFailure(t *testing.T) {
	m, _ := sharedPageMemory(t)
	backend := &sharedPageFailBackend{Backend: m}
	s := sharedPageStore(t, backend, "page-tests", "snapshot-v1")
	local := cache.New(8)
	_, err := cache.GetOrLoad(context.Background(), local, "local", cache.Options{TTL: time.Minute}, func(context.Context) (string, error) { return "owned", nil })
	if err != nil {
		t.Fatal(err)
	}
	a := sharedPageApp(t, Config{Cache: local, SharedCache: s, Routes: []Route{sharedPageRoute(static("snapshot"), &CachePolicy{Shared: true, TTL: time.Minute})}})
	if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, selector := range []shared.Selector{{Kind: "wrong"}, {Kind: "tag", Value: ""}, {Kind: "all", Value: "unexpected"}} {
		if err := a.ExpireCache(context.Background(), selector); !errors.Is(err, shared.ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, window := range []time.Duration{-2, 366 * 24 * time.Hour} {
		if err := a.MarkCacheStale(context.Background(), shared.Selector{Kind: "all"}, window); !errors.Is(err, shared.ErrInvalid) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := a.ExpireCache(ctx, shared.Selector{Kind: "all"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if keys, _ := m.Stats(); keys != 1 || local.Len() != 1 {
		t.Fatal("invalid/canceled operation changed stores", keys, local.Len())
	}
	backend.down.Store(true)
	if err := a.ExpireCache(context.Background(), shared.Selector{Kind: "all"}); !errors.Is(err, shared.ErrUnavailable) {
		t.Fatal("remote failure was hidden", err)
	}
	if keys, _ := m.Stats(); keys != 1 || local.Len() != 0 {
		t.Fatal("local invalidation did not survive remote failure", keys, local.Len())
	}
}

func TestSharedPageBackendOutageHasNoL1OrRenderFallback(t *testing.T) {
	for _, lifetime := range []bool{false, true} {
		t.Run(fmt.Sprint(lifetime), func(t *testing.T) {
			m, _ := sharedPageMemory(t)
			backend := &sharedPageFailBackend{Backend: m}
			s := sharedPageStore(t, backend, "page-tests", "snapshot-v1")
			var loads atomic.Int32
			policy := &CachePolicy{Shared: true, TTL: time.Minute}
			if lifetime {
				policy.TTL = 0
				policy.Lifetime = &cache.Lifetime{Revalidate: time.Minute, Expire: time.Hour}
			}
			a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("prior public snapshot")}, nil }, policy)}})
			if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
				t.Fatal(w.Code)
			}
			backend.down.Store(true)
			failed := sharedPageRead(t, a, "http://public.example/", true, nil)
			if failed.Code != 500 || strings.Contains(failed.Body.String(), "prior public snapshot") || loads.Load() != 1 {
				t.Fatal("outage silently returned local/rendered fallback", failed.Code, loads.Load(), failed.Body.String())
			}
			backend.down.Store(false)
			if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 || !strings.Contains(sharedPageEnvelope(t, w).HTML, "prior public snapshot") || loads.Load() != 1 {
				t.Fatal("recovery lost stored snapshot", w.Code, loads.Load())
			}
		})
	}
}

type sharedPageCorruptBackend struct {
	shared.Backend
	mode    string
	enabled atomic.Bool
}

func (b *sharedPageCorruptBackend) Begin(ctx context.Context, id shared.ID, p shared.Policy, d time.Duration) (shared.View, error) {
	v, err := b.Backend.Begin(ctx, id, p, d)
	if err != nil || !v.Found || v.Lease != "" || !b.enabled.Load() {
		return v, err
	}
	var payload map[string]any
	if err := json.Unmarshal(v.Value, &payload); err != nil {
		return shared.View{}, err
	}
	payload["html"] = "INCOMPATIBLE-CACHED-BODY"
	if b.mode == "binding" {
		payload["binding"] = "wrong-binding"
	} else {
		payload["head"] = []map[string]any{{"tag": "script", "attrs": map[string]string{"type": "text/javascript"}, "text": "INCOMPATIBLE-EXECUTABLE-HEAD"}}
	}
	v.Value, err = json.Marshal(payload)
	return v, err
}
func TestSharedPageStoredBindingAndHeadFailClosedBeforeAdoption(t *testing.T) {
	for _, mode := range []string{"binding", "head"} {
		t.Run(mode, func(t *testing.T) {
			m, _ := sharedPageMemory(t)
			backend := &sharedPageCorruptBackend{Backend: m, mode: mode}
			s := sharedPageStore(t, backend, "page-tests", "snapshot-v1")
			var loads atomic.Int32
			a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("valid stored body")}, nil }, &CachePolicy{Shared: true, TTL: time.Minute})}})
			if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
				t.Fatal(w.Code)
			}
			backend.enabled.Store(true)
			for _, nav := range []bool{false, true} {
				w := sharedPageRead(t, a, "http://public.example/", nav, nil)
				if w.Code != 500 || strings.Contains(w.Body.String(), "INCOMPATIBLE-") || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("stored snapshot adopted before binding/head checks", mode, nav, w.Code, w.Header(), w.Body.String())
				}
			}
			if loads.Load() != 1 {
				t.Fatal("corrupt storage silently rerendered instead of failing closed", loads.Load())
			}
		})
	}
}

func TestSharedPageRefreshFailurePolicyAndHardWindow(t *testing.T) {
	for _, status := range []int{500, 404, 401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			m, s := sharedPageMemory(t)
			var broken atomic.Bool
			a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(func(*Context) (Page, error) {
				if broken.Load() {
					switch status {
					case 404:
						return Page{}, ErrNotFound
					case 401:
						return Page{}, ErrUnauthorized
					case 403:
						return Page{}, ErrForbidden
					}
					return Page{}, errors.New("private transient detail")
				}
				return Page{Title: "Last good title", Body: ui.Text("last good")}, nil
			}, &CachePolicy{Shared: true, Lifetime: &cache.Lifetime{Revalidate: time.Minute, Expire: time.Hour}, Tags: []string{"pages"}})}})
			if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
				t.Fatal(w.Code)
			}
			broken.Store(true)
			if err := a.MarkCacheStale(context.Background(), shared.Selector{Kind: "tag", Value: "pages"}, shared.Never); err != nil {
				t.Fatal(err)
			}
			old, done := sharedPageCollected(t, a, "http://public.example/", true, nil)
			if e := sharedPageEnvelope(t, old); e.HTML != "last good" || e.Title != "Last good title" {
				t.Fatal(e)
			}
			done.Finish(context.Background(), true)
			want := 0
			if status == 500 {
				want = 1
			}
			if keys, _ := m.Stats(); keys != want {
				t.Fatal("transient/permanent retention policy", status, keys, want)
			}
			if err := a.MarkCacheStale(context.Background(), shared.Selector{Kind: "tag", Value: "pages"}, time.Millisecond); err != nil {
				t.Fatal(err)
			}
			time.Sleep(5 * time.Millisecond)
			failed := sharedPageRead(t, a, "http://public.example/", true, nil)
			e := sharedPageEnvelope(t, failed)
			if failed.Code != status || e.HTML == "last good" || e.Title == "Last good title" || strings.Contains(failed.Body.String(), "private transient detail") {
				t.Fatal("hard/permanent failure served old/private output", status, failed.Code, e)
			}
		})
	}
}

func TestSharedPageUnrepresentableRequestInputsBypassSafely(t *testing.T) {
	for _, kind := range []string{"long-path", "invalid-query"} {
		t.Run(kind, func(t *testing.T) {
			m, s := sharedPageMemory(t)
			var loads atomic.Int32
			r := sharedPageRoute(func(c *Context) (Page, error) {
				loads.Add(1)
				if err := c.SetHeader("X-Uncached-Input", "ok"); err != nil {
					return Page{}, err
				}
				return Page{Body: ui.Text("valid uncached result")}, nil
			}, &CachePolicy{Shared: true, TTL: time.Minute})
			target := "http://public.example/"
			if kind == "long-path" {
				r.Pattern = "/:part"
				target += strings.Repeat("a", 600)
			}
			a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{r}})
			for i := 0; i < 2; i++ {
				w := sharedPageRead(t, a, target, true, func(r *http.Request) {
					if kind == "invalid-query" {
						r.URL.RawQuery = "q=\xff"
					}
				})
				if w.Code != 200 || w.Header().Get("X-Uncached-Input") != "ok" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("backend key/label admission broke valid uncached rendering", kind, w.Code, w.Header(), w.Body.String())
				}
			}
			if keys, _ := m.Stats(); keys != 0 || loads.Load() != 2 {
				t.Fatal("unrepresentable key retained/collided", kind, keys, loads.Load())
			}
		})
	}
}

func TestSharedPageMetadataMatchesCredentialBypass(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, legacy := range []bool{false, true} {
			t.Run(fmt.Sprintf("shared=%v/legacy=%v", enabled, legacy), func(t *testing.T) {
				_, s := sharedPageMemory(t)
				var loads, metadataReads atomic.Int32
				r := sharedPageRoute(func(*Context) (Page, error) {
					loads.Add(1)
					p := Page{Body: ui.El("p", nil, ui.Text("deterministic <body>")), Metadata: seo.Metadata{Title: "Child metadata title", Direction: "rtl", Canonical: "child"}, Meta: map[string]string{"Description": "Description from legacy metadata map", "ROBOTS": "index", "X-Info": "public"}}
					if legacy {
						p.Title = "Explicit legacy title"
						p.Description = "Explicit legacy description"
					}
					return p, nil
				}, &CachePolicy{Shared: enabled, TTL: time.Minute})
				r.Metadata = []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
					metadataReads.Add(1)
					return seo.Metadata{Title: "Ancestor", TitleTemplate: "%s | Shared site", Description: "Inherited description", Language: "fr", BaseURL: "https://public.example/base/", Viewport: &seo.Viewport{InitialScale: 1.5, ThemeColors: []seo.ThemeColor{{Color: "#123456"}}}, OpenGraph: seo.OpenGraph{SiteName: "Shared site"}}, nil
				}}}
				a := sharedPageApp(t, Config{SharedCache: s, Routes: []Route{r}})
				for i := 0; i < 2; i++ {
					if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
						t.Fatal(w.Code, w.Body.String())
					}
				}
				cached := sharedPageEnvelope(t, sharedPageRead(t, a, "http://public.example/", true, nil))
				fresh := sharedPageEnvelope(t, sharedPageRead(t, a, "http://public.example/", true, func(r *http.Request) { r.Header.Set("Cookie", "private=bypass") }))
				if cached.Title != fresh.Title || cached.Description != fresh.Description || cached.Document != fresh.Document || !reflect.DeepEqual(cached.Meta, fresh.Meta) || !reflect.DeepEqual(cached.Head, fresh.Head) || cached.HTML != fresh.HTML {
					t.Fatalf("frozen and bypass metadata diverged: cached=%+v fresh=%+v", cached, fresh)
				}
				if loads.Load() != 2 || metadataReads.Load() != 2 {
					t.Fatal("comparison did not exercise a real cache hit and uncached render", loads.Load(), metadataReads.Load())
				}
				wantTitle, wantDescription := "Child metadata title | Shared site", "Description from legacy metadata map"
				if legacy {
					wantTitle, wantDescription = "Explicit legacy title", "Explicit legacy description"
				}
				if cached.Title != wantTitle || cached.Description != wantDescription || cached.Document.Language != "fr" || cached.Document.Direction != "rtl" {
					t.Fatal(cached, wantTitle, wantDescription)
				}
			})
		}
	}
}

func TestSharedPageInvalidationFencesAnAlreadyRunningSnapshot(t *testing.T) {
	_, s := sharedPageMemory(t)
	var revision, loads atomic.Int32
	revision.Store(1)
	entered, release := make(chan struct{}), make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	defer releaseOnce()
	page := func(c *Context) (Page, error) {
		call := loads.Add(1)
		v := revision.Load()
		if call == 2 {
			close(entered)
			select {
			case <-release:
			case <-c.Context.Done():
				return Page{}, c.Context.Err()
			}
		}
		return Page{Title: fmt.Sprintf("Revision %d", v), Body: ui.Text(fmt.Sprint(v))}, nil
	}
	makeApp := func() *App {
		return sharedPageApp(t, Config{SharedCache: s, Routes: []Route{sharedPageRoute(page, &CachePolicy{Shared: true, Lifetime: &cache.Lifetime{Revalidate: time.Minute, Expire: time.Hour}, Tags: []string{"pages"}})}})
	}
	a, b := makeApp(), makeApp()
	if w := sharedPageRead(t, a, "http://public.example/", true, nil); w.Code != 200 {
		t.Fatal(w.Code)
	}
	revision.Store(2)
	if err := a.MarkCacheStale(context.Background(), shared.Selector{Kind: "tag", Value: "pages"}, shared.Never); err != nil {
		t.Fatal(err)
	}
	stale, done := sharedPageCollected(t, a, "http://public.example/", true, nil)
	if sharedPageEnvelope(t, stale).HTML != "1" {
		t.Fatal(stale.Body.String())
	}
	finished := make(chan struct{})
	go func() { done.Finish(context.Background(), true); close(finished) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("refresh never started")
	}
	revision.Store(3)
	if err := b.ExpireCache(context.Background(), shared.Selector{Kind: "tag", Value: "pages"}); err != nil {
		t.Fatal(err)
	}
	newer := sharedPageRead(t, b, "http://public.example/", true, nil)
	if e := sharedPageEnvelope(t, newer); newer.Code != 200 || e.HTML != "3" || e.Title != "Revision 3" {
		t.Fatal(newer.Code, e)
	}
	releaseOnce()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("retired refresh did not finish")
	}
	current := sharedPageRead(t, a, "http://public.example/", true, nil)
	if e := sharedPageEnvelope(t, current); current.Code != 200 || e.HTML != "3" || e.Title != "Revision 3" || loads.Load() != 3 {
		t.Fatal("old encoded snapshot republished after expiration", current.Code, e, loads.Load())
	}
}

func TestSharedPageHEADFillUsesGETRepresentation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", enabled), func(t *testing.T) {
			_, s := sharedPageMemory(t)
			var loads, metadataLoads atomic.Int32
			makeApp := func() *App {
				r := sharedPageRoute(func(c *Context) (Page, error) {
					loads.Add(1)
					if c.Request.Method == http.MethodHead {
						return Page{Title: "HEAD shortcut", Body: ui.Fragment()}, nil
					}
					if c.Request.Method != http.MethodGet {
						t.Errorf("unexpected cached loader method %s", c.Request.Method)
					}
					return Page{Title: "GET representation", Body: ui.El("p", nil, ui.Text("Useful GET body"))}, nil
				}, &CachePolicy{Shared: enabled, TTL: time.Minute})
				r.Metadata = []MetadataLayer{{Load: func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
					metadataLoads.Add(1)
					return seo.Metadata{Description: "Metadata for " + c.Request.Method}, nil
				}}}
				return sharedPageApp(t, Config{SharedCache: s, Routes: []Route{r}})
			}
			a := makeApp()
			head := httptest.NewRequest(http.MethodHead, "http://public.example/", nil)
			response := httptest.NewRecorder()
			a.ServeHTTP(response, head)
			if response.Code != 200 || response.Body.Len() != 0 || head.Method != http.MethodHead {
				t.Fatal("HEAD transport body/original request changed", response.Code, response.Body.Len(), head.Method)
			}
			getApp := a
			if enabled {
				getApp = makeApp()
			}
			get := sharedPageRead(t, getApp, "http://public.example/", true, nil)
			page := sharedPageEnvelope(t, get)
			if get.Code != 200 || page.HTML != "<p>Useful GET body</p>" || page.Title != "GET representation" || page.Description != "Metadata for GET" || loads.Load() != 1 || metadataLoads.Load() != 1 {
				t.Fatal("HEAD seeded an incomplete cached representation", get.Code, page, loads.Load(), metadataLoads.Load())
			}
			last := httptest.NewRecorder()
			getApp.ServeHTTP(last, httptest.NewRequest(http.MethodHead, "http://public.example/", nil))
			if last.Code != 200 || last.Body.Len() != 0 || loads.Load() != 1 || metadataLoads.Load() != 1 {
				t.Fatal("cached HEAD wrote body or reloaded", last.Code, last.Body.Len(), loads.Load(), metadataLoads.Load())
			}
		})
	}
}

func TestSharedPageInvalidStartupPolicies(t *testing.T) {
	_, s := sharedPageMemory(t)
	for _, tc := range []struct {
		name     string
		store    *shared.Store
		route    Route
		profiles map[string]cache.Profile
	}{
		{"missing-store", nil, sharedPageRoute(static("x"), &CachePolicy{Shared: true, TTL: time.Minute}), nil},
		{"disabled", s, sharedPageRoute(static("x"), &CachePolicy{Shared: true}), nil},
		{"too-many-tags", s, sharedPageRoute(static("x"), &CachePolicy{Shared: true, TTL: time.Minute, Tags: make([]string, 65)}), nil},
		{"bad-tag", s, sharedPageRoute(static("x"), &CachePolicy{Shared: true, TTL: time.Minute, Tags: []string{"bad\x00tag"}}), nil},
		{"long-lifetime", s, sharedPageRoute(static("x"), &CachePolicy{Shared: true, Lifetime: &cache.Lifetime{Revalidate: time.Minute, Expire: 366 * 24 * time.Hour}}), nil},
		{"resolved-profile", s, sharedPageRoute(static("x"), &CachePolicy{Shared: true, Profile: "long"}), map[string]cache.Profile{"long": {Revalidate: time.Minute, Expire: 366 * 24 * time.Hour}}},
		{"api", s, Route{Pattern: "/", API: func(http.ResponseWriter, *http.Request) {}, Cache: &CachePolicy{Shared: true, TTL: time.Minute}}, nil},
		{"prerender", s, Route{Pattern: "/", Page: static("x"), Cache: &CachePolicy{Shared: true, TTL: time.Minute}, Prerender: &PrerenderPolicy{HostIndependent: true}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := New(Config{BuildID: "shared-policy-test", SharedCache: tc.store, CacheProfiles: tc.profiles, Routes: []Route{tc.route}})
			if err == nil {
				_ = a.Close(context.Background())
				t.Fatal("invalid shared policy accepted")
			}
		})
	}
}
