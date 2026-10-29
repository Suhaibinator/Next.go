//go:build !js

package nextgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/offline"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func managedOfflineFixture(t *testing.T, base string) (Config, fstest.MapFS) {
	t.Helper()
	runtime := strings.Repeat("a", 20)
	files := fstest.MapFS{}
	build := offline.Build{Version: offline.Version, RuntimeID: runtime, BridgeTemplate: "const descriptor=" + offline.BuildDescriptorPlaceholder + ";"}
	add := func(suffix string, kind offline.AssetKind, contentType string, body []byte) {
		path := "_nextgo/" + runtime + "/" + suffix
		files[path] = &fstest.MapFile{Data: bytes.Clone(body)}
		build.Files = append(build.Files, offline.BuildFile{Path: path, Kind: kind, SHA256: offlineDigest(body), DecodedBytes: int64(len(body)), ContentType: contentType})
	}
	wasm := []byte{'\x00', 'a', 's', 'm', 1, 0, 0, 0}
	add("app.wasm", offline.CoreWASM, "application/wasm", wasm)
	add("worker-"+offlineDigest(wasm)+".wasm", offline.WorkerWASM, "application/wasm", wasm)
	add("wasm_exec.js", offline.RuntimeShim, "text/javascript; charset=utf-8", []byte("// paired SDK fixture"))
	add("boot.js", offline.BootScript, "text/javascript; charset=utf-8", []byte("// bootstrap fixture"))
	add("offline-public/assets/style.css", offline.Style, "text/css; charset=utf-8", []byte("body{color:navy}"))
	add("offline-public/assets/logo.png", offline.PublicAsset, "image/png", []byte("public image fixture"))
	policy := offlineTestPolicy()
	policy.BasePath = base
	config := Config{BuildID: runtime, BasePath: base, Assets: files, Offline: &policy, OfflineBuild: &build, Routes: []Route{
		offlineTestRoute("/public", func(c *Context) (Page, error) {
			return Page{Title: "Public", Body: ui.Fragment(ui.El("h1", nil, ui.Text("Saved public HTML")), ui.El("img", ui.Attrs{"src": MustAssetURL(c, "/logo.png"), "alt": "Public"}))}, nil
		}),
		offlineTestRoute("/fallback", func(*Context) (Page, error) {
			return Page{Title: "Offline", Description: "Reconnect later.", Body: ui.Text("not the fallback body")}, nil
		}),
	}}
	return config, files
}
func managedOfflineApp(t *testing.T, config Config) *App {
	t.Helper()
	app, err := New(config)
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
func managedOfflineRequest(app *App, method, path string, header http.Header) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://site.test"+path, nil)
	if header != nil {
		request.Header = header.Clone()
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, request)
	return response
}
func managedOfflinePreparedPath(t *testing.T, bundle *managedOfflineBundle, path string) string {
	t.Helper()
	value, err := bundle.catalog.PreparationURL(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(value, bundle.catalog.Spec().Config.Origin)
}

func TestManagedOfflineNewFreezesInventoryAndAtomicBundle(t *testing.T) {
	config, original := managedOfflineFixture(t, "/reader")
	app := managedOfflineApp(t, config)
	if got := managedOfflineRequest(app, "GET", "/reader"+offline.CatalogPath, nil); got.Code != 503 {
		t.Fatal(got.Code)
	}
	// Caller metadata and FS buffers cannot change the owned startup snapshot.
	config.Offline.Pages[0].Path = "/changed"
	config.OfflineBuild.BridgeTemplate = "changed"
	for _, file := range original {
		file.Data[0] = '!'
	}
	if err := app.PrepareOfflineBundle(context.Background()); err != nil {
		t.Fatal(err)
	}
	bundle := app.managedOffline.published.Load()
	if bundle == nil {
		t.Fatal("no bundle")
	}
	response := managedOfflineRequest(app, "GET", "/reader"+offline.CatalogPath, nil)
	if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), bundle.catalogJSON) {
		t.Fatal(response.Code, response.Body.String())
	}
	catalog, err := offline.ParseCatalog(response.Body.Bytes())
	if err != nil || catalog.Generation() != bundle.catalog.Generation() {
		t.Fatal(err)
	}
	var descriptor offline.WorkerDescriptor
	script := strings.TrimSuffix(strings.TrimPrefix(string(bundle.script), "const descriptor="), ";")
	if err := json.Unmarshal([]byte(script), &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.CatalogJSON != string(bundle.catalogJSON) || descriptor.CatalogSHA256 != offlineDigest(bundle.catalogJSON) || descriptor.Generation != catalog.Generation() || descriptor.Namespace != app.managedOffline.policy.StorageNamespace() {
		t.Fatal("bootstrap/catalog differ")
	}
	if descriptor.Client.Scope != "https://site.test/reader" {
		t.Fatal(descriptor.Client.Scope)
	}
	response = managedOfflineRequest(app, "HEAD", "/reader"+offline.WorkerScriptPath, nil)
	if response.Code != 200 || response.Body.Len() != 0 || response.Header().Get("Service-Worker-Allowed") != "/reader" || response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" {
		t.Fatal(response.Code, response.Header())
	}
	for _, asset := range catalog.Spec().Assets {
		path := strings.TrimPrefix(asset.URL, "https://site.test")
		response = managedOfflineRequest(app, "GET", path, nil)
		if response.Code != 200 || response.Header().Get("Content-Type") != asset.ContentType || offlineDigest(response.Body.Bytes()) != asset.SHA256 {
			t.Fatal(path, response.Code, response.Header())
		}
	}
	response = managedOfflineRequest(app, "GET", managedOfflinePreparedPath(t, bundle, "/public"), nil)
	if response.Code != 200 || response.Header().Get(offlineAdmissionHeader) != offline.NativeAdmissionV1 || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get(offlineDocumentHeader) != offlineDigest(response.Body.Bytes()) || response.Header().Get(navigation.ConfigurationHeader) != app.configurationID || !strings.Contains(response.Body.String(), `data-nextgo-offline-saved="1"`) || strings.Contains(response.Body.String(), `src="/reader/assets/`) {
		t.Fatal(response.Code, response.Header(), response.Body.String())
	}
	plain := managedOfflineRequest(app, "GET", "/reader/public", nil)
	if plain.Code != 200 || strings.Contains(plain.Body.String(), `data-nextgo-offline-saved=`) || !strings.Contains(plain.Body.String(), `data-nextgo-offline=`) || !strings.Contains(plain.Body.String(), `src="/reader/assets/logo.png"`) {
		t.Fatal(plain.Code, plain.Body.String())
	}
	fallback := managedOfflineRequest(app, "GET", managedOfflinePreparedPath(t, bundle, "/fallback"), nil)
	if fallback.Code != 200 || strings.Contains(fallback.Body.String(), "data-nextgo") || strings.Contains(fallback.Body.String(), "not the fallback body") {
		t.Fatal(fallback.Code, fallback.Body.String())
	}
	prior := bundle.catalog.Generation()
	if err := app.PrepareOfflineBundle(context.Background()); err != nil || app.managedOffline.published.Load().catalog.Generation() != prior {
		t.Fatal("non-idempotent ready call", err)
	}
}

func TestManagedOfflineRejectsInvalidConfigAndAssetBytes(t *testing.T) {
	for _, kind := range []string{"missing-policy", "missing-build", "wrong-runtime", "wrong-digest", "missing-file", "directory", "truncated", "bad-wasm", "budget", "base", "phase", "unlisted-page", "handler-shadow", "document-handler", "document-route", "rewrite"} {
		t.Run(kind, func(t *testing.T) {
			config, files := managedOfflineFixture(t, "")
			switch kind {
			case "missing-policy":
				config.Offline = nil
			case "missing-build":
				config.OfflineBuild = nil
			case "wrong-runtime":
				config.OfflineBuild.RuntimeID = strings.Repeat("b", 20)
			case "wrong-digest":
				config.OfflineBuild.Files[0].SHA256 = strings.Repeat("b", 64)
			case "missing-file":
				delete(files, config.OfflineBuild.Files[0].Path)
			case "directory":
				files[config.OfflineBuild.Files[0].Path].Mode = fs.ModeDir
			case "truncated":
				files[config.OfflineBuild.Files[0].Path].Data = nil
			case "bad-wasm":
				record := &config.OfflineBuild.Files[0]
				files[record.Path].Data = []byte("not-wasm")
				record.SHA256 = offlineDigest(files[record.Path].Data)
				record.DecodedBytes = int64(len(files[record.Path].Data))
			case "budget":
				config.Offline.Budget.AssetBytes = 1
			case "base":
				config.Offline.BasePath = "/other"
			case "phase":
				config.Phase = PhaseStaticExport
			case "unlisted-page":
				config.Offline.Pages[0].Path = "/missing"
			case "document-handler":
				config.Handlers = map[string]http.Handler{offline.PreparedDocumentsPath: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("document handler ran") })}
			case "document-route":
				config.Routes = append(config.Routes, Route{Pattern: offline.PreparedDocumentsPath + ":generation/:hash", API: func(http.ResponseWriter, *http.Request) { t.Error("document API ran") }})
			case "handler-shadow":
				config.Handlers = map[string]http.Handler{"/public": http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler ran during New") })}
			case "rewrite":
				config.Rewrites = []RewriteRule{{Source: "/public", Destination: "/fallback"}}
			}
			app, err := New(config)
			if err == nil {
				_ = app.Close(context.Background())
				t.Fatal("invalid offline configuration accepted")
			}
		})
	}
}

func TestManagedOfflineServiceNameCannotBeRuntimeID(t *testing.T) {
	config, _ := managedOfflineFixture(t, "")
	config.Offline, config.OfflineBuild, config.BuildID = nil, nil, "offline"
	app, err := New(config)
	if err == nil {
		_ = app.Close(context.Background())
		t.Fatal("service name accepted as runtime ID")
	}
}

func TestManagedOfflinePreparedRequestsRejectCredentialsVariantsAndAPIs(t *testing.T) {
	config, _ := managedOfflineFixture(t, "")
	var calls atomic.Int32
	config.Routes = append(config.Routes, Route{Pattern: "/api/health", API: func(w http.ResponseWriter, r *http.Request) { calls.Add(1); _, _ = w.Write([]byte("health")) }})
	config.NotFound = func(*Context) (Page, error) { calls.Add(1); return Page{Body: ui.Text("not found")}, nil }
	app := managedOfflineApp(t, config)
	if err := app.PrepareOfflineBundle(context.Background()); err != nil {
		t.Fatal(err)
	}
	bundle := app.managedOffline.published.Load()
	originalPath := managedOfflinePreparedPath(t, bundle, "/public")
	for _, kind := range []string{"cookie", "lowercase-cookie", "authorization", "range", "query", "spa", "stream", "prefetch", "generation", "digest", "extra-segment", "encoded", "encoded-root", "encoded-namespace", "encoded-separator", "legacy-selector", "request-generation", "api", "post", "body", "host"} {
		t.Run(kind, func(t *testing.T) {
			path := originalPath
			header := make(http.Header)
			method := "GET"
			var body *strings.Reader
			switch kind {
			case "cookie":
				header.Set("Cookie", "session=private")
			case "lowercase-cookie":
				header["cookie"] = []string{""}
			case "authorization":
				header.Set("Authorization", "Bearer private")
			case "range":
				header.Set("Range", "bytes=0-1")
			case "query":
				path += "?private=1"
			case "spa":
				header.Set("X-Nextgo-Navigate", "1")
			case "stream":
				header.Set(navigation.StreamHeader, "1")
			case "prefetch":
				header.Set(navigation.PurposeHeader, "prefetch")
			case "generation":
				path = strings.Replace(path, bundle.catalog.Generation(), strings.Repeat("b", 64), 1)
			case "digest":
				path = path[:strings.LastIndex(path, "/")+1] + strings.Repeat("b", 64)
			case "extra-segment":
				path += "/extra"
			case "encoded":
				path = strings.Replace(path, "/documents/", "/%64ocuments/", 1)
			case "encoded-root":
				path = strings.Replace(path, "/_nextgo/", "/%5fnextgo/", 1)
			case "encoded-namespace":
				path = strings.Replace(path, "/offline/", "/%6fffline/", 1)
			case "encoded-separator":
				path = strings.Replace(path, "/_nextgo/offline/", "/_nextgo%2Foffline/", 1)
			case "legacy-selector":
				header.Set("X-Nextgo-Offline-Prepare", "1")
			case "request-generation":
				header["x-nextgo-offline-generation"] = []string{bundle.catalog.Generation()}
			case "api":
				path = offline.PreparedDocumentsPath + "api/health"
			case "post":
				method = "POST"
			case "body":
				body = strings.NewReader("private")
			}
			var request *http.Request
			if body == nil {
				request = httptest.NewRequest(method, "https://site.test"+path, nil)
			} else {
				request = httptest.NewRequest(method, "https://site.test"+path, body)
			}
			request.Header = header
			if kind == "host" {
				request.Host = "other.test"
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code < 400 || calls.Load() != 0 || response.Header().Get(offlineAdmissionHeader) != "" {
				t.Fatal(response.Code, response.Header(), calls.Load())
			}
		})
	}
	if got := managedOfflineRequest(app, "GET", "/api/health", nil); got.Code != 200 || calls.Load() != 1 {
		t.Fatal(got.Code, calls.Load())
	}
	// Old selector-shaped headers never select saved bytes at an original URL.
	legacy := http.Header{"X-Nextgo-Offline-Prepare": {"1"}, offlineGenerationHeader: {bundle.catalog.Generation()}}
	normal := managedOfflineRequest(app, "GET", "/public", legacy)
	if normal.Code != 200 || normal.Header().Get(offlineAdmissionHeader) != "" || strings.Contains(normal.Body.String(), `data-nextgo-offline-saved=`) || strings.Contains(strings.Join(normal.Header().Values("Vary"), ","), "X-Nextgo-Offline") {
		t.Fatal("normal URL became a prepared representation", normal.Code, normal.Header())
	}
	prepared := managedOfflineRequest(app, "GET", originalPath, nil)
	if prepared.Code != 200 || prepared.Header().Get("Cache-Control") != "no-store" || !strings.Contains(prepared.Body.String(), `data-nextgo-offline-saved="1"`) {
		t.Fatal(prepared.Code, prepared.Header())
	}
}

func TestManagedOfflineFailurePublishesNothingAndWaitingCancellation(t *testing.T) {
	for _, kind := range []string{"cookie", "unknown-asset", "raw-mutable-image", "remote-image", "middleware-fallback-script"} {
		t.Run(kind, func(t *testing.T) {
			config, _ := managedOfflineFixture(t, "")
			config.Routes[0].Page = func(c *Context) (Page, error) {
				switch kind {
				case "cookie":
					_ = c.SetCookie(&http.Cookie{Name: "session", Value: "private"})
				case "unknown-asset":
					_, _ = AssetURL(c, "/missing.png")
				case "raw-mutable-image":
					return Page{Body: ui.El("img", ui.Attrs{"src": "/assets/logo.png"})}, nil
				case "remote-image":
					return Page{Body: ui.El("img", ui.Attrs{"src": "https://foreign.invalid/p.png"})}, nil
				}
				return Page{Body: ui.Text("public")}, nil
			}
			if kind == "middleware-fallback-script" {
				config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						next.ServeHTTP(w, r)
						if r.URL.Path == "/fallback" {
							_, _ = w.Write([]byte(`<script src="/assets/x.js"></script>`))
						}
					})
				}}
			}
			app := managedOfflineApp(t, config)
			if err := app.PrepareOfflineBundle(context.Background()); err == nil {
				t.Fatal("invalid bundle published")
			}
			if app.managedOffline.published.Load() != nil {
				t.Fatal("partial bundle published")
			}
			if got := managedOfflineRequest(app, "GET", offline.CatalogPath, nil); got.Code != 503 {
				t.Fatal(got.Code)
			}
		})
	}
	config, _ := managedOfflineFixture(t, "")
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	config.Routes[0].Page = func(*Context) (Page, error) {
		calls.Add(1)
		close(started)
		<-release
		return Page{Body: ui.Text("public")}, nil
	}
	app := managedOfflineApp(t, config)
	done := make(chan error, 1)
	go func() { done <- app.PrepareOfflineBundle(context.Background()) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := app.PrepareOfflineBundle(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if app.managedOffline.published.Load() != nil {
		t.Fatal("published while loader active")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
}

func TestManagedOfflineMountedRootCanonicalDocumentAndPolicyIdentity(t *testing.T) {
	config, _ := managedOfflineFixture(t, "/reader")
	config.Offline.Pages[0].Path = "/"
	config.Routes[0].Pattern = "/"
	app := managedOfflineApp(t, config)
	if err := app.PrepareOfflineBundle(context.Background()); err != nil {
		t.Fatal(err)
	}
	bundle := app.managedOffline.published.Load()
	response := managedOfflineRequest(app, "GET", managedOfflinePreparedPath(t, bundle, "/"), nil)
	if response.Code != 200 || response.Header().Get("Location") != "" {
		t.Fatal(response.Code, response.Header())
	}
	seen := false
	for _, entry := range bundle.catalog.Entries() {
		if entry.Kind == "document" {
			if entry.URL != "https://site.test/reader" {
				t.Fatal(entry.URL)
			}
			seen = true
		}
	}
	if !seen {
		t.Fatal("root document missing")
	}
	if got := managedOfflineRequest(app, "GET", managedOfflinePreparedPath(t, bundle, "/")+"/", nil); got.Code < 400 {
		t.Fatal("root alias served saved bytes", got.Code)
	}
	other, _ := managedOfflineFixture(t, "/reader")
	other.Offline.Pages[0].Path = "/"
	other.Routes[0].Pattern = "/"
	other.Offline.Pages[0].MaxAge = 2 * time.Hour
	changed := managedOfflineApp(t, other)
	if changed.configurationID == app.configurationID {
		t.Fatal("offline policy did not bind configuration")
	}
}

func TestManagedOfflineSavedHTMLResourcesAndRawTextAreClosed(t *testing.T) {
	config, _ := managedOfflineFixture(t, "")
	structured, err := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "Thing", "name": "<safe public text>"})
	if err != nil {
		t.Fatal(err)
	}
	config.Routes[0].Page = func(c *Context) (Page, error) {
		logo := MustAssetURL(c, "/logo.png")
		return Page{Title: "Public", JSONLD: []seo.StructuredData{structured}, Resources: []assets.Resource{assets.Stylesheet("/assets/style.css")}, Body: ui.Fragment(ui.El("h1", nil, ui.Text("Safe")), ui.El("br", nil), ui.El("input", ui.Attrs{"type": "checkbox", "checked": ""}), ui.El("source", ui.Attrs{"srcset": logo + " 1w"}), ui.El("img", ui.Attrs{"src": logo, "alt": "Public"}), ui.El("a", ui.Attrs{"href": "https://outside.invalid/"}, ui.Text("Ordinary link")))}, nil
	}
	app := managedOfflineApp(t, config)
	prepared, err := app.prepareOffline(context.Background(), app.managedOffline.policy.Config(), &offlinePrepareOptions{bundle: app.managedOffline})
	if err != nil {
		t.Fatal(err)
	}
	var original string
	for _, document := range prepared.Documents() {
		if !document.Fallback {
			original = string(document.HTML)
		}
	}
	if original == "" || app.validateManagedOfflineHTML([]byte(original)) != nil {
		t.Fatal("valid framework HTML rejected")
	}
	for name, transform := range map[string]func(string) string{
		"foreign-fetch": func(s string) string {
			return strings.Replace(s, "</body>", `<svg><image href="https://outside.invalid/x"></image></svg></body>`, 1)
		},
		"body-cdata": func(s string) string {
			return strings.Replace(s, "</body>", `<![CDATA[foo> <img src="https://outside.invalid/x">]]></body>`, 1)
		},
		"style-comment": func(s string) string {
			return strings.Replace(s, "<style>", `<style><!-- @import "https://outside.invalid/x.css"; -->`, 1)
		},
		"style-cdata": func(s string) string {
			return strings.Replace(s, "<style>", `<style><![CDATA[ @import "https://outside.invalid/x.css"; ]]>`, 1)
		},
		"script-cdata": func(s string) string {
			return strings.Replace(s, "</script>", `<![CDATA[</script><img src="https://outside.invalid/x">]]></script>`, 1)
		},
		"script-comment": func(s string) string {
			return strings.Replace(s, "</script>", `<!--</script><img src="https://outside.invalid/x">--></script>`, 1)
		},
		"inline-style": func(s string) string {
			return strings.Replace(s, "<h1>", `<h1 style="background:url(https://outside.invalid/x)">`, 1)
		},
		"srcdoc": func(s string) string {
			return strings.Replace(s, "</body>", `<iframe srcdoc="private"></iframe></body>`, 1)
		},
		"refresh": func(s string) string {
			return strings.Replace(s, "</body>", `<meta http-equiv="refresh" content="0;url=https://outside.invalid/"></body>`, 1)
		},
		"mutable-srcset": func(s string) string {
			return strings.Replace(s, "</body>", `<img srcset="/assets/logo.png 1w"></body>`, 1)
		},
		"unknown-preload": func(s string) string {
			return strings.Replace(s, "</head>", `<link rel="preload" href="/custom.css"></head>`, 1)
		},
		"speculation": func(s string) string {
			return strings.Replace(s, "</head>", `<link rel="prefetch" href="/api/private"></head>`, 1)
		},
		"wrong-marker": func(s string) string {
			return strings.Replace(s, `data-nextgo-offline-saved="1"`, `data-nextgo-offline-saved="2"`, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := app.validateManagedOfflineHTML([]byte(transform(original))); !errors.Is(err, ErrOfflineAdmission) {
				t.Fatal("unsupported saved resource accepted", err)
			}
		})
	}
	config, _ = managedOfflineFixture(t, "")
	var handlerCalls atomic.Int32
	config.Handlers = map[string]http.Handler{"/custom.css": http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handlerCalls.Add(1) })}
	config.Routes[0].Page = func(*Context) (Page, error) {
		return Page{Body: ui.Text("Public"), Resources: []assets.Resource{assets.Stylesheet("/custom.css")}}, nil
	}
	app = managedOfflineApp(t, config)
	if err := app.PrepareOfflineBundle(context.Background()); err == nil || app.managedOffline.published.Load() != nil || handlerCalls.Load() != 0 {
		t.Fatal("unlisted handler stylesheet admitted", err, handlerCalls.Load())
	}
}

func TestManagedOfflineRegistrationHelperIsInertAndUsesApplicationPolicies(t *testing.T) {
	for _, base := range []string{"", "/reader"} {
		t.Run(base, func(t *testing.T) {
			config, _ := managedOfflineFixture(t, base)
			var pages, helpers, ordinary atomic.Int32
			page := config.Routes[0].Page
			config.Routes[0].Page = func(c *Context) (Page, error) { pages.Add(1); return page(c) }
			config.Routes = append(config.Routes, Route{Pattern: "/api/health", API: func(w http.ResponseWriter, r *http.Request) {
				ordinary.Add(1)
				w.WriteHeader(201)
				_, _ = io.WriteString(w, "normal")
			}})
			config.Headers = http.Header{"X-Global-Policy": {"normal"}}
			config.HeaderRules = []HeaderRule{
				{Source: offline.RegistrationHelperPath, Headers: []HeaderValue{{Name: "Content-Security-Policy", Value: "worker-src 'none'"}, {Name: "X-Frame-Options", Value: "DENY"}}},
				{Source: offline.RegistrationHelperPath, Has: []RuleCondition{{Type: HeaderCondition, Key: "X-Session"}}, Headers: []HeaderValue{{Name: "X-Sensitive-Rule", Value: "applied"}}},
			}
			config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == offline.RegistrationHelperPath {
						helpers.Add(1)
						if w.Header().Get("Content-Security-Policy") != "worker-src 'none'" || w.Header().Get("X-Global-Policy") != "normal" {
							t.Error("helper bypassed application header rules")
						}
						w.Header().Set("Cache-Control", "public, max-age=999")
						if r.Header.Get("Cookie") != "" {
							http.SetCookie(w, &http.Cookie{Name: "session", Value: "rotated", HttpOnly: true, Secure: true})
						}
					}
					next.ServeHTTP(w, r)
				})
			}}
			app := managedOfflineApp(t, config)
			path := base + offline.RegistrationHelperPath
			before := managedOfflineRequest(app, "GET", path, nil)
			if before.Code != 503 || helpers.Load() != 1 || pages.Load() != 0 || before.Header().Get("Set-Cookie") != "" {
				t.Fatal("unready helper bypassed policy or ran a loader", before.Code, before.Header(), helpers.Load(), pages.Load())
			}
			if err := app.PrepareOfflineBundle(context.Background()); err != nil {
				t.Fatal(err)
			}
			pages.Store(0)
			for _, tc := range []struct {
				method  string
				session bool
			}{{"GET", false}, {"HEAD", false}, {"GET", true}} {
				header := http.Header{"Accept-Encoding": {"gzip"}, "X-Session": {"present"}, "If-None-Match": {"*"}, "If-Modified-Since": {"Wed, 21 Oct 2015 07:28:00 GMT"}}
				if tc.session {
					header.Set("Cookie", "session=old")
					header.Set("Authorization", "Bearer private")
				}
				response := managedOfflineRequest(app, tc.method, path, header)
				if response.Code != 200 || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Content-Length") != fmt.Sprint(len(offlineRegistrationHelperHTML)) || response.Header().Get("Cache-Control") != "no-store, no-transform" || response.Header().Get("Content-Encoding") != "" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("X-Frame-Options") != "DENY" || response.Header().Get("X-Sensitive-Rule") != "applied" {
					t.Fatal(tc, response.Code, response.Header())
				}
				if tc.method == "HEAD" && response.Body.Len() != 0 || tc.method == "GET" && response.Body.String() != offlineRegistrationHelperHTML {
					t.Fatal(tc, response.Body.String())
				}
				policies := response.Header().Values("Content-Security-Policy")
				if len(policies) != 2 || policies[0] != "worker-src 'none'" || !strings.Contains(policies[1], "worker-src https://site.test"+base+offline.WorkerScriptPath+";") || !strings.Contains(policies[1], "script-src 'none'") {
					t.Fatal("application/helper CSP policies did not intersect", policies)
				}
				cookies := response.Header().Values("Set-Cookie")
				if tc.session && (len(cookies) != 1 || cookies[0] != "session=rotated; HttpOnly; Secure") || !tc.session && len(cookies) != 0 {
					t.Fatal("middleware session refresh dropped or helper originated cookie", cookies)
				}
				for _, name := range []string{"ETag", "Last-Modified", "Location", "Refresh", "Content-Disposition", offlineAdmissionHeader, offlineDocumentHeader, offlineGenerationHeader} {
					if response.Header().Get(name) != "" {
						t.Fatal("helper inherited variant/navigation data", name, response.Header())
					}
				}
			}
			if pages.Load() != 0 {
				t.Fatal("helper ran page loader", pages.Load())
			}
			for _, entry := range app.managedOffline.published.Load().catalog.Entries() {
				if strings.Contains(entry.URL, offline.RegistrationHelperPath) {
					t.Fatal("helper entered persistent catalog", entry)
				}
			}
			if _, exists := app.managedOffline.files[path]; exists {
				t.Fatal("helper became a compiler/runtime asset")
			}
			plain := managedOfflineRequest(app, "GET", base+"/api/health", nil)
			if plain.Code != 201 || plain.Body.String() != "normal" || ordinary.Load() != 1 || plain.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" || plain.Header().Get("Content-Security-Policy") != "" {
				t.Fatal("ordinary middleware/handler behavior changed", plain.Code, plain.Header(), plain.Body.String())
			}
			prior := helpers.Load()
			if got := managedOfflineRequest(app, "GET", base+offline.CatalogPath, nil); got.Code != 200 || helpers.Load() != prior {
				t.Fatal("existing early service path changed", got.Code, helpers.Load())
			}
		})
	}
}

func TestManagedOfflineRegistrationHelperRejectsVariantsBeforeAnyLoader(t *testing.T) {
	config, _ := managedOfflineFixture(t, "/reader")
	var pages atomic.Int32
	page := config.Routes[0].Page
	config.Routes[0].Page = func(c *Context) (Page, error) { pages.Add(1); return page(c) }
	config.NotFound = func(*Context) (Page, error) { pages.Add(1); return Page{Body: ui.Text("private not found")}, nil }
	app := managedOfflineApp(t, config)
	if err := app.PrepareOfflineBundle(context.Background()); err != nil {
		t.Fatal(err)
	}
	pages.Store(0)
	path := "/reader" + offline.RegistrationHelperPath
	for _, kind := range []string{"post", "query", "force-query", "trailing", "encoded", "encoded-namespace", "body", "length", "transfer", "trailer", "range", "if-range", "spa", "stream", "purpose", "prepare", "generation", "admission", "document", "host", "scheme", "userinfo", "fragment", "opaque", "invalid-raw-path"} {
		t.Run(kind, func(t *testing.T) {
			request := httptest.NewRequest("GET", "https://site.test"+path, nil)
			switch kind {
			case "post":
				request.Method = "POST"
			case "query":
				request.URL.RawQuery = "private=1"
			case "force-query":
				request.URL.ForceQuery = true
			case "trailing":
				request.URL.Path += "/"
			case "encoded":
				request.URL.RawPath = strings.Replace(path, "registration", "%72egistration", 1)
			case "encoded-namespace":
				request.URL.RawPath = strings.Replace(path, "offline", "%6fffline", 1)
			case "body":
				request.Body = io.NopCloser(strings.NewReader("private"))
			case "length":
				request.ContentLength = 1
			case "transfer":
				request.TransferEncoding = []string{"chunked"}
			case "trailer":
				request.Trailer = http.Header{"X-Private": {"present"}}
			case "range":
				request.Header.Set("Range", "bytes=0-1")
			case "if-range":
				request.Header.Set("If-Range", "tag")
			case "spa":
				request.Header.Set("X-Nextgo-Navigate", "1")
			case "stream":
				request.Header.Set(navigation.StreamHeader, "1")
			case "purpose":
				request.Header.Set(navigation.PurposeHeader, "prefetch")
			case "prepare":
				request.Header.Set("X-Nextgo-Offline-Prepare", "1")
			case "generation":
				request.Header.Set(offlineGenerationHeader, "old")
			case "admission":
				request.Header.Set(offlineAdmissionHeader, "old")
			case "document":
				request.Header.Set(offlineDocumentHeader, "old")
			case "host":
				request.Host = "foreign.test"
			case "scheme":
				request.URL.Scheme = "http"
			case "userinfo":
				request.URL.User = url.User("private")
			case "fragment":
				request.URL.Fragment = "private"
			case "opaque":
				request.URL.Opaque = "opaque"
			case "invalid-raw-path":
				request.URL.RawPath = "/invalid%not-an-escape"
			}
			response := httptest.NewRecorder()
			app.ServeHTTP(response, request)
			if response.Code < 400 || strings.Contains(response.Body.String(), "data-nextgo-offline-helper") || pages.Load() != 0 {
				t.Fatal(kind, response.Code, response.Header(), response.Body.String(), pages.Load())
			}
			if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
				t.Fatal("helper refusal cache policy", kind, response.Header())
			}
		})
	}
	for _, kind := range []string{"rewrite", "strip-query", "change-method", "deny", "enter-helper"} {
		t.Run(kind, func(t *testing.T) {
			local, _ := managedOfflineFixture(t, "")
			local.NotFound = func(*Context) (Page, error) { t.Error("middleware mutation entered NotFound"); return Page{}, nil }
			local.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if kind == "enter-helper" && r.URL.Path == "/entered" {
						clone := r.Clone(r.Context())
						clone.URL.Path = offline.RegistrationHelperPath
						clone.URL.RawPath = ""
						next.ServeHTTP(w, clone)
						return
					}
					if r.URL.Path == offline.RegistrationHelperPath {
						clone := r.Clone(r.Context())
						switch kind {
						case "rewrite":
							clone.URL.Path = "/public"
							clone.URL.RawPath = ""
						case "strip-query":
							clone.URL.RawQuery = ""
						case "change-method":
							clone.Method = "HEAD"
						case "deny":
							w.Header().Set("Cache-Control", "public")
							w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
							http.SetCookie(w, &http.Cookie{Name: "session", Value: "refreshed"})
							http.Error(w, "application denied", 403)
							return
						}
						r = clone
					}
					next.ServeHTTP(w, r)
				})
			}}
			app := managedOfflineApp(t, local)
			if err := app.PrepareOfflineBundle(context.Background()); err != nil {
				t.Fatal(err)
			}
			path := offline.RegistrationHelperPath
			if kind == "strip-query" {
				path += "?private=1"
			}
			if kind == "enter-helper" {
				path = "/entered"
			}
			response := managedOfflineRequest(app, "GET", path, http.Header{"Accept-Encoding": {"gzip"}})
			if response.Code < 400 || response.Header().Get("Cache-Control") != "no-store, no-transform" || response.Header().Get("Content-Encoding") != "" || strings.Contains(response.Body.String(), "data-nextgo-offline-helper") {
				t.Fatal(kind, response.Code, response.Header(), response.Body.String())
			}
			if kind == "deny" && (response.Code != 403 || response.Header().Get("Set-Cookie") != "session=refreshed" || response.Header().Values("Content-Security-Policy")[0] != "frame-ancestors 'none'") {
				t.Fatal("application denial/session policy changed", response.Code, response.Header())
			}
		})
	}
}

func TestManagedOfflineRegistrationHelperHasNoDefaultServiceAndCannotCollide(t *testing.T) {
	config, _ := managedOfflineFixture(t, "")
	config.Routes = append(config.Routes, Route{Pattern: "/_nextgo/:service/registration-helper-v1", API: func(http.ResponseWriter, *http.Request) { t.Error("shadowing API ran") }})
	if app, err := New(config); err == nil {
		_ = app.Close(context.Background())
		t.Fatal("dynamic helper collision admitted")
	}
	plain := testApp(t, Config{Routes: []Route{{Pattern: offline.RegistrationHelperPath, API: func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(202)
		_, _ = io.WriteString(w, "ordinary explicit API")
	}}}})
	response := managedOfflineRequest(plain, "GET", offline.RegistrationHelperPath, nil)
	if response.Code != 202 || response.Body.String() != "ordinary explicit API" || response.Header().Get("Content-Security-Policy") != "" {
		t.Fatal("default-off API behavior changed", response.Code, response.Header(), response.Body.String())
	}
}
