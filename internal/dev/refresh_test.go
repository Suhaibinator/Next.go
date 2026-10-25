package dev

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
	"github.com/Suhaibinator/Next.go/internal/project"
)

func refreshFixture(t *testing.T, n int) *project.Build {
	t.Helper()
	dir := t.TempDir()
	id := fmt.Sprintf("%020x", n)
	bytes := []byte(fmt.Sprintf("public-module-%d", n))
	digest := sha256.Sum256(bytes)
	epoch := sha256.Sum256([]byte(fmt.Sprintf("closure-%d", n)))
	hash := hex.EncodeToString(digest[:])
	path := filepath.Join(dir, "server", "assets", "_nextgo", id, "module-"+hash+".wasm")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes, 0644); err != nil {
		t.Fatal(err)
	}
	return &project.Build{ID: id, Dir: dir, Fingerprint: fmt.Sprint(n), DevRefresh: &project.DevRefreshBuild{Version: devrefresh.Version, Compatibility: strings.Repeat("c", 64), ShimDigest: devrefresh.ShimDigest, ModuleABI: devrefresh.ModuleABI, Modules: []devrefresh.Module{{Name: "counter", URL: "/_nextgo/" + id + "/module-" + hash + ".wasm", Digest: hash, Size: len(bytes), Epoch: hex.EncodeToString(epoch[:]), StateSchema: "counter-v1", DOMSchema: "counter-dom-v1"}}}}
}
func servingRefreshFixture(t *testing.T) (*supervisor, *child, *project.Build, chan string) {
	t.Helper()
	base := refreshFixture(t, 1)
	generation := strings.Repeat("a", 32)
	updates := make(chan string, 64)
	s := &supervisor{authority: "127.0.0.1:3000", id: base.ID, generation: generation, proxy: &httputil.ReverseProxy{}, output: io.Discard, listeners: map[chan string]bool{updates: true}, currentIDs: map[string]bool{base.ID: true}}
	active := &child{generation: generation, refreshReady: true, done: make(chan struct{})}
	key := s.setServingRefreshLocked(base, active, base.ID)
	s.retainBuildAssetsLocked(key, base.Dir, developmentAssets(base))
	if s.refresh == nil {
		t.Fatal("fixture baseline was not enabled")
	}
	return s, active, base, updates
}
func refreshRequest(s *supervisor, method, query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:3000"+devrefresh.ManifestPath+query, nil)
	r.RemoteAddr = "127.0.0.1:40000"
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestClientRefreshKeepsNativeBindingPinsBaselineAndPublishesLatestOverlay(t *testing.T) {
	s, active, base, updates := servingRefreshFixture(t)
	proxy := s.proxy
	for n := 2; n <= retainedBuilds+6; n++ {
		candidate := refreshFixture(t, n)
		ok, expired := s.tryClientRefresh(candidate, active, true)
		if !ok {
			t.Fatal("compatible refresh rejected", n)
		}
		s.removeExpired(expired)
		if s.proxy != proxy || s.id != base.ID || s.generation != active.generation {
			t.Fatal("client-only publication changed native server binding")
		}
		if len(s.buildDirs) > retainedBuilds || len(s.buildOrder) > retainedBuilds {
			t.Fatal("refresh output unbounded")
		}
		if _, err := os.Stat(base.Dir); err != nil {
			t.Fatal("active native baseline was removed", err)
		}
		for _, module := range candidate.DevRefresh.Modules {
			r := httptest.NewRequest("GET", "http://127.0.0.1:3000"+module.URL, nil)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal("latest candidate asset not available", w.Code)
			}
		}
		message := <-updates
		if !strings.HasPrefix(message, "event: refresh\ndata: {") || strings.Contains(message, "event: reload") {
			t.Fatal("refresh was not a public structured event", message)
		}
	}
	if s.refresh.Revision != uint64(retainedBuilds+5) {
		t.Fatal(s.refresh.Revision)
	}
	response := refreshRequest(s, "GET", "?build="+base.ID+"&generation="+active.generation)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	manifest, err := devrefresh.ParseManifest(response.Body.Bytes())
	if err != nil || manifest.Revision != s.refresh.Revision || manifest.ServingBuild != base.ID {
		t.Fatal(manifest, err)
	}
	for _, name := range []string{"state", "cells", "snapshot", "input", "source"} {
		var top map[string]json.RawMessage
		_ = json.Unmarshal(response.Body.Bytes(), &top)
		if top[name] != nil {
			t.Fatal("private/state payload in descriptor response", name)
		}
	}
	snapshot := s.inspectionSnapshot()
	if snapshot.BuildID != base.ID || snapshot.Generation != active.generation || !snapshot.RefreshEnabled || snapshot.RefreshRevision != manifest.Revision {
		t.Fatal(snapshot)
	}
}

func TestRefreshRejectsUnprovenOrIncompatibleCandidatesWithoutPublication(t *testing.T) {
	tests := map[string]func(*supervisor, *child, *project.Build){
		"native unsupported": func(_ *supervisor, c *child, _ *project.Build) { c.refreshReady = false },
		"native exited":      func(_ *supervisor, c *child, _ *project.Build) { close(c.done) },
		"unknown shim":       func(_ *supervisor, _ *child, b *project.Build) { b.DevRefresh.ShimDigest = strings.Repeat("0", 64) },
		"native core input":  func(_ *supervisor, _ *child, b *project.Build) { b.DevRefresh.Compatibility = strings.Repeat("e", 64) },
		"state schema":       func(_ *supervisor, _ *child, b *project.Build) { b.DevRefresh.Modules[0].StateSchema = "counter-v2" },
		"DOM schema":         func(_ *supervisor, _ *child, b *project.Build) { b.DevRefresh.Modules[0].DOMSchema = "dom-v2" },
		"module inventory":   func(_ *supervisor, _ *child, b *project.Build) { b.DevRefresh.Modules[0].Name = "other" },
		"foreign namespace": func(_ *supervisor, _ *child, b *project.Build) {
			b.DevRefresh.Modules[0].URL = strings.Replace(b.DevRefresh.Modules[0].URL, b.ID, strings.Repeat("f", 20), 1)
		},
		"zone": func(s *supervisor, _ *child, b *project.Build) {
			s.zoneHosts = map[string]bool{}
			b.Zones = &project.ZonesManifest{}
		},
		"missing artifact": func(_ *supervisor, _ *child, b *project.Build) {
			_ = os.RemoveAll(filepath.Join(b.Dir, "server", "assets"))
		},
		"tampered artifact": func(_ *supervisor, _ *child, b *project.Build) {
			module := b.DevRefresh.Modules[0]
			path := filepath.Join(b.Dir, "server", "assets", strings.TrimPrefix(module.URL, "/"))
			data, _ := os.ReadFile(path)
			data[0] ^= 1
			_ = os.WriteFile(path, data, 0600)
		},
		"no proof": func(_ *supervisor, _ *child, b *project.Build) { b.DevRefresh = nil },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s, c, _, updates := servingRefreshFixture(t)
			candidate := refreshFixture(t, 2)
			mutate(s, c, candidate)
			if ok, _ := s.tryClientRefresh(candidate, c, true); ok {
				t.Fatal("unsafe candidate published")
			}
			if s.refresh.Revision != 0 || len(updates) != 0 {
				t.Fatal("rejected candidate changed live metadata")
			}
		})
	}
	s, c, _, updates := servingRefreshFixture(t)
	if ok, _ := s.tryClientRefresh(refreshFixture(t, 2), c, false); ok || len(updates) != 0 {
		t.Fatal("changed environment enabled refresh")
	}
}

func TestRefreshEndpointGatesBindingsAndSSEReplaysLatest(t *testing.T) {
	s, c, base, _ := servingRefreshFixture(t)
	if ok, _ := s.tryClientRefresh(refreshFixture(t, 2), c, true); !ok {
		t.Fatal("update failed")
	}
	query := "?build=" + base.ID + "&generation=" + c.generation
	for _, tc := range []struct {
		method, query string
		status        int
	}{{"HEAD", query, 200}, {"POST", query, 405}, {"GET", query + "&state=secret", 400}, {"GET", query + "&build=" + base.ID, 400}, {"GET", query + "&bad=%xx", 400}, {"GET", "", 400}, {"GET", "?build=stale&generation=" + c.generation, 409}} {
		w := refreshRequest(s, tc.method, tc.query)
		if w.Code != tc.status {
			t.Fatal(tc, w.Code, w.Body.String())
		}
		if tc.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatal("HEAD returned data")
		}
	}
	for _, mutation := range []func(*http.Request){func(r *http.Request) { r.RemoteAddr = "192.0.2.1:40000"; r.Header.Set("Forwarded", "for=127.0.0.1") }, func(r *http.Request) { r.Header.Set("Origin", "http://attacker.example") }, func(r *http.Request) { r.Host = "attacker.example:3000" }} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:3000"+devrefresh.ManifestPath+query, nil)
		r.RemoteAddr = "127.0.0.1:40000"
		mutation(r)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("control policy bypass", w.Code)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "http://127.0.0.1:3000/_nextgo/events"+query, nil).WithContext(ctx)
	w := httptest.NewRecorder()
	s.events(w, r)
	if strings.Contains(w.Body.String(), "event: reload") || !strings.Contains(w.Body.String(), "event: refresh\ndata: {") {
		t.Fatal("cold/reconnect did not receive latest overlay", w.Body.String())
	}
	s.refresh = nil
	if got := refreshRequest(s, "GET", query); got.Code != 204 {
		t.Fatal(got.Code)
	}
}

func TestNoOpRecoveryAndSameBuildIDNeverReplaceNativeDirectory(t *testing.T) {
	s, c, base, updates := servingRefreshFixture(t)
	s.buildError = "compile failure"
	if ok, expired := s.tryClientRefresh(base, c, true); !ok || len(expired) != 0 {
		t.Fatal(ok, expired)
	}
	if s.refresh.Revision != 1 || s.buildError != "" || len(updates) != 1 {
		t.Fatal("no-op recovery failed", s.refresh)
	}
	candidate := refreshFixture(t, 2)
	oldID := candidate.ID
	candidate.ID = base.ID
	if err := os.Rename(filepath.Join(candidate.Dir, "server", "assets", "_nextgo", oldID), filepath.Join(candidate.Dir, "server", "assets", "_nextgo", base.ID)); err != nil {
		t.Fatal(err)
	}
	module := &candidate.DevRefresh.Modules[0]
	module.URL = "/_nextgo/" + base.ID + "/module-" + module.Digest + ".wasm"
	if ok, expired := s.tryClientRefresh(candidate, c, true); !ok {
		t.Fatal("same ID candidate rejected", expired)
	} else {
		for _, dir := range expired {
			if dir == base.Dir {
				t.Fatal("active binary directory expired")
			}
		}
	}
	if s.buildDirs[s.refreshBaseKey] != base.Dir {
		t.Fatal("native baseline pointer was replaced")
	}
}

func TestRefreshStatusNeverReloadsAnExitedNativeBaseline(t *testing.T) {
	s, c, base, _ := servingRefreshFixture(t)
	close(c.done)
	if !s.exited(c) {
		t.Fatal("exited native child missed")
	}
	query := "?build=" + base.ID + "&generation=" + c.generation
	if got := refreshRequest(s, "GET", query); got.Code != 503 {
		t.Fatal(got.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", "http://127.0.0.1:3000/_nextgo/events", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	s.events(response, request)
	if strings.Contains(response.Body.String(), "event: reload") || strings.Contains(response.Body.String(), "event: refresh") {
		t.Fatal("building/error document would enter a reload loop", response.Body.String())
	}
	if s.refresh != nil || len(s.pinnedBuilds) != 0 {
		t.Fatal("exited native baseline remained refreshable")
	}
}

func TestRefreshRejectsStateBodiesAndHandlesRevisionExhaustion(t *testing.T) {
	s, c, base, updates := servingRefreshFixture(t)
	query := "?build=" + base.ID + "&generation=" + c.generation
	request := httptest.NewRequest("GET", "http://127.0.0.1:3000"+devrefresh.ManifestPath+query, strings.NewReader(`{"cells":{"private":"NEVER_ON_WIRE"}}`))
	request.RemoteAddr = "127.0.0.1:40000"
	response := httptest.NewRecorder()
	s.ServeHTTP(response, request)
	if response.Code != 400 || strings.Contains(response.Body.String(), "NEVER_ON_WIRE") {
		t.Fatal(response.Code, response.Body.String())
	}
	s.refresh.Revision = devrefresh.MaxRevision
	s.refresh.Previous = devrefresh.MaxRevision - 1
	if ok, _ := s.tryClientRefresh(refreshFixture(t, 2), c, true); ok || len(updates) != 0 {
		t.Fatal("revision exhaustion wrapped or published")
	}
}
