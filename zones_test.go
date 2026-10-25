package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/telemetry"
	"github.com/Suhaibinator/Next.go/ui"
)

func zoneTestConfig(id, host, prefix string) ZoneConfig {
	return ZoneConfig{ID: id, Host: host, Tenant: id + "-tenant", Config: Config{BasePath: prefix, BuildID: id + "-build", Logger: log.New(io.Discard, "", 0), Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		info := CurrentZone(c.Context)
		return Page{Title: info.ID, Body: ui.Text(info.ID + "=" + info.Tenant)}, nil
	}}}, NotFound: func(c *Context) (Page, error) {
		return Page{Title: "Missing", Body: ui.Text(CurrentZone(c.Context).ID + "-missing")}, nil
	}}}
}
func zoneTestGroup(t *testing.T, declarations ...ZoneConfig) *Zones {
	t.Helper()
	group, err := NewZones(context.Background(), declarations)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := group.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return group
}
func zoneRequest(group *Zones, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	group.ServeHTTP(response, request)
	return response
}

func TestZonesExactHostLongestPrefixAndNoFallback(t *testing.T) {
	root := zoneTestConfig("root", "EXAMPLE.TEST.", "")
	root.Config.Routes = append(root.Config.Routes, Route{Pattern: "/blog/missing", Page: static("parent must never render")}, Route{Pattern: "/blogger", Page: static("root blogger")})
	child := zoneTestConfig("blog", "example.test", "/blog")
	deep := zoneTestConfig("deep", "example.test", "/blog/deep")
	other := zoneTestConfig("other", "other.test", "")
	group := zoneTestGroup(t, root, child, deep, other)
	for target, text := range map[string]string{
		"http://example.test/":           "root=root-tenant",
		"http://example.test/blog":       "blog=blog-tenant",
		"http://EXAMPLE.TEST./blog/deep": "deep=deep-tenant",
		"http://other.test/":             "other=other-tenant",
		"http://example.test/blogger":    "root blogger",
	} {
		response := zoneRequest(group, "GET", target, nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), text) {
			t.Fatal(target, response.Code, response.Body.String())
		}
	}
	missing := zoneRequest(group, "GET", "http://example.test/blog/missing", nil)
	if missing.Code != 404 || !strings.Contains(missing.Body.String(), "blog-missing") || strings.Contains(missing.Body.String(), "parent must never") {
		t.Fatal("child 404 fell through", missing.Code, missing.Body.String())
	}
	if !reflect.DeepEqual(group.mounts[0].app.config.DocumentPaths, []string{"/blog", "/blog/deep"}) || !reflect.DeepEqual(group.mounts[1].app.config.DocumentPaths, []string{"/deep"}) {
		t.Fatal("child document boundaries missing", group.mounts[0].app.config.DocumentPaths, group.mounts[1].app.config.DocumentPaths)
	}
	var posts atomic.Int32
	protected := zoneTestConfig("protected", "secure.test:8443", "/app")
	protected.Config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { posts.Add(1); next.ServeHTTP(w, r) })
	}}
	secure := zoneTestGroup(t, protected)
	for _, tc := range []struct {
		target string
		status int
	}{
		{"http://secure.test/app", 421}, {"http://unknown.test:8443/app", 421},
		{"http://secure.test:8443/application", 404}, {"http://secure.test:8443/%61pp", 404},
		{"http://secure.test:8443/app/a%2Fb", 400}, {"http://secure.test:8443/app/%2e%2e", 400},
		{"http://secure.test:8443/app//tail", 400},
	} {
		response := zoneRequest(secure, "GET", tc.target, map[string]string{"X-Forwarded-Host": "secure.test:8443", "Forwarded": "host=secure.test:8443"})
		if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(tc.target, response.Code, response.Header())
		}
	}
	badHost := httptest.NewRequest("GET", "http://secure.test:8443/app", nil)
	badHost.Host = "secure.test:8443@evil.test"
	badResponse := httptest.NewRecorder()
	secure.ServeHTTP(badResponse, badHost)
	if badResponse.Code != 400 || posts.Load() != 0 {
		t.Fatal("rejected request reached middleware", badResponse.Code, posts.Load())
	}
	badHost.Host = "secure.test:8443"
	badHost.URL.Host = "evil.test"
	badResponse = httptest.NewRecorder()
	secure.ServeHTTP(badResponse, badHost)
	if badResponse.Code != 400 || posts.Load() != 0 {
		t.Fatal("absolute authority mismatch accepted", badResponse.Code)
	}
}

func TestZonesConfigureBeforeFactoriesOwnsContextAndPreservesInputs(t *testing.T) {
	declaration := zoneTestConfig("site", "Example.Test", "/site")
	var hooks, producers int
	declaration.Configure = func(config *Config) error {
		hooks++
		if got := config.Zone(); got.ID != "site" || got.Host != "example.test" || got.BasePath != "/site" || config.Phase != PhaseProduction {
			t.Fatal("hook missing declared scope/phase", got, config.Phase)
		}
		config.MetadataFiles.Robots = func(ctx context.Context) (seo.RobotsOptions, error) {
			producers++
			if CurrentZone(ctx) != config.Zone() {
				t.Fatal("factory missing zone scope", CurrentZone(ctx))
			}
			return seo.RobotsOptions{}, nil
		}
		return nil
	}
	group := zoneTestGroup(t, declaration)
	for range 3 {
		if response := zoneRequest(group, "GET", "http://example.test/site/robots.txt", nil); response.Code != 200 {
			t.Fatal(response.Code)
		}
	}
	if hooks != 1 || producers != 1 || declaration.Config.Zone().ID != "" || declaration.Config.Phase != "" || declaration.Config.MetadataFiles.Robots != nil {
		t.Fatal("startup repeated or caller config mutated", hooks, producers, declaration.Config)
	}
	paths := make([]string, 1, 8)
	paths[0] = "/explicit"
	backing := paths[:cap(paths)]
	backing[1] = "sentinel"
	parent, child := zoneTestConfig("parent", "example.test", ""), zoneTestConfig("child", "example.test", "/child")
	parent.Config.DocumentPaths = paths
	zoneTestGroup(t, parent, child)
	if backing[1] != "sentinel" {
		t.Fatal("document exclusions mutated caller backing array")
	}
}

func TestZonesRejectDeclarationOwnershipAndEndpointConflicts(t *testing.T) {
	for name, mutate := range map[string]func(*ZoneConfig, *ZoneConfig){
		"duplicate-id":     func(a, b *ZoneConfig) { b.ID = a.ID },
		"duplicate-mount":  func(a, b *ZoneConfig) { b.Config.BasePath = a.Config.BasePath },
		"invalid-id":       func(a, _ *ZoneConfig) { a.ID = "../bad" },
		"invalid-tenant":   func(a, _ *ZoneConfig) { a.Tenant = "tenant with space" },
		"invalid-host":     func(a, _ *ZoneConfig) { a.Host = "*.example.test" },
		"invalid-prefix":   func(_, b *ZoneConfig) { b.Config.BasePath = "/blog/" },
		"alternate-assets": func(_, b *ZoneConfig) { b.Config.AssetPrefix = "/shared" },
		"cdn-assets":       func(_, b *ZoneConfig) { b.Config.AssetPrefix = "https://cdn.test/assets" },
		"locale-domains": func(_, b *ZoneConfig) {
			b.Config.Locales = &LocaleConfig{Domains: []LocaleDomain{{Host: "example.test"}}}
		},
		"nested-locales": func(a, _ *ZoneConfig) { a.Config.Locales = &LocaleConfig{Codes: []string{"en"}, Default: "en"} },
		"runtime":        func(_, b *ZoneConfig) { b.Config.BasePath = "/_nextgo/other" },
		"assets":         func(_, b *ZoneConfig) { b.Config.BasePath = "/assets/other" },
		"api":            func(_, b *ZoneConfig) { b.Config.BasePath = "/api/other" },
		"metadata":       func(_, b *ZoneConfig) { b.Config.BasePath = "/robots.txt/other" },
		"static-api": func(a, _ *ZoneConfig) {
			a.Config.Routes = append(a.Config.Routes, Route{Pattern: "/blog/status", API: func(http.ResponseWriter, *http.Request) {}})
		},
		"dynamic-api": func(a, _ *ZoneConfig) {
			a.Config.Routes = append(a.Config.Routes, Route{Pattern: "/:name/status", API: func(http.ResponseWriter, *http.Request) {}})
		},
		"catchall-api": func(a, _ *ZoneConfig) {
			a.Config.Routes = append(a.Config.Routes, Route{Pattern: "/*all", API: func(http.ResponseWriter, *http.Request) {}})
		},
		"exact-handler": func(a, _ *ZoneConfig) {
			a.Config.Handlers = map[string]http.Handler{"/blog/status": http.NotFoundHandler()}
		},
		"prefix-handler":   func(a, _ *ZoneConfig) { a.Config.Handlers = map[string]http.Handler{"/": http.NotFoundHandler()} },
		"hook-base-change": func(_, b *ZoneConfig) { b.Configure = func(c *Config) error { c.BasePath = "/other"; return nil } },
		"hook-scope-erasure": func(_, b *ZoneConfig) {
			b.Configure = func(c *Config) error { *c = Config{BasePath: "/blog"}; return nil }
		},
		"hook-error": func(_, b *ZoneConfig) { b.Configure = func(*Config) error { return errors.New("configured error") } },
		"hook-panic": func(_, b *ZoneConfig) { b.Configure = func(*Config) error { panic("private panic detail") } },
	} {
		t.Run(name, func(t *testing.T) {
			first, second := zoneTestConfig("first", "example.test", ""), zoneTestConfig("second", "example.test", "/blog")
			mutate(&first, &second)
			if group, err := NewZones(context.Background(), []ZoneConfig{first, second}); err == nil {
				group.Close(context.Background())
				t.Fatal("invalid ownership accepted")
			} else if strings.Contains(err.Error(), "private panic detail") {
				t.Fatal("panic details escaped startup boundary")
			}
		})
	}
	for _, declarations := range [][]ZoneConfig{nil, make([]ZoneConfig, MaxZones+1)} {
		if _, err := NewZones(context.Background(), declarations); err == nil {
			t.Fatal("invalid zone count accepted")
		}
	}
	if _, err := NewZones(nil, []ZoneConfig{zoneTestConfig("one", "example.test", "")}); err == nil {
		t.Fatal("nil startup context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewZones(ctx, []ZoneConfig{zoneTestConfig("one", "example.test", "")}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	leaf := zoneTestConfig("leaf", "example.test", "/leaf")
	leaf.Config.Locales = &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}
	if got := zoneRequest(zoneTestGroup(t, leaf), "GET", "http://example.test/leaf/fr", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "leaf=leaf-tenant") {
		t.Fatal("leaf subpath locales lost zone selection", got.Code, got.Body.String())
	}
}

func TestZonesIndependentStoresAssetsMetadataAndActionOrigins(t *testing.T) {
	backend, err := shared.NewMemory(64, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	sharedA, _ := shared.New(backend, "same", "a", shared.Options{})
	sharedB, _ := shared.New(backend, "same", "b", shared.Options{})
	tracer, _ := telemetry.New(telemetry.Options{Recorder: func(telemetry.Record) error { return nil }})
	store := cache.New(4)
	for name, configure := range map[string]func(*Config, *Config){
		"local":            func(a, b *Config) { a.Cache, b.Cache = store, store },
		"shared-namespace": func(a, b *Config) { a.SharedCache, b.SharedCache = sharedA, sharedB },
		"tracer":           func(a, b *Config) { a.Tracing, b.Tracing = tracer, tracer },
	} {
		t.Run(name, func(t *testing.T) {
			a, b := zoneTestConfig("a", "a.test", ""), zoneTestConfig("b", "b.test", "")
			configure(&a.Config, &b.Config)
			if group, err := NewZones(context.Background(), []ZoneConfig{a, b}); err == nil {
				group.Close(context.Background())
				t.Fatal("shared isolation resource accepted")
			}
		})
	}
	makeZone := func(id, prefix string) ZoneConfig {
		zone := zoneTestConfig(id, "example.test", prefix)
		zone.Config.Assets = fstest.MapFS{"public/same.txt": {Data: []byte(id)}, "_nextgo/build/same.wasm": {Data: []byte(id + " wasm")}}
		zone.Config.MetadataFiles = metadatafiles.Sources{Static: fstest.MapFS{"robots.txt": {Data: []byte("User-agent: *\nDisallow: /" + id + "\n")}}}
		zone.Config.ActionRegistrars = []func(*actions.Router){func(router *actions.Router) {
			router.Handle("same", func(c *actions.Context) (actions.Result, error) {
				return actions.Result{Data: CurrentZone(c.Context)}, nil
			})
		}}
		return zone
	}
	group := zoneTestGroup(t, makeZone("a", "/a"), makeZone("b", "/b"))
	if group.mounts[0].app.config.Cache == group.mounts[1].app.config.Cache || group.mounts[0].app.configurationID == group.mounts[1].app.configurationID {
		t.Fatal("independent default stores or identities collapsed")
	}
	for _, id := range []string{"a", "b"} {
		for _, suffix := range []string{"/assets/same.txt", "/_nextgo/build/same.wasm", "/robots.txt"} {
			got := zoneRequest(group, "GET", "http://example.test/"+id+suffix, nil)
			if got.Code != 200 || !strings.Contains(got.Body.String(), id) {
				t.Fatal(id, suffix, got.Code, got.Body.String())
			}
		}
		ok := zoneRequest(group, "POST", "http://example.test/"+id+"/_nextgo/actions/same", map[string]string{"Origin": "http://example.test", "Accept": "application/json"})
		var result struct{ Data ZoneInfo }
		if err := json.Unmarshal(ok.Body.Bytes(), &result); err != nil || ok.Code != 200 || result.Data.ID != id {
			t.Fatal("action context lost selected app", ok.Code, ok.Body.String(), err)
		}
		denied := zoneRequest(group, "POST", "http://example.test/"+id+"/_nextgo/actions/same", map[string]string{"Origin": "http://evil.test", "X-Forwarded-Host": "evil.test", "Accept": "application/json"})
		if denied.Code != 403 {
			t.Fatal("zone routing bypassed action origin", denied.Code)
		}
	}
}

func TestZonesRollbackCloseAndOuterTelemetryLifecycle(t *testing.T) {
	var shutdown, flush atomic.Int32
	tracer, _ := telemetry.New(telemetry.Options{Recorder: func(telemetry.Record) error { return nil }, Flush: func(context.Context) error { flush.Add(1); return nil }, Shutdown: func(context.Context) error { shutdown.Add(1); return nil }})
	first, invalid := zoneTestConfig("first", "a.test", ""), zoneTestConfig("invalid", "b.test", "")
	first.Config.Tracing = tracer
	invalid.Config.Routes = []Route{{Pattern: "/bad"}}
	if _, err := NewZones(context.Background(), []ZoneConfig{first, invalid}); err == nil || shutdown.Load() != 1 {
		t.Fatal("failed startup retained earlier app resources", shutdown.Load(), err)
	}
	shutdown.Store(0)
	group := zoneTestGroup(t, zoneTestConfig("one", "one.test", ""), zoneTestConfig("two", "two.test", ""))
	for _, mount := range group.mounts {
		trace, _ := telemetry.New(telemetry.Options{Recorder: func(telemetry.Record) error { return nil }, Flush: func(ctx context.Context) error {
			if CurrentZone(ctx) != mount.info {
				return errors.New("flush scope differs from owning zone")
			}
			flush.Add(1)
			return nil
		}, Shutdown: func(ctx context.Context) error {
			if CurrentZone(ctx) != mount.info {
				return errors.New("shutdown scope differs from owning zone")
			}
			shutdown.Add(1)
			return nil
		}})
		mount.app.config.Tracing = trace
	}
	if err := group.FlushTelemetry(context.Background()); err != nil || flush.Load() != 2 {
		t.Fatal("outer group omitted child flush", flush.Load(), err)
	}
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() { defer wait.Done(); _ = group.Close(context.Background()) }()
	}
	wait.Wait()
	if shutdown.Load() != 2 {
		t.Fatal("close repeated child lifecycle", shutdown.Load())
	}
	if got := zoneRequest(group, "GET", "http://one.test/", nil); got.Code != 503 {
		t.Fatal("closed group still accepted work", got.Code)
	}
	if err := group.Close(nil); err == nil {
		t.Fatal("nil close context accepted")
	}
	if err := group.FlushTelemetry(nil); err == nil {
		t.Fatal("nil flush context accepted")
	}
	var zero Zones
	if err := zero.Close(context.Background()); err != nil {
		t.Fatal("zero group close failed", err)
	}
}

func TestZonesPublicPageCachesAndInvalidationStayInSelectedApp(t *testing.T) {
	var first, second atomic.Int32
	makeZone := func(id string, loads *atomic.Int32) ZoneConfig {
		zone := zoneTestConfig(id, id+".test", "")
		zone.Config.BuildID = "same-source"
		zone.Config.Routes = []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute, Tags: []string{"same-tag"}}, Page: func(c *Context) (Page, error) {
			info := CurrentZone(c.Context)
			return Page{Body: ui.Text(fmt.Sprintf("%s-value-%d", info.ID, loads.Add(1)))}, nil
		}}, {Pattern: "/invalidate", API: func(w http.ResponseWriter, r *http.Request) {
			if err := ExpireCache(r.Context(), shared.Selector{Kind: "tag", Value: "same-tag"}); err != nil {
				http.Error(w, "invalidation failed", 500)
				return
			}
			w.WriteHeader(204)
		}}}
		return zone
	}
	group := zoneTestGroup(t, makeZone("first", &first), makeZone("second", &second))
	for range 2 {
		for _, id := range []string{"first", "second"} {
			got := zoneRequest(group, "GET", "http://"+id+".test/", nil)
			if got.Code != 200 || !strings.Contains(got.Body.String(), id+"-value-1") {
				t.Fatal(id, got.Code, got.Body.String())
			}
		}
	}
	if first.Load() != 1 || second.Load() != 1 {
		t.Fatal("public snapshots did not cache", first.Load(), second.Load())
	}
	if got := zoneRequest(group, "POST", "http://first.test/invalidate", nil); got.Code != 204 {
		t.Fatal(got.Code, got.Body.String())
	}
	zoneRequest(group, "GET", "http://first.test/", nil)
	zoneRequest(group, "GET", "http://second.test/", nil)
	if first.Load() != 2 || second.Load() != 1 {
		t.Fatal("same tag invalidated another zone", first.Load(), second.Load())
	}
}

func TestZonesCloseBoundsUncooperativeNativeHook(t *testing.T) {
	block, entered := make(chan struct{}), make(chan struct{})
	defer close(block)
	zone := zoneTestConfig("one", "example.test", "")
	tracer, _ := telemetry.New(telemetry.Options{Recorder: func(telemetry.Record) error { return nil }, Shutdown: func(context.Context) error { close(entered); <-block; return nil }})
	zone.Config.Tracing = tracer
	group, err := NewZones(context.Background(), []ZoneConfig{zone})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- group.Close(ctx) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("close did not begin native shutdown")
	}
	started := time.Now()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) || time.Since(started) > time.Second {
			t.Fatal("uncooperative hook held caller", err, time.Since(started))
		}
	case <-time.After(time.Second):
		t.Fatal("uncooperative hook held canceled caller")
	}
	if err := group.Close(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal("repeated close restarted native hooks", err)
	}
}

func TestParseZoneHostsStrictOperatorOverrides(t *testing.T) {
	known := []string{"root", "blog"}
	for _, empty := range []string{"", " \n\t"} {
		if got, err := ParseZoneHosts(empty, known); err != nil || got != nil {
			t.Fatal(got, err)
		}
	}
	got, err := ParseZoneHosts(`{"root":"EXAMPLE.TEST.","blog":"[::1]:8443"}`, known)
	if err != nil || got["root"] != "example.test" || got["blog"] != "[::1]:8443" {
		t.Fatal(got, err)
	}
	for _, input := range []string{`null`, `[]`, `{"root":null}`, `{"root":1}`, `{"unknown":"example.test"}`, `{"root":"one.test","root":"two.test"}`, `{"root":"one.test","\u0072oot":"two.test"}`, `{"root":"https://example.test"}`, `{"root":"*.example.test"}`, `{"root":"example.test:0"}`, `{"root":"user@example.test"}`, `{"root":"example.test/path"}`, `{"root":"example.test"} {}`, strings.Repeat(" ", 64<<10+1), string([]byte{255})} {
		if _, err := ParseZoneHosts(input, known); err == nil {
			t.Fatal("invalid host override accepted", fmt.Sprintf("%.80q", input))
		}
	}
	for _, ids := range [][]string{nil, {"root", "root"}, {"../bad"}, make([]string, MaxZones+1)} {
		if _, err := ParseZoneHosts("", ids); err == nil {
			t.Fatal("invalid known ID inventory accepted")
		}
	}
}
