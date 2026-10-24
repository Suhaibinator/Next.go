package nextgo

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestTrailingSlashRoutesAndControls(t *testing.T) {
	for _, base := range []string{"", "/docs"} {
		a := testApp(t, Config{BasePath: base, TrailingSlash: true, Redirects: []RedirectRule{{Source: "/old/:id", Destination: "/posts/:id?from=old"}}, Rewrites: []RewriteRule{{Source: "/alias/:id", Destination: "/posts/:id/"}}, Routes: []Route{
			{Pattern: "/", Page: static("home")},
			{Pattern: "/posts/:id", Page: func(c *Context) (Page, error) {
				if strings.HasSuffix(c.Request.URL.Path, "/") {
					t.Error("page path is not logical", c.Request.URL.Path)
				}
				return Page{Title: "Post", Body: ui.Text(c.Params["id"] + ":" + c.Query.Get("q"))}, nil
			}, Loading: func(c *Context) (ui.Node, error) { return ui.Text("loading " + c.Params["id"]), nil }},
			{Pattern: "/file.txt", Page: static("file")}, {Pattern: "/.well-known/token", Page: static("token")},
			{Pattern: "/api/write", API: func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/write" {
					t.Error("API path is not logical", r.URL.Path)
				}
				data, _ := io.ReadAll(r.Body)
				_, _ = w.Write(data)
			}},
			{Pattern: "/redirect", Page: func(c *Context) (Page, error) { return Page{}, c.Redirect("/posts/saved", 303) }},
		}})
		for path, want := range map[string]string{"/posts/first?q=go": "/posts/first/?q=go", "/posts/a%3Fb": "/posts/a%3Fb/", "/file.txt/": "/file.txt", "/.well-known/token/": "/.well-known/token", "/old/first": "/posts/first/?from=old", "/redirect/": "/posts/saved/"} {
			w := request(a, base+path, false)
			if w.Header().Get("Location") != base+want {
				t.Fatalf("%s redirect: %d %q want %q", base+path, w.Code, w.Header().Get("Location"), base+want)
			}
		}
		if base != "" {
			w := request(a, base, false)
			if w.Code != 308 || w.Header().Get("Location") != base+"/" {
				t.Fatal("mounted root", w.Code, w.Header())
			}
		}
		for _, path := range []string{"/", "/posts/first/", "/file.txt", "/.well-known/token"} {
			if w := request(a, base+path, false); w.Code != 200 {
				t.Fatalf("%s status %d", path, w.Code)
			}
		}
		w := request(a, base+"/alias/first/?q=go", true)
		var data Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data.URL != base+"/alias/first/?q=go" || data.HTML != "first:go" {
			t.Fatalf("rewrite envelope: %+v %v", data, err)
		}
		w = request(a, base+"/_nextgo/loading?path="+strings.ReplaceAll(base+"/posts/first/", "/", "%2F"), false)
		if w.Code != 200 || w.Body.String() != "loading first" {
			t.Fatalf("loading: %d %s", w.Code, w.Body.String())
		}
		r := httptest.NewRequest("POST", base+"/api/write/", strings.NewReader("preserved payload"))
		w = httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != "preserved payload" {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, path := range []string{"/posts//first/", "/posts/a%2Fb/", "/posts/first//"} {
			if w := request(a, base+path, false); w.Code == 200 {
				t.Fatal("malformed path served", path)
			}
		}
	}
}
func TestSlashPolicyMiddlewareAndHTTPSubrouters(t *testing.T) {
	a := testApp(t, Config{TrailingSlash: true, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/private" {
				http.Error(w, "requires authentication", 401)
				return
			}
			next.ServeHTTP(w, r)
		})
	}}, Handlers: map[string]http.Handler{"/files/": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, r.URL.Path) })}, Routes: []Route{{Pattern: "/private", Page: static("private")}}})
	for _, path := range []string{"/private", "/private/"} {
		if w := request(a, path, false); w.Code != 401 {
			t.Fatalf("middleware bypass %s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/files/", "/files/folder/"} {
		w := request(a, path, false)
		if w.Code != 200 || w.Body.String() != path {
			t.Fatalf("subrouter path: %d %s", w.Code, w.Body.String())
		}
	}
}
func TestSlashPolicyNamespacesSharedPageCache(t *testing.T) {
	store := testApp(t, Config{}).config.Cache
	for _, slash := range []bool{false, true} {
		a := testApp(t, Config{BuildID: "same", Cache: store, TrailingSlash: slash, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) { return Page{Body: ui.Text(MustURL(c, "/about"))}, nil }}}})
		w := request(a, "/", true)
		var data Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		want := "/about"
		if slash {
			want += "/"
		}
		if data.HTML != want {
			t.Fatalf("cache reuse across slash policy: %q", data.HTML)
		}
	}
}

func TestSlashRewritePreservesEscapesAndQuery(t *testing.T) {
	for _, slash := range []bool{false, true} {
		for _, destination := range []string{"/posts/:id/", "/posts/:id/?"} {
			a := testApp(t, Config{TrailingSlash: slash, Rewrites: []RewriteRule{{Source: "/alias/:id", Destination: destination}}, Routes: []Route{{Pattern: "/posts/:id", Page: func(c *Context) (Page, error) {
				return Page{Body: ui.Text(c.Request.URL.RequestURI())}, nil
			}}}})
			suffix := ""
			if slash {
				suffix = "/"
			}
			w := request(a, "/alias/a%3Fb"+suffix+"?q=one", true)
			var data Envelope
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			want := "/posts/a%3Fb?q=one"
			if strings.HasSuffix(destination, "?") {
				want = "/posts/a%3Fb?"
			}
			if data.HTML != want {
				t.Fatalf("%v %s: %q want %q", slash, destination, data.HTML, want)
			}
		}
	}
}
