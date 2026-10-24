package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublishBinaryReplacesAtomicallyWithoutFollowingTemporarySymlinks(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "server")
	victim := filepath.Join(dir, "unrelated")
	if err := os.WriteFile(victim, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, binary+".tmp"); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"first", "replacement"} {
		if err := publishBinary(binary, []byte(content)); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(binary)
		if err != nil || string(got) != content {
			t.Fatalf("binary contents %q: %v", got, err)
		}
	}
	got, _ := os.ReadFile(victim)
	if string(got) != "keep" {
		t.Fatal("publication followed a pre-existing temporary symlink")
	}
	info, err := os.Stat(binary)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("binary must be executable: %v, %v", info, err)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".nextgo-server-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary files leaked: %v, %v", matches, err)
	}
}

func TestPublishBinaryCleansFailedPublication(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "server")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := publishBinary(target, []byte("cannot replace a directory")); err == nil {
		t.Fatal("expected publication failure")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != "server" {
		t.Fatalf("temporary file leaked after failure: %v, %v", files, err)
	}
}
