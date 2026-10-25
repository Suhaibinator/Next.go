package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedMetadataDiscoveryAndCopySnapshot(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "app/robots.txt": "User-agent: *\nCrawl-delay: 3\n", "app/sitemap.xml": `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://example.test/</loc></url></urlset>`})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.MetadataFiles) != 2 || p.MetadataFiles[0].Name != "robots.txt" || !p.MetadataFiles[0].Static() || p.MetadataFiles[0].Source != "app/robots.txt" || len(p.MetadataFiles[0].Digest) != 64 {
		t.Fatalf("%#v", p.MetadataFiles)
	}
	target := filepath.Join(t.TempDir(), "metadata")
	if err := CopyMetadataFiles(context.Background(), p, target); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "robots.txt"))
	if err != nil || string(data) != "User-agent: *\nCrawl-delay: 3\n" {
		t.Fatal(string(data), err)
	}
	if err := CopyMetadataFiles(context.Background(), p, target); err == nil {
		t.Fatal("staged overwrite allowed")
	}
	newFile := filepath.Join(root, "app/manifest.webmanifest")
	if err := os.WriteFile(newFile, []byte(`{"name":"appeared"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CopyMetadataFiles(context.Background(), p, filepath.Join(t.TempDir(), "new-metadata")); err == nil || !strings.Contains(err.Error(), "appeared") {
		t.Fatal("new resource escaped discovered inventory", err)
	}
	if err := os.Remove(newFile); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app/robots.txt"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CopyMetadataFiles(context.Background(), p, filepath.Join(t.TempDir(), "metadata")); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatal("changed snapshot published", err)
	}
}

func TestManagedMetadataNativeProducersAreParseOnly(t *testing.T) {
	files := map[string]string{"app/page.go": "package app"}
	for file, function := range map[string]string{"robots": "Robots", "sitemap": "Sitemap", "manifest": "Manifest"} {
		files["app/"+file+".go"] = "//go:build !js\n\npackage app\nfunc " + function + "(){}\n"
	}
	// The generator's typed Sources assignments, not parser execution, check
	// signatures. Discovery must not execute initializers or producer functions.
	files["app/robots.go"] += "func init(){panic(\"must not execute discovery\")}\n"
	root := fixture(t, files)
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.MetadataFiles) != 3 {
		t.Fatal(p.MetadataFiles)
	}
	for _, file := range p.MetadataFiles {
		if file.Static() || file.Import != "example.com/site/app" || file.Factory == "" || file.Digest != "" {
			t.Fatal(file)
		}
	}
}

func TestManagedMetadataDiscoveryRejectsUnsafeInputsAndConflicts(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"static-go":           {"app/robots.txt": "User-agent: *", "app/robots.go": "//go:build !js\n\npackage app\nfunc Robots(){}"},
		"wrong-case-static":   {"app/ROBOTS.TXT": "User-agent: *"},
		"wrong-case-go":       {"app/Robots.go": "package app"},
		"invalid-manifest":    {"app/manifest.webmanifest": `{"start_url":"javascript:bad"}`},
		"invalid-sitemap":     {"app/sitemap.xml": `<!DOCTYPE urlset SYSTEM "https://never-fetch.invalid/"><urlset/>`},
		"invalid-raster":      {"app/icon.png": "<svg/>"},
		"no-native-tag":       {"app/robots.go": "package app\nfunc Robots(){}"},
		"missing-function":    {"app/robots.go": "//go:build !js\n\npackage app\nvar Robots=1"},
		"nested-module":       {"app/go.mod": "module nested\n", "app/robots.txt": "User-agent: *"},
		"exact-page-conflict": {"app/robots.txt": "User-agent: *", "app/group_meta/robots.txt/page.go": "package robots"},
		"exact-api-conflict":  {"app/robots.go": "//go:build !js\n\npackage app\nfunc Robots(){}", "app/robots.txt/route.go": "package robots"},
	} {
		t.Run(name, func(t *testing.T) {
			files["app/page.go"] = "package app"
			if _, err := Discover(fixture(t, files)); err == nil {
				t.Fatal("invalid managed metadata accepted")
			}
		})
	}
	for _, kind := range []string{"static", "producer"} {
		t.Run("symlink-"+kind, func(t *testing.T) {
			root := fixture(t, map[string]string{"app/page.go": "package app"})
			outside := filepath.Join(t.TempDir(), "outside")
			if err := os.WriteFile(outside, []byte("User-agent: *"), 0644); err != nil {
				t.Fatal(err)
			}
			name := "robots.txt"
			if kind == "producer" {
				name = "robots.go"
			}
			if err := os.Symlink(outside, filepath.Join(root, "app", name)); err != nil {
				t.Skip(err)
			}
			if _, err := Discover(root); err == nil {
				t.Fatal("symlink metadata accepted")
			}
		})
	}
	// Manual endpoint directories are not automatically claimed as resources.
	p, err := Discover(fixture(t, map[string]string{"app/page.go": "package app", "app/robots.txt/route.go": "package robots"}))
	if err != nil || len(p.MetadataFiles) != 0 {
		t.Fatal(p, err)
	}
	// A managed concrete resource outranks a normal dynamic fallback; it does
	// not turn every overlapping dynamic page pattern into a collision.
	p, err = Discover(fixture(t, map[string]string{"app/page.go": "package app", "app/param_slug/page.go": "package slug", "app/robots.txt": "User-agent: *"}))
	if err != nil || len(p.MetadataFiles) != 1 {
		t.Fatal(p, err)
	}
}

func TestManagedMetadataCopyPreflightsDestinationAndCancellation(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "app/robots.txt": "User-agent: *", "app/manifest.webmanifest": `{"name":"Go"}`})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "robots.txt"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CopyMetadataFiles(context.Background(), p, target); err == nil {
		t.Fatal("existing output accepted")
	}
	if _, err := os.Stat(filepath.Join(target, "manifest.webmanifest")); !os.IsNotExist(err) {
		t.Fatal("copy partially wrote before collision preflight")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := CopyMetadataFiles(ctx, p, filepath.Join(t.TempDir(), "never")); err == nil {
		t.Fatal("canceled copy succeeded")
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skip(err)
	}
	if err := CopyMetadataFiles(context.Background(), p, link); err == nil {
		t.Fatal("symlink destination accepted")
	}
}
