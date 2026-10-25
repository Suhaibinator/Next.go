package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/actionwire"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

func zoneReleaseCLIFixture(t *testing.T) *project.ZoneReleaseBuild {
	t.Helper()
	build := zonesFixtureBuild(t, "selected independent server")
	data, err := os.ReadFile(build.Binary)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return &project.ZoneReleaseBuild{Build: build, Manifest: zonecontract.ReleaseManifest{Release: zonecontract.ReleaseSpec{
		Version: zonecontract.Version, Zone: zonecontract.Binding{ID: "blog", Host: "example.test", BasePath: "/blog"}, ReleaseID: "A1", RuntimeID: strings.Repeat("a", 20), PublicOrigin: "https://example.test", AssetPrefix: "/blog/_nextgo/releases/A1", AssetsSHA256: strings.Repeat("b", 64), TopologySHA256: strings.Repeat("c", 64), NavigationProtocol: navigation.ProtocolVersion, ActionProtocol: actionwire.Protocol,
	}, BinarySHA256: hex.EncodeToString(sum[:])}}
}

func TestZoneReleaseCommandPublishesMatchingAtomicArtifact(t *testing.T) {
	build := zoneReleaseCLIFixture(t)
	out := filepath.Join(t.TempDir(), "release")
	var output bytes.Buffer
	called := 0
	compile := func(ctx context.Context, path, zone, origin, release string, writer io.Writer) (*project.ZoneReleaseBuild, error) {
		called++
		if path != "zones.json" || zone != "blog" || origin != "https://example.test" || release != "A1" || writer != &output {
			t.Fatal("incorrect release request")
		}
		return build, nil
	}
	if err := executeZoneReleaseWith(context.Background(), []string{"-zone", "blog", "-public-origin", "https://example.test", "-release", "A1", "-out", out, "zones.json"}, &output, compile); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, project.ZoneReleaseManifestName))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := zonecontract.ParseReleaseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(filepath.Join(out, "server"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(binary)
	if manifest.BinarySHA256 != hex.EncodeToString(sum[:]) || called != 1 {
		t.Fatal("published artifact identity mismatch")
	}
	for _, name := range []string{"server", project.ZoneReleaseManifestName} {
		info, err := os.Stat(filepath.Join(out, name))
		if err != nil || info.Mode().Perm()&0444 != 0444 {
			t.Fatal("artifact not readable", name, err)
		}
	}
	if _, err := os.Stat(build.Dir); !os.IsNotExist(err) {
		t.Fatal("compiler generation retained", err)
	}
}

func TestZoneReleaseCommandRejectsRequestsBeforeCompilation(t *testing.T) {
	for _, args := range [][]string{{}, {"-zone", "blog"}, {"-zone", "blog", "-public-origin", "https://example.test", "-out", "", "zones.json"}, {"-unknown", "zones.json"}, {"-zone", "blog", "-public-origin", "https://example.test", "-out", "somewhere", "one.json", "two.json"}} {
		var output bytes.Buffer
		called := false
		compile := func(context.Context, string, string, string, string, io.Writer) (*project.ZoneReleaseBuild, error) {
			called = true
			return nil, errors.New("must not compile")
		}
		if err := executeZoneReleaseWith(context.Background(), args, &output, compile); err == nil || called {
			t.Fatal("invalid request reached compiler", args, err)
		}
	}
	existing := t.TempDir()
	var output bytes.Buffer
	compile := func(context.Context, string, string, string, string, io.Writer) (*project.ZoneReleaseBuild, error) {
		t.Fatal("existing output reached compiler")
		return nil, nil
	}
	if err := executeZoneReleaseWith(context.Background(), []string{"-zone", "blog", "-public-origin", "https://example.test", "-out", existing, "zones.json"}, &output, compile); err == nil {
		t.Fatal("existing output accepted")
	}
	if err := execute(context.Background(), []string{"zones", "release"}); err == nil || !strings.Contains(err.Error(), "zones release") {
		t.Fatal("public zones release dispatch missing", err)
	}
}

func TestZoneReleasePublicationFailuresDoNotPublishMixedArtifact(t *testing.T) {
	for _, mode := range []string{"mismatch", "invalid-manifest", "cancel", "missing-binary"} {
		t.Run(mode, func(t *testing.T) {
			build := zoneReleaseCLIFixture(t)
			out := filepath.Join(t.TempDir(), "release")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "mismatch":
				if err := os.WriteFile(build.Binary, []byte("changed artifact"), 0755); err != nil {
					t.Fatal(err)
				}
			case "invalid-manifest":
				build.Manifest.BinarySHA256 = "invalid"
			case "cancel":
				cancel()
			case "missing-binary":
				if err := os.Remove(build.Binary); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			compile := func(context.Context, string, string, string, string, io.Writer) (*project.ZoneReleaseBuild, error) {
				return build, nil
			}
			if err := executeZoneReleaseWith(ctx, []string{"-zone", "blog", "-public-origin", "https://example.test", "-out", out, "zones.json"}, &output, compile); err == nil {
				t.Fatal("invalid artifact published")
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Fatal("partial output exists", err)
			}
			stages, err := filepath.Glob(filepath.Join(filepath.Dir(out), ".nextgo-zone-release-*"))
			if err != nil || len(stages) != 0 {
				t.Fatal("publication staging retained", stages, err)
			}
			if _, err := os.Stat(build.Dir); !os.IsNotExist(err) {
				t.Fatal("failed compiler generation retained", err)
			}
		})
	}
}

func TestZoneReleasePublishedManifestOmitsPrivateTenant(t *testing.T) {
	build := zoneReleaseCLIFixture(t)
	data, err := json.Marshal(build.Manifest)
	if err != nil || bytes.Contains(data, []byte("tenant")) {
		t.Fatal("private ownership leaked into portable contract", err)
	}
}
