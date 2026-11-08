package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/examples/demo/localfonts"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestManagedFontStaticExportScopesBytesAndRollback(t *testing.T) {
	set := localfonts.Set()
	app, err := nextgo.New(nextgo.Config{Phase: nextgo.PhaseStaticExport, BuildID: "font-build", BasePath: "/site", AssetPrefix: "/cdn", TrailingSlash: true,
		LocalFonts: []*assets.LocalFonts{set}, Locales: &nextgo.LocaleConfig{Codes: []string{"en", "fr"}, Default: "en"},
		Routes: []nextgo.Route{{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Resources: []assets.Resource{set.Stylesheet(), set.Manifest().Faces[0].Preload()}, Body: ui.Text("Font publication")}, nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	config, err := app.StaticExportConfig()
	if err != nil || config.Version != 4 || len(config.Fonts) != 3 || len(config.Scopes) != 2 {
		t.Fatal(config, err)
	}
	var fault atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, assets.LocalFontPrefix) && fault.Load() > 0 {
			capture := httptest.NewRecorder()
			app.ServeHTTP(capture, r)
			for name, values := range capture.Header() {
				w.Header()[name] = values
			}
			body := capture.Body.Bytes()
			switch fault.Load() {
			case 1:
				body[len(body)-1] ^= 1
			case 2:
				w.Header().Set("X-Nextgo-Build", "other")
			case 3:
				w.Header().Set("Set-Cookie", "private=1")
			case 4:
				w.Header().Set("Content-Encoding", "gzip")
			case 5:
				w.Header().Set("Content-Type", "text/plain")
			case 6:
				w.WriteHeader(302)
				return
			case 7:
				body = append(body, 'x')
				w.Header().Del("Content-Length")
			case 8:
				w.Header().Set("ETag", "W/"+w.Header().Get("ETag"))
			case 9:
				body = body[:len(body)-1]
			}
			w.WriteHeader(capture.Code)
			_, _ = w.Write(body)
			return
		}
		app.ServeHTTP(w, r)
	}))
	defer server.Close()
	root := t.TempDir()
	for _, name := range []string{"_nextgo", "public"} {
		if err := os.Mkdir(filepath.Join(root, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "published")
	publish := func(fonts []assets.FontAsset) error {
		return exportWithResources(context.Background(), server.URL, root, output, config.Build, []string{"/", "/fr"}, config.Paths, config.Scopes, nil, fonts)
	}
	if err := publish(config.Fonts); err != nil {
		t.Fatal(err)
	}
	for _, scope := range config.Scopes {
		for _, entry := range config.Fonts {
			data, err := os.ReadFile(filepath.Join(output, "site"+scope.Prefix, entry.URL))
			sum := sha256.Sum256(data)
			if err != nil || int64(len(data)) != entry.Bytes || hex.EncodeToString(sum[:]) != entry.SHA256 {
				t.Fatal("published bytes differ", entry, err)
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
	var manifest exportManifest
	if err != nil || json.Unmarshal(data, &manifest) != nil || !reflect.DeepEqual(manifest.Fonts, config.Fonts) {
		t.Fatal("missing font inventory", err)
	}
	previous := exportDependencySnapshot(t, output)
	for mode := int32(1); mode <= 9; mode++ {
		fault.Store(mode)
		if err := publish(config.Fonts); err == nil {
			t.Fatal("accepted bad font response", mode)
		}
		if !reflect.DeepEqual(previous, exportDependencySnapshot(t, output)) {
			t.Fatal("failure changed published generation", mode)
		}
	}
	fault.Store(0)
	if err := publish(nil); err == nil {
		t.Fatal("accepted undeclared font links")
	}
	collision := filepath.Join(root, "_nextgo", strings.TrimPrefix(config.Fonts[0].URL, "/_nextgo/"))
	if err := os.MkdirAll(filepath.Dir(collision), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(collision, []byte("collision"), 0644); err != nil {
		t.Fatal(err)
	}
	// Use the default asset mount so the copied file actually collides.
	deployment := config.Paths
	deployment.AssetPrefix = ""
	if err := exportWithResources(context.Background(), server.URL, root, output, config.Build, nil, deployment, config.Scopes, nil, config.Fonts); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatal("accepted existing publication target", err)
	}
	if !reflect.DeepEqual(previous, exportDependencySnapshot(t, output)) {
		t.Fatal("collision changed published generation")
	}
}

func TestManagedFontInventoryAndLinkAdmission(t *testing.T) {
	entries := localfonts.Set().Manifest().Assets
	if err := validateFontInventory(entries); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*assets.FontAsset){
		func(e *assets.FontAsset) { e.URL = "/../secret" }, func(e *assets.FontAsset) { e.URL += "?x" },
		func(e *assets.FontAsset) { e.SHA256 = strings.Repeat("A", 64) }, func(e *assets.FontAsset) { e.Bytes = 0 },
		func(e *assets.FontAsset) { e.Bytes = 32<<20 + 1 }, func(e *assets.FontAsset) { e.ContentType = "text/plain" },
		func(e *assets.FontAsset) { e.ContentType = "font/css" }, func(e *assets.FontAsset) { e.ContentType = "woff" },
	} {
		bad := entries[0]
		mutate(&bad)
		if validateFontInventory([]assets.FontAsset{bad}) == nil {
			t.Fatal("accepted invalid entry", bad)
		}
	}
	if validateFontInventory(append(entries, entries[0])) == nil {
		t.Fatal("accepted duplicates")
	}
	tooMany := make([]assets.FontAsset, 257)
	if validateFontInventory(tooMany) == nil {
		t.Fatal("accepted too many files")
	}
	var oversized []assets.FontAsset
	for _, character := range []string{"a", "b", "c"} {
		digest := strings.Repeat(character, 64)
		oversized = append(oversized, assets.FontAsset{URL: assets.LocalFontPrefix + digest + ".woff", ContentType: "font/woff", SHA256: digest, Bytes: 32 << 20})
	}
	if validateFontInventory(oversized) == nil {
		t.Fatal("accepted oversized aggregate")
	}
	url := entries[0].URL
	declared := map[string]bool{url: true}
	if unpublishedFontURL("/site/fr"+url, "/site/fr", declared) || !unpublishedFontURL("/site/fr"+url+"?x", "/site/fr", declared) || !unpublishedFontURL("/site/fr"+url, "/site/fr", nil) {
		t.Fatal("scope declaration or query admission")
	}
}

func TestManagedFontsAndStaticShellPublishTogetherWithVersionFour(t *testing.T) {
	deployment := nextgo.PathConfig{BasePath: "/docs", AssetPrefix: "/shared", TrailingSlash: true}
	root, base, config := shellExportFixture(t, deployment, &atomic.Int32{}, localfonts.Set())
	if config.Version != 4 || len(config.Fonts) != 3 || config.StaticShell == nil {
		t.Fatal("combined native inventory lost fonts or shell", config)
	}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeExportConfig(raw, config.Build, ""); err != nil {
		t.Fatal("combined native contract rejected", err)
	}
	legacy := config
	legacy.Version = 3
	raw, _ = json.Marshal(legacy)
	if _, err := decodeExportConfig(raw, legacy.Build, ""); err == nil {
		t.Fatal("ambiguous combined version 3 accepted")
	}
	for _, fonts := range []bool{true, false} {
		legacy := config
		legacy.Version = 3
		if fonts {
			legacy.StaticShell = nil
		} else {
			legacy.Fonts = nil
		}
		raw, _ = json.Marshal(legacy)
		if _, err := decodeExportConfig(raw, legacy.Build, ""); err != nil {
			t.Fatal("single-feature legacy version 3 rejected", fonts, err)
		}
	}
	output := filepath.Join(t.TempDir(), "published")
	if err := exportWithPublication(t.Context(), base, root, output, config.Build, []string{"/"}, deployment, config.Scopes, config.Metadata, config.Fonts, config.StaticShell); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
	var manifest exportManifest
	if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Format != "nextgo-static-v3" || manifest.StaticShell == nil || len(manifest.Fonts) != 3 || len(manifest.Paths) != 2 {
		t.Fatal("combined publication lost either inventory", string(data), err)
	}
	for _, entry := range manifest.Fonts {
		data, err := os.ReadFile(filepath.Join(output, "docs", entry.URL))
		sum := sha256.Sum256(data)
		if err != nil || int64(len(data)) != entry.Bytes || hex.EncodeToString(sum[:]) != entry.SHA256 {
			t.Fatal("combined font bytes changed", entry.URL, err)
		}
	}
	seed, err := readStaticShellArtifact(output, manifest.StaticShell.EnvelopeURL, 1<<20)
	if err != nil || manifest.StaticShell.CheckEnvelope(seed) != nil {
		t.Fatal("combined shell bytes changed", err)
	}
}
