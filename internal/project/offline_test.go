package project

import (
	"context"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/buildassets"
	"github.com/Suhaibinator/Next.go/offline"
)

const offlineProvider = "package app\nimport policy \"github.com/Suhaibinator/Next.go/offline\"\nfunc Offline() policy.Config{return policy.Config{}}\n"

func TestOfflineConventionRequiresNativeRootProvider(t *testing.T) {
	valid := fixture(t, map[string]string{"app/page.go": "package app", "app/offline.go": offlineProvider})
	got, err := discoverOffline(&Project{Root: valid, Import: "example.com/site"})
	if err != nil || got != "example.com/site/app" {
		t.Fatalf("provider=%q err=%v", got, err)
	}
	for name, source := range map[string]string{
		"missing":      "package app",
		"pointer":      "package app;import o \"github.com/Suhaibinator/Next.go/offline\";func Offline()*o.Config{return nil}",
		"parameters":   "package app;import o \"github.com/Suhaibinator/Next.go/offline\";func Offline(int)o.Config{return o.Config{}}",
		"two-results":  "package app;import o \"github.com/Suhaibinator/Next.go/offline\";func Offline()(o.Config,error){return o.Config{},nil}",
		"wrong-import": "package app;import o \"example.com/offline\";func Offline()o.Config{return o.Config{}}",
		"method":       "package app;import o \"github.com/Suhaibinator/Next.go/offline\";type T int;func(T)Offline()o.Config{return o.Config{}}",
	} {
		t.Run(name, func(t *testing.T) {
			root := fixture(t, map[string]string{"app/offline.go": source})
			if _, err := discoverOffline(&Project{Root: root, Import: "example.com/site"}); err == nil {
				t.Fatal("invalid provider accepted")
			}
		})
	}
	nested := fixture(t, map[string]string{"app/page.go": "package app", "app/nested/offline.go": offlineProvider})
	if selected, err := discoverOffline(&Project{Root: nested, Import: "example.com/site"}); err != nil || selected != "" {
		t.Fatal("nested helper incorrectly opted the application into offline", selected, err)
	}
	root := fixture(t, map[string]string{"app/page.go": "package app", "provider.go": offlineProvider})
	if err := os.Symlink(filepath.Join(root, "provider.go"), filepath.Join(root, "app", "offline.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverOffline(&Project{Root: root, Import: "example.com/site"}); err == nil {
		t.Fatal("symlink convention accepted")
	}
}

func TestOfflinePurposeErrorsBeforeApplicationConfiguration(t *testing.T) {
	p := &Project{Offline: "example.com/site/app"}
	for _, c := range []struct{ dev, prerender, release bool }{{true, true, false}, {false, false, false}, {false, true, true}} {
		if err := validateOfflinePurpose(p, c.dev, c.prerender, c.release); err == nil {
			t.Fatal("unsupported purpose accepted")
		}
	}
	if err := validateOfflinePurpose(p, false, true, false); err != nil {
		t.Fatal(err)
	}
	p.Routes = []Route{{Pattern: "/", Prerender: true}}
	if err := validateOfflinePurpose(p, false, true, false); err == nil {
		t.Fatal("offline prerender accepted")
	}
	p.Routes = nil
	p.MetadataFiles = []MetadataFile{{Name: "manifest.webmanifest"}}
	if err := validateOfflinePurpose(p, false, true, false); err == nil {
		t.Fatal("unpinned managed metadata accepted")
	}
	if err := validateOfflinePurpose(&Project{}, true, false, true); err != nil {
		t.Fatal("ordinary build behavior changed", err)
	}
}

func offlinePublicFixture(t *testing.T, files map[string]string) (*offlineBuildPlan, string, string) {
	t.Helper()
	root := fixture(t, files)
	stage := t.TempDir()
	plan := &offlineBuildPlan{publicStage: filepath.Join(stage, "snapshot"), bridge: "const d=" + offline.BuildDescriptorPlaceholder + ";"}
	return plan, root, filepath.Join(stage, "public")
}

func TestOfflinePublicSnapshotsOwnImmutableBytesAndCompleteKinds(t *testing.T) {
	plan, root, public := offlinePublicFixture(t, map[string]string{"app/page.go": "package app", "public/style.css": "body{color:rgb(22,59,40)}", "public/logo.png": "png", "public/font.woff2": "font"})
	if err := plan.snapshotPublic(context.Background(), root, nil, public); err != nil {
		t.Fatal(err)
	}
	if len(plan.files) != 3 {
		t.Fatal(plan.files)
	}
	if err := os.WriteFile(filepath.Join(root, "public", "style.css"), []byte("body{color:red}"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{public, filepath.Join(plan.publicStage, "assets")} {
		data, err := os.ReadFile(filepath.Join(base, "style.css"))
		if err != nil || string(data) != "body{color:rgb(22,59,40)}" {
			t.Fatalf("snapshot mutated: %s %v", data, err)
		}
	}
	worker := []byte("\x00asm\x01\x00\x00\x00")
	plan.workerData = worker
	plan.files = append(plan.files, offlineRecord(offline.WorkerWASM, "worker-"+offlineSHA(worker)+".wasm", worker, "application/wasm"))
	id := strings.Repeat("a", 20)
	identity := plan.identity()
	core, shim := []byte("core"), []byte("paired shim")
	boot := "boot"
	lazy := []byte("module")
	module := lazyOutput{File: "module-" + offlineSHA(lazy) + ".wasm", Data: lazy}
	if err := plan.finalize(id, core, shim, boot, []lazyOutput{module}); err != nil {
		t.Fatal(err)
	}
	if plan.identity() != identity {
		t.Fatal("final runtime prefix fed back into pre-runtime identity")
	}
	if err := plan.finalize(strings.Repeat("b", 20), core, shim, boot, []lazyOutput{module}); err != nil || plan.identity() != identity {
		t.Fatal("runtime identity cycle", err)
	}
	if err := plan.finalize(id, core, shim, boot, []lazyOutput{module}); err != nil {
		t.Fatal(err)
	}
	assets := t.TempDir()
	runtime := filepath.Join(assets, "_nextgo", id)
	if err := os.MkdirAll(runtime, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"app.wasm": core, "wasm_exec.js": shim, "boot.js": []byte(boot), module.File: lazy} {
		if err := os.WriteFile(filepath.Join(runtime, name), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := plan.publish(runtime); err != nil {
		t.Fatal(err)
	}
	for _, entry := range plan.build.Files {
		body, err := fs.ReadFile(os.DirFS(assets), entry.Path)
		if err != nil || offlineSHA(body) != entry.SHA256 || int64(len(body)) != entry.DecodedBytes {
			t.Fatalf("missing or mismatched dependency %+v: %v", entry, err)
		}
	}
}

func TestOfflinePublicRejectsUnsupportedUnboundedAndCollidingInputs(t *testing.T) {
	for _, name := range []string{"secret.txt", "runtime.js", "picture.svg", ".env", ".env.production", "bad%20name.png", "UPPER.CSS"} {
		t.Run(name, func(t *testing.T) {
			plan, root, public := offlinePublicFixture(t, map[string]string{"public/style.css": "body{}", "public/" + name: "bytes"})
			if err := plan.snapshotPublic(context.Background(), root, nil, public); err == nil {
				t.Fatal("unsupported source accepted")
			}
		})
	}
	plan, root, public := offlinePublicFixture(t, map[string]string{"public/style.css": "body{}"})
	generated := t.TempDir()
	if err := os.WriteFile(filepath.Join(generated, "STYLE.css"), []byte("body{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := plan.snapshotPublic(context.Background(), root, &buildassets.Result{Dir: generated}, public); err == nil {
		t.Fatal("case collision accepted")
	}
	plan, root, public = offlinePublicFixture(t, map[string]string{"public/style.css": "body{}", "public/zero.png": ""})
	if err := plan.snapshotPublic(context.Background(), root, nil, public); err == nil {
		t.Fatal("empty dependency accepted")
	}
	plan, root, public = offlinePublicFixture(t, map[string]string{"public/style.css": "body{}"})
	if err := os.Symlink(filepath.Join(root, "public", "style.css"), filepath.Join(root, "public", "linked.css")); err != nil {
		t.Fatal(err)
	}
	if err := plan.snapshotPublic(context.Background(), root, nil, public); err == nil {
		t.Fatal("symlink dependency accepted")
	}
	plan, root, public = offlinePublicFixture(t, map[string]string{"public/style.css": "body{}", "public/huge.png": "x"})
	if err := os.Truncate(filepath.Join(root, "public", "huge.png"), maxOfflineFileBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := plan.snapshotPublic(context.Background(), root, nil, public); err == nil {
		t.Fatal("oversized dependency accepted")
	}
	plan, root, public = offlinePublicFixture(t, map[string]string{"public/style.css": "body{}", "public/a.png": "a"})
	plan.publicLimit = 1
	if err := plan.snapshotPublic(context.Background(), root, nil, public); err == nil {
		t.Fatal("inventory admission bound ignored")
	}
}

func TestOfflineCSSRefusesResourceDiscoveryAndEscapedGrammar(t *testing.T) {
	for _, css := range []string{"@import 'remote.css';", "@/**/import 'remote.css';", "body{background:url(/assets/icon.png)}", "body{background:u/**/rl(https://remote.invalid/a)}", "body{background:image-set('remote.png' 1x)}", "body{background:paint(custom)}", "body{content:'\\75rl'}", "body{color:future-function(1)}", "/* unterminated", "body{content:'unterminated}"} {
		if err := validateOfflineCSS([]byte(css)); err == nil {
			t.Fatalf("CSS dependency grammar accepted: %s", css)
		}
	}
	for _, css := range []string{"body{color:rgb(22,59,40);margin:calc(1px + 2px)}", "/* url(remote) */body{content:'url(remote)';color:var(--public-color)}", ":is(h1,h2){transform:translateX(1px);background:linear-gradient(red,blue)}", "@media (prefers-color-scheme: dark){body{color:rgb(1,2,3)}}"} {
		if err := validateOfflineCSS([]byte(css)); err != nil {
			t.Fatalf("pure style rejected: %s: %v", css, err)
		}
	}
}

func TestOfflineRuntimeIdentityBindsWorkerPublicShimAndTemplate(t *testing.T) {
	bridge, err := inlineOfflineShim("shim="+offlineShimPlaceholder+";descriptor="+offline.BuildDescriptorPlaceholder, []byte("paired-v1"))
	if err != nil {
		t.Fatal(err)
	}
	base := &offlineBuildPlan{bridge: bridge, files: []offline.BuildFile{offlineRecord(offline.Style, "offline-public/assets/style.css", []byte("body{}"), "text/css; charset=utf-8"), offlineRecord(offline.WorkerWASM, "worker-"+offlineSHA([]byte("worker"))+".wasm", []byte("worker"), "application/wasm")}}
	id := runtimeBuildID([]byte("core"), "source"+base.identity(), []byte("paired-v1"), "boot")
	for _, change := range []func(*offlineBuildPlan){func(p *offlineBuildPlan) { p.files[0].SHA256 = offlineSHA([]byte("body{color:red}")) }, func(p *offlineBuildPlan) { p.files[1].SHA256 = offlineSHA([]byte("new worker")) }, func(p *offlineBuildPlan) { p.bridge = strings.Replace(p.bridge, "paired-v1", "paired-v2", 1) }, func(p *offlineBuildPlan) { p.bridge += ";newBridge=true" }} {
		copy := *base
		copy.files = append([]offline.BuildFile(nil), base.files...)
		change(&copy)
		if runtimeBuildID([]byte("core"), "source"+copy.identity(), []byte("paired-v1"), "boot") == id {
			t.Fatal("changed offline dependency did not change runtime identity")
		}
	}
	for _, template := range []string{"missing", offlineShimPlaceholder + offlineShimPlaceholder + offline.BuildDescriptorPlaceholder, offlineShimPlaceholder + offline.BuildDescriptorPlaceholder + offline.BuildDescriptorPlaceholder} {
		if _, err := inlineOfflineShim(template, []byte("paired")); err == nil {
			t.Fatal("ambiguous template accepted")
		}
	}
	var absent *offlineBuildPlan
	if absent.identity() != "" {
		t.Fatal("ordinary opt-out identity changed")
	}
}

func TestOfflineGeneratedStartupAndPurposeOrdering(t *testing.T) {
	p := &Project{Import: "example.com/site", Offline: "example.com/site/app", Configure: "example.com/site/app", Routes: []Route{{Pattern: "/", Import: "example.com/site/app"}}, offlineBuild: &offlineBuildPlan{build: offline.Build{Version: 1}}}
	source := serverSource(p, strings.Repeat("a", 20), false)
	if _, err := format.Source([]byte(source)); err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{".Configure(&config)", ".Offline()", "nextgo.NewContext(ctx,config)", "app.Close(drain)", "app.PrepareOfflineBundle(ctx)", "serverless.Run(ctx,app)", "server.ListenAndServe()"} {
		if strings.Count(source, needle) != 1 {
			t.Fatalf("missing/duplicate lifecycle %q", needle)
		}
	}
	order := []string{"if exporting||*prerenderOut", ".Configure(&config)", ".Offline()", "nextgo.NewContext(ctx,config)", "app.Close(drain)", "app.PrepareOfflineBundle(ctx)", "serverless.Run(ctx,app)", "server.ListenAndServe()"}
	last := -1
	for _, needle := range order {
		index := strings.Index(source, needle)
		if index <= last {
			t.Fatalf("startup lifecycle order wrong at %s", needle)
		}
		last = index
	}
	ordinary := serverSource(&Project{}, "ordinary", false)
	if strings.Contains(ordinary, "offline.Build") || strings.Contains(ordinary, "PrepareOfflineBundle") || strings.Contains(ordinary, ".Offline()") {
		t.Fatal("ordinary server unexpectedly opted in")
	}
	worker := offlineWorkerSource()
	if !strings.Contains(worker, "func main(){worker.Run()}") || strings.Contains(worker, "/client") || strings.Contains(worker, "/app") || strings.Contains(worker, "select{") {
		t.Fatal("worker entry imports document/application runtime")
	}
}
