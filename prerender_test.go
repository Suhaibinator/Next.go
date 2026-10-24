package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func buildPrerenderTest(t *testing.T, c Config) (Config, []byte) {
	t.Helper()
	c.BuildID = "source-code"
	c.PrerenderBuildMode = true
	app := testApp(t, c)
	data, err := app.BuildPrerender(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.PrerenderBuildMode = false
	c.BuildID = PrerenderBuildID(c.BuildID, data)
	c.PrerenderFS = fstest.MapFS{"prerender.json": &fstest.MapFile{Data: data}}
	return c, data
}
func prerenderRead(app *App, target string, headers http.Header) (*httptest.ResponseRecorder, *postresponse.Collector) {
	ctx, collector := postresponse.Collect(context.Background())
	r := httptest.NewRequest("GET", target, nil).WithContext(ctx)
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	r.Header.Set("X-Nextgo-Navigate", "1")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w, collector
}
func finishPrerender(t *testing.T, w *httptest.ResponseRecorder, c *postresponse.Collector) Envelope {
	t.Helper()
	defer c.Finish(context.Background(), true)
	var e Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return e
}
func TestPrerenderBuildSeedsBodyHeadAndMetadata(t *testing.T) {
	var revision, loads atomic.Int64
	revision.Store(1)
	routes := []Route{{Pattern: "/posts/:slug", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Tags: []string{"posts"}}, StaticParams: func(context.Context) ([]Params, error) { return []Params{{"slug": "go"}}, nil }, Page: func(c *Context) (Page, error) {
		loads.Add(1)
		if !errors.Is(After(c, func(context.Context) error { return nil }), ErrAfterCache) {
			t.Error("build allowed After")
		}
		return Page{Title: fmt.Sprintf("Version %d", revision.Load()), Metadata: seo.Metadata{Language: "fr"}, Body: ui.El("p", nil, ui.Text(fmt.Sprintf("%s:%d", c.Params["slug"], revision.Load())))}, nil
	}, Layouts: []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return ui.El("main", nil, n), nil }}}}
	c, data := buildPrerenderTest(t, Config{Routes: routes})
	revision.Store(2)
	app := testApp(t, c)
	w, done := prerenderRead(app, "http://different-host/posts/go", nil)
	e := finishPrerender(t, w, done)
	if w.Code != 200 || e.Title != "Version 1" || e.HTML != "<main><p>go:1</p></main>" || e.Document.Language != "fr" || w.Header().Get("X-Nextgo-Prerender") != "build" || loads.Load() != 1 {
		t.Fatal(w.Code, e, w.Header(), loads.Load())
	}
	if strings.Contains(string(data), "<html") {
		t.Fatal("snapshot stores document shell")
	}
	app.RevalidateTag("posts")
	w, done = prerenderRead(app, "/posts/go", nil)
	e = finishPrerender(t, w, done)
	if !strings.Contains(e.HTML, "go:2") || w.Header().Get("X-Nextgo-Prerender") != "runtime" || loads.Load() != 2 {
		t.Fatal(e, w.Header(), loads.Load())
	}
}
func TestPrerenderBuildAgeRefreshAndHardExpiry(t *testing.T) {
	var revision atomic.Int64
	revision.Store(1)
	route := Route{Pattern: "/", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Second, Expire: time.Minute}, Page: func(*Context) (Page, error) {
		return Page{Title: fmt.Sprint(revision.Load()), Body: ui.Text(fmt.Sprint(revision.Load()))}, nil
	}}
	c, data := buildPrerenderTest(t, Config{Routes: []Route{route}})
	var manifest prerenderManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	clock := &atomic.Int64{}
	clock.Store(manifest.Pages[0].GeneratedAt.Add(2 * time.Second).UnixNano())
	c.Cache = cache.New(8, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	app := testApp(t, c)
	revision.Store(2)
	w, done := prerenderRead(app, "/", nil)
	var stale Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &stale); err != nil {
		t.Fatal(err)
	}
	if stale.HTML != "1" || w.Header().Get("X-Nextgo-Cache") != "stale" {
		t.Fatal(stale, w.Header())
	}
	done.Finish(context.Background(), true)
	w, done = prerenderRead(app, "/", nil)
	e := finishPrerender(t, w, done)
	if e.HTML != "2" || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(e, w.Header())
	}
	clock.Add(int64(2 * time.Minute))
	revision.Store(3)
	w, done = prerenderRead(app, "/", nil)
	e = finishPrerender(t, w, done)
	if e.HTML != "3" {
		t.Fatal(e)
	}
}
func TestPrerenderPrivateAndQueryRequestsBypassSeeds(t *testing.T) {
	var calls atomic.Int64
	c, _ := buildPrerenderTest(t, Config{Routes: []Route{{Pattern: "/", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}, Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text(fmt.Sprintf("%d:%s", calls.Add(1), c.Query.Get("q")))}, nil
	}}}})
	app := testApp(t, c)
	for _, tc := range []struct {
		target  string
		headers http.Header
	}{{"/?q=private", nil}, {"/?", nil}, {"/", http.Header{"Cookie": {"session=private"}}}, {"/", http.Header{"Authorization": {"Bearer secret"}}}} {
		w, done := prerenderRead(app, tc.target, tc.headers)
		_ = finishPrerender(t, w, done)
		if w.Header().Get("X-Nextgo-Prerender") != "" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal(w.Header())
		}
	}
	w, done := prerenderRead(app, "/", nil)
	e := finishPrerender(t, w, done)
	if e.HTML != "1:" {
		t.Fatal("private request poisoned public snapshot", e)
	}
}
func TestPrerenderFallbackAndKnownPathsSurviveEviction(t *testing.T) {
	var loads atomic.Int64
	c, _ := buildPrerenderTest(t, Config{Cache: cache.New(1), Routes: []Route{{Pattern: "/items/:id", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Fallback: FallbackNotFound}, StaticParams: func(context.Context) ([]Params, error) { return []Params{{"id": "a"}, {"id": "b"}}, nil }, Page: func(c *Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text(c.Params["id"])}, nil }, NotFound: func(*Context) (Page, error) { return Page{Body: ui.Text("nearest missing")}, nil }}}})
	// Build capture never uses the store; capacity admission may discard one seed.
	app := testApp(t, c)
	w, done := prerenderRead(app, "/items/a", nil)
	e := finishPrerender(t, w, done)
	if w.Code != 200 || e.HTML != "a" {
		t.Fatal(e)
	}
	w, done = prerenderRead(app, "/items/unknown", http.Header{"Cookie": {"private=yes"}})
	e = finishPrerender(t, w, done)
	if w.Code != 404 || e.HTML != "nearest missing" {
		t.Fatal(w.Code, e)
	}
	app.RevalidatePath("/items")
	w, done = prerenderRead(app, "/items/b", nil)
	e = finishPrerender(t, w, done)
	if w.Code != 200 || e.HTML != "b" || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(e, w.Header())
	}
}
func TestPrerenderRejectsUnsafeBuildOutput(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"response cookie": func(c *Config) {
			c.Routes[0].Page = func(c *Context) (Page, error) {
				_ = c.SetCookie(&http.Cookie{Name: "secret", Value: "x"})
				return Page{Body: ui.Text("private")}, nil
			}
		},
		"stream": func(c *Config) {
			c.Routes[0].Page = func(*Context) (Page, error) { return Page{Stream: true, Body: ui.Text("stream")}, nil }
		},
		"loader error": func(c *Config) {
			c.Routes[0].Page = func(*Context) (Page, error) { return Page{}, errors.New("failed") }
		},
		"middleware cookie": func(c *Config) {
			c.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.SetCookie(w, &http.Cookie{Name: "private", Value: "x"})
					next.ServeHTTP(w, r)
				})
			}}
		},
		"late private middleware": func(c *Config) {
			c.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r)
					w.Header().Set("Cache-Control", "private, no-store")
				})
			}}
		},
		"nonce": func(c *Config) {
			c.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx, _ := scripts.WithNonce(r.Context(), "abcdefghijklmnopqrstuvwx")
					next.ServeHTTP(w, r.WithContext(ctx))
				})
			}}
		},
		"rewrite": func(c *Config) {
			c.Rewrites = []RewriteRule{{Source: "/", Destination: "/other"}}
			c.Routes = append(c.Routes, Route{Pattern: "/other", Page: func(*Context) (Page, error) { return Page{Body: ui.Text("other")}, nil }})
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := Config{BuildID: "code", PrerenderBuildMode: true, Routes: []Route{{Pattern: "/", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}, Page: func(*Context) (Page, error) { return Page{Body: ui.Text("public")}, nil }}}}
			mutate(&c)
			app := testApp(t, c)
			if _, err := app.BuildPrerender(context.Background()); err == nil {
				t.Fatal("unsafe build accepted")
			}
		})
	}
}
func TestPrerenderArtifactBindingAndRuntimeConfigChange(t *testing.T) {
	c, data := buildPrerenderTest(t, Config{Routes: []Route{{Pattern: "/", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}, Page: func(c *Context) (Page, error) {
		return Page{Body: ui.El("a", ui.Attrs{"href": MustURL(c, "/")}, ui.Text("home"))}, nil
	}}}})
	bad := c
	bad.PrerenderFS = fstest.MapFS{"prerender.json": &fstest.MapFile{Data: append(data, ' ')}}
	if _, err := New(bad); err == nil {
		t.Fatal("tampered private artifact accepted")
	}
	c.BasePath = "/mounted"
	app := testApp(t, c)
	w, done := prerenderRead(app, "/mounted", nil)
	e := finishPrerender(t, w, done)
	if w.Code != 200 || strings.Contains(e.HTML, "href=\"/\"") || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(w.Code, e, w.Header())
	}
}
func TestPrerenderPoliciesValidatedAndDevRendersOnRequest(t *testing.T) {
	for _, p := range []PrerenderPolicy{{}, {HostIndependent: true, Revalidate: -time.Second}, {HostIndependent: true, Revalidate: time.Hour, Expire: time.Minute}, {HostIndependent: true, Fallback: "bad"}} {
		if _, err := New(Config{Routes: []Route{{Pattern: "/", Prerender: &p, Page: func(*Context) (Page, error) { return Page{}, nil }}}}); err == nil {
			t.Fatal("invalid policy accepted", p)
		}
	}
	var calls atomic.Int64
	app := testApp(t, Config{Dev: true, Routes: []Route{{Pattern: "/", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}, Page: func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(calls.Add(1)))}, nil }}}})
	for i := 0; i < 2; i++ {
		w, done := prerenderRead(app, "/", nil)
		e := finishPrerender(t, w, done)
		if e.HTML != fmt.Sprint(i+1) {
			t.Fatal(e)
		}
	}
}
func TestPrerenderLocaleSnapshotsAndSharedStore(t *testing.T) {
	c, _ := buildPrerenderTest(t, Config{Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}, Routes: []Route{{Pattern: "/", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}, Page: func(c *Context) (Page, error) { return Page{Body: ui.Text(CurrentLocale(c))}, nil }}}})
	app := testApp(t, c)
	for _, code := range []string{"en", "fr"} {
		w, done := prerenderRead(app, "/"+code, nil)
		e := finishPrerender(t, w, done)
		if w.Code != 200 || e.HTML != code || e.Document.Language != code || w.Header().Get("X-Nextgo-Prerender") != "build" {
			t.Fatal(w.Code, e, w.Header())
		}
	}
}

func TestPrerenderStaleFailureKeepsLastGoodAndUsedCacheSkipsImport(t *testing.T) {
	var broken atomic.Bool
	c, data := buildPrerenderTest(t, Config{Routes: []Route{{Pattern: "/", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Second}, Page: func(*Context) (Page, error) {
		if broken.Load() {
			return Page{}, errors.New("temporary failure")
		}
		return Page{Body: ui.Text("last good")}, nil
	}}}})
	var manifest prerenderManifest
	_ = json.Unmarshal(data, &manifest)
	c.Cache = cache.New(8, cache.WithClock(func() time.Time { return manifest.Pages[0].GeneratedAt.Add(time.Minute) }))
	app := testApp(t, c)
	broken.Store(true)
	for i := 0; i < 2; i++ {
		w, done := prerenderRead(app, "/", nil)
		e := finishPrerender(t, w, done)
		if w.Code != 200 || e.HTML != "last good" || w.Header().Get("X-Nextgo-Prerender") != "build" {
			t.Fatal(w.Code, e, w.Header())
		}
	}
	// An invalidation before application construction wins over embedded seeds.
	broken.Store(false)
	c.Cache = cache.New(8)
	c.Cache.ExpireTag("even-a-missing-tag")
	used := testApp(t, c)
	w, done := prerenderRead(used, "/", nil)
	_ = finishPrerender(t, w, done)
	if w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(w.Header())
	}
}

func TestPrerenderCanonicalEncodedPathsAndDecodedInvalidation(t *testing.T) {
	var calls atomic.Int64
	c, _ := buildPrerenderTest(t, Config{Routes: []Route{{Pattern: "/items/:slug", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Fallback: FallbackNotFound}, StaticParams: func(context.Context) ([]Params, error) { return []Params{{"slug": "café"}}, nil }, Page: func(c *Context) (Page, error) { calls.Add(1); return Page{Body: ui.Text(c.Params["slug"])}, nil }}}})
	app := testApp(t, c)
	for _, path := range []string{"/items/caf%C3%A9", "/items/caf%c3%a9"} {
		w, done := prerenderRead(app, path, nil)
		e := finishPrerender(t, w, done)
		if w.Code != 200 || e.HTML != "café" || w.Header().Get("X-Nextgo-Prerender") != "build" {
			t.Fatal(path, w.Code, e, w.Header())
		}
	}
	app.RevalidatePath("/items/café")
	w, done := prerenderRead(app, "/items/caf%c3%a9", nil)
	e := finishPrerender(t, w, done)
	if w.Code != 200 || e.HTML != "café" || calls.Load() != 2 || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(e, w.Header(), calls.Load())
	}
}

func TestPrerenderComposedParametersAndDeclarationOwnership(t *testing.T) {
	route := Route{Pattern: "/:category/:product", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Fallback: FallbackNotFound}, Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text(c.Params["category"] + "/" + c.Params["product"])}, nil
	}, StaticParamsLayers: []StaticParamsLayer{{ID: "parent-v1", Pattern: "/:category", Load: func(context.Context) ([]Params, error) { return []Params{{"category": "coffee"}}, nil }}}, StaticParams: func(ctx context.Context) ([]Params, error) {
		if StaticParentParams(ctx)["category"] != "coffee" {
			return nil, errors.New("missing parent")
		}
		return []Params{{"product": "espresso"}}, nil
	}}
	c, _ := buildPrerenderTest(t, Config{Routes: []Route{route}})
	app := testApp(t, c)
	w, done := prerenderRead(app, "/coffee/espresso", nil)
	e := finishPrerender(t, w, done)
	if w.Code != 200 || e.HTML != "coffee/espresso" || w.Header().Get("X-Nextgo-Prerender") != "build" {
		t.Fatal(w.Code, e, w.Header())
	}
	c.Routes[0].StaticParamsLayers = []StaticParamsLayer{route.StaticParamsLayers[0]}
	c.Routes[0].StaticParamsLayers[0].ID = "parent-v2"
	changed := testApp(t, c)
	w, done = prerenderRead(changed, "/coffee/espresso", nil)
	_ = finishPrerender(t, w, done)
	if w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal("changed declaration reused old build ownership", w.Header())
	}
}

func TestPrerenderComposedEnumerationBudgetsSpanLocaleScopes(t *testing.T) {
	route := Route{Pattern: "/:category/:product", Page: func(*Context) (Page, error) { t.Fatal("empty child enumeration rendered a page"); return Page{}, nil }, Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}, StaticParamsLayers: []StaticParamsLayer{{ID: "parent", Pattern: "/:category", Load: func(context.Context) ([]Params, error) {
		rows := make([]Params, 6000)
		for i := range rows {
			rows[i] = Params{"category": fmt.Sprint(i)}
		}
		return rows, nil
	}}}, StaticParams: func(context.Context) ([]Params, error) { return nil, nil }}
	c := Config{BuildID: "source", PrerenderBuildMode: true, Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}, Routes: []Route{route}}
	app := testApp(t, c)
	if _, err := app.BuildPrerender(context.Background()); err == nil || !strings.Contains(err.Error(), "bounds") {
		t.Fatal("build generator budget multiplied per locale", err)
	}
	c.PrerenderBuildMode = false
	c.Dev = true
	if app, err := New(c); err == nil {
		_ = app.Close(context.Background())
		t.Fatal("runtime generator budget multiplied per locale")
	}
}

func TestPrerenderBuildChecksDefaultLocaleAliasMembership(t *testing.T) {
	var rendered atomic.Int64
	app := testApp(t, Config{BuildID: "alias-budget", PrerenderBuildMode: true, Locales: &LocaleConfig{Codes: []string{"en"}, Default: "en"}, Routes: []Route{{Pattern: "/:id", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Fallback: FallbackNotFound}, StaticParams: func(context.Context) ([]Params, error) {
		rows := make([]Params, 5001)
		for i := range rows {
			rows[i] = Params{"id": fmt.Sprint(i)}
		}
		return rows, nil
	}, Page: func(*Context) (Page, error) { rendered.Add(1); return Page{Body: ui.Text("public")}, nil }}}})
	if _, err := app.BuildPrerender(context.Background()); err == nil || !strings.Contains(err.Error(), "10000") {
		t.Fatal("build published an artifact exceeding runtime alias membership limits", err)
	}
	if rendered.Load() != 5001 {
		t.Fatal("test did not reach post-capture alias validation", rendered.Load())
	}
}
