package project

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/devrefresh"
)

func TestComponentRefreshRequiresExplicitLazyVersionAndSchema(t *testing.T) {
	valid := `{"loading":"lazy","refresh":{"version":1,"stateSchema":"counter/v1","domSchema":"counter-dom/v1"}}`
	root := fixture(t, map[string]string{"app/page.go": "package app", "components/counter/client_js.go": "package counter", "components/counter/component.json": valid})
	p, err := Discover(root)
	if err != nil || len(p.Components) != 1 || p.Components[0].Refresh == nil || p.Components[0].Refresh.StateSchema != "counter/v1" {
		t.Fatalf("refresh opt-in: %+v %v", p, err)
	}
	for _, invalid := range []string{
		`{"loading":"eager","refresh":{"version":1,"stateSchema":"v1","domSchema":"v1"}}`,
		`{"loading":"lazy","refresh":null}`,
		`{"loading":"lazy","refresh":{}}`,
		`{"loading":"lazy","refresh":{"version":2,"stateSchema":"v1","domSchema":"v1"}}`,
		`{"loading":"lazy","refresh":{"version":1,"stateSchema":"v1"}}`,
		`{"loading":"lazy","refresh":{"version":1,"stateSchema":null,"domSchema":"v1"}}`,
		`{"loading":"lazy","refresh":{"Version":1,"stateSchema":"v1","domSchema":"v1"}}`,
		`{"loading":"lazy","Refresh":{"version":1,"stateSchema":"v1","domSchema":"v1"}}`,
		`{"loading":"lazy","refresh":{"version":1,"stateSchema":"v1","domSchema":"v1","extra":true}}`,
		`{"loading":"lazy","refresh":{"version":1,"stateSchema":"v1","stateSchema":"v2","domSchema":"v1"}}`,
		strings.Replace(valid, "counter/v1", "invalid schema", 1),
		strings.Replace(valid, "counter/v1", strings.Repeat("a", 129), 1),
	} {
		path := filepath.Join(root, "components/counter/component.json")
		if err := os.WriteFile(path, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Discover(root); err == nil {
			t.Fatalf("accepted invalid refresh contract %s", invalid)
		}
	}
}

func TestDevRefreshGenerationKeepsProductionOrdinaryAndRequiresQualifiedShim(t *testing.T) {
	component := Component{Name: "counter", Import: "example.com/site/components/counter", Lazy: true, Refresh: &ComponentRefresh{Version: 1, StateSchema: "state/v1", DOMSchema: "dom/v1"}}
	epoch := strings.Repeat("a", 64)
	production := lazyMainSource(component, epoch, false)
	development := lazyMainSource(component, epoch, true)
	if !strings.Contains(production, "client.RunModule(") || !strings.Contains(production, "select{}") || strings.Contains(production, "RunModuleRefresh") {
		t.Fatal("ordinary production lazy lifecycle changed", production)
	}
	if !strings.Contains(development, `<-client.RunModuleRefresh("counter",`) || !strings.Contains(development, `"state/v1","dom/v1"`) || strings.Contains(development, "select{}") {
		t.Fatal("development module cannot return after retirement", development)
	}
	p := &Project{Components: []Component{component}}
	if plan, err := prepareDevRefresh(context.Background(), p, t.TempDir(), "", "", []byte("unknown shim")); err != nil || plan != nil {
		t.Fatal("unknown SDK shim must retain ordinary reload", plan, err)
	}
	module := lazyOutput{Name: component.Name, File: "module-" + epoch + ".wasm", Digest: epoch, Epoch: epoch, Size: 1, StateSchema: "state/v1", DOMSchema: "dom/v1"}
	source := clientSourceRefresh(p, []lazyOutput{module}, strings.Repeat("b", 64))
	if registered, run := strings.Index(source, "client.RegisterDevRefresh("), strings.Index(source, "client.Run()"); registered < 0 || run <= registered || !strings.Contains(source, `StateSchema:"state/v1",DOMSchema:"dom/v1"`) {
		t.Fatal("core refresh overlay registration is not before hydration", source)
	}
	if ordinary := clientSource(p, []lazyOutput{module}); strings.Contains(ordinary, "RegisterDevRefresh") || strings.Contains(ordinary, "StateSchema") {
		t.Fatal("ordinary core entry advertised development metadata", ordinary)
	}
	server := serverSource(&Project{}, strings.Repeat("a", 20), true, true)
	constructed := strings.Index(server, "nextgo.NewContext(ctx,config)")
	readiness := strings.Index(server, "supported:=true")
	listening := strings.Index(server, "server.ListenAndServe()")
	if constructed < 0 || readiness <= constructed || listening <= readiness || !strings.Contains(server, `app.Paths().AssetPrefix==""`) || !strings.Contains(server, "os.O_CREATE|os.O_EXCL|os.O_WRONLY,0600") {
		t.Fatal("effective native readiness proof missing or premature", server)
	}
	if strings.Contains(server, `log.Fatal("development readiness unavailable")`) {
		t.Fatal("optional capability output must not abort an otherwise valid development server")
	}
	if err := writeGo(filepath.Join(t.TempDir(), "ready.go"), server); err != nil {
		t.Fatal("development readiness generation is not valid Go", err)
	}
	productionServer := serverSource(&Project{}, strings.Repeat("a", 20), false, true)
	if strings.Contains(productionServer, "supported:=true") || !strings.Contains(productionServer, `*devReady!=""&&!false`) {
		t.Fatal("production accepted a development readiness output", productionServer)
	}
}

func TestRefreshInputExclusionsAreExactAndDoNotWeakenNormalFingerprints(t *testing.T) {
	root := t.TempDir()
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"edit.go", "edit.go.extra.go", "edit_test.go", "props.go", "server.go"} {
		write(name, "package sample\n")
	}
	ordinary := inputPlan{Roots: []string{root}}
	filtered := ordinary
	filtered.Excluded = map[string]bool{filepath.Join(root, "edit.go"): true}
	before, err := hashInputs(ordinary, "")
	if err != nil {
		t.Fatal(err)
	}
	omitted, err := hashInputs(filtered, "")
	if err != nil {
		t.Fatal(err)
	}
	write("edit.go", "package sample\n// browser edit\n")
	if after, err := hashInputs(ordinary, ""); err != nil || after == before {
		t.Fatal("ordinary watcher stopped detecting component edits", err)
	}
	if after, err := hashInputs(filtered, ""); err != nil || after != omitted {
		t.Fatal("exact compiler-proven exclusion was ignored", err)
	}
	for _, name := range []string{"edit.go.extra.go", "edit_test.go", "props.go", "server.go"} {
		write(name, "package sample\n// changed\n")
		if after, err := hashInputs(filtered, ""); err != nil || after == omitted {
			t.Fatal("exclusion hid a prefix/test/props/server input", name, err)
		}
		write(name, "package sample\n")
	}
}

func TestRefreshContextNormalizesOnlyCmdGoTemporaryCompilerMapping(t *testing.T) {
	temp := t.TempDir()
	a := "-fPIC -ffile-prefix-map=" + filepath.Join(temp, "go-build123") + "=/tmp/go-build -g"
	b := strings.Replace(a, "go-build123", "go-build987", 1)
	if stableRefreshGCCFlags(a, temp) != stableRefreshGCCFlags(b, temp) {
		t.Fatal("cmd/go temporary mapping churned compatibility")
	}
	if stableRefreshGCCFlags(a+" -fno-common", temp) == stableRefreshGCCFlags(a, temp) || stableRefreshGCCFlags(strings.Replace(a, "=/tmp/go-build", "=/different", 1), temp) == stableRefreshGCCFlags(a, temp) {
		t.Fatal("real compiler flags or mappings were discarded")
	}
	if stableRefreshGCCFlags(strings.Replace(a, "go-build123", "go-build-not-numeric", 1), temp) == stableRefreshGCCFlags(a, temp) || stableRefreshGCCFlags(a, filepath.Join(temp, "sibling")) == stableRefreshGCCFlags(a, temp) {
		t.Fatal("unrecognized compiler path was normalized")
	}
}

// These fixtures query and compile real selected native/core/module dependency
// graphs, including their .a archives. They do not build or serve a live app.
type refreshCompilerFixture struct {
	project *Project
	native  string
	core    string
	module  string
	extra   map[string]string
}

func newRefreshCompilerFixture(t *testing.T) *refreshCompilerFixture {
	t.Helper()
	root := fixture(t, map[string]string{
		"app/page.go":                        "package app\n",
		"components/counter/server.go":       "//go:build !js\n\npackage counter\nfunc Native() int { return Shared }\n",
		"components/counter/client_js.go":    "//go:build js && wasm\n\npackage counter\nimport \"example.com/site/shared\"\nfunc Client() int { return 1+Shared+shared.Value }\n",
		"components/counter/props.go":        "package counter\nconst Shared = 1\n",
		"components/counter/component.json":  `{"loading":"lazy","refresh":{"version":1,"stateSchema":"state/v1","domSchema":"dom/v1"}}`,
		"components/counter/unselected.go":   "//go:build unselected_refresh_test\n\npackage counter\nconst Unselected = 1\n",
		"components/counter/counter_test.go": "package counter\n",
		"shared/value.go":                    "package shared\nconst Value = 1\n",
		"eager/value.go":                     "package eager\nconst Value = 1\n",
		"public/style.css":                   "body { color: black }\n",
		"arbitrary-config.txt":               "native startup input\n",
	})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	return &refreshCompilerFixture{project: p,
		native: "package main\nimport c \"example.com/site/components/counter\"\nfunc main(){_ = c.Native()}\n",
		core:   "package main\nimport e \"example.com/site/eager\"\nfunc main(){_ = e.Value}\n",
		module: "package main\nimport c \"example.com/site/components/counter\"\nfunc main(){_ = c.Client()}\n"}
}

func (fixture *refreshCompilerFixture) plan(t *testing.T) *devRefreshPlan {
	t.Helper()
	plan, err := fixture.proof(t)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func (fixture *refreshCompilerFixture) proof(t *testing.T) (*devRefreshPlan, error) {
	t.Helper()
	ctx := context.Background()
	p := fixture.project
	coreTags, moduleTags, err := browserBuildTags(ctx, p.Root)
	if err != nil {
		t.Fatal(err)
	}
	inputs, contextDigest, err := devRefreshInputs(ctx, p.Root, coreTags, moduleTags)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p.Root, ".nextgo"), 0755); err != nil {
		t.Fatal(err)
	}
	buildDir, err := os.MkdirTemp(filepath.Join(p.Root, ".nextgo"), "build-refresh-test-")
	if err != nil {
		t.Fatal(err)
	}
	var queries []refreshGraphQuery
	specs := []struct {
		name, source, tags string
		browser            bool
	}{{"native", fixture.native, "", false}, {"core", fixture.core, coreTags, true}, {"counter", fixture.module, moduleTags, true}}
	for name, source := range fixture.extra {
		specs = append(specs, struct {
			name, source, tags string
			browser            bool
		}{name, source, moduleTags, true})
	}
	for _, spec := range specs {
		query, err := refreshProbe(ctx, p, buildDir, "refresh-"+spec.name, spec.source, spec.tags, spec.browser, false)
		if err != nil {
			t.Fatal(err)
		}
		query.name = spec.name
		if spec.name != "native" && spec.name != "core" {
			query.name = refreshComponentGraphName(spec.name)
		}
		queries = append(queries, query)
	}
	return collectDevRefreshPlan(ctx, p, inputs, queries, contextDigest, fixture.native, fixture.core)
}

func TestDevRefreshActualCompilerGraphRejectsUnprovenSymbolicTypeShapes(t *testing.T) {
	fixture := newRefreshCompilerFixture(t)
	path := filepath.Join(fixture.project.Root, "components/counter/client_js.go")
	body := "//go:build js && wasm\n\npackage counter\nconst Size = 1\ntype BrowserProps [Size]int\nfunc Client() int { return 1 }\n"
	for _, value := range []string{"1", "2"} {
		if err := os.WriteFile(path, []byte(strings.Replace(body, "Size = 1", "Size = "+value, 1)), 0600); err != nil {
			t.Fatal(err)
		}
		if plan, err := fixture.proof(t); err == nil || plan != nil {
			t.Fatal("symbolic array type was granted AST-only semantic proof", plan, err)
		}
	}
}

func TestDevRefreshActualCompilerGraphsRejectSharedAndNonOptedLazyEdits(t *testing.T) {
	for _, shared := range []bool{true, false} {
		t.Run(strconv.FormatBool(shared), func(t *testing.T) {
			fixture := newRefreshCompilerFixture(t)
			root := fixture.project.Root
			dir := filepath.Join(root, "components/other")
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			body := "//go:build js && wasm\n\npackage other\nfunc Client() int { return 1 }\n"
			config := `{"loading":"lazy"}`
			if shared {
				body = "//go:build js && wasm\n\npackage other\nimport c \"example.com/site/components/counter\"\nfunc Client() int { return c.Client() }\n"
				config = `{"loading":"lazy","refresh":{"version":1,"stateSchema":"state/v1","domSchema":"dom/v1"}}`
			}
			for name, data := range map[string]string{"client_js.go": body, "component.json": config} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			fixture.project, err = Discover(root)
			if err != nil {
				t.Fatal(err)
			}
			fixture.extra = map[string]string{"other": "package main\nimport c \"example.com/site/components/other\"\nfunc main(){_ = c.Client()}\n"}
			before := fixture.plan(t)
			path := filepath.Join(dir, "client_js.go")
			if shared {
				path = filepath.Join(root, "components/counter/client_js.go")
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(original), "return 1", "return 2", 1)), 0600); err != nil {
				t.Fatal(err)
			}
			if fixture.plan(t).compatibility == before.compatibility {
				t.Fatal("shared or non-opted selected lazy source was granted module-only authority")
			}
		})
	}
}

func TestDevRefreshActualCompilerGraphsPermitOnlySelectedLazyLocalEdits(t *testing.T) {
	fixture := newRefreshCompilerFixture(t)
	before := fixture.plan(t)
	path := filepath.Join(fixture.project.Root, "components/counter/client_js.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "return 1+", "return 2+", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	after := fixture.plan(t)
	if before.compatibility != after.compatibility || before.epochs["counter"] == after.epochs["counter"] {
		t.Log("context", before.contextDigest, after.contextDigest, "invariant", before.invariant, after.invariant)
		for key, value := range before.invariantRows {
			if after.invariantRows[key] != value {
				t.Log("changed invariant row", key, value, after.invariantRows[key])
			}
		}
		for key := range after.invariantRows {
			if _, exists := before.invariantRows[key]; !exists {
				t.Log("new invariant row", key)
			}
		}
		t.Fatalf("selected lazy-only edit did not preserve native/core compatibility: before=%s after=%s epochBefore=%s epochAfter=%s", before.compatibility, after.compatibility, before.epochs["counter"], after.epochs["counter"])
	}
	for _, name := range []string{"components/counter/props.go", "components/counter/server.go", "components/counter/unselected.go", "components/counter/counter_test.go", "shared/value.go", "eager/value.go", "public/style.css", "arbitrary-config.txt", "components/counter/component.json", "go.mod"} {
		path := filepath.Join(fixture.project.Root, filepath.FromSlash(name))
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		next := append(append([]byte(nil), original...), '\n')
		if err := os.WriteFile(path, next, 0600); err != nil {
			t.Fatal(err)
		}
		changed := fixture.plan(t)
		if changed.compatibility == after.compatibility {
			t.Fatal("noneligible input change was granted module-only authority", name)
		}
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDevRefreshGraphUsesActualCoreSelectionNotComponentFilename(t *testing.T) {
	fixture := newRefreshCompilerFixture(t)
	fixture.core = fixture.module // A lazy package unexpectedly reaches the core.
	before := fixture.plan(t)
	path := filepath.Join(fixture.project.Root, "components/counter/client_js.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "return 1+", "return 2+", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	after := fixture.plan(t)
	if before.compatibility == after.compatibility {
		t.Fatal("client_js.go naming bypassed actual core dependency ownership")
	}
}

func TestDevRefreshActualCompilerGraphPreservesTypesAcrossBodyEdits(t *testing.T) {
	fixture := newRefreshCompilerFixture(t)
	path := filepath.Join(fixture.project.Root, "components/counter/client_js.go")
	write := func(source string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body := "//go:build js && wasm\n\npackage counter\ntype BrowserProps struct { Value int `json:\"value\"` }\ntype BrowserAlias = BrowserProps\ntype Box[T any] struct { Value T }\nfunc typedHelper(p BrowserProps) int { return int(p.Value)+1 }\nfunc Client() int { return typedHelper(BrowserProps{Value: 1}) }\n"
	write(body)
	before := fixture.plan(t)
	edited := strings.Replace(body, "return int(p.Value)+1", "return int(p.Value)+2", 1)
	write(edited)
	after := fixture.plan(t)
	if before.compatibility != after.compatibility || before.epochs["counter"] == after.epochs["counter"] {
		t.Fatal("typed helper body edit did not retain compatibility and change epoch")
	}
	for _, change := range []struct{ old, next string }{
		{"Value int `json:", "Value int64 `json:"},
		{"json:\"value\"", "json:\"changed\""},
		{"type BrowserAlias = BrowserProps", "type BrowserAlias = Box[int]"},
		{"Box[T any]", "Box[T comparable]"},
	} {
		write(strings.Replace(edited, change.old, change.next, 1))
		if fixture.plan(t).compatibility == after.compatibility {
			t.Fatal("browser-only type contract change was granted refresh", change.old)
		}
	}
	write(edited)
	shape, err := refreshTypeShape(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	write(strings.Replace(edited, "type BrowserProps", "// Documentation only.\ntype BrowserProps", 1))
	documented, err := refreshTypeShape(context.Background(), path)
	if err != nil || shape != documented {
		t.Fatal("documentation changed canonical type invariant", err)
	}
	for _, declaration := range []string{"struct { Value int }", "interface { Value() int }"} {
		write(edited + "func anonymous() { _ = (*" + declaration + ")(nil) }\n")
		first := fixture.plan(t)
		write(edited + "func anonymous() { _ = (*" + strings.Replace(declaration, " int", " int64", 1) + ")(nil) }\n")
		if fixture.plan(t).compatibility == first.compatibility {
			t.Fatal("anonymous type shape change was granted refresh", declaration)
		}
	}
}

func TestDevRefreshGraphUsesEffectiveNativeTagsAndOverlayInputs(t *testing.T) {
	t.Run("native tags", func(t *testing.T) {
		fixture := newRefreshCompilerFixture(t)
		root := fixture.project.Root
		if err := os.WriteFile(filepath.Join(root, "components/counter/client_js.go"), []byte("//go:build js && wasm\n\npackage counter\n"), 0600); err != nil {
			t.Fatal(err)
		}
		browser := filepath.Join(root, "components/counter/browser.go")
		body := "//go:build js || hmr_native\n\npackage counter\nfunc Client() int { return 1 }\n"
		if err := os.WriteFile(browser, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -tags=hmr_native"))
		before := fixture.plan(t)
		if err := os.WriteFile(browser, []byte(strings.Replace(body, "return 1", "return 2", 1)), 0600); err != nil {
			t.Fatal(err)
		}
		after := fixture.plan(t)
		if before.compatibility == after.compatibility {
			t.Fatal("actual native-selected source was treated as browser-only")
		}
	})
	t.Run("overlay", func(t *testing.T) {
		fixture := newRefreshCompilerFixture(t)
		backing := filepath.Join(t.TempDir(), "editor.go")
		body := "//go:build js && wasm\n\npackage counter\nfunc Client() int { return 1 }\n"
		if err := os.WriteFile(backing, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		manifest := filepath.Join(t.TempDir(), "overlay.json")
		data := `{"Replace":{` + strconv.Quote(filepath.Join(fixture.project.Root, "components/counter/client_js.go")) + `:` + strconv.Quote(backing) + `}}`
		if err := os.WriteFile(manifest, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -overlay="+manifest))
		before := fixture.plan(t)
		if err := os.WriteFile(backing, []byte(strings.Replace(body, "return 1", "return 2", 1)), 0600); err != nil {
			t.Fatal(err)
		}
		after := fixture.plan(t)
		if before.compatibility == after.compatibility {
			t.Fatal("overlay backing edits bypassed the invariant")
		}
	})
}

func TestDevRefreshProofRejectsWithinBuildMutationAndBindsCallerFlags(t *testing.T) {
	fixture := newRefreshCompilerFixture(t)
	before := fixture.plan(t)
	coreTags, moduleTags, err := browserBuildTags(context.Background(), fixture.project.Root)
	if err != nil {
		t.Fatal(err)
	}
	if err := before.validate(context.Background(), fixture.project, coreTags, moduleTags); err != nil {
		t.Fatal("unchanged selected proof failed validation", err)
	}
	path := filepath.Join(fixture.project.Root, "components/counter/client_js.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if err := before.validate(context.Background(), fixture.project, coreTags, moduleTags); !errors.Is(err, errDevRefreshProofChanged) {
		t.Fatal("within-build source mutation accepted", err)
	}
	current := fixture.plan(t)
	t.Setenv("GOFLAGS", strings.TrimSpace(os.Getenv("GOFLAGS")+" -trimpath"))
	withFlags := fixture.plan(t)
	if current.compatibility == withFlags.compatibility {
		t.Fatal("changed effective caller flags did not invalidate refresh")
	}
}

func TestDevRefreshBuildMetadataContainsOnlyValidatedPublicDescriptors(t *testing.T) {
	digest := strings.Repeat("a", 64)
	plan := &devRefreshPlan{compatibility: strings.Repeat("b", 64)}
	build, err := plan.build(strings.Repeat("c", 20), []lazyOutput{{Name: "counter", File: "module-" + digest + ".wasm", Digest: digest, Epoch: digest, Size: 1, StateSchema: "state/v1", DOMSchema: "dom/v1"}, {Name: "ordinary"}})
	if err != nil || len(build.Modules) != 1 || build.Modules[0].Validate() != nil || build.ShimDigest != devrefresh.ShimDigest || build.ModuleABI != devrefresh.ModuleABI {
		t.Fatal(build, err)
	}
	if strings.Contains(build.Modules[0].URL, "\\") || !strings.HasPrefix(build.Modules[0].URL, "/_nextgo/") {
		t.Fatal(build.Modules[0])
	}
}
