package project

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeCallerOverlay(t *testing.T, name string, mappings map[string]string) {
	t.Helper()
	data, err := json.Marshal(struct{ Replace map[string]string }{mappings})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func callerOverlayFlags(name string) string { return "'-overlay=" + name + "'" }
func TestCallerOverlayWorkingDirectoryEmbedsAndRecoveryWatch(t *testing.T) {
	root := canonicalInput(fixture(t, map[string]string{"shared/value.go": "package shared\n//go:embed missing-original.txt\nvar unused string\n", "shared/assets/value.txt": "physical ignored by overlay"}))
	external := canonicalInput(t.TempDir())
	manifest := filepath.Join(external, "overlay with spaces.json")
	backing := filepath.Join(external, "code.source")
	asset := filepath.Join(external, "value.data")
	source := []byte("package shared\nimport _ \"embed\"\n//go:embed assets/value.txt\nvar value string\n")
	if err := os.WriteFile(backing, source, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(asset, []byte("first overlay value"), 0600); err != nil {
		t.Fatal(err)
	}
	relativeCode, _ := filepath.Rel(root, backing)
	relativeAsset, _ := filepath.Rel(root, asset)
	mapping := map[string]string{"shared/value.go": relativeCode, "shared/assets/value.txt": relativeAsset}
	writeCallerOverlay(t, manifest, mapping)
	t.Setenv("GOFLAGS", callerOverlayFlags(manifest))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	overlay, err := readCallerOverlay(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if overlay.Replace[filepath.Join(root, "shared", "value.go")] != backing {
		t.Fatal("paths resolved relative to JSON instead of command cwd", overlay.Replace)
	}
	var watcher SourceWatcher
	before, err := watcher.Fingerprint(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shared", "assets", "value.txt"), []byte("unused physical edit"), 0644); err != nil {
		t.Fatal(err)
	}
	unchanged, err := watcher.Fingerprint(ctx, root)
	if err != nil || unchanged != before {
		t.Fatal("effective overlay embed still hashes replaced physical asset", err)
	}
	if err := os.WriteFile(asset, []byte("changed overlay value"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := watcher.Fingerprint(ctx, root)
	if err != nil || changed == before {
		t.Fatal("external backing edit was not watched", err)
	}
	if err := os.WriteFile(manifest, []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	malformed, err := watcher.Fingerprint(ctx, root)
	if err == nil || malformed == changed {
		t.Fatal("malformed caller overlay did not change watcher state", err)
	}
	if err := os.WriteFile(asset, []byte("edit during metadata failure"), 0600); err != nil {
		t.Fatal(err)
	}
	retained, err := watcher.Fingerprint(ctx, root)
	if err == nil || retained == malformed {
		t.Fatal("watcher lost prior external backing during malformed metadata", err)
	}
	writeCallerOverlay(t, manifest, mapping)
	restored, err := watcher.Fingerprint(ctx, root)
	if err != nil || restored == retained {
		t.Fatal("valid caller overlay did not recover", err)
	}
	if err := os.Remove(asset); err != nil {
		t.Fatal(err)
	}
	missing, err := watcher.Fingerprint(ctx, root)
	if err == nil || missing == restored {
		t.Fatal("backing deletion not observed", err)
	}
	if err := os.WriteFile(asset, []byte("recreated"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := watcher.Fingerprint(ctx, root); err != nil {
		t.Fatal("backing recreation did not recover", err)
	}
	mapping["shared/assets/value.txt"] = ""
	writeCallerOverlay(t, manifest, mapping)
	deleted, err := watcher.Fingerprint(ctx, root)
	if err != nil || deleted == missing {
		t.Fatal("virtual deletion was not represented", err)
	}
}

func TestCallerOverlayGoDirectiveIntroducesPhysicalEmbedInputs(t *testing.T) {
	root := canonicalInput(fixture(t, map[string]string{"shared/value.go": "package shared\n", "shared/data/extra.txt": "one"}))
	external := canonicalInput(t.TempDir())
	backing := filepath.Join(external, "source.payload")
	manifest := filepath.Join(external, "overlay.json")
	if err := os.WriteFile(backing, []byte("package shared\nimport _ \"embed\"\n//go:embed data/*.txt\nvar data string\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeCallerOverlay(t, manifest, map[string]string{filepath.Join(root, "shared", "value.go"): backing})
	t.Setenv("GOFLAGS", callerOverlayFlags(manifest))
	before, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "shared", "data", "extra.txt"), []byte("two"), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := Fingerprint(root)
	if err != nil || before == after {
		t.Fatal("overlay-added directive did not track physical embed files", err)
	}
}

func TestCallerOverlayReservedCollisionsAndBounds(t *testing.T) {
	root := canonicalInput(fixture(t, map[string]string{}))
	external := canonicalInput(t.TempDir())
	manifest := filepath.Join(external, "overlay.json")
	backing := filepath.Join(external, "source.go")
	if err := os.WriteFile(backing, []byte("package shared"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", callerOverlayFlags(manifest))
	ctx := context.Background()
	collisions := []string{filepath.Join(root, ".nextgo", "generated", "client", "main.go"), filepath.Join(root, ".nextgo", "build-owned", "server", "main.go"), filepath.Join(root, ".nextgo")}
	alias := filepath.Join(external, "root-alias")
	if err := os.Symlink(root, alias); err == nil {
		collisions = append(collisions, filepath.Join(alias, ".nextgo", "generated", "client", "main.go"))
	}
	for _, from := range collisions {
		writeCallerOverlay(t, manifest, map[string]string{from: backing})
		if _, err := readCallerOverlay(ctx, root); err == nil || !strings.Contains(err.Error(), "framework generated") {
			t.Fatal("caller replaced framework-owned entrypoint", from, err)
		}
	}
	writeCallerOverlay(t, manifest, map[string]string{"shared/value.go": external + string(filepath.Separator)})
	if _, err := readCallerOverlay(ctx, root); err == nil || !strings.Contains(err.Error(), "directory backing") {
		t.Fatal("unsupported SDK directory overlay was not diagnosed", err)
	}

	many := map[string]string{}
	for i := 0; i <= maxCallerOverlayMappings; i++ {
		many[fmt.Sprintf("shared/file%d.go", i)] = backing
	}
	writeCallerOverlay(t, manifest, many)
	if _, err := readCallerOverlay(ctx, root); err == nil || !strings.Contains(err.Error(), "mappings") {
		t.Fatal("unbounded caller mapping count", err)
	}
	if err := os.WriteFile(manifest, bytes.Repeat([]byte(" "), maxCallerOverlayBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readCallerOverlay(ctx, root); err == nil || !strings.Contains(err.Error(), "2 MiB") {
		t.Fatal("unbounded caller overlay JSON", err)
	}
}

func TestCallerOverlayEnvironmentEmbedGuardUsesEffectiveFiles(t *testing.T) {
	root := canonicalInput(fixture(t, map[string]string{"shared/value.go": "package shared\n", "shared/.env": "secret"}))
	external := canonicalInput(t.TempDir())
	backing := filepath.Join(external, "source.payload")
	manifest := filepath.Join(external, "overlay.json")
	if err := os.WriteFile(backing, []byte("package shared\nimport _ \"embed\"\n//go:embed .env\nvar value string\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeCallerOverlay(t, manifest, map[string]string{filepath.Join(root, "shared", "value.go"): backing})
	t.Setenv("GOFLAGS", callerOverlayFlags(manifest))
	if _, err := Fingerprint(root); err == nil || !strings.Contains(err.Error(), "environment files") {
		t.Fatal("overlay source bypassed environment embed policy", err)
	}
}

func TestCompileMergesCallerOverlayAndTracksExternalBacking(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles native/WASM merged-overlay fixture")
	}
	root := canonicalInput(reproducibleProjectFixture(t))
	original := filepath.Join(root, "app", "page.go")
	if err := os.WriteFile(original, []byte("//go:build !js\npackage app\nimport(nextgo \"github.com/Suhaibinator/Next.go\";\"github.com/Suhaibinator/Next.go/ui\")\nfunc Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Text(\"caller overlay was ignored\")},nil}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	external := canonicalInput(t.TempDir())
	source := filepath.Join(external, "native.source")
	message := filepath.Join(external, "message.txt")
	manifest := filepath.Join(external, "caller overlay.json")
	native := []byte("//go:build !js\npackage app\nimport(_ \"embed\";nextgo \"github.com/Suhaibinator/Next.go\";\"github.com/Suhaibinator/Next.go/ui\")\n//go:embed message.txt\nvar message string\nfunc Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.El(\"h1\",nil,ui.Text(message))},nil}\n")
	if err := os.WriteFile(source, native, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(message, []byte("Stable SSR v1"), 0600); err != nil {
		t.Fatal(err)
	}
	eager := filepath.Join(external, "eager.source")
	lazy := filepath.Join(external, "lazy.source")
	browser := func(pkg, marker string) []byte {
		return []byte(fmt.Sprintf("//go:build js && wasm\npackage %s\nimport(\"encoding/json\";\"github.com/Suhaibinator/Next.go/client\")\nfunc Mount(*client.Scope,json.RawMessage)error{return client.Prefetch(%q)}\n", pkg, marker))
	}
	if err := os.WriteFile(eager, browser("eager", "/caller-overlay-eager-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lazy, browser("lazyone", "/caller-overlay-lazy-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	writeCallerOverlay(t, manifest, map[string]string{original: source, filepath.Join(root, "app", "message.txt"): message, filepath.Join(root, "components", "eager", "client_js.go"): eager, filepath.Join(root, "components", "lazyone", "client_js.go"): lazy})
	t.Setenv("GOFLAGS", callerOverlayFlags(manifest))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	compile := func() *Build {
		var log bytes.Buffer
		b, err := Compile(ctx, root, false, &log)
		if err != nil {
			t.Fatalf("%v\n%s", err, &log)
		}
		t.Cleanup(func() { _ = os.RemoveAll(b.Dir) })
		return b
	}
	first, second := compile(), compile()
	assertReproducibleBuild(t, first, second)
	assertReproducibleServerAssets(t, ctx, first)
	eagerFound, lazyFound := false, false
	for name, data := range reproducibleBuildAssets(t, first) {
		if !strings.HasSuffix(name, ".wasm") {
			continue
		}
		if bytes.Contains(data, []byte("/caller-overlay-eager-marker")) {
			eagerFound = true
		}
		if bytes.Contains(data, []byte("/caller-overlay-lazy-marker")) {
			lazyFound = true
			if strings.HasSuffix(name, "/app.wasm") {
				t.Fatal("lazy overlaid implementation leaked into core")
			}
		}
	}
	if !eagerFound || !lazyFound {
		t.Fatal("caller overlay was not merged into core and lazy compiler views", eagerFound, lazyFound)
	}
	if err := os.WriteFile(message, []byte("Stable SSR v2"), 0600); err != nil {
		t.Fatal(err)
	}
	changed := compile()
	if changed.ID == first.ID || changed.Fingerprint == first.Fingerprint {
		t.Fatal("external backing edit did not alter runtime identity")
	}
}

func TestGeneratedVirtualNamespaceCollisionAndWatchRecovery(t *testing.T) {
	root := canonicalInput(fixture(t, map[string]string{}))
	t.Setenv("GOFLAGS", "")
	ctx := context.Background()
	var watcher SourceWatcher
	baseline, err := watcher.Fingerprint(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	physical := filepath.Join(root, ".nextgo", "build-private", "client")
	if err := os.MkdirAll(physical, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(physical, "main.go"), []byte("package main\nfunc main(){}"), 0600); err != nil {
		t.Fatal(err)
	}
	virtualRoot := filepath.Join(root, ".nextgo", "generated")
	if err := os.MkdirAll(filepath.Join(virtualRoot, "client"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(virtualRoot, "client", "extra.go"), []byte("package main"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := generatedEntryOverlay(ctx, root, physical, "client"); err == nil {
		t.Fatal("untracked physical file could join virtual generated package")
	}
	collision, err := watcher.Fingerprint(ctx, root)
	if err == nil || collision == baseline {
		t.Fatal("reserved namespace collision did not change watch state", err)
	}
	if err := os.RemoveAll(virtualRoot); err != nil {
		t.Fatal(err)
	}
	recovered, err := watcher.Fingerprint(ctx, root)
	if err != nil || recovered != baseline {
		t.Fatal("removing reserved collision did not restore watch/build state", err)
	}
}
