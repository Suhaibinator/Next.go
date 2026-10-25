package project

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

func zoneIngressManifestFixture(t *testing.T) (zonecontract.IngressManifest, string) {
	t.Helper()
	release := zonecontract.ReleaseManifest{Release: zonecontract.ReleaseSpec{Version: zonecontract.Version, Zone: zonecontract.Binding{ID: "blog", Host: "example.test", BasePath: "/blog"}, ReleaseID: "A1", RuntimeID: strings.Repeat("a", 20), PublicOrigin: "https://example.test", AssetPrefix: "/blog/_nextgo/releases/A1", AssetsSHA256: strings.Repeat("b", 64), TopologySHA256: strings.Repeat("c", 64), NavigationProtocol: navigation.ProtocolVersion, ActionProtocol: actionwire.Protocol}, BinarySHA256: strings.Repeat("d", 64)}
	manifest := zonecontract.IngressManifest{Version: zonecontract.Version, TopologySHA256: release.Release.TopologySHA256, Zones: []zonecontract.IngressZone{{Binding: release.Release.Zone, ActiveRelease: "A1", Releases: []zonecontract.IngressRelease{{Artifact: release, Origin: "http://127.0.0.1:4321"}}}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ingress.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return manifest, path
}

func TestZoneIngressBuildReadsStrictManifestAndGeneratesSourceIndependentEntrypoint(t *testing.T) {
	manifest, path := zoneIngressManifestFixture(t)
	parsed, err := ReadZoneIngressManifest(path)
	if err != nil || len(parsed.Zones) != 1 {
		t.Fatal(err)
	}
	canonical, _ := json.Marshal(manifest)
	source := zoneIngressServerSource(canonical)
	if !strings.Contains(source, "nextgo.NewZoneIngress(") || !strings.Contains(source, "serverless.Run(ctx,app)") || !strings.Contains(source, "app.Close(") || strings.Contains(source, "os.ReadFile") || strings.Contains(source, "os/exec") || strings.Contains(source, "example.com/app") {
		t.Fatal("standalone ingress has app/source dependency", source)
	}
	if err := writeGo(filepath.Join(t.TempDir(), "main.go"), source); err != nil {
		t.Fatal("invalid generated ingress source", err)
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"Version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadZoneIngressManifest(path); err == nil {
		t.Fatal("case-aliased ingress manifest accepted")
	}
	var output bytes.Buffer
	if _, err := CompileZoneIngress(context.Background(), "absent-anchor", path, &output); err == nil || output.Len() != 0 {
		t.Fatal("invalid manifest reached native compiler", err)
	}
}

// Run after the coordinated source freeze: even malformed application Go is
// irrelevant to the explicitly anchored framework-only native compiler graph.
func TestZoneIngressActualCompilerDoesNotImportOrExecuteAnchorApplication(t *testing.T) {
	_, manifest := zoneIngressManifestFixture(t)
	root := fixture(t, map[string]string{"app/page.go": "this application does not compile"})
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	module := "module independent.example.test\ngo 1.24\nrequire " + framework + " v0.0.0\nreplace " + framework + " => " + strconv.Quote(repo) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var output bytes.Buffer
	build, err := CompileZoneIngress(ctx, root, manifest, &output)
	if err != nil {
		t.Fatalf("framework-only ingress build: %v\n%s", err, output.String())
	}
	defer os.RemoveAll(build.Dir)
	if build.Project != nil || build.BinarySHA256 == "" {
		t.Fatal("ingress compiler unexpectedly discovered an app")
	}
	source, err := os.ReadFile(filepath.Join(build.Dir, "server/main.go"))
	if err != nil || bytes.Contains(source, []byte("independent.example.test/app")) {
		t.Fatal("anchor app imported", err)
	}
	command := exec.CommandContext(ctx, build.Binary)
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=invalid")
	data, err := command.CombinedOutput()
	if err == nil || !bytes.Contains(data, []byte("AWS_LAMBDA_RUNTIME_API must be host:port")) {
		t.Fatalf("standalone binary required source, manifest file or compiler at runtime: %v\n%s", err, data)
	}
	for _, flags := range [][]string{{"-max-inflight", "0"}, {"-addr", ""}} {
		command := exec.CommandContext(ctx, build.Binary, flags...)
		command.Env = append(os.Environ(), "AWS_LAMBDA_RUNTIME_API=invalid")
		data, err := command.CombinedOutput()
		if err == nil || !bytes.Contains(data, []byte("invalid ingress startup options")) {
			t.Fatalf("standalone flags differ from CLI ingress validation: %v\n%s", err, data)
		}
	}
}

func TestIndependentZoneGeneratedEntrypointsAlwaysCloseOwnedApplication(t *testing.T) {
	for name, source := range map[string]string{"release": zoneReleaseServerSource(&Project{}, &zoneReleasePlan{}), "ingress": zoneIngressServerSource([]byte(`{}`))} {
		constructor := strings.Index(source, "app,err:=nextgo.NewZone")
		drain := strings.Index(source, "defer func(){drain,cancel:=")
		lambda := strings.Index(source, `if os.Getenv("AWS_LAMBDA_RUNTIME_API")`)
		if constructor < 0 || drain <= constructor || lambda <= drain || strings.Contains(source, "log.Fatal(") || !strings.Contains(source, "func run()error") || !strings.Contains(source, "defer func(){stop();<-shutdownDone}()") {
			t.Fatal("generated startup failure can bypass app drain or server goroutine join", name, source)
		}
		if err := writeGo(filepath.Join(t.TempDir(), "main.go"), source); err != nil {
			t.Fatal(name, err)
		}
	}
}
