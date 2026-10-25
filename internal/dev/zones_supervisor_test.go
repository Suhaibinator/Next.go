package dev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/project"
)

// The existing test executable supplies an actual disposable native child;
// these flags are never part of the framework/application API.
var fixtureAddr = flag.String("addr", "", "development child fixture address")
var fixtureEnvDir = flag.String("env-dir", "", "development child fixture environment directory")
var fixtureEnvMode = flag.String("env-mode", "", "development child fixture mode")
var fixtureGeneration = flag.String("dev-generation", "", "development child fixture generation")
var fixtureZoneHosts = flag.String("zone-hosts", "", "development child fixture hosts")
var fixtureLabel = flag.String("dev-test-label", "", "development child fixture label")
var fixtureReadyPath = flag.String("dev-ready", "", "development child fixture readiness file")
var fixtureRefreshSupported = flag.Bool("dev-test-refresh-supported", true, "development child fixture refresh capability")
var fixtureSkipReady = flag.Bool("dev-test-skip-ready", false, "development child fixture missing readiness")

func TestDevelopmentHTTPChildHelper(t *testing.T) {
	if os.Getenv("NEXTGO_TEST_DEV_SUPERVISOR") != "1" {
		return
	}
	if *fixtureEnvDir == "" || *fixtureEnvMode != "development" || *fixtureGeneration == "" {
		os.Exit(71)
	}
	if *fixtureReadyPath != "" && !*fixtureSkipReady {
		data, _ := json.Marshal(developmentReady{Version: 1, Build: *fixtureLabel, Generation: *fixtureGeneration, Supported: *fixtureRefreshSupported})
		if file, err := os.OpenFile(*fixtureReadyPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
			_, _ = file.Write(data)
			_ = file.Close()
		}
	}
	var nativeRequests atomic.Int64
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	listener, err := net.Listen("tcp", *fixtureAddr)
	if err != nil {
		os.Exit(72)
	}
	hosts := map[string]string{"one": "one.example", "two": "two.example"}
	if *fixtureZoneHosts != "" {
		if err := json.Unmarshal([]byte(*fixtureZoneHosts), &hosts); err != nil {
			os.Exit(74)
		}
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != hosts["one"] && r.Host != hosts["two"] {
			http.Error(w, "unknown fixture host", 421)
			return
		}
		if r.URL.Path == "/__fixture/native-counter" {
			fmt.Fprint(w, nativeRequests.Add(1))
			return
		}
		fmt.Fprintf(w, "%s|%s", *fixtureLabel, r.Host)
	}), ReadHeaderTimeout: time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		os.Exit(73)
	}
	os.Exit(0)
}

type controlledDevelopmentSource struct {
	value atomic.Value
	reads atomic.Int64
}

func (s *controlledDevelopmentSource) Fingerprint(ctx context.Context, _ string) (string, error) {
	s.reads.Add(1)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return s.value.Load().(string), nil
}

func awaitDevelopment(t *testing.T, description string, ready func() bool) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for " + description)
		case <-ticker.C:
		}
	}
}

func TestZonesSupervisorRecoversRetainsAssetsRestartsEnvironmentAndReapsChildren(t *testing.T) {
	t.Setenv("NEXTGO_TEST_DEV_SUPERVISOR", "1")
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source := &controlledDevelopmentSource{}
	source.value.Store("first")
	updates := make(chan string, 16)
	s := &supervisor{authority: "127.0.0.1:4230", zoneHosts: map[string]bool{"one.example": true, "two.example": true}, listeners: map[chan string]bool{updates: true}, output: io.Discard}
	s.installControlHosts("127.0.0.1:4230")
	var compiles atomic.Int64
	var mu sync.Mutex
	var children []*child
	var directories []string
	compile := func(context.Context) (*project.Build, error) {
		compiles.Add(1)
		version := source.value.Load().(string)
		if version == "broken" {
			return nil, fmt.Errorf("fixture compile failure")
		}
		dir, err := os.MkdirTemp(root, "generation-")
		if err != nil {
			return nil, err
		}
		build := &project.Build{Dir: dir, Binary: executable, ID: "aggregate-" + version, Fingerprint: version, ZoneAssets: map[string]string{}, ZoneRuntimeIDs: map[string]string{}, Zones: &project.ZonesManifest{Root: root, Zones: []project.ZoneManifest{{ID: "one", Host: "one.example", BasePath: "/one", Root: root + "/app"}, {ID: "two", Host: "two.example", BasePath: "/two", Root: root + "/app"}}}, ZoneProjects: []*project.Project{{Root: root + "/app", Routes: []project.Route{{Pattern: "/"}}}}}
		for _, zone := range build.Zones.Zones {
			runtime := zone.ID + "-" + version
			base := filepath.Join(dir, "server", "assets", zone.ID)
			path := filepath.Join(base, "_nextgo", runtime, "app.wasm")
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, []byte(runtime), 0644); err != nil {
				return nil, err
			}
			build.ZoneAssets[runtime] = base
			build.ZoneRuntimeIDs[zone.ID] = runtime
		}
		mu.Lock()
		directories = append(directories, dir)
		mu.Unlock()
		return build, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watchBuilds(ctx, developmentBuild{root: root, watcher: source, compile: compile, start: func(ctx context.Context, build *project.Build, _ *developmentPublication) (*child, *url.URL, error) {
			next, target, err := startWithArgs(ctx, build, root, io.Discard, []string{"-test.run=^TestDevelopmentHTTPChildHelper$", "-zone-hosts", `{"one":"one.example","two":"two.example"}`, "-dev-test-label", build.ID})
			if err == nil {
				mu.Lock()
				children = append(children, next)
				mu.Unlock()
			}
			return next, target, err
		}, validate: func(build *project.Build) (*developmentPublication, error) { return zonePublication(build, "") }})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Error("supervisor failed to shut down")
		}
	})
	ready := func(id string) bool {
		snapshot := s.inspectionSnapshot()
		return snapshot.State == "ready" && snapshot.BuildID == id && snapshot.Serving
	}
	awaitDevelopment(t, "first aggregate build", func() bool { return ready("aggregate-first") })
	requestView := func(want string) {
		t.Helper()
		r := httptest.NewRequest("GET", "http://one.example/one/", nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != want+"|one.example" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	requestView("aggregate-first")
	source.value.Store("broken")
	awaitDevelopment(t, "failed build preserving serving proxy", func() bool {
		v := s.inspectionSnapshot()
		return v.State == "failed" && v.Serving && v.Stale && v.BuildID == "aggregate-first"
	})
	requestView("aggregate-first")
	reads := source.reads.Load()
	awaitDevelopment(t, "unchanged failure polling", func() bool { return source.reads.Load() >= reads+2 })
	if compiles.Load() != 2 {
		t.Fatal("unchanged failed source retried", compiles.Load())
	}
	source.value.Store("second")
	awaitDevelopment(t, "recovered aggregate build", func() bool { return ready("aggregate-second") })
	requestView("aggregate-second")
	for _, zone := range []string{"one", "two"} {
		r := httptest.NewRequest("GET", "http://"+zone+".example/"+zone+"/_nextgo/"+zone+"-first/app.wasm", nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != zone+"-first" {
			t.Fatal("previous zone runtime was lost", w.Code, w.Body.String())
		}
	}
	mu.Lock()
	first := children[0]
	mu.Unlock()
	awaitDevelopment(t, "previous child exit", func() bool {
		select {
		case <-first.done:
			return true
		default:
			return false
		}
	})
	oldGeneration := s.inspectionSnapshot().Generation
	if err := os.WriteFile(filepath.Join(root, ".env.development"), []byte("NEXTGO_DEV_FIXTURE=value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitDevelopment(t, "environment-only restart", func() bool {
		v := s.inspectionSnapshot()
		return ready("aggregate-second") && v.Generation != oldGeneration
	})
	if compiles.Load() != 3 {
		t.Fatal("environment-only restart recompiled source", compiles.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("supervisor cleanup timed out")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(children) != 3 {
		t.Fatal("unexpected child count", len(children))
	}
	for _, c := range children {
		select {
		case <-c.done:
		default:
			t.Fatal("owned child still running")
		}
	}
	for _, dir := range directories {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("owned compiler output retained after shutdown", dir, err)
		}
	}
	reloads, failures := 0, 0
	for len(updates) > 0 {
		event := <-updates
		if strings.Contains(event, "event: reload") {
			reloads++
		}
		if strings.Contains(event, "event: build-error") {
			failures++
		}
	}
	if reloads != 3 || failures != 1 {
		t.Fatal("duplicate/missing browser updates", reloads, failures)
	}
}

type developmentLog struct {
	mu   sync.Mutex
	text bytes.Buffer
}

func (l *developmentLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.text.Write(b)
}
func (l *developmentLog) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.text.String() }

func TestZonesGlobalDotenvHostChangesKeepLastGoodAndReloadOnce(t *testing.T) {
	t.Setenv("NEXTGO_TEST_DEV_SUPERVISOR", "1")
	ambient := []string{}
	for _, pair := range os.Environ() {
		if !strings.HasPrefix(pair, "NEXTGO_ZONE_HOSTS=") {
			ambient = append(ambient, pair)
		}
	}
	root := t.TempDir()
	envFile := filepath.Join(root, ".env.development")
	writeHosts := func(host string) {
		t.Helper()
		if err := os.WriteFile(envFile, []byte("NEXTGO_ZONE_HOSTS='{"+`"one":"`+host+`","two":"two.example"}`+"'\nPRIVATE_DEV_VALUE=private-dotenv-value\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeHosts("first.example")
	initial, err := developmentZoneHostOverride(root, ambient)
	if err != nil {
		t.Fatal(err)
	}
	manifest := &project.ZonesManifest{Root: root, Zones: []project.ZoneManifest{{ID: "one", Host: "one.example", BasePath: "/one", Root: root + "/app"}, {ID: "two", Host: "two.example", BasePath: "/two", Root: root + "/app"}}}
	hosts, err := zoneDevelopmentHosts(manifest, initial)
	if err != nil {
		t.Fatal(err)
	}
	log := &developmentLog{}
	updates := make(chan string, 16)
	s := &supervisor{authority: "127.0.0.1:4230", zoneHosts: hosts, listeners: map[chan string]bool{updates: true}, output: log}
	s.installControlHosts("127.0.0.1:4230")
	source := &controlledDevelopmentSource{}
	source.value.Store("same-source")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "owned-build")
	build := &project.Build{Dir: dir, Binary: executable, ID: "same-bundle", Fingerprint: "same-source", Zones: manifest, ZoneAssets: map[string]string{}, ZoneRuntimeIDs: map[string]string{}, ZoneProjects: []*project.Project{{Root: root + "/app"}}}
	for _, zone := range manifest.Zones {
		runtime := zone.ID + "-runtime"
		base := filepath.Join(dir, "server", "assets", zone.ID)
		path := filepath.Join(base, "_nextgo", runtime, "app.wasm")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(runtime), 0644); err != nil {
			t.Fatal(err)
		}
		build.ZoneAssets[runtime] = base
		build.ZoneRuntimeIDs[zone.ID] = runtime
	}
	var compiles atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watchBuilds(ctx, developmentBuild{root: root, watcher: source, compile: func(context.Context) (*project.Build, error) { compiles.Add(1); return build, nil }, validate: func(build *project.Build) (*developmentPublication, error) {
			raw, err := developmentZoneHostOverride(root, ambient)
			if err != nil {
				return nil, err
			}
			return zonePublication(build, raw)
		}, start: func(ctx context.Context, build *project.Build, publication *developmentPublication) (*child, *url.URL, error) {
			return startWithEnvironment(ctx, build, root, log, []string{"-test.run=^TestDevelopmentHTTPChildHelper$", "-zone-hosts", publication.zoneHosts, "-dev-test-label", build.ID}, ambient)
		}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Error("dotenv fixture supervisor did not stop")
		}
	})
	awaitDevelopment(t, "dotenv-only startup host", func() bool { return s.inspectionSnapshot().State == "ready" })
	if !s.allowedHost("first.example") || s.allowedHost("one.example") {
		t.Fatal("startup ignored effective dotenv host")
	}
	get := func(host, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "http://"+host+path, nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	if w := get("first.example", "/one/"); w.Code != 200 || w.Body.String() != "same-bundle|first.example" {
		t.Fatal(w.Code, w.Body.String())
	}
	firstGeneration := s.inspectionSnapshot().Generation
	const rawSecret = "MALFORMED_PRIVATE_DOTENV_VALUE"
	if err := os.WriteFile(envFile, []byte("NEXTGO_ZONE_HOSTS='"+rawSecret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitDevelopment(t, "invalid dotenv preserving last good host", func() bool {
		v := s.inspectionSnapshot()
		return v.State == "failed" && v.Serving && v.Generation == firstGeneration
	})
	if !s.allowedHost("first.example") {
		t.Fatal("invalid dotenv replaced the host allowlist")
	}
	if w := get("first.example", "/one/"); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	reads := source.reads.Load()
	awaitDevelopment(t, "stable invalid dotenv polling", func() bool { return source.reads.Load() >= reads+2 })
	writeHosts("second.example")
	awaitDevelopment(t, "new dotenv host publication", func() bool { v := s.inspectionSnapshot(); return v.State == "ready" && v.Generation != firstGeneration })
	if compiles.Load() != 1 {
		t.Fatal("dotenv-only changes rebuilt source", compiles.Load())
	}
	if !s.allowedHost("second.example") || s.allowedHost("first.example") {
		t.Fatal("effective host map did not change atomically")
	}
	if w := get("second.example", "/one/"); w.Code != 200 || w.Body.String() != "same-bundle|second.example" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := get("first.example", "/one/_nextgo/one-runtime/app.wasm"); w.Code != 200 || w.Body.String() != "one-runtime" {
		t.Fatal("previous host lost retained unchanged-bundle assets", w.Code, w.Body.String())
	}
	if w := get("first.example", "/one/"); w.Code != 403 {
		t.Fatal("retained asset authority escaped to application proxy", w.Code)
	}
	snapshot, _ := json.Marshal(s.inspectionSnapshot())
	if strings.Contains(string(snapshot), rawSecret) || strings.Contains(log.String(), rawSecret) || strings.Contains(log.String(), "private-dotenv-value") {
		t.Fatal("private dotenv data escaped diagnostics")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("dotenv fixture cleanup timed out")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("shared output not removed on final shutdown", err)
	}
	reloads, failures := 0, 0
	for len(updates) > 0 {
		value := <-updates
		if strings.Contains(value, rawSecret) {
			t.Fatal("dotenv value escaped SSE")
		}
		if strings.Contains(value, "event: reload") {
			reloads++
		}
		if strings.Contains(value, "event: build-error") {
			failures++
		}
	}
	if reloads != 2 || failures != 1 {
		t.Fatal("dotenv changes generated duplicate/missing updates", reloads, failures)
	}
}
