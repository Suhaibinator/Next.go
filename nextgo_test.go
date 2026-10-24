package nextgo

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Suhaibinator/Next.go/ui"
)

func testApp(t *testing.T, c Config) *App {
	t.Helper()
	c.Logger = log.New(io.Discard, "", 0)
	a, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func request(a *App, path string, nav bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if nav {
		r.Header.Set("X-Nextgo-Navigate", "1")
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func static(text string) PageFunc {
	return func(*Context) (Page, error) { return Page{Title: text, Body: ui.Text(text)}, nil }
}

func TestRoutingAndLayouts(t *testing.T) {
	wrap := func(tag string) LayoutFunc {
		return func(_ *Context, n ui.Node) (ui.Node, error) { return ui.El(tag, nil, n), nil }
	}
	a := testApp(t, Config{BuildID: "test", Routes: []Route{
		{Pattern: "/posts/:slug", Page: func(c *Context) (Page, error) {
			return Page{Title: "Post", Body: ui.Text(c.Params["slug"] + ":" + c.Query.Get("q"))}, nil
		}, Layouts: []LayoutFunc{wrap("main"), wrap("article")}},
		{Pattern: "/posts/new", Page: static("static wins")},
		{Pattern: "/", Page: static("home")},
	}})
	for _, tc := range []struct {
		path, want string
		status     int
	}{{"/", "home", 200}, {"/posts/new", "static wins", 200}, {"/posts/hello%20world?q=Go", "<main><article>hello world:Go</article></main>", 200}, {"/posts/a%2Fb", "Page not found", 404}, {"/posts/a/extra", "Page not found", 404}} {
		t.Run(tc.path, func(t *testing.T) {
			w := request(a, tc.path, false)
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
		})
	}
	if got := request(a, "/posts/new/?q=x", false); got.Code != 308 || got.Header().Get("Location") != "/posts/new?q=x" {
		t.Fatalf("trailing slash: %v", got)
	}
}

func TestRouteValidation(t *testing.T) {
	for _, routes := range [][]Route{
		{{Pattern: "/:id", Page: static("a")}, {Pattern: "/:name", Page: static("b")}},
		{{Pattern: "/:id/:id", Page: static("a")}}, {{Pattern: "/:9id", Page: static("a")}}, {{Pattern: "/a/", Page: static("a")}}, {{Pattern: "/a//b", Page: static("a")}}, {{Pattern: "//a", Page: static("a")}}, {{Pattern: "/empty"}},
	} {
		if _, err := New(Config{Routes: routes}); err == nil {
			t.Fatalf("accepted invalid routes: %+v", routes)
		}
	}
}

func TestStaticRoutePrecedenceWithMixedDepths(t *testing.T) {
	// A shorter route used to create a comparator cycle with /a/:id and /b/c.
	// Check every registration order so unstable ordering cannot hide an
	// overlapping static route behind a dynamic match.
	routes := []Route{
		{Pattern: "/", Page: static("root")},
		{Pattern: "/b", Page: static("short")},
		{Pattern: "/a/:id", Page: static("dynamic id")},
		{Pattern: "/b/c", Page: static("static c")},
		{Pattern: "/a/new", Page: static("static new")},
		{Pattern: "/:section/new", Page: static("dynamic section")},
	}
	var permutations func(int)
	permutations = func(index int) {
		if index != len(routes) {
			for i := index; i < len(routes); i++ {
				routes[index], routes[i] = routes[i], routes[index]
				permutations(index + 1)
				routes[index], routes[i] = routes[i], routes[index]
			}
			return
		}
		a := testApp(t, Config{Routes: routes})
		for i, pattern := range []string{"/", "/b", "/a/new", "/b/c", "/a/:id", "/:section/new"} {
			if a.routes[i].Pattern != pattern {
				t.Fatalf("registration order %+v: route %d is %q, want %q", routes, i, a.routes[i].Pattern, pattern)
			}
		}
		for _, tc := range []struct{ path, want string }{
			{"/", "root"}, {"/b", "short"}, {"/a/new", "static new"},
			{"/b/c", "static c"}, {"/a/other", "dynamic id"}, {"/b/new", "dynamic section"},
		} {
			var envelope Envelope
			w := request(a, tc.path, true)
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Code != http.StatusOK || envelope.HTML != tc.want {
				t.Fatalf("registration order %+v: %s got %d %q (%v); want %q", routes, tc.path, w.Code, envelope.HTML, err, tc.want)
			}
		}
	}
	permutations(0)
}

func TestCanonicalPathsPreserveEscapesAndStayLocal(t *testing.T) {
	a := testApp(t, Config{Routes: []Route{{Pattern: "/posts/:slug", Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text(c.Params["slug"])}, nil
	}}}})
	for _, tc := range []struct{ path, location string }{
		{"/posts/a%3Fb/?q=yes", "/posts/a%3Fb?q=yes"},
		{"/posts/a%23b/", "/posts/a%23b"},
		{"/posts/a%25b/", "/posts/a%25b"},
		{"//other.example/", "/other.example"},
		{"///", "/"},
	} {
		w := request(a, tc.path, false)
		if w.Code != http.StatusPermanentRedirect || w.Header().Get("Location") != tc.location {
			t.Errorf("%s: got status %d, Location %q; want %q", tc.path, w.Code, w.Header().Get("Location"), tc.location)
		}
	}
	for _, path := range []string{"/posts/a%2F", "//posts/a", "/posts//a"} {
		w := request(a, path, false)
		if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
			t.Errorf("%s should not match or redirect: %d %q", path, w.Code, w.Header().Get("Location"))
		}
	}
	w := request(a, "/posts/a%3Fb?q=Go%26WASM", true)
	var data Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != http.StatusOK || data.HTML != "a?b" || data.URL != "/posts/a%3Fb?q=Go%26WASM" {
		t.Fatalf("escaped parameter lost: %d %+v %v", w.Code, data, err)
	}
}

func TestFragmentAndMetadata(t *testing.T) {
	a := testApp(t, Config{BuildID: "build-123", Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Title: `<Go>`, Description: `"quoted"`, Meta: map[string]string{"author": "Go & you"}, Body: ui.El("p", nil, ui.Text("before wasm"))}, nil
	}}}})
	full := request(a, "/", false)
	for _, s := range []string{"<!doctype html>", "before wasm", "&lt;Go&gt;", "&#34;quoted&#34;", "Go &amp; you", "/_nextgo/build-123/boot.js"} {
		if !strings.Contains(full.Body.String(), s) {
			t.Fatalf("missing %s in %s", s, full.Body)
		}
	}
	nav := request(a, "/", true)
	var data Envelope
	if err := json.Unmarshal(nav.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.HTML != "<p>before wasm</p>" || data.Title != "<Go>" || data.Build != "build-123" || data.URL != "/" || data.Status != 200 || data.Meta["author"] != "Go & you" {
		t.Fatalf("bad envelope: %+v", data)
	}
	vary := strings.Join(nav.Header().Values("Vary"), ",")
	if !strings.Contains(vary, "X-Nextgo-Navigate") || !strings.Contains(vary, "Accept-Encoding") || nav.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(nav.Header())
	}
	r := httptest.NewRequest("HEAD", "/", nil)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("HEAD must have no body")
	}
	r = httptest.NewRequest("POST", "/", nil)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal("page methods")
	}
}

func TestNotFoundAndErrors(t *testing.T) {
	a := testApp(t, Config{NotFound: static("custom missing"), Routes: []Route{
		{Pattern: "/missing", Page: func(*Context) (Page, error) { return Page{}, ErrNotFound }},
		{Pattern: "/error", Page: func(*Context) (Page, error) { return Page{}, errors.New("secret database detail") }},
		{Pattern: "/panic", Page: func(*Context) (Page, error) { panic("secret panic") }},
		{Pattern: "/layout-error", Page: static("ok"), Layouts: []LayoutFunc{func(*Context, ui.Node) (ui.Node, error) { return nil, errors.New("secret") }}},
		{Pattern: "/bad-html", Page: func(*Context) (Page, error) {
			return Page{Body: ui.El("a", ui.Attrs{"href": "javascript:evil()"})}, nil
		}},
		{Pattern: "/render-panic", Page: func(*Context) (Page, error) { return Page{Body: panicNode{}}, nil }},
	}})
	for _, path := range []string{"/missing", "/unknown", "/error", "/panic", "/layout-error", "/bad-html", "/render-panic"} {
		for _, nav := range []bool{false, true} {
			w := request(a, path, nav)
			status := 500
			want := "Something went wrong"
			if path == "/missing" || path == "/unknown" {
				status = 404
				want = "custom missing"
			}
			if w.Code != status || !strings.Contains(w.Body.String(), want) || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("%s: %d %s", path, w.Code, w.Body)
			}
		}
	}
}

type panicNode struct{}

func (panicNode) Render(io.Writer) error { panic("secret render panic") }

func TestAPIAndAssets(t *testing.T) {
	a := testApp(t, Config{Assets: fstest.MapFS{"_nextgo/id/app.wasm": &fstest.MapFile{Data: []byte("wasm")}, "public/test.txt": &fstest.MapFile{Data: []byte("asset")}}, Routes: []Route{{Pattern: "/api/:id", API: func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": r.PathValue("id")})
	}}}})
	r := httptest.NewRequest("POST", "/api/test", nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Body.String() != "{\"id\":\"test\"}\n" {
		t.Fatal(w.Body)
	}
	asset := request(a, "/assets/test.txt", false)
	if asset.Code != 200 || asset.Body.String() != "asset" {
		t.Fatal(asset)
	}
	wasm := request(a, "/_nextgo/id/app.wasm", false)
	if wasm.Header().Get("Content-Type") != "application/wasm" || !strings.Contains(wasm.Header().Get("Cache-Control"), "immutable") {
		t.Fatal(wasm.Header())
	}
}

func TestErrorBoundaryLayoutFailureLogsItsActualCause(t *testing.T) {
	var output bytes.Buffer
	app, err := New(Config{Logger: log.New(&output, "", 0), Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{}, errors.New("page failed") }, Error: func(*Context, error) (Page, error) { return Page{Body: ui.Text("boundary")}, nil }, Layouts: []LayoutFunc{func(*Context, ui.Node) (ui.Node, error) { return nil, errors.New("layout failed") }}}}})
	if err != nil {
		t.Fatal(err)
	}
	response := request(app, "/", false)
	if response.Code != 500 || !strings.Contains(output.String(), "error boundary failed: layout failed") || strings.Contains(response.Body.String(), "layout failed") {
		t.Fatalf("status=%d logs=%s body=%s", response.Code, output.String(), response.Body.String())
	}
}

func TestNavigationProtocolSeparatesPagesFromJSONEndpoints(t *testing.T) {
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: static("page")}, {Pattern: "/api/echo", API: func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"html":"not a framework page"}`)
	}}}})
	page := request(app, "/", true)
	var envelope Envelope
	if err := json.Unmarshal(page.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if page.Header().Get("X-Nextgo-Protocol") != "3" || envelope.Version != NavigationProtocolVersion {
		t.Fatalf("page protocol: %v %+v", page.Header(), envelope)
	}
	endpoint := request(app, "/api/echo", true)
	if endpoint.Header().Get("X-Nextgo-Protocol") != "" {
		t.Fatal("JSON API advertised page protocol")
	}
}
