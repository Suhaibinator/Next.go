package nextgo

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func rootTestRoute(pattern, id string) Route {
	return Route{Pattern: pattern, Page: static("root body"), Layouts: []LayoutFunc{func(_ *Context, body ui.Node) (ui.Node, error) { return body, nil }}, LayoutIDs: []string{id}}
}

func TestRootLayoutsBindDocumentsAndNavigationOutsideCache(t *testing.T) {
	a, b := rootTestRoute("/a", "private/import/marketing"), rootTestRoute("/b", "private/import/dashboard")
	loads := 0
	a.Cache = &CachePolicy{TTL: time.Minute}
	a.Page = func(*Context) (Page, error) { loads++; return Page{Body: ui.Text("cached")}, nil }
	stream := rootTestRoute("/stream", b.LayoutIDs[0])
	stream.Page = func(*Context) (Page, error) { return Page{Stream: true, Body: ui.Text("stream body")}, nil }
	app := testApp(t, Config{BuildID: "same", Routes: []Route{a, b, stream}, Rewrites: []RewriteRule{{Source: "/alias", Destination: "/b"}}})
	var manifest navigation.Manifest
	if err := json.Unmarshal([]byte(app.navigationJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if !app.multipleRoots || strings.Contains(app.navigationJSON, "private/import") {
		t.Fatal("roots missing or imports disclosed", app.navigationJSON)
	}
	for _, path := range []string{"/a", "/a", "/b", "/alias", "/stream"} {
		want := routeRootIdentity(b)
		if path == "/a" {
			want = routeRootIdentity(a)
		}
		document := request(app, path, false)
		if document.Code != 200 || !strings.Contains(document.Body.String(), `data-nextgo-root="`+want+`"`) {
			t.Fatal(path, document.Code, document.Body.String())
		}
		for _, streaming := range []bool{false, true} {
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("X-Nextgo-Navigate", "1")
			if streaming {
				req.Header.Set(navigation.StreamHeader, "1")
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, req)
			body := response.Body.Bytes()
			if strings.Contains(response.Header().Get("Content-Type"), navigation.StreamMediaType) {
				line := strings.Split(response.Body.String(), "\n")[0]
				frame, err := navigation.DecodeStreamFrame([]byte(line))
				if err != nil {
					t.Fatal(err)
				}
				body = frame.Page
			}
			var envelope Envelope
			if json.Unmarshal(body, &envelope) != nil || envelope.Root != want {
				t.Fatal(path, string(body))
			}
		}
	}
	// Different Host values in the helpers produce separate cache keys; each
	// repeated request kind must still reuse its materialized page.
	if loads > 2 {
		t.Fatal("cache did not reuse the page", loads)
	}
	if manifest.SameRoot("/b", routeRootIdentity(a)) || !manifest.SameRoot("/stream", routeRootIdentity(b)) {
		t.Fatal("root classification")
	}
}

func TestSingleRootAndTemplatesPreserveDocumentNavigation(t *testing.T) {
	a, b := rootTestRoute("/a", "shared"), rootTestRoute("/b", "shared")
	b.Layouts = append([]LayoutFunc{b.Layouts[0]}, b.Layouts...)
	b.LayoutIDs = []string{"", "shared"}
	app := testApp(t, Config{Routes: []Route{a, b}})
	if app.multipleRoots || strings.Contains(app.navigationJSON, `"root"`) || strings.Contains(request(app, "/a", false).Body.String(), "data-nextgo-root") {
		t.Fatal("single shared root changed wire format")
	}
	if routeRootIdentity(a) != routeRootIdentity(b) {
		t.Fatal("template became document root")
	}
	implicit := testApp(t, Config{Routes: []Route{a, {Pattern: "/plain", Page: static("plain")}}})
	if !implicit.multipleRoots {
		t.Fatal("implicit root merged with persistent layout")
	}
	if strings.Contains(request(implicit, "/missing", false).Body.String(), `data-nextgo-root="`+routeRootIdentity(a)+`"`) {
		t.Fatal("global not-found borrowed a route root")
	}
}
