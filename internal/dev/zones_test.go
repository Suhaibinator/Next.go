package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/project"
)

func TestZonesHostsAreExplicitAndOverridesAreValidated(t *testing.T) {
	manifest := &project.ZonesManifest{Zones: []project.ZoneManifest{{ID: "one", Host: "one.example:4230"}, {ID: "two", Host: "two.example:4230"}}}
	hosts, err := zoneDevelopmentHosts(manifest, `{"one":"DEV.example:4230"}`)
	if err != nil || !hosts["dev.example:4230"] || !hosts["two.example:4230"] || hosts["one.example:4230"] {
		t.Fatal(hosts, err)
	}
	for _, bad := range []string{`{"unknown":"dev.example:4230"}`, `{"one":"*.example:4230"}`, `{"one":"http://dev.example:4230"}`, `{"one":"dev.example:4230","one":"evil.example:4230"}`} {
		if _, err := zoneDevelopmentHosts(manifest, bad); err == nil {
			t.Fatal("invalid override accepted", bad)
		}
	}
	s := &supervisor{authority: "127.0.0.1:4230", zoneHosts: hosts}
	s.installControlHosts("127.0.0.1:4230")
	for _, host := range []string{"dev.example:4230", "two.example:4230", "127.0.0.1:4230"} {
		if !s.allowedHost(host) {
			t.Fatal("trusted authority rejected", host)
		}
	}
	for _, host := range []string{"one.example:4230", "attacker.example:4230", "dev.example:4231", "localhost:4230", "127.0.0.2:4230"} {
		if s.allowedHost(host) {
			t.Fatal("authority expanded beyond startup configuration", host)
		}
	}
}

func TestZonesControlsRequireLoopbackPeerAndTrustedOrigin(t *testing.T) {
	s := &supervisor{authority: "127.0.0.1:4230", zoneHosts: map[string]bool{"one.example": true}, listeners: map[chan string]bool{}, buildError: "private compiler detail"}
	s.installControlHosts("127.0.0.1:4230")
	for _, path := range []string{"/", InspectionPath, "/_nextgo/events"} {
		for _, tc := range []struct {
			name, peer, origin, host string
			allowed                  bool
		}{
			{"local", "127.0.0.1:40000", "http://one.example", "one.example", true},
			{"remote", "192.0.2.1:40000", "http://one.example", "one.example", false},
			{"cross origin", "127.0.0.1:40000", "http://attacker.example", "one.example", false},
			{"untrusted host", "127.0.0.1:40000", "http://attacker.example", "attacker.example", false},
		} {
			t.Run(path+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				r := httptest.NewRequest(http.MethodGet, "http://one.example"+path, nil).WithContext(ctx)
				r.Host = tc.host
				r.RemoteAddr = tc.peer
				r.Header.Set("Origin", tc.origin)
				r.Header.Set("Forwarded", "for=127.0.0.1;host=one.example")
				r.Header.Set("X-Forwarded-For", "127.0.0.1")
				w := httptest.NewRecorder()
				s.ServeHTTP(w, r)
				if tc.allowed {
					if w.Code == 403 {
						t.Fatal(w.Code, w.Body.String())
					}
				} else if w.Code != 403 || strings.Contains(w.Body.String(), "private compiler detail") {
					t.Fatal(w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestZoneEventsRecognizeEachCurrentRuntimeAndHost(t *testing.T) {
	s := &supervisor{id: "aggregate", generation: "generation", proxy: &httputil.ReverseProxy{}, zoneHosts: map[string]bool{"one.example": true, "two.example": true}, currentIDs: map[string]bool{"one-runtime": true, "two-runtime": true}, currentOwners: map[string][]assetOwner{"one-runtime": {{host: "one.example"}}, "two-runtime": {{host: "two.example"}}}, listeners: map[chan string]bool{}}
	for _, tc := range []struct {
		host, build, generation string
		reload                  bool
	}{
		{"one.example", "one-runtime", "generation", false}, {"two.example", "two-runtime", "generation", false},
		{"one.example", "two-runtime", "generation", true}, {"one.example", "one-runtime", "old", true},
		{"one.example", "aggregate", "generation", true}, {"one.example", "missing", "generation", true},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := httptest.NewRequest("GET", fmt.Sprintf("http://%s/_nextgo/events?build=%s&generation=%s", tc.host, tc.build, tc.generation), nil).WithContext(ctx)
		w := httptest.NewRecorder()
		s.events(w, r)
		if strings.Contains(w.Body.String(), "event: reload") != tc.reload {
			t.Fatalf("%+v: %s", tc, w.Body.String())
		}
	}
}

func TestZoneRetentionKeepsAllBagsAndExactPreviousOwners(t *testing.T) {
	root := t.TempDir()
	s := &supervisor{authority: "127.0.0.1:4230", zoneHosts: map[string]bool{"current.example": true}}
	var dirs []string
	for i := 0; i < retainedBuilds+2; i++ {
		id := fmt.Sprintf("generation-%d", i)
		dir := filepath.Join(root, id)
		dirs = append(dirs, dir)
		assets := map[string]string{}
		owners := map[string][]assetOwner{}
		for _, zone := range []string{"one", "two"} {
			runtime := fmt.Sprintf("%s-%d", zone, i)
			base := filepath.Join(dir, "server", "assets", zone)
			path := filepath.Join(base, "_nextgo", runtime, "app.wasm")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(runtime), 0644); err != nil {
				t.Fatal(err)
			}
			assets[runtime] = base
			owners[runtime] = []assetOwner{{host: zone + ".example", prefix: "/old-" + zone + "/_nextgo/" + runtime + "/"}}
		}
		for _, expired := range s.retainBuildRuntimeLocked(id, dir, assets, owners) {
			if err := os.RemoveAll(expired); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(s.buildDirs) != retainedBuilds || len(s.assets) != retainedBuilds*2 || len(s.buildOwners) != retainedBuilds {
		t.Fatal("retention counted zones instead of generations")
	}
	for i, dir := range dirs {
		_, err := os.Stat(dir)
		if i < 2 {
			if !os.IsNotExist(err) {
				t.Fatal("expired generation remains", dir)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	for _, zone := range []string{"one", "two"} {
		runtime := zone + "-2"
		r := httptest.NewRequest("GET", "http://"+zone+".example/old-"+zone+"/_nextgo/"+runtime+"/app.wasm", nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != runtime {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, wrong := range []struct{ host, path string }{{"other.example", r.URL.Path}, {zone + ".example", "/wrong/_nextgo/" + runtime + "/app.wasm"}, {zone + ".example", "/old-" + zone + "-suffix/_nextgo/" + runtime + "/app.wasm"}} {
			q := httptest.NewRequest("GET", "http://"+wrong.host+wrong.path, nil)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, q)
			if w.Code == 200 {
				t.Fatal("retained asset escaped owner", wrong)
			}
		}
	}
}

func TestSharedRuntimeRetentionNeverPointsIntoExpiredDirectory(t *testing.T) {
	s := &supervisor{}
	for i := 0; i < retainedBuilds+2; i++ {
		s.retainBuildRuntimeLocked(fmt.Sprint(i), fmt.Sprintf("/owned/%d", i), map[string]string{"same": fmt.Sprintf("/owned/%d/assets", i)}, map[string][]assetOwner{"same": {{host: fmt.Sprintf("%d.example", i), prefix: "/_nextgo/same/"}}})
	}
	if s.assets["same"] != "/owned/9/assets" || len(s.assetOwners["same"]) != retainedBuilds {
		t.Fatal(s.assets, s.assetOwners)
	}
}

func TestPublicZoneInspectionGraphIsBoundedAndPrivate(t *testing.T) {
	secret := "PRIVATE_NATIVE_TENANT_ROOT_IMPORT"
	p := &project.Project{Root: "/" + secret, Import: secret, Routes: []project.Route{{Pattern: "/", Layouts: []string{secret}, Templates: map[int]string{}}}, Components: []project.Component{{Name: "counter"}}}
	build := &project.Build{ZoneProjects: []*project.Project{p}, Zones: &project.ZonesManifest{Zones: []project.ZoneManifest{{ID: "one", Root: p.Root, BasePath: "/one", Tenant: secret}, {ID: "two", Root: p.Root, BasePath: "/two", Tenant: secret}}}}
	graph := publicBuildInspectionGraph(build)
	data, err := json.Marshal(graph)
	if err != nil || strings.Contains(string(data), secret) || graph.Truncated {
		t.Fatal(string(data), err)
	}
	zones := 0
	for _, node := range graph.Nodes {
		if node.Kind == "zone" {
			zones++
		}
	}
	if zones != 2 {
		t.Fatal(graph)
	}
	p.Routes = make([]project.Route, maxInspectionNodes*2)
	for i := range p.Routes {
		p.Routes[i].Pattern = fmt.Sprintf("/route/%d", i)
	}
	graph = publicBuildInspectionGraph(build)
	data, err = json.Marshal(graph)
	if err != nil || !graph.Truncated || len(graph.Nodes) > maxInspectionNodes || len(graph.Edges) > maxInspectionEdges || len(data) > maxInspectionBytes {
		t.Fatal("zone graph exceeded protocol bounds", len(data), err)
	}
}

func TestZoneEnvironmentHostPrecedenceDoesNotMutateProcess(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env.development"), []byte(`NEXTGO_ZONE_HOSTS='{"one":"file.example"}'`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, present := os.LookupEnv("NEXTGO_ZONE_HOSTS")
	raw, err := developmentZoneHostOverride(root, []string{"NEXTGO_ZONE_HOSTS="})
	if err != nil || raw != "" {
		t.Fatal("explicit empty ambient value did not outrank dotenv", raw, err)
	}
	raw, err = developmentZoneHostOverride(root, nil)
	if err != nil || raw != `{"one":"file.example"}` {
		t.Fatal(raw, err)
	}
	after, stillPresent := os.LookupEnv("NEXTGO_ZONE_HOSTS")
	if before != after || present != stillPresent {
		t.Fatal("loading dev hosts mutated process environment")
	}
}

func TestZoneHostExpansionUsesTheSameNativeDevelopmentEnvironment(t *testing.T) {
	t.Setenv("AWS_LAMBDA_RUNTIME_API", "unexpected")
	root := t.TempDir()
	// Expand using exactly the environment supplied to the owned native child.
	if err := os.WriteFile(filepath.Join(root, ".env.development"), []byte("NEXTGO_ZONE_HOSTS=\"{\\\"one\\\":\\\"dev${AWS_LAMBDA_RUNTIME_API}.example\\\"}\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := developmentZoneHostOverride(root, developmentEnvironment(os.Environ()))
	if err != nil || raw != `{"one":"dev.example"}` {
		t.Fatal(raw, err)
	}
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "unexpected" {
		t.Fatal("native development normalization mutated the supervisor process")
	}
}

func TestEnvironmentPublicationsRetainSharedOutputUntilLastReference(t *testing.T) {
	s := &supervisor{}
	for i := 0; i < retainedBuilds; i++ {
		if expired := s.retainBuildRuntimeLocked(fmt.Sprint(i), "/owned/shared", map[string]string{"runtime": "/owned/shared/assets"}, map[string][]assetOwner{"runtime": {{host: fmt.Sprintf("%d.example", i), prefix: "/_nextgo/runtime/"}}}); len(expired) != 0 {
			t.Fatal(expired)
		}
	}
	for i := retainedBuilds; i < 2*retainedBuilds-1; i++ {
		if expired := s.retainBuildRuntimeLocked(fmt.Sprint(i), fmt.Sprintf("/owned/%d", i), map[string]string{fmt.Sprint(i): fmt.Sprintf("/owned/%d/assets", i)}, nil); len(expired) != 0 {
			t.Fatal("shared output expired while retained publication still used it", expired)
		}
	}
	expired := s.retainBuildRuntimeLocked("final", "/owned/final", map[string]string{"last": "/owned/final/assets"}, nil)
	if len(expired) != 1 || expired[0] != "/owned/shared" {
		t.Fatal("shared output was not removed after final reference", expired)
	}
}
