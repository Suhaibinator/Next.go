package nextgo

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/draft"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestDraftBypassesPageAndDataCachesAndEnforcesPrivateResponses(t *testing.T) {
	m, err := draft.New(draft.Options{Keys: map[string][]byte{"a": bytes.Repeat([]byte{7}, 32)}, ActiveKey: "a", Scope: "fixture", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	store := cache.New(16)
	var loads atomic.Int64
	page := func(c *Context) (Page, error) {
		value, err := cache.GetOrLoad(c.Context, store, "article", cache.Options{TTL: time.Hour}, func(ctx context.Context) (string, error) {
			kind := "published"
			if draft.Enabled(ctx) {
				kind = "draft"
			}
			return fmt.Sprintf("%s-%d", kind, loads.Add(1)), nil
		})
		return Page{Body: ui.Text(value), Stream: c.Query.Get("stream") == "1"}, err
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=600")
		w.Header().Set("X-Nextgo-Prefetch", "public")
		fmt.Fprint(w, draft.Enabled(r.Context()))
	})
	// Configured handlers may occupy paths that look like public assets.
	app, err := New(Config{Draft: m, Cache: store, Routes: []Route{{Pattern: "/preview", Page: page, Cache: &CachePolicy{TTL: time.Hour}, Loading: func(c *Context) (ui.Node, error) { return ui.Text(fmt.Sprint(draft.Enabled(c))), nil }}}, Handlers: map[string]http.Handler{"/api/preview": handler, "/assets/preview": handler}})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.CheckStaticExport(); err == nil {
		t.Fatal("draft configuration exported to a static host")
	}
	issue := httptest.NewRequest("POST", "http://example.test/preview", nil)
	cookie, err := m.Cookie(issue)
	if err != nil {
		t.Fatal(err)
	}
	serve := func(path string, preview, nav bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://example.test"+path, nil)
		if preview {
			r.AddCookie(cookie)
		}
		if nav {
			r.Header.Set("X-Nextgo-Navigate", "1")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		return w
	}
	first := serve("/preview", false, false)
	if !strings.Contains(first.Body.String(), "published-1") || first.Header().Get("X-Nextgo-Prefetch") != "" || first.Header().Get("X-Nextgo-Draft") != "0" {
		t.Fatal(first.Header(), first.Body.String())
	}
	for i := 2; i <= 3; i++ {
		w := serve("/preview", true, i == 3)
		if w.Code != 200 || !strings.Contains(w.Body.String(), fmt.Sprintf("draft-%d", i)) || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Nextgo-Draft") != "1" || w.Header().Get("X-Robots-Tag") != "noindex" || w.Header().Get("X-Nextgo-Prefetch") != "" {
			t.Fatal(w.Code, w.Header(), w.Body.String())
		}
	}
	if w := serve("/preview", false, false); !strings.Contains(w.Body.String(), "published-1") || loads.Load() != 3 {
		t.Fatal("preview replaced published cache", w.Body.String(), loads.Load())
	}
	for _, path := range []string{"/api/preview", "/assets/preview", "/_nextgo/loading?path=/preview", "/preview?stream=1", "/missing"} {
		w := serve(path, true, false)
		if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Nextgo-Prefetch") != "" || w.Header().Get("X-Robots-Tag") != "noindex" {
			t.Fatal(path, w.Code, w.Header())
		}
		if path == "/preview?stream=1" && !strings.Contains(w.Body.String(), `data-nextgo-draft="1"`) {
			t.Fatal("stream omitted session marker")
		}
	}
}

func TestDraftSessionSpansLocaleMountsAndRejectsAZeroManager(t *testing.T) {
	if _, err := New(Config{Draft: new(draft.Manager)}); err == nil {
		t.Fatal("zero draft manager accepted")
	}
	m, err := draft.New(draft.Options{Keys: map[string][]byte{"a": bytes.Repeat([]byte{7}, 32)}, ActiveKey: "a", Scope: "locale-fixture", Path: "/demo", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	app, err := New(Config{Draft: m, BasePath: "/demo", Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}, Routes: []Route{{Pattern: "/preview", Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text(fmt.Sprint(draft.Enabled(c), "/", CurrentLocale(c)))}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	cookie, err := m.Cookie(httptest.NewRequest("POST", "http://example.test/demo/en/preview", nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, locale := range []string{"en", "fr"} {
		r := httptest.NewRequest("GET", "http://example.test/demo/"+locale+"/preview", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "true/"+locale) || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal(locale, w.Code, w.Body.String())
		}
	}
}

func TestDraftRestoreProbeIsPrivateBoundedAndGenerationBound(t *testing.T) {
	for _, base := range []string{"", "/demo"} {
		t.Run(base, func(t *testing.T) {
			m, err := draft.New(draft.Options{Keys: map[string][]byte{"a": bytes.Repeat([]byte{7}, 32)}, ActiveKey: "a", Scope: "probe", Path: base, AllowInsecure: true})
			if err != nil {
				t.Fatal(err)
			}
			var loads atomic.Int64
			a, err := New(Config{Draft: m, BasePath: base, BuildID: "probe-build", Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("page")}, nil }}}})
			if err != nil {
				t.Fatal(err)
			}
			cookie, err := m.Cookie(httptest.NewRequest("POST", "http://example.test"+base+"/", nil))
			if err != nil {
				t.Fatal(err)
			}
			for _, enabled := range []bool{false, true} {
				for _, method := range []string{"GET", "HEAD"} {
					r := httptest.NewRequest(method, "http://example.test"+base+"/_nextgo/draft", nil)
					if enabled {
						r.AddCookie(cookie)
					}
					w := httptest.NewRecorder()
					a.ServeHTTP(w, r)
					mode := "0"
					if enabled {
						mode = "1"
					}
					if w.Code != 204 || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Nextgo-Build") != "probe-build" || w.Header().Get("X-Nextgo-Configuration") != a.configurationID || w.Header().Get("X-Nextgo-Protocol") != "3" || w.Header().Get("X-Nextgo-Draft") != mode || w.Header().Get("X-Nextgo-Prefetch") != "" {
						t.Fatal(enabled, method, w.Code, w.Header(), w.Body.String())
					}
				}
			}
			w := httptest.NewRecorder()
			a.ServeHTTP(w, httptest.NewRequest("POST", "http://example.test"+base+"/_nextgo/draft", nil))
			if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal(w.Code, w.Header())
			}
			if loads.Load() != 0 {
				t.Fatal("probe executed a page loader")
			}
			w = httptest.NewRecorder()
			a.AssetHandler().ServeHTTP(w, httptest.NewRequest("GET", "http://example.test/_nextgo/draft", nil))
			if w.Code != 404 {
				t.Fatal("asset-only origin served a session probe", w.Code)
			}
		})
	}
	a, err := New(Config{Routes: []Route{{Pattern: "/", Page: static("page")}}})
	if err != nil {
		t.Fatal(err)
	}
	w := request(a, "/_nextgo/draft", false)
	if w.Code != 404 {
		t.Fatal("unconfigured preview probe enabled")
	}
	if _, err := New(Config{BuildID: "draft"}); err == nil {
		t.Fatal("accepted reserved runtime generation")
	}
}
