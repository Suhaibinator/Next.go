package project

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Compile the generated native startup directly, without a full application or
// WASM build. Configure records the phase it actually observes in each process.
func TestGeneratedConfigurationPhases(t *testing.T) {
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeInput(t, root, "go.mod", fmt.Sprintf("module phase.fixture\n\ngo 1.24\n\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => %s\n", strconv.Quote(frameworkRoot)))
	writeInput(t, root, "app/app.go", `package app
import("context";"fmt";"os";nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
var phase nextgo.Phase
func Configure(c *nextgo.Config){phase=c.Phase;if name:=os.Getenv("NEXTGO_TEST_PHASE_RECORD");name!=""{if err:=os.WriteFile(name,[]byte(phase),0600);err!=nil{panic(err)}};c.BasePath="/"+string(phase);if os.Getenv("NEXTGO_TEST_INVALID_PHASE")=="true"{c.Phase="unknown"}}
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text("phase fixture")},nil}
func StaticParams(context.Context)([]nextgo.Params,error){if phase!=nextgo.PhaseStaticExport{return nil,fmt.Errorf("enumeration observed %s",phase)};return []nextgo.Params{{"slug":"known"}},nil}
`)
	p := &Project{Import: "phase.fixture", Configure: "phase.fixture/app", Routes: []Route{{Pattern: "/items/:slug", Import: "phase.fixture/app", StaticParams: true}}}
	for _, dev := range []bool{false, true} {
		entry := fmt.Sprintf("entry-%t", dev)
		writeInput(t, root, entry+"/assets/public/placeholder", "asset")
		writeInput(t, root, entry+"/private/prerender.json", "{}")
		if err := writeGo(filepath.Join(root, entry, "main.go"), serverSource(p, "phase-fixture", dev)); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(root, fmt.Sprintf("server-%t", dev))
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./"+entry)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "GOWORK=off")
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("compile generated startup: %v\n%s", err, output)
		}
		if dev {
			runPhaseProbe(t, binary, "development", true, "-path-config")
			continue
		}
		for _, args := range [][]string{{"-path-config"}, {"-export-config"}, {"-export-check"}, {"-static-paths"}, {"-static-export", "-path-config"}, {"-prerender-out", filepath.Join(root, "captured.json")}} {
			want := "static-export"
			if args[0] == "-path-config" {
				want = "production"
			}
			if args[0] == "-prerender-out" {
				want = "prerender"
			}
			out := runPhaseProbe(t, binary, want, true, args...)
			if args[0] == "-export-config" {
				var value struct{ Paths struct{ BasePath string } }
				if err := json.Unmarshal(out, &value); err != nil || value.Paths.BasePath != "/static-export" {
					t.Fatal("export configuration phase", string(out), err)
				}
			}
			if args[0] == "-static-paths" && strings.TrimSpace(string(out)) != `["/items/known"]` {
				t.Fatal("static enumeration did not use export configuration", string(out))
			}
		}
		for _, args := range [][]string{{"-prerender-out", filepath.Join(root, "bad.json"), "-export-config"}, {"-prerender-out", filepath.Join(root, "bad.json"), "-static-export"}} {
			runPhaseProbe(t, binary, "", false, args...)
		}
		ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		cmd = exec.CommandContext(ctx, binary, "-path-config")
		cmd.Env = append(os.Environ(), "NEXTGO_TEST_INVALID_PHASE=true", "NEXTGO_TEST_PHASE_RECORD=", "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_ENV_DIR=")
		output, err = cmd.CombinedOutput()
		cancel()
		if err == nil || !strings.Contains(string(output), "unknown configuration phase") {
			t.Fatal("Configure phase override was not validated", string(output), err)
		}
	}
}

func runPhaseProbe(t *testing.T, binary, want string, success bool, args ...string) []byte {
	t.Helper()
	record := filepath.Join(t.TempDir(), "observed-phase")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "NEXTGO_TEST_PHASE_RECORD="+record, "NEXTGO_TEST_INVALID_PHASE=", "AWS_LAMBDA_RUNTIME_API=", "NEXTGO_ENV_DIR=")
	output, err := cmd.CombinedOutput()
	if success && err != nil {
		t.Fatalf("phase probe %v: %v\n%s", args, err, output)
	}
	if !success {
		if err == nil || !strings.Contains(string(output), "prerender and static export purposes conflict") {
			t.Fatal("conflicting process flags accepted", args, string(output), err)
		}
		if _, err := os.Stat(record); !os.IsNotExist(err) {
			t.Fatal("conflicting purpose flags executed Configure")
		}
		return output
	}
	observed, err := os.ReadFile(record)
	if err != nil || string(observed) != want {
		t.Fatal("Configure saw wrong phase", args, string(observed), want, err)
	}
	return output
}
