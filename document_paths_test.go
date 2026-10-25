package nextgo

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestDocumentPathsValidateFreezeAndBound(t *testing.T) {
	for _, prefix := range []string{"", "/", "blog", "/blog/", "//blog", "/a//b", "/a/../b", "/a/./b", "/a?x", "/a#x", "/a\\b", "/a%20b", "/a/:id", "/a/*tail", "/café", "/a\n", "/" + strings.Repeat("a", 8192)} {
		if _, err := New(Config{DocumentPaths: []string{prefix}}); err == nil {
			t.Errorf("invalid document prefix accepted: %q", prefix)
		}
	}
	if _, err := New(Config{DocumentPaths: make([]string, maxDocumentPaths+1)}); err == nil {
		t.Fatal("document path count bound ignored")
	}
	large := strings.Repeat("a", 8190)
	if _, err := New(Config{DocumentPaths: []string{"/" + large, "/b" + large, "/c" + large, "/d" + large, "/e" + large, "/f" + large, "/g" + large, "/h" + large, "/i" + large}}); err == nil {
		t.Fatal("aggregate document path bound ignored")
	}
	input := []string{"/docs/help", "/blog", "/blog"}
	a := testApp(t, Config{DocumentPaths: input})
	input[0], input[1] = "/changed", "/also-changed"
	if !reflect.DeepEqual(a.config.DocumentPaths, []string{"/blog", "/docs/help"}) {
		t.Fatal("document prefixes not owned/canonical", a.config.DocumentPaths)
	}
	var manifest navigation.Manifest
	if err := json.Unmarshal([]byte(a.navigationJSON), &manifest); err != nil || !reflect.DeepEqual(manifest.Documents, []string{"/blog/*nextgoDocument?", "/docs/help/*nextgoDocument?"}) {
		t.Fatal("document subtree patterns not frozen", manifest.Documents, err)
	}
	b := testApp(t, Config{DocumentPaths: []string{"/blog", "/docs/help"}})
	if a.configurationID != b.configurationID || a.prerenderShape != b.prerenderShape {
		t.Fatal("equivalent document boundaries changed compatibility identity")
	}
	c := testApp(t, Config{DocumentPaths: []string{"/other"}})
	if a.configurationID == c.configurationID || a.prerenderShape == c.prerenderShape {
		t.Fatal("document boundaries omitted from compatibility/cache identity")
	}
}

func TestDocumentPathsAugmentExternalRulesWithoutChangingNativeDispatch(t *testing.T) {
	a := testApp(t, Config{BasePath: "/app", DocumentPaths: []string{"/blog"},
		Rewrites: []RewriteRule{{Source: "/external/:id", Destination: "https://example.test/:id"}},
		Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
			return Page{Body: ui.El("a", ui.Attrs{"href": MustURL(c, "/blog/a?q=one#part")}, ui.Text("Open blog"))}, nil
		}}, {Pattern: "/blog/:slug", Page: static("native blog")},
			{Pattern: "/api/health", API: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("healthy")) }}},
	})
	var manifest navigation.Manifest
	if err := json.Unmarshal([]byte(a.navigationJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/blog", "/blog/a", "/external/a", "/api/health"} {
		if !manifest.IsEndpoint(path, path) {
			t.Fatal("document/API path interceptable", path, manifest.Documents)
		}
	}
	if manifest.IsEndpoint("/", "/") || manifest.IsEndpoint("/blogger", "/blogger") {
		t.Fatal("ordinary page ownership changed")
	}
	if w := request(a, "/app", false); w.Code != 200 || !strings.Contains(w.Body.String(), `href="/app/blog/a?q=one#part"`) {
		t.Fatal("document declaration changed native anchor", w.Code, w.Body.String())
	}
	if w := request(a, "/app/blog/a?q=one", false); w.Code != 200 || !strings.Contains(w.Body.String(), "native blog") {
		t.Fatal("document declaration changed native routing", w.Code, w.Body.String())
	}
	if w := request(a, "/app/api/health", true); w.Code != 200 || w.Body.String() != "healthy" {
		t.Fatal("document declaration changed API dispatch", w.Code, w.Body.String())
	}
}

func TestDocumentPathsInheritedByLocaleChildren(t *testing.T) {
	a := testApp(t, Config{BasePath: "/shop", DocumentPaths: []string{"/blog"}, Locales: localeConfig(true), Routes: []Route{{Pattern: "/", Page: static("home")}}})
	for _, scope := range a.locales.mounts {
		var manifest navigation.Manifest
		if err := json.Unmarshal([]byte(scope.app.navigationJSON), &manifest); err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"/blog", "/blog/a%20b?q=one#part"} {
			u, err := url.Parse(scope.app.paths.BasePath + suffix)
			if err != nil {
				t.Fatal(err)
			}
			logical, inside := scope.app.paths.Strip(u.EscapedPath())
			decoded, _ := url.PathUnescape(logical)
			if !inside || !manifest.IsEndpoint(logical, decoded) {
				t.Fatal("localized child omitted document ownership", scope.code, suffix, logical)
			}
		}
		if _, inside := (paths.Config{BasePath: scope.app.paths.BasePath}).Strip(strings.Replace(scope.app.paths.BasePath, "/shop", "/%73hop", 1) + "/blog"); inside {
			t.Fatal("encoded global mount became an alias")
		}
	}
}
