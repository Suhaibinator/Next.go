package project

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPrerenderDiscoveryAndGeneratedBuildProbe(t *testing.T) {
	p, err := Discover(fixture(t, map[string]string{"app/page.go": "package app", "app/prerender.go": "package app", "app/posts/param_slug/page.go": "package slug", "app/posts/param_slug/prerender.go": "package slug", "app/posts/param_slug/staticparams.go": "package slug"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Routes {
		if !r.Prerender {
			t.Fatal(r)
		}
	}
	source := serverSource(p, "code", false)
	for _, fragment := range []string{"all:assets all:private", "Prerender:", ".Prerender()", "PrerenderBuildMode=", "PrerenderFS", "BuildPrerender(buildCtx)", "prerender-out"} {
		if !strings.Contains(source, fragment) {
			t.Fatal("missing", fragment)
		}
	}
	if err := writeGo(filepath.Join(t.TempDir(), "main.go"), source); err != nil {
		t.Fatal(err)
	}
}
func TestPrerenderDiscoveryRejectsOrphansAndEndpoints(t *testing.T) {
	for _, files := range []map[string]string{{"app/page.go": "package app", "app/other/prerender.go": "package other"}, {"app/api/route.go": "package api", "app/api/prerender.go": "package api"}} {
		if _, err := Discover(fixture(t, files)); err == nil {
			t.Fatal("accepted", files)
		}
	}
}

// Runtime-only configuration (including secrets/services) need not be available
// during a compile-only build. Opt-in files enable the build execution phase.
func TestCompileWithoutPrerenderDoesNotExecuteConfigure(t *testing.T) {
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root := fixture(t, map[string]string{
		"app/page.go": `//go:build !js
package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text("runtime only")},nil}
`,
		"app/config.go": `//go:build !js
package app
import nextgo "github.com/Suhaibinator/Next.go"
func Configure(*nextgo.Config){panic("runtime configuration must not execute at build time")}
`,
	})
	module := fmt.Sprintf("module example.com/site\n\ngo 1.24\n\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => %s\n", strconv.Quote(frameworkRoot))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var output bytes.Buffer
	build, err := Compile(ctx, root, false, &output)
	if err != nil {
		t.Fatal(err, output.String())
	}
	defer os.RemoveAll(build.Dir)
	if info, err := os.Stat(build.Binary); err != nil || info.Size() == 0 {
		t.Fatal("missing compile-only binary", err)
	}
}
