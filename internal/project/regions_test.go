package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRegionsDiscoveryAndNativeOnlyRegistration(t *testing.T) {
	p, err := Discover(fixture(t, map[string]string{
		"app/page.go": "package app", "app/regions.go": "package app",
		"app/group_blog/posts/param_slug/page.go": "package slug", "app/group_blog/posts/param_slug/regions.go": "package slug",
		"app/plain/page.go": "package plain", "app/api/health/route.go": "package health",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range p.Routes {
		if route.Regions != (route.Pattern == "/" || route.Pattern == "/posts/:slug") {
			t.Fatal("registry leaked across route ancestry", route)
		}
	}
	native, browser := serverSource(p, "regions", false), clientSource(p, nil)
	if strings.Count(native, ".Regions(),") != 2 || strings.Contains(browser, ".Regions") || strings.Contains(browser, p.Import+"/app") {
		t.Fatal("region registration entered wrong target", native, browser)
	}
	if err := writeGo(filepath.Join(t.TempDir(), "main.go"), native); err != nil {
		t.Fatal(err)
	}
}

func TestRegionsDiscoveryRejectsUnsupportedOwners(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"orphan":           {"app/page.go": "package app", "app/orphan/regions.go": "package orphan"},
		"only-regions":     {"app/regions.go": "package app"},
		"api":              {"app/api/health/route.go": "package health", "app/api/health/regions.go": "package health"},
		"default-only":     {"app/layout.go": "package app", "app/default.go": "package app", "app/regions.go": "package app", "app/slot_panel/chart/page.go": "package chart"},
		"named-page":       {"app/layout.go": "package app", "app/page.go": "package app", "app/slot_panel/chart/page.go": "package chart", "app/slot_panel/chart/regions.go": "package chart"},
		"named-default":    {"app/layout.go": "package app", "app/page.go": "package app", "app/slot_panel/default.go": "package panel", "app/slot_panel/regions.go": "package panel"},
		"intercept":        {"app/layout.go": "package app", "app/page.go": "package app", "app/posts/page.go": "package posts", "app/slot_modal/default.go": "package modal", "app/slot_modal/intercept_root/posts/page.go": "package posts", "app/slot_modal/intercept_root/posts/regions.go": "package posts"},
		"composed-primary": {"app/layout.go": "package app", "app/page.go": "package app", "app/regions.go": "package app", "app/slot_panel/default.go": "package panel"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Discover(fixture(t, files)); err == nil || !strings.Contains(err.Error(), "regions.go") {
				t.Fatal("unsupported registry owner accepted", err)
			}
		})
	}
}

const regionFixturePage = `//go:build !js

package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text("static shell")},nil}
`
const regionFixtureRegistry = `//go:build !js

package app
import("encoding/json";nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui";"example.com/site/private")
func Regions()map[string]nextgo.RegionFunc{
 serverValue:=private.Value()
 return map[string]nextgo.RegionFunc{"account":func(_ *nextgo.Context,raw json.RawMessage)(ui.Node,error){return ui.Text(serverValue+string(raw)),nil}}
}
`

func regionCompilerFixture(t *testing.T, registry string, extra map[string]string) (*Project, string) {
	t.Helper()
	files := map[string]string{"app/page.go": regionFixturePage, "app/regions.go": registry, "private/value.go": `//go:build !js

package private
import _ "github.com/Suhaibinator/Next.go/serveronly"
func Value()string{return "server-only closure value"}
`}
	for name, value := range extra {
		files[name] = value
	}
	root := fixture(t, files)
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := fmt.Sprintf("module example.com/site\n\ngo 1.24\nrequire %s v0.0.0\nreplace %s => %s\n", framework, framework, strconv.Quote(repo))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	server := filepath.Join(p.Root, "generated-server")
	for _, dir := range []string{"assets", "private"} {
		if err := os.MkdirAll(filepath.Join(server, dir), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(server, dir, "placeholder"), []byte("fixture"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeGo(filepath.Join(server, "main.go"), serverSource(p, "regions-fixture", false)); err != nil {
		t.Fatal(err)
	}
	return p, server
}
func regionCompilerRun(t *testing.T, p *Project, environment []string, args ...string) ([]byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = p.Root
	cmd.Env = append(cmd.Environ(), "GOWORK=off")
	cmd.Env = append(cmd.Env, environment...)
	return cmd.CombinedOutput()
}

func TestRegionsGeneratedNativeCompilerAndBrowserSeparation(t *testing.T) {
	p, server := regionCompilerFixture(t, regionFixtureRegistry, map[string]string{
		"components/counter/client_js.go": `//go:build js && wasm

package counter
import("encoding/json";"github.com/Suhaibinator/Next.go/client")
func Mount(*client.Scope,json.RawMessage)error{return nil}
`,
	})
	if output, err := regionCompilerRun(t, p, nil, "build", "-o", filepath.Join(t.TempDir(), "server"), server); err != nil {
		t.Fatalf("generated region server: %v\n%s", err, output)
	}
	browser := filepath.Join(p.Root, "generated-browser")
	if err := os.MkdirAll(browser, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeGo(filepath.Join(browser, "main.go"), clientSource(p, nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := validateBrowserGraph(ctx, p, browser); err != nil {
		t.Fatal(err)
	}
	if output, err := regionCompilerRun(t, p, []string{"GOOS=js", "GOARCH=wasm"}, "build", "-o", filepath.Join(t.TempDir(), "app.wasm"), browser); err != nil {
		t.Fatalf("browser build imported native registry closure: %v\n%s", err, output)
	}
	deps, err := regionCompilerRun(t, p, []string{"GOOS=js", "GOARCH=wasm"}, "list", "-deps", browser)
	if err != nil {
		t.Fatal(err, string(deps))
	}
	for _, dep := range strings.Fields(string(deps)) {
		if dep == p.Import+"/private" || dep == p.Import+"/app" || dep == framework || dep == framework+"/serveronly" {
			t.Fatal("native region closure leaked into browser graph", dep)
		}
	}
}

func TestRegionsGeneratedCompilerRejectsInvalidFactoryAndHandlerSignatures(t *testing.T) {
	for name, registry := range map[string]string{
		"factory-argument": strings.Replace(regionFixtureRegistry, "func Regions()", "func Regions(int)", 1),
		"factory-result": `package app
func Regions()map[string]string{return map[string]string{"account":"wrong"}}
`,
		"handler-argument": strings.Replace(regionFixtureRegistry, "raw json.RawMessage", "raw string", 1),
		"handler-result":   strings.Replace(regionFixtureRegistry, "(ui.Node,error){return ui.Text(serverValue+string(raw)),nil}", "(string,error){return serverValue+string(raw),nil}", 1),
	} {
		t.Run(name, func(t *testing.T) {
			p, server := regionCompilerFixture(t, registry, nil)
			output, err := regionCompilerRun(t, p, nil, "build", "-o", filepath.Join(t.TempDir(), "server"), server)
			if err == nil || !strings.Contains(string(output), ".Regions") && !strings.Contains(string(output), "nextgo.RegionFunc") {
				t.Fatalf("invalid registry compiled or lost compiler diagnostic: %v\n%s", err, output)
			}
		})
	}
}

func TestRegionsBrowserGraphRejectsTransitiveClosureImports(t *testing.T) {
	p, _ := regionCompilerFixture(t, regionFixtureRegistry, map[string]string{
		"app/props.go":     "package app\nvar PublicProp=1\n",
		"bridge/shared.go": "package bridge\nimport \"example.com/site/app\"\nfunc Closure()func()int{return func()int{return app.PublicProp}}\n",
		"components/counter/client_js.go": `//go:build js && wasm

package counter
import("encoding/json";"github.com/Suhaibinator/Next.go/client";"example.com/site/bridge")
func Mount(*client.Scope,json.RawMessage)error{_ = bridge.Closure();return nil}
`,
	})
	browser := filepath.Join(p.Root, "generated-browser")
	if err := os.MkdirAll(browser, 0755); err != nil {
		t.Fatal(err)
	}
	if err := writeGo(filepath.Join(browser, "main.go"), clientSource(p, nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := validateBrowserGraph(ctx, p, browser); err == nil || !strings.Contains(err.Error(), "imports application route code") {
		t.Fatal("component closure imported server registry package", err)
	}
}
