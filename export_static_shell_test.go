package nextgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/metadatafiles"

	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

const staticShellTestBuild = "0123456789abcdef0123"

func staticShellTestApp(t *testing.T, config Config) *App {
	t.Helper()
	app := testApp(t, config)
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	return app
}

func staticShellTestConfig() Config {
	return Config{BuildID: staticShellTestBuild, StaticShell: &StaticShell{Pattern: "/workspace/*rest?", Component: "workspace"}, Routes: []Route{{Pattern: "/workspace/*rest?", Page: func(c *Context) (Page, error) {
		return Page{Title: "Workspace", Body: ui.ClientIsland("workspace", map[string]string{"public": "constant"}, ui.El("p", nil, ui.Text("Useful workspace fallback "+c.Params["rest"])))}, nil
	}}}}
}

func TestStaticShellDeclarationIsExplicitOwnedAndVersioned(t *testing.T) {
	config := staticShellTestConfig()
	declaration := config.StaticShell
	app := staticShellTestApp(t, config)
	declaration.Pattern, declaration.Component = "/changed/*path?", "changed"
	info, err := app.StaticExportConfig()
	if err != nil || info.Version != 3 || info.StaticShell == nil || info.StaticShell.Version != 1 || info.StaticShell.Pattern != "/workspace/*rest?" || info.StaticShell.Component != "workspace" || info.StaticShell.SeedPath != "/workspace" || len(info.Scopes) != 1 || info.Scopes[0].Locale != "" {
		t.Fatal(info, err)
	}
	encoded, err := json.Marshal(info)
	if err != nil || !strings.Contains(string(encoded), `"version":3`) || !strings.Contains(string(encoded), `"staticShell":`) {
		t.Fatal(string(encoded), err)
	}
	info.StaticShell.Component = "mutated"
	again, err := app.StaticExportConfig()
	if err != nil || again.StaticShell.Component != "workspace" {
		t.Fatal("export DTO aliases native declaration", again, err)
	}
	plain := staticShellTestConfig()
	plain.StaticShell = nil
	legacy, err := staticShellTestApp(t, plain).StaticExportConfig()
	if err != nil || legacy.Version != 1 || legacy.StaticShell != nil {
		t.Fatal("opt-in changed legacy export", legacy, err)
	}
}

func TestStaticShellEffectiveOwnershipAndPolicy(t *testing.T) {
	page := func(*Context) (Page, error) { return Page{Body: ui.Text("other")}, nil }
	for _, test := range []struct {
		name    string
		edit    func(*Config)
		allowed bool
	}{
		{"literal-child", func(c *Config) { c.Routes = append(c.Routes, Route{Pattern: "/workspace/settings", Page: page}) }, false},
		{"parameter-ancestor", func(c *Config) { c.Routes = append(c.Routes, Route{Pattern: "/:group/settings", Page: page}) }, false},
		{"catchall-ancestor", func(c *Config) {
			c.Routes = append(c.Routes, Route{Pattern: "/*other", API: func(http.ResponseWriter, *http.Request) {}})
		}, false},
		{"disjoint-route", func(c *Config) { c.Routes = append(c.Routes, Route{Pattern: "/workspaces/:id", Page: page}) }, true},
		{"handler-child", func(c *Config) { c.Handlers = map[string]http.Handler{"/workspace/api/": http.NotFoundHandler()} }, false},
		{"handler-ancestor", func(c *Config) { c.Handlers = map[string]http.Handler{"/": http.NotFoundHandler()} }, false},
		{"handler-disjoint", func(c *Config) { c.Handlers = map[string]http.Handler{"/workspaceish/": http.NotFoundHandler()} }, true},
		{"document-child", func(c *Config) { c.DocumentPaths = []string{"/workspace/docs"} }, false},
		{"document-disjoint", func(c *Config) { c.DocumentPaths = []string{"/workspaceish"} }, true},
		{"root-shell", func(c *Config) { c.StaticShell.Pattern = "/*rest?"; c.Routes[0].Pattern = "/*rest?" }, false},
		{"dynamic-prefix", func(c *Config) { c.StaticShell.Pattern = "/:space/*rest?"; c.Routes[0].Pattern = "/:space/*rest?" }, false},
		{"missing-exact-page", func(c *Config) { c.StaticShell.Pattern = "/other/*rest?" }, false},
		{"endpoint-shell", func(c *Config) { c.Routes[0].Page = nil; c.Routes[0].API = func(http.ResponseWriter, *http.Request) {} }, false},
		{"retained-layouts", func(c *Config) { c.PreserveLayouts = true }, false},
		{"layout", func(c *Config) {
			c.Routes[0].Layouts = []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return n, nil }}
		}, false},
		{"page-cache", func(c *Config) { c.Routes[0].Cache = &CachePolicy{TTL: time.Minute} }, false},
		{"shared-page-cache", func(c *Config) { c.Routes[0].Cache = &CachePolicy{TTL: time.Minute, Shared: true} }, false},
		{"page-cache-lifetime", func(c *Config) {
			c.Routes[0].Cache = &CachePolicy{Lifetime: &cache.Lifetime{Revalidate: time.Second, Expire: time.Minute}}
		}, false},
		{"prerender", func(c *Config) { c.Routes[0].Prerender = &PrerenderPolicy{} }, false},
		{"simple-rule", func(c *Config) { c.Redirects = []RedirectRule{{Source: "/elsewhere", Destination: "/another"}} }, false},
		{"actions", func(c *Config) { c.Actions = http.NotFoundHandler() }, false},
		{"images", func(c *Config) { c.Images = http.NotFoundHandler() }, false},
		{"locale", func(c *Config) { c.Locales = &LocaleConfig{Codes: []string{"en"}, Default: "en"} }, false},
		{"manual-build-id", func(c *Config) { c.BuildID = "test" }, false},
		{"asset-overlap", func(c *Config) { c.AssetPrefix = "/workspace" }, false},
		{"mounted-disjoint-assets", func(c *Config) { c.BasePath = "/docs"; c.AssetPrefix = "/shared" }, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := staticShellTestConfig()
			test.edit(&config)
			app, err := New(config)
			if err == nil {
				defer app.Close(t.Context())
				_, err = app.StaticExportConfig()
			}
			if (err == nil) != test.allowed {
				t.Fatalf("allowed=%v, error=%v", test.allowed, err)
			}
		})
	}
}

func TestStaticShellNativeHTTPStillRunsActualPage(t *testing.T) {
	app := staticShellTestApp(t, staticShellTestConfig())
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/workspace/deep/native?query=ignored", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Useful workspace fallback deep/native") || strings.Contains(response.Body.String(), "data-nextgo-static-shell") || response.Header().Get(RequiresServerHeader) != "" {
		t.Fatal(response.Code, response.Header(), response.Body.String())
	}
}

func TestStaticShellExportRefusesDeferredAndStreamsBeforeCacheFlattening(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		for _, cached := range []bool{false, true} {
			config := staticShellTestConfig()
			config.Phase = PhaseStaticExport
			if cached {
				config.Routes[0].Cache = &CachePolicy{TTL: time.Minute}
			}
			config.Routes[0].Page = func(*Context) (Page, error) {
				page := Page{Body: ui.ClientIsland("workspace", nil, ui.Text("Useful fallback")), Stream: !deferred}
				if deferred {
					page.DeferredMetadata = func(*Context, seo.Metadata) (seo.Metadata, error) {
						t.Error("excluded deferred metadata ran")
						return seo.Metadata{}, nil
					}
				}
				return page, nil
			}
			app := staticShellTestApp(t, config)
			response := httptest.NewRecorder()
			app.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/workspace", nil))
			if response.Code == http.StatusOK {
				t.Fatal("export accepted flattened stream/deferred metadata", deferred, cached)
			}
		}
	}
}

func TestStaticShellVersionThreeKeepsManagedMetadataInventory(t *testing.T) {
	config := staticShellTestConfig()
	config.MetadataFiles = metadatafiles.Sources{Static: fstest.MapFS{"robots.txt": {Data: []byte("User-agent: *\nDisallow: /private\n")}}}
	info, err := staticShellTestApp(t, config).StaticExportConfig()
	if err != nil || info.Version != 3 || info.StaticShell == nil || len(info.Metadata) != 1 || info.Metadata[0].Name != "robots.txt" {
		t.Fatal(info, err)
	}
	config.StaticShell = nil
	legacy, err := staticShellTestApp(t, config).StaticExportConfig()
	if err != nil || legacy.Version != 2 || legacy.StaticShell != nil || len(legacy.Metadata) != 1 {
		t.Fatal("legacy metadata export semantics changed", legacy, err)
	}
}

func TestStaticShellRefusesPrepopulatedPageCacheBeforeSeedRendering(t *testing.T) {
	store := cache.New(8)
	var pageCalls, metadataCalls atomic.Int64
	config := staticShellTestConfig()
	shell := config.StaticShell
	config.StaticShell, config.Phase, config.Cache = nil, PhaseStaticExport, store
	config.Routes[0].Cache = &CachePolicy{TTL: time.Minute}
	config.Routes[0].Page = func(*Context) (Page, error) {
		pageCalls.Add(1)
		return Page{Body: ui.ClientIsland("workspace", nil, ui.Text("Cached public fallback")), DeferredMetadata: func(*Context, seo.Metadata) (seo.Metadata, error) {
			metadataCalls.Add(1)
			return seo.Metadata{Title: "Flattened deferred title"}, nil
		}}, nil
	}
	before := staticShellTestApp(t, config)
	for range 2 {
		response := request(before, "/workspace", false)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Flattened deferred title") {
			t.Fatal("failed to prepare flattened reusable cache entry", response.Code, response.Body.String())
		}
	}
	if pageCalls.Load() != 1 || metadataCalls.Load() != 1 || store.Len() == 0 {
		t.Fatal("fixture did not actually reuse the native page cache", pageCalls.Load(), metadataCalls.Load(), store.Len())
	}
	config.StaticShell = shell
	after := staticShellTestApp(t, config)
	if before.configurationID != after.configurationID {
		t.Fatal("fixture does not share the cache compatibility identity")
	}
	entries := store.Len()
	if _, err := after.StaticExportConfig(); err == nil {
		t.Fatal("shell adopted cached output whose deferred declaration had been flattened")
	}
	if pageCalls.Load() != 1 || metadataCalls.Load() != 1 || store.Len() != entries {
		t.Fatal("refusal performed seed work or changed the previous cache")
	}
}
