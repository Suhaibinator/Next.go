package project

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalysisExistingRuntimeRetainsFilesAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{"app.wasm": {0, 'a', 's', 'm', 1, 0, 0, 0}, "boot.js": []byte("do not execute"), "wasm_exec.js": []byte("not a program")}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0400); err != nil {
			t.Fatal(err)
		}
	}
	report, err := AnalyzeRuntimeArtifacts(context.Background(), dir, AnalysisBudgets{CoreBytes: 1})
	if err != nil || len(report.Assets) != 3 || len(report.Violations) != 1 || report.Totals.AllBytes != 35 {
		t.Fatal(report, err)
	}
	for _, asset := range report.Assets {
		sum := sha256.Sum256(files[asset.File])
		if asset.SHA256 != hex.EncodeToString(sum[:]) {
			t.Fatal("report does not identify measured bytes", asset)
		}
		data, err := os.ReadFile(filepath.Join(dir, asset.File))
		if err != nil || !bytes.Equal(data, files[asset.File]) {
			t.Fatal("measurement changed caller-owned files", err)
		}
	}
	encoded, err := json.Marshal(report)
	if err != nil || bytes.Contains(encoded, []byte(dir)) || bytes.Contains(encoded, []byte(`"native"`)) || bytes.Contains(encoded, []byte(`"fingerprint"`)) {
		t.Fatal("runtime-only report leaked paths or inferred provenance", string(encoded), err)
	}
	link := filepath.Join(t.TempDir(), "namespace-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeRuntimeArtifacts(context.Background(), link, AnalysisBudgets{}); err == nil {
		t.Fatal("accepted symlink namespace")
	}
	if err := os.Symlink(filepath.Join(dir, "app.wasm"), filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := AnalyzeRuntimeArtifacts(context.Background(), dir, AnalysisBudgets{}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatal("followed a namespace symlink", err)
	}
}

func TestAnalysisExistingRuntimeCancellationPrecedesFilesystemAccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := AnalyzeRuntimeArtifacts(ctx, "absent", AnalysisBudgets{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
