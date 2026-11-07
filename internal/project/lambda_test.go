package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLambdaArchitectureRejectedBeforeDiscovery(t *testing.T) {
	for _, arch := range []string{"", "x86_64", "386", "ARM64"} {
		if _, err := CompileLambda(context.Background(), filepath.Join(t.TempDir(), "absent"), arch, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "explicit -arch") {
			t.Fatal(arch, err)
		}
	}
}

func TestLambdaOutputDoesNotEnterFutureInputClosure(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app"})
	plan := inputPlan{}
	for name, content := range map[string]string{
		"embed.go":  "package site\nimport \"embed\"\n//go:embed all:dist/*\nvar data embed.FS\n",
		"future.go": "package site\nimport _ \"embed\"\n//go:embed dist/lambda/function.zip\nvar data []byte\n",
	} {
		file := filepath.Join(root, name)
		if err := os.WriteFile(file, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if err := lambdaCheckDeclarations(context.Background(), root, filepath.Join(root, "dist", "lambda"), plan); err == nil {
			t.Fatal("future output admitted", name)
		}
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
	if err := lambdaCheckDeclarations(context.Background(), root, filepath.Join(root, "dist", "lambda"), plan); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, assetPipelineManifest), []byte(`{"version":1,"tools":[{"name":"copy","command":["never-run"],"inputs":["dist"],"outputs":["copy.css"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := lambdaCheckDeclarations(context.Background(), root, filepath.Join(root, "dist", "lambda"), plan); err == nil {
		t.Fatal("declared asset input admitted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := lambdaCheckDeclarations(ctx, root, filepath.Join(root, "dist", "lambda"), plan); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestCompileLambdaPreservesHostPrerenderBeforeTargetLink(t *testing.T) {
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := fixture(t, map[string]string{
		"app/page.go": `//go:build !js
package app
import("fmt";"os";"runtime";nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(c *nextgo.Context)(nextgo.Page,error){if err:=os.WriteFile(os.Getenv("LAMBDA_TEST_MARKER"),[]byte(fmt.Sprintf("%s/%s",runtime.GOOS,runtime.GOARCH)),0600);err!=nil{return nextgo.Page{},err};return nextgo.Page{Body:ui.Text("captured on host")},nil}
`,
		"app/prerender.go": `//go:build !js
package app
import nextgo "github.com/Suhaibinator/Next.go"
func Prerender()*nextgo.PrerenderPolicy{return &nextgo.PrerenderPolicy{HostIndependent:true}}
`,
	})
	module := fmt.Sprintf("module example.com/lambda\n\ngo 1.24\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => %s\n", strconv.Quote(frameworkRoot))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "host.txt")
	t.Setenv("LAMBDA_TEST_MARKER", marker)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// Always select the other architecture: executing this final artifact as a
	// capture probe would fail even on a Linux test host.
	arch := "arm64"
	if runtime.GOARCH == "arm64" {
		arch = "amd64"
	}
	var output bytes.Buffer
	build, err := CompileLambda(ctx, root, arch, &output)
	if err != nil {
		t.Fatal(err, output.String())
	}
	defer os.RemoveAll(build.Dir)
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatalf("host capture: %q %v", data, err)
	}
	if info, err := os.Stat(build.Binary); err != nil || info.Size() == 0 {
		t.Fatal("missing target binary", err)
	}
}

func TestLambdaOutputPlacementAndCancellation(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app"})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, rel := range []string{".", "app/package", "public/package", "components/dist/package", "dist/public/package"} {
		if _, err := ValidateLambdaOutput(ctx, root, filepath.Join(root, rel)); err == nil {
			t.Fatal("source/public output admitted", rel)
		}
	}
	for _, destination := range []string{filepath.Join(root, "dist", "lambda"), filepath.Join(t.TempDir(), "lambda")} {
		if _, err := ValidateLambdaOutput(ctx, root, destination); err != nil {
			t.Fatal(destination, err)
		}
	}
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := ValidateLambdaOutput(cancelled, root, filepath.Join(root, "dist", "lambda")); err == nil {
		t.Fatal("cancellation ignored")
	}
}
