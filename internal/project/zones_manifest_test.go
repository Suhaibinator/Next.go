package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestZonesManifestStrictBindingsAndPaths(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "zones.json")
	valid := `{"version":1,"zones":[{"id":"root","host":"EXAMPLE.test","basePath":"","app":"./site"},{"id":"blog","host":"example.test","basePath":"/blog","tenant":"tenant-1","app":"./site"}]}`
	write := func(data string) {
		t.Helper()
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(valid)
	got, err := DiscoverZones(name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Zones[0].Host != "example.test" || got.Zones[0].Root != filepath.Join(got.Root, "site") || got.Zones[1].Root != got.Zones[0].Root {
		t.Fatal(got)
	}
	for label, data := range map[string]string{
		"version":       strings.Replace(valid, `"version":1`, `"version":2`, 1),
		"unknown":       strings.Replace(valid, `"version":1`, `"version":1,"extra":true`, 1),
		"duplicate-key": strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1),
		"case-id":       strings.Replace(valid, `"id":"blog"`, `"id":"ROOT"`, 1),
		"binding":       strings.Replace(valid, `"basePath":"/blog"`, `"basePath":""`, 1),
		"host":          strings.Replace(valid, `"EXAMPLE.test"`, `"https://example.test"`, 1),
		"base":          strings.Replace(valid, `"basePath":"/blog"`, `"basePath":"/blog/"`, 1),
		"escape":        strings.ReplaceAll(valid, `"./site"`, `"../site"`),
		"generated":     strings.ReplaceAll(valid, `"./site"`, `".nextgo/site"`),
		"null":          `null`, "empty": `{"version":1,"zones":[]}`,
	} {
		t.Run(label, func(t *testing.T) {
			write(data)
			if _, err := DiscoverZones(name); err == nil {
				t.Fatal("accepted invalid manifest", data)
			}
		})
	}
	write(valid)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "site")); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverZones(name); err == nil {
		t.Fatal("accepted escaping app symlink")
	}
	write(strings.ReplaceAll(valid, `"./site"`, `"./site/not-created"`))
	if _, err := DiscoverZones(name); err == nil {
		t.Fatal("accepted escaping symlink before missing app directory")
	}
	if err := os.Remove(filepath.Join(root, "site")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "other.json"), name); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverZones(name); err == nil {
		t.Fatal("accepted manifest symlink")
	}
}

func TestZonesWatcherRetainsRootsDuringManifestRecovery(t *testing.T) {
	root := fixture(t, map[string]string{"site/app/page.go": "package app"})
	name := filepath.Join(root, "zones.json")
	valid := `{"version":1,"zones":[{"id":"site","host":"example.test","basePath":"","app":"site"}]}`
	writeInput(t, root, "zones.json", valid)
	var watcher ZonesWatcher
	read := func() (string, error) { return watcher.Fingerprint(context.Background(), name) }
	initial, err := read()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := ZonesFingerprint(context.Background(), name)
	if err != nil || fresh != initial {
		t.Fatal("fresh watcher differs", err)
	}
	writeInput(t, root, "zones.json", "{")
	broken, err := read()
	if err == nil || broken == initial {
		t.Fatal("invalid manifest not observed", err)
	}
	writeInput(t, root, "site/app/page.go", "package app\n// changed while manifest invalid")
	changed, err := read()
	if err == nil || changed == broken {
		t.Fatal("lost previous root while invalid", err)
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	missing, err := read()
	if err == nil || missing == changed {
		t.Fatal("manifest deletion not observed", err)
	}
	writeInput(t, root, "zones.json", valid)
	recovered, err := read()
	if err != nil || recovered == initial {
		t.Fatal("recovery ignored source edit", err)
	}
	if err := os.Remove(filepath.Join(root, "site", "app", "page.go")); err != nil {
		t.Fatal(err)
	}
	deleted, _ := read()
	if deleted == recovered {
		t.Fatal("source deletion not observed")
	}
	writeInput(t, root, "site/app/page.go", "package app\n// changed while manifest invalid")
	recreated, err := read()
	if err != nil || recreated != recovered {
		t.Fatal("source recreation did not recover", err)
	}
}

func TestZonesManifestBoundedDeclarations(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "zones.json")
	declarations := make([]string, 65)
	for i := range declarations {
		declarations[i] = fmt.Sprintf(`{"id":"z%d","host":"z%d.test","app":"site"}`, i, i)
	}
	writeInput(t, root, "zones.json", `{"version":1,"zones":[`+strings.Join(declarations, ",")+`]}`)
	if _, err := DiscoverZones(name); err == nil {
		t.Fatal("accepted more than64 zones")
	}
	writeInput(t, root, "zones.json", strings.Repeat(" ", maxZonesManifestBytes+1))
	if _, err := DiscoverZones(name); err == nil {
		t.Fatal("accepted oversized manifest")
	}
}
