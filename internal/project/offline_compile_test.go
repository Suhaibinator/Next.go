package project

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/offline"
)

// Real compiler fixture. Run only with the repository source freeze required by
// the full native suite: it compiles core, lazy, worker and generated native main.
func TestCompileOfflineProductionArtifacts(t *testing.T) {
	t.Setenv("GOWORK", "off")
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeInput(t, root, "go.mod", fmt.Sprintf("module offline.fixture\n\ngo 1.24\nrequire %s v0.0.0\nreplace %s => %s\n", framework, framework, strconv.Quote(frameworkRoot)))
	writeInput(t, root, "app/page.go", `//go:build !js
package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text("offline compiler fixture")},nil}
`)
	writeInput(t, root, "app/offline.go", `//go:build !js
package app
import "github.com/Suhaibinator/Next.go/offline"
func Offline()offline.Config{panic("offline provider must not run during compile or an unsupported purpose")}
`)
	writeInput(t, root, "app/config.go", `//go:build !js
package app
import nextgo "github.com/Suhaibinator/Next.go"
func Configure(*nextgo.Config){panic("Configure must not run during compile or an unsupported purpose")}
`)
	writeInput(t, root, "public/style.css", "body{color:#163b28}")
	writeInput(t, root, "components/lazy/component.json", `{"loading":"lazy"}`)
	writeInput(t, root, "components/lazy/client_js.go", `//go:build js && wasm
package lazy
import("encoding/json";"github.com/Suhaibinator/Next.go/client")
func Mount(*client.Scope,json.RawMessage)error{return nil}
`)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	var logs bytes.Buffer
	first, err := Compile(ctx, root, false, &logs)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer os.RemoveAll(first.Dir)
	if first.Project.Offline != "offline.fixture/app" || first.Project.offlineBuild == nil {
		t.Fatal("missing native offline discovery/build metadata")
	}
	plan := first.Project.offlineBuild
	counts := map[offline.AssetKind]int{}
	assets := os.DirFS(filepath.Join(first.Dir, "server", "assets"))
	var workerSHA string
	for _, entry := range plan.build.Files {
		counts[entry.Kind]++
		body, err := fs.ReadFile(assets, entry.Path)
		if err != nil || int64(len(body)) != entry.DecodedBytes || offlineSHA(body) != entry.SHA256 {
			t.Fatalf("actual compiled dependency differs: %+v %v", entry, err)
		}
		if entry.Kind == offline.WorkerWASM {
			workerSHA = entry.SHA256
			if string(body[:8]) != "\x00asm\x01\x00\x00\x00" {
				t.Fatal("worker is not WASM")
			}
		}
	}
	for _, kind := range []offline.AssetKind{offline.WorkerWASM, offline.CoreWASM, offline.RuntimeShim, offline.BootScript, offline.LazyWASM, offline.Style} {
		if counts[kind] != 1 {
			t.Fatalf("missing/duplicate actual dependency %s: %v", kind, counts)
		}
	}
	if strings.Contains(plan.build.BridgeTemplate, offlineShimPlaceholder) || strings.Count(plan.build.BridgeTemplate, offline.BuildDescriptorPlaceholder) != 1 {
		t.Fatal("paired shim/template was not finalized before runtime identity")
	}
	for _, flag := range []string{"-export-check", "-static-paths", "-static-export", "-prerender-out=" + filepath.Join(t.TempDir(), "forbidden.json")} {
		command := exec.CommandContext(ctx, first.Binary, flag)
		command.Dir = t.TempDir()
		command.Env = append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_ENV_DIR=")
		out, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "managed offline supports production serving only") || strings.Contains(string(out), "panic:") {
			t.Fatalf("unsupported purpose ran application code: %s %v %s", flag, err, out)
		}
	}
	writeInput(t, root, "public/style.css", "body{color:#123456}")
	logs.Reset()
	second, err := Compile(ctx, root, false, &logs)
	if err != nil {
		t.Fatal(err, logs.String())
	}
	defer os.RemoveAll(second.Dir)
	if first.ID == second.ID {
		t.Fatal("changed immutable CSS retained the runtime ID")
	}
	for _, entry := range second.Project.offlineBuild.build.Files {
		if entry.Kind == offline.WorkerWASM && entry.SHA256 != workerSHA {
			t.Fatal("worker unexpectedly imports application/public policy inputs")
		}
	}
}

func TestOfflineZonesRejectedBeforeGenerating(t *testing.T) {
	root := fixture(t, map[string]string{"site/app/page.go": "package app", "site/app/offline.go": offlineProvider})
	writeInput(t, root, "zones.json", `{"version":1,"zones":[{"id":"site","host":"example.test","app":"site"}]}`)
	var logs bytes.Buffer
	if _, err := CompileZones(context.Background(), filepath.Join(root, "zones.json"), false, &logs); err == nil || !strings.Contains(err.Error(), "managed offline is unavailable") {
		t.Fatal(err, logs.String())
	}
	if _, err := os.Stat(filepath.Join(root, "site", "routes", "nextgo_gen.go")); !os.IsNotExist(err) {
		t.Fatal("unsupported aggregate purpose generated routes", err)
	}
}

func TestOfflineWorkerRejectsIndirectDocumentClientGraph(t *testing.T) {
	t.Setenv("GOWORK", "off")
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeInput(t, root, "go.mod", fmt.Sprintf("module worker.fixture\n\ngo 1.24\nrequire %s v0.0.0\nreplace %s => %s\n", framework, framework, strconv.Quote(frameworkRoot)))
	writeInput(t, root, "entry/main.go", "package main\nimport _ \"worker.fixture/indirect\"\nfunc main(){}\n")
	writeInput(t, root, "indirect/indirect.go", "package indirect\nimport _ \"github.com/Suhaibinator/Next.go/client\"\n")
	p := &Project{Root: root, Import: "worker.fixture"}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	core, _, err := browserBuildTags(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBrowserGraph(ctx, p, filepath.Join(root, "entry"), core); err != nil {
		t.Fatal("ordinary document graph rejected", err)
	}
	if err := validateOfflineWorkerGraph(ctx, p, filepath.Join(root, "entry"), core); err == nil || !strings.Contains(err.Error(), "document client runtime") {
		t.Fatal("worker accepted indirect document dependency", err)
	}
}
