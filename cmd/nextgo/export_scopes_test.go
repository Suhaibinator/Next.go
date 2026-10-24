package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestScopedExportPathSelectionNamespacesAndBounds(t *testing.T) {
	p := &project.Project{Routes: []project.Route{{Pattern: "/"}, {Pattern: "/about"}, {Pattern: "/index"}, {Pattern: "/posts/:slug"}, {Pattern: "/api/health", API: true}}}
	config := nextgo.ExportConfig{Paths: nextgo.PathConfig{BasePath: "/shop"}, Scopes: []nextgo.StaticExportScope{{Prefix: "", Locale: "en", Configuration: strings.Repeat("a", 64)}, {Prefix: "/fr", Locale: "fr", Configuration: strings.Repeat("b", 64)}}}
	paths, err := expandExportPaths([]string{"/", "/posts/caf%C3%A9"}, config.Scopes)
	if err != nil {
		t.Fatal(err)
	}
	got, err := validateScopedExportPaths(p, paths, config)
	if err != nil || !reflect.DeepEqual(got, []string{"/", "/fr", "/fr/posts/caf%C3%A9", "/posts/caf%C3%A9"}) {
		t.Fatal(got, err)
	}
	for _, value := range []string{"/fr/api/health", "/fr/missing", "/fr/posts/a%2Fb", "/fr/../about"} {
		if _, err := validateScopedExportPaths(p, []string{value}, config); err == nil {
			t.Fatal("invalid scoped path accepted", value)
		}
	}
	if _, err := validateScopedExportPaths(p, []string{"/fr", "/fr/index"}, config); err == nil {
		t.Fatal("per-locale index collision accepted")
	}
	config.Paths.TrailingSlash = true
	if got, err := validateScopedExportPaths(p, []string{"/fr/", "/fr/about/"}, config); err != nil || !reflect.DeepEqual(got, []string{"/fr", "/fr/about"}) {
		t.Fatal(got, err)
	}
	if _, err := expandExportPaths(make([]string, 5001), config.Scopes); err == nil {
		t.Fatal("locale expansion bypassed total row bound")
	}
	for _, scope := range []nextgo.StaticExportScope{{Prefix: "/../bad", Configuration: strings.Repeat("c", 64)}, config.Scopes[0], {Prefix: "/other", Configuration: "bad"}} {
		if err := validateExportScopes(config.Paths, append(config.Scopes, scope)); err == nil {
			t.Fatal("unsafe/duplicate ownership scope accepted", scope)
		}
	}
}

func TestDomainExportUsesLoopbackTransportWithConfiguredHost(t *testing.T) {
	assets, _ := exportFixture(t)
	app, err := nextgo.New(nextgo.Config{BuildID: "test", BasePath: "/shop", AssetPrefix: "/shared", Locales: &nextgo.LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", Domains: []nextgo.LocaleDomain{{Host: "en.invalid", Default: "en"}, {Host: "fr.invalid:8443", Default: "fr"}}}, Routes: []nextgo.Route{{Pattern: "/", Page: func(c *nextgo.Context) (nextgo.Page, error) {
		return nextgo.Page{Body: ui.El("a", ui.Attrs{"href": nextgo.MustLocaleURL(c, "en", "/")}, ui.Text(nextgo.CurrentLocale(c)))}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "fr.invalid:8443" {
			t.Error("export did not select the configured authority", r.Host)
		}
		requests.Add(1)
		app.ServeHTTP(w, r)
	}))
	defer server.Close()
	info, err := app.StaticExportConfig(nextgo.StaticExportOptions{Domain: "fr.invalid:8443"})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := app.StaticPathsForDomain(context.Background(), info.Domain)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "site")
	if err := exportWithScopes(context.Background(), server.URL, assets, out, "test", paths, info.Paths, info.Scopes, info.Domain); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 4 {
		t.Fatal("expected loopback HTML/JSON requests for both language scopes", requests.Load())
	}
	data, err := os.ReadFile(filepath.Join(out, "shop", "index.html"))
	if err != nil || !strings.Contains(string(data), `lang="fr"`) || !strings.Contains(string(data), `href="https://en.invalid/shop"`) {
		t.Fatal(string(data), err)
	}
	manifestData, err := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
	var m exportManifest
	if err != nil || json.Unmarshal(manifestData, &m) != nil || m.Domain != "fr.invalid:8443" || m.Format != "nextgo-static-v2" {
		t.Fatal(string(manifestData), err)
	}
	if err := validateExportDestination(out); err != nil {
		t.Fatal("valid domain publication was not recognized", err)
	}
	if err := exportWithScopes(context.Background(), server.URL, assets, out, "test", paths, info.Paths, info.Scopes, "bad.invalid\r\nHeader:yes"); err == nil {
		t.Fatal("invalid rendering Host accepted")
	}
	current, _ := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
	if string(current) != string(manifestData) {
		t.Fatal("rejected host changed prior publication")
	}
}

func TestLocalizedExportPublishesDistinctDataAndRejectsConfigurationChanges(t *testing.T) {
	assets, _ := exportFixture(t)
	for _, prefixed := range []bool{false, true} {
		for _, trailing := range []bool{false, true} {
			func() {
				app, err := nextgo.New(nextgo.Config{BuildID: "test", BasePath: "/shop", AssetPrefix: "/shared", TrailingSlash: trailing, Locales: &nextgo.LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: prefixed}, Routes: []nextgo.Route{{Pattern: "/", Page: func(c *nextgo.Context) (nextgo.Page, error) {
					return nextgo.Page{Body: ui.Text(nextgo.CurrentLocale(c))}, nil
				}}, {Pattern: "/posts/:slug", Page: func(c *nextgo.Context) (nextgo.Page, error) {
					return nextgo.Page{Body: ui.Text(nextgo.CurrentLocale(c) + c.Params["slug"])}, nil
				}}}})
				if err != nil {
					t.Fatal(err)
				}
				defer app.Close(context.Background())
				server := httptest.NewServer(app)
				defer server.Close()
				info, err := app.StaticExportConfig()
				if err != nil {
					t.Fatal(err)
				}
				paths, err := expandExportPaths([]string{"/", "/posts/caf%C3%A9"}, info.Scopes)
				if err != nil {
					t.Fatal(err)
				}
				out := filepath.Join(t.TempDir(), "site")
				if err := exportWithScopes(context.Background(), server.URL, assets, out, "test", paths, info.Paths, info.Scopes); err != nil {
					t.Fatal(err)
				}
				for _, scope := range info.Scopes {
					for _, name := range []string{"index.json", "posts/café.json"} {
						file := filepath.Join(out, "shop", strings.TrimPrefix(scope.Prefix, "/"), "_nextgo", "data", filepath.FromSlash(name))
						data, err := os.ReadFile(file)
						var e nextgo.Envelope
						if err != nil || json.Unmarshal(data, &e) != nil || e.Configuration != scope.Configuration || e.Document.Language != scope.Locale {
							t.Fatal(file, string(data), err)
						}
					}
				}
				if _, err := os.Stat(filepath.Join(out, "shared", "_nextgo", "test", "app.wasm")); err != nil {
					t.Fatal(err)
				}
				manifestData, err := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
				var m exportManifest
				if err != nil || json.Unmarshal(manifestData, &m) != nil || m.Format != "nextgo-static-v2" || len(m.Scopes) != 2 {
					t.Fatal(string(manifestData), err)
				}
				// Both HTML and JSON can agree on a changed policy: compare them to
				// the build declaration too, before replacing an earlier publication.
				info.Scopes[0].Configuration = strings.Repeat("0", 64)
				if err := exportWithScopes(context.Background(), server.URL, assets, out, "test", paths, info.Paths, info.Scopes); err == nil {
					t.Fatal("undeclared configuration accepted")
				}
				current, _ := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
				if string(current) != string(manifestData) {
					t.Fatal("failed export changed publication")
				}
				info, _ = app.StaticExportConfig()
				if err := exportWithScopes(context.Background(), server.URL, assets, out, "test", paths[:1], info.Paths, info.Scopes); err != nil {
					t.Fatal("v2 publication could not be replaced", err)
				}
			}()
		}
	}
}
