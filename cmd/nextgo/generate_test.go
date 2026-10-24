package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateCommandCheckAndRefreshWithoutBuilding(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/generated\ngo 1.24\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "app"), 0755); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(root, "app", "page.go")
	if err := os.WriteFile(page, []byte("package app\n// No framework exports: generate must not compile pages.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, args := range [][]string{{"generate"}, {"generate", "-unknown", root}, {"generate", "-check", root}} {
		if err := execute(ctx, args); err == nil {
			t.Fatal("accepted", args)
		}
	}
	if err := execute(ctx, []string{"generate", root}); err != nil {
		t.Fatal(err)
	}
	if err := execute(ctx, []string{"generate", "-check", root}); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "app", "about"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "about", "page.go"), []byte("package about"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := execute(ctx, []string{"generate", "-check", root}); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatal("missing drift diagnostic", err)
	}
	if err := execute(ctx, []string{"generate", root}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".nextgo")); !os.IsNotExist(err) {
		t.Fatal("generation started a build", err)
	}
}
