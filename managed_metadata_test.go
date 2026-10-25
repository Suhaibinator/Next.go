package nextgo

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func managedMetadataFS(t *testing.T) fstest.MapFS {
	t.Helper()
	var encoded bytes.Buffer
	bitmap := image.NewRGBA(image.Rect(0, 0, 2, 2))
	bitmap.Set(0, 0, color.RGBA{R: 200, A: 255})
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	return fstest.MapFS{"robots.txt": {Data: []byte("User-agent: *\nDisallow: /private\n")}, "icon.png": {Data: encoded.Bytes()}, "manifest.webmanifest": {Data: []byte(`{"name":"Public app"}`)}}
}
func managedMetadataApp(t *testing.T, c Config) *App {
	t.Helper()
	app, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return app
}
func managedRequest(app *App, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}

func TestManagedMetadataPublicDispatchHeadersMethodsAndBuildProbe(t *testing.T) {
	var middleware atomic.Int32
	input := managedMetadataFS(t)
	app := managedMetadataApp(t, Config{BuildID: "managed", BasePath: "/site", TrailingSlash: true, AssetPrefix: "https://cdn.example.test/assets", MetadataFiles: metadatafiles.Sources{Static: input}, Headers: http.Header{"X-Configured": {"yes"}}, HeaderRules: []HeaderRule{{Source: "/robots.txt", Headers: []HeaderValue{{Name: "X-Resource-Rule", Value: "yes"}}}}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middleware.Add(1)
			http.Error(w, "private page middleware", http.StatusUnauthorized)
		})
	}}, Routes: []Route{{Pattern: "/:name", Page: func(*Context) (Page, error) { return Page{Body: ui.Text("dynamic fallback")}, nil }}}})
	original := append([]byte(nil), input["robots.txt"].Data...)
	input["robots.txt"].Data[0] = 'X'
	response := managedRequest(app, "GET", "http://example.test/site/robots.txt", map[string]string{"Accept-Language": "fr", "Cookie": "private=secret", "X-Nextgo-Navigate": "1"})
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), original) || middleware.Load() != 0 {
		t.Fatal(response.Code, response.Body.String(), middleware.Load())
	}
	if response.Header().Get("X-Configured") != "yes" || response.Header().Get("X-Resource-Rule") != "yes" || response.Header().Get(navigation.BuildHeader) != "managed" || response.Header().Get("Content-Type") != "text/plain; charset=utf-8" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Location") != "" {
		t.Fatal(response.Header())
	}
	etag := response.Header().Get("ETag")
	if len(etag) != 66 || !strings.HasPrefix(etag, `"`) {
		t.Fatal(etag)
	}
	conditional := managedRequest(app, "GET", "http://example.test/site/robots.txt", map[string]string{"If-None-Match": "W/" + etag})
	if conditional.Code != 304 || conditional.Body.Len() != 0 || conditional.Header().Get(navigation.BuildHeader) != "managed" {
		t.Fatal(conditional.Code, conditional.Header(), conditional.Body.String())
	}
	head := managedRequest(app, "HEAD", "http://example.test/site/robots.txt", nil)
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") == "" {
		t.Fatal(head.Code, head.Header(), head.Body.String())
	}
	post := managedRequest(app, "POST", "http://example.test/site/robots.txt", nil)
	if post.Code != 405 || post.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal(post.Code, post.Header())
	}
	for _, target := range []string{"http://example.test/robots.txt", "http://example.test/site/robots.txt/", "http://example.test/site/%72obots.txt"} {
		if got := managedRequest(app, "GET", target, nil); got.Code == 200 && bytes.Equal(got.Body.Bytes(), original) {
			t.Fatal("noncanonical public metadata alias served", target)
		}
	}
	probe := managedMetadataApp(t, Config{BuildID: "probe", PrerenderBuildMode: true, MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}})
	if got := managedRequest(probe, "GET", "http://example.test/robots.txt", nil); got.Code != 503 || strings.Contains(got.Body.String(), "Disallow:") {
		t.Fatal("build probe published metadata", got.Code, got.Body.String())
	}
}
func TestManagedMetadataLocaleGlobalMountOnceAndHostValidation(t *testing.T) {
	var producers atomic.Int32
	app := managedMetadataApp(t, Config{BuildID: "locales", BasePath: "/site", TrailingSlash: true, AssetPrefix: "https://cdn.example.test/static", Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true, Detect: true, Domains: []LocaleDomain{{Host: "en.example.test", Default: "en"}, {Host: "fr.example.test", Default: "fr"}}}, MetadataFiles: metadatafiles.Sources{Robots: func(context.Context) (seo.RobotsOptions, error) { producers.Add(1); return seo.RobotsOptions{}, nil }}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{Body: ui.Text("page")}, nil }}}})
	if producers.Load() != 1 {
		t.Fatal("metadata producer reran for locale children", producers.Load())
	}
	if app.config.MetadataFiles.Robots != nil || app.config.MetadataFiles.Static != nil {
		t.Fatal("startup sources retained")
	}
	for _, scope := range app.locales.mounts {
		if scope.app.config.metadataSet != app.config.metadataSet || scope.app.config.metadataBasePath != "/site" {
			t.Fatal("locale child did not share global frozen metadata")
		}
	}
	for _, host := range []string{"en.example.test", "fr.example.test"} {
		got := managedRequest(app, "GET", "https://"+host+"/site/robots.txt", map[string]string{"Accept-Language": "fr", "Cookie": "nextgo_locale=fr"})
		if got.Code != 200 || got.Header().Get("Location") != "" || got.Header().Get("Content-Language") != "" || len(got.Header().Values("Vary")) != 0 {
			t.Fatal(host, got.Code, got.Header(), got.Body.String())
		}
		for _, path := range []string{"/site/en/robots.txt", "/site/fr/robots.txt", "/robots.txt"} {
			if got := managedRequest(app, "GET", "https://"+host+path, nil); got.Code == 200 {
				t.Fatal("locale/root metadata alias served", host, path)
			}
		}
	}
	if got := managedRequest(app, "GET", "https://unknown.example.test/site/robots.txt", nil); got.Code != http.StatusMisdirectedRequest {
		t.Fatal("metadata bypassed configured host validation", got.Code)
	}
}
func TestManagedMetadataCollisionsAndDynamicFallbackPriority(t *testing.T) {
	for _, api := range []bool{false, true} {
		route := Route{Pattern: "/robots.txt", Page: func(*Context) (Page, error) { return Page{}, nil }}
		if api {
			route.Page = nil
			route.API = func(http.ResponseWriter, *http.Request) {}
		}
		if app, err := New(Config{MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}, Routes: []Route{route}}); err == nil {
			app.Close(context.Background())
			t.Fatal("exact page/API collision accepted", api)
		}
	}
	for _, pattern := range []string{"/robots.txt", "/"} {
		if app, err := New(Config{MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}, Handlers: map[string]http.Handler{pattern: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}}); err == nil {
			app.Close(context.Background())
			t.Fatal("handler collision accepted", pattern)
		}
	}
	app := managedMetadataApp(t, Config{MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}, Routes: []Route{{Pattern: "/:name", Page: func(*Context) (Page, error) { return Page{Body: ui.Text("dynamic fallback")}, nil }}}})
	if got := managedRequest(app, "GET", "http://example.test/robots.txt", nil); got.Code != 200 || strings.Contains(got.Body.String(), "dynamic fallback") {
		t.Fatal("dynamic fallback shadowed managed file", got.Code, got.Body.String())
	}
	if got := managedRequest(app, "GET", "http://example.test/other", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "dynamic fallback") {
		t.Fatal("dynamic fallback was removed", got.Code)
	}
}
func TestManagedMetadataHeadsCachesAndExplicitOverrides(t *testing.T) {
	var loads atomic.Int32
	empty := ""
	app := managedMetadataApp(t, Config{BuildID: "head", BasePath: "/site", AssetPrefix: "https://cdn.example.test/static", MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}, Routes: []Route{{Pattern: "/:mode", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) {
		loads.Add(1)
		page := Page{Body: ui.Text("page")}
		switch c.Params["mode"] {
		case "disabled":
			page.Metadata.Icons = []seo.Icon{}
			page.Metadata.Manifest = &empty
		case "explicit":
			value := "/custom.webmanifest"
			page.Metadata.Manifest = &value
			page.Metadata.Icons = []seo.Icon{{URL: "/custom.png"}}
		}
		return page, nil
	}}}})
	for _, mode := range []string{"default", "disabled", "explicit"} {
		html := managedRequest(app, "GET", "http://example.test/site/"+mode, nil)
		navigationResponse := managedRequest(app, "GET", "http://example.test/site/"+mode, map[string]string{"X-Nextgo-Navigate": "1"})
		var envelope Envelope
		if err := json.Unmarshal(navigationResponse.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err, navigationResponse.Body.String())
		}
		var icons, manifests []string
		for _, element := range envelope.Head {
			switch element.Attrs["rel"] {
			case "icon":
				icons = append(icons, element.Attrs["href"])
			case "manifest":
				manifests = append(manifests, element.Attrs["href"])
			}
		}
		switch mode {
		case "default":
			if len(icons) != 1 || icons[0] != "/site/icon.png" || len(manifests) != 1 || manifests[0] != "/site/manifest.webmanifest" || strings.Count(html.Body.String(), `href="/site/icon.png"`) != 1 {
				t.Fatal("managed default head lost or double-resolved", icons, manifests, html.Body.String())
			}
		case "disabled":
			if len(icons) != 0 || len(manifests) != 0 {
				t.Fatal("explicit empty disable lost in cached head", icons, manifests)
			}
		case "explicit":
			if len(icons) != 1 || icons[0] != "/site/custom.png" || len(manifests) != 1 || manifests[0] != "/site/custom.webmanifest" {
				t.Fatal("explicit head did not override defaults", icons, manifests)
			}
		}
	}
	if loads.Load() != 3 {
		t.Fatal("head checks did not reuse public snapshots", loads.Load())
	}
	// Locale defaults remain global while explicitly authored resource paths use
	// normal locale/CDN resolution. Prepared cached heads retain that distinction.
	localized := managedMetadataApp(t, Config{BuildID: "locale-head", BasePath: "/site", AssetPrefix: "https://cdn.example.test/static", Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}, MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
		return Page{Body: ui.Text("page"), Resources: []assets.Resource{assets.Stylesheet("/assets/app.css")}}, nil
	}}}})
	for _, code := range []string{"en", "fr"} {
		got := managedRequest(localized, "GET", "http://example.test/site/"+code, nil)
		if got.Code != 200 || !strings.Contains(got.Body.String(), `href="/site/icon.png"`) || !strings.Contains(got.Body.String(), `href="/site/manifest.webmanifest"`) || strings.Contains(got.Body.String(), "/"+code+"/icon.png") || !strings.Contains(got.Body.String(), "https://cdn.example.test/static/assets/app.css") {
			t.Fatal(code, got.Code, got.Body.String())
		}
	}
}
func TestManagedMetadataIdentityAndEndpointScope(t *testing.T) {
	sharedCache := cache.New(32)
	makeConfig := func(data string) Config {
		return Config{BuildID: "identity", BasePath: "/site", Cache: sharedCache, MetadataFiles: metadatafiles.Sources{Static: fstest.MapFS{"robots.txt": {Data: []byte(data)}}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{Body: ui.Text("page")}, nil }}}}
	}
	first := managedMetadataApp(t, makeConfig("User-agent: *\nDisallow: /first\n"))
	same := managedMetadataApp(t, makeConfig("User-agent: *\nDisallow: /first\n"))
	different := managedMetadataApp(t, makeConfig("User-agent: *\nDisallow: /second\n"))
	if first.configurationID != same.configurationID || first.configurationID == different.configurationID || first.prerenderShape == different.prerenderShape {
		t.Fatal("frozen metadata identity omitted or unstable")
	}
	if configurationMetadataPolicy("legacy", "") != "legacy" {
		t.Fatal("empty metadata changed legacy cache identity")
	}
	var manifest navigation.Manifest
	if err := json.Unmarshal([]byte(first.navigationJSON), &manifest); err != nil {
		t.Fatal(err)
	}
	if !manifest.IsEndpoint("/robots.txt", "/robots.txt") {
		t.Fatal("global metadata endpoint eligible for SPA")
	}
	localized := makeConfig("User-agent: *\nDisallow: /first\n")
	localized.Locales = &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en"}
	app := managedMetadataApp(t, localized)
	for _, scope := range app.locales.mounts {
		manifest = navigation.Manifest{}
		if err := json.Unmarshal([]byte(scope.app.navigationJSON), &manifest); err != nil {
			t.Fatal(err)
		}
		want := scope.app.paths.BasePath == "/site"
		if got := manifest.IsEndpoint("/robots.txt", "/robots.txt"); got != want {
			t.Fatal("endpoint excluded in wrong locale scope", scope.app.paths.BasePath, got, want)
		}
	}
}

func TestManagedMetadataTracingAndConditionalHeaderPolicy(t *testing.T) {
	var records traceRecords
	config := tracedConfig(t, &records)
	config.MetadataFiles = metadatafiles.Sources{Static: managedMetadataFS(t)}
	config.HeaderRules = []HeaderRule{{Source: "/robots.txt", Has: []RuleCondition{{Type: "header", Key: "X-Audience", Value: "public"}}, Headers: []HeaderValue{{Name: "X-Audience-Rule", Value: "matched"}}}}
	app := managedMetadataApp(t, config)
	got := managedRequest(app, "GET", "http://example.test/robots.txt?secret=never-record", map[string]string{"X-Audience": "public", "Authorization": "never-record"})
	if got.Code != 200 || got.Header().Get("X-Audience-Rule") != "matched" || got.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(got.Code, got.Header(), got.Body.String())
	}
	values := records.all()
	var requests, metadata int
	for _, value := range values {
		if value.Name == "nextgo.request" {
			requests++
		}
		if value.Name == "nextgo.metadata" {
			metadata++
			if value.Route != "/robots.txt" || value.Status != 200 || value.Bytes == 0 {
				t.Fatal(value)
			}
		}
	}
	if requests != 1 || metadata != 1 {
		t.Fatal("metadata trace missing or duplicated", values)
	}
	encoded, _ := json.Marshal(values)
	if strings.Contains(string(encoded), "never-record") {
		t.Fatal("metadata tracing recorded request secrets")
	}
}
func TestManagedMetadataInheritedEmptyOverridesAndFrozenHeadOwnership(t *testing.T) {
	empty := ""
	app := managedMetadataApp(t, Config{MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
		return seo.Metadata{Icons: []seo.Icon{}, Manifest: &empty}, nil
	}}}, Page: func(*Context) (Page, error) { return Page{Body: ui.Text("page")}, nil }}}})
	got := managedRequest(app, "GET", "http://example.test/", nil)
	if got.Code != 200 || strings.Contains(got.Body.String(), `rel="icon"`) || strings.Contains(got.Body.String(), `rel="manifest"`) {
		t.Fatal("inherited empty override lost", got.Code, got.Body.String())
	}
	page := app.withManagedMetadataHead(Page{})
	head, _, err := pageHeadPaths(page, app.paths)
	if err != nil {
		t.Fatal(err)
	}
	head[0].Attrs["href"] = "/changed"
	fresh := app.withManagedMetadataHead(Page{})
	again, _, err := pageHeadPaths(fresh, app.paths)
	if err != nil || again[0].Attrs["href"] == "/changed" {
		t.Fatal("render mutated frozen default head", again, err)
	}
}

func TestManagedMetadataPrerenderIncludesFinalValidatedHeads(t *testing.T) {
	var loads atomic.Int32
	config, data := buildPrerenderTest(t, Config{BasePath: "/site", MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}, Routes: []Route{{Pattern: "/article", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}, Page: func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("seeded article")}, nil }}}})
	if !bytes.Contains(data, []byte("/site/icon.png")) || !bytes.Contains(data, []byte("/site/manifest.webmanifest")) {
		t.Fatal("private prerender head omitted managed defaults", string(data))
	}
	before := loads.Load()
	app := managedMetadataApp(t, config)
	response := managedRequest(app, "GET", "http://example.test/site/article", nil)
	if response.Code != 200 || loads.Load() != before || strings.Count(response.Body.String(), `href="/site/icon.png"`) != 1 || strings.Count(response.Body.String(), `href="/site/manifest.webmanifest"`) != 1 {
		t.Fatal("prerender head lost, duplicated or unnecessarily rerendered", response.Code, loads.Load(), before, response.Body.String())
	}
	navigationResponse := managedRequest(app, "GET", "http://example.test/site/article", map[string]string{"X-Nextgo-Navigate": "1"})
	var envelope Envelope
	if err := json.Unmarshal(navigationResponse.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var icons, manifests int
	for _, element := range envelope.Head {
		if element.Attrs["rel"] == "icon" && element.Attrs["href"] == "/site/icon.png" {
			icons++
		}
		if element.Attrs["rel"] == "manifest" && element.Attrs["href"] == "/site/manifest.webmanifest" {
			manifests++
		}
	}
	if icons != 1 || manifests != 1 {
		t.Fatal("navigation/prerender head differs", envelope.Head)
	}
	// A changed frozen file cannot consume an old private snapshot namespace.
	config.MetadataFiles.Static = managedMetadataFS(t)
	config.MetadataFiles.Static.(fstest.MapFS)["robots.txt"].Data = []byte("User-agent: *\nDisallow: /changed\n")
	changed := managedMetadataApp(t, config)
	if changed.prerenderShape == app.prerenderShape {
		t.Fatal("metadata change did not fence private prerender shape")
	}
}

func TestManagedMetadataStaticParametersRejectActualGlobalShadow(t *testing.T) {
	makeConfig := func() Config {
		return Config{BasePath: "/site", MetadataFiles: metadatafiles.Sources{Static: managedMetadataFS(t)}, Routes: []Route{{Pattern: "/:name", Page: staticPage, StaticParams: func(context.Context) ([]Params, error) { return []Params{{"name": "robots.txt"}}, nil }}}}
	}
	app := managedMetadataApp(t, makeConfig())
	if _, err := app.StaticPaths(context.Background()); err == nil || !strings.Contains(err.Error(), "shadowed by managed metadata") {
		t.Fatal("metadata shadow missing from static parameter validation", err)
	}
	prefixed := makeConfig()
	prefixed.Locales = &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}
	scoped := managedMetadataApp(t, prefixed)
	values, err := scoped.StaticPaths(context.Background())
	if err != nil || len(values) != 2 || values[0] != "/en/robots.txt" || values[1] != "/fr/robots.txt" {
		t.Fatal("global metadata incorrectly shadowed locale-prefixed pages", values, err)
	}
	unprefixed := makeConfig()
	unprefixed.Locales = &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en"}
	if _, err := managedMetadataApp(t, unprefixed).StaticPaths(context.Background()); err == nil || !strings.Contains(err.Error(), "shadowed by managed metadata") {
		t.Fatal("unprefixed default locale bypassed metadata shadow validation", err)
	}
}
