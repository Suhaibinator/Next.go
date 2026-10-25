package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMetadataSourcesUseCompilerCheckedNativeFactories(t *testing.T) {
	p := &Project{MetadataFiles: []MetadataFile{
		{Name: "icon.png", Source: "app/icon.png"},
		{Name: "robots.txt", Import: "example.test/site/app", Factory: "Robots"},
	}}
	var generated strings.Builder
	writeMetadataSources(&generated, p, map[string]string{"example.test/site/app": "application"})
	if !strings.Contains(generated.String(), `fs.Sub(embedded,"assets/metadata")`) || !strings.Contains(generated.String(), "config.MetadataFiles.Robots=application.Robots") {
		t.Fatal(generated.String())
	}
	generated.Reset()
	p.MetadataFiles = p.MetadataFiles[1:]
	writeMetadataSources(&generated, p, map[string]string{"example.test/site/app": "application"})
	if strings.Contains(generated.String(), "fs.Sub") || !strings.Contains(generated.String(), "application.Robots") {
		t.Fatal("producer-only declarations must not require an absent static tree", generated.String())
	}
}

func TestMetadataInputsWatchContentDeletionAndCaseErrors(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	if err := os.Mkdir(app, 0700); err != nil {
		t.Fatal(err)
	}
	plan := inputPlan{Roots: []string{root}, MetadataRoot: app}
	snapshot := func() string {
		t.Helper()
		hash, err := hashInputsContext(context.Background(), plan, "")
		if err != nil {
			t.Fatal(err)
		}
		return hash
	}
	empty := snapshot()
	name := filepath.Join(app, "robots.txt")
	if err := os.WriteFile(name, []byte("User-agent: *\nDisallow: /private\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first := snapshot()
	if first == empty {
		t.Fatal("adding metadata was not observed")
	}
	if err := os.WriteFile(name, []byte("User-agent: *\nDisallow: /other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if snapshot() == first {
		t.Fatal("metadata content change was not observed")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if snapshot() != empty {
		t.Fatal("deleting metadata did not restore input identity")
	}
	if err := os.WriteFile(filepath.Join(app, "ROBOTS.txt"), []byte("invalid filename case"), 0600); err != nil {
		t.Fatal(err)
	}
	if snapshot() == empty {
		t.Fatal("case-invalid metadata must trigger discovery diagnostics and recovery")
	}
}
