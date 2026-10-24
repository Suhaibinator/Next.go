package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestExportPathSelectionAndFilesystemSafety(t *testing.T) {
	p := &project.Project{Routes: []project.Route{{Pattern: "/"}, {Pattern: "/about"}, {Pattern: "/posts/:slug"}, {Pattern: "/api/health", API: true}}}
	paths, err := exportPaths(p, "")
	if err != nil || !reflect.DeepEqual(paths, []string{"/", "/about"}) {
		t.Fatalf("static defaults: %v, %v", paths, err)
	}
	paths, err = exportPaths(p, "/posts/hello,/about,/about")
	if err != nil || !reflect.DeepEqual(paths, []string{"/about", "/posts/hello"}) {
		t.Fatalf("explicit dynamic selection: %v, %v", paths, err)
	}
	for _, path := range []string{"/api/health", "/missing", "/about?query=1", "/about?", "/about#hash", "/about#", "//host/path", "https://host/about", "/../secret", "/%2e%2e/secret", "/posts/a%2Fb", "/posts/a%5Cb", "/posts//hello", "/posts/hello/", "/posts/%00"} {
		if _, err := exportPaths(p, path); err == nil {
			t.Errorf("accepted unsafe or non-page path %q", path)
		}
	}
	canonical, decoded, err := exportPath("/posts/caf%C3%A9")
	if err != nil || canonical != "/posts/caf%C3%A9" || decoded != "/posts/café" {
		t.Fatalf("Unicode URL/filesystem mapping: %q %q %v", canonical, decoded, err)
	}
	if _, err := exportPaths(&project.Project{Routes: []project.Route{{Pattern: "/"}, {Pattern: "/index"}}}, ""); err == nil {
		t.Fatal("root and /index must not overwrite the same navigation JSON file")
	}
}

func exportFixture(t *testing.T) (string, string) {
	t.Helper()
	assets := t.TempDir()
	for name, text := range map[string]string{"_nextgo/test/app.wasm": "wasm", "_nextgo/test/wasm_exec.js": "runtime", "_nextgo/test/boot.js": "bootstrap", "public/style.css": "body{}"} {
		path := filepath.Join(assets, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	page := func(c *nextgo.Context) (nextgo.Page, error) {
		return nextgo.Page{Title: "Export " + c.Request.URL.Path, Body: ui.Island("counter", struct{ Count int }{3}, ui.El("button", nil, ui.Text("Count: 3")))}, nil
	}
	app, err := nextgo.New(nextgo.Config{BuildID: "test", Logger: log.New(io.Discard, "", 0), Routes: []nextgo.Route{
		{Pattern: "/", Page: page}, {Pattern: "/about", Page: page}, {Pattern: "/posts/:slug", Page: page},
		{Pattern: "/missing", Page: func(*nextgo.Context) (nextgo.Page, error) { return nextgo.Page{}, nextgo.ErrNotFound }},
		{Pattern: "/action", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Body: ui.El("form", ui.Attrs{"method": "post", "action": "/_nextgo/actions/save"})}, nil
		}},
		{Pattern: "/stream", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Stream: true, Body: ui.Defer("content", ui.Text("Loading"), func(context.Context) (ui.Node, error) { return ui.Text("Ready"), nil })}, nil
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	t.Cleanup(server.Close)
	return assets, server.URL
}

func TestExportWritesHydratableHTMLNavigationDataAndAssets(t *testing.T) {
	assets, base := exportFixture(t)
	out := filepath.Join(t.TempDir(), "static")
	paths := []string{"/", "/about", "/posts/caf%C3%A9"}
	if err := exportApplication(context.Background(), base, assets, out, "test", paths); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "about/index.html", "posts/café/index.html", "_nextgo/data/index.json", "_nextgo/data/about.json", "_nextgo/data/posts/café.json", "_nextgo/test/app.wasm", "_nextgo/test/wasm_exec.js", "_nextgo/test/boot.js", "assets/style.css", ".nextgo-export.json"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("missing export artifact %s: %v", name, err)
		}
	}
	html, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil || !strings.Contains(string(html), `data-nextgo-export="true"`) || !strings.Contains(string(html), `data-nextgo-island="counter"`) || !strings.Contains(string(html), "Count: 3") {
		t.Fatalf("SSR/island markup was not retained: %s %v", html, err)
	}
	data, err := os.ReadFile(filepath.Join(out, "_nextgo/data/posts/café.json"))
	var envelope nextgo.Envelope
	if err != nil || json.Unmarshal(data, &envelope) != nil || envelope.URL != "/posts/caf%C3%A9" || envelope.Build != "test" || envelope.Status != http.StatusOK {
		t.Fatalf("invalid exported navigation envelope: %s %v", data, err)
	}
	// Re-exporting replaces a previous complete generation and removes stale pages.
	if err := exportApplication(context.Background(), base, assets, out, "test", []string{"/about"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "index.html")); !os.IsNotExist(err) {
		t.Fatal("old output survived publication of a replacement generation")
	}
}

func TestExportFailuresPreservePreviousGeneration(t *testing.T) {
	assets, base := exportFixture(t)
	out := filepath.Join(t.TempDir(), "static")
	if err := exportApplication(context.Background(), base, assets, out, "test", []string{"/"}); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(filepath.Join(out, "index.html"))
	for _, failure := range []string{"/missing", "/stream", "/action"} {
		if err := exportApplication(context.Background(), base, assets, out, "test", []string{"/about", failure}); err == nil {
			t.Fatalf("export accepted unsupported or failing page %s", failure)
		}
		current, _ := os.ReadFile(filepath.Join(out, "index.html"))
		if string(current) != string(original) {
			t.Fatal("failed generation changed the previous export")
		}
		if _, err := os.Stat(filepath.Join(out, "about/index.html")); !os.IsNotExist(err) {
			t.Fatal("failed generation published a partial new page")
		}
	}
	if err := publishExport(filepath.Join(t.TempDir(), "missing-stage"), out); err == nil {
		t.Fatal("expected failed final rename")
	}
	current, _ := os.ReadFile(filepath.Join(out, "index.html"))
	if string(current) != string(original) {
		t.Fatal("publication failure did not restore previous output")
	}
}

func TestExportRefusesToReplaceUnrelatedDirectory(t *testing.T) {
	destination := t.TempDir()
	file := filepath.Join(destination, "go.mod")
	if err := os.WriteFile(file, []byte("module keep.me"), 0644); err != nil {
		t.Fatal(err)
	}
	stage := t.TempDir()
	if err := publishExport(stage, destination); err == nil {
		t.Fatal("export must not replace an unrelated developer directory")
	}
	got, err := os.ReadFile(file)
	if err != nil || string(got) != "module keep.me" {
		t.Fatal("unrelated output was changed")
	}
}

func TestMountedExportPlacesPagesAndAssetsAtDistinctPrefixes(t *testing.T) {
	assets, _ := exportFixture(t)
	deployment := nextgo.PathConfig{BasePath: "/docs", AssetPrefix: "https://cdn.example.test/cdn"}
	app, err := nextgo.New(nextgo.Config{BasePath: deployment.BasePath, AssetPrefix: deployment.AssetPrefix, BuildID: "test", Routes: []nextgo.Route{{Pattern: "/", Page: func(c *nextgo.Context) (nextgo.Page, error) {
		return nextgo.Page{Title: "mounted", Body: ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about")}, ui.Text("About"))}, nil
	}}, {Pattern: "/about", Page: func(*nextgo.Context) (nextgo.Page, error) { return nextgo.Page{Body: ui.Text("About")}, nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	defer server.Close()
	output := filepath.Join(t.TempDir(), "site")
	if err := exportApplication(context.Background(), server.URL, assets, output, "test", []string{"/", "/about"}, deployment); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docs/index.html", "docs/about/index.html", "docs/_nextgo/data/index.json", "docs/_nextgo/data/about.json", "cdn/_nextgo/test/app.wasm", "cdn/assets/style.css"} {
		if _, err := os.Stat(filepath.Join(output, filepath.FromSlash(name))); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	html, err := os.ReadFile(filepath.Join(output, "docs/index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`href="/docs/about"`, `data-nextgo-basepath="/docs"`, `src="https://cdn.example.test/cdn/_nextgo/test/boot.js"`} {
		if !strings.Contains(string(html), value) {
			t.Fatalf("missing %s", value)
		}
	}
	data, err := os.ReadFile(filepath.Join(output, "docs/_nextgo/data/about.json"))
	if err != nil {
		t.Fatal(err)
	}
	var envelope nextgo.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.URL != "/docs/about" {
		t.Fatalf("mounted data: %+v %v", envelope, err)
	}
}

func TestExportRejectsMixedRuntimeConfigurationsAndPreservesPublishedOutput(t *testing.T) {
	assets, originalServer := exportFixture(t)
	for _, betweenPages := range []bool{false, true} {
		output := filepath.Join(t.TempDir(), "published")
		if err := exportApplication(context.Background(), originalServer, assets, output, "test", []string{"/"}); err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(filepath.Join(output, "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		routes := []nextgo.Route{{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) { return nextgo.Page{Body: ui.Text("root")}, nil }}, {Pattern: "/about", Page: func(*nextgo.Context) (nextgo.Page, error) { return nextgo.Page{Body: ui.Text("about")}, nil }}}
		old, err := nextgo.New(nextgo.Config{BuildID: "test", Routes: routes})
		if err != nil {
			t.Fatal(err)
		}
		changed, err := nextgo.New(nextgo.Config{BuildID: "test", Routes: routes, AssetPrefix: "/other-assets"})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			selectNew := r.Header.Get("X-Nextgo-Navigate") == "1"
			if betweenPages {
				selectNew = r.URL.Path == "/about"
			}
			if selectNew {
				changed.ServeHTTP(w, r)
			} else {
				old.ServeHTTP(w, r)
			}
		}))
		err = exportApplication(context.Background(), server.URL, assets, output, "test", []string{"/", "/about"})
		server.Close()
		if err == nil || !strings.Contains(err.Error(), "configuration changed") {
			t.Fatalf("mixed generation was not rejected: %v", err)
		}
		after, err := os.ReadFile(filepath.Join(output, "index.html"))
		if err != nil || string(after) != string(before) {
			t.Fatalf("failed export replaced publication: %v", err)
		}
	}
}

func TestExportRejectsDocumentAttributeSkewWithoutReplacingPublishedFiles(t *testing.T) {
	assets, previous := exportFixture(t)
	output := filepath.Join(t.TempDir(), "published")
	if err := exportApplication(context.Background(), previous, assets, output, "test", []string{"/"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(output, "index.html"))
	app, err := nextgo.New(nextgo.Config{BuildID: "test", Routes: []nextgo.Route{{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) { return nextgo.Page{Body: ui.Text("body")}, nil }, Metadata: []nextgo.MetadataLayer{{Load: func(c *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
		language := "fr"
		if c.Request.Header.Get("X-Nextgo-Navigate") == "1" {
			language = "ar"
		}
		return seo.Metadata{Language: language}, nil
	}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	defer server.Close()
	err = exportApplication(context.Background(), server.URL, assets, output, "test", []string{"/"})
	if err == nil || !strings.Contains(err.Error(), "document attributes changed") {
		t.Fatal("document skew accepted", err)
	}
	after, _ := os.ReadFile(filepath.Join(output, "index.html"))
	if string(after) != string(before) {
		t.Fatal("mixed document export replaced publication")
	}
}

func TestStaticExporterRejectsPerRequestNonces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Nextgo-Nonce", "1")
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html></html>")
	}))
	defer server.Close()
	if _, err := exportResponse(context.Background(), server.Client(), server.URL, false, ""); err == nil || !strings.Contains(err.Error(), "nonces require dynamic rendering") {
		t.Fatal("nonce response accepted for static publication", err)
	}
}
