package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/project"
	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

const shellExportTestBuild = "0123456789abcdef0123"

func shellExportFixture(t *testing.T, deployment nextgo.PathConfig, mode *atomic.Int32) (string, string, nextgo.ExportConfig) {
	t.Helper()
	assets := t.TempDir()
	for name, value := range map[string]string{"_nextgo/" + shellExportTestBuild + "/app.wasm": "wasm", "_nextgo/" + shellExportTestBuild + "/boot.js": "boot", "public/style.css": "body{}"} {
		file := filepath.Join(assets, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	app, err := nextgo.New(nextgo.Config{Phase: nextgo.PhaseStaticExport, BuildID: shellExportTestBuild, BasePath: deployment.BasePath, AssetPrefix: deployment.AssetPrefix, TrailingSlash: deployment.TrailingSlash, StaticShell: &nextgo.StaticShell{Pattern: "/workspace/*rest?", Component: "workspace"}, Routes: []nextgo.Route{
		{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Title: "Home", Body: ui.Text("Ordinary home")}, nil
		}},
		{Pattern: "/workspace/*rest?", Page: func(c *nextgo.Context) (nextgo.Page, error) {
			if c.Request.URL.RawQuery != "" || c.Request.Header.Get("Cookie") != "" || c.Request.Header.Get("Authorization") != "" {
				t.Error("seed captured caller query or credentials")
			}
			page := nextgo.Page{Title: "Generic workspace", Meta: map[string]string{"robots": "noindex"}, Body: ui.ClientIsland("workspace", map[string]string{"public": "actual-native-seed"}, ui.El("p", nil, ui.Text("Useful generic workspace fallback")))}
			switch mode.Load() {
			case 1:
				page.Metadata.Canonical = "https://example.invalid/workspace"
			case 2:
				page.Body = ui.Island("workspace", nil, ui.Text("Ordinary island"))
			case 3:
				page.Meta["og:url"] = "https://example.invalid/workspace"
			case 4:
				data, err := seo.JSONLD(map[string]string{"@type": "Thing"})
				if err != nil {
					return nextgo.Page{}, err
				}
				page.JSONLD = []seo.StructuredData{data}
			}
			return page, nil
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	info, err := app.StaticExportConfig()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/workspace") {
			if mode.Load() == 5 {
				w.Header().Set("Set-Cookie", "session=public-export-must-refuse")
			}
			if mode.Load() == 6 {
				w.Header().Set("WWW-Authenticate", "Basic")
			}
		}
		app.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return assets, server.URL, info
}

func runShellFixtureExport(ctx context.Context, base, assets, out string, info nextgo.ExportConfig, paths []string) error {
	return exportWithStaticShell(ctx, base, assets, out, info.Build, paths, info.Paths, info.Scopes, info.Metadata, info.StaticShell)
}

func TestStaticShellExportPublishesActualBoundedSeedAndAllDocumentCarriers(t *testing.T) {
	for _, trailing := range []bool{false, true} {
		mode := &atomic.Int32{}
		deployment := nextgo.PathConfig{BasePath: "/docs", AssetPrefix: "/shared", TrailingSlash: trailing}
		assets, base, info := shellExportFixture(t, deployment, mode)
		out := filepath.Join(t.TempDir(), "static")
		// No concrete optional-catchall enumeration: export adds its native seed.
		if err := runShellFixtureExport(t.Context(), base, assets, out, info, []string{"/"}); err != nil {
			t.Fatal(err)
		}
		manifestData, err := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
		var manifest exportManifest
		if err != nil || json.Unmarshal(manifestData, &manifest) != nil || manifest.Format != "nextgo-static-v3" || manifest.StaticShell == nil || len(manifest.Paths) != 2 || len(manifest.Scopes) != 0 {
			t.Fatal(string(manifestData), err)
		}
		descriptor := *manifest.StaticShell
		if err := descriptor.Validate(deployment, info.Build, info.Scopes[0].Configuration); err != nil {
			t.Fatal(err)
		}
		seedData, err := readStaticShellArtifact(out, descriptor.EnvelopeURL, navigation.MaxStaticShellEnvelopeBytes)
		if err != nil || descriptor.CheckEnvelope(seedData) != nil || !bytes.Contains(seedData, []byte("actual-native-seed")) {
			t.Fatal("immutable bytes are not the actual seed", err)
		}
		var seed nextgo.Envelope
		if json.Unmarshal(seedData, &seed) != nil || seed.URL != descriptor.SeedURL || !strings.Contains(seed.HTML, "Useful generic workspace fallback") {
			t.Fatal(seed)
		}
		encoded, _ := json.Marshal(descriptor)
		for _, name := range []string{"docs/index.html", "docs/workspace/index.html"} {
			markup, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			body, err := staticShellDocumentBody(markup)
			if err != nil || body.attrs["data-nextgo-static-shell"] != string(encoded) || body.attrs["data-nextgo-export"] != "true" {
				t.Fatal(name, body, err)
			}
		}
		ordinary, err := os.ReadFile(filepath.Join(out, "docs", "_nextgo", "data", "index.json"))
		if err != nil || !bytes.Contains(ordinary, []byte("Ordinary home")) {
			t.Fatal("ordinary navigation data was replaced", err)
		}
		if err := validateExportDestination(out); err != nil {
			t.Fatal("new v3 destination is not recognized", err)
		}
		if err := runShellFixtureExport(t.Context(), base, assets, out, info, []string{"/"}); err != nil {
			t.Fatal("complete v3 destination cannot be replaced", err)
		}
	}
}

func TestStaticShellSeedFailuresPreserveCompletedPublication(t *testing.T) {
	mode := &atomic.Int32{}
	assets, base, info := shellExportFixture(t, nextgo.PathConfig{}, mode)
	out := filepath.Join(t.TempDir(), "static")
	if err := runShellFixtureExport(t.Context(), base, assets, out, info, []string{"/"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
	beforeSeed, _ := os.ReadFile(filepath.Join(out, "workspace", "index.html"))
	for _, failure := range []int32{1, 2, 3, 4, 5, 6} {
		mode.Store(failure)
		if err := runShellFixtureExport(t.Context(), base, assets, out, info, []string{"/"}); err == nil {
			t.Fatal("invalid seed published", failure)
		}
		after, _ := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
		afterSeed, _ := os.ReadFile(filepath.Join(out, "workspace", "index.html"))
		if !bytes.Equal(after, before) || !bytes.Equal(afterSeed, beforeSeed) {
			t.Fatal("failed staged export changed previous generation", failure)
		}
	}
	mode.Store(0)
	if err := publishExport(filepath.Join(t.TempDir(), "missing-stage"), out); err == nil {
		t.Fatal("expected publication rename failure")
	}
	after, _ := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
	if !bytes.Equal(after, before) {
		t.Fatal("rename failure lost completed v3 publication")
	}
}

func TestStaticShellDestinationRejectsCorruptOrSymlinkedImmutableBytes(t *testing.T) {
	mode := &atomic.Int32{}
	assets, base, info := shellExportFixture(t, nextgo.PathConfig{}, mode)
	out := filepath.Join(t.TempDir(), "static")
	if err := runShellFixtureExport(t.Context(), base, assets, out, info, []string{"/"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(out, ".nextgo-export.json"))
	var manifest exportManifest
	if json.Unmarshal(data, &manifest) != nil || manifest.StaticShell == nil {
		t.Fatal("missing descriptor")
	}
	file := filepath.Join(out, filepath.FromSlash(strings.TrimPrefix(manifest.StaticShell.EnvelopeURL, "/")))
	original, _ := os.ReadFile(file)
	if err := os.WriteFile(file, append(original, ' '), 0644); err != nil {
		t.Fatal(err)
	}
	if err := validateExportDestination(out); err == nil {
		t.Fatal("changed immutable bytes were trusted")
	}
	outside := filepath.Join(t.TempDir(), "seed.json")
	if err := os.WriteFile(outside, original, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if err := validateExportDestination(out); err == nil {
		t.Fatal("symlinked immutable artifact was trusted")
	}
}

func TestStaticShellExportConfigurationStrictVersionShapes(t *testing.T) {
	config := nextgo.ExportConfig{Version: 3, Build: shellExportTestBuild, Scopes: []nextgo.StaticExportScope{{Configuration: strings.Repeat("a", 64)}}, StaticShell: &nextgo.StaticShellDeclaration{Version: 1, Pattern: "/workspace/*rest?", Component: "workspace", SeedPath: "/workspace"}}
	data, _ := json.Marshal(config)
	if _, err := decodeExportConfig(data, config.Build, ""); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ field, value string }{
		{"version", "1"}, {"version", "2"}, {"version", "4"}, {"version", "null"}, {"staticShell", "null"}, {"paths", "null"}, {"scopes", "null"}, {"unknown", "true"},
		{"staticShell", `{"version":1,"pattern":"/workspace/*rest?","component":"workspace","seedPath":"/different"}`},
		{"staticShell", `{"version":1,"pattern":"/*rest?","component":"workspace","seedPath":"/"}`},
	} {
		copy := map[string]json.RawMessage{}
		for name, value := range fields {
			copy[name] = value
		}
		copy[test.field] = json.RawMessage(test.value)
		bad, _ := json.Marshal(copy)
		if _, err := decodeExportConfig(bad, config.Build, ""); err == nil {
			t.Fatal("invalid version-specific declaration accepted", string(bad))
		}
	}
	duplicate := append(append([]byte(nil), data[:len(data)-1]...), []byte(`,"version":3}`)...)
	if _, err := decodeExportConfig(duplicate, config.Build, ""); err == nil {
		t.Fatal("duplicate configuration key accepted")
	}
	config.Version, config.StaticShell = 1, nil
	legacy, _ := json.Marshal(config)
	if _, err := decodeExportConfig(legacy, config.Build, ""); err != nil {
		t.Fatal("legacy v1 declaration changed", err)
	}
	legacy = []byte(strings.TrimSuffix(string(legacy), "}") + `,"staticShell":null}`)
	if _, err := decodeExportConfig(legacy, config.Build, ""); err != nil {
		t.Fatal("legacy null extension behavior changed", err)
	}
}

type staticShellRawHTML string

func (raw staticShellRawHTML) Render(w io.Writer) error {
	_, err := io.WriteString(w, string(raw))
	return err
}

func TestStaticShellActualFragmentRefusesAmbiguousOrNestedDeclarations(t *testing.T) {
	valid, err := ui.HTML(ui.ClientIsland("workspace", nil, ui.El("p", nil, ui.Text("Useful fallback"))))
	if err != nil || validateStaticShellFragment([]byte(valid), "workspace") != nil {
		t.Fatal(valid, err)
	}
	for _, bad := range []string{
		strings.Replace(valid, `data-nextgo-client="1"`, `data-nextgo-client="2"`, 1),
		strings.Replace(valid, "workspace", "different", 1), valid + valid,
		strings.Replace(valid, "Useful fallback", "", 1),
		strings.Replace(valid, "Useful fallback", `<span data-nextgo-boundary="stream">Loading</span>`, 1),
		strings.Replace(valid, "Useful fallback", `<script>hidden</script>`, 1),
		strings.Replace(valid, "Useful fallback", `<span data-nextgo-key="x">nested key</span>`, 1),
		strings.Replace(valid, "Useful fallback", `<svg><foreignObject>ambiguous</foreignObject></svg>`, 1),
		`<nextgo-layout>` + valid + `</nextgo-layout>`,
	} {
		if err := validateStaticShellFragment([]byte(bad), "workspace"); err == nil {
			t.Fatal("invalid actual seed body accepted", bad)
		}
	}
	// A custom Node can bypass the typed fallback checks; rendered-byte checks
	// must still reject its unsafe attributes and framework declarations.
	custom, err := ui.HTML(ui.ClientIsland("workspace", nil, staticShellRawHTML(`<p onclick="run()">Useful fallback</p>`)))
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStaticShellFragment([]byte(custom), "workspace"); err == nil {
		t.Fatal("custom unsafe fallback accepted")
	}
}

func TestStaticShellSeedIsExplicitlyAddedBeforeScopedPathValidation(t *testing.T) {
	p := &project.Project{Routes: []project.Route{{Pattern: "/workspace/*rest?"}}}
	config := nextgo.ExportConfig{Version: 3, Build: shellExportTestBuild, Scopes: []nextgo.StaticExportScope{{Configuration: strings.Repeat("a", 64)}}, StaticShell: &nextgo.StaticShellDeclaration{Version: 1, Pattern: "/workspace/*rest?", Component: "workspace", SeedPath: "/workspace"}}
	paths, err := validateScopedExportPaths(p, []string{config.StaticShell.SeedPath}, config)
	if err != nil || fmt.Sprint(paths) != "[/workspace]" {
		t.Fatal(paths, err)
	}
	p.Routes = []project.Route{{Pattern: "/"}, {Pattern: "/index/*rest?"}}
	config.StaticShell.Pattern, config.StaticShell.SeedPath = "/index/*rest?", "/index"
	if _, err := validateScopedExportPaths(p, []string{"/", config.StaticShell.SeedPath}, config); err == nil {
		t.Fatal("seed bypassed navigation-data collision checks")
	}
}

func TestStaticShellProjectRequiresExactGeneratedComponent(t *testing.T) {
	shell := &nextgo.StaticShellDeclaration{Version: 1, Pattern: "/workspace/*rest?", Component: "workspace", SeedPath: "/workspace"}
	p := &project.Project{Components: []project.Component{{Name: "workspace"}, {Name: "workspacelazy", Lazy: true}}}
	if err := validateStaticShellProject(p, shell); err != nil {
		t.Fatal(err)
	}
	shell.Component = "missing"
	if err := validateStaticShellProject(p, shell); err == nil {
		t.Fatal("missing generated component accepted")
	}
}

func TestStaticShellConfigurationPreservesLegacyExtensions(t *testing.T) {
	entry, err := metadatafiles.Validate("robots.txt", []byte("User-agent: *\n"), metadatafiles.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{1, 2} {
		config := nextgo.ExportConfig{Version: version, Build: shellExportTestBuild, Scopes: []nextgo.StaticExportScope{{Configuration: strings.Repeat("a", 64)}}}
		if version == 2 {
			config.Metadata = []metadatafiles.Entry{entry}
		}
		base, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		// The previous reader used ordinary json.Unmarshal. Preserve its
		// acceptance of valid producer extension objects and last-key behavior.
		extended := []byte(strings.TrimSuffix(string(base), "}") + fmt.Sprintf(`,"producerExtension":{"version":7,"nested":{"owned":true}},"version":%d,"staticShell":null}`, version))
		var previous nextgo.ExportConfig
		if err := json.Unmarshal(extended, &previous); err != nil {
			t.Fatal("fixture was not accepted by the legacy decoder", err)
		}
		decoded, err := decodeExportConfig(extended, config.Build, "")
		if err != nil || decoded.Version != version || len(decoded.Metadata) != len(config.Metadata) || decoded.Scopes[0].Configuration != config.Scopes[0].Configuration {
			t.Fatal("legacy extension/duplicate-key behavior changed", version, decoded, err)
		}
		reserved := []byte(strings.TrimSuffix(string(base), "}") + `,"staticShell":{"version":1,"pattern":"/workspace/*rest?","component":"workspace","seedPath":"/workspace"}}`)
		if _, err := decodeExportConfig(reserved, config.Build, ""); err == nil {
			t.Fatal("legacy version accepted a nonnull shell declaration", version)
		}
	}
}
