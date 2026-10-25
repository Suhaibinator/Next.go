package nextgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func zoneScopeConfig(t *testing.T, info ZoneInfo, config Config) Config {
	t.Helper()
	config.BasePath = info.BasePath
	if err := setConfigZone(&config, info); err != nil {
		t.Fatal(err)
	}
	return config
}
func zoneScopeApp(t *testing.T, config Config) *App {
	t.Helper()
	app := testApp(t, config)
	t.Cleanup(func() {
		if err := app.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return app
}
func TestZoneScopeOwnedConfigAndContext(t *testing.T) {
	info := ZoneInfo{ID: "north", Tenant: "team-a", Host: "EXAMPLE.TEST:8443", BasePath: "/north"}
	config := zoneScopeConfig(t, info, Config{})
	want := info
	want.Host = "example.test:8443"
	if config.Zone() != want {
		t.Fatal(config.Zone())
	}
	snapshot := config.Zone()
	snapshot.ID = "changed"
	if config.Zone() != want {
		t.Fatal("getter changed private configuration")
	}
	ctx := bindConfigZone(context.Background(), config)
	snapshot = CurrentZone(ctx)
	snapshot.Tenant = "changed"
	if CurrentZone(ctx) != want || CurrentZone(nil) != (ZoneInfo{}) {
		t.Fatal("context snapshot not owned")
	}
	if got := CurrentZone(bindConfigZone(ctx, Config{})); got != (ZoneInfo{}) {
		t.Fatal("independent App inherited zone", got)
	}
	for _, bad := range []ZoneInfo{{Host: "example.test"}, {ID: "north\n", Host: "example.test"}, {ID: "north", Tenant: " x", Host: "example.test"}, {ID: "north", Host: "https://example.test"}, {ID: "north", Host: "example.test", BasePath: "/wrong"}} {
		if err := setConfigZone(&Config{}, bad); err == nil {
			t.Fatal("invalid zone accepted", bad)
		}
	}
	if err := setConfigZone(nil, want); err == nil {
		t.Fatal("nil config accepted")
	}
}
func TestZoneScopeOpaqueConfigurationIdentity(t *testing.T) {
	info := ZoneInfo{ID: "north", Tenant: "team-a", Host: "example.test", BasePath: "/north"}
	if configurationZonePolicy("legacy", ZoneInfo{}) != "legacy" {
		t.Fatal("unscoped legacy changed")
	}
	first := configurationZonePolicy("prior", info)
	if len(first) != 64 || strings.Contains(first, "north") {
		t.Fatal(first)
	}
	variants := []ZoneInfo{info, info, info, info}
	variants[0].ID = "south"
	variants[1].Tenant = "team-b"
	variants[2].Host = "other.test"
	variants[3].BasePath = "/south"
	for _, variant := range variants {
		if configurationZonePolicy("prior", variant) == first {
			t.Fatal("field did not partition identity", variant)
		}
	}
	if configurationZonePolicy("changed", info) == first {
		t.Fatal("previous policy omitted")
	}
	a := zoneScopeApp(t, zoneScopeConfig(t, info, Config{BuildID: "same"}))
	other := info
	other.Tenant = "team-b"
	b := zoneScopeApp(t, zoneScopeConfig(t, other, Config{BuildID: "same"}))
	if a.configurationID == b.configurationID || a.prerenderShape == b.prerenderShape || a.prerenderKey("/") == b.prerenderKey("/") {
		t.Fatal("trusted zone did not partition App/cache/prerender")
	}
}
func TestZoneScopeStartupStaticAndRequestRebinding(t *testing.T) {
	info := ZoneInfo{ID: "north", Tenant: "team-a", Host: "example.test", BasePath: "/north"}
	foreign := ZoneInfo{ID: "foreign", Host: "other.test"}
	var startup, enumerated ZoneInfo
	config := zoneScopeConfig(t, info, Config{MetadataFiles: metadatafiles.Sources{Robots: func(ctx context.Context) (seo.RobotsOptions, error) {
		startup = CurrentZone(ctx)
		return seo.RobotsOptions{}, nil
	}}, Routes: []Route{
		{Pattern: "/:slug", StaticParams: func(ctx context.Context) ([]Params, error) {
			enumerated = CurrentZone(ctx)
			return []Params{{"slug": "one"}}, nil
		}, Page: static("page")},
		{Pattern: "/api/zone", API: func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(CurrentZone(r.Context())) }},
	}})
	app := zoneScopeApp(t, config)
	if startup != info {
		t.Fatal("startup producer missed scope", startup)
	}
	if _, err := app.StaticPaths(withZoneContext(context.Background(), foreign)); err != nil {
		t.Fatal(err)
	}
	if enumerated != info {
		t.Fatal("static enumerator inherited caller zone", enumerated)
	}
	input := httptest.NewRequest("GET", "http://example.test/north/api/zone", nil).WithContext(withZoneContext(context.Background(), foreign))
	output := httptest.NewRecorder()
	app.ServeHTTP(output, input)
	var got ZoneInfo
	if err := json.Unmarshal(output.Body.Bytes(), &got); err != nil || got != info {
		t.Fatal(output.Code, output.Body.String(), err)
	}
	// An unscoped independent App must clear a reused request's private zone.
	plain := zoneScopeApp(t, Config{Routes: []Route{{Pattern: "/api/zone", API: func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(CurrentZone(r.Context())) }}}})
	output = httptest.NewRecorder()
	plain.ServeHTTP(output, httptest.NewRequest("GET", "http://example.test/api/zone", nil).WithContext(withZoneContext(context.Background(), foreign)))
	if err := json.Unmarshal(output.Body.Bytes(), &got); err != nil || got != (ZoneInfo{}) {
		t.Fatal("independent request scope leaked", output.Code, output.Body.String(), err)
	}
}
func TestZoneScopePublicShellAndSnapshotRebinding(t *testing.T) {
	info := ZoneInfo{ID: "north", Tenant: "team-a", Host: "example.test:8443", BasePath: "/north"}
	app := zoneScopeApp(t, zoneScopeConfig(t, info, Config{}))
	type privateKey struct{}
	ctx, cancel := context.WithTimeout(context.WithValue(withZoneContext(context.Background(), ZoneInfo{ID: "foreign"}), privateKey{}, "secret"), time.Minute)
	defer cancel()
	public := app.publicShellContext(ctx)
	if public.Value(privateKey{}) != nil || CurrentZone(public) != info {
		t.Fatal("public shell scope restoration failed")
	}
	before, _ := ctx.Deadline()
	after, _ := public.Deadline()
	if before != after {
		t.Fatal("shell deadline changed")
	}
	input := httptest.NewRequest("POST", "https://evil.test/north/page?secret=1", strings.NewReader("secret"))
	input.Header.Set("Cookie", "private=secret")
	clean := app.publicShellRequest(input, public)
	if clean.Host != info.Host || clean.URL.Host != info.Host || clean.Header.Get("Cookie") != "" || clean.URL.RawQuery != "" || CurrentZone(clean.Context()) != info {
		t.Fatal("synthetic host/scope not trusted", clean)
	}
	// Cache refresh callers may arrive with a foreign or absent context value.
	route := Route{Pattern: "/page", Page: func(c *Context) (Page, error) {
		if CurrentZone(c.Context) != info || CurrentZone(c.Request.Context()) != info {
			t.Fatal("snapshot scope not rebound")
		}
		return Page{Body: ui.Text("zone " + CurrentZone(c.Context).ID)}, nil
	}}
	snapshot, err := app.snapshotPage(withZoneContext(context.Background(), ZoneInfo{ID: "foreign"}), input, nil, nil, route)
	if err != nil || snapshot.HTML != "zone north" {
		t.Fatal(snapshot.HTML, err)
	}
	route.Prerender = &PrerenderPolicy{Partial: true, HostIndependent: true}
	route.Regions = map[string]RegionFunc{"visitor": func(*Context, json.RawMessage) (ui.Node, error) { return ui.Text("visitor"), nil }}
	route.Page = func(c *Context) (Page, error) {
		if CurrentZone(c.Context) != info || CurrentZone(c.Request.Context()) != info || c.Value(privateKey{}) != nil || c.Request.Host != info.Host {
			t.Fatal("partial cache snapshot lost trusted scope or retained private input")
		}
		return Page{Body: ui.El("main", nil, ui.Text("public zone "+CurrentZone(c.Context).ID), ui.RequestRegion("visitor", "visitor", nil, ui.Text("waiting")))}, nil
	}
	snapshot, err = app.snapshotPage(ctx, input, nil, nil, route)
	if err != nil || !strings.Contains(snapshot.HTML, "public zone north") || len(snapshot.Regions) != 1 {
		t.Fatal(snapshot.HTML, err)
	}
	resolver := app.requestRegionResolver(&Context{Context: ctx, Request: input, Params: map[string]string{}}, Route{Regions: map[string]RegionFunc{"region": func(c *Context, _ json.RawMessage) (ui.Node, error) {
		if CurrentZone(c.Context) != info || CurrentZone(c.Request.Context()) != info {
			t.Fatal("request region scope not rebound")
		}
		return ui.Text("region"), nil
	}}})
	if _, err := resolver(withZoneContext(context.Background(), ZoneInfo{ID: "foreign"}), "region", nil); err != nil {
		t.Fatal(err)
	}
	cancel()
	if public.Err() != context.Canceled {
		t.Fatal("shell cancellation lost")
	}
}

func TestZoneScopeBuildPrerenderUsesTrustedContextAndHost(t *testing.T) {
	info := ZoneInfo{ID: "north", Tenant: "team-a", Host: "example.test:8443", BasePath: "/north"}
	for _, localized := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "localized"}[localized], func(t *testing.T) {
			enumerations, pages := 0, 0
			config := zoneScopeConfig(t, info, Config{BuildID: "zone-build", PrerenderBuildMode: true, Routes: []Route{{Pattern: "/:slug", StaticParams: func(ctx context.Context) ([]Params, error) {
				enumerations++
				if CurrentZone(ctx) != info {
					t.Fatal("build static parameters lost trusted zone")
				}
				return []Params{{"slug": "one"}}, nil
			}, Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour, Fallback: FallbackNotFound}, Page: func(c *Context) (Page, error) {
				pages++
				if CurrentZone(c.Context) != info || CurrentZone(c.Request.Context()) != info || c.Request.Host != info.Host || c.Request.URL.Host != info.Host {
					t.Fatal("build synthetic request lost zone authority", CurrentZone(c.Context), c.Request.Host, c.Request.URL.Host)
				}
				return Page{Body: ui.Text("zone " + info.ID)}, nil
			}}}})
			if localized {
				config.Locales = &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}
			}
			app := zoneScopeApp(t, config)
			artifact, err := app.BuildPrerender(withZoneContext(context.Background(), ZoneInfo{ID: "foreign", Host: "evil.test"}))
			if err != nil || !strings.Contains(string(artifact), "zone north") || enumerations == 0 || pages == 0 {
				t.Fatal(string(artifact), enumerations, pages, err)
			}
		})
	}
}
