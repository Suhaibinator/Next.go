package scaffold

import (
	"context"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/project"
)

func frameworkRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStarterIsAnIndependentFormattedModuleWithRoutesAndComponent(t *testing.T) {
	parent := t.TempDir()
	destination := filepath.Join(parent, "app with spaces")
	if err := Create(context.Background(), Options{Directory: destination, Module: "example.com/starter", Framework: frameworkRoot(t)}, io.Discard); err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(filepath.Join(destination, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(module), "module example.com/starter") || !strings.Contains(string(module), "replace github.com/Suhaibinator/Next.go =>") {
		t.Fatal(string(module))
	}
	discovered, err := project.Discover(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered.Routes) != 4 || len(discovered.Components) != 1 || discovered.NotFound != "example.com/starter/app" {
		t.Fatalf("%+v", discovered)
	}
	err = filepath.WalkDir(destination, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") {
			_, err = parser.ParseFile(token.NewFileSet(), path, nil, parser.AllErrors)
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging artifacts retained: %v %v", entries, err)
	}
}

func TestStarterNeverOverwritesAndCleansRejectedInput(t *testing.T) {
	parent := t.TempDir()
	existing := filepath.Join(parent, "existing")
	if err := os.WriteFile(existing, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	options := Options{Directory: existing, Module: "example.com/starter", Framework: frameworkRoot(t)}
	if err := Create(context.Background(), options, io.Discard); err == nil {
		t.Fatal("existing destination overwritten")
	}
	value, _ := os.ReadFile(existing)
	if string(value) != "keep" {
		t.Fatal("existing file changed")
	}
	for _, module := range []string{"", framework, "bad module path", "bad.example\nreplace anything => /tmp"} {
		destination := filepath.Join(parent, "rejected")
		if err := Create(context.Background(), Options{Directory: destination, Module: module, Framework: frameworkRoot(t)}, io.Discard); err == nil {
			t.Fatalf("accepted invalid module %q", module)
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			t.Fatal("failed initialization published files")
		}
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 {
		t.Fatalf("failed initialization leaked staging: %v", entries)
	}
}
