package nextgo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func conditionRequest(a *App, path string, headers http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	r.Host = "EXAMPLE.TEST:8080"
	r.Header = headers.Clone()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func TestConditionalRulesCaptureAndNegation(t *testing.T) {
	rules := []RewriteRule{{Source: "/alias/:id", Destination: "/target/:id?capture=:capture", Has: []RuleCondition{{Type: HeaderCondition, Key: "X-Variant", Value: `part-(?<capture>[a-z&]+)`}, {Type: CookieCondition, Key: "ready", Value: "yes"}, {Type: QueryCondition, Key: "flag", Value: "on"}, {Type: HostCondition, Value: `example\.test`}}, Missing: []RuleCondition{{Type: QueryCondition, Key: "disable"}}}}
	a := testApp(t, Config{Rewrites: rules, Routes: []Route{{Pattern: "/target/:id", Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text(c.Params["id"] + "|" + c.Query.Get("capture"))}, nil
	}}}})
	headers := http.Header{"X-Variant": []string{"irrelevant", "part-a&b"}, "Cookie": []string{"ready=yes"}, "X-Nextgo-Navigate": []string{"1"}}
	response := conditionRequest(a, "/alias/a%3Fb?flag=on", headers)
	var envelope Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 200 || envelope.HTML != "a?b|a&amp;b" || envelope.URL != "/alias/a%3Fb?flag=on" {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	if response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(response.Header())
	}
	for _, query := range []string{"flag=off", "flag=on&disable=", "flag=on;disable=x"} {
		if got := conditionRequest(a, "/alias/a?"+query, headers); got.Code != 404 {
			t.Fatal(query, got.Code)
		}
	}
	headers.Set("Cookie", "ready=no")
	if got := conditionRequest(a, "/alias/a?flag=on", headers); got.Code != 404 {
		t.Fatal(got.Code)
	}
}
func TestConditionPresenceEmptyAndMissingExpression(t *testing.T) {
	for _, tc := range []struct {
		condition RuleCondition
		has       bool
		headers   http.Header
		query     string
		want      bool
	}{
		{RuleCondition{Type: HeaderCondition, Key: "X-Empty"}, true, http.Header{"X-Empty": []string{""}}, "", true},
		{RuleCondition{Type: QueryCondition, Key: "q", Value: "^$"}, true, nil, "?q=", true},
		{RuleCondition{Type: QueryCondition, Key: "q", Value: "^$"}, true, nil, "", false},
		{RuleCondition{Type: QueryCondition, Key: "q", Value: "blocked"}, false, nil, "?q=other", true},
		{RuleCondition{Type: QueryCondition, Key: "q", Value: "blocked"}, false, nil, "?q=blocked", false},
		{RuleCondition{Type: QueryCondition, Key: "q"}, false, nil, "?q=bad;parse=yes", false},
	} {
		has, missing := []RuleCondition{}, []RuleCondition{}
		if tc.has {
			has = append(has, tc.condition)
		} else {
			missing = append(missing, tc.condition)
		}
		rule, err := compileRule("/source", "/target", has, missing)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/source"+tc.query, nil)
		r.Header = tc.headers
		if _, ok := rule.match(r); ok != tc.want {
			t.Fatal(tc, ok)
		}
	}
}
func TestConditionalPathsNeverAdvertisePublicReuseIncludingUnmatchedFallback(t *testing.T) {
	var loads atomic.Int32
	app := testApp(t, Config{BuildID: "rules", Rewrites: []RewriteRule{{Source: "/target", Destination: "/target?selected=blue", Has: []RuleCondition{{Type: HeaderCondition, Key: "X-Variant", Value: "blue"}}}}, Routes: []Route{{Pattern: "/target", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) {
		loads.Add(1)
		_ = c.SetHeader("Cache-Control", "public, max-age=99")
		return Page{Body: ui.Text(c.Query.Get("selected"))}, nil
	}}}})
	for _, header := range []string{"blue", "red", "blue", "red"} {
		w := conditionRequest(app, "/target", http.Header{"X-Variant": []string{header}, "X-Nextgo-Navigate": []string{"1"}})
		if w.Code != 200 || w.Header().Get("X-Nextgo-Prefetch") != "" || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal(w.Code, w.Header())
		}
	}
	if loads.Load() != 4 {
		t.Fatal("conditional pages entered shared snapshots", loads.Load())
	}
}
func TestRuleDestinationsAreBoundedEscapedAndOriginsPinned(t *testing.T) {
	rule, err := compileRule("/in/*tail", "https://upstream.test:8443/root/*tail?label=:name", []RuleCondition{{Type: HeaderCondition, Key: "X-Name", Value: `(?<name>.*)`}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/in/a%20b/c?old=1", nil)
	r.Header.Set("X-Name", "x&admin=yes")
	params, ok := rule.match(r)
	if !ok {
		t.Fatal("no match")
	}
	target, err := rule.destination(params, r.URL)
	if err != nil || target.Scheme != "https" || target.Host != "upstream.test:8443" || target.EscapedPath() != "/root/a%20b/c" || target.Query().Get("label") != "x&admin=yes" || target.Query().Has("admin") {
		t.Fatal(target, err)
	}
	for _, value := range []string{".", "..", "//elsewhere.test", "x/y", "x\\y"} {
		one, err := compileRule("/in", "/target/:name", []RuleCondition{{Type: HeaderCondition, Key: "X-Name", Value: `(?<name>.*)`}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := one.destination(map[string]string{"name": value}, &url.URL{}); err == nil {
			t.Fatal("unsafe path capture accepted", value)
		}
	}
	redirect := testApp(t, Config{BasePath: "/base", Redirects: []RedirectRule{{Source: "/old/:id", Destination: "/new/:id"}}})
	w := request(redirect, "/base/old/a%3Fb?q=a%26b", false)
	if w.Code != 307 || w.Header().Get("Location") != "/base/new/a%3Fb?q=a%26b" {
		t.Fatal(w.Code, w.Header())
	}
	external := testApp(t, Config{Redirects: []RedirectRule{{Source: "/old/:id", Destination: "https://other.test/new/:id"}}})
	w = request(external, "/old/one?q=two", false)
	if w.Header().Get("Location") != "https://other.test/new/one?q=two" {
		t.Fatal(w.Header())
	}
}
func TestRuleStartupRejectsInvalidPoliciesAndStaticExportFailsClosed(t *testing.T) {
	for _, rule := range []RewriteRule{
		{Source: "/x", Destination: "//untrusted.test/x"}, {Source: "/x", Destination: "https://user:private@host.test/x"}, {Source: "/x", Destination: "https://:host/x"}, {Source: "/x", Destination: "/target/:missing"}, {Source: "/x", Destination: "/../escape"}, {Source: "/x", Destination: "/target", Has: []RuleCondition{{Type: HeaderCondition, Key: "Bad\nName"}}}, {Source: "/x", Destination: "/target", Has: []RuleCondition{{Type: HeaderCondition, Key: "X", Value: "["}}}, {Source: "/x/:id", Destination: "/target", Has: []RuleCondition{{Type: HeaderCondition, Key: "X", Value: "(?<id>.*)"}}}, {Source: "/x", Destination: "/target", Has: []RuleCondition{{Type: "unknown", Key: "x"}}}, {Source: "/x", Destination: "/target#fragment"},
	} {
		if _, err := New(Config{Rewrites: []RewriteRule{rule}}); err == nil {
			t.Fatal("invalid rule accepted", rule.Source, rule.Destination)
		}
	}
	for _, redirects := range [][]RedirectRule{{{Source: "/x/:id", Destination: "/x/:id?forever=1"}}, {{Source: "/a", Destination: "/b"}, {Source: "/b", Destination: "/a"}}} {
		if _, err := New(Config{Redirects: redirects}); err == nil {
			t.Fatal("unconditional redirect cycle accepted")
		}
	}
	for _, external := range []bool{false, true} {
		destination := "/target"
		conditions := []RuleCondition{{Type: QueryCondition, Key: "x"}}
		if external {
			destination = "https://upstream.test/target"
			conditions = nil
		}
		app := testApp(t, Config{Rewrites: []RewriteRule{{Source: "/alias", Destination: destination, Has: conditions}}})
		if _, err := app.StaticExportConfig(); err == nil {
			t.Fatal("static export discarded runtime rules")
		}
	}
}
func TestRuleOwnershipDigestAndEndpointManifest(t *testing.T) {
	app := testApp(t, Config{BuildID: "same", Rewrites: []RewriteRule{{Source: "/proxy/:id", Destination: "https://private-backend.test/:id", Has: []RuleCondition{{Type: CookieCondition, Key: "private-cookie", Value: "private-value"}}}}, Routes: []Route{{Pattern: "/", Page: static("home")}}})
	plain := testApp(t, Config{BuildID: "same", Routes: []Route{{Pattern: "/", Page: static("home")}}})
	if app.configurationID == plain.configurationID {
		t.Fatal("rules omitted from compatibility identity")
	}
	html := request(app, "/", false).Body.String()
	if strings.Contains(html, "private-backend") || strings.Contains(html, "private-cookie") || strings.Contains(html, "private-value") {
		t.Fatal("server rule details exposed")
	}
	var manifest navigation.Manifest
	if err := json.Unmarshal([]byte(app.navigationJSON), &manifest); err != nil || !manifest.IsEndpoint("/proxy/a", "/proxy/a") {
		t.Fatal(manifest, err)
	}
}
func TestLoadingFallbackResolvesLocalConditionalRewrite(t *testing.T) {
	app := testApp(t, Config{Rewrites: []RewriteRule{{Source: "/alias/:id", Destination: "/target/:id", Has: []RuleCondition{{Type: QueryCondition, Key: "mode", Value: "yes"}}}}, Routes: []Route{{Pattern: "/target/:id", Page: static("page"), Loading: func(c *Context) (ui.Node, error) { return ui.Text("Loading " + c.Params["id"]), nil }}}})
	response := request(app, "/_nextgo/loading?path="+url.QueryEscape("/alias/one?mode=yes"), false)
	if response.Code != 200 || response.Body.String() != "Loading one" {
		t.Fatal(response.Code, response.Body.String())
	}
}
func FuzzResolvedRuleOriginAndMount(f *testing.F) {
	for _, seed := range []string{"hello", "a b", "..", "//attacker.test", "x?y#z", "%2F", "\x00", "日本語"} {
		f.Add(seed)
	}
	rule, _ := compileRule("/source/:id", "https://pinned.test:8443/destination/:id?q=:id", nil, nil)
	f.Fuzz(func(t *testing.T, value string) {
		if len(value) > 8192 {
			return
		}
		target, err := rule.destination(map[string]string{"id": value}, &url.URL{})
		if err != nil {
			return
		}
		if target.Host != "pinned.test:8443" || target.Scheme != "https" || !strings.HasPrefix(target.EscapedPath(), "/destination/") {
			t.Fatal(target)
		}
		if target.Query().Get("q") != value {
			t.Fatal("query escaped incorrectly", target)
		}
	})
}
