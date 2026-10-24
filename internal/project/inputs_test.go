package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeInput(t *testing.T, root, name, value string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
func workspaceFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeInput(t, root, "site/go.mod", "module workspace.test/site\ngo 1.24.0\nrequire workspace.test/shared v0.0.0\n")
	writeInput(t, root, "site/app/page.go", "package app")
	writeInput(t, root, "shared/go.mod", "module workspace.test/shared\ngo 1.24.0\n")
	writeInput(t, root, "shared/value.go", "package shared\nconst Value=1\n")
	writeInput(t, root, "go.work", "go 1.24.0\nuse (\n ./site\n ./shared\n)\n")
	return root
}
func TestWorkspaceDiscoverySelectsOwningModule(t *testing.T) {
	root := workspaceFixture(t)
	p, err := Discover(filepath.Join(root, "site"))
	if err != nil {
		t.Fatal(err)
	}
	if p.ModulePath != "workspace.test/site" || p.Import != "workspace.test/site" {
		t.Fatal(p.ModulePath, p.Import)
	}
	writeInput(t, root, "site/nested/app/page.go", "package app")
	p, err = Discover(filepath.Join(root, "site/nested"))
	if err != nil || p.Import != "workspace.test/site/nested" {
		t.Fatal(p, err)
	}
}
func TestWorkspaceFingerprintAndOffPolicy(t *testing.T) {
	root := workspaceFixture(t)
	site := filepath.Join(root, "site")
	before, err := Fingerprint(site)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/value.go", "package shared\nconst Value=2\n")
	after, err := Fingerprint(site)
	if err != nil || after == before {
		t.Fatal("workspace change missed", err)
	}
	t.Setenv("GOWORK", "off")
	off, err := Fingerprint(site)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/value.go", "package shared\nconst Value=3\n")
	unchanged, err := Fingerprint(site)
	if err != nil || unchanged != off {
		t.Fatal("GOWORK=off ignored", err)
	}
}
func TestLocalReplacementsAndAlternateModfile(t *testing.T) {
	root := workspaceFixture(t)
	site := filepath.Join(root, "site")
	t.Setenv("GOWORK", "off")
	writeInput(t, root, "site/alternate.mod", "module workspace.test/site\ngo 1.24.0\nrequire workspace.test/shared v0.0.0\nreplace workspace.test/shared => ../shared\n")
	t.Setenv("GOFLAGS", "-modfile=alternate.mod")
	before, err := Fingerprint(site)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/value.go", "package shared\nconst Value=42\n")
	after, err := Fingerprint(site)
	if err != nil || before == after {
		t.Fatal("local alternate replacement missed", err)
	}
	p, err := Discover(site)
	if err != nil || p.ModulePath != "workspace.test/site" {
		t.Fatal(err)
	}
}
func TestWatcherRecoversBrokenWorkspaceAndReplacement(t *testing.T) {
	root := workspaceFixture(t)
	site := filepath.Join(root, "site")
	watcher := &SourceWatcher{}
	if _, err := watcher.Fingerprint(context.Background(), site); err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "go.work", "go 1.24.0\nuse invalid invalid\n")
	broken, err := watcher.Fingerprint(context.Background(), site)
	if err == nil {
		t.Fatal("invalid workspace accepted")
	}
	writeInput(t, root, "shared/value.go", "package shared\nconst Value=3\n")
	changed, err := watcher.Fingerprint(context.Background(), site)
	if err == nil || changed == broken {
		t.Fatal("lost external inputs during error", err)
	}
	writeInput(t, root, "go.work", "go 1.24.0\nuse (\n ./site\n ./shared\n)\n")
	if _, err := watcher.Fingerprint(context.Background(), site); err != nil {
		t.Fatal(err)
	}
}
func TestMissingReplacementCreationAndIgnoredDependencyReplaces(t *testing.T) {
	root := workspaceFixture(t)
	site := filepath.Join(root, "site")
	t.Setenv("GOWORK", "off")
	writeInput(t, root, "site/go.mod", "module workspace.test/site\ngo 1.24.0\nreplace workspace.test/unused => ../missing\nreplace workspace.test/shared => ../shared\n")
	before, err := Fingerprint(site)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "missing/go.mod", "module workspace.test/unused\ngo 1.24.0\n")
	after, err := Fingerprint(site)
	if err != nil || before == after {
		t.Fatal("new replacement missed", err)
	}
	writeInput(t, root, "shared/go.mod", "module workspace.test/shared\ngo 1.24.0\nreplace ignored.test/lib => ../ignored\n")
	before, err = Fingerprint(site)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "ignored/go.mod", "module ignored.test/lib\ngo 1.24.0\n")
	after, err = Fingerprint(site)
	if err != nil || before != after {
		t.Fatal("dependency replacement incorrectly applied", err)
	}
}
func TestWorkspaceUnselectedAppDiagnostic(t *testing.T) {
	root := workspaceFixture(t)
	writeInput(t, root, "go.work", "go 1.24.0\nuse ./shared\n")
	_, err := Discover(filepath.Join(root, "site"))
	if err == nil || !strings.Contains(err.Error(), "main Go module") {
		t.Fatal(err)
	}
}

func TestEmbeddedInputChangesAndLiteralCommentExclusion(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "shared/value.go": "package shared\nimport _ \"embed\"\n//go:embed assets/*.txt\nvar value string\nconst NotDirective=`//go:embed unrelated/*.txt`\n", "shared/assets/value.txt": "one", "shared/unrelated/value.txt": "one"})
	before, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/assets/value.txt", "two")
	after, err := Fingerprint(root)
	if err != nil || before == after {
		t.Fatal("embedded file change missed", err)
	}
	before = after
	writeInput(t, root, "shared/unrelated/value.txt", "two")
	after, err = Fingerprint(root)
	if err != nil || before != after {
		t.Fatal("directive string treated as source", err)
	}
}
func TestEmbeddedPatternQuotingAndEnvironmentPolicy(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "shared/value.go": "package shared\nimport _ \"embed\"\n//go:embed \"asset with spaces.txt\"\nvar value string\n", "shared/asset with spaces.txt": "one"})
	before, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/asset with spaces.txt", "two")
	after, err := Fingerprint(root)
	if err != nil || before == after {
		t.Fatal("quoted embed missed", err)
	}
	writeInput(t, root, "shared/value.go", "package shared\nimport _ \"embed\"\n//go:embed .env\nvar value string\n")
	writeInput(t, root, "shared/.env", "PRIVATE=never-publish")
	if _, err := Fingerprint(root); err == nil || strings.Contains(err.Error(), "never-publish") {
		t.Fatal("embedded environment accepted or disclosed", err)
	}
}
func TestInputCancellationAndMissingFileIdentity(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := fixture(t, map[string]string{"app/page.go": "package app"})
	if _, err := hashInputsContext(ctx, inputPlan{Roots: []string{root}}, ""); err == nil {
		t.Fatal("ignored cancellation")
	}
	file := filepath.Join(root, "optional.sum")
	before, err := hashInputs(inputPlan{Files: []string{file}}, "")
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "optional.sum", "missing")
	after, err := hashInputs(inputPlan{Files: []string{file}}, "")
	if err != nil || before == after {
		t.Fatal("presence not encoded", err)
	}
}

func TestWorkspaceParentDoesNotOwnUnlistedNestedModule(t *testing.T) {
	root := workspaceFixture(t)
	writeInput(t, root, "go.mod", "module workspace.test/root\ngo 1.24.0\n")
	writeInput(t, root, "go.work", "go 1.24.0\nuse .\n")
	if _, err := Discover(filepath.Join(root, "site")); err == nil {
		t.Fatal("parent workspace module captured an unlisted nested module")
	}
}
func TestSymlinkedManifestKeepsRelativeReplacements(t *testing.T) {
	root := workspaceFixture(t)
	t.Setenv("GOWORK", "off")
	site := filepath.Join(root, "site")
	writeInput(t, root, "configuration/deep/site.mod", "module workspace.test/site\ngo 1.24.0\nrequire workspace.test/shared v0.0.0\nreplace workspace.test/shared => ../shared\n")
	if err := os.Remove(filepath.Join(site, "go.mod")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../configuration/deep/site.mod", filepath.Join(site, "go.mod")); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(site)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/value.go", "package shared\nconst Value=9\n")
	after, err := Fingerprint(site)
	if err != nil || before == after {
		t.Fatal("symlink changed replacement ownership", err)
	}
}
func TestEmbedDirectoryExclusionsAndAllPrefix(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "shared/value.go": "package shared\nimport _ \"embed\"\n//go:embed assets\nvar value embed.FS\n", "shared/assets/plain.txt": "one", "shared/assets/.hidden": "one", "shared/assets/_private": "one", "shared/assets/vendor/value.txt": "one"})
	before, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/assets/.hidden", "two")
	writeInput(t, root, "shared/assets/_private", "two")
	writeInput(t, root, "shared/assets/vendor/value.txt", "two")
	after, err := Fingerprint(root)
	if err != nil || before != after {
		t.Fatal("excluded embedded files affected identity", err)
	}
	writeInput(t, root, "shared/value.go", "package shared\nimport _ \"embed\"\n//go:embed\tall:assets\nvar value embed.FS\n")
	before, err = Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/assets/.hidden", "three")
	after, err = Fingerprint(root)
	if err != nil || before == after {
		t.Fatal("all: hidden asset missed", err)
	}
}
func TestSymlinkedGoSourceChangesAreTracked(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app"})
	target := filepath.Join(t.TempDir(), "value.go")
	if err := os.WriteFile(target, []byte("package app\nconst Value=1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "app/value.go")); err != nil {
		t.Fatal(err)
	}
	before, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package app\nconst Value=2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	after, err := Fingerprint(root)
	if err != nil || before == after {
		t.Fatal("source symlink content change missed", err)
	}
}

func TestCompileWorkspaceWithSiblingServerAndBrowserDependency(t *testing.T) {
	root := workspaceFixture(t)
	frameworkRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "go.work", "go 1.24.0\nuse (\n "+strconv.Quote(frameworkRoot)+"\n ./site\n ./shared\n)\n")
	writeInput(t, root, "site/go.mod", "module workspace.test/site\ngo 1.24.0\nrequire (\n github.com/Suhaibinator/Next.go v0.0.0\n workspace.test/shared v0.0.0\n)\n")
	writeInput(t, root, "shared/data.go", "//go:build !js\npackage shared\nimport _ \"embed\"\n//go:embed note.txt\nvar Note string\n")
	writeInput(t, root, "shared/note.txt", "native-only-workspace-data")
	writeInput(t, root, "site/app/page.go", `//go:build !js
package app
import(nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui";"workspace.test/shared")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Body:ui.Island("counter",nil,ui.Text(shared.Note))},nil}
`)
	writeInput(t, root, "site/components/counter/client_js.go", `//go:build js && wasm
package counter
import("encoding/json";"github.com/Suhaibinator/Next.go/client";"workspace.test/shared")
func Mount(*client.Scope,json.RawMessage)error{_ = shared.Value;return nil}
`)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var output bytes.Buffer
	build, err := Compile(ctx, filepath.Join(root, "site"), false, &output)
	if err != nil {
		t.Fatal(err, output.String())
	}
	defer os.RemoveAll(build.Dir)
	wasm, err := os.ReadFile(filepath.Join(build.Dir, "server/assets/_nextgo", build.ID, "app.wasm"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wasm, []byte("native-only-workspace-data")) {
		t.Fatal("server-only sibling repository entered WASM")
	}
	writeInput(t, root, "shared/note.txt", "changed-native-only-workspace-data")
	fingerprint, err := Fingerprint(filepath.Join(root, "site"))
	if err != nil || fingerprint == build.Fingerprint {
		t.Fatal("embedded dependency did not invalidate build", err)
	}
}

func TestInputFileBudgetAndCompileCancellation(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app"})
	file := filepath.Join(root, "large.go")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(maxInputFile + 1)
	_ = f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hashInputs(inputPlan{Roots: []string{root}}, ""); err == nil {
		t.Fatal("oversized source accepted")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Compile(ctx, root, false, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal("compile input discovery ignored cancellation", err)
	}
}

func TestAlternateModfileReplacementsUseModuleRoot(t *testing.T) {
	root := workspaceFixture(t)
	site := filepath.Join(root, "site")
	t.Setenv("GOWORK", "off")
	writeInput(t, root, "configuration/alternate.mod", "module workspace.test/site\ngo 1.24.0\nrequire workspace.test/shared v0.0.0\nreplace workspace.test/shared => ../shared\n")
	t.Setenv("GOFLAGS", "-modfile=../configuration/alternate.mod")
	before, err := Fingerprint(site)
	if err != nil {
		t.Fatal(err)
	}
	writeInput(t, root, "shared/value.go", "package shared\nconst Value=10\n")
	after, err := Fingerprint(site)
	if err != nil || before == after {
		t.Fatal("alternate module file changed relative replacement base", err)
	}
	metadata, err := goInputJSON(context.Background(), site, "list", "-m", "-json", "workspace.test/shared")
	if err != nil {
		t.Fatal(err)
	}
	var resolved struct{ Replace struct{ Dir string } }
	if err := json.Unmarshal(metadata, &resolved); err != nil {
		t.Fatal(err)
	}
	if canonicalInput(resolved.Replace.Dir) != canonicalInput(filepath.Join(root, "shared")) {
		t.Fatal("fixture disagrees with actual Go replacement resolution")
	}
}

type metadataReadOnly struct{ io.Reader }

func TestMetadataCaptureCannotBypassLimitThroughReadFrom(t *testing.T) {
	output := &boundedInputOutput{}
	n, err := io.Copy(output, metadataReadOnly{strings.NewReader(strings.Repeat("x", 3<<20))})
	if err != nil || n != 3<<20 || output.Len() != 2<<20 || !output.overflow {
		t.Fatal("metadata byte cap bypassed", n, output.Len(), output.overflow, err)
	}
}
