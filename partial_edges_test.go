package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestPartialInterceptionTargetCacheBypassDoesNotPanic(t *testing.T) {
	var pages, regions atomic.Int32
	var broken atomic.Bool
	target := partialTestRoute(&pages, &regions, &broken)
	target.Pattern = "/photo"
	target.StaticParams = nil
	target.Page = func(*Context) (Page, error) {
		pages.Add(1)
		return Page{Body: ui.Fragment(ui.El("h1", nil, ui.Text("public photo shell")), ui.RequestRegion("visitor", "visitor", map[string]string{"prefix": "private"}, ui.Text("waiting")))}, nil
	}
	layout := func(c *Context, body ui.Node) (ui.Node, error) {
		modal, err := c.Slot("modal")
		return ui.El("main", nil, body, modal), err
	}
	owner := Route{Pattern: "/gallery", Page: static("gallery"), Layouts: []LayoutFunc{layout}, LayoutIDs: []string{"gallery"}, LayoutBases: []string{"/gallery"}, Primary: &PrimarySlot{ID: "gallery.children", Base: "/gallery", Layout: 0}, Slots: []Slot{{ID: "gallery.modal", Name: "modal", Base: "/gallery", Layout: 0, Default: static("empty"), Intercepts: []Route{{Pattern: "/photo", Page: static("modal photo")}}}}}
	c, _ := buildSharedPrerenderTest(t, Config{PreserveLayouts: true, Routes: []Route{owner, target}})
	app := sharedPageApp(t, c)
	w := partialTestRead(t, app, "/photo?message=private-query", "private context", "alice", false)
	e := sharedPageEnvelope(t, w)
	if w.Code != 200 || !strings.Contains(e.HTML, "private-query:alice:private context") || pages.Load() != 2 || regions.Load() != 1 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("partial interception target failed existing cache bypass", w.Code, e, w.Header(), pages.Load(), regions.Load())
	}
}

func TestPartialUncachedErrorStatusesRemain404And403(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprint(remote), func(t *testing.T) {
			var pages, regions atomic.Int32
			var broken atomic.Bool
			route := partialTestRoute(&pages, &regions, &broken)
			original := route.Page
			route.Page = func(c *Context) (Page, error) {
				if c.Params["slug"] == "denied" {
					return Page{}, ErrForbidden
				}
				return original(c)
			}
			route.NotFound = static("custom partial missing")
			config := Config{Routes: []Route{route}, Forbidden: static("custom partial denied")}
			if remote {
				_, store := sharedPrerenderMemory(t)
				config.SharedCache = store
				config.Routes[0].Prerender.Shared = true
			}
			c, _ := buildSharedPrerenderTest(t, config)
			app := sharedPageApp(t, c)
			for _, test := range []struct {
				slug   string
				status int
				text   string
			}{{"missing", 404, "custom partial missing"}, {"denied", 403, "custom partial denied"}} {
				for i := 0; i < 2; i++ {
					w := partialTestRead(t, app, "/article/"+test.slug, "private", "alice", false)
					e := sharedPageEnvelope(t, w)
					if w.Code != test.status || e.Status != test.status || !strings.Contains(e.HTML, test.text) || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || regions.Load() != 0 {
						t.Fatal("uncached shell error status lost", test.slug, w.Code, e, w.Header())
					}
				}
			}
		})
	}
}

func TestPartialSharedLongPathRendersOneUncachedPublicShell(t *testing.T) {
	probe, store := sharedPrerenderMemory(t)
	var pages, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&pages, &regions, &broken)
	route.Prerender.Shared = true
	slug := strings.Repeat("a", 600)
	route.StaticParams = func(context.Context) ([]Params, error) { return []Params{{"slug": slug}}, nil }
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{route}})
	app := sharedPageApp(t, c)
	for i := 0; i < 2; i++ {
		w := partialTestRead(t, app, "/article/"+slug+"?message=private-query", "private context", "alice", false)
		e := sharedPageEnvelope(t, w)
		if w.Code != 200 || !strings.Contains(e.HTML, "private-query:alice:private context") || pages.Load() != int32(i+2) || regions.Load() != int32(2*(i+1)) || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("path-label rejection broke valid uncached shell", w.Code, w.Header(), pages.Load(), regions.Load())
		}
	}
	if count, _ := probe.Stats(); count != 0 || app.config.Cache.Len() != 0 {
		t.Fatal("overlimit path retained data", count, app.config.Cache.Len())
	}
}

func TestPartialRegenerationCanonicalizesShellButNotRequestRegions(t *testing.T) {
	var pages atomic.Int32
	route := Route{Pattern: "/article/:slug", Prerender: &PrerenderPolicy{Partial: true, HostIndependent: true, Revalidate: time.Hour, Tags: []string{"canonical-shell"}}, StaticParams: func(context.Context) ([]Params, error) { return []Params{{"slug": "café"}}, nil },
		Page: func(c *Context) (Page, error) {
			pages.Add(1)
			return Page{Body: ui.Fragment(ui.El("p", ui.Attrs{"data-public-shell": ""}, ui.Text(c.Request.RequestURI+"|"+c.Request.URL.EscapedPath()+"|"+c.Request.URL.RawPath)), ui.RequestRegion("actual", "actual", nil, ui.Text("pending")))}, nil
		},
		Regions: map[string]RegionFunc{"actual": func(c *Context, _ json.RawMessage) (ui.Node, error) {
			return ui.El("p", ui.Attrs{"data-current-request": ""}, ui.Text(c.Request.RequestURI)), nil
		}},
	}
	c, _ := buildSharedPrerenderTest(t, Config{Routes: []Route{route}})
	app := sharedPageApp(t, c)
	app.RevalidateTag("canonical-shell")
	for _, target := range []string{"/article/caf%c3%a9?message=first", "/article/caf%C3%A9?message=second"} {
		w := partialTestRead(t, app, target, "private", "alice", false)
		e := sharedPageEnvelope(t, w)
		canonical := "/article/caf%C3%A9|/article/caf%C3%A9|/article/caf%C3%A9"
		if w.Code != 200 || !strings.Contains(e.HTML, canonical) || !strings.Contains(e.HTML, target) || pages.Load() != 2 {
			t.Fatal("shell key and sanitized request disagree", w.Code, e.HTML, pages.Load())
		}
	}
}

func TestPartialInvalidArtifactRegionsFailBeforeSharedAdmission(t *testing.T) {
	for _, mode := range []string{"unknown-registration", "bad-offset", "duplicate-props", "duplicate-instance", "version"} {
		t.Run(mode, func(t *testing.T) {
			probe, store := sharedPrerenderMemory(t)
			var pages, regions atomic.Int32
			var broken atomic.Bool
			route := partialTestRoute(&pages, &regions, &broken)
			route.Prerender.Shared = true
			c, data := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{route}})
			bad := sharedPrerenderRewriteArtifact(t, c, data, func(m *prerenderManifest) {
				switch mode {
				case "unknown-registration":
					m.Pages[0].Regions[0].Region = "unregistered"
				case "bad-offset":
					m.Pages[0].Regions[0].Start = -1
				case "duplicate-props":
					m.Pages[0].Regions[0].Props = json.RawMessage(`{"x":1,"x":2}`)
				case "duplicate-instance":
					m.Pages[0].Regions[1].ID = m.Pages[0].Regions[0].ID
				case "version":
					m.Version = 2
				}
			})
			if app, err := New(bad); err == nil {
				_ = app.Close(context.Background())
				t.Fatal("malformed partial artifact accepted")
			}
			if probe.sessions.Load() != 0 || regions.Load() != 0 {
				t.Fatal("invalid artifact consumed ticket or invoked request regions", probe.sessions.Load(), regions.Load())
			}
			valid := sharedPageApp(t, c)
			w := partialTestRead(t, valid, "/article/one", "private", "alice", false)
			if w.Code != 200 || w.Header().Get("X-Nextgo-Prerender") != "build" || probe.sessions.Load() != 1 {
				t.Fatal(w.Code, w.Header(), probe.sessions.Load())
			}
		})
	}
}

func TestPartialRequestBodyBypassNeverStoresPersonalizedPage(t *testing.T) {
	var pages, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&pages, &regions, &broken)
	// A deliberately request-sensitive page can run only in the explicit body
	// bypass. The ordinary public shell remains independent of this request.
	original := route.Page
	route.Page = func(c *Context) (Page, error) {
		if c.Request.Body != nil && c.Request.Body != http.NoBody {
			return Page{Body: ui.Fragment(ui.Text("private body page"), ui.RequestRegion("body", "visitor", map[string]string{"prefix": "private"}, ui.Text("waiting")))}, nil
		}
		return original(c)
	}
	c, _ := buildSharedPrerenderTest(t, Config{Routes: []Route{route}})
	app := sharedPageApp(t, c)
	r := httptest.NewRequest("GET", "/article/one", strings.NewReader("private payload"))
	r.Header.Set("X-Nextgo-Navigate", "1")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(sharedPageEnvelope(t, w).HTML, "private body page") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	again := partialTestRead(t, app, "/article/one", "private", "alice", false)
	if again.Code != 200 || strings.Contains(sharedPageEnvelope(t, again).HTML, "private body page") || pages.Load() != 1 {
		t.Fatal("body bypass poisoned shell cache", again.Code, pages.Load())
	}
}
