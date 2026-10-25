//go:build !js

package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/buildassets"
)

// These explicitly named integration tests execute the example's actual Go
// adapters. They require the separate native/CLI qualification lease.
func copyCSSPipelineFixture(t *testing.T) string {
	t.Helper()
	example, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	framework, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, name := range []string{"nextgo.assets.json", "styles/main.css", "styles/tokens.json", "tools/tokens/main.go", "tools/styles/main.go"} {
		data, err := os.ReadFile(filepath.Join(example, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	module := fmt.Sprintf("module csspipeline.fixture\n\ngo 1.24\n\nrequire github.com/Suhaibinator/Next.go v0.0.0\nreplace github.com/Suhaibinator/Next.go => %s\n", strconv.Quote(framework))
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func runCSSPipelineFixture(t *testing.T, root, name string) (*buildassets.Result, string, error) {
	t.Helper()
	pipeline, present, err := buildassets.Load(root)
	if err != nil || !present {
		t.Fatal("example manifest did not load", err)
	}
	if pipeline.Version != 2 {
		t.Fatal("example must exercise the declared dependency pipeline")
	}
	stage := filepath.Join(root, ".nextgo", name)
	if err := os.MkdirAll(filepath.Dir(stage), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var log bytes.Buffer
	result, err := buildassets.Run(ctx, pipeline, buildassets.Options{Root: root, Stage: stage, Log: &log})
	return result, log.String(), err
}

func assertCSSPipelinePublicOutput(t *testing.T, root string) []byte {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "style.css" || !entries[0].Type().IsRegular() {
		t.Fatal("pipeline must publish only style.css", entries, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "style.css"))
	if err != nil || bytes.Contains(data, []byte("{{")) {
		t.Fatal("invalid final stylesheet", err)
	}
	return data
}

func TestCSSPipelineDependencyChainGoAdapters(t *testing.T) {
	root := copyCSSPipelineFixture(t)
	result, log, err := runCSSPipelineFixture(t, root, "good")
	if err != nil {
		t.Fatal(err, log)
	}
	css := assertCSSPipelinePublicOutput(t, result.Dir)
	template, err := os.ReadFile(filepath.Join(root, "styles", "main.css"))
	if err != nil {
		t.Fatal(err)
	}
	want := "/* Generated from Go design tokens. */\n" + strings.NewReplacer("{{accent}}", "#c7512b", "{{background}}", "#fbf6ed", "{{ink}}", "#292621").Replace(string(template))
	if string(css) != want {
		t.Fatal("v2 pipeline changed the example's final stylesheet")
	}
	published := t.TempDir()
	if err := buildassets.Merge(context.Background(), result.Dir, published); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(assertCSSPipelinePublicOutput(t, published), css) {
		t.Fatal("publication changed CSS")
	}
	if _, err := os.Stat(filepath.Join(root, "public", "style.css")); !os.IsNotExist(err) {
		t.Fatal("pipeline mutated source public", err)
	}
}

func TestCSSPipelineBadTokensKeepLastGoodGoAdapters(t *testing.T) {
	root := copyCSSPipelineFixture(t)
	first, log, err := runCSSPipelineFixture(t, root, "first")
	if err != nil {
		t.Fatal(err, log)
	}
	oldCSS := assertCSSPipelinePublicOutput(t, first.Dir)
	if err := os.WriteFile(filepath.Join(root, "styles", "tokens.json"), []byte(`{"accent":"not-a-color","background":"#fbf6ed","ink":"#292621"}`), 0644); err != nil {
		t.Fatal(err)
	}
	failed, _, err := runCSSPipelineFixture(t, root, "failed")
	if err == nil || failed != nil {
		t.Fatal("invalid tokens published a generation", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".nextgo", "failed")); !os.IsNotExist(err) {
		t.Fatal("failed intermediate stage retained", err)
	}
	if !bytes.Equal(assertCSSPipelinePublicOutput(t, first.Dir), oldCSS) {
		t.Fatal("failed token normalization changed the last good generation")
	}
	if err := os.WriteFile(filepath.Join(root, "styles", "tokens.json"), []byte(`{"accent":"#336699","background":"#fbf6ed","ink":"#292621"}`), 0644); err != nil {
		t.Fatal(err)
	}
	recovered, log, err := runCSSPipelineFixture(t, root, "recovered")
	if err != nil {
		t.Fatal(err, log)
	}
	css := assertCSSPipelinePublicOutput(t, recovered.Dir)
	if !bytes.Contains(css, []byte("--accent: #336699")) || recovered.Digest == first.Digest {
		t.Fatal("token recovery did not produce new final CSS")
	}
	if !bytes.Equal(assertCSSPipelinePublicOutput(t, first.Dir), oldCSS) {
		t.Fatal("recovery modified the previous successful generation")
	}
}
