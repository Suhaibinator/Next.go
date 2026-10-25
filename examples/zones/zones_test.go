//go:build !js

package zones_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	blog "github.com/Suhaibinator/Next.go/examples/zones/blog/app"
	blogAPI "github.com/Suhaibinator/Next.go/examples/zones/blog/app/api/status"
	blogCache "github.com/Suhaibinator/Next.go/examples/zones/blog/app/cached"
	marketing "github.com/Suhaibinator/Next.go/examples/zones/marketing/app"
	marketingAPI "github.com/Suhaibinator/Next.go/examples/zones/marketing/app/api/status"
	marketingCache "github.com/Suhaibinator/Next.go/examples/zones/marketing/app/cached"
	tenant "github.com/Suhaibinator/Next.go/examples/zones/tenant/app"
	tenantAPI "github.com/Suhaibinator/Next.go/examples/zones/tenant/app/api/record"
	tenantStatus "github.com/Suhaibinator/Next.go/examples/zones/tenant/app/api/status"
	dashboard "github.com/Suhaibinator/Next.go/examples/zones/tenant/app/dashboard"
)

func sample(t *testing.T) *nextgo.Zones {
	t.Helper()
	declarations := []nextgo.ZoneConfig{
		{ID: "marketing", Host: "127.0.0.1:4230", Config: nextgo.Config{BuildID: "marketing-native", Routes: []nextgo.Route{{Pattern: "/", Page: marketing.Page}, {Pattern: "/cached", Page: marketingCache.Page}, {Pattern: "/api/status", API: marketingAPI.Handle}}}, Configure: func(c *nextgo.Config) error { marketing.Configure(c); return nil }},
		{ID: "blog", Host: "127.0.0.1:4230", Config: nextgo.Config{BuildID: "blog-native", BasePath: "/blog", Routes: []nextgo.Route{{Pattern: "/", Page: blog.Page}, {Pattern: "/cached", Page: blogCache.Page}, {Pattern: "/api/status", API: blogAPI.Handle}}}, Configure: func(c *nextgo.Config) error { blog.Configure(c); return nil }},
	}
	for _, name := range []string{"alpha", "beta"} {
		declarations = append(declarations, nextgo.ZoneConfig{ID: name, Tenant: name, Host: name + ".example.test", Config: nextgo.Config{BuildID: "tenant-native", Unauthorized: tenant.Unauthorized, Forbidden: tenant.Forbidden, Routes: []nextgo.Route{{Pattern: "/", Page: tenant.Page}, {Pattern: "/dashboard", Page: dashboard.Page}, {Pattern: "/api/record", API: tenantAPI.Handle}, {Pattern: "/api/status", API: tenantStatus.Handle}}}, Configure: func(c *nextgo.Config) error { tenant.Configure(c); return nil }})
	}
	group, err := nextgo.NewZones(context.Background(), declarations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := group.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return group
}

func request(group http.Handler, method, host, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(body))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "http://"+host)
		r.Header.Set("Accept", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	group.ServeHTTP(w, r)
	return w
}

func TestOrdinaryOwnersMetadataAndCacheInvalidationAreIndependent(t *testing.T) {
	group := sample(t)
	for _, tc := range []struct{ path, owner, base string }{{"/api/status", "marketing", ""}, {"/blog/api/status", "blog", "/blog"}} {
		w := request(group, "GET", "127.0.0.1:4230", tc.path, "", nil)
		var info map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &info) != nil || info["zone"] != tc.owner || info["basePath"] != tc.base {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	first := request(group, "GET", "127.0.0.1:4230", "/cached", "", nil).Body.String()
	blogFirst := request(group, "GET", "127.0.0.1:4230", "/blog/cached", "", nil).Body.String()
	if !strings.Contains(first, "marketing:0") || !strings.Contains(blogFirst, "blog:0") {
		t.Fatal(first, blogFirst)
	}
	action := request(group, "POST", "127.0.0.1:4230", "/_nextgo/actions/preview", "note=public+fixture", nil)
	if action.Code != 200 || !strings.Contains(action.Body.String(), `"zone":"marketing"`) {
		t.Fatal(action.Code, action.Body.String())
	}
	if current := request(group, "GET", "127.0.0.1:4230", "/cached", "", nil).Body.String(); !strings.Contains(current, "marketing:1") {
		t.Fatal(current)
	}
	if current := request(group, "GET", "127.0.0.1:4230", "/blog/cached", "", nil).Body.String(); !strings.Contains(current, "blog:0") {
		t.Fatal("other application's cache changed", current)
	}
	sitemap := request(group, "GET", "127.0.0.1:4230", "/sitemap.xml", "", nil)
	if sitemap.Code != 200 || !strings.Contains(sitemap.Body.String(), "http://127.0.0.1:4230/posts/welcome") {
		t.Fatal(sitemap.Code, sitemap.Body.String())
	}
	if w := request(group, "GET", "127.0.0.1:4230", "/blog/sitemap.xml", "", nil); w.Code != 404 {
		t.Fatal("leaf miss fell through to marketing", w.Code, w.Body.String())
	}
}

func TestTenantMembershipCheckedOnPageAPIAndAction(t *testing.T) {
	group := sample(t)
	for _, path := range []string{"/dashboard", "/api/record"} {
		if w := request(group, "GET", "alpha.example.test", path, "", nil); w.Code != 401 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if w := request(group, "POST", "alpha.example.test", "/_nextgo/actions/preview", "note=hello", nil); w.Code != 401 {
		t.Fatal(w.Code, w.Body.String())
	}
	issued := request(group, "POST", "alpha.example.test", "/_nextgo/actions/assume", "subject=alice", nil)
	if issued.Code != 200 || len(issued.Result().Cookies()) != 1 {
		t.Fatal(issued.Code, issued.Body.String())
	}
	cookie := issued.Result().Cookies()[0]
	for _, path := range []string{"/dashboard", "/api/record"} {
		if w := request(group, "GET", "alpha.example.test", path, "", cookie); w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if w := request(group, "GET", "beta.example.test", path, "", cookie); w.Code != 403 {
			t.Fatal("valid subject gained another tenant", path, w.Code, w.Body.String())
		}
	}
	if w := request(group, "POST", "beta.example.test", "/_nextgo/actions/preview", "note=hello", cookie); w.Code != 403 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request(group, "POST", "alpha.example.test", "/_nextgo/actions/preview", "note=hello", cookie); w.Code != 200 || !strings.Contains(w.Body.String(), `"tenant":"alpha"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	bob := request(group, "POST", "beta.example.test", "/_nextgo/actions/assume", "subject=bob", nil)
	if bob.Code != 200 || len(bob.Result().Cookies()) != 1 {
		t.Fatal(bob.Code, bob.Body.String())
	}
	betaRecord := request(group, "GET", "beta.example.test", "/api/record", "", bob.Result().Cookies()[0])
	if betaRecord.Code != 200 || !strings.Contains(betaRecord.Body.String(), `"tenant":"beta"`) || !strings.Contains(betaRecord.Body.String(), `"revision":0`) {
		t.Fatal("reused tenant source shared another instance's state", betaRecord.Code, betaRecord.Body.String())
	}
	r := httptest.NewRequest("GET", "http://alpha.example.test/api/record?tenant=beta", nil)
	r.Header.Set("X-Forwarded-Host", "beta.example.test")
	r.Header.Set("X-Nextgo-Zone", "beta")
	r.Header.Set("X-User", "alice")
	w := httptest.NewRecorder()
	group.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("untrusted claims became identity", w.Code, w.Body.String())
	}
	status := request(group, "GET", "alpha.example.test", "/api/status?tenant=beta", "", nil)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"tenant":"alpha"`) {
		t.Fatal(status.Code, status.Body.String())
	}
}
