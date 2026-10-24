package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func runtimeEnvelope(t *testing.T, a *App, path string, headers http.Header) (*httptest.ResponseRecorder, Envelope) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	r.Header.Set("X-Nextgo-Navigate", "1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	var result Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("invalid envelope: %d %s (%v)", w.Code, w.Body.String(), err)
	}
	return w, result
}

func TestRuntimeCatchallRouting(t *testing.T) {
	page := func(c *Context) (Page, error) {
		return Page{Body: ui.Text(c.Params["rest"] + "|" + c.Query.Get("q"))}, nil
	}
	a := testApp(t, Config{Routes: []Route{
		{Pattern: "/docs/*rest?", Page: page},
		{Pattern: "/files/*rest", Page: page},
		{Pattern: "/docs/:id", Page: static("parameter")},
		{Pattern: "/docs/start", Page: static("static")},
		{Pattern: "/api/files/*rest", API: func(w http.ResponseWriter, r *http.Request) { _, _ = fmt.Fprint(w, r.PathValue("rest")) }},
	}})
	for _, tc := range []struct {
		path, want string
		status     int
	}{
		{"/docs", "|", 200}, {"/docs/start", "static", 200}, {"/docs/other", "parameter", 200},
		{"/docs/one/two%20words?q=yes", "one/two words|yes", 200}, {"/files", "<h1>Page not found</h1>", 404},
		{"/files/a/b", "a/b|", 200}, {"/files/a%2Fb", "<h1>Page not found</h1>", 404},
	} {
		w, envelope := runtimeEnvelope(t, a, tc.path, nil)
		if w.Code != tc.status || envelope.Status != tc.status || envelope.HTML != tc.want {
			t.Errorf("%s: %d %+v, want %d %q", tc.path, w.Code, envelope, tc.status, tc.want)
		}
	}
	if w := request(a, "/api/files/a/b%20c", false); w.Code != 200 || w.Body.String() != "a/b c" {
		t.Fatalf("API catchall: %d %s", w.Code, w.Body.String())
	}
	for _, patterns := range [][2]string{{"/docs", "/docs/*rest?"}, {"/docs/*a", "/docs/*b?"}} {
		if _, err := New(Config{Routes: []Route{{Pattern: patterns[0], Page: static("a")}, {Pattern: patterns[1], Page: static("b")}}}); err == nil {
			t.Errorf("accepted ambiguous catchalls %v", patterns)
		}
	}
}

func TestRuntimeRouteBoundaries(t *testing.T) {
	wrap := func(_ *Context, n ui.Node) (ui.Node, error) {
		return ui.El("section", ui.Attrs{"data-layout": "root"}, n), nil
	}
	localError := func(_ *Context, err error) (Page, error) {
		if err == nil {
			t.Error("boundary missing cause")
		}
		return Page{Body: ui.Text("local failure")}, nil
	}
	a := testApp(t, Config{NotFound: static("global missing"), Routes: []Route{
		{Pattern: "/missing", Page: func(*Context) (Page, error) { return Page{}, ErrNotFound }, NotFound: static("local missing"), Layouts: []LayoutFunc{wrap}},
		{Pattern: "/error", Page: func(*Context) (Page, error) { return Page{}, errors.New("private detail") }, Error: localError, Layouts: []LayoutFunc{wrap}},
		{Pattern: "/panic", Page: func(*Context) (Page, error) { panic("private panic") }, Error: localError, Layouts: []LayoutFunc{wrap}},
		{Pattern: "/broken-boundary", Page: func(*Context) (Page, error) { return Page{}, errors.New("private") }, Error: func(*Context, error) (Page, error) { panic("private boundary panic") }},
	}})
	for _, tc := range []struct {
		path, want string
		status     int
	}{
		{"/missing", `<section data-layout="root">local missing</section>`, 404},
		{"/error", `<section data-layout="root">local failure</section>`, 500},
		{"/panic", `<section data-layout="root">local failure</section>`, 500},
		{"/unknown", "global missing", 404}, {"/broken-boundary", "<h1>Something went wrong</h1>", 500},
	} {
		w, envelope := runtimeEnvelope(t, a, tc.path, nil)
		if w.Code != tc.status || envelope.HTML != tc.want || strings.Contains(w.Body.String(), "private") {
			t.Errorf("%s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestRuntimeLoadingEndpoint(t *testing.T) {
	a := testApp(t, Config{Routes: []Route{
		{Pattern: "/posts/:slug", Page: static("post"), Loading: func(c *Context) (ui.Node, error) {
			return ui.El("p", nil, ui.Text(c.Params["slug"]+":"+c.Query.Get("q"))), nil
		}},
		{Pattern: "/fail", Page: static("ok"), Loading: func(*Context) (ui.Node, error) { return nil, errors.New("private loader") }},
		{Pattern: "/panic", Page: static("ok"), Loading: func(*Context) (ui.Node, error) { panic("private panic") }},
		{Pattern: "/bad-html", Page: static("ok"), Loading: func(*Context) (ui.Node, error) { return ui.El("script", nil), nil }},
	}})
	for _, tc := range []struct {
		path   string
		status int
		want   string
	}{
		{"/posts/hello%20world?q=%3Cgo%3E", 200, "<p>hello world:&lt;go&gt;</p>"},
		{"/not-found", 204, ""}, {"https://other.test/posts", 400, "Invalid path"},
		{"/fail", 500, "Unable to load fallback"}, {"/panic", 500, "Unable to load fallback"}, {"/bad-html", 500, "Unable to render fallback"},
	} {
		w := request(a, "/_nextgo/loading?path="+url.QueryEscape(tc.path), false)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), "private") || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s %v", tc.path, w.Code, w.Body.String(), w.Header())
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/_nextgo/loading?path=/posts/a", nil)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
}

func TestRuntimeMiddlewareRedirectRewrite(t *testing.T) {
	var order []string
	middleware := func(name string) func(http.Handler) http.Handler {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+" before")
				w.Header().Add("X-Middleware", name)
				next.ServeHTTP(w, r)
				order = append(order, name+" after")
			})
		}
	}
	a := testApp(t, Config{Headers: http.Header{"X-Configured": []string{"one", "two"}}, Middleware: []func(http.Handler) http.Handler{middleware("outer"), middleware("inner")}, Redirects: []RedirectRule{{Source: "/old/:slug", Destination: "/target/:slug?from=old", Permanent: true}}, Rewrites: []RewriteRule{{Source: "/legacy/:slug", Destination: "/target/:slug"}}, Routes: []Route{{Pattern: "/target/:slug", Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text(c.Params["slug"] + "|" + c.Query.Get("q"))}, nil
	}}}})
	w, envelope := runtimeEnvelope(t, a, "/legacy/a%3Fb?q=two", nil)
	if envelope.URL != "/legacy/a%3Fb?q=two" || envelope.HTML != "a?b|two" || w.Code != 200 {
		t.Fatalf("rewrite lost public URL or request data: %d %+v", w.Code, envelope)
	}
	if !reflect.DeepEqual(order, []string{"outer before", "inner before", "inner after", "outer after"}) || !reflect.DeepEqual(w.Header().Values("X-Configured"), []string{"one", "two"}) || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("middleware/header mismatch: %v %v", order, w.Header())
	}
	redirect := request(a, "/old/a%3Fb", false)
	if redirect.Code != 308 || redirect.Header().Get("Location") != "/target/a%3Fb?from=old" {
		t.Fatalf("redirect escaped param: %d %v", redirect.Code, redirect.Header())
	}
}

func TestRuntimePublicRenderCache(t *testing.T) {
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	store := cache.New(128, cache.WithClock(func() time.Time { return time.Unix(0, now.Load()) }))
	var loads atomic.Int64
	a := testApp(t, Config{Cache: store, Routes: []Route{{Pattern: "/public", Cache: &CachePolicy{TTL: time.Minute, Tags: []string{"articles"}}, Page: func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(loads.Add(1)))}, nil }}}})
	w, first := runtimeEnvelope(t, a, "/public", nil)
	_, second := runtimeEnvelope(t, a, "/public", nil)
	if first.HTML != "1" || second.HTML != "1" || loads.Load() != 1 || w.Header().Get("X-Nextgo-Prefetch") != "public" || w.Header().Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("cache miss: %v %+v %+v loads=%d", w.Header(), first, second, loads.Load())
	}
	_, query := runtimeEnvelope(t, a, "/public?q=other", nil)
	if query.HTML != "2" {
		t.Fatal("query inputs shared cache")
	}
	a.RevalidateTag("articles")
	_, tag := runtimeEnvelope(t, a, "/public", nil)
	if tag.HTML != "3" {
		t.Fatal("tag invalidation did not reload")
	}
	a.RevalidatePath("/public")
	_, path := runtimeEnvelope(t, a, "/public", nil)
	if path.HTML != "4" {
		t.Fatal("path invalidation did not reload")
	}
	now.Add(int64(2 * time.Minute))
	_, expired := runtimeEnvelope(t, a, "/public", nil)
	if expired.HTML != "5" {
		t.Fatal("expired cache did not reload")
	}
	for _, headers := range []http.Header{{"Cookie": []string{"session=private"}}, {"Authorization": []string{"Bearer private"}}} {
		before := loads.Load()
		for i := 0; i < 2; i++ {
			private, _ := runtimeEnvelope(t, a, "/public", headers)
			if private.Header().Get("Cache-Control") != "no-store" || private.Header().Get("X-Nextgo-Prefetch") != "" {
				t.Fatalf("private response cacheable: %v", private.Header())
			}
		}
		if loads.Load() != before+2 {
			t.Fatal("private request used public cache")
		}
	}
	_, anonymous := runtimeEnvelope(t, a, "/public", nil)
	if anonymous.HTML != "5" {
		t.Fatal("private output polluted anonymous cache")
	}
}

func TestRuntimeCacheBypassesDevAndResponseCookies(t *testing.T) {
	for _, mode := range []string{"development", "set-cookie"} {
		t.Run(mode, func(t *testing.T) {
			var loads atomic.Int64
			config := Config{Dev: mode == "development", Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("public")}, nil }}}}
			if mode == "set-cookie" {
				config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						http.SetCookie(w, &http.Cookie{Name: "session", Value: "private"})
						next.ServeHTTP(w, r)
					})
				}}
			}
			a := testApp(t, config)
			for i := 0; i < 2; i++ {
				w, _ := runtimeEnvelope(t, a, "/", nil)
				wantCache := "no-store"
				if mode == "set-cookie" {
					wantCache = "private, no-store"
				}
				if w.Header().Get("Cache-Control") != wantCache || w.Header().Get("X-Nextgo-Prefetch") != "" {
					t.Fatalf("uncacheable output marked public: %v", w.Header())
				}
			}
			if loads.Load() != 2 {
				t.Fatalf("uncacheable loader ran %d times", loads.Load())
			}
		})
	}
}

func TestRuntimeCacheDoesNotRetainFailures(t *testing.T) {
	for _, mode := range []string{"loader", "notfound", "html", "metadata"} {
		t.Run(mode, func(t *testing.T) {
			var loads atomic.Int64
			a := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
				loads.Add(1)
				switch mode {
				case "loader":
					return Page{}, errors.New("private failure")
				case "notfound":
					return Page{}, ErrNotFound
				case "html":
					return Page{Body: ui.El("script", nil)}, nil
				default:
					return Page{Body: ui.Text("body"), Metadata: seo.Metadata{Canonical: "javascript:private()"}}, nil
				}
			}}}})
			for i := 0; i < 2; i++ {
				w, _ := runtimeEnvelope(t, a, "/", nil)
				want := 500
				if mode == "notfound" {
					want = 404
				}
				if w.Code != want || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Nextgo-Prefetch") != "" || strings.Contains(w.Body.String(), "private") {
					t.Fatalf("invalid failed response: %d %s %v", w.Code, w.Body.String(), w.Header())
				}
			}
			if loads.Load() != 2 {
				t.Fatalf("failed %s render was retained: %d loads", mode, loads.Load())
			}
		})
	}
}

func TestRuntimeRichMetadataAndPersistentLayouts(t *testing.T) {
	layout := func(_ *Context, n ui.Node) (ui.Node, error) { return ui.El("main", nil, n), nil }
	a := testApp(t, Config{PreserveLayouts: true, Routes: []Route{{Pattern: "/", Layouts: []LayoutFunc{layout, layout}, LayoutIDs: []string{"root", "nested"}, Page: func(*Context) (Page, error) {
		return Page{Body: ui.Text("page"), Metadata: seo.Metadata{Title: "Rich <title>", Description: "A & B", Canonical: "https://example.test/article", Robots: "index,follow", OpenGraph: seo.OpenGraph{Title: "Social <title>", URL: "https://example.test/article", Images: []seo.Image{{URL: "https://example.test/image.png", Width: 1200, Height: 630}}}, Twitter: seo.Twitter{Card: "summary_large_image"}, Alternates: map[string]string{"fr": "https://example.test/fr/article"}, Icons: []seo.Icon{{URL: "/assets/icon.png"}}}}, nil
	}}}})
	w, envelope := runtimeEnvelope(t, a, "/", nil)
	if w.Code != 200 || envelope.Title != "Rich <title>" || envelope.Description != "A & B" {
		t.Fatalf("metadata defaults: %+v", envelope)
	}
	find := func(tag, key, value string) bool {
		for _, element := range envelope.Head {
			if element.Tag == tag && element.Attrs[key] == value {
				return true
			}
		}
		return false
	}
	if !find("link", "rel", "canonical") || !find("meta", "property", "og:title") || !find("meta", "name", "twitter:card") || !find("link", "hreflang", "fr") || !find("link", "rel", "icon") {
		t.Fatalf("incomplete metadata envelope: %+v", envelope.Head)
	}
	want := `<nextgo-layout data-nextgo-layout="root"><main><nextgo-outlet data-nextgo-outlet="root"><nextgo-layout data-nextgo-layout="nested"><main><nextgo-outlet data-nextgo-outlet="nested">page</nextgo-outlet></main></nextgo-layout></nextgo-outlet></main></nextgo-layout>`
	if envelope.HTML != want {
		t.Fatalf("persistent outlet nesting: %s", envelope.HTML)
	}
	full := request(a, "/", false).Body.String()
	for _, part := range []string{`<title>Rich &lt;title&gt;</title>`, `content="A &amp; B"`, `href="https://example.test/article" rel="canonical"`, `data-nextgo-preserve-layouts="true"`, `property="og:title"`} {
		if !strings.Contains(full, part) {
			t.Errorf("full document missing %q: %s", part, full)
		}
	}
}

type runtimeStreamRecorder struct {
	*httptest.ResponseRecorder
	flushes chan string
}

func (w *runtimeStreamRecorder) Flush() { w.ResponseRecorder.Flush(); w.flushes <- w.Body.String() }
func runtimeStreamFlush(t *testing.T, w *runtimeStreamRecorder) string {
	t.Helper()
	select {
	case content := <-w.flushes:
		return content
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not flush")
		return ""
	}
}

func TestRuntimeStreamingBoundaries(t *testing.T) {
	release := make(chan struct{})
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Title: "Streaming", Stream: true, Body: ui.Fragment(ui.Text("initial content"), ui.Defer("slow", ui.Text("loading fallback"), func(ctx context.Context) (ui.Node, error) {
			select {
			case <-release:
				return ui.Text("loaded <content>"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), ui.Text("initial footer"))}, nil
	}}}})
	w := &runtimeStreamRecorder{ResponseRecorder: httptest.NewRecorder(), flushes: make(chan string, 8)}
	done := make(chan struct{})
	go func() { defer close(done); a.ServeHTTP(w, httptest.NewRequest("GET", "/", nil)) }()
	initial := runtimeStreamFlush(t, w)
	if !strings.Contains(initial, `<div aria-busy="true" data-nextgo-boundary="slow">loading fallback</div>initial footer</div><span id="nextgo-initial-ready" hidden></span>`) || strings.Contains(initial, "data-nextgo-chunk") {
		t.Fatalf("initial stream incomplete: %s", initial)
	}
	close(release)
	chunk := runtimeStreamFlush(t, w)
	if !strings.Contains(chunk, `<template data-nextgo-chunk="slow">loaded &lt;content&gt;</template>`) {
		t.Fatalf("missing inert chunk: %s", chunk)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not complete")
	}
	if w.Code != 200 || !strings.HasSuffix(w.Body.String(), "</body></html>") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("bad streaming document: %d %s", w.Code, w.Body.String())
	}
	_, envelope := runtimeEnvelope(t, a, "/", nil)
	if envelope.HTML != "initial contentloaded &lt;content&gt;initial footer" {
		t.Fatalf("navigation must return complete streaming page: %s", envelope.HTML)
	}
}

func TestRuntimeStreamingInitialErrorsReturn500(t *testing.T) {
	load := func(context.Context) (ui.Node, error) { return ui.Text("loaded"), nil }
	for _, node := range []ui.Node{
		ui.Fragment(ui.Text("private initial markup"), ui.Defer("bad id", ui.Text("fallback"), load)),
		ui.Fragment(ui.Defer("same", nil, load), ui.Defer("same", nil, load)),
		ui.El("script", nil),
	} {
		a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{Title: "private title", Body: node, Stream: true}, nil }}}})
		w := request(a, "/", false)
		if w.Code != 500 || !strings.Contains(w.Body.String(), "Something went wrong") || strings.Contains(w.Body.String(), "private") || strings.Contains(w.Body.String(), "data-nextgo-boundary") {
			t.Fatalf("initial stream failure committed invalid 200 or leaked content: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestRuntimeStreamingRequestCancellation(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Defer("slow", ui.Text("loading"), func(ctx context.Context) (ui.Node, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		})}, nil
	}}}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &runtimeStreamRecorder{ResponseRecorder: httptest.NewRecorder(), flushes: make(chan string, 8)}
	done := make(chan struct{})
	go func() { defer close(done); a.ServeHTTP(w, httptest.NewRequest("GET", "/", nil).WithContext(ctx)) }()
	runtimeStreamFlush(t, w)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("loader did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP handler ignored cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("HTTP request did not cancel deferred loader")
	}
	if strings.Contains(w.Body.String(), "data-nextgo-chunk") {
		t.Fatal("canceled HTTP stream emitted a chunk")
	}
}

type runtimeDisconnectedWriter struct {
	header  http.Header
	started <-chan struct{}
	ctx     context.Context
}

func (w *runtimeDisconnectedWriter) Header() http.Header { return w.header }
func (w *runtimeDisconnectedWriter) WriteHeader(int)     {}
func (w *runtimeDisconnectedWriter) Write([]byte) (int, error) {
	select {
	case <-w.started:
	case <-w.ctx.Done():
	}
	return 0, errors.New("disconnected client")
}
func (w *runtimeDisconnectedWriter) Flush() {}

func TestRuntimeStreamingFirstWriteFailureCancelsLoaders(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Defer("slow", ui.Text("loading"), func(ctx context.Context) (ui.Node, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		})}, nil
	}}}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	w := &runtimeDisconnectedWriter{header: make(http.Header), started: started, ctx: ctx}
	done := make(chan struct{})
	go func() { defer close(done); a.ServeHTTP(w, httptest.NewRequest("GET", "/", nil).WithContext(ctx)) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stream waited on loader after initial destination write failed")
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("initial write failure did not cancel loader")
	}
}
