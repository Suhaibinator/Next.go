package nextgo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestMountedRoutesURLsAssetsAndControls(t *testing.T) {
	api := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/hello" || r.RequestURI != "/docs/api/hello" {
			t.Error("handler URL contract", r.URL, r.RequestURI)
		}
		_, _ = io.WriteString(w, "api")
	}
	router := actions.New(actions.Options{})
	router.Handle("save", func(*actions.Context) (actions.Result, error) { return actions.Result{Redirect: "/posts/saved"}, nil })
	a := testApp(t, Config{BasePath: "/docs", AssetPrefix: "/cdn", BuildID: "test", Actions: router, Assets: fstest.MapFS{"public/style.css": {Data: []byte("body{}")}, "_nextgo/test/app.wasm": {Data: []byte("wasm")}},
		Redirects: []RedirectRule{{Source: "/old/:slug", Destination: "/posts/:slug?from=old"}}, Rewrites: []RewriteRule{{Source: "/alias/:slug", Destination: "/posts/:slug"}},
		Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
			return Page{Body: ui.El("a", ui.Attrs{"href": MustURL(c, "/posts/first?q=go#item")}, ui.Text("Useful HTML")), Resources: []assets.Resource{assets.Stylesheet("/assets/page.css")}}, nil
		}},
			{Pattern: "/posts/:slug", Page: func(c *Context) (Page, error) {
				return Page{Body: ui.Text(c.Params["slug"] + ":" + c.Query.Get("q"))}, nil
			}},
			{Pattern: "/api/hello", API: api}, {Pattern: "/redirect", Page: func(c *Context) (Page, error) { return Page{}, c.Redirect("/posts/saved", 303) }},
		}})
	for _, path := range []string{"/", "/post", "/docs-other", "/%64ocs", "/_nextgo/test/app.wasm"} {
		if w := request(a, path, false); w.Code != 404 {
			t.Fatalf("outside mount %s: %d", path, w.Code)
		}
	}
	html := request(a, "/docs", false)
	for _, text := range []string{`href="/docs/posts/first?q=go#item"`, `src="/cdn/_nextgo/test/wasm_exec.js"`, `href="/cdn/assets/style.css"`, `href="/cdn/assets/page.css"`, `data-nextgo-basepath="/docs"`} {
		if !strings.Contains(html.Body.String(), text) {
			t.Fatalf("missing %s: %s", text, html.Body.String())
		}
	}
	for path, target := range map[string]string{"/docs/": "/docs", "/docs/posts/a%3Fb/?q=go": "/docs/posts/a%3Fb?q=go", "/docs/old/first": "/docs/posts/first?from=old", "/docs/redirect": "/docs/posts/saved"} {
		if w := request(a, path, false); w.Header().Get("Location") != target {
			t.Fatalf("redirect %s: %d %s", path, w.Code, w.Header().Get("Location"))
		}
	}
	nav := request(a, "/docs/alias/first?q=go", true)
	var envelope Envelope
	if err := json.Unmarshal(nav.Body.Bytes(), &envelope); err != nil || envelope.URL != "/docs/alias/first?q=go" || envelope.HTML != "first:go" {
		t.Fatalf("mounted rewrite: %+v %v", envelope, err)
	}
	if w := request(a, "/docs/api/hello", false); w.Body.String() != "api" {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, path := range []string{"/cdn/_nextgo/test/app.wasm", "/docs/_nextgo/test/app.wasm"} {
		w := request(a, path, false)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("asset %s: %d %v", path, w.Code, w.Header())
		}
	}
	if w := request(a, "/cdn/_nextgo/missing/app.wasm", false); strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("missing asset cached immutably")
	}
	req := httptest.NewRequest("POST", "http://example.com/docs/_nextgo/actions/save", strings.NewReader(""))
	req.Header.Set("Origin", "http://example.com")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr := httptest.NewRecorder()
	a.ServeHTTP(rr, req)
	if rr.Code != 303 || rr.Header().Get("Location") != "/docs/posts/saved" {
		t.Fatalf("action redirect: %d %s", rr.Code, rr.Header().Get("Location"))
	}
	assetOnly := httptest.NewRecorder()
	a.AssetHandler().ServeHTTP(assetOnly, httptest.NewRequest("GET", "/api/hello", nil))
	if assetOnly.Code != 404 {
		t.Fatal("asset handler exposed application")
	}
}
func TestMountedPublicCachesAreNamespaced(t *testing.T) {
	// A shared application store must not reuse HTML from a different deployment.
	store := testApp(t, Config{}).config.Cache
	for _, base := range []string{"/one", "/two"} {
		a := testApp(t, Config{BasePath: base, BuildID: "same", Cache: store, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) { return Page{Body: ui.Text(MustURL(c, "/about"))}, nil }}}})
		if w := request(a, base, false); !strings.Contains(w.Body.String(), base+"/about") {
			t.Fatal("cross-deployment cache reuse", w.Body.String())
		}
	}
	if got, err := URL(context.Background(), "/about"); err != nil || got != "/about" {
		t.Fatal(got, err)
	}
}

func TestCDNWithSamePathKeepsAssetsPublicAndServicesPrivate(t *testing.T) {
	for _, base := range []string{"", "/docs"} {
		router := actions.New(actions.Options{})
		router.Handle("save", func(*actions.Context) (actions.Result, error) { return actions.Result{Redirect: "/saved"}, nil })
		a := testApp(t, Config{BasePath: base, AssetPrefix: "https://cdn.example.com" + base, BuildID: "test", Actions: router, Images: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "image service") }), Assets: fstest.MapFS{"_nextgo/test/app.wasm": {Data: []byte("wasm")}}, Routes: []Route{{Pattern: "/", Page: static("page")}}})
		w := request(a, base+"/_nextgo/test/app.wasm", false)
		if w.Code != 200 || w.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Fatalf("same-path CDN: %d %v", w.Code, w.Header())
		}
		if w := request(a, base+"/_nextgo/image", false); w.Code != 200 || w.Body.String() != "image service" {
			t.Fatalf("image intercepted as public asset: %d", w.Code)
		}
		r := httptest.NewRequest("POST", "http://example.com"+base+"/_nextgo/actions/save", strings.NewReader(""))
		r.Header.Set("Origin", "http://example.com")
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 303 || w.Header().Get("Location") != base+"/saved" {
			t.Fatalf("action intercepted as public asset: %d %v", w.Code, w.Header())
		}
		for _, path := range []string{"/_nextgo/loading", "/_nextgo/image", "/_nextgo/actions/save", "/_nextgo/events"} {
			w = httptest.NewRecorder()
			a.AssetHandler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 404 {
				t.Fatalf("asset-only handler exposed %s: %d", path, w.Code)
			}
		}
	}
}
