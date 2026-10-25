package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

func zoneIngressCLIFixture(t *testing.T) (zonecontract.IngressManifest, string) {
	t.Helper()
	release := zoneReleaseCLIFixture(t)
	defer os.RemoveAll(release.Dir)
	manifest := zonecontract.IngressManifest{Version: zonecontract.Version, TopologySHA256: release.Manifest.Release.TopologySHA256, Zones: []zonecontract.IngressZone{{Binding: release.Manifest.Release.Zone, ActiveRelease: "A1", Releases: []zonecontract.IngressRelease{{Artifact: release.Manifest, Origin: "http://127.0.0.1:4321"}}}}}
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

func TestZoneIngressCommandLoadsOneImmutableManifestAndExplicitOptions(t *testing.T) {
	_, path := zoneIngressCLIFixture(t)
	var output bytes.Buffer
	called := 0
	sentinel := errors.New("runner result")
	run := func(ctx context.Context, m zonecontract.IngressManifest, addr string, o nextgo.ZoneIngressOptions, w io.Writer) error {
		called++
		if addr != "127.0.0.1:4333" || len(m.Zones) != 1 || m.Zones[0].ActiveRelease != "A1" || !o.TrustedTLSOffload || o.Timeout != time.Second || o.MaxRequestBytes != 1234 || o.MaxResponseBytes != 5678 || o.MaxInFlight != 3 || w != &output {
			t.Fatal("wrong frozen ingress options")
		}
		return sentinel
	}
	err := executeZoneIngressWith(context.Background(), []string{"-manifest", path, "-addr", "127.0.0.1:4333", "-trusted-tls-offload", "-timeout", "1s", "-max-request-bytes", "1234", "-max-response-bytes", "5678", "-max-inflight", "3"}, &output, run)
	if !errors.Is(err, sentinel) || called != 1 {
		t.Fatal("wrong runner completion", err)
	}
}

func TestZoneIngressCommandRejectsMalformedStartupBeforeServing(t *testing.T) {
	_, path := zoneIngressCLIFixture(t)
	for _, args := range [][]string{{}, {"-manifest", path, "positional"}, {"-manifest", path, "-addr", ""}, {"-manifest", path, "-timeout", "0s"}, {"-manifest", path, "-max-request-bytes", "0"}, {"-manifest", path, "-max-response-bytes", "67108865"}, {"-manifest", path, "-max-inflight", "1025"}, {"-manifest", path, "-unknown"}} {
		var output bytes.Buffer
		run := func(context.Context, zonecontract.IngressManifest, string, nextgo.ZoneIngressOptions, io.Writer) error {
			t.Fatal("bad startup reached serving")
			return nil
		}
		if err := executeZoneIngressWith(context.Background(), args, &output, run); err == nil {
			t.Fatal("malformed ingress startup accepted", args)
		}
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"Version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := executeZoneIngressWith(context.Background(), []string{"-manifest", path}, &output, func(context.Context, zonecontract.IngressManifest, string, nextgo.ZoneIngressOptions, io.Writer) error {
		t.Fatal("bad manifest reached serving")
		return nil
	}); err == nil {
		t.Fatal("case aliased manifest accepted")
	}
	for _, command := range []string{"ingress", "ingress-build"} {
		if err := execute(context.Background(), []string{"zones", command}); err == nil || !strings.Contains(err.Error(), "zones "+command) {
			t.Fatal("public dispatch missing", command, err)
		}
	}
}

func TestZoneIngressRuntimeCLIUsesLambdaAdapterAndJoinsOnListenFailure(t *testing.T) {
	manifest, _ := zoneIngressCLIFixture(t)
	var output bytes.Buffer
	t.Setenv("AWS_LAMBDA_RUNTIME_API", "invalid")
	err := serveZoneIngress(context.Background(), manifest, ":3000", nextgo.ZoneIngressOptions{}, &output)
	if err == nil || !strings.Contains(err.Error(), "AWS_LAMBDA_RUNTIME_API must be host:port") {
		t.Fatal("CLI did not use actual Lambda adapter", err)
	}
	t.Setenv("AWS_LAMBDA_RUNTIME_API", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := serveZoneIngress(ctx, manifest, "invalid address without port", nextgo.ZoneIngressOptions{}, &output); err == nil {
		t.Fatal("invalid listen address accepted")
	}
}

func TestZoneIngressBuildPublishesStandaloneBinaryAndCanonicalManifest(t *testing.T) {
	manifest, _ := zoneIngressCLIFixture(t)
	release := zoneReleaseCLIFixture(t)
	build := &project.ZoneIngressBuild{Build: release.Build, Manifest: manifest, BinarySHA256: release.Manifest.BinarySHA256}
	out := filepath.Join(t.TempDir(), "ingress")
	var output bytes.Buffer
	compile := func(ctx context.Context, app, path string, w io.Writer) (*project.ZoneIngressBuild, error) {
		if app != "selected/module" || path != "ingress.json" || w != &output {
			t.Fatal("wrong explicit module anchor")
		}
		return build, nil
	}
	if err := executeZoneIngressBuildWith(context.Background(), []string{"-out", out, "-app", "selected/module", "ingress.json"}, &output, compile); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, project.ZoneIngressManifestName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := zonecontract.ParseIngressManifest(data); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "server")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(build.Dir); !os.IsNotExist(err) {
		t.Fatal("temporary ingress build retained", err)
	}
}

func TestZoneIngressBuildRefusesExistingOutputBeforeCompiler(t *testing.T) {
	existing := t.TempDir()
	var output bytes.Buffer
	compile := func(context.Context, string, string, io.Writer) (*project.ZoneIngressBuild, error) {
		t.Fatal("existing destination reached compiler")
		return nil, nil
	}
	if err := executeZoneIngressBuildWith(context.Background(), []string{"-out", existing, "-app", "anchor", "ingress.json"}, &output, compile); err == nil {
		t.Fatal("existing ingress artifact overwritten")
	}
}

func TestZoneIngressPublishedManifestFitsExactReaderBudget(t *testing.T) {
	base, _ := zoneIngressCLIFixture(t)
	large := func(width int) zonecontract.IngressManifest {
		m := zonecontract.IngressManifest{Version: 1, TopologySHA256: base.TopologySHA256}
		for zone := 0; zone < 2; zone++ {
			z := zonecontract.IngressZone{Binding: base.Zones[0].Binding, ActiveRelease: "A0"}
			z.Binding.ID = fmt.Sprintf("zone%d", zone)
			z.Binding.Host = fmt.Sprintf("zone%d.example.test", zone)
			for release := 0; release < 8; release++ {
				r := base.Zones[0].Releases[0]
				r.Artifact = r.Artifact.Clone()
				r.Artifact.Release.Zone = z.Binding
				r.Artifact.Release.ReleaseID = fmt.Sprintf("A%d", release)
				r.Artifact.Release.AssetPrefix = "/blog/_nextgo/releases/" + r.Artifact.Release.ReleaseID
				r.Artifact.Release.PublicOrigin = "https://" + z.Binding.Host
				for doc := 0; doc < 64; doc++ {
					r.Artifact.Release.DocumentPaths = append(r.Artifact.Release.DocumentPaths, fmt.Sprintf("/docs-%02d/", doc)+strings.Repeat("x", width))
				}
				z.Releases = append(z.Releases, r)
			}
			m.Zones = append(m.Zones, z)
		}
		return m
	}
	low, high := 800, 1010
	var manifest zonecontract.IngressManifest
	for low <= high {
		mid := (low + high) / 2
		m, err := zonecontract.NormalizeIngressManifest(large(mid))
		if err == nil {
			manifest = m
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	compact, _ := json.Marshal(manifest)
	indented, _ := json.MarshalIndent(manifest, "", "  ")
	if len(compact) > zonecontract.MaxManifestBytes || len(indented) <= zonecontract.MaxManifestBytes {
		t.Fatal("fixture does not expose indentation expansion", len(compact), len(indented))
	}
	release := zoneReleaseCLIFixture(t)
	build := &project.ZoneIngressBuild{Build: release.Build, Manifest: manifest, BinarySHA256: release.Manifest.BinarySHA256}
	out := filepath.Join(t.TempDir(), "ingress")
	var output bytes.Buffer
	if err := executeZoneIngressBuildWith(context.Background(), []string{"-out", out, "-app", "anchor", "ingress.json"}, &output, func(context.Context, string, string, io.Writer) (*project.ZoneIngressBuild, error) { return build, nil }); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(out, project.ZoneIngressManifestName))
	if err != nil || len(actual) > zonecontract.MaxManifestBytes {
		t.Fatal("published bytes exceed reader budget", len(actual), err)
	}
	if _, err := project.ReadZoneIngressManifest(filepath.Join(out, project.ZoneIngressManifestName)); err != nil {
		t.Fatal("publisher produced unreadable own manifest", err)
	}
}
