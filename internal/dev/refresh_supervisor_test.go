package dev

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/project"
)

func TestNativeChildReadinessIsBoundToItsActualStartup(t *testing.T) {
	t.Setenv("NEXTGO_TEST_DEV_SUPERVISOR", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		extra []string
		ready bool
	}{{"supported", nil, true}, {"unsupported", []string{"-dev-test-refresh-supported=false"}, false}, {"missing", []string{"-dev-test-skip-ready=true"}, false}} {
		t.Run(tc.name, func(t *testing.T) {
			build := refreshFixture(t, 1)
			build.Binary = executable
			extra := append([]string{"-test.run=^TestDevelopmentHTTPChildHelper$", "-dev-test-label", build.ID}, tc.extra...)
			c, _, err := startWithArgs(context.Background(), build, t.TempDir(), io.Discard, extra)
			if err != nil {
				t.Fatal(err)
			}
			defer c.stop()
			if c.refreshReady != tc.ready {
				t.Fatal("native capability did not match startup assertion", c.refreshReady)
			}
			files, err := filepath.Glob(filepath.Join(build.Dir, ".dev-ready-*"))
			if err != nil || len(files) != 0 {
				t.Fatal("readiness file retained", files, err)
			}
		})
	}
}

func TestSingleApplicationRefreshPreservesNativeStateAndUsesOrdinaryFallback(t *testing.T) {
	t.Setenv("NEXTGO_TEST_DEV_SUPERVISOR", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	source := &controlledDevelopmentSource{}
	source.value.Store("baseline")
	names := []string{"baseline", "client", "slow", "latest", "unsafe", "unsupported", "unsupported-client"}
	builds := map[string]*project.Build{}
	for i, name := range names {
		b := refreshFixture(t, i+1)
		b.Binary = executable
		b.Fingerprint = name
		builds[name] = b
	}
	builds["unsafe"].DevRefresh.Compatibility = strings.Repeat("d", 64)
	builds["unsupported"].DevRefresh.Compatibility = strings.Repeat("e", 64)
	builds["unsupported-client"].DevRefresh.Compatibility = strings.Repeat("e", 64)
	updates := make(chan string, 64)
	s := &supervisor{authority: "127.0.0.1:4230", output: io.Discard, listeners: map[chan string]bool{updates: true}}
	var starts atomic.Int64
	var mu sync.Mutex
	var children []*child
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.watchBuilds(ctx, developmentBuild{root: root, watcher: source, compile: func(context.Context) (*project.Build, error) {
			name := source.value.Load().(string)
			if name == "broken" {
				return nil, errors.New("test compile error")
			}
			if name == "slow" {
				source.value.Store("latest")
			}
			return builds[name], nil
		}, start: func(ctx context.Context, b *project.Build, _ *developmentPublication) (*child, *url.URL, error) {
			extra := []string{"-test.run=^TestDevelopmentHTTPChildHelper$", "-dev-test-label", b.ID, "-zone-hosts", `{"one":"127.0.0.1:4230","two":"localhost:4230"}`}
			if b == builds["unsupported"] || b == builds["unsupported-client"] {
				extra = append(extra, "-dev-test-refresh-supported=false")
			}
			c, target, err := startWithArgs(ctx, b, root, io.Discard, extra)
			if err == nil {
				starts.Add(1)
				mu.Lock()
				children = append(children, c)
				mu.Unlock()
			}
			return c, target, err
		}})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(8 * time.Second):
			t.Error("refresh supervisor failed to stop")
		}
	})
	awaitDevelopment(t, "refresh-capable native baseline", func() bool { v := s.inspectionSnapshot(); return v.State == "ready" && v.RefreshEnabled })
	baseline := s.inspectionSnapshot()
	mu.Lock()
	pid := children[0].cmd.Process.Pid
	mu.Unlock()
	nativeCounter := func(want string) {
		t.Helper()
		r := httptest.NewRequest("GET", "http://127.0.0.1:4230/__fixture/native-counter", nil)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 200 || w.Body.String() != want {
			t.Fatal("native state changed unexpectedly", w.Code, w.Body.String(), want)
		}
	}
	nativeCounter("1")
	source.value.Store("client")
	awaitDevelopment(t, "eligible client refresh", func() bool { return s.inspectionSnapshot().RefreshRevision == 1 })
	v := s.inspectionSnapshot()
	if v.BuildID != baseline.BuildID || v.Generation != baseline.Generation || starts.Load() != 1 {
		t.Fatal("eligible edit restarted native child", v, starts.Load())
	}
	nativeCounter("2")
	mu.Lock()
	if children[0].cmd.Process.Pid != pid {
		t.Fatal("native PID changed")
	}
	mu.Unlock()
	source.value.Store("broken")
	awaitDevelopment(t, "failed edit retaining last good refresh", func() bool {
		v := s.inspectionSnapshot()
		return v.State == "failed" && v.RefreshRevision == 1 && v.Serving
	})
	nativeCounter("3")
	source.value.Store("client")
	awaitDevelopment(t, "no-op source recovery", func() bool { v := s.inspectionSnapshot(); return v.State == "ready" && v.RefreshRevision == 2 })
	nativeCounter("4")
	source.value.Store("slow")
	awaitDevelopment(t, "coalesced latest source", func() bool { return s.inspectionSnapshot().RefreshRevision == 3 })
	nativeCounter("5")
	if _, err := os.Stat(builds["slow"].Dir); !os.IsNotExist(err) {
		t.Fatal("superseded output was not discarded", err)
	}
	if starts.Load() != 1 {
		t.Fatal("coalescing restarted native state", starts.Load())
	}
	if err := os.WriteFile(filepath.Join(root, ".env.development"), []byte("REFRESH_ENV_CHANGE=value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	awaitDevelopment(t, "environment ordinary rebuild", func() bool {
		v := s.inspectionSnapshot()
		return v.State == "ready" && v.BuildID == builds["latest"].ID && v.Generation != baseline.Generation && v.RefreshRevision == 0
	})
	nativeCounter("1")
	source.value.Store("unsafe")
	awaitDevelopment(t, "native/core incompatibility ordinary rebuild", func() bool {
		v := s.inspectionSnapshot()
		return v.State == "ready" && v.BuildID == builds["unsafe"].ID && v.RefreshRevision == 0
	})
	nativeCounter("1")
	source.value.Store("unsupported")
	awaitDevelopment(t, "unsupported effective runtime", func() bool {
		v := s.inspectionSnapshot()
		return v.State == "ready" && v.BuildID == builds["unsupported"].ID && !v.RefreshEnabled
	})
	nativeCounter("1")
	source.value.Store("unsupported-client")
	awaitDevelopment(t, "unsupported runtime stays on ordinary rebuild", func() bool {
		v := s.inspectionSnapshot()
		return v.State == "ready" && v.BuildID == builds["unsupported-client"].ID && !v.RefreshEnabled
	})
	nativeCounter("1")
	if starts.Load() != 5 {
		t.Fatal("unexpected native start count", starts.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("cleanup timed out")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, c := range children {
		select {
		case <-c.done:
		default:
			t.Fatal("native child remained active")
		}
	}
	reloads, refreshes, failures := 0, 0, 0
	for len(updates) > 0 {
		message := <-updates
		if strings.Contains(message, "event: reload") {
			reloads++
		}
		if strings.Contains(message, "event: refresh") {
			refreshes++
		}
		if strings.Contains(message, "event: build-error") {
			failures++
		}
		if strings.Contains(message, "/_nextgo/"+builds["slow"].ID+"/") {
			t.Fatal("superseded module was published")
		}
	}
	if reloads != 5 || refreshes != 3 || failures != 1 {
		t.Fatal("publication counts", reloads, refreshes, failures)
	}
	for _, b := range builds {
		if _, err := os.Stat(b.Dir); !os.IsNotExist(err) {
			t.Fatal("owned output retained after shutdown", b.Dir, err)
		}
	}
}
