package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

func TestAnalysisWASMSectionsAccountForEveryByte(t *testing.T) {
	data := []byte{0, 'a', 's', 'm', 1, 0, 0, 0, 0, 4, 2, 'g', 'o', 7, 10, 1, 0, 11, 1, 0}
	sections, err := wasmSections(data)
	if err != nil || len(sections) != 3 {
		t.Fatal(sections, err)
	}
	if sections[0].Name != "custom:go" || sections[1].Name != "code" || sections[2].Name != "data" {
		t.Fatal(sections)
	}
	var size int64 = 8
	for _, section := range sections {
		size += section.PayloadBytes + section.FramingBytes
	}
	if size != int64(len(data)) {
		t.Fatal("WASM accounting omitted or duplicated bytes", size, len(data))
	}
	for _, suffix := range [][]byte{{10}, {10, 128}, {10, 128, 128, 128, 128, 16}, {10, 4, 0}, {14, 0}, {10, 0, 10, 0}, {0, 2, 1, 255}} {
		if _, err := wasmSections(append(append([]byte{}, data[:8]...), suffix...)); err == nil {
			t.Fatalf("accepted malformed section envelope %x", suffix)
		}
	}
}

func TestAnalysisRuntimeAdmissionAndCancellation(t *testing.T) {
	valid := fstest.MapFS{
		"app.wasm":     {Data: []byte{0, 'a', 's', 'm', 1, 0, 0, 0}},
		"boot.js":      {Data: []byte("boot")},
		"wasm_exec.js": {Data: []byte("shim")},
	}
	assets, err := analyzeRuntime(context.Background(), valid)
	if err != nil || len(assets) != 3 || assets[0].Kind != "core" || assets[0].Gzip9Bytes < 1 {
		t.Fatal(assets, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := analyzeRuntime(ctx, valid); !errors.Is(err, context.Canceled) {
		t.Fatal("analysis ignored cancellation", err)
	}
	valid["link"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("app.wasm")}
	if _, err := analyzeRuntime(context.Background(), valid); err == nil {
		t.Fatal("analysis followed a symlink")
	}
	delete(valid, "link")
	valid["pipe"] = &fstest.MapFile{Mode: fs.ModeNamedPipe}
	if _, err := analyzeRuntime(context.Background(), valid); err == nil {
		t.Fatal("analysis opened a nonregular asset")
	}
	delete(valid, "pipe")
	delete(valid, "boot.js")
	if _, err := analyzeRuntime(context.Background(), valid); err == nil {
		t.Fatal("analysis accepted an incomplete runtime")
	}
	root := t.TempDir()
	path := filepath.Join(root, "large")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, (64<<20)+1); err != nil {
		t.Fatal(err)
	}
	if _, err := analyzeRuntime(context.Background(), os.DirFS(root)); err == nil {
		t.Fatal("analysis accepted an oversized runtime file")
	}
}

func TestAnalysisBudgetsAreInclusiveOptionalAndPerLazyFile(t *testing.T) {
	asset := AssetAnalysis{File: "app.wasm", Kind: "core", Bytes: 10, Gzip9Bytes: 20}
	if got := assetViolations(asset, AnalysisBudgets{}); len(got) != 0 {
		t.Fatal("disabled budgets rejected bytes", got)
	}
	if got := assetViolations(asset, AnalysisBudgets{CoreBytes: 10, CoreGzip9Bytes: 20}); len(got) != 0 {
		t.Fatal("equal budget rejected", got)
	}
	if got := assetViolations(asset, AnalysisBudgets{CoreBytes: 9, CoreGzip9Bytes: 19}); len(got) != 2 {
		t.Fatal("missing raw/compressed violations", got)
	}
	asset.Kind = "lazy"
	if got := assetViolations(asset, AnalysisBudgets{CoreBytes: 1, LazyBytes: 9}); len(got) != 1 || got[0].Limit != 9 {
		t.Fatal("lazy budget used core limit", got)
	}
	if (AnalysisBudgets{LazyGzip9Bytes: -1}).Validate() == nil {
		t.Fatal("negative byte budget accepted")
	}
	if _, err := analyzeAsset(context.Background(), "module-incorrect.wasm", []byte{0, 'a', 's', 'm', 1, 0, 0, 0}); err == nil {
		t.Fatal("lazy content digest not verified")
	}
}

func TestAnalysisCompiledArtifactsMatchRegistrationsAndRemainUnchanged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build, err := Compile(ctx, reproducibleProjectFixture(t), false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(build.Dir)
	before := reproducibleBuildAssets(t, build)
	report, err := AnalyzeBuild(ctx, build, AnalysisBudgets{CoreBytes: 1, LazyBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Version != 1 || len(report.Violations) != 3 || len(report.Components) != 3 || len(report.Assets) != 5 || report.Native.Go == "" || report.Native.GOOS == "" {
		t.Fatalf("incomplete real compiled analysis: %+v", report)
	}
	for _, asset := range report.Assets {
		data := before["_nextgo/"+build.ID+"/"+asset.File]
		sum := sha256.Sum256(data)
		if asset.Bytes != int64(len(data)) || asset.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatal("report does not describe the published artifact", asset)
		}
	}
	byFile := map[string]bool{}
	for _, component := range report.Components {
		byFile[component.File] = true
	}
	if len(byFile) != 3 || !byFile["app.wasm"] || len(build.RuntimeModules) != 2 {
		t.Fatal("real lazy registrations lost their immutable assets", report.Components)
	}
	second, err := AnalyzeBuild(ctx, build, report.Budgets)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(report)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatal("unchanged artifact produced a different analysis")
	}
	for path, data := range reproducibleBuildAssets(t, build) {
		if !bytes.Equal(data, before[path]) {
			t.Fatal("analysis changed compiled assets", path)
		}
	}
}

func FuzzAnalysisWASMSections(f *testing.F) {
	f.Add([]byte{0, 'a', 's', 'm', 1, 0, 0, 0})
	f.Add([]byte{0, 'a', 's', 'm', 1, 0, 0, 0, 10, 1, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		sections, err := wasmSections(data)
		if err != nil {
			return
		}
		var count int64 = 8
		for _, section := range sections {
			count += section.PayloadBytes + section.FramingBytes
		}
		if count != int64(len(data)) {
			t.Fatal("WASM section accounting mismatch")
		}
	})
}
