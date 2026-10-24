package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBrowserDependencyGraphDiagnosesServerBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, server, client string
		extra                map[string]string
		want                 string
	}{
		{name: "proper server constraint", server: "//go:build !js\n\npackage counter", client: "package counter"},
		{name: "missing server constraint", server: "package counter", client: "package counter", want: "includes server.go"},
		{name: "transitive route dependency", server: "//go:build !js\n\npackage counter", client: "package counter\nimport _ \"example.com/site/shared\"", extra: map[string]string{"shared/value.go": "package shared\nimport _ \"example.com/site/app\""}, want: "imports application route code"},
	} {
		t.Run(test.name, func(t *testing.T) {
			files := map[string]string{"app/page.go": "package app", "components/counter/server.go": test.server, "components/counter/client_js.go": test.client, "browser/main.go": "package main\nimport _ \"example.com/site/components/counter\"\nfunc main(){}"}
			for path, source := range test.extra {
				files[path] = source
			}
			root := fixture(t, files)
			project, err := Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			err = validateBrowserGraph(ctx, project, filepath.Join(root, "browser"))
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("wanted %q, got%v", test.want, err)
			}
		})
	}
}

func TestCompilerEnforcesImportMarkerTargets(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, goos, arch string
		allowed          bool
	}{
		{"./cache/shared", runtime.GOOS, runtime.GOARCH, true}, {"./cache/shared", "js", "wasm", false},
		{"./environment", runtime.GOOS, runtime.GOARCH, true}, {"./environment", "js", "wasm", false}, {"./serveronly", runtime.GOOS, runtime.GOARCH, true}, {"./clientonly", runtime.GOOS, runtime.GOARCH, false}, {"./serveronly", "js", "wasm", false}, {"./clientonly", "js", "wasm", true},
	} {
		t.Run(test.path+test.goos, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "go", "build", test.path)
			command.Dir = root
			command.Env = append(command.Environ(), "GOWORK=off", "GOOS="+test.goos, "GOARCH="+test.arch)
			output, err := command.CombinedOutput()
			if test.allowed && err != nil {
				t.Fatalf("allowed target failed: %s %v", output, err)
			}
			if !test.allowed && (err == nil || !strings.Contains(string(output), "build constraints exclude all Go files")) {
				t.Fatalf("target barrier not enforced: %s %v", output, err)
			}
		})
	}
}

func TestCompileIndependentModuleWithTargetMarkers(t *testing.T) {
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := fixture(t, map[string]string{
		"app/page.go": `//go:build !js

package app
import(nextgo "github.com/Suhaibinator/Next.go";"example.com/site/components/counter")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Title:"Independent",Body:counter.View()},nil}`,
		"components/counter/server.go": `//go:build !js

package counter
import("github.com/Suhaibinator/Next.go/ui";_ "github.com/Suhaibinator/Next.go/serveronly")
func View()ui.Node{return ui.Text("Independent module")}`,
		"components/counter/client_js.go": `//go:build js && wasm

package counter
import("encoding/json";"github.com/Suhaibinator/Next.go/client";_ "github.com/Suhaibinator/Next.go/clientonly")
func Mount(*client.Scope,json.RawMessage)error{return nil}`,
	})
	module := fmt.Sprintf("module example.com/site\n\ngo 1.24\n\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => %s\n", strconv.Quote(frameworkRoot))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var log bytes.Buffer
	build, err := Compile(ctx, root, false, &log)
	if err != nil {
		t.Fatalf("independent module build failed: %v\n%s", err, log.String())
	}
	defer os.RemoveAll(build.Dir)
	if info, err := os.Stat(build.Binary); err != nil || info.Size() == 0 {
		t.Fatalf("missing native binary: %v", err)
	}
}
