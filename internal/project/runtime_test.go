package project

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRuntimeIdentityIncludesEveryPublishedAsset(t *testing.T) {
	original := runtimeBuildID([]byte("wasm"), "source", []byte("support"), "loader")
	variants := []string{
		runtimeBuildID([]byte("wasm changed"), "source", []byte("support"), "loader"),
		runtimeBuildID([]byte("wasm"), "source changed", []byte("support"), "loader"),
		runtimeBuildID([]byte("wasm"), "source", []byte("support changed"), "loader"),
		runtimeBuildID([]byte("wasm"), "source", []byte("support"), "loader changed"),
	}
	if len(original) != 20 {
		t.Fatal("unexpected runtime identity length")
	}
	for _, variant := range variants {
		if variant == original {
			t.Fatal("published artifact changed without a new immutable URL")
		}
	}
	if original != runtimeBuildID([]byte("wasm"), "source", []byte("support"), "loader") {
		t.Fatal("nondeterministic identity")
	}
	if runtimeBuildID([]byte("ab"), "c", nil, "") == runtimeBuildID([]byte("a"), "bc", nil, "") {
		t.Fatal("tuple boundaries are ambiguous")
	}
}

func TestRuntimeSupportUsesApplicationToolchainContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("toolchain fixture uses a POSIX shell")
	}
	tools := t.TempDir()
	root := t.TempDir()
	app := t.TempDir()
	record := filepath.Join(t.TempDir(), "pwd")
	support := filepath.Join(root, "lib", "wasm", "wasm_exec.js")
	if err := os.MkdirAll(filepath.Dir(support), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(support, []byte("selected runtime support"), 0644); err != nil {
		t.Fatal(err)
	}
	source := `#!/bin/sh
printf '%s\n' "$PWD" > "$NEXTGO_GOROOT_RECORD"
printf '%s\n' "$NEXTGO_GOROOT_FIXTURE"
`
	if err := os.WriteFile(filepath.Join(tools, "go"), []byte(source), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NEXTGO_GOROOT_RECORD", record)
	t.Setenv("NEXTGO_GOROOT_FIXTURE", root)
	data, err := wasmSupport(context.Background(), app)
	if err != nil || string(data) != "selected runtime support" {
		t.Fatalf("support: %s %v", data, err)
	}
	cwd, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	// Physical temp paths may differ from their /var symlink spelling on macOS.
	expected, _ := filepath.EvalSymlinks(app)
	actual, _ := filepath.EvalSymlinks(string(cwd[:len(cwd)-1]))
	if actual != expected {
		t.Fatalf("resolved toolchain in %q instead of %q", actual, expected)
	}
}
