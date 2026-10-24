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
	"time"

	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

type partialPrivateKey struct{}

func partialTestRoute(calls, regions *atomic.Int32, broken *atomic.Bool) Route {
	return Route{Pattern: "/article/:slug", StaticParams: func(context.Context) ([]Params, error) { return []Params{{"slug": "one"}}, nil },
		Prerender: &PrerenderPolicy{Partial: true, HostIndependent: true, Revalidate: time.Hour, Expire: 24 * time.Hour, Tags: []string{"partial-shell"}},
		Page: func(c *Context) (Page, error) {
			version := calls.Add(1)
			if broken.Load() {
				return Page{}, errors.New("private static source unavailable")
			}
			if c.Value(partialPrivateKey{}) != nil || c.Request.PathValue("private") != "" || len(c.Request.Header) != 0 || len(c.Query) != 0 || c.Request.RemoteAddr != "" || c.Request.URL.RawQuery != "" {
				return Page{}, errors.New("private inputs reached the static shell")
			}
			if c.Params["slug"] == "missing" {
				return Page{}, ErrNotFound
			}
			return Page{Title: fmt.Sprintf("Static title %d", version), Description: "public description", Body: ui.El("article", nil,
				ui.El("h1", nil, ui.Text(fmt.Sprintf("public shell %d", version))),
				ui.RequestRegion("visitor-first", "visitor", map[string]string{"prefix": "first"}, ui.Text("waiting first")),
				ui.RequestRegion("visitor-second", "visitor", map[string]string{"prefix": "second"}, ui.Text("waiting second")),
			)}, nil
		}, Regions: map[string]RegionFunc{"visitor": func(c *Context, props json.RawMessage) (ui.Node, error) {
			regions.Add(1)
			var input struct {
				Prefix string `json:"prefix"`
			}
			if err := json.Unmarshal(props, &input); err != nil {
				return nil, err
			}
			cookie, _ := c.Request.Cookie("visitor")
			name := "anonymous"
			if cookie != nil {
				name = cookie.Value
			}
			if !errors.Is(c.SetHeader("X-Late", "forbidden"), ErrHeadersCommitted) {
				return nil, errors.New("region headers were not sealed")
			}
			return ui.El("p", nil, ui.Text(input.Prefix+":"+c.Params["slug"]+":"+c.Query.Get("message")+":"+name+":"+fmt.Sprint(c.Value(partialPrivateKey{})))), nil
		}},
	}
}

func partialTestRead(t *testing.T, app *App, target, message, visitor string, stream bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", target, nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	r.Header.Set("Authorization", "private credentials")
	r.Header.Set("Cookie", "visitor="+visitor)
	r.Header.Set("X-Private", message)
	r.SetPathValue("private", "hidden path value")
	r = r.WithContext(context.WithValue(r.Context(), partialPrivateKey{}, message))
	if stream {
		r.Header.Set(navigation.StreamHeader, "1")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func TestPartialPrerenderUsesStaticShellAndFreshRequestRegions(t *testing.T) {
	var calls, regions, layouts atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&calls, &regions, &broken)
	route.Layouts = []LayoutFunc{func(_ *Context, body ui.Node) (ui.Node, error) {
		layouts.Add(1)
		if broken.Load() {
			return nil, errors.New("static layout unavailable")
		}
		return ui.El("main", nil, body), nil
	}}
	c, data := buildPrerenderTest(t, Config{Routes: []Route{route}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.SetPathValue("private", "middleware path state")
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), partialPrivateKey{}, "middleware private value")))
		})
	}}})
	if calls.Load() != 1 || layouts.Load() != 1 || regions.Load() != 0 || strings.Contains(string(data), "middleware private value") {
		t.Fatal("build evaluated regions or retained private data", calls.Load(), layouts.Load(), regions.Load())
	}
	var manifest prerenderManifest
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Version != 3 || len(manifest.Pages[0].Regions) != 2 {
		t.Fatal(manifest, err)
	}
	broken.Store(true)
	app := testApp(t, c)
	full := httptest.NewRecorder()
	app.ServeHTTP(full, httptest.NewRequest("GET", "/article/one?message=direct", nil))
	if full.Code != 200 || full.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(full.Body.String(), "data-nextgo-chunk") {
		t.Fatal("initial partial document lost its private policy or stream", full.Code, full.Header())
	}
	for _, stream := range []bool{false, true} {
		for _, name := range []string{"alice", "bob"} {
			w := partialTestRead(t, app, "/article/one?message=%3Cpersonal%3E", name, name, stream)
			if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Nextgo-Partial") != "1" || w.Header().Get("X-Nextgo-Prerender") != "build" || w.Header().Get("X-Nextgo-Prefetch") != "" || w.Header().Get("Age") != "" {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
			body := w.Body.String()
			if stream {
				decoder := &navigation.StreamDecoder{}
				var chunks strings.Builder
				if err := decoder.Feed(w.Body.Bytes(), func(frame navigation.StreamFrame) error {
					if frame.Type == "chunk" {
						chunks.WriteString(frame.HTML)
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				if err := decoder.Finish(); err != nil {
					t.Fatal(err)
				}
				body = chunks.String()
			} else {
				var envelope Envelope
				if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Title != "Static title 1" {
					t.Fatal(envelope, err)
				}
				body = envelope.HTML
			}
			if !strings.Contains(body, "first:one:&lt;personal&gt;:"+name+":middleware private value") || !strings.Contains(body, "second:one:&lt;personal&gt;:"+name) || strings.Contains(body, "waiting") {
				t.Fatal(body)
			}
		}
	}
	if calls.Load() != 1 || layouts.Load() != 1 || regions.Load() != 10 {
		t.Fatal("shell execution or region reuse", calls.Load(), layouts.Load(), regions.Load())
	}
}

func TestPartialPrerenderRegenerationRedactsPrivateInputsAndPreserves404(t *testing.T) {
	var calls, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&calls, &regions, &broken)
	route.NotFound = static("custom missing article")
	c, _ := buildPrerenderTest(t, Config{Routes: []Route{route}})
	app := testApp(t, c)
	app.RevalidateTag("partial-shell")
	w := partialTestRead(t, app, "http://private-host/article/one?message=private-query", "context secret", "alice", false)
	if w.Code != 200 || calls.Load() != 2 || w.Header().Get("X-Nextgo-Prerender") != "runtime" || strings.Contains(w.Body.String(), "private inputs reached") {
		t.Fatal(w.Code, w.Body.String(), calls.Load())
	}
	var envelope Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Title != "Static title 2" || !strings.Contains(envelope.HTML, "context secret") || !strings.Contains(envelope.HTML, "private-query") {
		t.Fatal(envelope, err)
	}
	w = partialTestRead(t, app, "/article/missing", "context secret", "alice", false)
	if w.Code != 404 || !strings.Contains(w.Body.String(), "custom missing article") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestPartialPrerenderSharedShellNeverCachesPrivateRegionResults(t *testing.T) {
	_, store := sharedPrerenderMemory(t)
	var calls, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&calls, &regions, &broken)
	route.Prerender.Shared = true
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{route}})
	broken.Store(true)
	a, b := sharedPageApp(t, c), sharedPageApp(t, c)
	for i, app := range []*App{a, b, a, b} {
		name := fmt.Sprintf("visitor%d", i)
		w := partialTestRead(t, app, "/article/one?message="+name, name, name, false)
		if w.Code != 200 || w.Header().Get("X-Nextgo-Prerender") != "build" || !strings.Contains(w.Body.String(), name) {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
		for j := 0; j < i; j++ {
			if strings.Contains(w.Body.String(), fmt.Sprintf("visitor%d", j)) {
				t.Fatal("private output reused across replicas", w.Body.String())
			}
		}
	}
	if calls.Load() != 1 || regions.Load() != 8 || a.config.Cache.Len() != 0 || b.config.Cache.Len() != 0 {
		t.Fatal(calls.Load(), regions.Load(), a.config.Cache.Len(), b.config.Cache.Len())
	}
	broken.Store(false)
	if err := a.ExpireCache(context.Background(), shared.Selector{Kind: "tag", Value: "partial-shell"}); err != nil {
		t.Fatal(err)
	}
	w := partialTestRead(t, b, "/article/one?message=new", "new private context", "new", false)
	if w.Code != 200 || calls.Load() != 2 || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(w.Code, w.Body.String(), calls.Load())
	}
}

func TestPartialRequestRegionRegistryValidation(t *testing.T) {
	for _, registry := range []map[string]RegionFunc{{"bad/name": func(*Context, json.RawMessage) (ui.Node, error) { return nil, nil }}, {"valid": nil}} {
		if _, err := New(Config{Routes: []Route{{Pattern: "/", Page: static("page"), Regions: registry}}}); err == nil {
			t.Fatal("invalid registry accepted")
		}
	}
	var calls, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&calls, &regions, &broken)
	route.Prerender.Partial = false
	if _, err := New(Config{Dev: true, Routes: []Route{route}}); err == nil {
		t.Fatal("request regions allowed in full-page prerender cache")
	}
}
