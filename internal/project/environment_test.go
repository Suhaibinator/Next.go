package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPublicEnvironmentFilesRejected(t *testing.T) {
	for _, name := range []string{".env", ".env.local", ".env.example", ".ENV", ".Env.Production"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, name), []byte("PRIVATE=synthetic"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := copyTree(root, t.TempDir()); err == nil {
				t.Fatal("published environment file")
			}
		})
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".well-known", "example"), []byte("safe"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(root, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateEnvironmentExcludedFromBuildFingerprint(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app"})
	before, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"first-secret", "changed-secret"} {
		if err := os.WriteFile(filepath.Join(root, ".env.development.local"), []byte("PRIVATE="+value), 0600); err != nil {
			t.Fatal(err)
		}
		after, err := Fingerprint(root)
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatal("private configuration became a public build identifier")
		}
	}
}
