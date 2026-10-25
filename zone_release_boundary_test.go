//go:build !js

package nextgo

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/publicorigin"
	"github.com/Suhaibinator/Next.go/internal/releaseassets"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/telemetry"
	"github.com/Suhaibinator/Next.go/ui"
)

const releaseBoundaryRuntime = "0123456789abcdef0123"

// These runtime bytes are deliberately nonexecutable fixtures. This suite tests
// native ownership/admission, not WASM validity or browser compatibility.
func releaseBoundaryConfig(t *testing.T) (ZoneReleaseConfig, fstest.MapFS) {
	t.Helper()
	assets := fstest.MapFS{
		"_nextgo/" + releaseBoundaryRuntime + "/app.wasm":     {Data: []byte("unit-only-wasm-fixture")},
		"_nextgo/" + releaseBoundaryRuntime + "/wasm_exec.js": {Data: []byte("unit-only-paired-shim")},
		"_nextgo/" + releaseBoundaryRuntime + "/boot.js":      {Data: []byte("unit-only-bootstrap")},
		"public/style.css":       {Data: []byte("body{color:navy}")},
		"public/nested/icon.txt": {Data: []byte("old-public-icon")},
	}
	_, digest, err := releaseassets.Freeze(context.Background(), assets, releaseBoundaryRuntime)
	if err != nil {
		t.Fatal(err)
	}
	spec := ZoneReleaseSpec{
		Version: zonecontract.Version, Zone: ZoneBinding{ID: "team", Host: "public.example.test", BasePath: "/team"},
		ReleaseID: "release-one", RuntimeID: releaseBoundaryRuntime, PublicOrigin: "https://public.example.test",
		AssetPrefix: "/team/_nextgo/releases/release-one", AssetsSHA256: digest, TopologySHA256: strings.Repeat("a", 64),
		DocumentPaths: []string{"/child"}, NavigationProtocol: navigation.ProtocolVersion, ActionProtocol: actionwire.Protocol,
	}
	return ZoneReleaseConfig{Zone: ZoneConfig{ID: "team", Host: spec.Zone.Host, Tenant: "tenant-a", Config: Config{Assets: assets}}, Release: spec, BackendHosts: []string{"backend.internal:8080"}}, assets
}

func releaseBoundaryNew(t *testing.T, declaration ZoneReleaseConfig) *ZoneRelease {
	t.Helper()
	release, err := NewZoneRelease(context.Background(), declaration)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := release.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return release
}

func releaseBoundaryRequest(spec ZoneReleaseSpec, method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, "http://backend.internal:8080"+path, body)
	zonecontract.SetExpectation(r.Header, spec.Expectation())
	return r
}

func releaseBoundaryResponse(t *testing.T, release *ZoneRelease, r *http.Request, status int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	release.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: status %d, want %d: %s", r.Method, r.URL, w.Code, status, w.Body.String())
	}
	if err := zonecontract.CheckExpectation(w.Header(), release.Release().Expectation()); err != nil {
		t.Fatalf("response did not identify its actual release: %v", err)
	}
	return w
}

func TestZoneReleaseBoundaryConfigureAndOwnedSnapshot(t *testing.T) {
	declaration, assets := releaseBoundaryConfig(t)
	want := declaration.Release.Clone()
	configured, factories := 0, 0
	declaration.Zone.Configure = func(c *Config) error {
		configured++
		if c.Zone() != (ZoneInfo{ID: "team", Tenant: "tenant-a", Host: want.Zone.Host, BasePath: "/team"}) || c.AssetPrefix != want.AssetPrefix || c.BuildID != want.RuntimeID || c.BasePath != "/team" || !slices.Contains(c.DocumentPaths, "/child") || c.Phase != PhaseProduction {
			t.Fatalf("hook did not receive fixed ownership: zone=%+v paths=%q/%q runtime=%q documents=%v phase=%v", c.Zone(), c.BasePath, c.AssetPrefix, c.BuildID, c.DocumentPaths, c.Phase)
		}
		return nil
	}
	declaration.Zone.Config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler { factories++; return next }}
	release := releaseBoundaryNew(t, declaration)
	if configured != 1 || factories != 1 {
		t.Fatalf("construction count: configure=%d factories=%d", configured, factories)
	}
	old := slices.Clone(assets["public/style.css"].Data)
	assets["public/style.css"].Data[0] = '!'
	delete(assets, "public/nested/icon.txt")
	assets["public/new-secret.txt"] = &fstest.MapFile{Data: []byte("added-after-freeze")}
	declaration.Release.DocumentPaths[0] = "/changed"
	declaration.BackendHosts[0] = "changed.internal"
	copy := release.Release()
	copy.DocumentPaths[0] = "/changed-again"
	if !slices.Contains(release.Release().DocumentPaths, "/child") {
		t.Fatal("caller changed retained descriptor")
	}
	w := releaseBoundaryResponse(t, release, releaseBoundaryRequest(want, "GET", want.AssetPrefix+"/assets/style.css", nil), 200)
	if got, expected := sha256.Sum256(w.Body.Bytes()), sha256.Sum256(old); got != expected {
		t.Fatalf("frozen bytes changed: got %x want %x", got, expected)
	}
	w = releaseBoundaryResponse(t, release, releaseBoundaryRequest(want, "GET", want.AssetPrefix+"/assets/nested/icon.txt", nil), 200)
	if w.Body.String() != "old-public-icon" {
		t.Fatal("deleted source changed retained asset", w.Body.String())
	}
	releaseBoundaryResponse(t, release, releaseBoundaryRequest(want, "GET", want.AssetPrefix+"/assets/new-secret.txt", nil), 404)
}

func TestZoneReleaseBoundaryRejectsBeforeFactories(t *testing.T) {
	changes := map[string]func(*Config){
		"mount":        func(c *Config) { c.BasePath = "/other" },
		"asset-prefix": func(c *Config) { c.AssetPrefix = "/other" },
		"runtime":      func(c *Config) { c.BuildID = "other" },
		"zone": func(c *Config) {
			if err := setConfigZone(c, ZoneInfo{ID: "other", Host: "other.test", BasePath: c.BasePath}); err != nil {
				t.Fatal(err)
			}
		},
		"public-origin": func(c *Config) {
			origin, err := publicorigin.Parse("https://other.test")
			if err != nil {
				t.Fatal(err)
			}
			c.publicOrigin = origin
		},
		"remove-peer-boundary": func(c *Config) { c.DocumentPaths = nil },
		"images":               func(c *Config) { c.Images = http.NotFoundHandler() },
		"metadata-static":      func(c *Config) { c.MetadataFiles.Static = fstest.MapFS{} },
		"metadata-producer": func(c *Config) {
			c.MetadataFiles.Robots = func(context.Context) (seo.RobotsOptions, error) {
				t.Fatal("unsupported producer executed")
				return seo.RobotsOptions{}, nil
			}
		},
		"locales":         func(c *Config) { c.Locales = &LocaleConfig{Codes: []string{"en"}, Default: "en"} },
		"prerender-files": func(c *Config) { c.PrerenderFS = fstest.MapFS{} },
		"prerender-route": func(c *Config) {
			c.Routes = []Route{{Pattern: "/", Page: static("page"), Prerender: &PrerenderPolicy{HostIndependent: true}}}
		},
		"prerender-phase": func(c *Config) { c.Phase = PhasePrerender; c.PrerenderBuildMode = true },
		"development":     func(c *Config) { c.Phase = PhaseDevelopment; c.Dev = true },
	}
	for _, name := range []string{"images", "metadata-static", "metadata-producer", "locales", "prerender-files", "prerender-route", "prerender-phase", "development"} {
		t.Run("initial-"+name, func(t *testing.T) {
			declaration, _ := releaseBoundaryConfig(t)
			factories, hooks := 0, 0
			declaration.Zone.Config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler { factories++; return next }}
			declaration.Zone.Configure = func(*Config) error { hooks++; return nil }
			changes[name](&declaration.Zone.Config)
			if release, err := NewZoneRelease(context.Background(), declaration); err == nil {
				release.Close(context.Background())
				t.Fatal("unsupported initial configuration accepted")
			}
			if factories != 0 || hooks != 0 {
				t.Fatalf("unsupported input executed factories=%d hooks=%d", factories, hooks)
			}
		})
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			declaration, _ := releaseBoundaryConfig(t)
			factories := 0
			declaration.Zone.Config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler { factories++; return next }}
			declaration.Zone.Config.ActionRegistrars = []func(*actions.Router){func(*actions.Router) { factories++ }}
			declaration.Zone.Configure = func(c *Config) error { change(c); return nil }
			if release, err := NewZoneRelease(context.Background(), declaration); err == nil {
				release.Close(context.Background())
				t.Fatal("unsupported or changed ownership accepted")
			}
			if factories != 0 {
				t.Fatal("application factory ran before release validation", factories)
			}
		})
	}
	for _, initial := range []bool{true, false} {
		t.Run(fmt.Sprintf("asset-digest-initial-%t", initial), func(t *testing.T) {
			declaration, _ := releaseBoundaryConfig(t)
			factories := 0
			declaration.Zone.Config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler { factories++; return next }}
			if initial {
				declaration.Release.AssetsSHA256 = strings.Repeat("b", 64)
			} else {
				declaration.Zone.Configure = func(c *Config) error {
					c.Assets = fstest.MapFS{"private/secret": {Data: []byte("private")}}
					return nil
				}
			}
			if release, err := NewZoneRelease(context.Background(), declaration); err == nil {
				release.Close(context.Background())
				t.Fatal("wrong asset inventory accepted")
			}
			if factories != 0 {
				t.Fatal("application initialized before asset verification")
			}
		})
	}
}

func TestZoneReleaseBoundaryProjectsPublicOriginWithoutTransportInference(t *testing.T) {
	for _, tlsPresent := range []bool{false, true} {
		t.Run(fmt.Sprintf("backend-tls-%t", tlsPresent), func(t *testing.T) {
			declaration, _ := releaseBoundaryConfig(t)
			var observed *http.Request
			declaration.Zone.Config.Routes = []Route{{Pattern: "/api/observe", API: func(w http.ResponseWriter, r *http.Request) {
				observed = r
				w.Header().Set(zonecontract.HeaderReleaseID, "application-spoof")
				w.WriteHeader(204)
			}}}
			release := releaseBoundaryNew(t, declaration)
			r := releaseBoundaryRequest(declaration.Release, "GET", "/team/api/observe?kept=1", nil)
			r.RemoteAddr = "192.0.2.4:55001"
			if tlsPresent {
				r.TLS = &tls.ConnectionState{ServerName: "backend.internal"}
			}
			transportTLS := r.TLS
			r.Header.Set("Forwarded", "host=evil.test;proto=http")
			r.Header.Set("X-Forwarded-Host", "evil.test")
			r.Header.Set("X-Forwarded-Proto", "http")
			releaseBoundaryResponse(t, release, r, 204)
			if observed == nil {
				t.Fatal("application not reached")
			}
			if observed.Host != "public.example.test" || observed.URL.Host != "public.example.test" || observed.URL.Scheme != "https" || PublicOrigin(observed.Context()) != declaration.Release.PublicOrigin || CurrentZone(observed.Context()).Tenant != "tenant-a" {
				t.Fatalf("wrong public ownership: host=%q url=%s origin=%q zone=%+v", observed.Host, observed.URL, PublicOrigin(observed.Context()), CurrentZone(observed.Context()))
			}
			if observed.TLS != transportTLS || observed.RemoteAddr != r.RemoteAddr || observed.URL.RawQuery != "kept=1" {
				t.Fatal("projection replaced actual transport or query")
			}
			for name := range observed.Header {
				if strings.HasPrefix(strings.ToLower(name), "x-nextgo-zone-") {
					t.Fatal("private identity entered application", name)
				}
			}
			if r.Host != "backend.internal:8080" || r.URL.Scheme != "http" || r.URL.Host != "backend.internal:8080" {
				t.Fatal("endpoint mutated caller request")
			}
		})
	}
}

type releaseBoundaryBody struct{ reads int }

func (r *releaseBoundaryBody) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }
func (*releaseBoundaryBody) Close() error               { return nil }

func TestZoneReleaseBoundaryAdmissionPrecedesBodyAndApplication(t *testing.T) {
	declaration, _ := releaseBoundaryConfig(t)
	calls := 0
	declaration.Zone.Config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; next.ServeHTTP(w, r) })
	}}
	declaration.Zone.Config.Routes = []Route{{Pattern: "/api/change", API: func(w http.ResponseWriter, r *http.Request) { calls++; io.Copy(io.Discard, r.Body); w.WriteHeader(204) }}}
	release := releaseBoundaryNew(t, declaration)
	checks := []struct {
		name   string
		status int
		change func(*http.Request)
	}{
		{"missing", 409, func(r *http.Request) { r.Header.Del(zonecontract.HeaderReleaseID) }},
		{"duplicate", 409, func(r *http.Request) { r.Header.Add(zonecontract.HeaderReleaseID, declaration.Release.ReleaseID) }},
		{"case-alias", 409, func(r *http.Request) { r.Header[strings.ToLower(zonecontract.HeaderZoneID)] = []string{"team"} }},
		{"wrong-release", 409, func(r *http.Request) { r.Header.Set(zonecontract.HeaderReleaseID, "release-other") }},
		{"wrong-topology", 409, func(r *http.Request) { r.Header.Set(zonecontract.HeaderTopology, strings.Repeat("b", 64)) }},
		{"wrong-protocol", 409, func(r *http.Request) { r.Header.Set(zonecontract.HeaderProtocol, "99") }},
		{"wrong-zone", 409, func(r *http.Request) { r.Header.Set(zonecontract.HeaderZoneID, "other") }},
		{"forwarded-backend", 421, func(r *http.Request) {
			r.Host = "attacker.test"
			r.Header.Set("X-Forwarded-Host", "backend.internal:8080")
		}},
		{"absolute-authority", 400, func(r *http.Request) { r.URL.Host = "other.internal:8080" }},
		{"outside-mount", 404, func(r *http.Request) { r.URL.Path = "/other/api/change"; r.URL.RawPath = "" }},
		{"peer-mount", 404, func(r *http.Request) { r.URL.Path = "/team/child/api/change"; r.URL.RawPath = "" }},
		{"encoded-mount", 404, func(r *http.Request) { r.URL.RawPath = "/%74eam/api/change" }},
		{"upgrade", 400, func(r *http.Request) { r.Header.Set("Upgrade", "websocket") }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			body := &releaseBoundaryBody{}
			r := releaseBoundaryRequest(declaration.Release, "POST", "/team/api/change", body)
			check.change(r)
			w := releaseBoundaryResponse(t, release, r, check.status)
			if body.reads != 0 || calls != 0 {
				t.Fatalf("rejected request reached application/body: calls=%d reads=%d", calls, body.reads)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("admission error cache policy", w.Header())
			}
		})
	}
}

func TestZoneReleaseBoundaryRetainedAssetsAreExactAndTerminal(t *testing.T) {
	declaration, _ := releaseBoundaryConfig(t)
	calls := 0
	declaration.Zone.Config.Handlers = map[string]http.Handler{"/": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Add("Set-Cookie", "unexpected=1")
		w.Write([]byte("dynamic-parent"))
	})}
	declaration.Zone.Config.Headers = http.Header{"Set-Cookie": []string{"app-cookie=1"}}
	release := releaseBoundaryNew(t, declaration)
	prefix := declaration.Release.AssetPrefix
	for _, path := range []string{prefix + "/assets/style.css", prefix + "/_nextgo/" + releaseBoundaryRuntime + "/app.wasm"} {
		for _, method := range []string{"GET", "HEAD"} {
			w := releaseBoundaryResponse(t, release, releaseBoundaryRequest(declaration.Release, method, path, nil), 200)
			if method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("HEAD returned bytes")
			}
			if len(w.Header().Values("Set-Cookie")) != 0 {
				t.Fatal("asset response emitted cookie")
			}
		}
	}
	for _, path := range []string{prefix, prefix + "/assets/", prefix + "/assets/nested/", prefix + "/_nextgo/" + releaseBoundaryRuntime + "/", prefix + "/api/change", prefix + "/custom", prefix + "/assets/missing", prefix + "/_nextgo/ffffffffffffffffffff/app.wasm", "/team/assets/style.css", "/team/_nextgo/" + releaseBoundaryRuntime + "/app.wasm", "/team/_nextgo/releases/other/assets/style.css"} {
		w := releaseBoundaryResponse(t, release, releaseBoundaryRequest(declaration.Release, "GET", path, nil), 404)
		if strings.Contains(w.Body.String(), "dynamic-parent") {
			t.Fatal("retained path fell through", path)
		}
	}
	// Reserved namespace spellings must agree with the ingress's literal
	// selection, rather than gaining aliases through URL.Path decoding.
	for _, path := range []string{prefix + "/%61ssets/style.css", prefix + "/%5fnextgo/" + releaseBoundaryRuntime + "/app.wasm"} {
		w := releaseBoundaryResponse(t, release, releaseBoundaryRequest(declaration.Release, "GET", path, nil), 404)
		if strings.Contains(w.Body.String(), "dynamic-parent") {
			t.Fatal("encoded retained namespace fell through", path)
		}
	}
	releaseBoundaryResponse(t, release, releaseBoundaryRequest(declaration.Release, "POST", prefix+"/assets/style.css", nil), 405)
	if calls != 0 {
		t.Fatal("retained asset ownership reached dynamic handler", calls)
	}
}

func TestZoneReleaseBoundaryDynamicNotFoundKeepsItsOwner(t *testing.T) {
	declaration, _ := releaseBoundaryConfig(t)
	notFound := 0
	declaration.Zone.Config.NotFound = func(c *Context) (Page, error) {
		notFound++
		if CurrentZone(c.Context).Tenant != "tenant-a" || PublicOrigin(c.Context) != declaration.Release.PublicOrigin {
			t.Fatal("not-found lost release ownership")
		}
		return static("owned-release-not-found")(c)
	}
	release := releaseBoundaryNew(t, declaration)
	w := releaseBoundaryResponse(t, release, releaseBoundaryRequest(declaration.Release, "GET", "/team/missing-page", nil), 404)
	if notFound != 1 || !strings.Contains(w.Body.String(), "owned-release-not-found") {
		t.Fatal("dynamic not-found did not remain in owning application", notFound, w.Body.String())
	}
}

func TestZoneReleaseBoundaryOrdinaryAppClearsForeignReleaseContext(t *testing.T) {
	declaration, _ := releaseBoundaryConfig(t)
	var foreign context.Context
	declaration.Zone.Config.Routes = []Route{{Pattern: "/api/context", API: func(w http.ResponseWriter, r *http.Request) {
		foreign = context.WithoutCancel(r.Context())
		w.WriteHeader(204)
	}}}
	release := releaseBoundaryNew(t, declaration)
	releaseBoundaryResponse(t, release, releaseBoundaryRequest(declaration.Release, "GET", "/team/api/context", nil), 204)
	if foreign == nil || PublicOrigin(foreign) == "" || CurrentZone(foreign).ID == "" {
		t.Fatal("fixture did not obtain actual release context")
	}
	startup, requests := 0, 0
	plain, err := NewContext(foreign, Config{MetadataFiles: metadatafiles.Sources{Robots: func(ctx context.Context) (seo.RobotsOptions, error) {
		startup++
		if PublicOrigin(ctx) != "" || CurrentZone(ctx) != (ZoneInfo{}) {
			t.Fatal("ordinary startup inherited release identity")
		}
		return seo.RobotsOptions{}, nil
	}}, Routes: []Route{{Pattern: "/api/context", API: func(w http.ResponseWriter, r *http.Request) {
		requests++
		if PublicOrigin(r.Context()) != "" || CurrentZone(r.Context()) != (ZoneInfo{}) {
			t.Fatal("ordinary request inherited release identity")
		}
		origin, err := publicorigin.Request(r)
		if err != nil || origin.String() != "http://ordinary.test" {
			t.Fatal("ordinary origin fallback used foreign declaration", origin, err)
		}
		w.WriteHeader(204)
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := plain.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	w := httptest.NewRecorder()
	plain.ServeHTTP(w, httptest.NewRequest("GET", "http://ordinary.test/api/context", nil).WithContext(foreign))
	if w.Code != 204 || startup != 1 || requests != 1 {
		t.Fatal("ordinary app not exercised", w.Code, startup, requests)
	}
}

func TestZoneReleaseBoundaryActionsUseDeclaredHTTPSOrigin(t *testing.T) {
	declaration, _ := releaseBoundaryConfig(t)
	effects := 0
	declaration.Zone.Config.ActionRegistrars = []func(*actions.Router){func(router *actions.Router) {
		router.Handle("change", func(c *actions.Context) (actions.Result, error) {
			effects++
			if c.Request.TLS != nil || c.Request.Host != declaration.Release.Zone.Host || PublicOrigin(c.Context) != declaration.Release.PublicOrigin {
				t.Fatal("action confused declared public HTTPS with backend HTTP transport")
			}
			return actions.Result{Data: "changed"}, nil
		})
	}}
	release := releaseBoundaryNew(t, declaration)
	for _, check := range []struct {
		origin string
		status int
	}{
		{declaration.Release.PublicOrigin, 200},
		{"http://backend.internal:8080", 403},
		{"http://public.example.test", 403},
		{"https://evil.test", 403},
	} {
		r := releaseBoundaryRequest(declaration.Release, "POST", "/team/_nextgo/actions/change", strings.NewReader(`{}`))
		r.Header.Set("Origin", check.origin)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-Host", "evil.test")
		r.Header.Set("X-Forwarded-Proto", "https")
		releaseBoundaryResponse(t, release, r, check.status)
	}
	if effects != 1 {
		t.Fatal("rejected action origin reached mutation", effects)
	}
}

func TestZoneReleaseBoundaryPublicHelpersRebindOriginAndClearForeignScope(t *testing.T) {
	type privateInput struct{}
	foreignOrigin, err := publicorigin.Parse("https://foreign.test")
	if err != nil {
		t.Fatal(err)
	}
	foreign := publicorigin.WithContext(withZoneContext(context.Background(), ZoneInfo{ID: "foreign", Host: "foreign.test"}), foreignOrigin)
	foreign = context.WithValue(foreign, privateInput{}, "must-not-enter-public-shell")
	ctx, cancel := context.WithCancel(foreign)
	defer cancel()
	declaration, _ := releaseBoundaryConfig(t)
	wantZone := ZoneInfo{ID: "team", Tenant: "tenant-a", Host: declaration.Release.Zone.Host, BasePath: "/team"}
	makeRoute := func(wantOrigin string, zone ZoneInfo, enumerations *int) Route {
		check := func(ctx context.Context) {
			t.Helper()
			if PublicOrigin(ctx) != wantOrigin || CurrentZone(ctx) != zone {
				t.Fatalf("public helper retained foreign deployment: origin=%q zone=%+v; want origin=%q zone=%+v", PublicOrigin(ctx), CurrentZone(ctx), wantOrigin, zone)
			}
		}
		return Route{Pattern: "/:slug", StaticParams: func(ctx context.Context) ([]Params, error) {
			*enumerations++
			check(ctx)
			return []Params{{"slug": "one"}}, nil
		}, Page: func(c *Context) (Page, error) {
			check(c.Context)
			check(c.Request.Context())
			return Page{Body: ui.Text("scoped-public-snapshot")}, nil
		}, Regions: map[string]RegionFunc{"public": func(c *Context, _ json.RawMessage) (ui.Node, error) {
			check(c.Context)
			check(c.Request.Context())
			return ui.Text("scoped-region"), nil
		}}}
	}
	releaseEnumerations, ordinaryEnumerations := 0, 0
	releaseRoute := makeRoute(declaration.Release.PublicOrigin, wantZone, &releaseEnumerations)
	declaration.Zone.Config.Routes = []Route{releaseRoute}
	release := releaseBoundaryNew(t, declaration)
	ordinaryRoute := makeRoute("", ZoneInfo{}, &ordinaryEnumerations)
	ordinary, err := NewContext(ctx, Config{Routes: []Route{ordinaryRoute}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := ordinary.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, check := range []struct {
		name, origin string
		zone         ZoneInfo
		app          *App
		route        Route
	}{
		{"release", declaration.Release.PublicOrigin, wantZone, release.app, releaseRoute},
		{"ordinary", "", ZoneInfo{}, ordinary, ordinaryRoute},
	} {
		t.Run(check.name, func(t *testing.T) {
			paths, err := check.app.StaticPaths(ctx)
			if err != nil || !slices.Equal(paths, []string{"/one"}) {
				t.Fatal("static enumerator did not execute with owning origin", paths, err)
			}
			public := check.app.publicShellContext(ctx)
			if PublicOrigin(public) != check.origin || CurrentZone(public) != check.zone || public.Value(privateInput{}) != nil {
				t.Fatal("public shell lost own deployment or retained caller data", PublicOrigin(public), CurrentZone(public))
			}
			input := httptest.NewRequest("GET", "https://foreign.test/one?private=1", nil).WithContext(ctx)
			clean := check.app.publicShellRequest(input, public)
			if PublicOrigin(clean.Context()) != check.origin || CurrentZone(clean.Context()) != check.zone || clean.URL.RawQuery != "" {
				t.Fatal("public request lost scoped origin or retained private query")
			}
			// Full-page snapshot rendering and late region resolution each rebind
			// ownership even when initiated from an unrelated request context.
			snapshotRoute := check.route
			snapshotRoute.Regions = nil
			value, err := check.app.snapshotPage(ctx, input, map[string]string{"slug": "one"}, nil, snapshotRoute)
			if err != nil || value.HTML != "scoped-public-snapshot" {
				t.Fatal("snapshot failed scoped rendering", value.HTML, err)
			}
			resolver := check.app.requestRegionResolver(&Context{Context: ctx, Request: input}, check.route)
			if _, err := resolver(ctx, "public", nil); err != nil {
				t.Fatal("region resolution failed", err)
			}
		})
	}
	if releaseEnumerations != 1 || ordinaryEnumerations != 1 {
		t.Fatal("static origin assertions were not exercised", releaseEnumerations, ordinaryEnumerations)
	}
	cancel()
	if release.app.publicShellContext(ctx).Err() != context.Canceled {
		t.Fatal("public scope rebind lost cancellation")
	}
}

func TestZoneReleaseBoundaryLifecycleRebindsOriginAndPreservesDeadline(t *testing.T) {
	declaration, _ := releaseBoundaryConfig(t)
	want := ZoneInfo{ID: "team", Tenant: "tenant-a", Host: declaration.Release.Zone.Host, BasePath: "/team"}
	foreignOrigin, err := publicorigin.Parse("https://foreign.test")
	if err != nil {
		t.Fatal(err)
	}
	foreign := publicorigin.WithContext(withZoneContext(context.Background(), ZoneInfo{ID: "foreign", Host: "foreign.test"}), foreignOrigin)
	ctx, cancel := context.WithTimeout(foreign, time.Minute)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	flushes, shutdowns := 0, 0
	var flushContext, shutdownContext context.Context
	check := func(ctx context.Context) {
		t.Helper()
		deadline, present := ctx.Deadline()
		if PublicOrigin(ctx) != declaration.Release.PublicOrigin || CurrentZone(ctx) != want || !present || !deadline.Equal(wantDeadline) {
			t.Fatalf("lifecycle lost deployment/deadline: origin=%q zone=%+v deadline=%v", PublicOrigin(ctx), CurrentZone(ctx), deadline)
		}
	}
	tracer, err := telemetry.New(telemetry.Options{Recorder: func(telemetry.Record) error { return nil }, Flush: func(ctx context.Context) error {
		flushes++
		flushContext = ctx
		check(ctx)
		return nil
	}, Shutdown: func(ctx context.Context) error {
		shutdowns++
		shutdownContext = ctx
		check(ctx)
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	declaration.Zone.Config.Tracing = tracer
	release := releaseBoundaryNew(t, declaration)
	if err := release.FlushTelemetry(nil); err == nil {
		t.Fatal("nil lifecycle context accepted")
	}
	if err := release.Close(nil); err == nil {
		t.Fatal("nil close context accepted")
	}
	if err := release.FlushTelemetry(ctx); err != nil {
		t.Fatal(err)
	}
	if err := release.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := release.Close(ctx); err != nil {
		t.Fatal("cooperative repeated close failed", err)
	}
	if flushes != 1 || shutdowns != 1 || flushContext == nil || shutdownContext == nil {
		t.Fatal("lifecycle assertions were not exercised once", flushes, shutdowns)
	}
	cancel()
	if flushContext.Err() != context.Canceled || shutdownContext.Err() != context.Canceled {
		t.Fatal("lifecycle rebinding detached caller cancellation")
	}
}
