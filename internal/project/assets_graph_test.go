package project

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/buildassets"
)

func TestAssetGraphProjectToolProcess(t *testing.T) {
	if os.Getenv("TEST_NEXTGO_ASSET_PROJECT_GRAPH") != "1" {
		return
	}
	w, err := buildassets.WorkspaceFromEnv()
	if err != nil {
		os.Exit(1)
	}
	mode := os.Args[len(os.Args)-1]
	source, output := filepath.Join(w.Inputs, "styles", "main.css"), "palette.css"
	if mode == "consumer" {
		source = filepath.Join(w.Dependencies(), "tokens", "palette.css")
		output = "style.css"
	}
	data, err := os.ReadFile(source)
	if err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(filepath.Join(w.Outputs, output), data, 0644); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestAssetGraphProjectStagingAndWatchRecovery(t *testing.T) {
	t.Setenv("GOWORK", "off")
	root := fixture(t, map[string]string{"app/page.go": "package app", "styles/main.css": "body{color:blue}"})
	tool := func(name, mode, output string) buildassets.Tool {
		return buildassets.Tool{Name: name, Command: []string{os.Args[0], "-test.run=^TestAssetGraphProjectToolProcess$", "--", mode}, Outputs: []string{output}, Env: map[string]string{"TEST_NEXTGO_ASSET_PROJECT_GRAPH": "1"}}
	}
	producer := tool("Tokens", "producer", "palette.css")
	producer.Inputs = []string{"styles"}
	producer.Intermediate = true
	consumer := tool("Styles", "consumer", "style.css")
	consumer.Dependencies = map[string]string{"tokens": "Tokens"}
	pipeline := buildassets.Pipeline{Version: 2, Tools: []buildassets.Tool{consumer, producer}}
	manifest := func() {
		t.Helper()
		raw, err := json.Marshal(pipeline)
		if err != nil {
			t.Fatal(err)
		}
		writeInput(t, root, buildassets.ManifestName, string(raw))
	}
	manifest()
	var watcher SourceWatcher
	fingerprint := func() string {
		t.Helper()
		value, err := watcher.Fingerprint(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	initial := fingerprint()
	stage := func(name string) (*buildassets.Result, error) {
		t.Helper()
		dir := filepath.Join(root, ".nextgo", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		return stageAssetPipeline(context.Background(), root, dir, nil)
	}
	first, err := stage("first")
	if err != nil {
		t.Fatal(err)
	}
	if after := fingerprint(); after != initial {
		t.Fatal("private intermediate triggered a rebuild loop")
	}
	for _, name := range []string{"palette.css", "dependencies/tokens/palette.css"} {
		if _, err := os.Stat(filepath.Join(first.Dir, name)); !os.IsNotExist(err) {
			t.Fatal("intermediate in final result", name, err)
		}
	}
	read := func(result *buildassets.Result) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(result.Dir, "style.css"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	if read(first) != "body{color:blue}" {
		t.Fatal("wrong generated output")
	}
	pipeline.Tools[1].Name = "Tokens renamed"
	pipeline.Tools[0].Dependencies["tokens"] = "Tokens renamed"
	manifest()
	graphChange := fingerprint()
	if graphChange == initial {
		t.Fatal("graph-only manifest change was not watched")
	}
	writeInput(t, root, "styles/main.css", "body{color:red}")
	edited := fingerprint()
	if edited == graphChange {
		t.Fatal("producer input edit was not watched")
	}
	if err := os.Remove(filepath.Join(root, "styles", "main.css")); err != nil {
		t.Fatal(err)
	}
	missing := fingerprint()
	if missing == edited {
		t.Fatal("producer input deletion was not watched")
	}
	if _, err := stage("failed"); err == nil {
		t.Fatal("missing producer input published a generation")
	}
	if _, err := os.Stat(filepath.Join(root, ".nextgo", "failed", "asset-pipeline")); !os.IsNotExist(err) {
		t.Fatal("failed asset stage retained", err)
	}
	if read(first) != "body{color:blue}" {
		t.Fatal("failed graph modified last successful assets")
	}
	writeInput(t, root, "styles/main.css", "body{color:red}")
	if fingerprint() != edited {
		t.Fatal("producer recreation did not recover")
	}
	second, err := stage("recovered")
	if err != nil {
		t.Fatal(err)
	}
	if read(second) != "body{color:red}" || generatedAssetIdentity(first) == generatedAssetIdentity(second) {
		t.Fatal("generated identity ignored final dependency bytes")
	}
	public := filepath.Join(root, "published")
	writeInput(t, public, "logo.svg", "<svg/>")
	if err := mergeGeneratedAssets(context.Background(), second, public); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(public)
	if err != nil || len(entries) != 2 {
		t.Fatal("unexpected published tree", entries, err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "palette") || strings.Contains(entry.Name(), "dependenc") {
			t.Fatal("private graph output published", entry.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "public", "style.css")); !os.IsNotExist(err) {
		t.Fatal("source public tree mutated", err)
	}
}
