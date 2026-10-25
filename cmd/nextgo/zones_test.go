package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/project"
)

func zonesFixtureBuild(t *testing.T, data string) *project.Build {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "server")
	if err := os.WriteFile(binary, []byte(data), 0755); err != nil {
		t.Fatal(err)
	}
	return &project.Build{Dir: dir, Binary: binary}
}

func TestZonesBuildPublishesOneStandaloneBinaryAndCleansGeneration(t *testing.T) {
	build := zonesFixtureBuild(t, "standalone zones native binary")
	destination := t.TempDir()
	server := filepath.Join(destination, "server")
	if err := os.WriteFile(server, []byte("old server"), 0755); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(destination, "operator-notes")
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	compiled := 0
	compile := func(got context.Context, manifest string, dev bool, writer io.Writer) (*project.Build, error) {
		compiled++
		if got != ctx || manifest != "apps/zones.json" || dev || writer != &output {
			t.Fatalf("wrong compile request: %v %q development=%v", got, manifest, dev)
		}
		return build, nil
	}
	if err := executeZonesWith(ctx, []string{"build", "-out", destination, "apps/zones.json"}, &output, compile, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(server)
	if err != nil || string(data) != "standalone zones native binary" || compiled != 1 {
		t.Fatalf("published server %q, compile count %d, error %v", data, compiled, err)
	}
	info, err := os.Stat(server)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("published server must be executable: %v %v", info, err)
	}
	if _, err := os.Stat(build.Dir); !os.IsNotExist(err) {
		t.Fatalf("temporary zones generation retained: %v", err)
	}
	if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep" {
		t.Fatalf("unrelated output changed: %q %v", data, err)
	}
	if !strings.Contains(output.String(), server) {
		t.Fatalf("missing standalone artifact path: %s", output.String())
	}
}

func TestZonesBuildFailuresPreservePublishedBinaryAndCleanTemporaryBuild(t *testing.T) {
	for _, failure := range []string{"compile", "read", "publish"} {
		t.Run(failure, func(t *testing.T) {
			build := zonesFixtureBuild(t, "new server")
			destination := t.TempDir()
			server := filepath.Join(destination, "server")
			if failure == "publish" {
				if err := os.Mkdir(server, 0755); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(server, []byte("last good server"), 0755); err != nil {
				t.Fatal(err)
			}
			if failure == "read" {
				if err := os.Remove(build.Binary); err != nil {
					t.Fatal(err)
				}
			}
			compileError := errors.New("zones configuration failed")
			compile := func(context.Context, string, bool, io.Writer) (*project.Build, error) {
				if failure == "compile" {
					return nil, compileError
				}
				return build, nil
			}
			var output bytes.Buffer
			err := executeZonesWith(context.Background(), []string{"build", "-out", destination, "zones.json"}, &output, compile, nil)
			if err == nil || failure == "compile" && !errors.Is(err, compileError) {
				t.Fatalf("missing build failure: %v", err)
			}
			if failure == "publish" {
				if info, err := os.Stat(server); err != nil || !info.IsDir() {
					t.Fatalf("publication replaced existing directory: %v %v", info, err)
				}
			} else if data, err := os.ReadFile(server); err != nil || string(data) != "last good server" {
				t.Fatalf("last good binary changed: %q %v", data, err)
			}
			if failure != "compile" {
				if _, err := os.Stat(build.Dir); !os.IsNotExist(err) {
					t.Fatalf("failed temporary generation retained: %v", err)
				}
			}
			temps, err := filepath.Glob(filepath.Join(destination, ".nextgo-server-*"))
			if err != nil || len(temps) != 0 {
				t.Fatalf("publication left temporary files: %v %v", temps, err)
			}
		})
	}
}

func TestZonesCommandRejectsUnsupportedRequestsBeforeCompiling(t *testing.T) {
	for _, args := range [][]string{
		{}, {"unknown"}, {"export", "zones.json"}, {"build"}, {"build", "one.json", "two.json"},
		{"build", "-unknown", "zones.json"}, {"build", "zones.json", "-out", "dist"}, {"build", "-out", "", "zones.json"},
		{"dev"}, {"dev", "one.json", "two.json"}, {"dev", "-out", "dist", "zones.json"}, {"dev", "-addr", "", "zones.json"},
		{"dev", "-zone-hosts", `{"marketing":"localhost:3000"}`, "zones.json"},
	} {
		var output bytes.Buffer
		compiled := false
		compile := func(context.Context, string, bool, io.Writer) (*project.Build, error) {
			compiled = true
			return nil, errors.New("must not compile")
		}
		developed := false
		develop := func(context.Context, string, string, io.Writer) error {
			developed = true
			return errors.New("must not develop")
		}
		if err := executeZonesWith(context.Background(), args, &output, compile, develop); err == nil || compiled || developed {
			t.Fatalf("unsupported request reached compiler: %v, compiled=%v, error=%v", args, compiled, err)
		}
	}
	err := execute(context.Background(), []string{"zones", "export", "zones.json"})
	if err == nil || !strings.Contains(err.Error(), "aggregate zones export is not supported") {
		t.Fatalf("public zones command not dispatched: %v", err)
	}
}

func TestZonesHelpDoesNotCompile(t *testing.T) {
	var output bytes.Buffer
	compile := func(context.Context, string, bool, io.Writer) (*project.Build, error) {
		t.Fatal("help attempted a build")
		return nil, nil
	}
	if err := executeZonesWith(context.Background(), []string{"help"}, &output, compile, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "zones build") || !strings.Contains(output.String(), "zones dev") {
		t.Fatalf("missing zones usage: %s", output.String())
	}
}

func TestZonesDevelopmentUsesSupervisorWithoutCompilingProductionOrChangingEnvironment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	operatorHosts := `{"marketing":"127.0.0.1:4230"}`
	t.Setenv("NEXTGO_ZONE_HOSTS", operatorHosts)
	compile := func(context.Context, string, bool, io.Writer) (*project.Build, error) {
		t.Fatal("development command compiled a production build")
		return nil, nil
	}
	runError := errors.New("supervisor stopped")
	develop := func(got context.Context, manifest, addr string, writer io.Writer) error {
		if got != ctx || manifest != "examples/zones/zones.json" || addr != "127.0.0.1:4230" || writer != &output {
			t.Fatalf("wrong development request: %v %q %q", got, manifest, addr)
		}
		if os.Getenv("NEXTGO_ZONE_HOSTS") != operatorHosts {
			t.Fatal("operator host mapping did not remain native process configuration")
		}
		return runError
	}
	err := executeZonesWith(ctx, []string{"dev", "-addr", "127.0.0.1:4230", "examples/zones/zones.json"}, &output, compile, develop)
	if !errors.Is(err, runError) || os.Getenv("NEXTGO_ZONE_HOSTS") != operatorHosts {
		t.Fatalf("development result or environment changed: %v", err)
	}
}
