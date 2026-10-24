package nextgo

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestHeaderRulesOrderedCapturesAndFrozenPolicy(t *testing.T) {
	headers := []HeaderValue{{Name: "X-Policy", Value: "first"}, {Name: "Link", Value: "<one>"}}
	conditions := []RuleCondition{{Type: QueryCondition, Key: "v", Value: `(?<variant>[a-z]+)`}}
	config := Config{Headers: http.Header{"X-Policy": {"global"}}, HeaderRules: []HeaderRule{
		{Source: "/docs/:slug", Headers: headers},
		{Source: "/docs/:slug", Has: conditions, Missing: []RuleCondition{{Type: CookieCondition, Key: "disable"}}, Headers: []HeaderValue{{Name: "X-Policy", Value: "{{slug}}:{{variant}}"}, {Name: "Link", Value: "<two>", Append: true}, {Name: "X-Empty", Value: ""}}},
	}, Routes: []Route{{Pattern: "/docs/:slug", Page: staticPage}}}
	app := testApp(t, config)
	headers[0].Value = "mutated"
	conditions[0].Value = "never"
	config.HeaderRules[1].Headers[0].Value = "changed"
	w := request(app, "/docs/caf%C3%A9?v=blue", false)
	if w.Code != 200 || w.Header().Get("X-Policy") != "café:blue" || strings.Join(w.Header().Values("Link"), ",") != "<one>,<two>" || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Robots-Tag") != "" {
		t.Fatal(w.Code, w.Header())
	}
	w = conditionRequest(app, "/docs/a?v=blue", http.Header{"Cookie": {"disable=true"}})
	if w.Header().Get("X-Policy") != "first" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Header())
	}
}
func TestHeaderRulesBeforeMiddlewareRewritesAPIsAndNotFound(t *testing.T) {
	app := testApp(t, Config{HeaderRules: []HeaderRule{{Source: "/source/:id", Headers: []HeaderValue{{Name: "X-Source", Value: "{{id}}"}}}, {Source: "/api/:id", Headers: []HeaderValue{{Name: "X-API", Value: "{{id}}"}}}, {Source: "/missing", Headers: []HeaderValue{{Name: "X-Missing", Value: "yes"}}}}, Rewrites: []RewriteRule{{Source: "/source/:id", Destination: "/target/:id"}}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/source/one" && w.Header().Get("X-Source") != "one" {
				t.Error("header policy ran after middleware")
			}
			w.Header().Set("X-Middleware", "yes")
			next.ServeHTTP(w, r)
		})
	}}, Routes: []Route{{Pattern: "/target/:id", Page: staticPage}, {Pattern: "/api/:id", API: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) }}}})
	for _, tc := range []struct {
		path, key, value string
		status           int
	}{{"/source/one", "X-Source", "one", 200}, {"/api/two", "X-API", "two", 201}, {"/missing", "X-Missing", "yes", 404}} {
		w := request(app, tc.path, false)
		if w.Code != tc.status || w.Header().Get(tc.key) != tc.value || w.Header().Get("X-Middleware") != "yes" {
			t.Fatal(tc, w.Code, w.Header())
		}
	}
}
func TestConditionalHeaderPrivacyAtFinalHandlerWrite(t *testing.T) {
	var loads atomic.Int32
	app := testApp(t, Config{HeaderRules: []HeaderRule{{Source: "/*tail", Has: []RuleCondition{{Type: HeaderCondition, Key: "X-User", Value: `(?<user>.+)`}}, Headers: []HeaderValue{{Name: "X-User-Rule", Value: "{{user}}"}}}}, Routes: []Route{
		{Pattern: "/page", Cache: &CachePolicy{TTL: time.Hour}, Page: func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("page")}, nil }},
		{Pattern: "/api", API: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "public, max-age=999")
			w.Header().Set("X-Nextgo-Prefetch", "public")
			w.Header().Set("Age", "20")
			_, _ = io.WriteString(w, "api")
		}},
	}})
	for _, value := range []string{"alice", "", "bob"} {
		for _, path := range []string{"/page", "/api"} {
			h := http.Header{}
			if value != "" {
				h.Set("X-User", value)
			}
			w := conditionRequest(app, path, h)
			if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Nextgo-Prefetch") != "" || w.Header().Get("Age") != "" || w.Header().Get("X-Robots-Tag") != "" {
				t.Fatal(path, w.Code, w.Header())
			}
		}
	}
	if loads.Load() != 3 {
		t.Fatal("conditional header source used shared page cache", loads.Load())
	}
}
func TestHeaderRuleValidationAndResolutionAreFailClosed(t *testing.T) {
	for _, rule := range []HeaderRule{
		{Source: "/x"}, {Source: "/x", Headers: []HeaderValue{{Name: "Bad Header", Value: "x"}}},
		{Source: "/x", Headers: []HeaderValue{{Name: "X-Nextgo-Protocol", Value: "3"}}},
		{Source: "/x", Headers: []HeaderValue{{Name: "Set-Cookie", Value: "secret=x"}}},
		{Source: "/x", Headers: []HeaderValue{{Name: "Content-Length", Value: "1"}}},
		{Source: "/x", Headers: []HeaderValue{{Name: "X-Test", Value: "bad\r\nx: y"}}},
		{Source: "/x", Headers: []HeaderValue{{Name: "X-Test", Value: "{{missing}}"}}},
		{Source: "/x", Headers: []HeaderValue{{Name: "X-Test", Value: "{{bad"}}},
		{Source: "/x", Has: []RuleCondition{{Type: QueryCondition, Key: "q", Value: "(?<q>.*)"}}, Headers: []HeaderValue{{Name: "X-Test", Value: strings.Repeat("{{q}}", 130)}}},
	} {
		if _, err := New(Config{HeaderRules: []HeaderRule{rule}}); err == nil {
			t.Fatal("accepted invalid header rule", rule)
		}
	}
	var called atomic.Bool
	app := testApp(t, Config{Logger: log.New(io.Discard, "", 0), HeaderRules: []HeaderRule{{Source: "/x", Has: []RuleCondition{{Type: QueryCondition, Key: "q", Value: `(?<q>.*)`}}, Headers: []HeaderValue{{Name: "X-First", Value: "must not be applied"}, {Name: "X-Test", Value: "{{q}}"}}}}, Routes: []Route{{Pattern: "/x", API: func(w http.ResponseWriter, r *http.Request) { called.Store(true) }}}})
	w := request(app, "/x?q=%0DInjected", false)
	if w.Code != 400 || called.Load() || w.Header().Get("X-First") != "" || strings.Contains(w.Body.String(), "Injected") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
}
func TestHeaderRulesAssetsLocaleRedirectsAndCanonicalPaths(t *testing.T) {
	config := Config{BuildID: "build", BasePath: "/site", AssetPrefix: "/cdn", Assets: fstest.MapFS{"public/test.txt": &fstest.MapFile{Data: []byte("public")}, "_nextgo/build/app.wasm": &fstest.MapFile{Data: []byte("wasm")}}, HeaderRules: []HeaderRule{{Source: "/*tail?", Headers: []HeaderValue{{Name: "X-Path", Value: "{{tail}}"}}}}, Routes: []Route{{Pattern: "/about", Page: staticPage}}}
	app := testApp(t, config)
	for _, path := range []string{"/site/about", "/site/about/", "/cdn/assets/test.txt", "/cdn/_nextgo/build/app.wasm"} {
		w := request(app, path, false)
		if w.Code >= 400 || w.Header().Get("X-Path") == "" {
			t.Fatal(path, w.Code, w.Header())
		}
	}
	w := httptest.NewRecorder()
	app.AssetHandler().ServeHTTP(w, httptest.NewRequest("GET", "/assets/test.txt", nil))
	if w.Header().Get("X-Path") != "assets/test.txt" {
		t.Fatal(w.Header())
	}
	config.Locales = &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}
	localized := testApp(t, config)
	for _, path := range []string{"/site/about", "/site/FR/about", "/site/fr/about"} {
		w := request(localized, path, false)
		if w.Code >= 400 || w.Header().Get("X-Path") != "about" {
			t.Fatal(path, w.Code, w.Header())
		}
	}
}
func TestHeaderRulesBindOwnershipAndRejectStaticPublication(t *testing.T) {
	c := Config{BuildID: "same", HeaderRules: []HeaderRule{{Source: "/", Headers: []HeaderValue{{Name: "X-Private-Policy", Value: "private-policy-expression"}}}}, Routes: []Route{{Pattern: "/", Page: staticPage}}}
	app := testApp(t, c)
	if err := app.CheckStaticExport(); err == nil {
		t.Fatal("static publication dropped response header policy")
	}
	w := request(app, "/", false)
	if strings.Contains(w.Body.String(), "private-policy-expression") {
		t.Fatal("header config leaked into browser manifest")
	}
	first := app.configurationID
	c.HeaderRules[0].Headers = []HeaderValue{{Name: "X-Private-Policy", Value: "different"}}
	changed := testApp(t, c)
	if changed.configurationID == first {
		t.Fatal("header policy absent from runtime ownership")
	}
	var e Envelope
	w = request(changed, "/", true)
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Configuration != changed.configurationID {
		t.Fatal(err, w.Body.String())
	}
	c.PrerenderBuildMode = true
	c.Routes[0].Prerender = &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}
	c.HeaderRules[0].Has = []RuleCondition{{Type: CookieCondition, Key: "preview"}}
	build := testApp(t, c)
	if _, err := build.BuildPrerender(context.Background()); err == nil {
		t.Fatal("conditional header source prerendered")
	}
}

func TestHeaderRuleAdmissionAndResolvedBudgets(t *testing.T) {
	many := make([]HeaderRule, 257)
	for i := range many {
		many[i] = HeaderRule{Source: "/x", Headers: []HeaderValue{{Name: "X-A", Value: "a"}}}
	}
	if _, err := New(Config{HeaderRules: many}); err == nil {
		t.Fatal("unbounded header rule count")
	}
	values := make([]HeaderValue, 20)
	for i := range values {
		values[i] = HeaderValue{Name: "X-Big", Value: strings.Repeat("a", 8192)}
	}
	if _, err := New(Config{HeaderRules: []HeaderRule{{Source: "/x", Headers: values}}}); err == nil {
		t.Fatal("unbounded header configuration")
	}
	var called atomic.Bool
	app := testApp(t, Config{HeaderRules: []HeaderRule{{Source: "/x", Headers: values[:4]}}, Routes: []Route{{Pattern: "/x", API: func(http.ResponseWriter, *http.Request) { called.Store(true) }}}})
	w := request(app, "/x", false)
	if w.Code != 400 || called.Load() || w.Header().Get("X-Big") != "" {
		t.Fatal(w.Code, w.Header())
	}
}
func TestHeaderRulePrivacyPreservesFlushAndHandlerOverrides(t *testing.T) {
	app := testApp(t, Config{HeaderRules: []HeaderRule{{Source: "/stream", Has: []RuleCondition{{Type: QueryCondition, Key: "v"}}, Headers: []HeaderValue{{Name: "X-Default", Value: "configured"}}}}, Routes: []Route{{Pattern: "/stream", API: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Default", "handler")
		w.Header().Set("Cache-Control", "public, max-age=99")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error("conditional privacy removed streaming", err)
		}
		_, _ = io.WriteString(w, "streamed")
	}}}})
	w := request(app, "/stream?v=yes", false)
	if w.Code != 200 || !w.Flushed || w.Body.String() != "streamed" || w.Header().Get("X-Default") != "handler" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
}
