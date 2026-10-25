package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func zoneReleaseTopologyFixture(t *testing.T) string {
	t.Helper()
	return fixture(t, map[string]string{
		"selected/app/page.go": "package app\n",
		"selected/go.mod":      "module selected.example.test\ngo 1.24\n",
		// Neither an unavailable peer SDK nor invalid peer Go enters this build.
		"peer/go.mod":      "module peer.example.test\ngo 99.99\n",
		"peer/app/page.go": "this peer cannot compile",
		"zones.json":       `{"version":1,"zones":[{"id":"marketing","host":"example.test","basePath":"","tenant":"private-tenant","app":"./selected"},{"id":"blog","host":"example.test","basePath":"/blog","app":"./peer"},{"id":"details","host":"example.test","basePath":"/blog/details","app":"./missing-peer"}]}`,
	})
}

func TestZoneReleasePlanDoesNotRequirePeerModulesAndBindsOnlyPublicTopology(t *testing.T) {
	root := zoneReleaseTopologyFixture(t)
	manifest := filepath.Join(root, "zones.json")
	plan, err := prepareZoneRelease(manifest, "marketing", "https://example.test", "A1")
	if err != nil {
		t.Fatal(err)
	}
	if plan.selected.Root != canonicalInput(filepath.Join(root, "selected")) || plan.spec.AssetPrefix != "/_nextgo/releases/A1" || !reflect.DeepEqual(plan.spec.DocumentPaths, []string{"/blog", "/blog/details"}) {
		t.Fatalf("incorrect selected release: %+v", plan)
	}
	before := plan.spec.TopologySHA256
	if err := os.WriteFile(filepath.Join(root, "peer/app/page.go"), []byte("another invalid peer program"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := plan.validateTopology(); err != nil {
		t.Fatal("peer source should not enter release topology", err)
	}
	data, _ := os.ReadFile(manifest)
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(data), "private-tenant", "different-private-tenant", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := prepareZoneRelease(manifest, "marketing", "https://example.test", "A1")
	if err != nil || before == after.spec.TopologySHA256 || plan.validateTopology() == nil {
		t.Fatal("private topology tenant did not change opaque identity", err)
	}
}

func TestZoneReleasePlanValidatesOperatorIdentityAndAllocatesRandomIDOnce(t *testing.T) {
	root := zoneReleaseTopologyFixture(t)
	manifest := filepath.Join(root, "zones.json")
	first, err := prepareZoneRelease(manifest, "marketing", "https://example.test", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := prepareZoneRelease(manifest, "marketing", "https://example.test", "")
	if err != nil || len(first.spec.ReleaseID) != 32 || first.spec.ReleaseID == second.spec.ReleaseID || first.validateTopology() != nil {
		t.Fatal("release identity is not independently allocated and retained", err)
	}
	for _, args := range [][3]string{{"unknown", "https://example.test", "A1"}, {"marketing", "https://other.test", "A1"}, {"marketing", "https://example.test/", "A1"}, {"marketing", "https://example.test", "../A1"}} {
		if _, err := prepareZoneRelease(manifest, args[0], args[1], args[2]); err == nil {
			t.Fatal("invalid operator identity accepted", args)
		}
	}
}

func TestZoneReleaseAssetsAndBinaryContractAreComputedFromActualStaging(t *testing.T) {
	root := zoneReleaseTopologyFixture(t)
	plan, err := prepareZoneRelease(filepath.Join(root, "zones.json"), "marketing", "https://example.test", "A1")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	assets := filepath.Join(dir, "server/assets")
	id := strings.Repeat("a", 20)
	runtime := filepath.Join(assets, "_nextgo", id)
	if err := os.MkdirAll(runtime, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(assets, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app.wasm", "wasm_exec.js", "boot.js"} {
		if err := os.WriteFile(filepath.Join(runtime, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := plan.bindAssets(context.Background(), assets, id); err != nil {
		t.Fatal(err)
	}
	before := plan.spec.AssetsSHA256
	if err := os.WriteFile(filepath.Join(assets, "public/empty.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := plan.bindAssets(context.Background(), assets, id); err != nil || before == plan.spec.AssetsSHA256 {
		t.Fatal("actual public tree not bound", err)
	}
	binary := filepath.Join(dir, "server-bin")
	data := []byte("selected native artifact")
	if err := os.WriteFile(binary, data, 0755); err != nil {
		t.Fatal(err)
	}
	build, err := finishZoneRelease(context.Background(), &Build{Dir: dir, Binary: binary, ID: id}, plan)
	sum := sha256.Sum256(data)
	if err != nil || build.Manifest.BinarySHA256 != hex.EncodeToString(sum[:]) || build.Manifest.Release.RuntimeID != id || build.Manifest.Release.ReleaseID != "A1" {
		t.Fatal("native digest or independent release ID mismatch", build, err)
	}
}

func TestZoneReleaseServerGenerationInstallsContextBeforeOneHook(t *testing.T) {
	root := zoneReleaseTopologyFixture(t)
	plan, err := prepareZoneRelease(filepath.Join(root, "zones.json"), "marketing", "https://example.test", "A1")
	if err != nil {
		t.Fatal(err)
	}
	source := zoneReleaseServerSource(&Project{Configure: "selected.example.test/app"}, plan)
	if strings.Count(source, ".Configure(config)") != 1 || !strings.Contains(source, "nextgo.NewZoneRelease(ctx,") || strings.Contains(source, "nextgo.NewContext(") || strings.Contains(source, "peer.example.test") || strings.Contains(source, "RunModuleRefresh") {
		t.Fatal("release constructor/hook/import isolation missing", source)
	}
	if !strings.Contains(source, `Tenant:"private-tenant"`) || !strings.Contains(source, "explicitBackendHosts") || !strings.Contains(source, "NEXTGO_ZONE_BACKEND_HOSTS") {
		t.Fatal("native private context or explicit backend authority missing", source)
	}
	if err := writeGo(filepath.Join(t.TempDir(), "main.go"), source); err != nil {
		t.Fatal("release entry is not valid Go", err)
	}
	if err := plan.validateProject(&Project{MetadataFiles: []MetadataFile{{}}}); err == nil {
		t.Fatal("unsupported metadata accepted")
	}
	if err := plan.validateProject(&Project{Routes: []Route{{Prerender: true}}}); err == nil {
		t.Fatal("unsupported prerender accepted")
	}
	var output bytes.Buffer
	if _, err := CompileZoneRelease(context.Background(), filepath.Join(root, "zones.json"), "missing", "https://example.test", "A1", &output); err == nil || output.Len() != 0 {
		t.Fatal("invalid selection reached compiler", err, output.String())
	}
}

// Exercise real SDK-paired core/lazy/native compilation in an isolated module.
// The deliberately broken peer must remain irrelevant, including its SDK.
func TestZoneReleaseActualSelectedCompilerGraphAndIndependentReleaseIdentity(t *testing.T) {
	root := zoneReleaseTopologyFixture(t)
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := "module selected.example.test\ngo 1.24\nrequire " + framework + " v0.0.0\nreplace " + framework + " => " + strconv.Quote(repo) + "\n"
	if err := os.WriteFile(filepath.Join(root, "selected/go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	page := `//go:build !js
package app
import (nextgo "github.com/Suhaibinator/Next.go";"github.com/Suhaibinator/Next.go/ui")
func Page(*nextgo.Context)(nextgo.Page,error){return nextgo.Page{Title:"Selected",Body:ui.Text("Selected only")},nil}
`
	config := `//go:build !js
package app
import ("os";nextgo "github.com/Suhaibinator/Next.go")
func Configure(c *nextgo.Config){
 if c.Zone().ID!="marketing"||c.Zone().Tenant!="private-tenant"||c.Phase!=nextgo.PhaseProduction||c.Dev||c.BasePath!=""||(c.AssetPrefix!="/_nextgo/releases/A1"&&c.AssetPrefix!="/_nextgo/releases/A2"){panic("release context missing before hook")}
 marker:=os.Getenv("NEXTGO_RELEASE_TEST_MARKER");if marker!=""{f,err:=os.OpenFile(marker,os.O_CREATE|os.O_APPEND|os.O_WRONLY,0600);if err!=nil{panic(err)};_,err=f.WriteString("hook\n");if err!=nil{panic(err)};if err=f.Close();err!=nil{panic(err)}}
}
`
	if err := os.WriteFile(filepath.Join(root, "selected/app/page.go"), []byte(page), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "selected/app/config.go"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join(root, "selected/components/counter")
	if err := os.MkdirAll(component, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"client_js.go": "//go:build js && wasm\npackage counter\nimport(\"encoding/json\";\"github.com/Suhaibinator/Next.go/client\")\nfunc Mount(*client.Scope,json.RawMessage)error{return nil}\n", "component.json": `{"loading":"lazy"}`} {
		if err := os.WriteFile(filepath.Join(component, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var output bytes.Buffer
	first, err := CompileZoneRelease(ctx, filepath.Join(root, "zones.json"), "marketing", "https://example.test", "A1", &output)
	if err != nil {
		t.Fatalf("selected release compile: %v\n%s", err, output.String())
	}
	defer os.RemoveAll(first.Dir)
	if first.Project.Root != canonicalInput(filepath.Join(root, "selected")) || first.DevRefresh != nil {
		t.Fatal("wrong selected app or development mode")
	}
	support, err := wasmSupport(ctx, first.Project.Root)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(first.Dir, "server/assets/_nextgo", first.ID)
	actual, err := os.ReadFile(filepath.Join(staged, "wasm_exec.js"))
	if err != nil || !bytes.Equal(actual, support) {
		t.Fatal("selected SDK support was not paired with browser compiler", err)
	}
	main, err := os.ReadFile(filepath.Join(first.Dir, "server/main.go"))
	if err != nil || bytes.Contains(main, []byte("peer.example.test")) || bytes.Contains(main, []byte("app/details")) {
		t.Fatal("peer source leaked into native graph", err)
	}
	marker := filepath.Join(t.TempDir(), "hook.txt")
	command := exec.CommandContext(ctx, first.Binary, "-backend-hosts", "127.0.0.1:4321")
	command.Env = append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=invalid", "NEXTGO_ENV_DIR=", "NEXTGO_RELEASE_TEST_MARKER="+marker)
	result, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(result, []byte("AWS_LAMBDA_RUNTIME_API must be host:port")) {
		t.Fatalf("release startup/context hook failed before intentional local RuntimeAPI validation: %v\n%s", err, result)
	}
	count, err := os.ReadFile(marker)
	if err != nil || string(count) != "hook\n" {
		t.Fatal("configuration hook was not invoked exactly once", string(count), err)
	}
	output.Reset()
	second, err := CompileZoneRelease(ctx, filepath.Join(root, "zones.json"), "marketing", "https://example.test", "A2", &output)
	if err != nil {
		t.Fatalf("second selected release compile: %v\n%s", err, output.String())
	}
	defer os.RemoveAll(second.Dir)
	if first.ID != second.ID || first.Manifest.Release.RuntimeID != second.Manifest.Release.RuntimeID || first.Manifest.Release.AssetsSHA256 != second.Manifest.Release.AssetsSHA256 || first.Manifest.BinarySHA256 == second.Manifest.BinarySHA256 || second.Manifest.Release.ReleaseID != "A2" {
		t.Fatal("release identity incorrectly replaced runtime identity or failed to bind native contract")
	}
}
